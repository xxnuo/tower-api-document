package main

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Lists are used to choose a resource. Detail calls retain its full attributes.
var listFields = map[string]string{
	"teams": "name", "projects": "name is_archived", "todolists": "name is_archived is_default",
	"members": "nickname role is_active", "todos": "content team_wide_id is_completed completed due_at priority",
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
				return compactResource(obj, listFields[t] != "" && (summary || t == "members" || t == "projects" || t == "todos" || t == "todolists"), nil)
			}
			return id
		}
		switch d := rel["data"].(type) {
		case []any:
			if len(d) > 0 {
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
	var doc map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !json.Valid(raw) {
		return string(raw)
	}
	if dec.Decode(&doc) != nil || doc == nil {
		return json.RawMessage(raw)
	}
	data, ok := doc["data"]
	if !ok {
		return doc
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
		if tool == "tower_get_team_resource" && d["type"] != nil {
			out["type"] = d["type"]
		}
		return out
	case []any:
		items := make([]any, 0, len(d))
		for _, v := range d {
			if r, ok := v.(map[string]any); ok {
				v = compactResource(r, true, included)
			}
			items = append(items, v)
		}
		out := map[string]any{"items": items}
		if m, ok := doc["meta"]; ok {
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
	}
	return rows
}
