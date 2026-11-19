package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMCPLogConsentAndPersonalAccess(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/teams" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer tower.Close()

	path := filepath.Join(t.TempDir(), "audit.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	cfg := testConfig(tower.URL)
	cfg.Store = store
	mux := newMux(cfg)
	admin, adminUser, _ := registerTestUser(t, mux, "admin", "testpass")
	member, memberUser, _ := registerTestUser(t, mux, "member", "testpass")
	bind := func(u User) *StoredAccount {
		t.Helper()
		acc, err := store.SaveLoginForUser(u.ID, &UserInfo{ID: u.Username}, TokenResponse{AccessToken: "secret-" + u.Username})
		if err != nil {
			t.Fatal(err)
		}
		return acc
	}
	memberAccount, adminAccount := bind(memberUser), bind(adminUser)
	request := func(method, path, body string, cookie *http.Cookie, key string, status int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if key != "" {
			r.Header.Set("AGENTS_TOWER_API_KEY", key)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s returned %d, want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		return w.Body.String()
	}
	const own = "/api/mcp-logs"
	const audit = "/api/admin/mcp-logs"
	type logPage struct {
		Enabled bool
		Logs    []mcpLogEntry
		HasMore bool `json:"has_more"`
	}
	list := func(cookie *http.Cookie, path string) logPage {
		t.Helper()
		body := request("GET", path, "", cookie, "", 200)
		var p logPage
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(body, "secret-") || strings.Contains(body, "at_sk-") {
			t.Fatal("sensitive value in log")
		}
		return p
	}
	call := func(acc *StoredAccount, name, args string) string {
		return request("POST", "/mcp", fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, name, args), nil, acc.MCPAPIKey, 200)
	}
	set := func(cookie *http.Cookie, enabled bool) {
		request("PUT", own+"/settings", fmt.Sprintf(`{"enabled":%t}`, enabled), cookie, "", 200)
	}
	request("GET", own, "", nil, "", 401)
	request("PUT", own+"/settings", `{"enabled":true}`, nil, "", 401)
	request("GET", audit, "", member, "", 404)
	request("GET", audit, "", admin, "", 404)
	request("PUT", own+"/settings", `{}`, member, "", 400)
	call(memberAccount, "list_teams", "{}")
	if p := list(member, own); p.Enabled || len(p.Logs) != 0 {
		t.Fatalf("default consent: %+v", p)
	}
	set(member, true)
	set(member, true)
	call(memberAccount, "list_teams", "{}")
	call(memberAccount, "raw_request", `{"method":"GET","path":"secret-input"}`)
	call(memberAccount, "secret-unknown-tool", "{}")
	call(adminAccount, "list_teams", "{}")
	request("POST", "/mcp", `{"jsonrpc":"2.0","id":2,"method":"ping"}`, nil, memberAccount.MCPAPIKey, 200)
	if p := list(admin, own+"?all=true&user_id=2"); len(p.Logs) != 0 {
		t.Fatal("admin can see member logs")
	}
	p := list(member, own)
	if !p.Enabled || len(p.Logs) != 3 || p.Logs[0].Success || p.Logs[1].Success || !p.Logs[2].Success ||
		p.Logs[2].Tool != "list_teams" || p.Logs[2].Username != memberUser.Username || p.Logs[2].TowerAccount != memberAccount.TowerID {
		t.Fatalf("unexpected member results: %+v", p)
	}
	set(admin, true)
	call(adminAccount, "list_teams", "{}")
	set(member, false)
	call(memberAccount, "list_teams", "{}")
	if p := list(member, own+"?user_id=1"); p.Enabled || len(p.Logs) != 3 || p.Logs[0].Username != memberUser.Username {
		t.Fatalf("personal history after opt-out: %+v", p)
	}
	// Both views must hide expired rows even between cleanup runs.
	if _, err := store.db.Exec(`INSERT INTO mcp_operation_logs(user_id, username, tower_account, tool, started_at, duration_ms, success)
		SELECT user_id, username, tower_account, tool, 1, duration_ms, success FROM mcp_operation_logs`); err != nil {
		t.Fatal(err)
	}
	if len(list(member, own).Logs) != 3 || len(list(admin, own).Logs) != 1 {
		t.Fatal("expired logs are visible")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = store
	mux = newMux(cfg)
	var stored int
	if err := store.db.QueryRow("SELECT count(*) FROM mcp_operation_logs").Scan(&stored); err != nil || stored != 4 {
		t.Fatalf("startup cleanup: stored=%d err=%v", stored, err)
	}
	if p := list(admin, own); !p.Enabled || len(p.Logs) != 1 || p.Logs[0].Username != adminUser.Username {
		t.Fatalf("admin history after restart: %+v", p)
	}
	for i := 0; i < mcpLogPageSize; i++ {
		call(adminAccount, "raw_request", `{"method":"GET","path":"secret-input"}`)
	}
	first := list(admin, own)
	if !first.HasMore || len(first.Logs) != mcpLogPageSize {
		t.Fatalf("unexpected first page length %d", len(first.Logs))
	}
	cursor := first.Logs[len(first.Logs)-1].ID
	last := list(admin, fmt.Sprintf("%s?before=%d", own, cursor))
	if last.HasMore || len(last.Logs) != 1 || last.Logs[0].ID >= cursor {
		t.Fatalf("unexpected last page: %+v", last)
	}
	request("GET", own+"?before=invalid", "", member, "", 400)
}

func TestMCPLogExpiryAtMonthEnd(t *testing.T) {
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, pair := range [][2]string{
		{"2023-11-30T09:45:01.123+08:00", "2024-02-29T09:45:01.123+08:00"},
		{"2024-11-30T01:45:01.123+08:00", "2025-02-28T01:45:01.123+08:00"},
		{"2025-12-31T23:59:59.999+08:00", "2026-03-31T23:59:59.999+08:00"},
	} {
		start, err := time.Parse(time.RFC3339Nano, pair[0])
		if err != nil {
			t.Fatal(err)
		}
		expires, err := time.Parse(time.RFC3339Nano, pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`INSERT INTO mcp_operation_logs(user_id, username, tower_account, tool, started_at, duration_ms, success)
			VALUES (1, 'member', 'tower', 'list_teams', ?, 0, 1)`, start.UnixMilli()); err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			now  time.Time
			want int
		}{{expires.Add(-time.Millisecond), 1}, {expires, 0}} {
			if err := store.purgeExpiredMCPLogs(test.now); err != nil {
				t.Fatal(err)
			}
			var got int
			if err := store.db.QueryRow("SELECT count(*) FROM mcp_operation_logs").Scan(&got); err != nil || got != test.want {
				t.Fatalf("cleanup at %v: rows=%d want=%d err=%v", test.now, got, test.want, err)
			}
		}
	}
}
