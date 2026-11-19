package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	dom "golang.org/x/net/html"
)

type todoListMapping struct {
	Path   string
	ListID string
}

type todoMovePage struct {
	Links     map[string]todoListMapping
	Parent    []string
	TargetURL string
}

func readTodoMovePage(page, todoID, projectID string) (todoMovePage, error) {
	doc, err := dom.Parse(strings.NewReader(page))
	if err != nil {
		return todoMovePage{}, err
	}
	state := todoMovePage{Links: map[string]todoListMapping{}}
	foundTodo := false
	prefix := "/todos/" + todoID + "/section_links/"
	validPath := func(path string) bool {
		id, found := strings.CutPrefix(path, prefix)
		return found && id != "" && strings.Trim(id, "0123456789") == ""
	}
	for node := range doc.Descendants() {
		if node.Type != dom.ElementNode {
			continue
		}
		switch node.Data {
		case "tr-todo-page-main":
			foundTodo = foundTodo || attr(node, "guid") == todoID
		case "tr-todo-page-breadcrumbs":
			for link := range node.Descendants() {
				if link.Type == dom.ElementNode && link.Data == "a" {
					href, err := url.Parse(attr(link, "href"))
					if err != nil {
						return todoMovePage{}, err
					}
					state.Parent = append(state.Parent, href.Path)
				}
			}
		case "tr-todo-section-links-field-detail-item":
			linkURL := attr(node, "data-url")
			link, err := url.Parse(linkURL)
			if err != nil || link.IsAbs() || link.Host != "" || !validPath(link.Path) {
				return todoMovePage{}, errors.New("任务页的项目关联地址无效")
			}
			var project, list string
			for selector := range node.Descendants() {
				if selector.Type != dom.ElementNode || selector.Data != "tr-selector-field" {
					continue
				}
				classes := strings.Fields(attr(selector, "class"))
				for option := range selector.Descendants() {
					if option.Type != dom.ElementNode || option.Data != "option" ||
						!slices.ContainsFunc(option.Attr, func(a dom.Attribute) bool { return a.Key == "selected" }) {
						continue
					}
					if slices.Contains(classes, "container-select") {
						project = attr(option, "value")
					}
					if slices.Contains(classes, "sectionable-select") {
						list = attr(option, "value")
					}
				}
			}
			if _, exists := state.Links[project]; exists || !guidPattern.MatchString(project) || !guidPattern.MatchString(list) {
				return todoMovePage{}, errors.New("任务页的项目或清单关联无法确认")
			}
			state.Links[project] = todoListMapping{Path: link.Path, ListID: list}
			if project == projectID {
				state.TargetURL = linkURL
			}
		}
	}
	if !foundTodo {
		return todoMovePage{}, errors.New("网页未返回指定任务，请确认登录状态和权限")
	}
	return state, nil
}

func (client *webClient) moveTodo(ctx context.Context, pagePath, todoID, projectID, todolistID string) (map[string]any, error) {
	if !guidPattern.MatchString(todoID) || !guidPattern.MatchString(projectID) || !guidPattern.MatchString(todolistID) {
		return nil, errors.New("todo_id、project_id 和 todolist_id 必须是 Tower GUID")
	}
	page, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	before, err := readTodoMovePage(page, todoID, projectID)
	if err != nil {
		return nil, err
	}
	target, exists := before.Links[projectID]
	if !exists {
		return nil, errors.New("任务在指定项目中没有可移动的清单关联")
	}
	result := map[string]any{
		"todo_id": todoID, "project_id": projectID, "todolist_id": todolistID,
		"section_link_id": target.Path[strings.LastIndexByte(target.Path, '/')+1:],
	}
	if target.ListID == todolistID {
		result["already"] = true
		return result, nil
	}
	lists, err := client.todolists(ctx, projectID)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(lists, func(list todolistChoice) bool { return list.GUID == todolistID })
	if i == -1 {
		return nil, errors.New("目标项目中没有这个清单")
	}
	if client.csrf == "" {
		return nil, errors.New("任务页没有提供 CSRF 令牌")
	}
	form := url.Values{"project_guid": {projectID}, "todolist_guid": {todolistID}}
	status, _, err := client.request(ctx, http.MethodPut, client.resolve(before.TargetURL), form.Encode(), "application/x-www-form-urlencoded; charset=UTF-8")
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("移动任务到清单失败 HTTP %d", status)
	}
	page, err = client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	after, err := readTodoMovePage(page, todoID, projectID)
	if err != nil {
		return nil, err
	}
	target.ListID = todolistID
	before.Links[projectID] = target
	if !maps.Equal(before.Links, after.Links) || !slices.Equal(before.Parent, after.Parent) {
		return nil, errors.New("移动后未能确认目标清单、其他项目归属和父任务")
	}
	result["todolist"] = lists[i].Label
	return result, nil
}
