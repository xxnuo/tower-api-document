package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type rawMCPOutput struct {
	dataOnly bool
	fields   []string
}

func executeRawRequest(ctx context.Context, cfg Config, key string, acc *StoredAccount, args map[string]any) (string, bool) {
	method := strings.ToUpper(stringifyArg(args["method"]))
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
	default:
		return "不支持的 HTTP 方法: " + method, true
	}
	path := stringifyArg(args["path"])
	if !strings.HasPrefix(path, "/api/v1/") {
		return "path 必须以 /api/v1/ 开头", true
	}
	query := strings.TrimPrefix(stringifyArg(args["query"]), "?")
	page := parsePositivePage(stringifyArg(args["page"]))
	q, err := url.ParseQuery(query)
	if err != nil {
		return "无效的 query 参数：" + err.Error(), true
	}
	if method == http.MethodGet && isTowerCollectionPath(path, "todolists", "todos") {
		for _, order := range q["order"] {
			if strings.TrimSpace(order) == "-created_at" {
				return fmt.Sprintf("%s %s 不支持 order=-created_at。请使用 list_todos 并传入 order=-created_at，网关会对完整清单排序后分页。", method, path), true
			}
		}
	}
	if page > 0 {
		q.Set("page[number]", strconv.Itoa(page))
	} else {
		page = parsePositivePage(q.Get("page[number]"))
		if page == 0 {
			page = parsePositivePage(q.Get("page"))
		}
	}
	if ps := stringifyArg(args["page_size"]); ps != "" {
		q.Set("page[size]", strconv.Itoa(clampPageSize(ps)))
	}
	if method == http.MethodGet && isTowerCollectionPath(path, "teams", "events") && page > 1 {
		return "团队动态使用 last_item_id 游标分页，请在 query 中传入上一页最后一条动态 ID", true
	}
	query, maxItems := normalizeListQuery(method, path, q.Encode())
	if page == 0 {
		page = 1
	}
	var body []byte
	if obj := args["body"]; obj != nil && method != http.MethodGet && method != http.MethodDelete {
		body, err = json.Marshal(obj)
		if err != nil {
			return fmt.Sprintf("%s %s 序列化请求体失败: %v", method, path, err), true
		}
	}
	output := rawMCPOutput{dataOnly: stringifyArg(args["data_only"]) == "true"}
	switch fields := args["fields"].(type) {
	case []any:
		for _, field := range fields {
			if name, ok := field.(string); ok && strings.TrimSpace(name) != "" {
				output.fields = append(output.fields, strings.TrimSpace(name))
			}
		}
	case []string:
		output.fields = fields
	}
	if args["fields"] != nil && len(output.fields) == 0 {
		return fmt.Sprintf("%s %s 的 fields 至少需要一个非空字段名", method, path), true
	}
	return callTowerForMCP(ctx, cfg, key, acc, "raw_request", method, path, query, body, page, maxItems, false, &output)
}

func (o rawMCPOutput) text(body []byte) (string, bool) {
	if !o.dataOnly && len(o.fields) == 0 {
		return agentText(body), false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if !json.Valid(body) || decoder.Decode(&value) != nil {
		return "Tower API 的响应无法按所选字段提取", true
	}
	project := func(item any) any {
		resource, ok := item.(map[string]any)
		if !ok {
			return item
		}
		attrs, _ := resource["attributes"].(map[string]any)
		out := make(map[string]any, len(o.fields))
		for _, name := range o.fields {
			field, exists := resource[name]
			if !exists {
				field, exists = attrs[name]
			}
			if exists {
				out[name] = field
			}
		}
		return out
	}
	selectFields := func(data any) any {
		if len(o.fields) == 0 {
			return data
		}
		if rows, ok := data.([]any); ok {
			for i, row := range rows {
				rows[i] = project(row)
			}
			return rows
		}
		return project(data)
	}
	if doc, ok := value.(map[string]any); ok {
		if data, exists := doc["data"]; exists {
			if o.dataOnly {
				return renderAgentValue(selectFields(data)), false
			}
			out := map[string]any{"data": selectFields(data)}
			for _, key := range []string{"meta", "links"} {
				if val, ok := doc[key]; ok {
					out[key] = val
				}
			}
			return renderAgentValue(out, "data", "meta", "links"), false
		}
	}
	return renderAgentValue(selectFields(value)), false
}

func isTowerCollectionPath(path, parent, collection string) bool {
	parts := strings.Split(strings.TrimRight(path, "/"), "/")
	return len(parts) == 6 && parts[0] == "" && parts[1] == "api" && parts[2] == "v1" &&
		parts[3] == parent && parts[4] != "" && parts[5] == collection
}

func towerMCPError(method, path string, status int, body []byte, err error) string {
	path, _, _ = strings.Cut(path, "?")
	if err != nil {
		var requestErr *url.Error
		if errors.As(err, &requestErr) {
			err = requestErr.Err
		}
		return fmt.Sprintf("请求 Tower API %s %s 失败: %v", method, path, err)
	}
	message := fmt.Sprintf("Tower API %s %s 返回错误 HTTP %d:\n%s", method, path, status, agentText(body))
	if status == http.StatusNotFound {
		message += "\n仅凭 HTTP 404 无法区分路径无效、资源不存在或当前账号不可见。"
	}
	if status == http.StatusNotFound && method == http.MethodGet && isTowerCollectionPath(path, "projects", "todos") {
		message += "\n请使用 list_project_todos 并传入 project_id，或先调用 list_todolists，再用 list_todos 获取各清单任务。"
	}
	return message
}
