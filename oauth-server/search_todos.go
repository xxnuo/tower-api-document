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
	"sync"
	"sync/atomic"
	"time"
)

const (
	searchRateLimitMax       = 10
	searchRateLimitWindow    = time.Minute
	searchMaxGlobalConcur    = 4
	searchMaxUpstreamConcur  = 4
	searchMaxUpstreamBudget  = 20
	searchMaxTeamsScan       = 3
	searchMaxProjectsScan    = 5
	searchMaxTodolistsPerPrj = 15
)

type searchRateLimiter struct {
	mu      sync.Mutex
	maxReq  int
	window  time.Duration
	hits    map[string][]time.Time
	sem     chan struct{}
	nowFunc func() time.Time
}

func newSearchRateLimiter(maxReq int, window time.Duration, maxConcur int) *searchRateLimiter {
	if maxReq <= 0 {
		maxReq = searchRateLimitMax
	}
	if window <= 0 {
		window = searchRateLimitWindow
	}
	if maxConcur <= 0 {
		maxConcur = searchMaxGlobalConcur
	}
	return &searchRateLimiter{
		maxReq:  maxReq,
		window:  window,
		hits:    make(map[string][]time.Time),
		sem:     make(chan struct{}, maxConcur),
		nowFunc: time.Now,
	}
}

func (l *searchRateLimiter) Acquire(key string) (func(), time.Duration, string) {
	if key == "" {
		key = "anonymous"
	}
	now := l.nowFunc()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	recent := l.hits[key][:0]
	for _, ts := range l.hits[key] {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}
	if len(recent) >= l.maxReq {
		retryAfter := recent[0].Add(l.window).Sub(now)
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		l.hits[key] = recent
		l.mu.Unlock()
		return nil, retryAfter, fmt.Sprintf("搜索请求过于频繁（每分钟最多 %d 次），请在 %d 秒后重试", l.maxReq, int(retryAfter.Seconds()+0.999))
	}
	select {
	case l.sem <- struct{}{}:
	default:
		l.hits[key] = recent
		l.mu.Unlock()
		return nil, 2 * time.Second, "当前并发搜索请求过多，请稍后重试"
	}
	l.hits[key] = append(recent, now)
	l.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() { <-l.sem })
	}
	return release, 0, ""
}

var defaultTodoSearchLimiter = newSearchRateLimiter(searchRateLimitMax, searchRateLimitWindow, searchMaxGlobalConcur)

type searchTodosParams struct {
	Keyword   string
	TeamID    string
	ProjectID string
	MemberID  string
	Status    string
	Limit     int
}

type todoFetchFunc func(ctx context.Context, path, query string) (int, []byte, error)

func normalizeSearchStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "uncompleted", "open":
		return "uncompleted"
	case "completed", "done":
		return "completed"
	default:
		return "all"
	}
}

