package main

import (
	"bufio"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	stateCookieName  = "tower_oauth_state"
	resultCookieName = "tower_auth_result"
)

//go:embed all:web/dist
var webDistFS embed.FS

type pendingEntry struct {
	Payload   any
	ExpiresAt time.Time
}

var (
	pendingMu      sync.Mutex
	pendingResults = map[string]pendingEntry{}
)

type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string
	BaseURL      string
	ListenAddr   string
	DevProxyURL  string
	HTTPClient   *http.Client
}

type TokenResponse struct {
	AccessToken      string `json:"access_token,omitempty"`
	TokenType        string `json:"token_type,omitempty"`
	ExpiresIn        int    `json:"expires_in,omitempty"`
	RefreshToken     string `json:"refresh_token,omitempty"`
	CreatedAt        int64  `json:"created_at,omitempty"`
	Email            string `json:"email,omitempty"`
	Error            string `json:"error,omitempty"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type UserInfo struct {
	ID       string `json:"id,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Email    string `json:"email,omitempty"`
	Avatar   string `json:"gavatar,omitempty"`
}

type LoginResult struct {
	User  *UserInfo     `json:"user,omitempty"`
	Token TokenResponse `json:"token"`
}

func loadDotEnv(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key != "" && os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
}

func loadConfigFromEnv() (Config, error) {
	loadDotEnv(".env")
	cfg := Config{
		ClientID:     strings.TrimSpace(os.Getenv("TOWER_CLIENT_ID")),
		ClientSecret: strings.TrimSpace(os.Getenv("TOWER_CLIENT_SECRET")),
		RedirectURI:  strings.TrimSpace(os.Getenv("TOWER_REDIRECT_URI")),
		BaseURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("TOWER_BASE_URL")), "/"),
		ListenAddr:   strings.TrimSpace(os.Getenv("LISTEN_ADDR")),
		DevProxyURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("VITE_DEV_URL")), "/"),
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	}
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = "http://localhost:8888/oauth2/callback"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://tower.im"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "[::]:8888"
	}
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return cfg, errors.New("TOWER_CLIENT_ID and TOWER_CLIENT_SECRET are required")
	}
	return cfg, nil
}

func randomState() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func newMux(cfg Config) http.Handler {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(writer http.ResponseWriter, req *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/config", func(writer http.ResponseWriter, req *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]any{
			"is_oob":       isOOB(cfg),
			"redirect_uri": cfg.RedirectURI,
		})
	})
	mux.HandleFunc("GET /api/auth/result", handlePendingResult())
	mux.HandleFunc("POST /api/verify", handleVerify(cfg))
	mux.HandleFunc("POST /api/proxy", handleAPIProxy(cfg))
	mux.HandleFunc("GET /login", handleLogin(cfg))
	mux.HandleFunc("GET /oauth2/callback", handleCallback(cfg))
	mux.HandleFunc("POST /oauth2/code", handleManualCode(cfg))
	mux.HandleFunc("POST /oauth2/otp", handleOTP(cfg))
	mux.HandleFunc("POST /oauth2/refresh", handleRefresh(cfg))

	if cfg.DevProxyURL != "" {
		target, parseErr := url.Parse(cfg.DevProxyURL)
		if parseErr != nil {
			log.Fatal(parseErr)
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		mux.Handle("/", proxy)
		return mux
	}

	distFS, err := fs.Sub(webDistFS, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	fileServer := http.FileServer(http.FS(distFS))
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, req *http.Request) {
		cleanPath := strings.TrimPrefix(path.Clean(req.URL.Path), "/")
		if cleanPath != "" {
			if _, statErr := fs.Stat(distFS, cleanPath); statErr == nil {
				fileServer.ServeHTTP(writer, req)
				return
			}
		}
		indexBytes, readErr := fs.ReadFile(distFS, "index.html")
		if readErr != nil {
			http.Error(writer, "frontend not built", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(indexBytes)
	})
	return mux
}

func isOOB(cfg Config) bool {
	return cfg.RedirectURI == "urn:ietf:wg:oauth:2.0:oob"
}

func handleLogin(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		state, err := randomState()
		if err != nil {
			respondAuthOutcome(writer, req, http.StatusInternalServerError, map[string]any{"error": "无法生成 state"})
			return
		}
		http.SetCookie(writer, &http.Cookie{
			Name:     stateCookieName,
			Value:    state,
			Path:     "/",
			MaxAge:   600,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		query := url.Values{}
		query.Set("client_id", cfg.ClientID)
		query.Set("redirect_uri", cfg.RedirectURI)
		query.Set("response_type", "code")
		query.Set("state", state)
		http.Redirect(writer, req, cfg.BaseURL+"/oauth/authorize?"+query.Encode(), http.StatusFound)
	}
}

func handleCallback(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		if errParam := req.URL.Query().Get("error"); errParam != "" {
			desc := req.URL.Query().Get("error_description")
			respondAuthOutcome(writer, req, http.StatusBadRequest, map[string]any{"error": strings.TrimSpace(errParam + " " + desc)})
			return
		}
		cookie, err := req.Cookie(stateCookieName)
		state := req.URL.Query().Get("state")
		if err != nil || state == "" || cookie.Value != state {
			respondAuthOutcome(writer, req, http.StatusBadRequest, map[string]any{"error": "state 校验失败，请重新登录"})
			return
		}
		http.SetCookie(writer, &http.Cookie{
			Name:     stateCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		})
		code := req.URL.Query().Get("code")
		if code == "" {
			respondAuthOutcome(writer, req, http.StatusBadRequest, map[string]any{"error": "缺少授权码 code"})
			return
		}
		finishCodeExchange(cfg, writer, req, code, "")
	}
}

func handleManualCode(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效表单"})
			return
		}
		code := extractCodeInput(req.FormValue("code"))
		if code == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "code 不能为空"})
			return
		}
		finishCodeExchange(cfg, writer, req, code, "")
	}
}

