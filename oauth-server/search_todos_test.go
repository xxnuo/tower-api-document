package main

import (
	"context"
	"net/http"
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
