package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	dom "golang.org/x/net/html"
)

var gatewayScheduleTools = []mcpToolSpec{
	{
		Name: "get_todo_schedule", Title: "读取任务排期设置",
		Desc:   "读取任务的预估工时（分钟）、里程碑和循环设置。需要网页登录。",
		Method: http.MethodGet, PathTemplate: "/api/v1/todos/{todo_id}/schedule",
		Params: []mcpParam{pPath("todo_id", "任务 GUID")},
	},
	{
		Name: "set_todo_estimated_time", Title: "设置任务预估工时",
		Desc:   "按分钟设置任务预估工时，设为 0 可清空。需要网页登录。",
		Method: http.MethodPut, PathTemplate: "/api/v1/todos/{todo_id}/estimated_time",
		Params: []mcpParam{pPath("todo_id", "任务 GUID"), pBody("estimated_minutes", "integer", true, "预估分钟数，0 至 120000")},
	},
	{
		Name: "set_todo_milestone", Title: "设置或取消任务里程碑",
		Desc:   "设置或取消已有任务的里程碑状态。需要网页登录。",
		Method: http.MethodPut, PathTemplate: "/api/v1/todos/{todo_id}/milestone",
		Params: []mcpParam{pPath("todo_id", "任务 GUID"), pBody("enabled", "boolean", true, "true 转为里程碑，false 取消")},
	},
	{
		Name: "set_todo_recurrence", Title: "设置或取消任务循环",
		Desc:   "设置已有截止时间的任务的循环规则，或用 enabled=false 取消后续循环。需要网页登录。",
		Method: http.MethodPut, PathTemplate: "/api/v1/todos/{todo_id}/recurrence",
		Params: []mcpParam{
			pPath("todo_id", "任务 GUID"),
			pBody("enabled", "boolean", true, "true 开启或更新循环，false 取消"),
			pBody("unit", "string", false, "开启时必填，day、week 或 month"),
			pBody("interval", "integer", false, "开启时必填，day 为 1–7，week 和 month 为 1–12"),
			pBody("selected_days", "string_array", false, "week 填 1–7（周一至周日），month 填 1–28 或 l（月末），day 留空"),
		},
	},
}

type todoRecurrence struct {
	Active   bool   `json:"is_active"`
	Unit     string `json:"unit"`
	Interval int    `json:"interval"`
	Selected []any  `json:"selected_value"`
}

type todoSchedulePage struct {
	editPath     string
	selectorPath string
	canEditDates bool
	dates        string
	milestone    *bool
	canMilestone bool
	estimate     *int
	canEstimate  bool
}

var todoDateSelectorPath = regexp.MustCompile(`^/todos/[0-9]+/date_time_selector$`)
var todoCycleSetting = regexp.MustCompile(`<tr-date-time-picker-field-list[^>]*\scycle-setting=\\?"([^"]*?)\\?"`)

func readTodoSchedulePage(page, todoID string) (todoSchedulePage, error) {
	doc, err := dom.Parse(strings.NewReader(page))
	if err != nil {
		return todoSchedulePage{}, err
	}
	state := todoSchedulePage{}
	found := false
	for node := range doc.Descendants() {
		if node.Type != dom.ElementNode {
			continue
		}
		switch node.Data {
		case "tr-todo-page-main":
			if attr(node, "guid") != todoID {
				continue
			}
			found = true
			state.editPath = attr(node, "edit-url")
			for control := range node.Descendants() {
				if control.Data != "a" || !slices.Contains(strings.Fields(attr(control, "class")), "date-time-selector") {
					continue
				}
				if path := attr(control, "href"); todoDateSelectorPath.MatchString(path) {
					state.selectorPath = path
					state.canEditDates = attr(control, "data-access") == "true"
					for input := range control.Descendants() {
						if input.Data == "input" && attr(input, "type") == "hidden" {
							state.dates = attr(input, "value")
							break
						}
					}
				}
			}
		case "a":
			if !slices.Contains(strings.Fields(attr(node, "class")), "milestone-button") || attr(node, "href") != "/todos/"+todoID+"/milestone" {
				continue
			}
			method := attr(node, "data-method")
			if method == "post" || method == "delete" {
				enabled := method == "delete"
				state.milestone = &enabled
				state.canMilestone = attr(node, "data-access") == "true"
			}
		case "tr-react-task-page-working-hours":
			var work struct {
				ID       string `json:"todo_guid"`
				Estimate *int   `json:"estimated_time"`
				CanEdit  bool   `json:"can_edit_todo"`
			}
			if json.Unmarshal([]byte(attr(node, "work-hours")), &work) == nil && work.ID == todoID {
				state.estimate, state.canEstimate = work.Estimate, work.CanEdit
			}
		}
	}
	if !found {
		return todoSchedulePage{}, errors.New("网页未返回指定任务，请确认登录状态和权限")
	}
	return state, nil
}

