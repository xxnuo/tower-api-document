package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// shortcut: 微信扫码依赖 open.weixin.qq.com 的 uuid 轮询，页面改版后要更新解析。
// shortcut: 搜索和所属项目解析 Tower 网页 HTML，改版后要更新解析。

const towerWebUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36"

var (
	errNoWebSession    = errors.New("该 Tower 账号尚未连接网页登录")
	guidPattern        = regexp.MustCompile(`^[a-f0-9]{32}$`)
	csrfPattern        = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)
	emailPattern       = regexp.MustCompile(`id="user-email" value="([^"]*)"`)
	hiddenPattern      = regexp.MustCompile(`id="([^"]+)" value="([^"]*)"`)
	teamHrefPattern    = regexp.MustCompile(`href="/teams/([a-f0-9]{32})"`)
	searchItemPattern  = regexp.MustCompile(`(?s)<li data-category="([^"]*)">(.*?)</li>`)
	searchLinkPattern  = regexp.MustCompile(`(?s)<a[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
	completedPattern   = regexp.MustCompile(`data-completed="(true|false)"`)
	resultTimePattern  = regexp.MustCompile(`class="result-time" title="([^"]*)"`)
	resultCountPattern = regexp.MustCompile(`class="result-count">\s*(\d+)`)
	todoIDPattern      = regexp.MustCompile(`/todos/([a-f0-9]{32})`)
	tagPattern         = regexp.MustCompile(`(?s)<[^>]+>`)
	createPathPattern  = regexp.MustCompile(`create-path="([^"]+)"`)
	newLinkPattern     = regexp.MustCompile(`(?s)section_links/new.*?</select>`)
	optionPattern      = regexp.MustCompile(`<option([^>]*)>([^<]*)</option>`)
	valuePattern       = regexp.MustCompile(`value="([^"]*)"`)
	sectionItemPattern = regexp.MustCompile(`(?s)<tr-todo-section-links-field-detail-item\b([^>]*)>(.*?)</tr-todo-section-links-field-detail-item>`)
	sectionURLPattern  = regexp.MustCompile(`data-url="([^"]*section_links/\d+[^"]*)"`)
	projectHrefPattern = regexp.MustCompile(`/projects/([a-f0-9]{32})`)
	wxAppPattern       = regexp.MustCompile(`appid:\s*'([^']+)'`)
	wxStatePattern     = regexp.MustCompile(`state:\s*'([^']*)'`)
	wxRedirectPattern  = regexp.MustCompile(`redirect_uri:\s*encodeURIComponent\('([^']+)'\)`)
	wxUUIDPattern      = regexp.MustCompile(`uuid\s*[:=]\s*['"]([^'"]+)['"]`)
	wxImagePattern     = regexp.MustCompile(`src="([^"]*qrcode[^"]*)"`)
	wxCodePattern      = regexp.MustCompile(`wx_errcode=(\d+);window\.wx_code='([^']*)'`)
)

type webCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Expires  int64  `json:"expires,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"http_only,omitempty"`
}

type webSessionRecord struct {
	Cookies    []webCookie
	Email      string
	MemberGUID string
}

type memoryJar struct {
	jar   *cookiejar.Jar
	mu    sync.Mutex
	saved map[string]*http.Cookie
}

func newMemoryJar() (*memoryJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &memoryJar{jar: jar, saved: map[string]*http.Cookie{}}, nil
}

func (jar *memoryJar) SetCookies(rawURL *url.URL, cookies []*http.Cookie) {
	jar.jar.SetCookies(rawURL, cookies)
	jar.mu.Lock()
	defer jar.mu.Unlock()
	for _, cookie := range cookies {
		copied := *cookie
		if copied.Domain == "" {
			copied.Domain = rawURL.Hostname()
		}
		if copied.Path == "" {
			copied.Path = "/"
		}
		key := copied.Name + "|" + copied.Domain + "|" + copied.Path
		if copied.MaxAge < 0 {
			delete(jar.saved, key)
			continue
		}
		jar.saved[key] = &copied
	}
}

func (jar *memoryJar) Cookies(rawURL *url.URL) []*http.Cookie {
	return jar.jar.Cookies(rawURL)
}

