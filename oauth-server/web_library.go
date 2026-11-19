package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var (
	libraryEntryPath = regexp.MustCompile(`^/teams/([1-9][0-9]*)/repositories/?$`)
	libraryItemPath  = regexp.MustCompile(`^/teams/([1-9][0-9]*)/(repositories|repository_folders|repository_documents|repository_uploads|repository_tencent_documents)/([1-9][0-9]*)/?$`)
)

type libraryItem struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

var gatewayLibraryTools = []mcpToolSpec{
	{
		Name: "list_repositories", Title: "发现团队知识库",
		Desc:   "从当前网页登录账号可访问的团队页面发现知识库入口。需要网页登录。",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/repositories",
		Params: []mcpParam{pPath("team_id", "团队 GUID，可从 list_teams 获取")},
	},
	{
		Name: "list_repository_items", Title: "列出知识库目录",
		Desc:   "列出当前知识库根目录或文件夹页面中可见的直接子项及原文链接。需要网页登录。",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/repository_items",
		Params: []mcpParam{
			pPath("team_id", "团队 GUID，可从 list_teams 获取"),
			{Name: "url", Type: "string", In: "query", Required: true, Desc: "list_repositories 或此前目录结果返回的知识库或文件夹 URL"},
		},
	},
	{
		Name: "read_repository_item", Title: "读取知识库文档",
		Desc:   "读取 Tower 原生 Markdown 正文。普通文档、Office、上传等类型返回原文页面链接。需要网页登录。",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/repository_item",
		Params: []mcpParam{
			pPath("team_id", "团队 GUID，可从 list_teams 获取"),
			{Name: "url", Type: "string", In: "query", Required: true, Desc: "list_repository_items 返回的文档或文件 URL"},
		},
	},
}

func libraryHasAttr(n *html.Node, key string) bool {
	for _, a := range n.Attr {
		if a.Key == key {
			return true
		}
	}
	return false
}

