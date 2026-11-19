package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListTodosOrdersWholeCollectionBeforePaging(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("order") {
			t.Error("gateway sort forwarded to Tower")
		}
		if q.Get("page[number]") == "2" {
			fmt.Fprint(w, `{"data":[{"id":"c","attributes":{"created_at":"2026-11-19T02:00:00Z"}}],"links":{"next":null}}`)
		} else {
			fmt.Fprint(w, `{"data":[{"id":"a","attributes":{"created_at":"2026-11-19T08:00:00+08:00"}},{"id":"b","attributes":{"created_at":"2026-11-19T01:00:00Z"}}],"links":{"next":"?page%5Bnumber%5D=2"}}`)
		}
	}))
	defer tower.Close()
	index := map[string]mcpToolSpec{}
	for _, spec := range towerMCPTools {
		index[spec.Name] = spec
	}
	for i, want := range []string{"c", "b", "a"} {
		text, failed := executeMCPTool(t.Context(), testConfig(tower.URL), "", &StoredAccount{}, index, "list_todos", map[string]any{"todolist_id": "t", "order": "-created_at", "page": i + 1, "page_size": 1})
		if failed || !strings.Contains(text, "ID "+want+"\n") || !strings.Contains(text, "total：3") {
			t.Fatalf("page %d: %s", i+1, text)
		}
	}
}
