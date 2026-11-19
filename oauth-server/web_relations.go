package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var gatewayRelationTools = []mcpToolSpec{
	{
		Name: "list_todo_dependencies", Title: "查询任务依赖",
		Desc:   "列出任务的前置和后置依赖。需要网页登录。",
		Method: "GET", PathTemplate: "/api/v1/todos/{todo_id}/dependencies",
		Params: []mcpParam{pPath("todo_id", "任务 GUID")},
	},
	{
		Name: "add_todo_dependency", Title: "添加任务依赖",
		Desc:   "将另一任务设为当前任务的前置或后置依赖。需要网页登录。",
		Method: "POST", PathTemplate: "/api/v1/todos/{todo_id}/add_dependency",
		Params: []mcpParam{
			pPath("todo_id", "任务 GUID"),
			pBody("dependency_todo_id", "string", true, "另一任务 GUID"),
			pBody("direction", "string", true, "parent 为前置依赖，child 为后置依赖"),
		},
	},
	{
		Name: "remove_todo_dependency", Title: "解除任务依赖",
		Desc:   "按另一任务和方向解除任务依赖。需要网页登录。",
		Method: "POST", PathTemplate: "/api/v1/todos/{todo_id}/remove_dependency",
		Params: []mcpParam{
			pPath("todo_id", "任务 GUID"),
			pBody("dependency_todo_id", "string", true, "另一任务 GUID"),
			pBody("direction", "string", true, "parent 为前置依赖，child 为后置依赖"),
		},
	},
	{
		Name: "copy_todo", Title: "复制一份任务",
		Desc:   "通过 Tower 原生功能复制一份任务到目标项目，清单可选。仅当目标清单出现唯一新增同名任务时返回候选 GUID。结果不明时先核对目标清单，重复调用会再复制一份。需要网页登录。",
		Method: "POST", PathTemplate: "/api/v1/todos/{todo_id}/copy",
		Params: []mcpParam{
			pPath("todo_id", "来源任务 GUID"),
			pBody("project_id", "string", true, "目标项目 GUID"),
			pBody("todolist_id", "string", false, "目标清单 GUID，省略则复制到清单外任务"),
			pBody("copy_assignee", "boolean", false, "保留负责人，默认 true"),
			pBody("copy_due_at", "boolean", false, "保留截止时间，默认 false"),
			pBody("copy_desc", "boolean", false, "保留任务描述，默认 true"),
			pBody("copy_sub_todos", "boolean", false, "保留子任务，默认 false"),
			pBody("copy_attachments", "boolean", false, "保留附件，默认 false"),
			pBody("copy_labels", "boolean", false, "保留标签，默认 false"),
			pBody("copy_cc_members", "boolean", false, "保留关注者，默认 false"),
			pBody("copy_todos_dependencies", "boolean", false, "保留任务依赖，默认 false"),
		},
	},
}

const relationFormType = "application/x-www-form-urlencoded; charset=UTF-8"

type todoDependency struct {
	TodoID, Direction, Title, RemovePath string
}