func searchTodos(
	ctx context.Context,
	limiter *searchRateLimiter,
	callerKey string,
	fetch todoFetchFunc,
	params searchTodosParams,
) (int, map[string]any) {
	keyword := strings.TrimSpace(params.Keyword)
	if keyword == "" {
		return http.StatusBadRequest, map[string]any{"error": "keyword 不能为空"}
	}
	if limiter == nil {
		limiter = defaultTodoSearchLimiter
	}
	release, retryAfter, limitMsg := limiter.Acquire(callerKey)
	if limitMsg != "" {
		return http.StatusTooManyRequests, map[string]any{
			"error":               limitMsg,
			"retry_after_seconds": int(retryAfter.Seconds() + 0.999),
		}
	}
	defer release()

	status := normalizeSearchStatus(params.Status)
	limit := params.Limit
	if limit <= 0 {
		limit = defaultListPageSize
	} else if limit > maxListPageSize {
		limit = maxListPageSize
	}

	var (
		upstreamCount atomic.Int32
		resultLimited bool
		mu            sync.Mutex
		seenIDs       = make(map[string]struct{})
		matched       = make([]map[string]any, 0, limit)
		scanned       int
		scope         string
		warnings      []string
	)
	warn := func(reason string) {
		mu.Lock()
		defer mu.Unlock()
		if !slices.Contains(warnings, reason) {
			warnings = append(warnings, reason)
		}
	}
	budgetFetch := func(path, query string) (int, []byte, error) {
		if upstreamCount.Add(1) > searchMaxUpstreamBudget {
			upstreamCount.Add(-1)
			warn(fmt.Sprintf("已达上游请求预算（%d 次），部分范围未扫描", searchMaxUpstreamBudget))
			return 0, nil, nil
		}
		code, body, err := fetch(ctx, path, query)
		if err != nil {
			warn(fmt.Sprintf("请求 %s 失败: %v", path, err))
		} else if code < 200 || code >= 300 {
			warn(fmt.Sprintf("请求 %s 返回 HTTP %d", path, code))
		}
		return code, body, err
	}
	scanRows := func(raw []byte, label string, capRows int) []map[string]any {
		doc, rows := decodeMCPList(raw)
		if rows == nil {
			warn(label + "响应缺少有效列表")
			return nil
		}
		if mcpListHasOmittedRows(doc, len(rows)) {
			meta, _ := doc["meta"].(map[string]any)
			reason := stringifyArg(meta["incomplete_reason"])
			if reason == "" {
				reason = "Tower 只返回了部分记录"
			}
			warn(label + "：" + reason)
		}
		if capRows > 0 && len(rows) > capRows {
			warn(fmt.Sprintf("%s扫描上限为 %d 条，额外记录未扫描", label, capRows))
			rows = rows[:capRows]
		}
		items := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			item, ok := row.(map[string]any)
			if !ok || stringifyArg(item["id"]) == "" {
				warn(label + "响应包含缺少有效 ID 的记录")
				continue
			}
			items = append(items, item)
		}
		return items
	}

	kwLower := strings.ToLower(keyword)
	collectItems := func(items []map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		for _, item := range items {
			scanned++
			id := stringifyArg(item["id"])
			if _, ok := seenIDs[id]; ok {
				continue
			}
			seenIDs[id] = struct{}{}
			if !todoMatchesStatus(item, status) || !todoMatchesKeyword(item, kwLower) {
				continue
			}
			if len(matched) < limit {
				matched = append(matched, item)
			} else {
				resultLimited = true
			}
		}
	}

	fetchMemberTodos := func(memberID string) {
		for _, ep := range []string{"assigned_todos", "created_todos"} {
			path := "/api/v1/members/" + url.PathEscape(memberID) + "/" + ep
			code, body, err := fetchScopedMemberTodos(budgetFetch, path, status)
			if err != nil {
				warn("成员任务：" + err.Error())
				continue
			}
			if code < 200 || code >= 300 {
				continue
			}
			collectItems(scanRows(body, "成员任务", 0))
		}
	}

	fetchProjectTodos := func(projectID string) {
		tlPath := "/api/v1/projects/" + url.PathEscape(projectID) + "/todolists"
		code, body, err := budgetFetch(tlPath, fmt.Sprintf("page[number]=1&page[size]=%d", searchMaxTodolistsPerPrj))
		if err != nil || code < 200 || code >= 300 {
			return
		}
		todolists := scanRows(body, "项目清单", searchMaxTodolistsPerPrj)
		sem := make(chan struct{}, searchMaxUpstreamConcur)
		var wg sync.WaitGroup
		for _, tl := range todolists {
			tlID := stringifyArg(tl["id"])
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				tPath := "/api/v1/todolists/" + url.PathEscape(id) + "/todos"
				query := fmt.Sprintf("page[number]=1&page[size]=%d", maxListPageSize)
				if status != "uncompleted" {
					query += "&completed_todo=true"
				}
				tCode, tBody, tErr := budgetFetch(tPath, query)
				if tErr != nil || tCode < 200 || tCode >= 300 {
					return
				}
				collectItems(scanRows(tBody, "清单任务", 0))
			}(tlID)
		}
		wg.Wait()
	}

	switch {
	case strings.TrimSpace(params.MemberID) != "":
		scope = "member:" + strings.TrimSpace(params.MemberID)
		fetchMemberTodos(strings.TrimSpace(params.MemberID))
	case strings.TrimSpace(params.ProjectID) != "":
		scope = "project:" + strings.TrimSpace(params.ProjectID)
		fetchProjectTodos(strings.TrimSpace(params.ProjectID))
	case strings.TrimSpace(params.TeamID) != "":
		teamID := strings.TrimSpace(params.TeamID)
		scope = "team:" + teamID
		pPath := "/api/v1/teams/" + url.PathEscape(teamID) + "/projects"
		code, body, err := budgetFetch(pPath, fmt.Sprintf("page[number]=1&page[size]=%d", searchMaxProjectsScan))
		if err != nil {
			return http.StatusBadGateway, map[string]any{"error": "查询团队项目失败: " + err.Error()}
		}
		if code >= 400 {
			return code, map[string]any{"error": fmt.Sprintf("查询团队项目返回 HTTP %d", code)}
		}
		for _, prj := range scanRows(body, "团队项目", searchMaxProjectsScan) {
			fetchProjectTodos(stringifyArg(prj["id"]))
		}
	default:
		scope = "my_teams"
		code, body, err := budgetFetch("/api/v1/teams", "")
		if err != nil {
			return http.StatusBadGateway, map[string]any{"error": "查询团队列表失败: " + err.Error()}
		}
		if code >= 400 {
			return code, map[string]any{"error": fmt.Sprintf("查询团队列表返回 HTTP %d", code)}
		}
		for _, tm := range scanRows(body, "团队列表", searchMaxTeamsScan) {
			tID := stringifyArg(tm["id"])
			mCode, mBody, mErr := budgetFetch("/api/v1/teams/"+url.PathEscape(tID)+"/member", "")
			if mErr != nil || mCode < 200 || mCode >= 300 {
				continue
			}
			var memberDoc struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(mBody, &memberDoc) == nil && memberDoc.Data.ID != "" {
				fetchMemberTodos(memberDoc.Data.ID)
			} else {
				warn("Tower 团队成员响应缺少有效成员 ID")
			}
		}
	}

	if resultLimited {
		warn(fmt.Sprintf("已达搜索结果上限（%d 条），额外匹配结果已省略", limit))
	}
	slices.Sort(warnings)
	result := map[string]any{
		"keyword":           keyword,
		"status":            status,
		"scope":             scope,
		"limit":             limit,
		"matched_count":     len(matched),
		"scanned_count":     scanned,
		"upstream_requests": int(upstreamCount.Load()),
		"truncated":         len(warnings) > 0,
		"items":             matched,
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
		result["incomplete_reason"] = strings.Join(warnings, "。")
	}
	return http.StatusOK, result
}