func (jar *memoryJar) export() []webCookie {
	jar.mu.Lock()
	defer jar.mu.Unlock()
	out := make([]webCookie, 0, len(jar.saved))
	for _, cookie := range jar.saved {
		item := webCookie{
			Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path,
			Secure: cookie.Secure, HTTPOnly: cookie.HttpOnly,
		}
		if !cookie.Expires.IsZero() {
			item.Expires = cookie.Expires.Unix()
		}
		out = append(out, item)
	}
	return out
}

type webClient struct {
	base      string
	http      *http.Client
	jar       *memoryJar
	csrf      string
	accountID string
	email     string
	member    string
	store     *Store
}

func newWebClient(base string, cookies []webCookie) (*webClient, error) {
	jar, err := newMemoryJar()
	if err != nil {
		return nil, err
	}
	client := &webClient{
		base: strings.TrimRight(base, "/"),
		jar:  jar,
		http: &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}
	if len(cookies) == 0 {
		return client, nil
	}
	parsed, err := url.Parse(client.base + "/")
	if err != nil {
		return nil, err
	}
	restored := make([]*http.Cookie, 0, len(cookies))
	for _, item := range cookies {
		if item.Name == "" {
			continue
		}
		cookie := &http.Cookie{
			Name: item.Name, Value: item.Value, Path: item.Path,
			Secure: item.Secure, HttpOnly: item.HTTPOnly,
		}
		if cookie.Path == "" {
			cookie.Path = "/"
		}
		if item.Expires > 0 {
			cookie.Expires = time.Unix(item.Expires, 0)
		}
		restored = append(restored, cookie)
	}
	jar.SetCookies(parsed, restored)
	return client, nil
}

func (client *webClient) save() error {
	if client.store == nil || client.accountID == "" {
		return nil
	}
	return client.store.SaveWebSession(client.accountID, client.jar.export(), client.email, client.member)
}

func (client *webClient) request(ctx context.Context, method, rawURL string, body string, contentType string) (int, string, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("User-Agent", towerWebUA)
	req.Header.Set("Accept", "text/html,application/json,text/javascript,*/*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if client.csrf != "" && method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", client.csrf)
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 3<<20))
	if err != nil {
		return resp.StatusCode, "", err
	}
	text := string(payload)
	if token := firstMatch(csrfPattern, text); token != "" {
		client.csrf = html.UnescapeString(token)
	}
	return resp.StatusCode, text, nil
}

func (client *webClient) get(ctx context.Context, path string) (string, error) {
	status, text, err := client.request(ctx, http.MethodGet, client.resolve(path), "", "")
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("网页请求失败 HTTP %d", status)
	}
	return text, nil
}

func (client *webClient) resolve(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return client.base + path
}

func (client *webClient) loginWithEmail(ctx context.Context, email, password string) (string, error) {
	page, err := client.get(ctx, "/users/account_sign_in")
	if err != nil {
		return "", err
	}
	if client.csrf == "" {
		client.csrf = html.UnescapeString(firstMatch(csrfPattern, page))
	}
	if client.csrf == "" {
		return "", errors.New("登录页没有 CSRF 令牌")
	}
	form := url.Values{
		"authenticity_token": {client.csrf},
		"email":              {email},
		"password":           {password},
		"remember_me":        {"1"},
	}
	status, text, err := client.request(ctx, http.MethodPost, client.base+"/users/account_sign_in", form.Encode(), "application/x-www-form-urlencoded; charset=UTF-8")
	if err != nil {
		return "", err
	}
	if status >= 400 || !strings.Contains(text, `"success":true`) {
		return "", errors.New("邮箱登录失败")
	}
	identity, err := client.get(ctx, "/launchpad/")
	if err != nil {
		return "", err
	}
	webEmail := html.UnescapeString(firstMatch(emailPattern, identity))
	if webEmail == "" {
		return "", errors.New("登录后没有读到网页账号邮箱")
	}
	return webEmail, nil
}

type webSearchParams struct {
	Keyword   string
	TeamID    string
	ProjectID string
	MemberID  string
	Status    string
	Limit     int
}

