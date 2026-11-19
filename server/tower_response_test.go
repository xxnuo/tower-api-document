package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOversizeTowerResponseIsNotASuccessfulPartialResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat(" ", (2<<20)+1)))
	}))
	defer server.Close()
	_, body, err := doTowerHTTP(context.Background(), testConfig(server.URL), "token", http.MethodGet, "/api/v1/teams", "", nil)
	if err == nil || len(body) != 0 {
		t.Fatalf("oversized response returned %d bytes with error %v", len(body), err)
	}
}

func TestListTodosIncludesCompletedOnlyWhenRequested(t *testing.T) {
	included := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Has("completed_todo") != included || included && query.Get("completed_todo") != "true" {
			t.Errorf("unexpected completed_todo query %v with include=%t", query, included)
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	index := make(map[string]mcpToolSpec)
	for _, spec := range towerMCPTools {
		index[spec.Name] = spec
	}
	for _, included = range []bool{false, true} {
		_, failed := executeMCPTool(t.Context(), testConfig(server.URL), "", &StoredAccount{}, index, "list_todos", map[string]any{"todolist_id": "test", "completed_todo": included})
		if failed {
			t.Fatal("list_todos failed")
		}
	}
}