func todoMatchesStatus(item map[string]any, status string) bool {
	if status == "all" {
		return true
	}
	attrs, ok := item["attributes"].(map[string]any)
	if !ok {
		return true
	}
	completed, hasCompleted := attrs["is_completed"].(bool)
	if !hasCompleted {
		completed, hasCompleted = attrs["completed"].(bool)
	}
	if !hasCompleted {
		return true
	}
	if status == "completed" {
		return completed
	}
	return !completed
}

func todoMatchesKeyword(item map[string]any, kwLower string) bool {
	if id, _ := item["id"].(string); id != "" && strings.Contains(strings.ToLower(id), kwLower) {
		return true
	}
	attrs, ok := item["attributes"].(map[string]any)
	if !ok {
		return false
	}
	for _, field := range []string{"content", "desc", "title"} {
		if val, ok := attrs[field].(string); ok && strings.Contains(strings.ToLower(val), kwLower) {
			return true
		}
	}
	if wideID, ok := attrs["team_wide_id"]; ok && wideID != nil {
		if strings.Contains(strings.ToLower(fmt.Sprint(wideID)), kwLower) {
			return true
		}
	}
	return false
}

func executeAdvancedSearchTodos(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	args map[string]any,
) (string, bool) {
	limit, _ := strconv.Atoi(stringifyArg(args["limit"]))
	params := searchTodosParams{
		Keyword:   stringifyArg(args["keyword"]),
		TeamID:    stringifyArg(args["team_id"]),
		ProjectID: stringifyArg(args["project_id"]),
		MemberID:  stringifyArg(args["member_id"]),
		Status:    stringifyArg(args["status"]),
		Limit:     limit,
	}
	fetch := func(callCtx context.Context, path, query string) (int, []byte, error) {
		account := *acc
		return doTowerHTTPWithRefresh(callCtx, cfg, mcpKey, &account, http.MethodGet, path, query, nil)
	}
	status, payload := searchTodos(ctx, defaultTodoSearchLimiter, "mcp:"+mcpKey, fetch, params)
	if items, ok := payload["items"].([]map[string]any); ok {
		payload["items"] = compactTodoItems(items)
	}
	out, _ := json.Marshal(payload)
	if status >= 400 {
		return agentText(out), true
	}
	return resultProfiles["advanced_search_todos"].compositeText(out), false
}
