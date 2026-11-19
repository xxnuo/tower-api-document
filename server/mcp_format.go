package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Lists are used to choose a resource. Detail calls retain its full attributes.
var listFields = map[string]string{
	"teams": "name", "projects": "name is_archived", "todolists": "name is_archived is_default",
	"members": "nickname role is_active", "todos": "content team_wide_id is_completed completed created_at updated_at due_at priority",
	"topics": "subject title created_at", "messages": "subject title created_at",
	"uploads": "name filename", "attachments": "filename byte_size content_type url",
	"comments": "content created_at", "events": "action title ancestor_type ancestor_name created_at",
	"notifications": "subject_str content_str action_str read target_guid notifyable_type created_at extra_info",
	"progresses":    "nickname subject_str action_str content_str progress_ty",
	"time_logs":     "duration starts_at ends_at desc",
}

func compactResource(item map[string]any, summary bool, included map[[2]string]map[string]any) map[string]any {
	attrs, _ := item["attributes"].(map[string]any)
	typ, _ := item["type"].(string)
	if attrs == nil && item["relationships"] == nil {
		return item
	}
	out := map[string]any{"id": item["id"]}
	fields := listFields[typ]
	for k, v := range attrs {
		if v == nil || v == "" {
			continue
		}
		if summary && fields != "" && !strings.Contains(" "+fields+" ", " "+k+" ") {
			continue
		}
		out[k] = v
	}
	rels, _ := item["relationships"].(map[string]any)
	for k, raw := range rels {
		if summary && !strings.Contains(" assignee project todolist creator attachment attachments message progress todo labels ", " "+k+" ") {
			continue
		}
		rel, _ := raw.(map[string]any)
		ref := func(raw any) any {
			r, _ := raw.(map[string]any)
			id, _ := r["id"].(string)
			t, _ := r["type"].(string)
			if id == "" {
				return raw
			}
			if obj := included[[2]string{t, id}]; obj != nil {
				return compactResource(obj, listFields[t] != "" && (summary || t == "attachments" || t == "members" || t == "projects" || t == "todos" || t == "todolists"), nil)
			}
			return id
		}
		switch d := rel["data"].(type) {
		case []any:
			if len(d) > 0 || (!summary && k == "sub_todos") {
				refs := make([]any, 0, len(d))
				for _, r := range d {
					refs = append(refs, ref(r))
				}
				out[k] = refs
			}
		case map[string]any:
			out[k] = ref(d)
		}
	}
	return out
}

func compactTowerBody(raw []byte, tool string) any {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !json.Valid(raw) {
		return string(raw)
	}
	if dec.Decode(&value) != nil {
		return string(raw)
	}
	doc, ok := value.(map[string]any)
	if rows, isArray := value.([]any); isArray {
		doc = map[string]any{"data": rows}
	} else if !ok {
		return value
	}
	selectDetail := func(record map[string]any) map[string]any {
		if tool == "get_todo" || tool == "get_todo_by_number" {
			if children, ok := record["sub_todos"].([]any); ok {
				record["sub_todo_count"] = len(children)
			}
		}
		return resultProfiles[tool].selectFields(record)
	}
	data, ok := doc["data"]
	if !ok {
		return selectDetail(doc)
	}
	included := map[[2]string]map[string]any{}
	rows, _ := doc["included"].([]any)
	for _, v := range rows {
		r, _ := v.(map[string]any)
		t, _ := r["type"].(string)
		id, _ := r["id"].(string)
		included[[2]string{t, id}] = r
	}
	switch d := data.(type) {
	case map[string]any:
		out := compactResource(d, false, included)
		if tool == "get_team_resource" && d["type"] != nil {
			out["type"] = d["type"]
		}
		return selectDetail(out)
	case []any:
		items := make([]any, 0, len(d))
		for _, v := range d {
			if r, ok := v.(map[string]any); ok {
				out := compactResource(r, false, included)
				if tool == "list_todos" || (strings.HasPrefix(tool, "list_member_") && strings.HasSuffix(tool, "_todos")) {
					addTodoListDates(out, r)
				}
				v = resultProfiles[tool].selectFields(out)
			}
			items = append(items, v)
		}
		out := map[string]any{"items": items}
		if m, ok := doc["meta"]; ok {
			if meta, ok := m.(map[string]any); ok {
				if more, exists := meta["has_more"]; exists && more == nil {
					meta["has_more"] = "未知"
				}
			}
			out["meta"] = m
		}
		if links, ok := doc["links"].(map[string]any); ok && links["next"] != nil {
			out["next"] = links["next"]
		}
		return out
	}
	return doc
}

func compactTodoItems(rows []map[string]any) []map[string]any {
	for i, r := range rows {
		rows[i] = compactResource(r, true, nil)
		addTodoListDates(rows[i], r)
	}
	return rows
}

// agentText renders Tower values as records and indented sections. Keep the
// leading list record stable: the web inspector uses it for resource pickers.
func agentText(raw []byte) string {
	return resultProfile{fields: "*"}.compositeText(raw)
}

func (p resultProfile) compositeText(raw []byte) string {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if !json.Valid(raw) || d.Decode(&value) != nil {
		return strings.TrimSpace(string(raw))
	}
	return p.text(value)
}

func renderAgentValue(value any, preferred ...string) string {
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for _, k := range preferred {
			if _, ok := v[k]; ok {
				keys = append(keys, k)
			}
		}
		n := len(keys)
		for k := range v {
			if !slices.Contains(preferred, k) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys[n:])
		lines := make([]string, 0, len(keys))
		for _, k := range keys {
			field := renderAgentValue(v[k])
			if raw, ok := v[k].(string); ok && (strings.HasSuffix(k, "_at") || k == "datetime" || k == "result_time") {
				if date := towerDateValue(raw); date != "" {
					field = date
				}
			}
			switch v[k].(type) {
			case map[string]any, []any:
				lines = append(lines, k+"：\n  "+strings.ReplaceAll(field, "\n", "\n  "))
			default:
				lines = append(lines, k+"："+strings.ReplaceAll(field, "\n", "\n  "))
			}
		}
		if len(lines) == 0 {
			return "无字段"
		}
		return strings.Join(lines, "\n")
	case []any:
		if len(v) == 0 {
			return "无记录"
		}
		lines := make([]string, 0, len(v))
		for _, item := range v {
			heading := ""
			if r, ok := item.(map[string]any); ok && r["id"] != nil {
				heading = "ID " + fmt.Sprint(r["id"])
				r = maps.Clone(r)
				delete(r, "id")
				for _, k := range []string{"name", "nickname", "subject", "title", "content", "filename", "desc"} {
					if s, ok := r[k].(string); ok && s != "" && !strings.ContainsAny(s, "\r\n") {
						heading += " | " + s
						delete(r, k)
						break
					}
				}
				if len(r) > 0 {
					heading += "\n" + renderAgentValue(r)
				}
			} else {
				heading = renderAgentValue(item)
			}
			lines = append(lines, "- "+strings.ReplaceAll(heading, "\n", "\n  "))
		}
		return strings.Join(lines, "\n")
	case bool:
		if v {
			return "是"
		}
		return "否"
	case nil:
		return "无"
	default:
		return fmt.Sprint(v)
	}
}
