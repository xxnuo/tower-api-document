package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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

func TestHomeRendersFrontendAndDefaultPort(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("TOWER_CLIENT_ID", "cid")
	t.Setenv("TOWER_CLIENT_SECRET", "csecret")

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
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Tower OAuth 凭据中心") {
		t.Fatalf("unexpected home response: %d", rec.Code)
	}

	cfgReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	cfgRec := httptest.NewRecorder()
	mux.ServeHTTP(cfgRec, cfgReq)
	if cfgRec.Code != http.StatusOK || !strings.Contains(cfgRec.Body.String(), `"is_oob":false`) {
		t.Fatalf("unexpected config response: %d %s", cfgRec.Code, cfgRec.Body.String())
	}
}

func TestLoginRedirectsWithStateCookie(t *testing.T) {
	mux := newMux(testConfig("https://tower.im"))
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code=abc&state=wrong", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code=authcode&state=ok&format=json", nil)
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
	req := httptest.NewRequest(http.MethodGet, "/oauth2/callback?code=authcode&state=ok&format=json", nil)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: "ok"})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"otp_required":true`) {
		t.Fatalf("expected otp_required json, got %d: %s", rec.Code, rec.Body.String())
	}

	form := url.Values{"code": {"authcode"}, "captcha": {"654321"}}
	otpReq := httptest.NewRequest(http.MethodPost, "/oauth2/otp?format=json", strings.NewReader(form.Encode()))
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
	req := httptest.NewRequest(http.MethodPost, "/oauth2/refresh", strings.NewReader(`{"refresh_token":"ref-1","access_token":"old-acc"}`))
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

	cfgReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	cfgRec := httptest.NewRecorder()
	mux.ServeHTTP(cfgRec, cfgReq)
	if cfgRec.Code != http.StatusOK || !strings.Contains(cfgRec.Body.String(), `"is_oob":true`) {
		t.Fatalf("expected is_oob=true in config: %s", cfgRec.Body.String())
	}

	form := url.Values{"code": {"oob-code"}}
	req := httptest.NewRequest(http.MethodPost, "/oauth2/code?format=json", strings.NewReader(form.Encode()))
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

	okReq := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(`{"access_token":"good-token"}`))
	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK || !strings.Contains(okRec.Body.String(), `"valid":true`) {
		t.Fatalf("expected valid=true, got %d %s", okRec.Code, okRec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(`{"access_token":"bad-token"}`))
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

	okReq := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"method":"GET","path":"/api/v1/teams","query":"page=1","access_token":"tok-1"}`))
	okRec := httptest.NewRecorder()
	mux.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusOK || !strings.Contains(okRec.Body.String(), `"Team A"`) {
		t.Fatalf("unexpected proxy response: %d %s", okRec.Code, okRec.Body.String())
	}

	badReq := httptest.NewRequest(http.MethodPost, "/api/proxy", strings.NewReader(`{"method":"GET","path":"/oauth/token","access_token":"tok-1"}`))
	badRec := httptest.NewRecorder()
	mux.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-api path, got %d", badRec.Code)
	}
}