func (client *webClient) searchTodos(ctx context.Context, params webSearchParams) (map[string]any, error) {
	keyword := strings.TrimSpace(params.Keyword)
	if keyword == "" {
		return nil, errors.New("keyword 不能为空")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultListPageSize
	} else if limit > maxListPageSize {
		limit = maxListPageSize
	}
	teams, err := client.teamGUIDs(ctx, params.TeamID)
	if err != nil {
		return nil, err
	}
	status := normalizeWebStatus(params.Status)
	items := make([]any, 0, limit)
	seen := map[string]struct{}{}
	for _, team := range teams {
		query := url.Values{
			"keyword":       {keyword},
			"category":      {"2"},
			"exclude_trash": {"true"},
		}
		if params.ProjectID != "" {
			query.Set("target_gid", params.ProjectID)
		}
		if params.MemberID != "" {
			query.Set("member_guid", params.MemberID)
		}
		for page := 1; page <= 3 && len(items) < limit; page++ {
			if page > 1 {
				query.Set("page", fmt.Sprint(page))
			}
			text, getErr := client.get(ctx, "/teams/"+team+"/search?"+query.Encode())
			if getErr != nil {
				return nil, getErr
			}
			added := 0
			for _, item := range parseSearchItems(text, status) {
				id := fmt.Sprint(item["id"])
				if _, ok := seen[id]; ok {
					continue
				}
				seen[id] = struct{}{}
				item["team"] = team
				items = append(items, item)
				added++
				if len(items) >= limit {
					break
				}
			}
			if added == 0 || !strings.Contains(text, "btn-load-more") {
				break
			}
		}
	}
	scope := params.TeamID
	if scope == "" {
		scope = "全部已加入团队"
	}
	return map[string]any{
		"keyword": keyword,
		"team":    scope,
		"status":  status,
		"count":   len(items),
		"items":   items,
	}, nil
}

func (client *webClient) teamGUIDs(ctx context.Context, teamID string) ([]string, error) {
	page, err := client.get(ctx, "/launchpad/")
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	guids := make([]string, 0)
	for _, match := range teamHrefPattern.FindAllStringSubmatch(page, -1) {
		if _, ok := seen[match[1]]; ok {
			continue
		}
		seen[match[1]] = struct{}{}
		guids = append(guids, match[1])
	}
	if teamID == "" {
		if len(guids) == 0 {
			return nil, errors.New("网页登录后没有可见团队")
		}
		return guids, nil
	}
	if guidPattern.MatchString(teamID) {
		return []string{teamID}, nil
	}
	hidden := hiddenValues(page)
	if hidden["team-id"] == teamID && guidPattern.MatchString(hidden["team-guid"]) {
		return []string{hidden["team-guid"]}, nil
	}
	// /teams/{guid} 会跳到个人待办并丢掉 team-id，项目页才带数字团队编号。
	for _, guid := range guids {
		teamPage, getErr := client.get(ctx, "/teams/"+guid+"/projects")
		if getErr != nil {
			continue
		}
		if hiddenValues(teamPage)["team-id"] == teamID {
			return []string{guid}, nil
		}
	}
	return nil, fmt.Errorf("找不到团队 %s", teamID)
}

type sectionLink struct {
	ID        string
	ProjectID string
	URL       string
}

