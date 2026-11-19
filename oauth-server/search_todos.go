package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

	var upstreamCount atomic.Int32
	var truncated atomic.Bool

	budgetFetch := func(path, query string) (int, []byte, error) {
		if upstreamCount.Add(1) > searchMaxUpstreamBudget {
			upstreamCount.Add(-1)
			truncated.Store(true)
			return 0, nil, nil
		}
		return fetch(ctx, path, query)
	}

	var (
		mu       sync.Mutex
		seenIDs  = make(map[string]struct{})
		matched  = make([]map[string]any, 0, limit)
		scanned  int
		scope    string
		warnings []string
	)

	kwLower := strings.ToLower(keyword)
	collectItems := func(items []map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		for _, item := range items {
			scanned++
			id, _ := item["id"].(string)
			if id != "" {
				if _, ok := seenIDs[id]; ok {
					continue
				}
				seenIDs[id] = struct{}{}
			}
			if !todoMatchesStatus(item, status) || !todoMatchesKeyword(item, kwLower) {
				continue
			}
			if len(matched) < limit {
				matched = append(matched, item)
			} else {
				truncated.Store(true)
			}
		}
	}

	fetchMemberTodos := func(memberID string) {
		endpoints := memberTodoEndpoints(status)
		q := fmt.Sprintf("page[number]=1&page[size]=%d", maxListPageSize)
		for _, ep := range endpoints {
			path := "/api/v1/members/" + url.PathEscape(memberID) + "/" + ep
			code, body, err := budgetFetch(path, q)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("请求 %s 失败: %v", ep, err))
				mu.Unlock()
				continue
			}
			if code == 0 || code >= 400 {
				continue
			}
			collectItems(extractJSONAPIDataItems(body))
		}
	}

	fetchProjectTodos := func(projectID string) {
		tlPath := "/api/v1/projects/" + url.PathEscape(projectID) + "/todolists"
		code, body, err := budgetFetch(tlPath, fmt.Sprintf("page[number]=1&page[size]=%d", searchMaxTodolistsPerPrj))
		if err != nil || code == 0 || code >= 400 {
			return
		}
		todolists := extractJSONAPIDataItems(body)
		if len(todolists) > searchMaxTodolistsPerPrj {
			todolists = todolists[:searchMaxTodolistsPerPrj]
			truncated.Store(true)
		}
		sem := make(chan struct{}, searchMaxUpstreamConcur)
		var wg sync.WaitGroup
		for _, tl := range todolists {
			tlID, _ := tl["id"].(string)
			if tlID == "" {
				continue
			}
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				tPath := "/api/v1/todolists/" + url.PathEscape(id) + "/todos"
				tCode, tBody, tErr := budgetFetch(tPath, fmt.Sprintf("page[number]=1&page[size]=%d", maxListPageSize))
				if tErr != nil || tCode == 0 || tCode >= 400 {
					return
				}
				collectItems(extractJSONAPIDataItems(tBody))
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
		projects := extractJSONAPIDataItems(body)
		if len(projects) > searchMaxProjectsScan {
			projects = projects[:searchMaxProjectsScan]
			truncated.Store(true)
		}
		for _, prj := range projects {
			pID, _ := prj["id"].(string)
			if pID == "" {
				continue
			}
			fetchProjectTodos(pID)
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
		teams := extractJSONAPIDataItems(body)
		if len(teams) > searchMaxTeamsScan {
			teams = teams[:searchMaxTeamsScan]
			truncated.Store(true)
		}
		for _, tm := range teams {
			tID, _ := tm["id"].(string)
			if tID == "" {
				continue
			}
			mCode, mBody, mErr := budgetFetch("/api/v1/teams/"+url.PathEscape(tID)+"/member", "")
			if mErr != nil || mCode == 0 || mCode >= 400 {
				continue
			}
			var memberDoc struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if json.Unmarshal(mBody, &memberDoc) == nil && memberDoc.Data.ID != "" {
				fetchMemberTodos(memberDoc.Data.ID)
			}
		}
	}

	result := map[string]any{
		"keyword":           keyword,
		"status":            status,
		"scope":             scope,
		"limit":             limit,
		"matched_count":     len(matched),
		"scanned_count":     scanned,
		"upstream_requests": int(upstreamCount.Load()),
		"truncated":         truncated.Load(),
		"items":             matched,
	}
	if len(warnings) > 0 {
		result["warnings"] = warnings
	}
	return http.StatusOK, result
}

func memberTodoEndpoints(status string) []string {
	switch status {
	case "uncompleted":
		return []string{"assigned_uncompleted_todos", "created_uncompleted_todos"}
	case "completed":
		return []string{"assigned_completed_todos", "created_completed_todos"}
	default:
		return []string{"assigned_todos", "created_todos"}
	}
}

func todoMatchesStatus(item map[string]any, status string) bool {
	if status == "all" {
		return true
	}
	attrs, ok := item["attributes"].(map[string]any)
	if !ok {
		return true
	}
	completed, hasCompleted := attrs["completed"].(bool)
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

func executeSearchTodos(
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
		return doTowerHTTPWithRefresh(callCtx, cfg, mcpKey, acc, http.MethodGet, path, query, nil)
	}
	status, payload := searchTodos(ctx, defaultTodoSearchLimiter, "mcp:"+mcpKey, fetch, params)
	if items, ok := payload["items"].([]map[string]any); ok {
		payload["items"] = compactTodoItems(items)
	}
	out, _ := json.Marshal(payload)
	if status >= 400 {
		return agentText(out), true
	}
	return resultProfiles["search_todos"].compositeText(out), false
}
