package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

func projectTodosToolDefinition() map[string]any {
	return map[string]any{
		"name":        "list_project_todos",
		"description": "分页列出指定项目全部清单中的任务，按任务 ID 去重。保留 project_id 和 completed_todo，把上一页的 next_cursor 传给 cursor 继续。",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"project_id":     map[string]any{"type": "string", "description": "项目 ID"},
				"completed_todo": map[string]any{"type": "boolean", "default": true, "description": "是否包含已完成的任务，默认 true"},
				"page_size":      map[string]any{"type": "integer", "minimum": 1, "maximum": maxListPageSize, "description": "每次最多返回的任务数，默认 20"},
				"cursor":         map[string]any{"type": "string", "description": "上一页返回的 next_cursor"},
			},
			"required": []string{"project_id"},
		},
		"annotations": map[string]any{"title": "列出项目任务", "readOnlyHint": true, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
	}
}

type projectTodoScan struct {
	lists     []string
	listPage  upstreamProjectPage
	listIndex int
	taskPage  upstreamProjectPage
	rows      []map[string]any
	pending   map[string]any
	seen      map[string]bool
}

type upstreamProjectPage struct {
	number              int
	seen                map[[32]byte]bool
	includeFilterFields bool
}

type savedProjectScan struct {
	scan      projectTodoScan
	keyHash   [32]byte
	baseURL   string
	store     *Store
	projectID string
	completed bool
	filterKey string
	expires   time.Time
}

var projectScanCache = struct {
	sync.Mutex
	entries map[string]savedProjectScan
}{entries: make(map[string]savedProjectScan)}

func saveProjectScan(saved savedProjectScan) string {
	token := rand.Text()
	now := time.Now()
	saved.expires = now.Add(15 * time.Minute)
	projectScanCache.Lock()
	defer projectScanCache.Unlock()
	var oldest string
	for token, entry := range projectScanCache.entries {
		if !entry.expires.After(now) {
			delete(projectScanCache.entries, token)
		} else if oldest == "" || entry.expires.Before(projectScanCache.entries[oldest].expires) {
			oldest = token
		}
	}
	if len(projectScanCache.entries) >= 128 {
		delete(projectScanCache.entries, oldest)
	}
	projectScanCache.entries[token] = saved
	return token
}