func (client *webClient) addTodoProject(ctx context.Context, pagePath, todoID, projectID, todolistID string) (map[string]any, error) {
	if !guidPattern.MatchString(todoID) || !guidPattern.MatchString(projectID) {
		return nil, errors.New("todo_id 和 project_id 必须是 Tower GUID")
	}
	if todolistID != "" && !guidPattern.MatchString(todolistID) {
		return nil, errors.New("todolist_id 必须是 Tower GUID")
	}
	page, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	for _, link := range parseSectionLinks(page) {
		if link.ProjectID == projectID {
			return map[string]any{
				"todo_id": todoID, "project_id": projectID, "section_link_id": link.ID, "already": true,
			}, nil
		}
	}
	createPath := html.UnescapeString(firstMatch(createPathPattern, page))
	if createPath == "" {
		return nil, errors.New("任务页没有添加所属项目入口")
	}
	choices, err := client.todolists(ctx, projectID)
	if err != nil {
		return nil, err
	}
	chosen := choices[0]
	if todolistID != "" {
		found := false
		for _, choice := range choices {
			if choice.GUID == todolistID {
				chosen = choice
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("目标项目中没有这个清单")
		}
	} else {
		for _, choice := range choices {
			if choice.Default {
				chosen = choice
				break
			}
		}
	}
	form := url.Values{
		"authenticity_token": {client.csrf},
		"project_guid":       {projectID},
		"todolist_guid":      {chosen.GUID},
		"filter_id":          {""},
	}
	status, text, err := client.request(ctx, http.MethodPost, client.resolve(createPath), form.Encode(), "application/x-www-form-urlencoded; charset=UTF-8")
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("添加所属项目失败 HTTP %d", status)
	}
	confirmed, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	for _, link := range parseSectionLinks(confirmed) {
		if link.ProjectID == projectID {
			return map[string]any{
				"todo_id": todoID, "project_id": projectID, "todolist_id": chosen.GUID,
				"todolist": chosen.Label, "section_link_id": link.ID,
			}, nil
		}
	}
	snippet := strings.TrimSpace(tagPattern.ReplaceAllString(text, " "))
	if len(snippet) > 180 {
		snippet = snippet[:180]
	}
	if snippet == "" {
		snippet = "页面没有出现新的项目关联"
	}
	return nil, errors.New("添加所属项目后未确认成功: " + snippet)
}

func (client *webClient) removeTodoProject(ctx context.Context, pagePath, todoID, projectID string) (map[string]any, error) {
	if !guidPattern.MatchString(todoID) || !guidPattern.MatchString(projectID) {
		return nil, errors.New("todo_id 和 project_id 必须是 Tower GUID")
	}
	page, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	var target sectionLink
	for _, link := range parseSectionLinks(page) {
		if link.ProjectID == projectID {
			target = link
			break
		}
	}
	if target.URL == "" {
		return nil, errors.New("任务上没有这个所属项目")
	}
	status, _, err := client.request(ctx, http.MethodDelete, client.resolve(target.URL), "", "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, fmt.Errorf("移除所属项目失败 HTTP %d", status)
	}
	confirmed, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	for _, link := range parseSectionLinks(confirmed) {
		if link.ProjectID == projectID {
			return nil, errors.New("移除请求已返回，但项目关联仍在")
		}
	}
	return map[string]any{"todo_id": todoID, "project_id": projectID, "section_link_id": target.ID}, nil
}

type todolistChoice struct {
	Label   string `json:"label"`
	GUID    string `json:"guid"`
	Default bool   `json:"default"`
}

func (client *webClient) todolists(ctx context.Context, projectID string) ([]todolistChoice, error) {
	text, err := client.get(ctx, "/projects/"+projectID+"/lists/uncompleted/?with_default=true")
	if err != nil {
		return nil, err
	}
	var choices []todolistChoice
	if err := json.Unmarshal([]byte(text), &choices); err != nil || len(choices) == 0 {
		return nil, errors.New("没有读到目标项目的清单")
	}
	return choices, nil
}

func useWebClient(ctx context.Context, cfg Config, acc *StoredAccount) (*webClient, error) {
	if cfg.Store == nil || acc == nil {
		return nil, errNoWebSession
	}
	record, err := cfg.Store.GetWebSession(acc.ID)
	if err != nil {
		return nil, err
	}
	client, err := newWebClient(cfg.BaseURL, record.Cookies)
	if err != nil {
		return nil, err
	}
	client.accountID = acc.ID
	client.email = record.Email
	client.member = record.MemberGUID
	client.store = cfg.Store
	return client, nil
}

func executeSearchTodos(ctx context.Context, cfg Config, _ string, acc *StoredAccount, args map[string]any) (string, bool) {
	limit, _ := strconvAtoi(stringifyArg(args["limit"]))
	payload, err := runWebSearch(ctx, cfg, acc, webSearchParams{
		Keyword:   stringifyArg(args["keyword"]),
		TeamID:    stringifyArg(args["team_id"]),
		ProjectID: stringifyArg(args["project_id"]),
		MemberID:  stringifyArg(args["member_id"]),
		Status:    stringifyArg(args["status"]),
		Limit:     limit,
	})
	return webToolText("search_todos", payload, err)
}