func readTodoDependencyPage(page, todoID string) ([]todoDependency, error) {
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, err
	}
	var main, section *html.Node
	for node := range doc.Descendants() {
		if node.Type == html.ElementNode && node.Data == "tr-todo-page-main" && attr(node, "guid") == todoID {
			main = node
			break
		}
	}
	if main != nil {
		for node := range main.Parent.Descendants() {
			if node.Type == html.ElementNode && node.Data == "tr-todo-page-dependencies" {
				section = node
				break
			}
		}
	}
	if section == nil {
		return nil, errors.New("网页未返回指定任务的依赖区域，请确认登录状态和权限")
	}
	items := []todoDependency{}
	removePrefix := "/todos/" + todoID + "/dependencies/"
	for row := range section.Descendants() {
		if row.Type != html.ElementNode || row.Data != "tr-grid-dependency-row" {
			continue
		}
		item := todoDependency{TodoID: attr(row, "guid"), Direction: attr(row, "dependency-type")}
		if !guidPattern.MatchString(item.TodoID) || (item.Direction != "parent" && item.Direction != "child") {
			return nil, errors.New("无法确认 Tower 任务依赖的任务 GUID 或方向")
		}
		for node := range row.Descendants() {
			if item.Title == "" && webHasClass(node, "todo-rest") {
				item.Title = webNodeText(node)
			}
			if node.Data == "a" && attr(node, "data-method") == "delete" {
				path := attr(node, "href")
				if id, found := strings.CutPrefix(path, removePrefix); found && id != "" && strings.Trim(id, "0123456789") == "" {
					item.RemovePath = path
				}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

func (client *webClient) todoDependencies(ctx context.Context, pagePath, todoID string) ([]todoDependency, error) {
	page, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	return readTodoDependencyPage(page, todoID)
}

func (client *webClient) mutateTodoDependency(ctx context.Context, pagePath, todoID, relatedID, direction string, add bool) (map[string]any, error) {
	find := func(d todoDependency) bool { return d.TodoID == relatedID && d.Direction == direction }
	items, err := client.todoDependencies(ctx, pagePath, todoID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(items, find)
	result := map[string]any{"todo_id": todoID, "dependency_todo_id": relatedID, "direction": direction}
	if (i >= 0) == add {
		result["already"] = true
		return result, nil
	}
	if add && slices.ContainsFunc(items, func(d todoDependency) bool { return d.TodoID == relatedID }) {
		return nil, fmt.Errorf("%w：两项任务已有反向依赖，再添加会形成循环", errWebArguments)
	}
	if client.csrf == "" {
		return nil, errors.New("任务页没有提供 CSRF 令牌")
	}
	method, path, body := http.MethodPost, "/todos/"+todoID+"/dependencies", ""
	if add {
		body = url.Values{
			"todos_dependency[todo_id]":  {relatedID},
			"todos_dependency[category]": {direction},
		}.Encode()
	} else {
		method, path = http.MethodDelete, items[i].RemovePath
		if path == "" {
			return nil, errors.New("任务页没有提供该依赖的解除操作")
		}
	}
	status, _, err := client.request(ctx, method, client.resolve(path), body, relationFormType)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("更新任务依赖失败 HTTP %d", status)
	}
	items, err = client.todoDependencies(ctx, pagePath, todoID)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(items, find) != add {
		return nil, errors.New("未能确认任务依赖已更新，请检查任务页")
	}
	return result, nil
}

// Tower returns the copy form as an escaped JavaScript template string.
var todoCopyTemplate = regexp.MustCompile(`template:\s*'((?:\\.|[^'\\])*)'`)

func todoCopyForm(script, action, projectID string) (string, error) {
	match := todoCopyTemplate.FindStringSubmatch(script)
	if match == nil {
		return "", errors.New("Tower 未提供复制任务表单")
	}
	doc, err := html.Parse(strings.NewReader(webJSUnescape.Replace(match[1])))
	if err != nil {
		return "", err
	}
	for form := range doc.Descendants() {
		if form.Data != "form" || attr(form, "action") != action || attr(form, "method") != "post" || !webHasClass(form, "form-todo-copy") {
			continue
		}
		var token string
		target := false
		for node := range form.Descendants() {
			if node.Data == "input" && attr(node, "name") == "authenticity_token" {
				token = attr(node, "value")
			}
			if node.Data == "select" && attr(node, "name") == "copy[tpuid]" {
				for option := range node.Descendants() {
					target = target || (option.Data == "option" && attr(option, "value") == projectID)
				}
			}
		}
		if token != "" && target {
			return token, nil
		}
	}
	return "", errors.New("无法确认 Tower 复制表单或目标项目")
}

func (client *webClient) submitTodoCopy(ctx context.Context, pagePath, todoID, projectID, listID string, options url.Values) (map[string]any, error) {
	parts := strings.Split(pagePath, "/")
	if len(parts) != 5 || parts[1] != "projects" || !guidPattern.MatchString(parts[2]) || parts[3] != "todos" || parts[4] != todoID {
		return nil, errors.New("无法确认复制来源任务的项目")
	}
	source := parts[2]
	formPath := "/todos/" + todoID + "/copy/new?project_guid=" + source
	action := pagePath + "/copy"
	status, script, err := client.request(ctx, http.MethodGet, client.resolve(formPath), "", relationFormType)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("读取复制表单失败 HTTP %d", status)
	}
	token, err := todoCopyForm(script, action, projectID)
	if err != nil {
		return nil, err
	}
	form := options
	form.Set("authenticity_token", token)
	form.Set("copy[tpuid]", projectID)
	form.Set("copy[tluid]", listID)
	form.Set("copy[count]", "1")
	result := map[string]any{"source_todo_id": todoID, "project_id": projectID, "todolist_id": listID}
	// A 307 or 308 can resend the POST, creating an extra copy.
	postClient, httpClient := *client, *client.http
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	postClient.http = &httpClient
	status, body, err := postClient.request(ctx, http.MethodPost, client.resolve(action), form.Encode(), relationFormType)
	if err != nil {
		result["identity_status"] = "unconfirmed"
		result["message"] = "Tower 响应中断，复制结果未知，请先检查目标清单"
		return result, nil
	}
	var reply struct {
		Success bool `json:"success"`
	}
	if status != http.StatusOK || json.Unmarshal([]byte(body), &reply) != nil || !reply.Success {
		result["identity_status"] = "unconfirmed"
		result["message"] = fmt.Sprintf("Tower 未确认复制结果（HTTP %d），请先检查目标清单", status)
		return result, nil
	}
	result["success"] = true
	return result, nil
}

func (client *webClient) runTodoRelation(ctx context.Context, cfg Config, acc *StoredAccount, name, pagePath, todoID string, args map[string]any) (map[string]any, error) {
	switch name {
	case "list_todo_dependencies":
		items, err := client.todoDependencies(ctx, pagePath, todoID)
		if err != nil {
			return nil, err
		}
		rows := make([]map[string]any, 0, len(items))
		for _, item := range items {
			rows = append(rows, map[string]any{"todo_id": item.TodoID, "direction": item.Direction, "title": item.Title})
		}
		return map[string]any{"todo_id": todoID, "items": rows}, nil
	case "add_todo_dependency", "remove_todo_dependency":
		relatedID := stringifyArg(args["dependency_todo_id"])
		direction := stringifyArg(args["direction"])
		if relatedID == todoID {
			return nil, fmt.Errorf("%w：依赖任务不能是当前任务", errWebArguments)
		}
		if direction != "parent" && direction != "child" {
			return nil, fmt.Errorf("%w：direction 须为 parent 或 child", errWebArguments)
		}
		return client.mutateTodoDependency(ctx, pagePath, todoID, relatedID, direction, name == "add_todo_dependency")
	case "copy_todo":
		return client.copyTodo(ctx, cfg, acc, pagePath, todoID, args)
	}
	return nil, errors.New("未知任务依赖或复制工具")
}

func copyTodoOptions(args map[string]any) (url.Values, error) {
	form := url.Values{}
	for name, enabled := range map[string]bool{
		"copy_assignee": true, "copy_due_at": false, "copy_desc": true, "copy_sub_todos": false,
		"copy_attachments": false, "copy_labels": false, "copy_cc_members": false, "copy_todos_dependencies": false,
	} {
		if input, present := args[name]; present {
			var ok bool
			enabled, ok = input.(bool)
			if !ok {
				return nil, fmt.Errorf("%w：%s 须为布尔值", errWebArguments, name)
			}
		}
		value := "0"
		if enabled {
			value = "1"
		}
		form.Set("copy["+name+"]", value)
	}
	return form, nil
}

func copySourceTitle(ctx context.Context, cfg Config, acc *StoredAccount, todoID string) (string, error) {
	path := "/api/v1/todos/" + todoID
	status, body, err := doTowerHTTPWithRefresh(ctx, cfg, acc.MCPAPIKey, acc, http.MethodGet, path, "", nil)
	if err != nil || status != http.StatusOK {
		return "", errors.New(towerMCPError(http.MethodGet, path, status, body, err))
	}
	var doc struct {
		Data struct {
			ID         string `json:"id"`
			Attributes struct {
				Content string `json:"content"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Data.ID != todoID || doc.Data.Attributes.Content == "" {
		return "", errors.New("无法确认复制来源任务的标题")
	}
	return doc.Data.Attributes.Content, nil
}

func copyTargetTodos(ctx context.Context, cfg Config, acc *StoredAccount, listID, title string) (map[string]bool, error) {
	matches := map[string]bool{}
	page := upstreamProjectPage{number: 1, includeFilterFields: true}
	for page.number > 0 {
		rows, err := page.fetch(ctx, cfg, acc.MCPAPIKey, acc, "/api/v1/todolists/"+listID+"/todos", true, true)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row["content"] != title {
				continue
			}
			id, ok := row["id"].(string)
			if !ok || !guidPattern.MatchString(id) {
				return nil, errors.New("目标清单返回了无效的任务 GUID")
			}
			matches[id] = true
		}
	}
	return matches, nil
}

func findCopiedTodo(before, after map[string]bool) (string, bool) {
	var id string
	for candidate := range after {
		if before[candidate] {
			continue
		}
		if id != "" {
			return "", true
		}
		id = candidate
	}
	return id, false
}

func (client *webClient) copyTodo(ctx context.Context, cfg Config, acc *StoredAccount, pagePath, todoID string, args map[string]any) (map[string]any, error) {
	projectID, listID := stringifyArg(args["project_id"]), stringifyArg(args["todolist_id"])
	if _, provided := args["todolist_id"]; provided && !guidPattern.MatchString(listID) {
		return nil, fmt.Errorf("%w：todolist_id 须为 Tower GUID", errWebArguments)
	}
	options, err := copyTodoOptions(args)
	if err != nil {
		return nil, err
	}
	title, err := copySourceTitle(ctx, cfg, acc, todoID)
	if err != nil {
		return nil, err
	}
	lists, err := client.todolists(ctx, projectID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(lists, func(list todolistChoice) bool {
		return (listID == "" && list.Default) || list.GUID == listID
	})
	if i < 0 || !guidPattern.MatchString(lists[i].GUID) {
		return nil, fmt.Errorf("%w：无法确认目标项目中的清单", errWebArguments)
	}
	listID = lists[i].GUID
	before, err := copyTargetTodos(ctx, cfg, acc, listID, title)
	if err != nil {
		return nil, err
	}
	before[todoID] = true
	result, err := client.submitTodoCopy(ctx, pagePath, todoID, projectID, listID, options)
	if err != nil || result["success"] != true {
		return result, err
	}
	result["identity_status"] = "unconfirmed"
	result["message"] = "复制成功，尚未确认新任务 GUID，请先检查目标清单"
	search, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		after, err := copyTargetTodos(search, cfg, acc, listID, title)
		if err != nil {
			return result, nil
		}
		id, ambiguous := findCopiedTodo(before, after)
		if ambiguous {
			result["identity_status"] = "ambiguous"
			result["message"] = "复制成功，目标清单出现多个新增同名任务"
			return result, nil
		}
		if id != "" {
			result["identity_status"] = "unique_candidate"
			result["candidate_todo_id"] = id
			result["message"] = "目标清单中只有一个新增同名任务，Tower 未返回可核对的副本 GUID"
			return result, nil
		}
		select {
		case <-search.Done():
			return result, nil
		case <-time.After(350 * time.Millisecond):
		}
	}
}