func (state *upstreamProjectPage) fetch(ctx context.Context, cfg Config, key string, acc *StoredAccount, path string, todos, completed bool) ([]map[string]any, error) {
	query := url.Values{
		"page[number]": {strconv.Itoa(state.number)},
		"page[size]":   {strconv.Itoa(maxListPageSize)},
	}
	if todos && completed {
		query.Set("completed_todo", "true")
	}
	code, body, err := doTowerHTTPWithRefresh(ctx, cfg, key, acc, http.MethodGet, path, query.Encode(), nil)
	if code < 200 || code >= 300 || err != nil {
		return nil, fmt.Errorf("%s", towerMCPError(http.MethodGet, path, code, body, err))
	}
	var doc struct {
		Included []map[string]any           `json:"included"`
		Data     []map[string]any           `json:"data"`
		Links    map[string]json.RawMessage `json:"links"`
		Meta     struct {
			HasMore    *bool `json:"has_more"`
			TotalPages int   `json:"total_pages"`
		} `json:"meta"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Data == nil {
		return nil, fmt.Errorf("GET %s 返回的 data 不是任务或清单数组", path)
	}
	var ids strings.Builder
	for _, row := range doc.Data {
		id, _ := row["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("GET %s 返回了缺少 ID 的记录", path)
		}
		ids.WriteString(strconv.Quote(id))
	}
	signature := sha256.Sum256([]byte(ids.String()))
	repeated := state.seen[signature]
	next, hasNext := doc.Links["next"]
	switch {
	case hasNext && (string(next) == "null" || string(next) == "\"\""),
		doc.Meta.HasMore != nil && !*doc.Meta.HasMore,
		doc.Meta.TotalPages > 0 && state.number >= doc.Meta.TotalPages:
		state.number = 0
	case !hasNext && doc.Meta.HasMore == nil && doc.Meta.TotalPages == 0 && (len(doc.Data) == 0 || repeated && !todos):
		// Tower also exposes unpaginated collections and pages with no links.
		state.number = 0
		return nil, nil
	default:
		if repeated {
			return nil, fmt.Errorf("GET %s 重复返回相同记录，无法继续分页", path)
		}
		state.number++
		if hasNext {
			var href string
			if json.Unmarshal(next, &href) != nil {
				var link struct{ Href string }
				_ = json.Unmarshal(next, &link)
				href = link.Href
			}
			if u, err := url.Parse(href); err == nil {
				n := parsePositivePage(u.Query().Get("page[number]"))
				if n > 0 && n < state.number {
					return nil, fmt.Errorf("GET %s 返回了无法继续的分页链接", path)
				}
				if n > 0 {
					state.number = n
				}
			}
		}
	}
	if state.seen == nil {
		state.seen = make(map[[32]byte]bool)
	}
	state.seen[signature] = true
	if todos {
		included := map[[2]string]map[string]any{}
		for _, obj := range doc.Included {
			included[[2]string{stringifyArg(obj["type"]), stringifyArg(obj["id"])}] = obj
		}
		for i, row := range doc.Data {
			item := compactResource(row, false, included)
			if state.includeFilterFields {
				// Keep the original field values during condition evaluation.
				attrs, _ := row["attributes"].(map[string]any)
				if due, ok := attrs["due_at"]; ok {
					item["due_at"] = due
				}
				doc.Data[i] = item
			} else {
				addTodoListDates(item, row)
				doc.Data[i] = resultProfiles["list_todos"].selectFields(item)
			}
		}
	}
	return doc.Data, nil
}

func executeListProjectTodos(ctx context.Context, cfg Config, key string, acc *StoredAccount, args map[string]any) (string, bool) {
	out, err := scanProjectTodos(ctx, cfg, key, acc, args, nil)
	if err != nil {
		return err.Error(), true
	}
	return resultProfiles["list_project_todos"].text(out), false
}

func scanProjectTodos(ctx context.Context, cfg Config, key string, acc *StoredAccount, args map[string]any, filter *projectTodoFilter) (map[string]any, error) {
	projectID := stringifyArg(args["project_id"])
	if projectID == "" {
		return nil, fmt.Errorf("缺少必填参数 project_id")
	}
	completed := stringifyArg(args["completed_todo"]) != "false"
	var filterKey string
	if filter != nil {
		completed = filter.Status != "uncompleted"
		encoded, _ := json.Marshal(filter)
		filterKey = string(encoded)
	}
	keyHash := sha256.Sum256([]byte(key))
	pageSize := clampPageSize(stringifyArg(args["page_size"]))
	scan := projectTodoScan{
		seen:     make(map[string]bool),
		listPage: upstreamProjectPage{number: 1},
		taskPage: upstreamProjectPage{number: 1, includeFilterFields: filter != nil},
	}
	requests := 0
	canFetch := func() bool { return filter == nil || requests < projectFilterRequestLimit }

	if cursor := stringifyArg(args["cursor"]); cursor != "" {
		projectScanCache.Lock()
		saved, ok := projectScanCache.entries[cursor]
		projectScanCache.Unlock()
		if !ok || !saved.expires.After(time.Now()) || saved.keyHash != keyHash || saved.store != cfg.Store || saved.baseURL != cfg.BaseURL ||
			saved.projectID != projectID || saved.completed != completed || saved.filterKey != filterKey {
			if filter != nil {
				return nil, fmt.Errorf("%w：cursor 已失效或与本次查询不匹配，请从第一页开始", errWebArguments)
			}
			return nil, fmt.Errorf("cursor 已失效或与本次查询不匹配，请从第一页开始")
		}
		scan = saved.scan
		scan.seen = maps.Clone(scan.seen)
		scan.taskPage.seen = maps.Clone(scan.taskPage.seen)
	}
	if scan.listPage.number > 0 {
		scan.listPage.seen = maps.Clone(scan.listPage.seen)
		scan.lists = slices.Clone(scan.lists)
		path := "/api/v1/projects/" + url.PathEscape(projectID) + "/todolists"
		seenLists := make(map[string]bool)
		for _, id := range scan.lists {
			seenLists[id] = true
		}
		for scan.listPage.number > 0 && canFetch() {
			requests++
			lists, err := scan.listPage.fetch(ctx, cfg, key, acc, path, false, completed)
			if err != nil {
				return nil, err
			}
			for _, row := range lists {
				id := row["id"].(string)
				if !seenLists[id] {
					seenLists[id] = true
					scan.lists = append(scan.lists, id)
				}
			}
		}
	}

	items := make([]any, 0, pageSize)
	if scan.pending != nil {
		items = append(items, scan.pending)
		scan.pending = nil
	}
	for scan.listPage.number == 0 && scan.listIndex < len(scan.lists) {
		listID := scan.lists[scan.listIndex]
		if len(scan.rows) == 0 {
			if scan.taskPage.number == 0 {
				scan.listIndex++
				scan.taskPage = upstreamProjectPage{number: 1, includeFilterFields: filter != nil}
				continue
			}
			if !canFetch() {
				break
			}
			path := "/api/v1/todolists/" + url.PathEscape(listID) + "/todos"
			requests++
			rows, err := scan.taskPage.fetch(ctx, cfg, key, acc, path, true, completed)
			if err != nil {
				return nil, err
			}
			scan.rows = rows
			continue
		}
		if !canFetch() {
			break
		}
		row := scan.rows[0]
		scan.rows = scan.rows[1:]
		id := row["id"].(string)
		if scan.seen[id] {
			continue
		}
		item := maps.Clone(row)
		item["todolist_id"] = listID
		if filter != nil {
			detail := func() (map[string]any, error) {
				requests++
				return fetchProjectTodoDetail(ctx, cfg, key, acc, id)
			}
			matched, err := filter.matches(item, detail)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
			addTodoListDates(item, item)
			item = resultProfiles["list_todos"].selectFields(item)
			item["todolist_id"] = listID
		}
		scan.seen[id] = true
		if len(items) == pageSize {
			scan.pending = item
			break
		}
		items = append(items, item)
	}
	hasMore := scan.pending != nil || scan.listPage.number > 0 || scan.listIndex < len(scan.lists)
	out := map[string]any{"project_id": projectID, "page_size": pageSize, "returned": len(items), "has_more": hasMore, "items": items}
	if hasMore {
		out["next_cursor"] = saveProjectScan(savedProjectScan{scan: scan, keyHash: keyHash, baseURL: cfg.BaseURL, store: cfg.Store, projectID: projectID, completed: completed, filterKey: filterKey})
	}
	return out, nil
}