func executeAddTodoProject(ctx context.Context, cfg Config, acc *StoredAccount, args map[string]any) (string, bool) {
	todoID := stringifyArg(args["todo_id"])
	pagePath, err := todoWebPath(ctx, cfg, acc, todoID)
	if err != nil {
		return webToolText("add_todo_project", nil, err)
	}
	client, err := useWebClient(ctx, cfg, acc)
	if err != nil {
		return webToolText("add_todo_project", nil, err)
	}
	defer client.save()
	payload, err := client.addTodoProject(ctx, pagePath, todoID, stringifyArg(args["project_id"]), stringifyArg(args["todolist_id"]))
	return webToolText("add_todo_project", payload, err)
}

func executeRemoveTodoProject(ctx context.Context, cfg Config, acc *StoredAccount, args map[string]any) (string, bool) {
	todoID := stringifyArg(args["todo_id"])
	pagePath, err := todoWebPath(ctx, cfg, acc, todoID)
	if err != nil {
		return webToolText("remove_todo_project", nil, err)
	}
	client, err := useWebClient(ctx, cfg, acc)
	if err != nil {
		return webToolText("remove_todo_project", nil, err)
	}
	defer client.save()
	payload, err := client.removeTodoProject(ctx, pagePath, todoID, stringifyArg(args["project_id"]))
	return webToolText("remove_todo_project", payload, err)
}

func runWebSearch(ctx context.Context, cfg Config, acc *StoredAccount, params webSearchParams) (map[string]any, error) {
	client, err := useWebClient(ctx, cfg, acc)
	if err != nil {
		return nil, err
	}
	defer client.save()
	return client.searchTodos(ctx, params)
}

func webToolText(tool string, payload map[string]any, err error) (string, bool) {
	if err != nil {
		raw, _ := json.Marshal(map[string]any{"error": err.Error()})
		return agentText(raw), true
	}
	raw, _ := json.Marshal(payload)
	return resultProfiles[tool].compositeText(raw), false
}

func strconvAtoi(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	var number int
	_, err := fmt.Sscan(raw, &number)
	return number, err
}

func normalizeWebStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "uncompleted", "open":
		return "uncompleted"
	case "completed", "done":
		return "completed"
	default:
		return "all"
	}
}

func parseSearchItems(page, status string) []map[string]any {
	matches := searchItemPattern.FindAllStringSubmatch(page, -1)
	items := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		link := searchLinkPattern.FindStringSubmatch(match[2])
		if link == nil {
			continue
		}
		completed := firstMatch(completedPattern, match[2]) == "true"
		if status == "completed" && !completed {
			continue
		}
		if status == "uncompleted" && completed {
			continue
		}
		item := map[string]any{
			"id":        firstMatch(todoIDPattern, link[1]),
			"title":     plainText(link[2]),
			"category":  match[1],
			"url":       html.UnescapeString(link[1]),
			"completed": completed,
		}
		if when := firstMatch(resultTimePattern, match[2]); when != "" {
			item["time"] = html.UnescapeString(when)
		}
		if item["id"] == "" {
			item["id"] = item["url"]
		}
		items = append(items, item)
	}
	return items
}

func parseSectionLinks(page string) []sectionLink {
	matches := sectionItemPattern.FindAllStringSubmatch(page, -1)
	links := make([]sectionLink, 0, len(matches))
	for _, match := range matches {
		rawURL := html.UnescapeString(firstMatch(sectionURLPattern, match[1]+match[2]))
		projectID := firstMatch(projectHrefPattern, match[2])
		id := ""
		if parts := strings.Split(strings.Split(rawURL, "?")[0], "/"); len(parts) > 0 {
			id = parts[len(parts)-1]
		}
		if rawURL == "" || projectID == "" {
			continue
		}
		links = append(links, sectionLink{ID: id, ProjectID: projectID, URL: rawURL})
	}
	return links
}

