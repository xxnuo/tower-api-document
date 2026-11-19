package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	dom "golang.org/x/net/html"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
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
	wxImageIDPattern   = regexp.MustCompile(`src="/connect/qrcode/([A-Za-z0-9_-]+)"`)
	wxStatusPattern    = regexp.MustCompile(`\bwx_errcode\s*=\s*(\d+)`)
	wxCodePattern      = regexp.MustCompile(`\bwx_code\s*=\s*['"]([^'"]+)['"]`)
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
	host  string
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
	if !strings.EqualFold(rawURL.Hostname(), jar.host) {
		return
	}
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
	parsed, err := url.Parse(client.base + "/")
	if err != nil {
		return nil, err
	}
	jar.host = parsed.Hostname()
	if len(cookies) == 0 {
		return client, nil
	}
	restored := make([]*http.Cookie, 0, len(cookies))
	for _, item := range cookies {
		if item.Name == "" || (item.Domain != "" && !strings.EqualFold(strings.TrimPrefix(item.Domain, "."), parsed.Hostname())) {
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
	const maxWebResponseSize = 3 << 20
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxWebResponseSize+1))
	if err == nil && len(payload) > maxWebResponseSize {
		return resp.StatusCode, "", errors.New("Tower 网页响应超过 3 MiB，无法读取完整结果")
	}
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

func webSearchQuery(args map[string]any) (url.Values, error) {
	q := url.Values{"page": {"1"}, "category": {"2"}, "exclude_trash": {"true"}}
	for key, val := range args {
		v := stringifyArg(val)
		switch key {
		case "tag":
			raw, ok := val.(string)
			name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
			if !ok || name == "" {
				return nil, errors.New("tag 须为非空字符串")
			}
			v = "#" + name
		case "keyword", "target_gid", "member_guid":
			if _, ok := val.(string); !ok {
				return nil, fmt.Errorf("%s 须为字符串", key)
			}
			if key == "member_guid" && !guidPattern.MatchString(v) {
				return nil, errors.New("member_guid 必须是 Tower 成员 GUID")
			}
			if key == "keyword" && v == "" {
				continue
			}
		case "page", "category":
			n, err := strconv.ParseInt(v, 10, 32)
			if err != nil || (n < 1 && !(key == "category" && n == -1)) {
				return nil, fmt.Errorf("%s 无效", key)
			}
		case "sort":
			if v != "1" && v != "2" {
				return nil, errors.New("sort 须为 1 或 2")
			}
		case "exclude_trash":
			if v != "true" && v != "false" {
				return nil, errors.New("exclude_trash 须为布尔值")
			}
		default:
			return nil, fmt.Errorf("未知搜索参数 %s", key)
		}
		q.Set(key, v)
	}
	if q.Get("keyword") == "" && q.Get("tag") == "" {
		return nil, errors.New("须提供非空的 keyword 或 tag，member_guid 仅用于附加筛选")
	}
	return q, nil
}

func (client *webClient) searchTodos(ctx context.Context, query url.Values) (map[string]any, error) {
	teams, err := client.teamGUIDs(ctx)
	if err != nil {
		return nil, err
	}
	return client.searchTeamTodos(ctx, query, teams)
}

func (client *webClient) searchTeamTodos(ctx context.Context, query url.Values, teams []string) (map[string]any, error) {
	page, _ := strconv.Atoi(query.Get("page"))
	items := []any{}
	more := false
	for _, team := range teams {
		text, err := client.get(ctx, "/teams/"+team+"/search?"+query.Encode())
		if err != nil {
			return nil, err
		}
		total, err := strconv.Atoi(firstMatch(resultCountPattern, text))
		if err != nil {
			return nil, errors.New("无法读取 Tower 搜索结果总数，请确认网页登录状态")
		}
		// Tower 网页每个团队每页返回 20 条，包括末页在内均可能显示加载更多按钮。
		if int64(total) > int64(page)*20 {
			more = true
		}
		for _, item := range parseSearchItems(text) {
			item["team"] = team
			items = append(items, item)
		}
	}
	result := map[string]any{"keyword": query.Get("keyword"), "items": items, "count": len(items), "page": page, "has_more": more}
	if tag := query.Get("tag"); tag != "" {
		result["tag"] = tag
	}
	if more {
		result["next_page"] = page + 1
	}
	return result, nil
}

