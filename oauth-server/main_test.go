package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testConfig(towerURL string) Config {
	return Config{
		ClientID:     "cid",
		ClientSecret: "csecret",
		RedirectURI:  "http://localhost:8888/oauth2/callback",
		BaseURL:      towerURL,
		HTTPClient:   http.DefaultClient,
	}
}

func registerTestUser(t *testing.T, mux http.Handler, username, password string) (*http.Cookie, User, string) {
	t.Helper()
	body := `{"username":"` + username + `","password":"` + password + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("registerTestUser(%s) failed: %d %s", username, rec.Code, rec.Body.String())
	}
	var resp struct {
		User         User   `json:"user"`
		SessionToken string `json:"session_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode register response: %v", err)
	}
	var sessCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			sessCookie = c
		}
	}
	if sessCookie == nil {
		t.Fatalf("expected %s cookie", sessionCookieName)
	}
	return sessCookie, resp.User, resp.SessionToken
}

func TestHomeRendersFrontendAndDefaultPort(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TOWER_CLIENT_ID", "cid")
	t.Setenv("TOWER_CLIENT_SECRET", "csecret")
	t.Setenv("TOWER_REDIRECT_URI", "")
	t.Setenv("SITE_BASE_URL", "")
	t.Setenv("MCP_BASE_URL", "")

	cfg, err := loadConfigFromEnv()
	if err != nil {
		t.Fatalf("loadConfigFromEnv: %v", err)
	}
	if cfg.ListenAddr != "[::]:8888" || cfg.RedirectURI != "http://localhost:8888/oauth2/callback" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}

	mux := newMux(cfg)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "AgentsTower") {
		t.Fatalf("unexpected home response: %d", rec.Code)
	}

	cfgReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	cfgRec := httptest.NewRecorder()
	mux.ServeHTTP(cfgRec, cfgReq)
	if cfgRec.Code != http.StatusOK || !strings.Contains(cfgRec.Body.String(), `"is_oob":false`) {
		t.Fatalf("unexpected config response: %d %s", cfgRec.Code, cfgRec.Body.String())
	}
}

func TestPublicBaseURLs(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TOWER_CLIENT_ID", "cid")
	t.Setenv("TOWER_CLIENT_SECRET", "csecret")
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, tc := range []struct {
		name, site, mcp, redirect, wantMCP, wantRedirect string
	}{
		{"request fallback", "", "", "", "/mcp", "http://localhost:8888/oauth2/callback"},
		{"website only", " https://site.example/ ", "", "", "https://site.example/mcp", "https://site.example/oauth2/callback"},
		{"MCP only", "", "https://mcp.example/", "", "https://mcp.example/mcp", "http://localhost:8888/oauth2/callback"},
		{"split hosts", "https://site.example", "https://mcp.example/tower/", "", "https://mcp.example/tower/mcp", "https://site.example/oauth2/callback"},
		{"explicit redirect", "https://site.example", "https://mcp.example", "urn:ietf:wg:oauth:2.0:oob", "https://mcp.example/mcp", "urn:ietf:wg:oauth:2.0:oob"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SITE_BASE_URL", tc.site)
			t.Setenv("MCP_BASE_URL", tc.mcp)
			t.Setenv("TOWER_REDIRECT_URI", tc.redirect)
			cfg, err := loadConfigFromEnv()
			if err != nil {
				t.Fatal(err)
			}
			cfg.Store = store
			mux := newMux(cfg)
			config := httptest.NewRecorder()
			mux.ServeHTTP(config, httptest.NewRequest(http.MethodGet, "/api/config", nil))
			var got struct {
				MCPURL      string `json:"mcp_url"`
				RedirectURI string `json:"redirect_uri"`
			}
			if err := json.Unmarshal(config.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if config.Code != http.StatusOK || got.MCPURL != tc.wantMCP || got.RedirectURI != tc.wantRedirect {
				t.Fatalf("unexpected config: %d %+v", config.Code, got)
			}
			skillReq := httptest.NewRequest(http.MethodGet, "http://proxy.example/api/skill.md", nil)
			skillReq.Header.Set("X-Forwarded-Proto", "https")
			skill := httptest.NewRecorder()
			mux.ServeHTTP(skill, skillReq)
			wantSkillURL := tc.wantMCP
			if tc.site == "" && tc.mcp == "" {
				wantSkillURL = "https://proxy.example/mcp"
			}
			if skill.Code != http.StatusOK || !strings.Contains(skill.Body.String(), `"url": "`+wantSkillURL+`"`) {
				t.Fatalf("skill.md did not advertise %q: %d", wantSkillURL, skill.Code)
			}
		})
	}
	t.Setenv("SITE_BASE_URL", "https://site.example")
	t.Setenv("MCP_BASE_URL", "https://user:secret@mcp.example")
	if _, err := loadConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "MCP_BASE_URL") {
		t.Fatalf("expected invalid MCP_BASE_URL error, got %v", err)
	}
}

func TestManagementSiteIndexes(t *testing.T) {
	for _, site := range []string{"https://manage.example", ""} {
		cfg := testConfig("https://tower.im")
		cfg.SiteBaseURL = site
		cfg.MCPBaseURL = "https://mcp.example"
		cfg.DevProxyURL = "http://unused.example"
		mux := newMux(cfg)
		wantSite := cmp.Or(site, "https://proxy.example")
		get := func(path, contentType string) string {
			t.Helper()
			req := httptest.NewRequest(http.MethodGet, "http://proxy.example"+path, nil)
			req.Header.Set("X-Forwarded-Proto", "https")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), contentType) {
				t.Fatalf("%s: status %d, type %q", path, rec.Code, rec.Header().Get("Content-Type"))
			}
			return rec.Body.String()
		}

		var sitemap struct {
			URLs []string `xml:"url>loc"`
		}
		if err := xml.Unmarshal([]byte(get("/sitemap.xml", "application/xml")), &sitemap); err != nil {
			t.Fatal(err)
		}
		if len(sitemap.URLs) != 1 || sitemap.URLs[0] != wantSite+"/" {
			t.Fatalf("unexpected sitemap: %v", sitemap.URLs)
		}
		llms := get("/llms.txt", "text/plain")
		if !strings.HasPrefix(llms, "# AgentsTower\n") || !strings.Contains(llms, "("+wantSite+"/api/skill.md)") {
			t.Fatalf("unexpected llms.txt: %s", llms)
		}
		if skill := get("/api/skill.md", "text/markdown"); !strings.Contains(skill, "https://mcp.example/mcp") {
			t.Fatal("skill does not advertise the configured MCP endpoint")
		}
	}
}

func TestLoginRedirectsWithStateCookie(t *testing.T) {
	mux := newMux(testConfig("https://tower.im"))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(sess)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302, got %d", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://tower.im/oauth/authorize?") {
		t.Fatalf("unexpected redirect location: %s", loc)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != stateCookieName || cookies[0].Value == "" {
		t.Fatalf("expected %s cookie to be set", stateCookieName)
	}
}

func TestCallbackRejectsInvalidState(t *testing.T) {
	mux := newMux(testConfig("https://tower.im"))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code=abc&state=wrong", nil)
	req.AddCookie(sess)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "expected"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("expected 302 redirect to /, got %d", rec.Code)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != resultCookieName {
		t.Fatalf("expected %s cookie", resultCookieName)
	}

	resReq := httptest.NewRequest(http.MethodGet, "/api/auth/result", nil)
	resReq.AddCookie(sess)
	resReq.AddCookie(cookies[0])
	resRec := httptest.NewRecorder()
	mux.ServeHTTP(resRec, resReq)
	if resRec.Code != http.StatusOK || !strings.Contains(resRec.Body.String(), "state 校验失败") {
		t.Fatalf("expected state error from /api/auth/result, got %d %s", resRec.Code, resRec.Body.String())
	}
}

func TestCallbackExchangesCodeAndFetchesUser(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/oauth/token":
			_ = req.ParseForm()
			if req.FormValue("grant_type") != "authorization_code" || req.FormValue("code") != "authcode" {
				t.Fatalf("unexpected token form: %v", req.Form)
			}
			_ = json.NewEncoder(writer).Encode(TokenResponse{
				AccessToken:  "acc-123",
				TokenType:    "bearer",
				ExpiresIn:    7199,
				RefreshToken: "ref-456",
				Email:        "member@tower.im",
			})
		case "/api/v1/user":
			if req.Header.Get("Authorization") != "Bearer acc-123" {
				t.Fatalf("missing bearer header")
			}
			_, _ = writer.Write([]byte(`{"data":{"id":"u1","attributes":{"nickname":"Alice","email":"member@tower.im"}}}`))
		default:
			http.NotFound(writer, req)
		}
	}))
	defer tower.Close()

	mux := newMux(testConfig(tower.URL))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code=authcode&state=ok&format=json", nil)
	req.AddCookie(sess)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "ok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var result LoginResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Token.AccessToken != "acc-123" || result.User == nil || result.User.Nickname != "Alice" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestOTPRequiredAndSubmit(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/oauth/token":
			_ = req.ParseForm()
			if req.FormValue("captcha") == "" {
				_ = json.NewEncoder(writer).Encode(TokenResponse{Error: "otp_required"})
				return
			}
			if req.FormValue("captcha") != "654321" {
				t.Fatalf("unexpected captcha: %s", req.FormValue("captcha"))
			}
			_ = json.NewEncoder(writer).Encode(TokenResponse{AccessToken: "acc-otp", ExpiresIn: 7199})
		case "/api/v1/user":
			http.NotFound(writer, req)
		}
	}))
	defer tower.Close()

	mux := newMux(testConfig(tower.URL))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code=authcode&state=ok&format=json", nil)
	req.AddCookie(sess)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "ok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"otp_required":true`) {
		t.Fatalf("expected otp_required json, got %d: %s", rec.Code, rec.Body.String())
	}

	form := url.Values{"code": {"authcode"}, "captcha": {"654321"}}
	otpReq := httptest.NewRequest(http.MethodPost, "/oauth2/otp?format=json", strings.NewReader(form.Encode()))
	otpReq.AddCookie(sess)
	otpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	otpRec := httptest.NewRecorder()
	mux.ServeHTTP(otpRec, otpReq)
	if otpRec.Code != http.StatusOK || !strings.Contains(otpRec.Body.String(), "acc-otp") {
		t.Fatalf("expected token after otp, got %d: %s", otpRec.Code, otpRec.Body.String())
	}
}