func hiddenValues(page string) map[string]string {
	out := map[string]string{}
	for _, match := range hiddenPattern.FindAllStringSubmatch(page, -1) {
		out[match[1]] = html.UnescapeString(match[2])
	}
	return out
}

func plainText(value string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagPattern.ReplaceAllString(value, " "))), " ")
}

func firstMatch(pattern *regexp.Regexp, text string) string {
	match := pattern.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	return match[1]
}

type wechatPending struct {
	mu        sync.Mutex
	userID    int64
	accountID string
	client    *webClient
	uuid      string
	redirect  string
	state     string
	expires   time.Time
	done      bool
}

var wechatPendingLogins sync.Map

func handleWebEmailLogin(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		user := userFromContext(req.Context())
		var payload struct {
			AccountID string `json:"account_id"`
			Email     string `json:"email"`
			Password  string `json:"password"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		acc, err := webAccountForUser(cfg, user.ID, payload.AccountID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "找不到这个 Tower 账号"})
			return
		}
		client, err := newWebClient(cfg.BaseURL, nil)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		webEmail, err := client.loginWithEmail(req.Context(), strings.TrimSpace(payload.Email), payload.Password)
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if acc.Email != "" && !strings.EqualFold(acc.Email, webEmail) {
			writeJSON(writer, http.StatusConflict, map[string]any{"error": "网页登录邮箱与该 Tower 账号不一致"})
			return
		}
		if err := cfg.Store.SaveWebSession(acc.ID, client.jar.export(), webEmail, ""); err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"web_connected": true, "web_email": webEmail})
	}
}

func handleWebWechatStart(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		user := userFromContext(req.Context())
		var payload struct {
			AccountID string `json:"account_id"`
		}
		if err := json.NewDecoder(io.LimitReader(req.Body, 1<<20)).Decode(&payload); err != nil {
			writeJSON(writer, http.StatusBadRequest, map[string]any{"error": "无效 JSON 请求体"})
			return
		}
		acc, err := webAccountForUser(cfg, user.ID, payload.AccountID)
		if err != nil {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "找不到这个 Tower 账号"})
			return
		}
		client, err := newWebClient(cfg.BaseURL, nil)
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		page, err := client.get(req.Context(), "/users/sign_in")
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		appID := firstMatch(wxAppPattern, page)
		state := firstMatch(wxStatePattern, page)
		redirect := firstMatch(wxRedirectPattern, page)
		if appID == "" || redirect == "" {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "登录页没有微信扫码参数"})
			return
		}
		qrURL := "https://open.weixin.qq.com/connect/qrconnect?" + url.Values{
			"appid": {appID}, "redirect_uri": {redirect}, "response_type": {"code"},
			"scope": {"snsapi_login"}, "state": {state},
		}.Encode()
		_, qrPage, err := client.request(req.Context(), http.MethodGet, qrURL, "", "")
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		uuid := firstMatch(wxUUIDPattern, qrPage)
		imagePath := html.UnescapeString(firstMatch(wxImagePattern, qrPage))
		if uuid == "" || imagePath == "" {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "没有拿到微信二维码"})
			return
		}
		if strings.HasPrefix(imagePath, "/") {
			imagePath = "https://open.weixin.qq.com" + imagePath
		}
		status, image, err := client.request(req.Context(), http.MethodGet, imagePath, "", "")
		if err != nil || status >= 400 || image == "" {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "下载微信二维码失败"})
			return
		}
		id, err := generateMCPKey()
		if err != nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		id = strings.TrimPrefix(id, "at_sk-")
		wechatPendingLogins.Store(id, &wechatPending{
			userID: user.ID, accountID: acc.ID, client: client, uuid: uuid,
			redirect: redirect, state: state, expires: time.Now().Add(3 * time.Minute),
		})
		writeJSON(writer, http.StatusOK, map[string]any{
			"id":    id,
			"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(image)),
		})
	}
}

func handleWebWechatPoll(cfg Config) http.HandlerFunc {
	return func(writer http.ResponseWriter, req *http.Request) {
		user := userFromContext(req.Context())
		value, ok := wechatPendingLogins.Load(req.PathValue("id"))
		if !ok {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "扫码登录已失效"})
			return
		}
		pending := value.(*wechatPending)
		pending.mu.Lock()
		defer pending.mu.Unlock()
		if pending.userID != user.ID {
			writeJSON(writer, http.StatusNotFound, map[string]any{"error": "扫码登录已失效"})
			return
		}
		if pending.done {
			writeJSON(writer, http.StatusOK, map[string]any{"status": "connected"})
			return
		}
		if time.Now().After(pending.expires) {
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "expired"})
			return
		}
		status, text, err := pending.client.request(req.Context(), http.MethodGet, "https://lp.open.weixin.qq.com/connect/l/qrconnect?uuid="+url.QueryEscape(pending.uuid), "", "")
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		codeMatch := wxCodePattern.FindStringSubmatch(text)
		if status != http.StatusOK || codeMatch == nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "微信未返回有效的扫码状态，请重新获取二维码"})
			return
		}
		switch codeMatch[1] {
		case "408":
			writeJSON(writer, http.StatusOK, map[string]any{"status": "pending"})
		case "404":
			writeJSON(writer, http.StatusOK, map[string]any{"status": "scanned"})
		case "403":
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "cancelled"})
		case "402":
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "expired"})
		case "405":
			if err := finishWechat(req.Context(), cfg, pending, codeMatch[2]); err != nil {
				writeJSON(writer, http.StatusConflict, map[string]any{"error": err.Error()})
				return
			}
			pending.done = true
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "connected", "web_email": pending.client.email})
		default:
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "微信返回了未知的扫码状态，请重新获取二维码"})
		}
	}
}

func finishWechat(ctx context.Context, cfg Config, pending *wechatPending, code string) error {
	callback, err := url.Parse(pending.redirect)
	if err != nil {
		return err
	}
	query := callback.Query()
	query.Set("code", code)
	query.Set("state", pending.state)
	callback.RawQuery = query.Encode()
	status, _, err := pending.client.request(ctx, http.MethodGet, callback.String(), "", "")
	if err != nil {
		return err
	}
	if status >= 400 {
		return fmt.Errorf("微信回调失败 HTTP %d", status)
	}
	page, err := pending.client.get(ctx, "/launchpad/")
	if err != nil {
		return err
	}
	webEmail := html.UnescapeString(firstMatch(emailPattern, page))
	if webEmail == "" {
		return errors.New("扫码后没有读到网页账号邮箱")
	}
	acc, err := cfg.Store.GetByID(pending.accountID)
	if err != nil {
		return err
	}
	if acc.Email != "" && !strings.EqualFold(acc.Email, webEmail) {
		return errors.New("扫码账号邮箱与该 Tower 账号不一致")
	}
	pending.client.email = webEmail
	return cfg.Store.SaveWebSession(acc.ID, pending.client.jar.export(), webEmail, "")
}

func webAccountForUser(cfg Config, userID int64, accountID string) (*StoredAccount, error) {
	if cfg.Store == nil || strings.TrimSpace(accountID) == "" {
		return nil, ErrAccountNotFound
	}
	acc, err := cfg.Store.GetByID(accountID)
	if err != nil {
		return nil, err
	}
	if acc.UserID != userID {
		return nil, ErrAccountNotFound
	}
	return acc, nil
}

func todoWebPath(ctx context.Context, cfg Config, acc *StoredAccount, todoID string) (string, error) {
	if acc == nil || !guidPattern.MatchString(todoID) {
		return "", errors.New("todo_id 必须是 Tower GUID")
	}
	status, body, err := doTowerHTTPWithRefresh(ctx, cfg, acc.MCPAPIKey, acc, http.MethodGet, "/api/v1/todos/"+todoID, "", nil)
	if err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("读取任务失败 HTTP %d", status)
	}
	var doc struct {
		Data struct {
			Relationships struct {
				Project struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"project"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Data.Relationships.Project.Data.ID == "" {
		return "", errors.New("任务没有可打开的所属项目")
	}
	return "/projects/" + doc.Data.Relationships.Project.Data.ID + "/todos/" + todoID, nil
}
