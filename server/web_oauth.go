package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type webGrant struct {
	owner       int64
	rebindID    string
	client      *webClient
	state, code string
	consent     url.Values
}

type webGrantChallenge struct {
	RebindID    string
	State, Code string
	Consent     url.Values
	Cookies     []webCookie
	Email       string
}

// Keep the session and authorization code server-side while waiting for user input.
func (g *webGrant) challenge(cfg Config, status string, message string) (map[string]any, error) {
	id, err := randomState()
	if err != nil {
		return nil, err
	}
	snapshot := webGrantChallenge{
		RebindID: g.rebindID, State: g.state, Code: g.code, Consent: g.consent,
		Cookies: g.client.jar.export(), Email: g.client.email,
	}
	if err := cfg.Store.putAuthState(authStateGrant, id, g.owner, snapshot, 5*time.Minute); err != nil {
		return nil, errors.New("无法保存授权状态")
	}
	return map[string]any{"status": status, "challenge": id, "message": message}, nil
}

func handleGrantContinue(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Challenge, Captcha string
			Approve            bool
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil {
			writeJSON(w, 400, map[string]any{"error": "无效授权请求"})
			return
		}
		owner := userFromContext(r.Context()).ID
		var snapshot webGrantChallenge
		err := cfg.Store.takeAuthState(authStateGrant, input.Challenge, owner, &snapshot)
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, 409, map[string]any{"error": "授权已失效，请重新登录"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "无法读取授权状态"})
			return
		}
		client, err := newWebClient(cfg.BaseURL, snapshot.Cookies)
		if err != nil {
			writeGrant(w, nil, err)
			return
		}
		client.email = snapshot.Email
		g := webGrant{owner: owner, rebindID: snapshot.RebindID, client: client,
			state: snapshot.State, code: snapshot.Code, consent: snapshot.Consent}
		var result map[string]any
		if g.consent != nil {
			if !input.Approve {
				err = errors.New("已取消授权")
			} else {
				result, err = g.authorize(r.Context(), cfg)
			}
		} else if strings.TrimSpace(input.Captcha) == "" {
			err = errors.New("缺少验证码")
		} else {
			result, err = g.exchange(cfg, input.Captcha)
		}
		writeGrant(w, result, err)
	}
}

func writeGrant(w http.ResponseWriter, result map[string]any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (g *webGrant) authorize(ctx context.Context, cfg Config) (map[string]any, error) {
	endpoint := strings.TrimRight(cfg.BaseURL, "/") + "/oauth/authorize"
	method, target, body := http.MethodGet, endpoint, ""
	if g.consent == nil {
		state, err := randomState()
		if err != nil {
			return nil, err
		}
		g.state = state
		target += "?" + url.Values{"client_id": {cfg.ClientID}, "redirect_uri": {cfg.RedirectURI}, "response_type": {"code"}, "state": {state}}.Encode()
	} else {
		method = http.MethodPost
		body = g.consent.Encode()
		g.consent = nil
	}
	client := *g.client.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for i := 0; i < 6; i++ {
		req, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
		if err != nil {
			return nil, err
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, errors.New("无法连接 Tower 授权页")
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc, err := resp.Location()
			resp.Body.Close()
			if err != nil {
				return nil, errors.New("Tower 授权跳转无效")
			}
			callback, _ := url.Parse(cfg.RedirectURI)
			if !isOOB(cfg) && callback != nil && loc.Scheme == callback.Scheme && loc.Host == callback.Host && loc.Path == callback.Path {
				if loc.Query().Get("state") != g.state || loc.Query().Get("code") == "" {
					return nil, errors.New("Tower 授权回调校验失败")
				}
				g.code = loc.Query().Get("code")
				return g.exchange(cfg, "")
			}
			if loc.Scheme != req.URL.Scheme || loc.Host != req.URL.Host || !strings.HasPrefix(loc.Path, "/oauth/") {
				return nil, errors.New("Tower 未完成 OAuth 授权")
			}
			target, method, body = loc.String(), http.MethodGet, ""
			continue
		}
		doc, parseErr := html.Parse(io.LimitReader(resp.Body, 2<<20))
		resp.Body.Close()
		if parseErr != nil || resp.StatusCode != 200 {
			return nil, errors.New("读取 Tower 授权页面失败")
		}
		for n := range doc.Descendants() {
			if n.Type != html.ElementNode {
				continue
			}
			if isOOB(cfg) && n.Data == "code" && attr(n, "id") == "authorization_code" && n.FirstChild != nil {
				g.code = strings.TrimSpace(n.FirstChild.Data)
				return g.exchange(cfg, "")
			}
			if n.Data != "form" || !strings.EqualFold(attr(n, "method"), "post") {
				continue
			}
			action, err := req.URL.Parse(attr(n, "action"))
			if err != nil || strings.TrimSuffix(action.String(), "/") != endpoint {
				continue
			}
			fields := url.Values{}
			for input := range n.Descendants() {
				if input.Data == "input" && attr(input, "type") == "hidden" && attr(input, "name") != "" {
					fields.Set(attr(input, "name"), attr(input, "value"))
				}
			}
			if fields.Get("_method") != "" || fields.Get("client_id") != cfg.ClientID || fields.Get("redirect_uri") != cfg.RedirectURI || fields.Get("state") != g.state {
				continue
			}
			g.consent = fields
			message := "允许 AgentsTower 使用此 Tower 账号调用 API？"
			if scope := fields.Get("scope"); scope != "" {
				message += " 授权范围：" + scope
			}
			return g.challenge(cfg, "consent", message)
		}
		return nil, errors.New("Tower 授权页未提供授权码或确认表单")
	}
	return nil, errors.New("Tower 授权跳转次数过多")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func (g *webGrant) exchange(cfg Config, captcha string) (map[string]any, error) {
	form := url.Values{"client_id": {cfg.ClientID}, "client_secret": {cfg.ClientSecret}, "code": {g.code}, "grant_type": {"authorization_code"}, "redirect_uri": {cfg.RedirectURI}}
	if captcha != "" {
		form.Set("captcha", captcha)
	}
	token, err := postToken(cfg, form, "")
	if err != nil {
		return nil, errors.New("Tower Token 交换失败")
	}
	if token.Error == "otp_required" {
		return g.challenge(cfg, "otp", "请输入 Tower 发送的验证码")
	}
	if token.Error != "" {
		return nil, errors.New(formatOAuthError(token.Error, token.ErrorDescription))
	}
	if token.AccessToken == "" {
		return nil, errors.New("Tower 未返回 API Token")
	}
	user, err := fetchUser(cfg, token.AccessToken)
	if err != nil {
		return nil, errors.New("无法确认 Tower API 身份")
	}
	email := user.Email
	if email == "" {
		email = token.Email
	}
	if user.ID == "" || email == "" || !strings.EqualFold(email, g.client.email) {
		return nil, errors.New("网页与 API 账号身份不一致")
	}
	acc, err := cfg.Store.saveLogin(g.owner, user, token, &webSessionRecord{Cookies: g.client.jar.export(), Email: email}, g.rebindID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "connected", "web_connected": true, "web_email": email, "account_id": acc.ID}, nil
}