func TestRefreshToken(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		_ = req.ParseForm()
		if req.FormValue("grant_type") != "refresh_token" || req.FormValue("refresh_token") != "ref-1" {
			t.Fatalf("unexpected refresh form: %v", req.Form)
		}
		if req.Header.Get("Authorization") != "Bearer old-acc" {
			t.Fatalf("expected bearer header, got %q", req.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(writer).Encode(TokenResponse{AccessToken: "new-acc", RefreshToken: "ref-2"})
	}))
	defer tower.Close()

	mux := newMux(testConfig(tower.URL))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")
	req := httptest.NewRequest(http.MethodPost, "/oauth2/refresh", strings.NewReader(`{"refresh_token":"ref-1","access_token":"old-acc"}`))
	req.AddCookie(sess)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "new-acc") {
		t.Fatalf("expected refreshed token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestManualOOBCode(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/oauth/token":
			_ = req.ParseForm()
			if req.FormValue("redirect_uri") != "urn:ietf:wg:oauth:2.0:oob" || req.FormValue("code") != "oob-code" {
				t.Fatalf("unexpected oob form: %v", req.Form)
			}
			_ = json.NewEncoder(writer).Encode(TokenResponse{AccessToken: "acc-oob", ExpiresIn: 7199})
		case "/api/v1/user":
			http.NotFound(writer, req)
		}
	}))
	defer tower.Close()

	cfg := testConfig(tower.URL)
	cfg.RedirectURI = "urn:ietf:wg:oauth:2.0:oob"
	mux := newMux(cfg)
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")

	cfgReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	cfgRec := httptest.NewRecorder()
	mux.ServeHTTP(cfgRec, cfgReq)
	if cfgRec.Code != http.StatusOK || !strings.Contains(cfgRec.Body.String(), `"is_oob":true`) {
		t.Fatalf("expected is_oob=true in config: %s", cfgRec.Body.String())
	}

	form := url.Values{"code": {"oob-code"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth2/code?format=json", strings.NewReader(form.Encode()))
	req.AddCookie(sess)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "acc-oob") {
		t.Fatalf("expected oob token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDevProxyMode(t *testing.T) {
	viteMock := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		_, _ = writer.Write([]byte("vite-dev:" + req.URL.Path))
	}))
	defer viteMock.Close()

	cfg := testConfig("https://tower.im")
	cfg.DevProxyURL = viteMock.URL
	mux := newMux(cfg)

	homeReq := httptest.NewRequest(http.MethodGet, "/", nil)
	homeRec := httptest.NewRecorder()
	mux.ServeHTTP(homeRec, homeReq)
	if homeRec.Code != http.StatusOK || homeRec.Body.String() != "vite-dev:/" {
		t.Fatalf("expected proxied vite response, got %d %s", homeRec.Code, homeRec.Body.String())
	}

	apiReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	apiRec := httptest.NewRecorder()
	mux.ServeHTTP(apiRec, apiReq)
	if apiRec.Code != http.StatusOK || !strings.Contains(apiRec.Body.String(), `"is_oob":false`) {
		t.Fatalf("expected api/config handled by Go, got %d %s", apiRec.Code, apiRec.Body.String())
	}
}