func (client *webClient) library(ctx context.Context, tool, team, rawURL string) (map[string]any, error) {
	if !guidPattern.MatchString(team) {
		return nil, fmt.Errorf("%w：team_id 须为 list_teams 返回的团队 GUID", errWebArguments)
	}
	origin, err := url.Parse(client.base)
	if err != nil {
		return nil, errors.New("Tower 服务地址无效")
	}
	var item []string
	if tool != "list_repositories" {
		u, err := url.Parse(rawURL)
		if err != nil || u.User != nil || u.RawPath != "" || u.ForceQuery || u.RawQuery != "" || strings.Contains(rawURL, "#") ||
			(u.Host != "" || u.Scheme != "") && (u.Host != origin.Host || u.Scheme != origin.Scheme) {
			return nil, fmt.Errorf("%w：须使用 Tower 的原文页面 URL 或站内路径", errWebArguments)
		}
		item = libraryItemPath.FindStringSubmatch(u.Path)
		if item == nil {
			return nil, fmt.Errorf("%w：无效的 Tower 知识库路径", errWebArguments)
		}
		container := item[2] == "repositories" || item[2] == "repository_folders"
		if container != (tool == "list_repository_items") {
			return nil, fmt.Errorf("%w：列出目录须使用知识库或文件夹 URL，读取正文须使用文档或文件 URL", errWebArguments)
		}
	}

	teamPage, _, err := client.libraryPage(ctx, origin, "/teams/"+team+"/projects", "")
	if err != nil {
		return nil, err
	}
	if hiddenValues(teamPage)["team-guid"] != team {
		return nil, errors.New("无法确认当前会话有权访问指定团队")
	}
	teamDoc, err := html.Parse(strings.NewReader(teamPage))
	if err != nil {
		return nil, err
	}
	var entry []string
	for n := range teamDoc.Descendants() {
		if n.Data == "a" && attr(n.Parent, "id") == "nav-repository" {
			entry = libraryEntryPath.FindStringSubmatch(attr(n, "href"))
			if entry != nil {
				break
			}
		}
	}
	if entry == nil {
		return nil, errors.New("指定团队没有可访问的知识库入口")
	}
	path := strings.TrimRight(entry[0], "/")
	if item != nil {
		if item[1] != entry[1] {
			return nil, fmt.Errorf("%w：知识库路径与指定团队不一致", errWebArguments)
		}
		path = strings.TrimRight(item[0], "/")
	}

	pageText, path, err := client.libraryPage(ctx, origin, path, entry[1])
	if err != nil {
		return nil, err
	}
	resource := libraryItemPath.FindStringSubmatch(path)
	if resource == nil || hiddenValues(pageText)["team-guid"] != team {
		return nil, errors.New("无法确认知识库资源属于指定团队")
	}
	doc, err := html.Parse(strings.NewReader(pageText))
	if err != nil {
		return nil, err
	}
	var page *html.Node
	for n := range doc.Descendants() {
		if n.Data == "tr-repository-page" {
			page = n
			break
		}
	}
	if page == nil {
		return nil, errors.New("未找到 Tower 知识库页面")
	}
	current := client.libraryItem(resource, "")
	repositoryURL := ""
	var table, content *html.Node
	for n := range page.Descendants() {
		switch {
		case (n.Data == "h1" || n.Data == "h2") && current.Title == "" &&
			(webHasClass(n, "header-title") || webHasClass(n, "breadcrumb-name") ||
				webHasClass(n, "document-title") || webHasClass(n, "upload-title")):
			current.Title = webNodeText(n)
		case n.Data == "a" && webHasClass(n, "breadcrumb-name") && repositoryURL == "":
			root := libraryItemPath.FindStringSubmatch(attr(n, "href"))
			if root != nil && root[1] == entry[1] && root[2] == "repositories" {
				repositoryURL = client.resolve(strings.TrimRight(root[0], "/"))
			}
		case n.Data == "tr-repository-folder-table" && table == nil:
			table = n
		case webHasClass(n, "document-content") && content == nil:
			content = n
		}
	}
	if current.Type == "repository" {
		repositoryURL = current.URL
	}
	if repositoryURL == "" || current.Title == "" {
		return nil, errors.New("无法确认知识库页面的标题或所属知识库")
	}
	if tool == "list_repositories" {
		return map[string]any{"team_id": team, "items": []libraryItem{current}}, nil
	}
	result := map[string]any{"team_id": team, "id": current.ID, "url": current.URL, "title": current.Title, "type": current.Type, "repository_url": repositoryURL}
	if tool == "list_repository_items" {
		if table == nil {
			return nil, errors.New("未找到知识库目录列表")
		}
		items := make([]libraryItem, 0)
		for n := range table.Descendants() {
			if n.Data != "a" || !webHasClass(n, "item-name") {
				continue
			}
			link := libraryItemPath.FindStringSubmatch(attr(n, "href"))
			if link != nil && link[1] == entry[1] && link[2] != "repositories" {
				items = append(items, client.libraryItem(link, webNodeText(n)))
			}
		}
		result["items"] = items
		return result, nil
	}
	if current.Type == "document" && content != nil {
		for n := range content.Descendants() {
			if n.Data == "tr-markdown-parser" && libraryHasAttr(n, "content") {
				result["format"] = "markdown"
				result["markdown"] = attr(n, "content")
				return result, nil
			}
		}
	}
	result["format"] = "link"
	result["message"] = "此条目未提供原生 Markdown 正文，请打开原文页面。"
	return result, nil
}

func (client *webClient) libraryItem(match []string, title string) libraryItem {
	kind := strings.TrimSuffix(strings.TrimPrefix(match[2], "repository_"), "s")
	if match[2] == "repositories" {
		kind = "repository"
	}
	return libraryItem{ID: match[3], Type: kind, Title: title, URL: client.resolve(strings.TrimRight(match[0], "/"))}
}

func (client *webClient) libraryPage(ctx context.Context, origin *url.URL, path, teamNumber string) (string, string, error) {
	finalPath := path
	discover := libraryEntryPath.MatchString(path)
	web := *client
	httpClient := *client.http
	web.http = &httpClient
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		u := req.URL
		clean := strings.TrimRight(u.Path, "/")
		if len(via) >= 5 || u.Scheme != origin.Scheme || u.Host != origin.Host || u.User != nil || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return http.ErrUseLastResponse
		}
		if clean != path {
			match := libraryItemPath.FindStringSubmatch(clean)
			if !discover || match == nil || match[1] != teamNumber || match[2] != "repositories" || finalPath != path {
				return http.ErrUseLastResponse
			}
		}
		finalPath = clean
		return nil
	}
	status, body, err := web.request(ctx, http.MethodGet, web.resolve(path), "", "")
	if err != nil {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		return "", "", errors.New("Tower 知识库网页请求失败")
	}
	if status != http.StatusOK {
		return "", "", fmt.Errorf("Tower 知识库网页请求失败 HTTP %d", status)
	}
	return body, finalPath, nil
}
