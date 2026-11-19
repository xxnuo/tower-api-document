package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSearchTodosFilterDedupeAndRateLimit(t *testing.T) {
	limiter := newSearchRateLimiter(2, time.Minute, 1)
	mockFetch := func(ctx context.Context, path, query string) (int, []byte, error) {
		switch path {
		case "/api/v1/members/m1/assigned_todos":
			return http.StatusOK, []byte(`{"data":[
				{"id":"t1","attributes":{"content":"修复登录 Bug","desc":"OAuth 回调","completed":false}},
				{"id":"t2","attributes":{"content":"发布版本","desc":"已完成","completed":true}}
			]}`), nil
		case "/api/v1/members/m1/created_todos":
			return http.StatusOK, []byte(`{"data":[
				{"id":"t1","attributes":{"content":"修复登录 Bug","desc":"OAuth 回调","completed":false}},
				{"id":"t3","attributes":{"content":"补充 OAuth 测试","desc":"","completed":false}}
			]}`), nil
		default:
			return http.StatusNotFound, []byte(`{}`), nil
		}
	}

	code, res := searchTodos(context.Background(), limiter, "u1", mockFetch, searchTodosParams{
		Keyword:  "oauth",
		MemberID: "m1",
		Status:   "all",
		Limit:    10,
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, res)
	}
	if res["matched_count"] != 2 {
		t.Fatalf("expected 2 deduplicated matches, got %v", res["matched_count"])
	}

	// Second request within window succeeds.
	code2, _ := searchTodos(context.Background(), limiter, "u1", mockFetch, searchTodosParams{
		Keyword:  "发布",
		MemberID: "m1",
		Status:   "completed",
	})
	if code2 != http.StatusOK {
		t.Fatalf("expected second request 200, got %d", code2)
	}

	// Third request exceeds window limit -> 429.
	code3, res3 := searchTodos(context.Background(), limiter, "u1", mockFetch, searchTodosParams{
		Keyword:  "oauth",
		MemberID: "m1",
	})
	if code3 != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 3rd request, got %d: %v", code3, res3)
	}
}

func TestSearchRateLimiterConcurrency(t *testing.T) {
	limiter := newSearchRateLimiter(10, time.Minute, 1)
	release, _, msg := limiter.Acquire("u1")
	if msg != "" {
		t.Fatalf("unexpected limit on first acquire: %s", msg)
	}
	_, _, msg2 := limiter.Acquire("u2")
	if msg2 == "" {
		t.Fatalf("expected concurrency limit on second acquire")
	}
	release()
	release3, _, msg3 := limiter.Acquire("u2")
	if msg3 != "" {
		t.Fatalf("expected acquire after release to succeed, got: %s", msg3)
	}
	release3()
}

func TestDeclaredSearchArguments(t *testing.T) {
	for _, def := range listMCPToolDefinitions() {
		name := def["name"].(string)
		if name != "search_todos" && name != "advanced_search_todos" {
			continue
		}
		fields := def["inputSchema"].(map[string]any)["properties"].(map[string]any)
		if _, ok := fields["keyword"]; !ok {
			t.Fatalf("%s lacks keyword", name)
		}
		if name == "search_todos" {
			for _, key := range []string{"page", "target_gid", "member_guid", "category", "sort", "exclude_trash"} {
				if fields[key] == nil {
					t.Fatalf("missing web filter %s", key)
				}
			}
		}
		if name == "advanced_search_todos" {
			for _, key := range []string{"team_id", "project_id", "member_id", "status", "limit"} {
				if _, ok := fields[key]; !ok {
					t.Fatalf("advanced search lacks %s", key)
				}
			}
		}
	}
}

func TestCurrentTowerCompletionField(t *testing.T) {
	item := map[string]any{"attributes": map[string]any{"is_completed": true}}
	if todoMatchesStatus(item, "uncompleted") || !todoMatchesStatus(item, "completed") {
		t.Fatal("is_completed ignored")
	}
}

func TestAdvancedSearchIncompleteReasons(t *testing.T) {
	fetch := func(_ context.Context, path, _ string) (int, []byte, error) {
		switch path {
		case "/api/v1/projects/p/todolists":
			return 200, []byte(`{"data":[{"id":"a"},{"id":"b"}],"meta":{"has_more":true}}`), nil
		case "/api/v1/todolists/a/todos":
			return 200, []byte(`{"data":[{"id":"t1","attributes":{"content":"match"}},{"id":"t2","attributes":{"content":"match"}}]}`), nil
		default:
			return 403, nil, nil
		}
	}
	code, result := searchTodos(t.Context(), newSearchRateLimiter(1, time.Minute, 1), "test", fetch,
		searchTodosParams{Keyword: "match", ProjectID: "p", Limit: 1})
	reason, _ := result["incomplete_reason"].(string)
	for _, want := range []string{"清单", "HTTP 403", "结果上限"} {
		if code != 200 || result["truncated"] != true || !strings.Contains(reason, want) {
			t.Fatalf("missing %q: %d %v", want, code, result)
		}
	}
}

func TestAdvancedSearchKeepsMemberRowsBeyondRequestedPage(t *testing.T) {
	rows := make([]map[string]any, 60)
	for i := range rows {
		rows[i] = map[string]any{"id": fmt.Sprint(i)}
	}
	rows[len(rows)-1]["attributes"] = map[string]any{"content": "late match"}
	body, err := json.Marshal(map[string]any{"data": rows})
	if err != nil {
		t.Fatal(err)
	}
	fetch := func(_ context.Context, _ string, _ string) (int, []byte, error) {
		return 200, body, nil
	}
	code, result := searchTodos(t.Context(), newSearchRateLimiter(1, time.Minute, 1), "test", fetch,
		searchTodosParams{Keyword: "late match", MemberID: "m1"})
	if code != 200 || result["matched_count"] != 1 || result["incomplete_reason"] != nil {
		t.Fatalf("unexpected result: %d %v", code, result)
	}
}

func TestAdvancedSearchReportsRequestBudget(t *testing.T) {
	fetch := func(_ context.Context, path, _ string) (int, []byte, error) {
		switch {
		case strings.HasSuffix(path, "/projects"):
			return 200, []byte(`{"data":[{"id":"p1"},{"id":"p2"}]}`), nil
		case strings.HasSuffix(path, "/todolists"):
			rows := make([]map[string]string, searchMaxTodolistsPerPrj)
			for i := range rows {
				rows[i] = map[string]string{"id": fmt.Sprint(i)}
			}
			body, err := json.Marshal(map[string]any{"data": rows})
			return 200, body, err
		default:
			return 200, []byte(`{"data":[]}`), nil
		}
	}
	code, result := searchTodos(t.Context(), newSearchRateLimiter(1, time.Minute, 1), "test", fetch,
		searchTodosParams{Keyword: "match", TeamID: "t1"})
	reason, _ := result["incomplete_reason"].(string)
	if code != 200 || result["upstream_requests"] != searchMaxUpstreamBudget || !strings.Contains(reason, "上游请求预算") {
		t.Fatalf("unexpected result: %d %v", code, result)
	}
}
