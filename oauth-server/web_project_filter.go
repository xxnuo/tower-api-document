package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func filterProjectTodos(ctx context.Context, cfg Config, acc *StoredAccount, args map[string]any) (map[string]any, error) {
	if id, ok := args["project_id"].(string); !ok || !guidPattern.MatchString(id) {
		return nil, fmt.Errorf("%w：project_id 须为 Tower GUID", errWebArguments)
	}
	if raw, ok := args["page_size"]; ok {
		size, err := strconv.Atoi(stringifyArg(raw))
		if err != nil || size < 1 || size > maxListPageSize {
			return nil, fmt.Errorf("%w：page_size 范围为 1 到 %d", errWebArguments, maxListPageSize)
		}
	}
	if raw, ok := args["cursor"]; ok {
		if cursor, valid := raw.(string); !valid || cursor == "" {
			return nil, fmt.Errorf("%w：cursor 须为非空字符串", errWebArguments)
		}
	}
	filter, err := newProjectTodoFilter(args)
	if err != nil {
		return nil, err
	}
	result, err := scanProjectTodos(ctx, cfg, acc.MCPAPIKey, acc, args, filter)
	if err != nil {
		return nil, err
	}
	result["source"] = "tower_open_api"
	return result, nil
}

type projectTodoCondition struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

type projectTodoFilter struct {
	Status     string
	Match      string
	Conditions []projectTodoCondition
}

var projectTodoFilterOperators = map[string]string{
	"title":       "in",
	"assignee_id": "in not_in be_nil not_be_nil",
	"creator_id":  "in not_in",
	"closer_id":   "in not_in be_nil not_be_nil",
	"todolist_id": "in not_in",
	"priority":    "in not_in be_nil not_be_nil",
	"label":       "in all_in not_in eq be_nil not_be_nil",
	"start_at":    "eq lt gt be_nil",
	"due_at":      "eq lt gt be_nil",
	"closed_at":   "eq lt gt be_nil",
	"created_at":  "eq lt gt be_nil not_be_nil",
}

func newProjectTodoFilter(args map[string]any) (*projectTodoFilter, error) {
	filter := &projectTodoFilter{Status: "all", Match: "all"}
	for key, allowed := range map[string][]string{"match": {"all", "any"}, "status": {"all", "uncompleted", "completed"}} {
		if v, exists := args[key]; exists {
			value, _ := v.(string)
			if !slices.Contains(allowed, value) {
				return nil, fmt.Errorf("%w：%s 的可选值为 %s", errWebArguments, key, strings.Join(allowed, "、"))
			}
			if key == "match" {
				filter.Match = value
			} else {
				filter.Status = value
			}
		}
	}
	list, ok := args["conditions"].([]any)
	if !ok || len(list) == 0 || len(list) > 20 {
		return nil, fmt.Errorf("%w：conditions 须包含 1 到 20 项查询条件", errWebArguments)
	}
	for i, raw := range list {
		item, _ := raw.(map[string]any)
		field, _ := item["field"].(string)
		op, _ := item["operator"].(string)
		allowed := strings.Fields(projectTodoFilterOperators[field])
		if strings.HasPrefix(field, "custom.") && len(field) > len("custom.") {
			allowed = []string{"in", "not_in", "all_in", "eq", "be_nil", "not_be_nil"}
		}
		if !slices.Contains(allowed, op) {
			return nil, fmt.Errorf("%w：第 %d 项 field 或 operator 无效", errWebArguments, i+1)
		}
		values, err := projectTodoConditionValues(item["value"], field, op)
		if err != nil {
			return nil, fmt.Errorf("%w：第 %d 项 %s", errWebArguments, i+1, err)
		}
		filter.Conditions = append(filter.Conditions, projectTodoCondition{Field: field, Operator: op, Values: values})
	}
	return filter, nil
}

func projectTodoConditionValues(raw any, field, op string) ([]string, error) {
	if op == "be_nil" || op == "not_be_nil" {
		if raw != nil {
			return nil, fmt.Errorf("operator %s 无需 value", op)
		}
		return nil, nil
	}
	var values []string
	switch v := raw.(type) {
	case string:
		values = []string{v}
	case []any:
		for _, x := range v {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("value 须为字符串或字符串列表")
			}
			values = append(values, s)
		}
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("缺少 value")
	}
	for i, v := range values {
		v = strings.TrimSpace(v)
		if field == "label" {
			v = strings.TrimPrefix(v, "#")
		}
		if v == "" {
			return nil, fmt.Errorf("value 不能为空")
		}
		values[i] = v
	}
	if strings.HasSuffix(field, "_at") {
		if _, err := time.Parse(time.DateOnly, values[0]); len(values) != 1 || err != nil {
			return nil, fmt.Errorf("value 须为有效的 YYYY-MM-DD 日期")
		}
	}
	slices.Sort(values)
	return slices.Compact(values), nil
}

