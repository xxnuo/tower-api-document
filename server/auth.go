package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

const sessionCookieName = "agents_tower_session"

type authUserCtxKey struct{}

func userFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(authUserCtxKey{}).(*User)
	return u
}

func extractSessionToken(req *http.Request) string {
	if c, err := req.Cookie(sessionCookieName); err == nil && strings.TrimSpace(c.Value) != "" {
		return strings.TrimSpace(c.Value)
	}
	if h := strings.TrimSpace(req.Header.Get("X-Session-Token")); h != "" {
		return h
	}
	auth := strings.TrimSpace(req.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		tok := strings.TrimSpace(auth[7:])
		if strings.HasPrefix(tok, "at_sess-") {
			return tok
		}
	}
	return ""
}

func setSessionCookie(writer http.ResponseWriter, token string) {
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func currentSessionUser(cfg Config, req *http.Request) (*User, string) {
	if cfg.Store == nil {
		return nil, ""
	}
	tok := extractSessionToken(req)
	if tok == "" {
		return nil, ""
	}
	u, err := cfg.Store.GetUserBySession(tok)
	if err != nil {
		return nil, ""
	}
	return u, tok
}

func requireAuth(cfg Config, next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		u, _ := currentSessionUser(cfg, req)
		if u == nil {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": "请先登录 AgentsTower 账户"})
			return
		}
		ctx := context.WithValue(req.Context(), authUserCtxKey{}, u)
		next(writer, req.WithContext(ctx))
	}
}

func requireAdmin(cfg Config, next http.HandlerFunc) http.HandlerFunc {
	return requireAuth(cfg, func(writer http.ResponseWriter, req *http.Request) {
		u := userFromContext(req.Context())
		if u == nil || u.Role != RoleAdmin {
			writeJSON(writer, http.StatusForbidden, map[string]any{"error": "无权限：仅系统管理员可执行该操作"})
			return
		}
		next(writer, req)
	})
}

type boundAccount struct {
	AccountID    string    `json:"account_id"`
	User         *UserInfo `json:"user"`
	MCPAPIKey    string    `json:"mcp_api_key"`
	WebConnected bool      `json:"web_connected"`
}

func userLoginResults(cfg Config, userID int64) []boundAccount {
	if cfg.Store == nil {
		return []boundAccount{}
	}
	stored, err := cfg.Store.ListAccountsByUser(userID)
	if err != nil {
		return []boundAccount{}
	}
	out := make([]boundAccount, 0, len(stored))
	for i := range stored {
		acc := stored[i]
		item := boundAccount{AccountID: acc.ID, User: acc.ToLoginResult().User, MCPAPIKey: acc.MCPAPIKey}
		if _, sessErr := cfg.Store.GetWebSession(stored[i].ID); sessErr == nil {
			item.WebConnected = true
		}
		out = append(out, item)
	}
	return out
}

func handleAuthMe(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		hasUsers, _ := cfg.Store.HasUsers()
		settings := cfg.Store.GetSettings()
		u, tok := currentSessionUser(cfg, req)
		if u == nil {
			writeJSON(writer, http.StatusOK, map[string]any{
				"authenticated":     false,
				"has_users":         hasUsers,
				"registration_mode": settings.RegistrationMode,
				"allow_mcp_delete":  settings.AllowMCPDelete,
			})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"authenticated":     true,
			"has_users":         hasUsers,
			"registration_mode": settings.RegistrationMode,
			"allow_mcp_delete":  settings.AllowMCPDelete,
			"user":              u,
			"session_token":     tok,
			"accounts":          userLoginResults(cfg, u.ID),
		})
	}
}

