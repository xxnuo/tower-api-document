package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

var towerTimeZone = time.FixedZone("UTC+08:00", 8*60*60)

// Keep the precision supplied by Tower, including date-only due dates.
func towerDateValue(raw string) string {
	if _, err := time.Parse(time.DateOnly, raw); err == nil {
		return raw
	}
	if len(raw) > 10 && raw[10] == ' ' {
		raw = raw[:10] + "T" + raw[11:]
	}
	if len(raw) > 5 && (raw[len(raw)-5] == '+' || raw[len(raw)-5] == '-') {
		raw = raw[:len(raw)-2] + ":" + raw[len(raw)-2:]
	}
	if _, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return strings.ReplaceAll(raw, ",", ".")
	}
	return ""
}

func addTodoListDates(out, item map[string]any) {
	attrs, _ := item["attributes"].(map[string]any)
	if attrs == nil {
		attrs = item
	}
	for _, key := range []string{"created_at", "updated_at", "due_at"} {
		raw, exists := attrs[key]
		str, _ := raw.(string)
		value := towerDateValue(str)
		if value == "" {
			value = "上游未提供"
			if key == "due_at" && exists && (raw == nil || raw == "") {
				value = "无截止日期"
			}
		}
		out[key] = value
	}
}

func todoDueCutoff(args map[string]any, now time.Time) (string, error) {
	var before string
	if raw, ok := args["due_before"]; ok && raw != nil {
		var isString bool
		before, isString = raw.(string)
		if _, err := time.Parse(time.DateOnly, before); !isString || err != nil {
			return "", fmt.Errorf("due_before 须为有效的 YYYY-MM-DD 日期")
		}
	}
	if raw, ok := args["overdue"]; ok && raw != nil {
		overdue, isBool := raw.(bool)
		if !isBool {
			return "", fmt.Errorf("overdue 须为布尔值")
		}
		today := now.In(towerTimeZone).Format(time.DateOnly)
		if overdue && (before == "" || today < before) {
			before = today
		}
	}
	return before, nil
}

func todoDueDay(value any) string {
	raw, _ := value.(string)
	date := towerDateValue(raw)
	if len(date) == len(time.DateOnly) {
		return date
	}
	if parsed, err := time.Parse(time.RFC3339Nano, date); err == nil {
		return parsed.In(towerTimeZone).Format(time.DateOnly)
	}
	return ""
}

func executeDueTodoList(ctx context.Context, cfg Config, mcpKey string, acc *StoredAccount, tool, path, cutoff string, page, size int) (string, bool) {
	code, body, err := loadMemberUncompletedTodos(ctx, cfg, mcpKey, acc, path)
	if err != nil {
		return "查询成员待办失败: " + err.Error(), true
	}
	if code < 200 || code >= 300 {
		return fmt.Sprintf("查询成员待办返回 HTTP %d:\n%s", code, agentText(body)), true
	}
	doc, rows := decodeMCPList(body)
	if rows == nil {
		return "无法读取 Tower 返回的成员待办列表", true
	}
	matches := make([]any, 0, len(rows))
	unknown := 0
	for _, row := range rows {
		item, _ := row.(map[string]any)
		attrs, _ := item["attributes"].(map[string]any)
		if attrs == nil {
			attrs = item
		}
		due, present := attrs["due_at"]
		day := todoDueDay(due)
		if !present || (day == "" && due != nil && due != "") {
			unknown++
		}
		if day != "" && day < cutoff {
			matches = append(matches, item)
		}
	}
	upstreamMeta, _ := doc["meta"].(map[string]any)
	scopeComplete := upstreamMeta["scope_complete"] == true
	meta := map[string]any{
		"due_before": cutoff, "timezone": towerTimeZone.String(),
		"scanned_count": len(rows), "matched_count": len(matches),
		"scope_complete": scopeComplete && unknown == 0,
	}
	if !scopeComplete {
		meta["scope_warning"] = upstreamMeta["scope_warning"]
	}
	if unknown > 0 {
		meta["due_date_unavailable_count"] = unknown
	}
	filtered, _ := json.Marshal(map[string]any{"data": matches, "included": doc["included"], "meta": meta})
	return resultProfiles[tool].text(compactTowerBody(paginateMCPList(filtered, page, size, true, nil), tool)), false
}
