package main

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpLogEntry struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	TowerAccount string `json:"tower_account"`
	Tool         string `json:"tool"`
	StartedAt    int64  `json:"started_at"`
	DurationMS   int64  `json:"duration_ms"`
	Success      bool   `json:"success"`
}

func (s *Store) mcpLogOptIn(userID int64) (int64, error) {
	var id int64
	err := s.db.QueryRow("SELECT id FROM mcp_log_opt_ins WHERE user_id = ?", userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

func (s *Store) auditMCPCall(acc *StoredAccount, tool string, next func() (mcp.Result, error)) (mcp.Result, error) {
	optIn, err := s.mcpLogOptIn(acc.UserID)
	if err != nil {
		log.Printf("read MCP logging preference: %v", err)
	}
	if optIn == 0 || err != nil {
		return next()
	}
	start := time.Now()
	result, callErr := next()
	call, ok := result.(*mcp.CallToolResult)
	success := callErr == nil && ok && call != nil && !call.IsError
	// Match the original opt-in so disabling recording also excludes calls still in flight.
	_, err = s.db.Exec(`INSERT INTO mcp_operation_logs(user_id, username, tower_account, tool, started_at, duration_ms, success)
		SELECT u.id, u.username, ?, ?, ?, ?, ? FROM users u JOIN mcp_log_opt_ins o ON o.user_id = u.id WHERE o.id = ?`,
		cmp.Or(acc.Email, acc.TowerID, acc.ID), tool, start.UnixMilli(), time.Since(start).Milliseconds(), success, optIn)
	if err != nil {
		log.Printf("write MCP operation log: %v", err)
	}
	return result, callErr
}

func handleMCPLogSetting(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil || body.Enabled == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请指定是否开启日志记录"})
			return
		}
		stmt := "DELETE FROM mcp_log_opt_ins WHERE user_id = ?"
		if *body.Enabled {
			stmt = "INSERT INTO mcp_log_opt_ins(user_id) VALUES (?) ON CONFLICT(user_id) DO NOTHING"
		}
		if _, err := cfg.Store.db.ExecContext(r.Context(), stmt, userFromContext(r.Context()).ID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存日志设置失败"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": *body.Enabled})
	}
}

const mcpLogPageSize = 50

func handleMCPLogs(cfg Config, allUsers bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		userID := userFromContext(r.Context()).ID
		optIn, err := cfg.Store.mcpLogOptIn(userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取日志设置失败"})
			return
		}
		query := "SELECT id, username, tower_account, tool, started_at, duration_ms, success FROM mcp_operation_logs WHERE 1=1"
		var args []any
		if !allUsers {
			query += " AND user_id = ?"
			args = append(args, userID)
		}
		if raw := r.URL.Query().Get("before"); raw != "" {
			before, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || before <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的分页游标"})
				return
			}
			query += " AND id < ?"
			args = append(args, before)
		}
		rows, err := cfg.Store.db.QueryContext(r.Context(), query+" ORDER BY id DESC LIMIT ?", append(args, mcpLogPageSize+1)...)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取操作日志失败"})
			return
		}
		defer rows.Close()
		entries := []mcpLogEntry{}
		for rows.Next() {
			var e mcpLogEntry
			if err := rows.Scan(&e.ID, &e.Username, &e.TowerAccount, &e.Tool, &e.StartedAt, &e.DurationMS, &e.Success); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取操作日志失败"})
				return
			}
			entries = append(entries, e)
		}
		if rows.Err() != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取操作日志失败"})
			return
		}
		hasMore := len(entries) > mcpLogPageSize
		if hasMore {
			entries = entries[:mcpLogPageSize]
		}
		writeJSON(w, http.StatusOK, map[string]any{"enabled": optIn != 0, "logs": entries, "has_more": hasMore})
	}
}