func TestVerifyToken(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/user" {
			http.NotFound(writer, req)
			return
		}
		if req.Header.Get("Authorization") == "Bearer good-token" {
			_ = json.NewEncoder(writer).Encode(UserInfo{Nickname: "Alice", Email: "alice@example.com"})
			return
		}
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"error":"invalid_token"}`))
	}))
	defer tower.Close()

	mux := newMux(testConfig(tower.URL))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")

	okReq := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(`{"access_token":"good-token"}`))
	okReq.AddCookie(sess)
	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK || !strings.Contains(okRec.Body.String(), `"valid":true`) {
		t.Fatalf("expected valid=true, got %d %s", okRec.Code, okRec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(`{"access_token":"bad-token"}`))
	badReq.AddCookie(sess)
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusOK || !strings.Contains(badRec.Body.String(), `"valid":false`) {
		t.Fatalf("expected valid=false, got %d %s", badRec.Code, badRec.Body.String())
	}
}

func TestHealthzAndCodeExtraction(t *testing.T) {
	mux := newMux(testConfig("https://tower.im"))
	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok"`) {
		t.Fatalf("unexpected healthz response: %d %s", rec.Code, rec.Body.String())
	}

	if got := extractCodeInput(`  "http://localhost:8888/oauth2/callback?code=abc123&state=xyz" `); got != "abc123" {
		t.Fatalf("expected extracted code abc123, got %q", got)
	}
	if got := extractCodeInput("code=raw456&state=1"); got != "raw456" {
		t.Fatalf("expected extracted code raw456, got %q", got)
	}
}

func TestAPIProxyDeleteRestrictions(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer tower.Close()
	handler := handleAPIProxy(testConfig(tower.URL))

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/api/v1/projects/p1", http.StatusForbidden},
		{"/api/v1/teams/t1/todos/2", http.StatusForbidden},
		{"/api/v1/todos/1?x=/completion", http.StatusForbidden},
		{"/api/v1/todos/1/completion", http.StatusOK},
		{"/api/v1/todos/1/assignment", http.StatusOK},
	} {
		t.Run(tc.path, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{"method": "DELETE", "path": tc.path, "access_token": "test-token"})
			if err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(string(body))))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestAPIProxy(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/v1/teams" || req.URL.RawQuery != "page=1" {
			t.Fatalf("unexpected proxied path/query: %s?%s", req.URL.Path, req.URL.RawQuery)
		}
		if req.Header.Get("Authorization") != "Bearer tok-1" {
			t.Fatalf("missing bearer token: %s", req.Header.Get("Authorization"))
		}
		_, _ = writer.Write([]byte(`[{"guid":"t1","name":"Team A"}]`))
	}))
	defer tower.Close()

	mux := newMux(testConfig(tower.URL))
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")

	okReq := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"method":"GET","path":"/api/v1/teams","query":"page=1","access_token":"tok-1"}`))
	okReq.AddCookie(sess)
	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK || !strings.Contains(okRec.Body.String(), `"Team A"`) {
		t.Fatalf("unexpected proxy response: %d %s", okRec.Code, okRec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"method":"GET","path":"/oauth/token","access_token":"tok-1"}`))
	badReq.AddCookie(sess)
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-api path, got %d", badRec.Code)
	}
}

