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
	omitted := local && mcpListHasOmittedRows(doc, len(items))
	delete(meta, "next_page")
	delete(meta, "next_cursor")
	meta["page_size"] = size
	meta["has_more"] = nil
	total, totalKnown := mcpListTotal(meta)
	if omitted && meta["scope_complete"] != false {
		meta["scope_complete"] = false
		meta["incomplete_reason"] = "Tower 只返回了部分记录，无法通过本地翻页获取其余记录"
	}
	if meta["truncated"] == true && meta["incomplete_reason"] == nil {
		meta["incomplete_reason"] = "Tower 标记当前列表已截断"
	}
	if meta["scope_complete"] == false && meta["incomplete_reason"] == nil {
		meta["incomplete_reason"] = meta["scope_warning"]
		if meta["incomplete_reason"] == nil {
			meta["incomplete_reason"] = "当前列表只覆盖部分查询范围"
		}
	}
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
			case totalKnown && total <= len(items) && meta["scope_complete"] != false:
				meta["has_more"] = false
			}
		} else {
			next, hasNextLink := links["next"]
			lastPage := mcpLinkedPage(links["last"])
			if lastPage == 0 {
				lastPage = parsePositivePage(stringifyArg(meta["total_pages"]))
			}
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
		meta["incomplete_reason"] = "Tower 返回的本页记录超过请求大小，超出部分已省略"
	}
	if cursorQuery != nil && meta["has_more"] != false && meta["next_cursor"] == nil {
		meta["incomplete_reason"] = "Tower 未返回可继续翻页的动态 ID"
	}
	doc["data"] = sliced
	doc["meta"] = meta
	out, _ := json.Marshal(doc)
	return out
}

