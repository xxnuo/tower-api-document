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

func TestMemberTodoListsSortBeforePagination(t *testing.T) {
	for _, kind := range []string{"created", "assigned"} {
		t.Run(kind, func(t *testing.T) {
			role := map[string]string{"created": "creator", "assigned": "assignee"}[kind]
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/members/colleague/"+kind+"_todos" || r.URL.RawQuery != "" {
					t.Errorf("unexpected upstream request %s", r.URL)
				}
				fmt.Fprintf(w, `{"data":{"type":"members","id":"colleague","relationships":{"todos":{"data":[{"type":"todos","id":"old"},{"type":"todos","id":"recent"}]}}},"included":[{"type":"todos","id":"old","attributes":{"created_at":"2026-11-19T10:00:00+08:00"},"relationships":{"%[1]s":{"data":{"id":"colleague","type":"members"}}}},{"type":"todos","id":"recent","attributes":{"created_at":"2026-11-19T04:00:00Z"},"relationships":{"%[1]s":{"data":{"id":"colleague","type":"members"}}}}]}`, role)
			}))
			defer server.Close()
			tool := "list_member_" + kind + "_todos"
			index := map[string]mcpToolSpec{}
			for _, spec := range towerMCPTools {
				index[spec.Name] = spec
			}
			args := map[string]any{"member_id": "colleague", "page_size": 1, "order": "-created_at"}
			for page, want := range []string{"recent", "old"} {
				args["page"] = page + 1
				got, failed := executeMCPTool(t.Context(), testConfig(server.URL), "", &StoredAccount{}, index, tool, args)
				if failed || !strings.Contains(got, "ID "+want+"\n") || !strings.Contains(got, "total：2") {
					t.Fatalf("page %d: %s", page+1, got)
				}
			}
			args["order"] = "created_at"
			if got, failed := executeMCPTool(t.Context(), testConfig(server.URL), "", &StoredAccount{}, index, tool, args); !failed {
				t.Fatalf("accepted invalid order: %s", got)
			}
		})
	}
}