func TestSQLiteStorePersistenceAndAccountManagement(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "agents-tower.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	saved1, err := store.SaveLogin(&UserInfo{ID: "u-100", Nickname: "Bob", Email: "bob@tower.im"}, TokenResponse{
		AccessToken:  "acc-1",
		RefreshToken: "ref-1",
		ExpiresIn:    7200,
	})
	if err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}
	if !strings.HasPrefix(saved1.MCPAPIKey, "at_sk-") {
		t.Fatalf("expected at_sk- prefix, got %q", saved1.MCPAPIKey)
	}

	// Re-login preserves existing MCP API Key so agent configs don't break
	saved2, err := store.SaveLogin(&UserInfo{ID: "u-100", Nickname: "Bob2"}, TokenResponse{
		AccessToken: "acc-2",
		ExpiresIn:   7200,
	})
	if err != nil {
		t.Fatalf("SaveLogin 2: %v", err)
	}
	if saved2.MCPAPIKey != saved1.MCPAPIKey || saved2.RefreshToken != "ref-1" || saved2.AccessToken != "acc-2" {
		t.Fatalf("unexpected updated account: %+v", saved2)
	}
	_ = store.Close()

	// Reopen SQLite file to verify persistence on disk
	reopened, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()

	fetched, err := reopened.GetByMCPKey(saved1.MCPAPIKey)
	if err != nil || fetched.Nickname != "Bob2" || fetched.AccessToken != "acc-2" {
		t.Fatalf("unexpected reopened account: %+v, err=%v", fetched, err)
	}

	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v1/user" && req.Header.Get("Authorization") == "Bearer sync-tok" {
			_, _ = writer.Write([]byte(`{"data":{"id":"u-sync","attributes":{"nickname":"SyncUser","email":"sync@tower.im"}}}`))
			return
		}
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer tower.Close()

	cfg := testConfig(tower.URL)
	cfg.Store = reopened
	mux := newMux(cfg)
	sess, _, _ := registerTestUser(t, mux, "tester", "secret123")

	syncReq := httptest.NewRequest(http.MethodPost, "/api/accounts/sync", strings.NewReader(`{"token":{"access_token":"sync-tok","refresh_token":"sync-ref"}}`))
	syncReq.AddCookie(sess)
	syncRec := httptest.NewRecorder()
	mux.ServeHTTP(syncRec, syncReq)
	if syncRec.Code != http.StatusOK {
		t.Fatalf("sync failed: %d %s", syncRec.Code, syncRec.Body.String())
	}
	var syncRes LoginResult
	if err := json.Unmarshal(syncRec.Body.Bytes(), &syncRes); err != nil || !strings.HasPrefix(syncRes.MCPAPIKey, "at_sk-") {
		t.Fatalf("invalid sync response: %+v err=%v", syncRes, err)
	}

	rotReq := httptest.NewRequest(http.MethodPost, "/api/mcp-key/rotate", strings.NewReader(`{"credential":"`+syncRes.MCPAPIKey+`"}`))
	rotReq.AddCookie(sess)
	rotRec := httptest.NewRecorder()
	mux.ServeHTTP(rotRec, rotReq)
	if rotRec.Code != http.StatusOK || strings.Contains(rotRec.Body.String(), syncRes.MCPAPIKey) {
		t.Fatalf("rotate failed: %d %s", rotRec.Code, rotRec.Body.String())
	}
	var rotRes struct {
		MCPAPIKey string `json:"mcp_api_key"`
	}
	_ = json.Unmarshal(rotRec.Body.Bytes(), &rotRes)

	delReq := httptest.NewRequest(http.MethodDelete, "/api/account", strings.NewReader(`{"credential":"`+rotRes.MCPAPIKey+`"}`))
	delReq.AddCookie(sess)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if delRec.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", delRec.Code, delRec.Body.String())
	}
	if _, err := reopened.GetByMCPKey(rotRes.MCPAPIKey); err == nil {
		t.Fatalf("expected deleted account to be gone")
	}
}