func extractCodeInput(raw string) string {
	trimmed := strings.Trim(strings.TrimSpace(raw), `"'`)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil {
		if code := strings.TrimSpace(parsed.Query().Get("code")); code != "" {
			return code
		}
	}
	if strings.Contains(trimmed, "code=") {
		if query, err := url.ParseQuery(strings.TrimPrefix(trimmed, "?")); err == nil {
			if code := strings.TrimSpace(query.Get("code")); code != "" {
				return code
			}
		}
	}
	return trimmed
}

func formatOAuthError(errCode, desc string) string {
	hints := map[string]string{
		"invalid_grant":       "授权码或 Refresh Token 无效或已过期，请重新登录",
		"invalid_client":      "应用 Client ID 或 Client Secret 配置有误",
		"invalid_request":     "请求参数有误，请检查回调地址与授权码",
		"unauthorized_client": "当前应用未获得该授权方式权限",
		"access_denied":       "用户取消了授权",
	}
	if hint, ok := hints[errCode]; ok {
		return hint + " (" + errCode + ")"
	}
	return strings.TrimSpace(errCode + " " + desc)
}

func handleOTP(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		if err := req.ParseForm(); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效表单"})
			return
		}
		code := strings.TrimSpace(req.FormValue("code"))
		captcha := strings.TrimSpace(req.FormValue("captcha"))
		if code == "" || captcha == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "code 和 captcha 不能为空"})
			return
		}
		finishCodeExchange(cfg, writer, req, code, captcha)
	}
}

func finishCodeExchange(cfg Config, writer http.ResponseWriter, req *http.Request, code, captcha string) {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("code", code)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", cfg.RedirectURI)
	if captcha != "" {
		form.Set("captcha", captcha)
	}

	tokenResp, err := postToken(cfg, form, "")
	if err != nil {
		respondAuthOutcome(writer, req, http.StatusBadGateway, map[string]any{"error": "获取 Token 失败: " + err.Error()})
		return
	}
	if tokenResp.Error == "otp_required" {
		respondAuthOutcome(writer, req, http.StatusOK, map[string]any{"otp_required": true, "code": code})
		return
	}
	if tokenResp.Error != "" {
		respondAuthOutcome(writer, req, http.StatusBadGateway, map[string]any{"error": formatOAuthError(tokenResp.Error, tokenResp.ErrorDescription)})
		return
	}

	result := LoginResult{Token: tokenResp}
	if user, userErr := fetchUser(cfg, tokenResp.AccessToken); userErr == nil {
		result.User = user
	}
	respondAuthOutcome(writer, req, http.StatusOK, result)
}

func respondAuthOutcome(writer http.ResponseWriter, req *http.Request, status int, payload any) {
	if req.Method == http.MethodPost || wantsJSON(req) {
		writeJSON(writer, status, payload)
		return
	}
	key, err := randomState()
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "无法保存登录状态"})
		return
	}
	pendingMu.Lock()
	now := time.Now()
	for existingKey, entry := range pendingResults {
		if now.After(entry.ExpiresAt) {
			delete(pendingResults, existingKey)
		}
	}
	pendingResults[key] = pendingEntry{Payload: payload, ExpiresAt: now.Add(5 * time.Minute)}
	pendingMu.Unlock()

	http.SetCookie(writer, &http.Cookie{
		Name:     resultCookieName,
		Value:    key,
		Path:     "/",
		MaxAge:   300,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(writer, req, "/", http.StatusFound)
}

func handlePendingResult() http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		cookie, err := req.Cookie(resultCookieName)
		if err != nil || cookie.Value == "" {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		http.SetCookie(writer, &http.Cookie{
			Name:     resultCookieName,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
		})
		pendingMu.Lock()
		entry, ok := pendingResults[cookie.Value]
		delete(pendingResults, cookie.Value)
		pendingMu.Unlock()
		if !ok || time.Now().After(entry.ExpiresAt) {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		writeJSON(writer, http.StatusOK, entry.Payload)
	}
}

func handleRefresh(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			RefreshToken string `json:"refresh_token"`
			AccessToken  string `json:"access_token"`
		}
		if strings.Contains(req.Header.Get("Content-Type"), "application/json") {
			if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
				writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		} else {
			_ = req.ParseForm()
			payload.RefreshToken = req.FormValue("refresh_token")
			payload.AccessToken = req.FormValue("access_token")
		}
		if strings.TrimSpace(payload.RefreshToken) == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "refresh_token is required"})
			return
		}
		form := url.Values{}
		form.Set("client_id", cfg.ClientID)
		form.Set("client_secret", cfg.ClientSecret)
		form.Set("grant_type", "refresh_token")
		form.Set("redirect_uri", cfg.RedirectURI)
		form.Set("refresh_token", payload.RefreshToken)

		tokenResp, err := postToken(cfg, form, payload.AccessToken)
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, tokenResp)
	}
}