func (filter *projectTodoFilter) matches(item map[string]any, detail func() (map[string]any, error)) (bool, error) {
	if filter.Status != "all" {
		completed, ok := item["is_completed"].(bool)
		if !ok {
			completed, ok = item["completed"].(bool)
		}
		if !ok || completed != (filter.Status == "completed") {
			return false, nil
		}
	}
	all := filter.Match == "all"
	_, hasCreationDate := item["created_at"]
	var payload map[string]any
	// Evaluate list fields before reading a task detail, which may include many related resources.
	for _, needDetail := range []bool{false, true} {
		for _, condition := range filter.Conditions {
			key := condition.Field
			if (key == "start_at" || key == "created_at" && !hasCreationDate || strings.HasPrefix(key, "custom.")) != needDetail {
				continue
			}
			value := item[key]
			switch key {
			case "title":
				value = item["content"]
			case "label":
				value = item["labels"]
			case "assignee_id", "creator_id", "closer_id":
				value = item[strings.TrimSuffix(key, "_id")]
			}
			if needDetail {
				if detail == nil {
					return false, fmt.Errorf("缺少任务详情")
				}
				if payload == nil {
					var err error
					payload, err = detail()
					if err != nil {
						return false, err
					}
				}
				switch key {
				case "start_at", "created_at":
					value = payload[key]
					if key == "created_at" {
						item[key] = value
					}
				default:
					custom, _ := payload["custom_fields"].(map[string]any)
					field, _ := custom[strings.TrimPrefix(key, "custom.")].(map[string]any)
					value = field["value"]
				}
			}
			if condition.matches(value) != all {
				return !all, nil
			}
		}
	}
	return all, nil
}

func (condition projectTodoCondition) matches(raw any) bool {
	values := projectTodoValueStrings(raw)
	switch condition.Operator {
	case "be_nil":
		return len(values) == 0
	case "not_be_nil":
		if condition.Field == "created_at" {
			return todoDueDay(raw) != ""
		}
		return len(values) > 0
	}
	if strings.HasSuffix(condition.Field, "_at") {
		date := todoDueDay(raw)
		if date == "" {
			return false
		}
		target := condition.Values[0]
		switch condition.Operator {
		case "eq":
			return date == target
		case "lt":
			return date < target
		case "gt":
			return date > target
		}
	}
	matched := 0
	for _, want := range condition.Values {
		if slices.ContainsFunc(values, func(value string) bool {
			if condition.Field == "title" {
				return strings.Contains(strings.ToLower(value), strings.ToLower(want))
			}
			return value == want
		}) {
			matched++
		}
	}
	switch condition.Operator {
	case "not_in":
		return matched == 0
	case "all_in":
		return matched == len(condition.Values)
	case "eq":
		for _, v := range values {
			if !slices.Contains(condition.Values, v) {
				return false
			}
		}
		return matched == len(condition.Values)
	default:
		return matched > 0
	}
}

func projectTodoValueStrings(raw any) []string {
	switch value := raw.(type) {
	case []any:
		values := make([]string, 0, len(value))
		for _, v := range value {
			values = append(values, projectTodoValueStrings(v)...)
		}
		return values
	case string:
		if value != "" {
			return []string{value}
		}
	case float64, json.Number, bool:
		return []string{fmt.Sprint(value)}
	case map[string]any:
		if id := stringifyArg(value["id"]); id != "" {
			return []string{id}
		}
	}
	return nil
}

func fetchProjectTodoDetail(ctx context.Context, cfg Config, key string, acc *StoredAccount, id string) (map[string]any, error) {
	path := "/api/v1/todos/" + url.PathEscape(id)
	code, body, err := doTowerHTTPWithRefresh(ctx, cfg, key, acc, http.MethodGet, path, "include=custom_field_value", nil)
	if code < 200 || code >= 300 || err != nil {
		return nil, fmt.Errorf("%s", towerMCPError(http.MethodGet, path, code, body, err))
	}
	var doc struct {
		Data     map[string]any
		Included []map[string]any
	}
	if json.Unmarshal(body, &doc) != nil || doc.Data == nil {
		return nil, fmt.Errorf("无法读取任务 %s 的详情", id)
	}
	item := compactResource(doc.Data, false, nil)
	if customID, _ := item["custom_field_value"].(string); customID != "" {
		for _, included := range doc.Included {
			if included["type"] == "todos_custom_field_values" && included["id"] == customID {
				attrs, _ := included["attributes"].(map[string]any)
				item["custom_fields"] = attrs["custom_fields"]
				break
			}
		}
		if item["custom_fields"] == nil {
			return nil, fmt.Errorf("任务 %s 未返回自定义字段值", id)
		}
	}
	return item, nil
}
