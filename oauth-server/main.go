package main

import (
	"bufio"
	"bytes"
	"cmp"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
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

//go:embed SKILL.md
var embeddedSkillMD string

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
	SiteBaseURL  string
	MCPBaseURL   string
	ListenAddr   string
	DBPath       string
	DevProxyURL  string
	HTTPClient   *http.Client
	Store        *Store
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
	AccountID    string        `json:"account_id,omitempty"`
	User         *UserInfo     `json:"user,omitempty"`
	Token        TokenResponse `json:"token"`
	MCPAPIKey    string        `json:"mcp_api_key,omitempty"`
	WebConnected bool          `json:"web_connected,omitempty"`
	WebEmail     string        `json:"web_email,omitempty"`
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
		SiteBaseURL:  strings.TrimSpace(os.Getenv("SITE_BASE_URL")),
		MCPBaseURL:   strings.TrimSpace(os.Getenv("MCP_BASE_URL")),
		ListenAddr:   strings.TrimSpace(os.Getenv("LISTEN_ADDR")),
		DBPath:       strings.TrimSpace(os.Getenv("DB_PATH")),
		DevProxyURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("VITE_DEV_URL")), "/"),
		HTTPClient:   &http.Client{Timeout: 15 * time.Second},
	}
	for _, setting := range []struct {
		name  string
		value *string
	}{
		{"SITE_BASE_URL", &cfg.SiteBaseURL},
		{"MCP_BASE_URL", &cfg.MCPBaseURL},
	} {
		if *setting.value == "" {
			continue
		}
		u, err := url.Parse(*setting.value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			u.User != nil || u.ForceQuery || u.RawQuery != "" || strings.Contains(*setting.value, "#") {
			return cfg, errors.New(setting.name + " must be a public HTTP(S) base URL")
		}
		if setting.name == "SITE_BASE_URL" && strings.TrimRight(u.EscapedPath(), "/") != "" {
			return cfg, errors.New("SITE_BASE_URL must be a site origin without a path")
		}
		*setting.value = strings.TrimRight(u.String(), "/")
	}
	if cfg.RedirectURI == "" {
		cfg.RedirectURI = cmp.Or(cfg.SiteBaseURL, "http://localhost:8888") + "/oauth2/callback"
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://tower.im"
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "[::]:8888"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = "data/agents-tower.db"
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

func requestBaseURL(req *http.Request) string {
	scheme := "http"
	if req.TLS != nil || strings.EqualFold(req.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := cmp.Or(strings.TrimSpace(req.Host), "localhost:8888")
	return (&url.URL{Scheme: scheme, Host: host}).String()
}

func serveSiteIndex(cfg Config, file, contentType string) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		content, err := webDistFS.ReadFile("web/dist/" + file)
		if err != nil {
			http.Error(writer, "site index unavailable", http.StatusInternalServerError)
			return
		}
		site := cfg.SiteBaseURL
		if site == "" {
			site = requestBaseURL(req)
		}
		if file == "sitemap.xml" {
			var escaped bytes.Buffer
			_ = xml.EscapeText(&escaped, []byte(site))
			site = escaped.String()
		}
		writer.Header().Set("Content-Type", contentType+"; charset=utf-8")
		_, _ = writer.Write(bytes.ReplaceAll(content, []byte("https://agents-tower.invalid"), []byte(site)))
	}
}

func newMux(cfg Config) http.Handler {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 15 * time.Second}
	}
	if cfg.Store == nil {
		memStore, err := OpenStore(":memory:")
		if err != nil {
			log.Fatal(err)
		}
		cfg.Store = memStore
	}
	mcpBaseURL := cmp.Or(cfg.MCPBaseURL, cfg.SiteBaseURL)
	mcpURL := mcpBaseURL + "/mcp"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sitemap.xml", serveSiteIndex(cfg, "sitemap.xml", "application/xml"))
	mux.HandleFunc("GET /llms.txt", serveSiteIndex(cfg, "llms.txt", "text/plain"))
	mux.HandleFunc("GET /api/healthz", func(writer http.ResponseWriter, req *http.Request) {
		writeJSON(writer, http.StatusOK, map[string]string{"status": "ok", "service": "AgentsTower"})
	})
	mux.HandleFunc("GET /api/config", func(writer http.ResponseWriter, req *http.Request) {
		settings := cfg.Store.GetSettings()
		writeJSON(writer, http.StatusOK, map[string]any{
			"service":           "AgentsTower",
			"is_oob":            isOOB(cfg),
			"redirect_uri":      cfg.RedirectURI,
			"mcp_url":           mcpURL,
			"mcp_tools":         totalMCPToolCount(),
			"registration_mode": settings.RegistrationMode,
			"allow_mcp_delete":  settings.AllowMCPDelete,
		})
	})
	mux.HandleFunc("GET /api/skill.md", func(writer http.ResponseWriter, req *http.Request) {
		skillMCPURL := mcpURL
		if mcpBaseURL == "" {
			skillMCPURL = requestBaseURL(req) + mcpURL
		}
		content := strings.ReplaceAll(embeddedSkillMD, "{{MCP_URL}}", skillMCPURL)
		writer.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		if req.URL.Query().Get("download") == "1" {
			writer.Header().Set("Content-Disposition", `attachment; filename="SKILL.md"`)
		}
		_, _ = io.WriteString(writer, content)
	})
	mux.HandleFunc("GET /api/auth/me", handleAuthMe(cfg))
	mux.HandleFunc("POST /api/auth/register", handleRegister(cfg))
	mux.HandleFunc("POST /api/auth/login", handleAuthLogin(cfg))
	mux.HandleFunc("POST /api/auth/quick-login", handleQuickLogin(cfg))
	mux.HandleFunc("POST /api/auth/logout", handleAuthLogout(cfg))
	mux.HandleFunc("POST /api/auth/password", requireAuth(cfg, handleChangePassword(cfg)))
	mux.HandleFunc("POST /api/auth/logout-all", requireAuth(cfg, handleLogoutAll(cfg)))

	mux.HandleFunc("GET /api/auth/result", requireAuth(cfg, handlePendingResult()))
	mux.HandleFunc("GET /api/accounts", requireAuth(cfg, func(writer http.ResponseWriter, req *http.Request) {
		u := userFromContext(req.Context())
		writeJSON(writer, http.StatusOK, map[string]any{"accounts": userLoginResults(cfg, u.ID)})
	}))
	mux.HandleFunc("POST /api/accounts/sync", requireAuth(cfg, handleSyncAccount(cfg)))
	mux.HandleFunc("POST /api/accounts/web/email", requireAuth(cfg, handleWebEmailLogin(cfg)))
	mux.HandleFunc("POST /api/accounts/web/wechat", requireAuth(cfg, handleWebWechatStart(cfg)))
	mux.HandleFunc("GET /api/accounts/web/wechat/{id}", requireAuth(cfg, handleWebWechatPoll(cfg)))
	mux.HandleFunc("POST /api/mcp-key/rotate", requireAuth(cfg, handleRotateMCPKey(cfg)))
	mux.HandleFunc("DELETE /api/account", requireAuth(cfg, handleDeleteAccount(cfg)))
	mux.HandleFunc("POST /api/verify", requireAuth(cfg, handleVerify(cfg)))
	mux.HandleFunc("POST /api/proxy", requireAuth(cfg, handleAPIProxy(cfg)))

	mux.HandleFunc("GET /api/admin/users", requireAdmin(cfg, handleAdminListUsers(cfg)))
	mux.HandleFunc("POST /api/admin/users/{id}/role", requireAdmin(cfg, handleAdminSetRole(cfg)))
	mux.HandleFunc("POST /api/admin/users/{id}/password", requireAdmin(cfg, handleAdminResetPassword(cfg)))
	mux.HandleFunc("DELETE /api/admin/users/{id}", requireAdmin(cfg, handleAdminDeleteUser(cfg)))
	mux.HandleFunc("GET /api/admin/settings", requireAdmin(cfg, handleAdminGetSettings(cfg)))
	mux.HandleFunc("POST /api/admin/settings", requireAdmin(cfg, handleAdminUpdateSettings(cfg)))

	mcpHandler := handleMCP(cfg)
	mux.HandleFunc("POST /mcp", mcpHandler)
	mux.HandleFunc("GET /mcp", mcpHandler)
	mux.HandleFunc("DELETE /mcp", mcpHandler)
	mux.HandleFunc("OPTIONS /mcp", mcpHandler)
	mux.HandleFunc("GET /login", requireAuth(cfg, handleLogin(cfg)))
	mux.HandleFunc("GET /oauth2/callback", requireAuth(cfg, handleCallback(cfg)))
	mux.HandleFunc("POST /oauth2/code", requireAuth(cfg, handleManualCode(cfg)))
	mux.HandleFunc("POST /oauth2/otp", requireAuth(cfg, handleOTP(cfg)))
	mux.HandleFunc("POST /oauth2/refresh", requireAuth(cfg, handleRefresh(cfg)))

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
	if cfg.Store != nil {
		var ownerID int64
		if authUser := userFromContext(req.Context()); authUser != nil {
			ownerID = authUser.ID
		}
		if stored, storeErr := cfg.Store.SaveLoginForUser(ownerID, result.User, tokenResp); storeErr == nil {
			result = stored.ToLoginResult()
		} else {
			log.Printf("failed to save account to sqlite: %v", storeErr)
		}
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

func refreshTokenForm(cfg Config, refreshToken string) url.Values {
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("grant_type", "refresh_token")
	form.Set("redirect_uri", cfg.RedirectURI)
	form.Set("refresh_token", refreshToken)
	return form
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
		form := refreshTokenForm(cfg, payload.RefreshToken)

		tokenResp, err := postToken(cfg, form, payload.AccessToken)
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		if cfg.Store != nil && tokenResp.AccessToken != "" {
			if _, updErr := cfg.Store.UpdateTokensByRefresh(payload.RefreshToken, tokenResp); updErr != nil {
				if existing, getErr := cfg.Store.GetByAccessToken(payload.AccessToken); getErr == nil {
					_, _ = cfg.Store.UpdateTokens(existing.ID, tokenResp)
				}
			}
		}
		writeJSON(writer, http.StatusOK, tokenResp)
	}
}

