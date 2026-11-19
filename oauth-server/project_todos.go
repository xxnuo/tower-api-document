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
	listIndex int
	taskPage  upstreamProjectPage
	rows      []map[string]any
	pending   map[string]any
	seen      map[string]bool
}

type upstreamProjectPage struct {
	number int
	seen   map[[32]byte]bool
}

type savedProjectScan struct {
	scan      projectTodoScan
	keyHash   [32]byte
	baseURL   string
	store     *Store
	projectID string
	completed bool
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
			addTodoListDates(item, row)
			doc.Data[i] = resultProfiles["list_todos"].selectFields(item)
		}
	}
	return doc.Data, nil
}

func executeListProjectTodos(ctx context.Context, cfg Config, key string, acc *StoredAccount, args map[string]any) (string, bool) {
	projectID := stringifyArg(args["project_id"])
	if projectID == "" {
		return "缺少必填参数 project_id", true
	}
	completed := stringifyArg(args["completed_todo"]) != "false"
	keyHash := sha256.Sum256([]byte(key))
	pageSize := clampPageSize(stringifyArg(args["page_size"]))
	scan := projectTodoScan{seen: make(map[string]bool)}

	if cursor := stringifyArg(args["cursor"]); cursor != "" {
		projectScanCache.Lock()
		saved, ok := projectScanCache.entries[cursor]
		projectScanCache.Unlock()
		if !ok || !saved.expires.After(time.Now()) || saved.keyHash != keyHash || saved.store != cfg.Store || saved.baseURL != cfg.BaseURL ||
			saved.projectID != projectID || saved.completed != completed {
			return "cursor 已失效或与本次查询不匹配，请从第一页开始", true
		}
		scan = saved.scan
		scan.seen = maps.Clone(scan.seen)
		scan.taskPage.seen = maps.Clone(scan.taskPage.seen)
	} else {
		path := "/api/v1/projects/" + url.PathEscape(projectID) + "/todolists"
		seenLists := make(map[string]bool)
		for state := (upstreamProjectPage{number: 1}); state.number > 0; {
			lists, err := state.fetch(ctx, cfg, key, acc, path, false, completed)
			if err != nil {
				return err.Error(), true
			}
			for _, row := range lists {
				id := row["id"].(string)
				if !seenLists[id] {
					seenLists[id] = true
					scan.lists = append(scan.lists, id)
				}
			}
		}
		if len(scan.lists) > 0 {
			scan.taskPage.number = 1
		}
	}

	items := make([]any, 0, pageSize)
	if scan.pending != nil {
		items = append(items, scan.pending)
		scan.pending = nil
	}
	for scan.listIndex < len(scan.lists) {
		listID := scan.lists[scan.listIndex]
		if len(scan.rows) == 0 {
			if scan.taskPage.number == 0 {
				scan.listIndex++
				scan.taskPage = upstreamProjectPage{number: 1}
				continue
			}
			path := "/api/v1/todolists/" + url.PathEscape(listID) + "/todos"
			rows, err := scan.taskPage.fetch(ctx, cfg, key, acc, path, true, completed)
			if err != nil {
				return err.Error(), true
			}
			scan.rows = rows
			continue
		}
		row := scan.rows[0]
		scan.rows = scan.rows[1:]
		id := row["id"].(string)
		if scan.seen[id] {
			continue
		}
		scan.seen[id] = true
		item := maps.Clone(row)
		item["todolist_id"] = listID
		if len(items) == pageSize {
			scan.pending = item
			break
		}
		items = append(items, item)
	}
	out := map[string]any{"project_id": projectID, "page_size": pageSize, "returned": len(items), "has_more": scan.pending != nil, "items": items}
	if scan.pending != nil {
		out["next_cursor"] = saveProjectScan(savedProjectScan{scan: scan, keyHash: keyHash, baseURL: cfg.BaseURL, store: cfg.Store, projectID: projectID, completed: completed})
	}
	return resultProfiles["list_project_todos"].text(out), false
}