func (client *webClient) readTodoRecurrence(ctx context.Context, path string) (todoRecurrence, error) {
	if path == "" {
		return todoRecurrence{}, errors.New("任务页没有提供循环设置入口")
	}
	// Tower only returns the date selector markup in an AJAX request.
	status, page, err := client.request(ctx, http.MethodGet, client.resolve(path), "", "application/x-www-form-urlencoded")
	if err != nil {
		return todoRecurrence{}, err
	}
	if status != http.StatusOK {
		return todoRecurrence{}, fmt.Errorf("读取循环设置失败 HTTP %d", status)
	}
	var cycle todoRecurrence
	match := todoCycleSetting.FindStringSubmatch(page)
	if len(match) < 2 || json.Unmarshal([]byte(html.UnescapeString(match[1])), &cycle) != nil ||
		cycle.Interval < 1 || !slices.Contains([]string{"day", "week", "month"}, cycle.Unit) {
		return todoRecurrence{}, errors.New("无法确认 Tower 返回的循环设置")
	}
	return cycle, nil
}

func parseTodoRecurrence(args map[string]any, enabled bool) (todoRecurrence, error) {
	cycle := todoRecurrence{Active: enabled, Unit: "day", Interval: 1, Selected: []any{}}
	if !enabled {
		for _, param := range []string{"unit", "interval", "selected_days"} {
			if _, present := args[param]; present {
				return cycle, fmt.Errorf("%w：取消循环时只需传 enabled=false", errWebArguments)
			}
		}
		return cycle, nil
	}
	cycle.Unit = stringifyArg(args["unit"])
	interval, err := strconv.Atoi(stringifyArg(args["interval"]))
	if !slices.Contains([]string{"day", "week", "month"}, cycle.Unit) ||
		err != nil || interval < 1 || interval > 12 || (cycle.Unit == "day" && interval > 7) {
		return cycle, fmt.Errorf("%w：循环单位或间隔不符合 Tower 日期选择器支持的范围", errWebArguments)
	}
	cycle.Interval = interval
	var days []any
	switch value := args["selected_days"].(type) {
	case []any:
		days = value
	case []string:
		for _, d := range value {
			days = append(days, d)
		}
	case nil:
	default:
		return cycle, fmt.Errorf("%w：selected_days 须为数组", errWebArguments)
	}
	if (cycle.Unit == "day") != (len(days) == 0) {
		return cycle, fmt.Errorf("%w：周循环和月循环必须指定循环日，日循环不需要循环日", errWebArguments)
	}
	maxDay := 7
	if cycle.Unit == "month" {
		maxDay = 28
	}
	var numbers []int
	last := false
	for _, day := range days {
		v := stringifyArg(day)
		if v == "l" && cycle.Unit == "month" && !last {
			last = true
			continue
		}
		number, err := strconv.Atoi(v)
		if err != nil || number < 1 || number > maxDay || slices.Contains(numbers, number) {
			return cycle, fmt.Errorf("%w：循环日无效或重复", errWebArguments)
		}
		numbers = append(numbers, number)
	}
	slices.Sort(numbers)
	for _, n := range numbers {
		cycle.Selected = append(cycle.Selected, n)
	}
	if last {
		cycle.Selected = append(cycle.Selected, "l")
	}
	return cycle, nil
}