func handleRegister(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			Username    string `json:"username"`
			Password    string `json:"password"`
			DisplayName string `json:"display_name"`
			InviteCode  string `json:"invite_code"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		hasUsers, _ := cfg.Store.HasUsers()
		if hasUsers {
			settings := cfg.Store.GetSettings()
			switch settings.RegistrationMode {
			case RegModeClosed:
				writeJSON(writer, http.StatusForbidden, map[string]any{"error": "管理员已关闭公开注册，请联系管理员开通账号"})
				return
			case RegModeInvite:
				if settings.InviteCode == "" || strings.TrimSpace(payload.InviteCode) != settings.InviteCode {
					writeJSON(writer, http.StatusForbidden, map[string]any{"error": "邀请码错误或缺失，请联系管理员获取邀请码"})
					return
				}
			}
		}
		uname := strings.TrimSpace(payload.Username)
		if len(uname) < 2 || len(uname) > 64 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "用户名长度需在 2 到 64 个字符之间"})
			return
		}
		if len(payload.Password) < 6 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "密码长度至少需要 6 位"})
			return
		}
		u, err := cfg.Store.CreateUser(uname, payload.Password, payload.DisplayName)
		if errors.Is(err, ErrUserExists) {
			writeJSON(writer, http.StatusConflict, map[string]any{"error": "该用户名已被注册"})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "创建用户失败: " + err.Error()})
			return
		}
		sessTok, err := cfg.Store.CreateSession(u.ID)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "创建会话失败"})
			return
		}
		setSessionCookie(writer, sessTok)
		writeJSON(writer, http.StatusOK, map[string]any{
			"user":          u,
			"session_token": sessTok,
			"accounts":      userLoginResults(cfg, u.ID),
		})
	}
}

const (
	loginRateLimitMax    = 8
	loginRateLimitWindow = 5 * time.Minute
)

type loginFailBucket struct {
	count   int
	resetAt time.Time
}

func clientIPFromReq(req *http.Request) string {
	if xff := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if trimmed := strings.TrimSpace(first); trimmed != "" {
			return trimmed
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(req.RemoteAddr))
	if err == nil && host != "" {
		return host
	}
	return strings.TrimSpace(req.RemoteAddr)
}

func handleAuthLogin(cfg Config) http.HandlerFunc {
	var mu sync.Mutex
	buckets := map[string]loginFailBucket{}

	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		bucketKey := clientIPFromReq(req) + "|" + strings.ToLower(strings.TrimSpace(payload.Username))
		now := time.Now()
		mu.Lock()
		for k, b := range buckets {
			if now.After(b.resetAt) {
				delete(buckets, k)
			}
		}
		if b, ok := buckets[bucketKey]; ok && now.Before(b.resetAt) && b.count >= loginRateLimitMax {
			mu.Unlock()
			writeJSON(writer, http.StatusTooManyRequests, map[string]any{"error": "登录失败次数过多，请 5 分钟后再试"})
			return
		}
		mu.Unlock()

		u, err := cfg.Store.AuthenticateUser(payload.Username, payload.Password)
		if errors.Is(err, ErrInvalidCreds) {
			mu.Lock()
			b := buckets[bucketKey]
			if now.After(b.resetAt) {
				b = loginFailBucket{count: 0, resetAt: now.Add(loginRateLimitWindow)}
			}
			b.count++
			buckets[bucketKey] = b
			mu.Unlock()
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": "用户名或密码错误"})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "登录验证失败"})
			return
		}
		mu.Lock()
		delete(buckets, bucketKey)
		mu.Unlock()

		sessTok, err := cfg.Store.CreateSession(u.ID)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "创建会话失败"})
			return
		}
		setSessionCookie(writer, sessTok)
		writeJSON(writer, http.StatusOK, map[string]any{
			"user":          u,
			"session_token": sessTok,
			"accounts":      userLoginResults(cfg, u.ID),
		})
	}
}

func handleQuickLogin(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			SessionToken string `json:"session_token"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		tok := strings.TrimSpace(payload.SessionToken)
		u, err := cfg.Store.GetUserBySession(tok)
		if err != nil {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": "保存的快捷登录会话已失效，请重新输入密码登录"})
			return
		}
		setSessionCookie(writer, tok)
		writeJSON(writer, http.StatusOK, map[string]any{
			"user":          u,
			"session_token": tok,
			"accounts":      userLoginResults(cfg, u.ID),
		})
	}
}

func handleAuthLogout(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			SessionToken string `json:"session_token"`
			Revoke       bool   `json:"revoke"`
		}
		_ = json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload)
		cookieTok := extractSessionToken(req)
		clearSessionCookie(writer)
		if payload.Revoke {
			target := strings.TrimSpace(payload.SessionToken)
			if target == "" {
				target = cookieTok
			}
			if target != "" && cfg.Store != nil {
				_ = cfg.Store.DeleteSession(target)
			}
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleChangePassword(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		u := userFromContext(req.Context())
		if u == nil {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": "请先登录"})
			return
		}
		var payload struct {
			CurrentPassword string `json:"current_password"`
			NewPassword     string `json:"new_password"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		if len(payload.NewPassword) < 6 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "新密码长度至少需要 6 位"})
			return
		}
		if err := cfg.Store.ChangeUserPassword(u.ID, payload.CurrentPassword, payload.NewPassword); err != nil {
			if errors.Is(err, ErrInvalidCreds) {
				writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": "当前密码错误"})
				return
			}
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "修改密码失败: " + err.Error()})
			return
		}
		sessTok, err := cfg.Store.CreateSession(u.ID)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "创建新会话失败"})
			return
		}
		setSessionCookie(writer, sessTok)
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "session_token": sessTok})
	}
}

func handleLogoutAll(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		u := userFromContext(req.Context())
		if u != nil && cfg.Store != nil {
			_ = cfg.Store.DeleteUserSessions(u.ID)
		}
		clearSessionCookie(writer)
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleAdminGetSettings(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{"settings": cfg.Store.GetSettings()})
	}
}

func handleAdminUpdateSettings(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			RegistrationMode string  `json:"registration_mode"`
			InviteCode       *string `json:"invite_code"`
			AllowMCPDelete   *bool   `json:"allow_mcp_delete"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		updated, err := cfg.Store.UpdateSettings(strings.TrimSpace(payload.RegistrationMode), payload.InviteCode, payload.AllowMCPDelete)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"settings": updated})
	}
}