func (client *webClient) teamGUIDs(ctx context.Context) ([]string, error) {
	page, err := client.get(ctx, "/launchpad/")
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	guids := make([]string, 0)
	if current := hiddenValues(page)["team-guid"]; guidPattern.MatchString(current) {
		seen[current] = struct{}{}
		guids = append(guids, current)
	}
	for _, match := range teamHrefPattern.FindAllStringSubmatch(page, -1) {
		if _, ok := seen[match[1]]; ok {
			continue
		}
		seen[match[1]] = struct{}{}
		guids = append(guids, match[1])
	}
	if len(guids) == 0 {
		return nil, errors.New("网页登录后没有可见团队，请重新登录 Tower")
	}
	return guids, nil
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
	query, err := webSearchQuery(args)
	if err != nil {
		return webToolText("search_todos", nil, err)
	}
	payload, err := runWebSearch(ctx, cfg, acc, query)
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

func runWebSearch(ctx context.Context, cfg Config, acc *StoredAccount, query url.Values) (map[string]any, error) {
	client, err := useWebClient(ctx, cfg, acc)
	if err != nil {
		return nil, err
	}
	defer client.save()
	if member := query.Get("member_guid"); member != "" {
		team, err := searchMemberTeam(ctx, cfg, acc, member)
		if err != nil {
			return nil, err
		}
		return client.searchTeamTodos(ctx, query, []string{team})
	}
	return client.searchTodos(ctx, query)
}

func searchMemberTeam(ctx context.Context, cfg Config, acc *StoredAccount, member string) (string, error) {
	if !guidPattern.MatchString(member) {
		return "", errors.New("member_guid 必须是 Tower 成员 GUID")
	}
	status, body, err := doTowerHTTPWithRefresh(ctx, cfg, acc.MCPAPIKey, acc, http.MethodGet, "/api/v1/members/"+member, "", nil)
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusNotFound:
		return "", errors.New("找不到 member_guid 对应的成员，或当前账号没有读取权限")
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", errors.New("当前账号没有读取 member_guid 对应成员的权限")
	case http.StatusOK:
	default:
		return "", fmt.Errorf("读取 member_guid 对应的成员失败 HTTP %d", status)
	}
	var doc struct {
		Data struct {
			ID            string `json:"id"`
			Type          string `json:"type"`
			Relationships struct {
				Team struct {
					Data struct {
						ID   string `json:"id"`
						Type string `json:"type"`
					} `json:"data"`
				} `json:"team"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil || doc.Data.ID != member || doc.Data.Type != "members" {
		return "", errors.New("Tower 未返回 member_guid 对应的成员")
	}
	team := doc.Data.Relationships.Team.Data
	if team.Type != "teams" || !guidPattern.MatchString(team.ID) {
		return "", errors.New("无法确认 member_guid 对应成员的所属团队")
	}
	return team.ID, nil
}

func webToolText(tool string, payload map[string]any, err error) (string, bool) {
	if err != nil {
		raw, _ := json.Marshal(map[string]any{"error": err.Error()})
		return agentText(raw), true
	}
	raw, _ := json.Marshal(payload)
	return resultProfiles[tool].compositeText(raw), false
}

func parseSearchItems(page string) []map[string]any {
	doc, _ := dom.Parse(strings.NewReader(page))
	var items []map[string]any
	for row := range doc.Descendants() {
		category := attr(row, "data-category")
		if row.Data != "li" || category == "" {
			continue
		}
		item := map[string]any{"category": category}
		var link *dom.Node
		for n := range row.Descendants() {
			switch {
			case n.Data == "p" && strings.Contains(attr(n, "class"), "result-title"):
				if v := attr(n, "data-completed"); v != "" {
					item["completed"] = v == "true"
				}
				for a := range n.Descendants() {
					if a.Data == "a" && attr(a, "class") != "tag" && attr(a, "href") != "" {
						link = a
						break
					}
				}
			case n.Data == "a" && strings.Contains(attr(n, "class"), "attachment-name"):
				link = n
			case strings.Contains(" "+attr(n, "class")+" ", " result-time "):
				if raw := towerDateValue(attr(n, "data-readable-time")); len(raw) > len(time.DateOnly) {
					item["result_time"] = raw
				}
			}
		}
		if link == nil {
			continue
		}
		href := attr(link, "href")
		var title strings.Builder
		for n := range link.Descendants() {
			if n.Type == dom.TextNode {
				title.WriteString(n.Data)
			}
		}
		item["title"] = strings.Join(strings.Fields(title.String()), " ")
		item["url"] = href
		id := firstMatch(todoIDPattern, href)
		if id == "" || category != "Todo" {
			id = href
		}
		item["id"] = id
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
	last      string
	redirect  string
	state     string
	expires   time.Time
	result    map[string]any
	finishErr error
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
		if payload.AccountID != "" {
			if _, err := webAccountForUser(cfg, user.ID, payload.AccountID); err != nil {
				writeJSON(writer, 404, map[string]any{"error": "找不到这个 Tower 账号"})
				return
			}
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
		client.email = webEmail
		result, err := finishWebLogin(req.Context(), cfg, user.ID, payload.AccountID, client)
		writeGrant(writer, result, err)
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
		if payload.AccountID != "" {
			if _, err := webAccountForUser(cfg, user.ID, payload.AccountID); err != nil {
				writeJSON(writer, 404, map[string]any{"error": "找不到这个 Tower 账号"})
				return
			}
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
		uuid := firstMatch(wxImageIDPattern, qrPage)
		if uuid == "" {
			writeJSON(writer, 502, map[string]any{"error": "没有拿到微信二维码"})
			return
		}
		status, image, err := client.request(req.Context(), http.MethodGet, "https://open.weixin.qq.com/connect/qrcode/"+uuid, "", "")
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
			userID: user.ID, accountID: payload.AccountID, client: client, uuid: uuid,
			redirect: redirect, state: state, expires: time.Now().Add(3 * time.Minute),
		})
		time.AfterFunc(3*time.Minute, func() { wechatPendingLogins.Delete(id) })
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
		if pending.result != nil || pending.finishErr != nil {
			writeGrant(writer, pending.result, pending.finishErr)
			return
		}
		if time.Now().After(pending.expires) {
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "expired"})
			return
		}
		query := url.Values{"uuid": {pending.uuid}, "_": {fmt.Sprint(time.Now().UnixMilli())}}
		if pending.last != "" {
			query.Set("last", pending.last)
		}
		status, text, err := pending.client.request(req.Context(), http.MethodGet, "https://lp.open.weixin.qq.com/connect/l/qrconnect?"+query.Encode(), "", "")
		if err != nil {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		statusCode := firstMatch(wxStatusPattern, text)
		if status != http.StatusOK || statusCode == "" {
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "微信未返回有效的扫码状态，请重新获取二维码"})
			return
		}
		switch statusCode {
		case "408":
			writeJSON(writer, http.StatusOK, map[string]any{"status": "pending"})
		case "404":
			pending.last = "404"
			writeJSON(writer, http.StatusOK, map[string]any{"status": "scanned"})
		case "403":
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "cancelled"})
		case "402":
			wechatPendingLogins.Delete(req.PathValue("id"))
			writeJSON(writer, http.StatusOK, map[string]any{"status": "expired"})
		case "405":
			pending.finishErr = finishWechat(req.Context(), pending, firstMatch(wxCodePattern, text))
			if pending.finishErr == nil {
				pending.result, pending.finishErr = finishWebLogin(req.Context(), cfg, user.ID, pending.accountID, pending.client)
			}
			writeGrant(writer, pending.result, pending.finishErr)
		default:
			writeJSON(writer, http.StatusBadGateway, map[string]any{"error": "微信返回了未知的扫码状态，请重新获取二维码"})
		}
	}
}

func finishWechat(ctx context.Context, pending *wechatPending, code string) error {
	if code == "" {
		return errors.New("微信未返回确认码")
	}
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
	pending.client.email = webEmail
	return nil
}

func finishWebLogin(ctx context.Context, cfg Config, owner int64, accountID string, client *webClient) (map[string]any, error) {
	if accountID == "" {
		return (&webGrant{owner: owner, client: client}).authorize(ctx, cfg)
	}
	acc, err := webAccountForUser(cfg, owner, accountID)
	if err != nil {
		return nil, err
	}
	if acc.Email == "" || !strings.EqualFold(acc.Email, client.email) {
		return nil, errors.New("网页登录邮箱与该 Tower 账号不一致")
	}
	err = cfg.Store.SaveWebSession(acc.ID, client.jar.export(), client.email, "")
	return map[string]any{"status": "connected", "web_connected": true, "web_email": client.email, "account_id": acc.ID}, err
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