func TestMCPStreamableHTTPAndAutoRefresh(t *testing.T) {
	refreshed := false
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/oauth/token":
			_ = req.ParseForm()
			if req.FormValue("grant_type") != "refresh_token" || req.FormValue("refresh_token") != "ref-expired" {
				t.Fatalf("unexpected refresh form: %v", req.Form)
			}
			refreshed = true
			_ = json.NewEncoder(writer).Encode(TokenResponse{
				AccessToken:  "acc-fresh",
				RefreshToken: "ref-fresh",
				ExpiresIn:    7200,
				CreatedAt:    time.Now().Unix(),
			})
		case "/api/v1/teams":
			if req.Header.Get("Authorization") != "Bearer acc-fresh" {
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(`{"error":"expired"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"data":[{"id":"team-1","attributes":{"name":"AI Team"}}]}`))
		case "/api/v1/todolists/tl-1/todos":
			if req.Header.Get("Authorization") != "Bearer acc-fresh" {
				t.Fatalf("expected fresh token")
			}
			if req.Method == http.MethodGet {
				if req.URL.Query().Get("page[number]") != "2" || req.URL.Query().Get("completed_todo") != "true" {
					t.Fatalf("unexpected query: %s", req.URL.RawQuery)
				}
				_, _ = writer.Write([]byte(`{"data":[{"id":"todo-1"}]}`))
				return
			}
			rawBody, _ := io.ReadAll(req.Body)
			var bodyMap map[string]map[string]any
			_ = json.Unmarshal(rawBody, &bodyMap)
			if bodyMap["todo"]["content"] != "Ship MCP" || bodyMap["todo"]["select_custom"] != "v1" {
				t.Fatalf("unexpected create todo body: %s", string(rawBody))
			}
			_, _ = writer.Write([]byte(`{"data":{"id":"todo-new","attributes":{"content":"Ship MCP"}}}`))
		default:
			http.NotFound(writer, req)
		}
	}))
	defer tower.Close()

	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()

	// Seed an already-expired token so MCP call triggers automatic refresh!
	acc, err := store.SaveLogin(&UserInfo{ID: "u-mcp", Nickname: "AgentUser"}, TokenResponse{
		AccessToken:  "acc-old",
		RefreshToken: "ref-expired",
		ExpiresIn:    3600,
		CreatedAt:    time.Now().Add(-2 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}

	cfg := testConfig(tower.URL)
	cfg.Store = store
	mux := newMux(cfg)

	// 1. Unauthorized without header
	unauthReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	unauthRec := httptest.NewRecorder()
	mux.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without key, got %d", unauthRec.Code)
	}

	// 2. Initialize with AGENTS_TOWER_API_KEY header (also auto-refreshes expired token!)
	initReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`))
	initReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	initRec := httptest.NewRecorder()
	mux.ServeHTTP(initRec, initReq)
	if initRec.Code != http.StatusOK || !strings.Contains(initRec.Body.String(), `"AgentsTower"`) {
		t.Fatalf("initialize failed: %d %s", initRec.Code, initRec.Body.String())
	}
	if !refreshed {
		t.Fatalf("expected expired Tower token to be auto-refreshed on MCP request")
	}
	if initRec.Header().Get("Mcp-Session-Id") == "" {
		t.Fatalf("expected Mcp-Session-Id header")
	}

	// 3. Notification returns 202 Accepted
	notifReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	notifReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	notifRec := httptest.NewRecorder()
	mux.ServeHTTP(notifRec, notifReq)
	if notifRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for notification, got %d", notifRec.Code)
	}

	// 4. tools/list
	listReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	listReq.Header.Set("Authorization", "Bearer "+acc.MCPAPIKey)
	listRec := httptest.NewRecorder()
	mux.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), `"tower_create_todo"`) {
		t.Fatalf("tools/list failed: %d %s", listRec.Code, listRec.Body.String())
	}

	// 5. tools/call GET with query parameter wire mapping (page -> page[number])
	callListReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tower_list_todos","arguments":{"todolist_id":"tl-1","page":2,"completed_todo":true}}}`))
	callListReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	callListRec := httptest.NewRecorder()
	mux.ServeHTTP(callListRec, callListReq)
	if callListRec.Code != http.StatusOK || !strings.Contains(callListRec.Body.String(), `todo-1`) {
		t.Fatalf("tools/call list_todos failed: %d %s", callListRec.Code, callListRec.Body.String())
	}

	// 6. tools/call POST with envelope + custom_fields merge
	callCreateReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tower_create_todo","arguments":{"todolist_id":"tl-1","content":"Ship MCP","custom_fields":{"select_custom":"v1"}}}}`))
	callCreateReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	callCreateRec := httptest.NewRecorder()
	mux.ServeHTTP(callCreateRec, callCreateReq)
	if callCreateRec.Code != http.StatusOK || !strings.Contains(callCreateRec.Body.String(), `todo-new`) {
		t.Fatalf("tools/call create_todo failed: %d %s", callCreateRec.Code, callCreateRec.Body.String())
	}
}