func handleAdminListUsers(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		users, err := cfg.Store.ListUsers()
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"users": users})
	}
}

func handleAdminSetRole(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		targetID, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
		if err != nil || targetID <= 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效用户 ID"})
			return
		}
		var payload struct {
			Role string `json:"role"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		updated, err := cfg.Store.SetUserRole(targetID, strings.TrimSpace(payload.Role))
		if errors.Is(err, ErrLastAdmin) {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "不能取消唯一系统管理员的权限"})
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "用户不存在"})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"user": updated})
	}
}

func handleAdminResetPassword(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		targetID, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
		if err != nil || targetID <= 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效用户 ID"})
			return
		}
		var payload struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		if len(payload.Password) < 6 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "新密码长度至少需要 6 位"})
			return
		}
		if err := cfg.Store.SetUserPassword(targetID, payload.Password); err != nil {
			if errors.Is(err, ErrUserNotFound) {
				writeJSON(writer, http.StatusNotFound, map[string]any{"error": "用户不存在"})
				return
			}
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"ok": true})
	}
}

func handleAdminDeleteUser(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		admin := userFromContext(req.Context())
		targetID, err := strconv.ParseInt(req.PathValue("id"), 10, 64)
		if err != nil || targetID <= 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效用户 ID"})
			return
		}
		if admin != nil && admin.ID == targetID {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "不能删除当前正在登录的管理员自身"})
			return
		}
		err = cfg.Store.DeleteUser(targetID)
		if errors.Is(err, ErrLastAdmin) {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "不能删除唯一的系统管理员"})
			return
		}
		if errors.Is(err, ErrUserNotFound) {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "用户不存在"})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"deleted": true})
	}
}

func printCLIUsage(out io.Writer) {
	fmt.Fprintln(out, `AgentsTower 命令行工具用法:
  agents-tower                                      启动 HTTP 与 MCP 服务
  agents-tower user list                            列出所有注册用户及角色
  agents-tower admin grant <username>               将指定用户授权为管理员 (admin)
  agents-tower admin revoke <username>              取消指定用户的管理员权限 (设为 user)
  agents-tower user reset-password <user> <passwd>  重置指定用户的登录密码`)
}

func runCLI(args []string, dbPath string, out io.Writer) error {
	if len(args) == 0 {
		return nil
	}
	cmd := args[0]
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		printCLIUsage(out)
		return nil
	}

	store, err := OpenStore(dbPath)
	if err != nil {
		return fmt.Errorf("打开数据库 (%s) 失败: %w", dbPath, err)
	}
	defer store.Close()

	switch cmd {
	case "admin":
		if len(args) < 3 {
			return errors.New("用法: agents-tower admin <grant|revoke> <username>")
		}
		action, username := args[1], args[2]
		var targetRole string
		switch action {
		case "grant":
			targetRole = RoleAdmin
		case "revoke":
			targetRole = RoleUser
		default:
			return fmt.Errorf("未知子命令 %q，仅支持 grant 或 revoke", action)
		}
		u, err := store.SetUserRoleByUsername(username, targetRole)
		if errors.Is(err, ErrUserNotFound) {
			return fmt.Errorf("未找到用户 %q", username)
		}
		if errors.Is(err, ErrLastAdmin) {
			return errors.New("操作拒绝：不能取消唯一管理员的权限")
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已更新用户 %q (ID=%d) 角色为: %s\n", u.Username, u.ID, u.Role)
		return nil

	case "user":
		if len(args) < 2 {
			return errors.New("用法: agents-tower user <list|reset-password>")
		}
		switch args[1] {
		case "list":
			users, err := store.ListUsers()
			if err != nil {
				return err
			}
			if len(users) == 0 {
				fmt.Fprintln(out, "当前暂无注册用户（首个在网页注册的用户将自动成为管理员）")
				return nil
			}
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tUSERNAME\tDISPLAY_NAME\tROLE\tTOWER_ACCOUNTS\tCREATED_AT")
			for _, item := range users {
				created := time.Unix(item.CreatedAt, 0).Format("2006-01-02 15:04:05")
				fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d\t%s\n",
					item.ID, item.Username, item.DisplayName, item.Role, item.TowerCount, created)
			}
			return tw.Flush()
		case "reset-password":
			if len(args) < 4 {
				return errors.New("用法: agents-tower user reset-password <username> <new-password>")
			}
			if len(args[3]) < 6 {
				return errors.New("新密码长度至少需要 6 位")
			}
			u, err := store.GetUserByUsername(args[2])
			if errors.Is(err, ErrUserNotFound) {
				return fmt.Errorf("未找到用户 %q", args[2])
			}
			if err != nil {
				return err
			}
			if err := store.SetUserPassword(u.ID, args[3]); err != nil {
				return err
			}
			fmt.Fprintf(out, "已重置用户 %q (ID=%d) 的密码\n", u.Username, u.ID)
			return nil
		default:
			return fmt.Errorf("未知 user 子命令 %q", args[1])
		}

	default:
		printCLIUsage(out)
		return fmt.Errorf("未知命令: %s", cmd)
	}
}