func handleVerify(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"valid": false, "error": "无效 JSON 请求体"})
			return
		}
		token := strings.TrimSpace(payload.AccessToken)
		if token == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"valid": false, "error": "access_token 不能为空"})
			return
		}
		user, err := fetchUser(cfg, token)
		if err != nil {
			writeJSON(writer, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"valid": true, "user": user})
	}
}

func handleAPIProxy(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var payload struct {
			Method      string `json:"method"`
			Path        string `json:"path"`
			Query       string `json:"query"`
			Body        string `json:"body"`
			AccessToken string `json:"access_token"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		method := strings.ToUpper(strings.TrimSpace(payload.Method))
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		default:
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "不支持的 HTTP 方法"})
			return
		}
		apiPath := strings.TrimSpace(payload.Path)
		if !strings.HasPrefix(apiPath, "/api/v1/") {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "仅允许代理 /api/v1/ 路径"})
			return
		}
		token := strings.TrimSpace(payload.AccessToken)
		if token == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "access_token 不能为空"})
			return
		}

		targetURL := cfg.BaseURL + apiPath
		if q := strings.TrimPrefix(strings.TrimSpace(payload.Query), "?"); q != "" {
			targetURL += "?" + q
		}

		var bodyReader io.Reader
		if payload.Body != "" && method != http.MethodGet {
			bodyReader = strings.NewReader(payload.Body)
		}
		outReq, err := http.NewRequestWithContext(req.Context(), method, targetURL, bodyReader)
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效请求地址: " + err.Error()})
			return
		}
		outReq.Header.Set("Authorization", "Bearer "+token)
		outReq.Header.Set("Accept", "application/json")
		if bodyReader != nil {
			outReq.Header.Set("Content-Type", "application/json")
		}

		started := time.Now()
		resp, err := cfg.HTTPClient.Do(outReq)
		durationMs := time.Since(started).Milliseconds()
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "请求 Tower API 失败: " + err.Error()})
			return
		}
		defer resp.Body.Close()

		respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "读取响应失败: " + err.Error()})
			return
		}

		var parsed any
		if json.Unmarshal(respBytes, &parsed) != nil {
			parsed = string(respBytes)
		}
		writeJSON(writer, http.StatusOK, map[string]any{
			"status":      resp.StatusCode,
			"status_text": resp.Status,
			"duration_ms": durationMs,
			"body":        parsed,
		})
	}
}

func postToken(cfg Config, form url.Values, bearerToken string) (TokenResponse, error) {
	var tokenResp TokenResponse
	httpReq, err := http.NewRequest(http.MethodPost, cfg.BaseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResp, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if bearerToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+bearerToken)
	}
	resp, err := cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return tokenResp, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return tokenResp, err
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return tokenResp, err
	}
	if resp.StatusCode >= 400 && tokenResp.Error == "" {
		return tokenResp, errors.New(resp.Status)
	}
	return tokenResp, nil
}

func fetchUser(cfg Config, accessToken string) (*UserInfo, error) {
	httpReq, err := http.NewRequest(http.MethodGet, cfg.BaseURL+"/api/v1/user", nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	var envelope struct {
		Data struct {
			ID         string   `json:"id"`
			Attributes UserInfo `json:"attributes"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, err
	}
	user := envelope.Data.Attributes
	user.ID = envelope.Data.ID
	return &user, nil
}

func wantsJSON(req *http.Request) bool {
	return req.URL.Query().Get("format") == "json" || strings.Contains(req.Header.Get("Accept"), "application/json")
}

func writeJSON(writer http.ResponseWriter, status int, val any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(val)
}

func main() {
	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Tower OAuth server listening on %s (redirect_uri=%s)", cfg.ListenAddr, cfg.RedirectURI)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newMux(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