func handleSyncAccount(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		authUser := userFromContext(req.Context())
		var ownerID int64
		if authUser != nil {
			ownerID = authUser.ID
		}
		var payload struct {
			Token        TokenResponse `json:"token"`
			AccessToken  string        `json:"access_token"`
			RefreshToken string        `json:"refresh_token"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		tok := payload.Token
		if tok.AccessToken == "" {
			tok.AccessToken = strings.TrimSpace(payload.AccessToken)
		}
		if tok.RefreshToken == "" {
			tok.RefreshToken = strings.TrimSpace(payload.RefreshToken)
		}
		if tok.AccessToken == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "access_token 不能为空"})
			return
		}
		user, err := fetchUser(cfg, tok.AccessToken)
		if err != nil {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{"error": "Tower Access Token 无效或已过期: " + err.Error()})
			return
		}
		stored, err := cfg.Store.SaveLoginForUser(ownerID, user, tok)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "保存凭据失败: " + err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, stored.ToLoginResult())
	}
}

func extractCredentialFromReq(req *http.Request) string {
	if headerKey := extractMCPKey(req); headerKey != "" {
		return headerKey
	}
	var payload struct {
		Credential  string `json:"credential"`
		MCPAPIKey   string `json:"mcp_api_key"`
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload)
	for _, candidate := range []string{payload.Credential, payload.MCPAPIKey, payload.AccessToken} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func handleRotateMCPKey(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var ownerID int64
		if authUser := userFromContext(req.Context()); authUser != nil {
			ownerID = authUser.ID
		}
		cred := extractCredentialFromReq(req)
		if cred == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "缺少凭据参数"})
			return
		}
		updated, err := cfg.Store.RotateMCPKeyForUser(ownerID, cred)
		if errors.Is(err, ErrAccountNotFound) {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "未找到对应服务端账号，请先同步或重新登录"})
			return
		}
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "重置密钥失败: " + err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"mcp_api_key": updated.MCPAPIKey})
	}
}

func handleDeleteAccount(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		var ownerID int64
		if authUser := userFromContext(req.Context()); authUser != nil {
			ownerID = authUser.ID
		}
		cred := extractCredentialFromReq(req)
		if cred == "" {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "缺少凭据参数"})
			return
		}
		if err := cfg.Store.DeleteByAuthForUser(ownerID, cred); err != nil && !errors.Is(err, ErrAccountNotFound) {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": "删除服务端凭据失败: " + err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"deleted": true})
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
		if strings.HasPrefix(token, "at_sk-") && cfg.Store != nil {
			if acc, err := cfg.Store.EnsureFreshToken(cfg, token, false); err == nil {
				token = acc.AccessToken
			}
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
		if strings.HasPrefix(token, "at_sk-") && cfg.Store != nil {
			if acc, err := cfg.Store.EnsureFreshToken(cfg, token, false); err == nil {
				token = acc.AccessToken
			}
		}

		rawQuery := strings.TrimPrefix(strings.TrimSpace(payload.Query), "?")
		if method == http.MethodGet && apiPath == "/api/v1/todos/search" {
			started := time.Now()
			qVals, _ := url.ParseQuery(rawQuery)
			limit, _ := strconv.Atoi(qVals.Get("limit"))
			var statusCode int
			var result map[string]any
			var acc *StoredAccount
			var accErr error
			if cfg.Store == nil {
				accErr = ErrAccountNotFound
			} else {
				acc, accErr = cfg.Store.GetByAuth(strings.TrimSpace(payload.AccessToken))
			}
			if accErr != nil {
				statusCode, result = http.StatusNotFound, map[string]any{"error": "找不到对应 Tower 账号"}
			} else {
				payloadResult, searchErr := runWebSearch(req.Context(), cfg, acc, webSearchParams{
					Keyword: qVals.Get("keyword"), TeamID: qVals.Get("team_id"), ProjectID: qVals.Get("project_id"),
					MemberID: qVals.Get("member_id"), Status: qVals.Get("status"), Limit: limit,
				})
				if searchErr != nil {
					statusCode = http.StatusBadGateway
					if errors.Is(searchErr, errNoWebSession) {
						statusCode = http.StatusConflict
					}
					result = map[string]any{"error": searchErr.Error()}
				} else {
					statusCode, result = http.StatusOK, payloadResult
				}
			}
			writeJSON(writer, http.StatusOK, map[string]any{
				"status":      statusCode,
				"status_text": fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
				"duration_ms": time.Since(started).Milliseconds(),
				"body":        result,
			})
			return
		}
		if teamWriteDenied(req.Context(), cfg, token, method, apiPath) {
			writeJSON(writer, http.StatusForbidden, map[string]any{"error": renameDenied})
			return
		}
		normalizedQuery, maxItems := normalizeListQuery(method, apiPath, rawQuery)
		targetURL := cfg.BaseURL + apiPath
		if normalizedQuery != "" {
			targetURL += "?" + normalizedQuery
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
		if method == http.MethodDelete && !isSafeTodoStateDelete(outReq.URL.Path) {
			writeJSON(writer, http.StatusForbidden, map[string]any{"error": "此删除接口仅供查看，调试工具已禁用请求发送"})
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

		respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "读取响应失败: " + err.Error()})
			return
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && maxItems > 0 {
			respBytes = capJSONListBytes(respBytes, 1, maxItems, false)
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
	if len(os.Args) > 1 {
		loadDotEnv(".env")
		dbPath := strings.TrimSpace(os.Getenv("DB_PATH"))
		if dbPath == "" {
			dbPath = "data/agents-tower.db"
		}
		if err := runCLI(os.Args[1:], dbPath, os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}

	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Fatal(err)
	}
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("open sqlite store (%s): %v", cfg.DBPath, err)
	}
	defer store.Close()
	cfg.Store = store

	log.Printf("AgentsTower server listening on %s (redirect_uri=%s, db=%s)", cfg.ListenAddr, cfg.RedirectURI, cfg.DBPath)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           newMux(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