func (client *webClient) todoSchedule(ctx context.Context, pagePath, todoID, tool string, args map[string]any) (map[string]any, error) {
	var estimate int
	var enabled bool
	var cycle todoRecurrence
	var err error
	switch tool {
	case "set_todo_estimated_time":
		estimate, err = strconv.Atoi(stringifyArg(args["estimated_minutes"]))
		if err != nil || estimate < 0 || estimate > 120000 {
			return nil, fmt.Errorf("%w：estimated_minutes 须为 0 至 120000 的整数", errWebArguments)
		}
	case "set_todo_milestone", "set_todo_recurrence":
		var ok bool
		enabled, ok = args["enabled"].(bool)
		if !ok {
			return nil, fmt.Errorf("%w：enabled 须为布尔值", errWebArguments)
		}
		if tool == "set_todo_recurrence" {
			cycle, err = parseTodoRecurrence(args, enabled)
			if err != nil {
				return nil, err
			}
		}
	case "get_todo_schedule":
	default:
		return nil, errors.New("未知任务排期工具")
	}

	read := func() (todoSchedulePage, error) {
		page, err := client.get(ctx, pagePath)
		if err != nil {
			return todoSchedulePage{}, err
		}
		return readTodoSchedulePage(page, todoID)
	}
	before, err := read()
	if err != nil {
		return nil, err
	}
	result := map[string]any{"todo_id": todoID}
	if tool == "get_todo_schedule" {
		result["recurrence"] = nil
		if before.selectorPath != "" {
			recurrence, err := client.readTodoRecurrence(ctx, before.selectorPath)
			if err != nil {
				return nil, err
			}
			result["recurrence"] = recurrence
		}
		result["milestone"], result["estimated_minutes"] = nil, nil
		if before.milestone != nil {
			result["milestone"] = *before.milestone
		}
		if before.estimate != nil {
			result["estimated_minutes"] = *before.estimate
		}
		return result, nil
	}

	method, path, body := http.MethodPut, "", ""
	contentType := "application/x-www-form-urlencoded; charset=UTF-8"
	switch tool {
	case "set_todo_estimated_time":
		if before.estimate == nil || !before.canEstimate {
			return nil, errors.New("任务页未提供可编辑的预估工时")
		}
		result["estimated_minutes"] = estimate
		if *before.estimate == estimate {
			return result, nil
		}
		path = "/todos/" + todoID + "/estimated_times"
		body = url.Values{"estimated_time": {strconv.Itoa(estimate)}}.Encode()
	case "set_todo_milestone":
		if before.milestone == nil || !before.canMilestone {
			return nil, errors.New("任务页未提供可编辑的里程碑状态")
		}
		result["milestone"] = enabled
		if *before.milestone == enabled {
			return result, nil
		}
		method = http.MethodDelete
		if enabled {
			method = http.MethodPost
		}
		path = "/todos/" + todoID + "/milestone"
	case "set_todo_recurrence":
		current, err := client.readTodoRecurrence(ctx, before.selectorPath)
		if err != nil {
			return nil, err
		}
		if !enabled && !current.Active || sameTodoRecurrence(current, cycle) {
			result["recurrence"] = current
			return result, nil
		}
		parts := strings.Split(before.editPath, "/")
		if !before.canEditDates || len(parts) != 6 || parts[1] != "projects" || !guidPattern.MatchString(parts[2]) ||
			parts[3] != "todos" || parts[4] != todoID || parts[5] != "edit" {
			return nil, errors.New("任务页没有提供可确认的编辑地址")
		}
		if dates := strings.Split(before.dates, ","); enabled && strings.TrimSpace(dates[len(dates)-1]) == "" {
			return nil, fmt.Errorf("%w：请先设置任务截止时间", errWebArguments)
		}
		method, path, contentType = http.MethodPatch, before.editPath, "application/json; charset=UTF-8"
		raw, err := json.Marshal(map[string]any{"todos_management": map[string]any{"cycle_setting": cycle}})
		if err != nil {
			return nil, err
		}
		body = string(raw)
	}
	if client.csrf == "" {
		return nil, errors.New("任务页没有提供 CSRF 令牌")
	}
	status, _, err := client.request(ctx, method, client.resolve(path), body, contentType)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("更新任务排期失败 HTTP %d", status)
	}
	after, err := read()
	if err != nil {
		return nil, err
	}
	switch tool {
	case "set_todo_estimated_time":
		if after.estimate == nil || *after.estimate != estimate {
			return nil, errors.New("更新后未能确认预估工时")
		}
	case "set_todo_milestone":
		if after.milestone == nil || *after.milestone != enabled {
			return nil, errors.New("更新后未能确认里程碑状态")
		}
	case "set_todo_recurrence":
		actual, err := client.readTodoRecurrence(ctx, after.selectorPath)
		if err != nil {
			return nil, err
		}
		if actual.Active != enabled || enabled && !sameTodoRecurrence(actual, cycle) || before.dates != after.dates {
			return nil, errors.New("更新后未能确认循环设置与原有任务日期")
		}
		result["recurrence"] = actual
	}
	return result, nil
}

func sameTodoRecurrence(a, b todoRecurrence) bool {
	return a.Active == b.Active && a.Unit == b.Unit && a.Interval == b.Interval &&
		slices.EqualFunc(a.Selected, b.Selected, func(x, y any) bool { return stringifyArg(x) == stringifyArg(y) })
}