// Only explicit upstream evidence proves that records outside this page exist.
func mcpListHasOmittedRows(doc map[string]any, returned int) bool {
	meta, _ := doc["meta"].(map[string]any)
	links, _ := doc["links"].(map[string]any)
	total, known := mcpListTotal(meta)
	pages := parsePositivePage(stringifyArg(meta["total_pages"]))
	return meta["has_more"] == true || meta["scope_complete"] == false || meta["truncated"] == true ||
		stringifyArg(links["next"]) != "" || mcpLinkedPage(links["last"]) > 1 ||
		pages > 1 || known && total > returned
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
func unwrapMemberTodoList(raw []byte, memberID string) ([]byte, bool) {
	doc, existing := decodeMCPList(raw)
	if existing != nil {
		if !mcpListHasOmittedRows(doc, len(existing)) {
			return raw, true
		}
		meta, _ := doc["meta"].(map[string]any)
		if meta == nil {
			meta = map[string]any{}
			doc["meta"] = meta
		}
		meta["scope_complete"] = false
		meta["incomplete_reason"] = "Tower 成员任务关系只返回了部分记录"
		out, _ := json.Marshal(doc)
		return out, false
	}
	member, _ := doc["data"].(map[string]any)
	if stringifyArg(member["id"]) != memberID {
		return nil, false
	}
	rels, _ := member["relationships"].(map[string]any)
	todos, _ := rels["todos"].(map[string]any)
	refs, ok := todos["data"].([]any)
	if !ok {
		return nil, false
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
	complete := !mcpListHasOmittedRows(todos, len(refs)) && !mcpListHasOmittedRows(doc, len(refs))
	reason := "Tower 成员任务关系只返回了部分记录"
	for _, row := range refs {
		ref, _ := row.(map[string]any)
		key := resourceKey{stringifyArg(ref["type"]), stringifyArg(ref["id"])}
		if item := resources[key]; item != nil {
			row = item
		} else {
			complete = false
			reason = "Tower 成员任务关系有记录缺少详情"
		}
		items = append(items, row)
	}
	doc["data"] = items
	if !complete {
		doc["meta"] = map[string]any{
			"scope_complete":    false,
			"scope_warning":     reason,
			"incomplete_reason": reason,
		}
	}
	out, _ := json.Marshal(doc)
	return out, complete
}

func loadMemberTodos(ctx context.Context, cfg Config, mcpKey string, acc *StoredAccount, path string) (int, []byte, error) {
	return loadScopedMemberTodos(ctx, cfg, mcpKey, acc, path, "all")
}

func loadMemberUncompletedTodos(ctx context.Context, cfg Config, mcpKey string, acc *StoredAccount, path string) (int, []byte, error) {
	path = strings.TrimSuffix(path, "_uncompleted_todos") + "_todos"
	return loadScopedMemberTodos(ctx, cfg, mcpKey, acc, path, "uncompleted")
}

func loadScopedMemberTodos(ctx context.Context, cfg Config, mcpKey string, acc *StoredAccount, path, status string) (int, []byte, error) {
	fetch := func(path, query string) (int, []byte, error) {
		return doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, path, query, nil)
	}
	return fetchScopedMemberTodos(fetch, path, status)
}

func scopedMemberTodoRows(rows []any, memberID, relation, status string) ([]any, bool) {
	items := make([]any, 0, len(rows))
	complete := true
	for _, row := range rows {
		item, _ := row.(map[string]any)
		rels, _ := item["relationships"].(map[string]any)
		link, _ := rels[relation].(map[string]any)
		member, _ := link["data"].(map[string]any)
		if stringifyArg(member["id"]) != memberID || stringifyArg(item["id"]) == "" {
			complete = false
			continue
		}
		if status != "all" {
			attrs, _ := item["attributes"].(map[string]any)
			completed, hasCompleted := attrs["is_completed"].(bool)
			if !hasCompleted {
				completed, hasCompleted = attrs["completed"].(bool)
			}
			active, hasActive := attrs["is_active"].(bool)
			if !hasCompleted || status == "uncompleted" && !completed && !hasActive {
				complete = false
				continue
			}
			if status == "completed" && !completed || status == "uncompleted" && (completed || !active) {
				continue
			}
		}
		items = append(items, row)
	}
	return items, complete
}

func fetchScopedMemberTodos(fetch func(path, query string) (int, []byte, error), path, status string) (int, []byte, error) {
	prefix, endpoint, _ := strings.Cut(strings.TrimPrefix(path, "/api/v1/members/"), "/")
	memberID, err := url.PathUnescape(prefix)
	if err != nil || memberID == "" {
		return http.StatusBadRequest, nil, fmt.Errorf("成员 ID 无效")
	}
	relation := "assignee"
	if endpoint == "created_todos" {
		relation = "creator"
	}
	included := make(map[[2]string]map[string]any)
	rememberIncluded := func(doc map[string]any) {
		resources, _ := doc["included"].([]any)
		for _, value := range resources {
			row, _ := value.(map[string]any)
			typ, id := stringifyArg(row["type"]), stringifyArg(row["id"])
			if typ != "" && typ != "todos" && id != "" {
				included[[2]string{typ, id}] = row
			}
		}
	}
	makeBody := func(items []any, reason string) []byte {
		meta := map[string]any{"scope_complete": reason == ""}
		if reason == "" {
			meta["total"] = len(items)
		} else {
			meta["verified_count"] = len(items)
			meta["scope_warning"] = reason
			meta["incomplete_reason"] = reason
		}
		related := make([]any, 0, len(included))
		addRelated := func(value any) {
			ref, _ := value.(map[string]any)
			key := [2]string{stringifyArg(ref["type"]), stringifyArg(ref["id"])}
			if row := included[key]; row != nil {
				related = append(related, row)
				delete(included, key)
			}
		}
		for _, value := range items {
			item := value.(map[string]any)
			rels, _ := item["relationships"].(map[string]any)
			for _, v := range rels {
				link, _ := v.(map[string]any)
				if refs, ok := link["data"].([]any); ok {
					for _, ref := range refs {
						addRelated(ref)
					}
				} else {
					addRelated(link["data"])
				}
			}
		}
		body, _ := json.Marshal(map[string]any{"data": items, "meta": meta, "included": related})
		return body
	}
	code, raw, err := fetch(path, "")
	items := make([]any, 0)
	if err == nil && code >= 200 && code < 300 {
		unwrapped, complete := unwrapMemberTodoList(raw, memberID)
		doc, rows := decodeMCPList(unwrapped)
		if rows != nil {
			rememberIncluded(doc)
			var verified bool
			items, verified = scopedMemberTodoRows(rows, memberID, relation, status)
			if complete && verified {
				return code, makeBody(items, ""), nil
			}
		}
	}

	// shortcut: the uncompleted endpoint can omit tasks, replace it when Tower supports cross-member full relationships.
	reason := "Tower 全量接口未能确认该成员的完整任务关系，当前结果仅包含已核验归属的任务"
	seen := make(map[string]bool)
	for _, row := range items {
		seen[stringifyArg(row.(map[string]any)["id"])] = true
	}
	usable := len(items) > 0
	kind := strings.TrimSuffix(path, "_todos")
	for _, subset := range []string{"uncompleted", "completed"} {
		if status != "all" && status != subset {
			continue
		}
		query, page := "", 1
		for {
			fallbackCode, fallbackRaw, fallbackErr := fetch(kind+"_"+subset+"_todos", query)
			if fallbackErr != nil || fallbackCode < 200 || fallbackCode >= 300 {
				if status != "all" && !usable {
					return fallbackCode, fallbackRaw, fallbackErr
				}
				reason += fmt.Sprintf("，%s 列表第 %d 页未能读取", subset, page)
				break
			}
			doc, rows := decodeMCPList(fallbackRaw)
			if rows == nil {
				break
			}
			usable = true
			rememberIncluded(doc)
			// The dedicated endpoint already filters by status. Verify every row's member.
			matches, _ := scopedMemberTodoRows(rows, memberID, relation, "all")
			for _, row := range matches {
				id := stringifyArg(row.(map[string]any)["id"])
				if !seen[id] {
					items = append(items, row)
					seen[id] = true
				}
			}
			links, _ := doc["links"].(map[string]any)
			next := mcpLinkedPage(links["next"])
			if subset == "uncompleted" || next <= page {
				break
			}
			page = next
			query = url.Values{"page[number]": []string{strconv.Itoa(page)}}.Encode()
		}
	}
	if !usable {
		if err != nil || code < 200 || code >= 300 {
			return code, raw, err
		}
		return http.StatusBadGateway, nil, fmt.Errorf("Tower 未返回可验证归属的成员任务列表")
	}
	return http.StatusOK, makeBody(items, reason), nil
}
