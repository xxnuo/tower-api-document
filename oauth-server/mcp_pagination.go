package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func decodeMCPList(raw []byte) (map[string]any, []any) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if !json.Valid(raw) || decoder.Decode(&value) != nil {
		return nil, nil
	}
	switch v := value.(type) {
	case map[string]any:
		items, _ := v["data"].([]any)
		return v, items
	case []any:
		return map[string]any{"data": v}, v
	default:
		return nil, nil
	}
}

func paginateMCPList(raw []byte, page, size int, local bool, cursorQuery url.Values) []byte {
	doc, items := decodeMCPList(raw)
	if items == nil {
		return raw
	}
	links, _ := doc["links"].(map[string]any)
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	upstreamMore, hasUpstreamMore := meta["has_more"].(bool)
	delete(meta, "next_page")
	delete(meta, "next_cursor")
	meta["page_size"] = size
	meta["has_more"] = nil
	total, totalKnown := mcpListTotal(meta)
	if totalKnown {
		meta["total"] = total
	} else {
		delete(meta, "total")
	}

	sliced := slicePageItems(items, page, size, local)
	meta["returned"] = len(sliced)
	if cursorQuery != nil {
		switch {
		case hasUpstreamMore:
			meta["has_more"] = upstreamMore
		case len(items) < size:
			meta["has_more"] = false
		}
		if len(sliced) > 0 && meta["has_more"] != false {
			last, _ := sliced[len(sliced)-1].(map[string]any)
			id, err := strconv.ParseInt(stringifyArg(last["id"]), 10, 64)
			current, _ := strconv.ParseInt(cursorQuery.Get("last_item_id"), 10, 64)
			if err == nil && id > 0 && (current <= 0 || id < current) {
				meta["next_cursor"] = id
			}
		}
	} else {
		meta["page"] = page
		if local {
			if !totalKnown && meta["scope_complete"] != false {
				total, totalKnown = len(items), true
				meta["total"] = total
			}
			lastAvailablePage := (len(items) + size - 1) / size
			switch {
			case page < lastAvailablePage:
				meta["has_more"] = true
				meta["next_page"] = page + 1
			case totalKnown && total <= len(items):
				meta["has_more"] = false
			}
		} else {
			next, hasNextLink := links["next"]
			lastPage := mcpLinkedPage(links["last"])
			switch {
			case hasNextLink:
				meta["has_more"] = stringifyArg(next) != ""
				if p := mcpLinkedPage(next); p > page {
					meta["next_page"] = p
				}
			case hasUpstreamMore:
				meta["has_more"] = upstreamMore
			case lastPage > 0:
				meta["has_more"] = page < lastPage
			case totalKnown:
				meta["has_more"] = total > 0 && page <= (total-1)/size
			case len(items) < size:
				meta["has_more"] = false
			}
			if !totalKnown && page == 1 && meta["has_more"] == false && (hasNextLink || hasUpstreamMore || lastPage == 1) {
				meta["total"] = len(items)
			}
			if meta["has_more"] != false && meta["next_page"] == nil && page < math.MaxInt {
				meta["next_page"] = page + 1
			}
		}
	}
	if !local && len(items) > size {
		meta["truncated"] = true
	}
	doc["data"] = sliced
	doc["meta"] = meta
	out, _ := json.Marshal(doc)
	return out
}

func mcpLinkedPage(link any) int {
	if obj, ok := link.(map[string]any); ok {
		link = obj["href"]
	}
	u, err := url.Parse(stringifyArg(link))
	if err != nil {
		return 0
	}
	for _, name := range []string{"page[number]", "page"} {
		if page := parsePositivePage(u.Query().Get(name)); page > 0 {
			return page
		}
	}
	return 0
}

func mcpListTotal(meta map[string]any) (int, bool) {
	for _, key := range []string{"total", "total_count"} {
		if raw := stringifyArg(meta[key]); raw != "" {
			if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
				return n, true
			}
		}
	}
	return 0, false
}

// The two member-wide todo endpoints return a member resource and todo linkage.
func unwrapMemberTodoList(raw []byte) ([]byte, bool) {
	doc, existing := decodeMCPList(raw)
	if existing != nil {
		meta, _ := doc["meta"].(map[string]any)
		links, _ := doc["links"].(map[string]any)
		knownTotal, hasTotal := mcpListTotal(meta)
		return raw, stringifyArg(links["next"]) == "" && meta["has_more"] != true && (!hasTotal || knownTotal <= len(existing))
	}
	member, _ := doc["data"].(map[string]any)
	rels, _ := member["relationships"].(map[string]any)
	todos, _ := rels["todos"].(map[string]any)
	refs, ok := todos["data"].([]any)
	if !ok {
		return raw, false
	}
	type resourceKey struct{ typ, id string }
	included, _ := doc["included"].([]any)
	resources := make(map[resourceKey]map[string]any, len(included))
	for _, row := range included {
		item, _ := row.(map[string]any)
		key := resourceKey{stringifyArg(item["type"]), stringifyArg(item["id"])}
		resources[key] = item
	}
	items := make([]any, 0, len(refs))
	complete := true
	for _, row := range refs {
		ref, _ := row.(map[string]any)
		key := resourceKey{stringifyArg(ref["type"]), stringifyArg(ref["id"])}
		if item := resources[key]; item != nil {
			row = item
		} else {
			complete = false
		}
		items = append(items, row)
	}
	doc["data"] = items
	if !complete {
		doc["meta"] = map[string]any{
			"scope_complete": false,
			"scope_warning":  "Tower 成员任务关系有记录缺少详情",
		}
	}
	out, _ := json.Marshal(doc)
	return out, complete
}

// loadMemberUncompletedTodos uses the complete member relationship when it is
// accessible. The dedicated endpoint can omit tasks even below its 200-row cap.
func loadMemberUncompletedTodos(ctx context.Context, cfg Config, mcpKey string, acc *StoredAccount, path string) (int, []byte, error) {
	allPath := strings.TrimSuffix(path, "_uncompleted_todos") + "_todos"
	code, raw, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, allPath, "", nil)
	if err == nil && code >= 200 && code < 300 {
		unwrapped, complete := unwrapMemberTodoList(raw)
		doc, rows := decodeMCPList(unwrapped)
		if complete && rows != nil {
			items := make([]any, 0, len(rows))
			for _, row := range rows {
				item, _ := row.(map[string]any)
				attrs, _ := item["attributes"].(map[string]any)
				active, hasActive := attrs["is_active"].(bool)
				completed, hasCompleted := attrs["is_completed"].(bool)
				if !hasActive || !hasCompleted {
					complete = false
					break
				}
				if active && !completed {
					items = append(items, item)
				}
			}
			if complete {
				doc["data"] = items
				doc["meta"] = map[string]any{"scope_complete": true, "total": len(items)}
				delete(doc, "links")
				body, _ := json.Marshal(doc)
				return code, body, nil
			}
		}
	}

	code, raw, err = doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, path, "", nil)
	if err != nil || code < 200 || code >= 300 {
		return code, raw, err
	}
	doc, rows := decodeMCPList(raw)
	if rows == nil {
		return code, raw, fmt.Errorf("Tower 返回的成员待办格式无效或超出响应大小限制")
	}
	doc["meta"] = map[string]any{
		"scope_complete": false,
		"scope_warning":  "未能确认成员全部任务关系，当前结果来自 Tower 可能省略任务的专用待办接口。",
	}
	body, _ := json.Marshal(doc)
	return code, body, nil
}
