package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	dom "golang.org/x/net/html"
)

type projectLabel struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Scope string `json:"scope"`
}

type projectFieldOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type projectField struct {
	Key     string               `json:"key"`
	Name    string               `json:"name"`
	Type    string               `json:"field_type,omitempty"`
	Options []projectFieldOption `json:"options"`
}

func (client *webClient) projectFields(ctx context.Context, projectID string) (map[string]any, error) {
	if !guidPattern.MatchString(projectID) {
		return nil, errors.New("project_id 必须是 Tower 项目 GUID")
	}
	path := "/projects/" + projectID
	labels, labelsErr := client.projectLabels(ctx, path)
	fields, warnings, fieldsErr := client.projectCustomFields(ctx, path)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if labelsErr != nil && fieldsErr != nil {
		return nil, fmt.Errorf("读取项目标签和自定义字段失败：%w", errors.Join(labelsErr, fieldsErr))
	}
	if labelsErr != nil {
		warnings = append(warnings, "标签："+labelsErr.Error())
	}
	if fieldsErr != nil {
		warnings = append(warnings, "自定义字段："+fieldsErr.Error())
	}
	result := map[string]any{
		"project_id": projectID, "source": "tower_web",
		"labels": labels, "custom_fields": fields, "custom_fields_scope": "project_list_and_field_menu",
		"complete": len(warnings) == 0,
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return result, nil
}

func (client *webClient) projectLabels(ctx context.Context, path string) ([]projectLabel, error) {
	page, err := client.get(ctx, path+"/labels")
	if err != nil {
		return nil, err
	}
	doc, err := dom.Parse(strings.NewReader(page))
	if err != nil {
		return nil, err
	}
	labels := []projectLabel{}
	seenLists := map[string]bool{}
	for n := range doc.Descendants() {
		scope := ""
		switch {
		case webHasClass(n, "project-label-list"):
			scope = "project"
		case webHasClass(n, "team-label-list"):
			scope = "team"
		default:
			continue
		}
		seenLists[scope] = true
		for item := range n.Descendants() {
			if !webHasClass(item, "label-item") {
				continue
			}
			id, ok := strings.CutPrefix(attr(item, "id"), "label_")
			value, err := strconv.ParseInt(id, 10, 64)
			var name string
			for child := range item.ChildNodes() {
				if webHasClass(child, "name") {
					name = webNodeText(child)
					break
				}
			}
			if !ok || err != nil || value <= 0 || name == "" {
				return nil, errors.New("Tower 标签页面结构不完整")
			}
			labels = append(labels, projectLabel{ID: value, Name: name, Scope: scope})
		}
	}
	if !seenLists["project"] || !seenLists["team"] {
		return nil, errors.New("网页缺少项目或团队标签列表")
	}
	return labels, nil
}

func (client *webClient) projectCustomFields(ctx context.Context, path string) ([]projectField, []string, error) {
	page, err := client.get(ctx, path+"/list/todos")
	if err != nil {
		return nil, nil, err
	}
	doc, err := dom.Parse(strings.NewReader(page))
	if err != nil {
		return nil, nil, err
	}

	fields := []projectField{}
	var warnings []string
	seen := map[string]bool{}
	var template, sortKeys *dom.Node
	for n := range doc.Descendants() {
		if n.Data == "template" && attr(n, "id") == "tpl-default-todolist-todo-row" {
			template = n
		}
		if n.Data == "select" && attr(n, "name") == "projects_filter[sorts_attributes][0][key]" {
			sortKeys = n
		}
	}
	if template == nil && sortKeys == nil {
		return nil, nil, errors.New("网页缺少任务列表的字段定义")
	}
	for n := range doc.Descendants() {
		if !strings.HasPrefix(attr(n, "cache-key"), "field_prototypes/") {
			continue
		}
		key, name := attr(n, "key"), attr(n, "name")
		if key == "" || name == "" || seen[key] {
			continue
		}
		seen[key] = true
		field := projectField{Key: key, Name: name}
		editor, err := url.Parse(attr(n, "editor-url"))
		if err == nil && editor.Path == path+"/list/todo_cell_editor" && !editor.IsAbs() && editor.Host == "" && editor.Query().Get("field_key") == key {
			field.Type = editor.Query().Get("cell_type")
			if field.Type == "select" || field.Type == "multi_select" {
				field.Options, err = client.projectFieldOptions(ctx, editor.RequestURI())
				if err != nil {
					warnings = append(warnings, "无法读取字段 "+key+" 的选项："+err.Error())
				}
			}
		}
		fields = append(fields, field)
	}
	menu, menuErr := client.projectFieldMenu(ctx, path, doc)
	if menuErr != nil {
		warnings = append(warnings, "无法核对字段菜单："+menuErr.Error())
	}
	for i := range fields {
		if fields[i].Type == "" {
			warnings = append(warnings, "无法确定字段 "+fields[i].Key+" 的类型")
		}
		if fields[i].Options == nil {
			fields[i].Options = menu.options[fields[i].Key]
		}
	}
	for _, field := range menu.fields {
		if !seen[field.Key] {
			fields = append(fields, field)
			seen[field.Key] = true
			warnings = append(warnings, "字段菜单中发现 "+field.Key+"，当前列表未提供写入类型")
		}
	}
	if sortKeys == nil {
		return fields, warnings, nil
	}
	for n := range sortKeys.Descendants() {
		if n.Data != "option" {
			continue
		}
		key, name := attr(n, "value"), webNodeText(n)
		if key == "" || name == "" || seen[key] {
			continue
		}
		switch key {
		case "title", "is_completed", "label_ids", "assignee_id", "start_at", "due_at", "priority",
			"team_wide_id", "creator_id", "closed_at", "created_at", "estimated_time", "actual_time":
			continue
		}
		fields = append(fields, projectField{Key: key, Name: name})
		seen[key] = true
		warnings = append(warnings, "字段菜单中发现 "+key+"，当前列表未提供字段类型和选项")
	}
	return fields, warnings, nil
}

type projectFieldMenu struct {
	fields  []projectField
	options map[string][]projectFieldOption
}

func (client *webClient) projectFieldMenu(ctx context.Context, path string, doc *dom.Node) (projectFieldMenu, error) {
	var menu projectFieldMenu
	for n := range doc.Descendants() {
		if n.Data != "tr-todo-combo-filter" {
			continue
		}
		for link := range n.Descendants() {
			linkURL, err := url.Parse(attr(link, "href"))
			if link.Data != "a" || err != nil || linkURL.IsAbs() || linkURL.Host != "" ||
				!strings.HasPrefix(linkURL.Path, path+"/filters/") || !strings.HasSuffix(linkURL.Path, "/lazy_loaders") {
				continue
			}
			linkURL.Path += ".js"
			page, err := client.get(ctx, linkURL.RequestURI())
			if err != nil {
				return menu, err
			}
			return parseProjectFieldMenu(page)
		}
	}
	return menu, errors.New("项目列表没有展示筛选菜单链接")
}

func parseProjectFieldMenu(page string) (projectFieldMenu, error) {
	// Tower wraps an HTML fragment in a Rails escaped JavaScript string.
	_, frag, ok := strings.Cut(page, "var fresh = '")
	frag, _, ended := strings.Cut(frag, "'\n  var old")
	if !ok || !ended {
		return projectFieldMenu{}, errors.New("网页缺少筛选菜单内容")
	}
	doc, err := dom.Parse(strings.NewReader(webJSUnescape.Replace(frag)))
	if err != nil {
		return projectFieldMenu{}, err
	}
	menu := projectFieldMenu{options: make(map[string][]projectFieldOption)}
	seen := map[string]bool{}
	hasFilter := false
	for n := range doc.Descendants() {
		switch n.Data {
		case "tr-project-todo-advance-filter":
			hasFilter = true
		case "option":
			key, custom := strings.CutPrefix(attr(n, "value"), "custom_field_values.")
			if custom && key != "" && !seen[key] {
				seen[key] = true
				menu.fields = append(menu.fields, projectField{Key: key, Name: webNodeText(n)})
			}
		case "template":
			key, custom := strings.CutPrefix(attr(n, "id"), "tpl-todo-filter-custom_field_values__")
			if !custom {
				continue
			}
			for child := range n.Descendants() {
				if child.Data == "select" && strings.HasSuffix(attr(child, "name"), "[form_value][]") {
					menu.options[key] = readProjectFieldOptions(child)
					break
				}
			}
		}
	}
	if !hasFilter {
		return projectFieldMenu{}, errors.New("筛选菜单缺少项目条件定义")
	}
	for i := range menu.fields {
		menu.fields[i].Options = menu.options[menu.fields[i].Key]
	}
	return menu, nil
}

func (client *webClient) projectFieldOptions(ctx context.Context, path string) ([]projectFieldOption, error) {
	page, err := client.get(ctx, path)
	if err != nil {
		return nil, err
	}
	doc, err := dom.Parse(strings.NewReader(page))
	if err != nil {
		return nil, err
	}
	for n := range doc.Descendants() {
		if n.Data != "select" {
			continue
		}
		return readProjectFieldOptions(n), nil
	}
	return nil, errors.New("网页缺少字段选项列表")
}

func readProjectFieldOptions(selectNode *dom.Node) []projectFieldOption {
	options := []projectFieldOption{}
	for option := range selectNode.Descendants() {
		id := attr(option, "value")
		if option.Data == "option" && id != "" && id != "blank" {
			options = append(options, projectFieldOption{ID: id, Name: webNodeText(option)})
		}
	}
	return options
}