func TestUserAuthQuickLoginRolesAndCLI(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "users.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	cfg := testConfig("https://tower.im")
	cfg.Store = store
	mux := newMux(cfg)

	// 1. Unauthenticated access to protected endpoints returns 401
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	unauthRec := httptest.NewRecorder()
	mux.ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauth /api/accounts, got %d", unauthRec.Code)
	}

	// 2. First user automatically becomes admin; second user becomes normal user
	aliceCookie, alice, aliceSess := registerTestUser(t, mux, "alice", "pass1234")
	if alice.Role != RoleAdmin {
		t.Fatalf("expected first user alice to be admin, got %q", alice.Role)
	}
	bobCookie, bob, bobSess := registerTestUser(t, mux, "bob", "pass5678")
	if bob.Role != RoleUser {
		t.Fatalf("expected second user bob to be user, got %q", bob.Role)
	}

	// 3. Normal user cannot access /api/admin/users
	bobAdminReq := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	bobAdminReq.AddCookie(bobCookie)
	bobAdminRec := httptest.NewRecorder()
	mux.ServeHTTP(bobAdminRec, bobAdminReq)
	if bobAdminRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for normal user on admin API, got %d", bobAdminRec.Code)
	}

	// 4. Admin can list users, but cannot revoke sole admin
	aliceListReq := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	aliceListReq.AddCookie(aliceCookie)
	aliceListRec := httptest.NewRecorder()
	mux.ServeHTTP(aliceListRec, aliceListReq)
	if aliceListRec.Code != http.StatusOK || !strings.Contains(aliceListRec.Body.String(), `"bob"`) {
		t.Fatalf("admin list users failed: %d %s", aliceListRec.Code, aliceListRec.Body.String())
	}

	revokeSelfReq := httptest.NewRequest(http.MethodPost, "/api/admin/users/1/role", strings.NewReader(`{"role":"user"}`))
	revokeSelfReq.AddCookie(aliceCookie)
	revokeSelfRec := httptest.NewRecorder()
	mux.ServeHTTP(revokeSelfRec, revokeSelfReq)
	if revokeSelfRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 revoking sole admin, got %d", revokeSelfRec.Code)
	}

	// 5. Logout without revoke keeps quick-login session valid; quick-login restores cookie
	logoutSwitchReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{"revoke":false}`))
	logoutSwitchReq.AddCookie(aliceCookie)
	logoutSwitchRec := httptest.NewRecorder()
	mux.ServeHTTP(logoutSwitchRec, logoutSwitchReq)
	if logoutSwitchRec.Code != http.StatusOK {
		t.Fatalf("logout failed: %d", logoutSwitchRec.Code)
	}

	quickReq := httptest.NewRequest(http.MethodPost, "/api/auth/quick-login", strings.NewReader(`{"session_token":"`+aliceSess+`"}`))
	quickRec := httptest.NewRecorder()
	mux.ServeHTTP(quickRec, quickReq)
	if quickRec.Code != http.StatusOK || !strings.Contains(quickRec.Body.String(), `"alice"`) {
		t.Fatalf("quick-login failed: %d %s", quickRec.Code, quickRec.Body.String())
	}

	// Logout with revoke invalidates the saved session token
	revokeSessReq := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{"session_token":"`+bobSess+`","revoke":true}`))
	revokeSessRec := httptest.NewRecorder()
	mux.ServeHTTP(revokeSessRec, revokeSessReq)

	quickBobReq := httptest.NewRequest(http.MethodPost, "/api/auth/quick-login", strings.NewReader(`{"session_token":"`+bobSess+`"}`))
	quickBobRec := httptest.NewRecorder()
	mux.ServeHTTP(quickBobRec, quickBobReq)
	if quickBobRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after revoked session, got %d", quickBobRec.Code)
	}
	_ = store.Close()

	// 6. CLI manual admin grant, user list, revoke, and reset-password
	var out bytes.Buffer
	if err := runCLI([]string{"admin", "grant", "bob"}, dbPath, &out); err != nil {
		t.Fatalf("CLI admin grant bob failed: %v", err)
	}
	out.Reset()
	if err := runCLI([]string{"user", "list"}, dbPath, &out); err != nil {
		t.Fatalf("CLI user list failed: %v", err)
	}
	if !strings.Contains(out.String(), "bob") || !strings.Contains(out.String(), "admin") {
		t.Fatalf("unexpected CLI user list output:\n%s", out.String())
	}
	out.Reset()
	if err := runCLI([]string{"admin", "revoke", "bob"}, dbPath, &out); err != nil {
		t.Fatalf("CLI admin revoke bob failed: %v", err)
	}
	if err := runCLI([]string{"user", "reset-password", "bob", "newpass999"}, dbPath, &out); err != nil {
		t.Fatalf("CLI reset-password failed: %v", err)
	}

	reopened, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	authedBob, err := reopened.AuthenticateUser("bob", "newpass999")
	if err != nil || authedBob.Role != RoleUser {
		t.Fatalf("expected bob with new password and user role, got %+v err=%v", authedBob, err)
	}
}

func TestListPaginationDefaultsAndCaps(t *testing.T) {
	var seenQueries []string
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		seenQueries = append(seenQueries, req.URL.RawQuery)
		items := make([]map[string]any, 35)
		for i := range items {
			items[i] = map[string]any{"id": i + 1}
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"data": items})
	}))
	defer tower.Close()

	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	acc, err := store.SaveLogin(&UserInfo{ID: "u-page"}, TokenResponse{AccessToken: "tok-p"})
	if err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}

	cfg := testConfig(tower.URL)
	cfg.Store = store
	mux := newMux(cfg)

	// 1. Default MCP list call injects page[number]=1&page[size]=20 and caps 35 -> 20 items
	req1 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tower_list_todos","arguments":{"todolist_id":"tl-1"}}}`))
	req1.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK || !strings.Contains(rec1.Body.String(), `"truncated\"`) && !strings.Contains(rec1.Body.String(), `truncated`) {
		t.Fatalf("expected truncated meta in response: %d %s", rec1.Code, rec1.Body.String())
	}
	q1, _ := url.ParseQuery(seenQueries[0])
	if q1.Get("page[number]") != "1" || q1.Get("page[size]") != "20" {
		t.Fatalf("expected default page=1 & page[size]=20, got %s", seenQueries[0])
	}

	// 2. Oversized page_size=500 is clamped to 50
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tower_list_todos","arguments":{"todolist_id":"tl-1","page":3,"page_size":500}}}`))
	req2.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	q2, _ := url.ParseQuery(seenQueries[1])
	if q2.Get("page[number]") != "3" || q2.Get("page[size]") != "50" {
		t.Fatalf("expected clamped page[size]=50, got %s", seenQueries[1])
	}

	// 3. /api/proxy also clamps page[size]=999 to 50 and defaults empty list queries
	sess, _, _ := registerTestUser(t, mux, "pager", "pass123")
	proxyReq := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"method":"GET","path":"/api/v1/projects/p1/topics","query":"page[size]=999","access_token":"tok-p"}`))
	proxyReq.AddCookie(sess)
	proxyRec := httptest.NewRecorder()
	mux.ServeHTTP(proxyRec, proxyReq)
	q3, _ := url.ParseQuery(seenQueries[2])
	if q3.Get("page[number]") != "1" || q3.Get("page[size]") != "50" {
		t.Fatalf("expected proxy clamped query, got %s", seenQueries[2])
	}

	// 4. Local-sliced list tools (like tower_list_projects) honor explicit page=2 & page_size=10
	req4 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"tower_list_projects","arguments":{"team_id":"t1","page":2,"page_size":10}}}`))
	req4.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	rec4 := httptest.NewRecorder()
	mux.ServeHTTP(rec4, req4)
	var pageResult struct {
		Result struct{ Content []struct{ Text string } }
	}
	if err := json.Unmarshal(rec4.Body.Bytes(), &pageResult); err != nil || len(pageResult.Result.Content) != 1 {
		t.Fatal(rec4.Body.String())
	}
	var page struct {
		Items []struct{ ID int }
		Meta  struct {
			HasMore bool `json:"has_more"`
		}
	}
	if err := json.Unmarshal([]byte(pageResult.Result.Content[0].Text), &page); err != nil || len(page.Items) != 10 || !page.Meta.HasMore {
		t.Fatal(rec4.Body.String())
	}
	for i, item := range page.Items {
		if item.ID != 11+i {
			t.Fatalf("unexpected page item %d: %d", i, item.ID)
		}
	}
}

func TestSecuritySettingsHardDeleteAndRateLimit(t *testing.T) {
	var towerServerURL string
	tower := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/todos/t1/completion":
			writeJSON(writer, http.StatusOK, map[string]any{"completed": true})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v1/todos/t1/completion":
			writeJSON(writer, http.StatusOK, map[string]any{"reopened": true})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/v1/todos/t1":
			writeJSON(writer, http.StatusNoContent, nil)
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/teams":
			writeJSON(writer, http.StatusOK, map[string]any{
				"data": []map[string]any{
					{"id": "team-1", "type": "teams", "attributes": map[string]any{"name": "CoreTeam"}},
				},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/teams/team-1/member":
			writeJSON(writer, http.StatusOK, map[string]any{
				"data": map[string]any{"id": "mem-1", "type": "members", "attributes": map[string]any{"nickname": "Alice"}},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/members/mem-1/assigned_uncompleted_todos":
			writeJSON(writer, http.StatusOK, map[string]any{
				"data": []map[string]any{
					{"id": "todo-a1", "type": "todos", "attributes": map[string]any{"content": "Fix bug"}},
				},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/teams/team-1/direct_uploads":
			writeJSON(writer, http.StatusOK, map[string]any{
				"guid": "guid-oss-1",
				"directUpload": map[string]string{
					"url":            towerServerURL + "/mock-oss",
					"OSSAccessKeyId": "ak-1",
					"policy":         "pol-1",
					"Signature":      "sig-1",
					"callback":       "cb-1",
					"key":            "641/guid-oss-1",
				},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/mock-oss":
			if !strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data") {
				http.Error(writer, "expected multipart", http.StatusBadRequest)
				return
			}
			writeJSON(writer, http.StatusOK, map[string]any{"oss": "ok"})
		case req.Method == http.MethodPost && req.URL.Path == "/api/v1/projects/proj-1/uploads":
			writeJSON(writer, http.StatusOK, map[string]any{
				"data": map[string]any{"id": "up-1", "type": "uploads", "attributes": map[string]any{"name": "note.md"}},
			})
		default:
			http.NotFound(writer, req)
		}
	}))
	defer tower.Close()
	towerServerURL = tower.URL

	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer store.Close()
	acc, err := store.SaveLogin(&UserInfo{ID: "u-sec"}, TokenResponse{AccessToken: "tok-sec"})
	if err != nil {
		t.Fatalf("SaveLogin: %v", err)
	}

	cfg := testConfig(tower.URL)
	cfg.Store = store
	mux := newMux(cfg)

	// 1. SKILL.md endpoint renders dynamic host /mcp URL
	skillReq := httptest.NewRequest(http.MethodGet, "http://tower.example.lan:8888/api/skill.md?download=1", nil)
	skillRec := httptest.NewRecorder()
	mux.ServeHTTP(skillRec, skillReq)
	if skillRec.Code != http.StatusOK || !strings.Contains(skillRec.Body.String(), "http://tower.example.lan:8888/mcp") {
		t.Fatalf("skill.md failed: %d %s", skillRec.Code, skillRec.Body.String())
	}

	// 2. Register first user (admin)
	adminCookie, _, _ := registerTestUser(t, mux, "admin1", "pass123")

	// 3. Default MCP hard-delete protection blocks tower_delete_todo, but allows tower_complete_todo and tower_reopen_todo (non-destructive)
	for _, def := range listMCPToolDefinitions() {
		if def["name"] == "tower_complete_todo" || def["name"] == "tower_reopen_todo" || def["name"] == "tower_unassign_todo" {
			ann, _ := def["annotations"].(map[string]any)
			if ann["destructiveHint"] == true {
				t.Fatalf("expected %s destructiveHint=false, got %v", def["name"], ann)
			}
		}
	}

	compReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tower_complete_todo","arguments":{"todo_id":"t1"}}}`))
	compReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	compRec := httptest.NewRecorder()
	mux.ServeHTTP(compRec, compReq)
	if !strings.Contains(compRec.Body.String(), "completed") || strings.Contains(compRec.Body.String(), `"isError":true`) {
		t.Fatalf("expected complete_todo allowed, got: %s", compRec.Body.String())
	}

	delReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"tower_delete_todo","arguments":{"todo_id":"t1"}}}`))
	delReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	delRec := httptest.NewRecorder()
	mux.ServeHTTP(delRec, delReq)
	if !strings.Contains(delRec.Body.String(), "硬删除保护") || !strings.Contains(delRec.Body.String(), `"isError":true`) {
		t.Fatalf("expected hard delete blocked by default, got: %s", delRec.Body.String())
	}

	reopenReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"tower_reopen_todo","arguments":{"todo_id":"t1"}}}`))
	reopenReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	reopenRec := httptest.NewRecorder()
	mux.ServeHTTP(reopenRec, reopenReq)
	if !strings.Contains(reopenRec.Body.String(), "reopened") || strings.Contains(reopenRec.Body.String(), `"isError":true`) {
		t.Fatalf("expected reopen_todo allowed, got: %s", reopenRec.Body.String())
	}

	// 4. Admin updates settings: registration_mode=invite, invite_code=TEAM88, allow_mcp_delete=true
	setReq := httptest.NewRequest(http.MethodPost, "/api/admin/settings", strings.NewReader(`{"registration_mode":"invite","invite_code":"TEAM88","allow_mcp_delete":true}`))
	setReq.AddCookie(adminCookie)
	setRec := httptest.NewRecorder()
	mux.ServeHTTP(setRec, setReq)
	if setRec.Code != http.StatusOK {
		t.Fatalf("update settings failed: %d %s", setRec.Code, setRec.Body.String())
	}

	// Hard delete now succeeds
	delRec2 := httptest.NewRecorder()
	delReq2 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"tower_delete_todo","arguments":{"todo_id":"t1"}}}`))
	delReq2.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	mux.ServeHTTP(delRec2, delReq2)
	if strings.Contains(delRec2.Body.String(), `"isError":true`) {
		t.Fatalf("expected hard delete allowed after setting enabled, got: %s", delRec2.Body.String())
	}

	// 5. Invite code enforcement
	badReg := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"username":"bob","password":"password123"}`))
	badRegRec := httptest.NewRecorder()
	mux.ServeHTTP(badRegRec, badReg)
	if badRegRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without invite code, got %d", badRegRec.Code)
	}
	goodReg := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"username":"bob","password":"password123","invite_code":"TEAM88"}`))
	goodRegRec := httptest.NewRecorder()
	mux.ServeHTTP(goodRegRec, goodReg)
	if goodRegRec.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid invite code, got %d: %s", goodRegRec.Code, goodRegRec.Body.String())
	}
	var bobCookie *http.Cookie
	for _, c := range goodRegRec.Result().Cookies() {
		if c.Name == sessionCookieName {
			bobCookie = c
		}
	}

	// 6. Self password change & logout-all
	pwReq := httptest.NewRequest(http.MethodPost, "/api/auth/password", strings.NewReader(`{"current_password":"password123","new_password":"newpassword456"}`))
	pwReq.AddCookie(bobCookie)
	pwRec := httptest.NewRecorder()
	mux.ServeHTTP(pwRec, pwReq)
	if pwRec.Code != http.StatusOK {
		t.Fatalf("self password change failed: %d %s", pwRec.Code, pwRec.Body.String())
	}

	// 7. Login rate limiting (8 failed attempts -> 401, 9th -> 429)
	for i := 1; i <= 8; i++ {
		failReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"bob","password":"wrong"}`))
		failReq.RemoteAddr = "10.0.0.9:12345"
		failRec := httptest.NewRecorder()
		mux.ServeHTTP(failRec, failReq)
		if failRec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d expected 401, got %d", i, failRec.Code)
		}
	}
	rateReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"bob","password":"newpassword456"}`))
	rateReq.RemoteAddr = "10.0.0.9:12345"
	rateRec := httptest.NewRecorder()
	mux.ServeHTTP(rateRec, rateReq)
	if rateRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 9th attempt, got %d", rateRec.Code)
	}

	// 8. Composite MCP tools: tower_my_todo_overview & tower_upload_file
	ovReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"tower_my_todo_overview","arguments":{}}}`))
	ovReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	ovRec := httptest.NewRecorder()
	mux.ServeHTTP(ovRec, ovReq)
	if ovRec.Code != http.StatusOK || !strings.Contains(ovRec.Body.String(), "todo-a1") || !strings.Contains(ovRec.Body.String(), "CoreTeam") {
		t.Fatalf("tower_my_todo_overview failed: %d %s", ovRec.Code, ovRec.Body.String())
	}

	upReq := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"tower_upload_file","arguments":{"team_id":"team-1","project_id":"proj-1","filename":"note.md","content":"# Hello Tower"}}}`))
	upReq.Header.Set("AGENTS_TOWER_API_KEY", acc.MCPAPIKey)
	upRec := httptest.NewRecorder()
	mux.ServeHTTP(upRec, upReq)
	if upRec.Code != http.StatusOK || !strings.Contains(upRec.Body.String(), "guid-oss-1") || !strings.Contains(upRec.Body.String(), "up-1") {
		t.Fatalf("tower_upload_file failed: %d %s", upRec.Code, upRec.Body.String())
	}
}
