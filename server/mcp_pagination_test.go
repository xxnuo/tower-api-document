package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPListPaginationMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, body   string
		page, size   int
		local        bool
		want, absent []string
	}{
		{"native full", `{"data":[{"id":"1"},{"id":"2"}]}`, 1, 2, false, []string{`"has_more":null`, `"next_page":2`, `"returned":2`}, []string{`"total"`, `"truncated"`, `"incomplete_reason"`}},
		{"native overflow", `{"data":[{"id":"1"},{"id":"2"},{"id":"3"}]}`, 1, 2, false, []string{`"incomplete_reason"`, `"truncated":true`}, nil},
		{"native last", `{"data":[{"id":"3"}],"links":{"next":null},"meta":{"total_count":3}}`, 2, 2, false, []string{`"has_more":false`, `"total":3`, `"returned":1`}, []string{`"next_page"`, `"truncated"`}},
		{"native last via links", `{"data":[{"id":"1"},{"id":"2"}],"links":{"last":"https://tower.im/api/v1/teams/t/projects?page%5Bnumber%5D=1"}}`, 1, 2, false, []string{`"has_more":false`, `"total":2`, `"returned":2`}, []string{`"next_page"`, `"truncated"`}},
		{"local list", `[{"id":"1"},{"id":"2"},{"id":"3"}]`, 2, 2, true, []string{`"id":"3"`, `"total":3`, `"has_more":false`, `"returned":1`}, []string{`"id":"1"`, `"truncated"`}},
		{"scope incomplete", `{"meta":{"scope_complete":false},"data":[{"id":"1"},{"id":"2"},{"id":"3"}]}`, 2, 2, true, []string{`"has_more":null`, `"returned":1`, `"incomplete_reason"`}, []string{`"total"`, `"next_page"`, `"truncated"`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := string(paginateMCPList([]byte(tt.body), tt.page, tt.size, tt.local, nil))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %s: %s", want, got)
				}
			}
			for _, absent := range tt.absent {
				if strings.Contains(got, absent) {
					t.Fatalf("unexpected %s: %s", absent, got)
				}
			}
		})
	}
}

func TestMCPEventsUseNativeCursor(t *testing.T) {
	calls := 0
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		switch calls {
		case 1:
			if q.Encode() != "limit=2" {
				t.Errorf("first query: %s", q.Encode())
			}
			fmt.Fprint(w, `{"data":[{"id":"101"},{"id":"100"}]}`)
		case 2:
			if q.Encode() != "last_item_id=100&limit=2" {
				t.Errorf("second query: %s", q.Encode())
			}
			fmt.Fprint(w, `{"data":[{"id":"99"}]}`)
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}))
	defer tower.Close()
	call := func(args map[string]any) (string, bool) {
		args["team_id"] = "t1"
		index := map[string]mcpToolSpec{}
		for _, spec := range towerMCPTools {
			index[spec.Name] = spec
		}
		return executeMCPTool(t.Context(), testConfig(tower.URL), "", &StoredAccount{}, index, "list_events", args)
	}
	if got, failed := call(map[string]any{"page": 2}); !failed || !strings.Contains(got, "last_item_id") {
		t.Fatalf("page=2: %v, %s", failed, got)
	}
	if got, failed := call(map[string]any{"page_size": 2}); failed || !strings.Contains(got, "next_cursor：100") || !strings.Contains(got, "has_more：未知") {
		t.Fatalf("first page: %v, %s", failed, got)
	}
	if got, failed := call(map[string]any{"page_size": 2, "last_item_id": 100}); failed || strings.Contains(got, "next_cursor") || !strings.Contains(got, "has_more：否") {
		t.Fatalf("last page: %v, %s", failed, got)
	}
}

func TestMCPOverviewCountAndErrors(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.URL.Path; {
		case path == "/api/v1/teams":
			teams := make([]map[string]any, 12)
			for i := range teams {
				teams[i] = map[string]any{"id": fmt.Sprint(i + 1)}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": teams})
		case strings.HasPrefix(path, "/api/v1/teams/"):
			id := strings.Split(path, "/")[4]
			if id == "11" || id == "12" {
				t.Errorf("overview checked team %s beyond its cap", id)
			}
			fmt.Fprintf(w, `{"data":{"id":%q}}`, id)
		case path == "/api/v1/members/1/assigned_todos":
			fmt.Fprint(w, `{
				"data":{"id":"1","relationships":{"todos":{"data":[{"type":"todos","id":"1"},{"type":"todos","id":"2"},{"type":"todos","id":"3"},{"type":"todos","id":"4"},{"type":"todos","id":"5"}]}}},
				"included":[
					{"type":"todos","id":"1","relationships":{"assignee":{"data":{"id":"1"}}},"attributes":{"content":"first","is_active":true,"is_completed":false}},
					{"type":"todos","id":"2","relationships":{"assignee":{"data":{"id":"1"}}},"attributes":{"is_active":true,"is_completed":false}},
					{"type":"todos","id":"3","relationships":{"assignee":{"data":{"id":"1"}}},"attributes":{"is_active":true,"is_completed":false}},
					{"type":"todos","id":"4","relationships":{"assignee":{"data":{"id":"1"}}},"attributes":{"is_active":true,"is_completed":true}},
					{"type":"todos","id":"5","relationships":{"assignee":{"data":{"id":"1"}}},"attributes":{"is_active":false,"is_completed":false}}
				]
			}`)
		case strings.HasSuffix(path, "/assigned_todos"):
			http.NotFound(w, r)
		case path == "/api/v1/members/2/assigned_uncompleted_todos":
			todos := make([]map[string]any, 200)
			for i := range todos {
				todos[i] = map[string]any{"id": fmt.Sprint(i + 1), "relationships": map[string]any{"assignee": map[string]any{"data": map[string]any{"id": "2"}}}}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": todos})
		case path == "/api/v1/members/3/assigned_uncompleted_todos":
			http.Error(w, "forbidden", http.StatusForbidden)
		case path == "/api/v1/members/4/assigned_uncompleted_todos":
			fmt.Fprint(w, `{"data":[{"id":"4","relationships":{"assignee":{"data":{"id":"4"}}}}]}`)
		default:
			fmt.Fprint(w, `{"data":[]}`)
		}
	}))
	defer tower.Close()
	for _, tt := range []struct {
		name string
		args map[string]any
		want []string
	}{
		{"known", map[string]any{"team_id": "1", "page_size": 1}, []string{"total_assigned_uncompleted：3", "returned_assigned_uncompleted：1", "assigned_uncompleted_count：3", "assigned_uncompleted_returned：1"}},
		{"member cap", map[string]any{"team_id": "2", "page_size": 1}, []string{"total_assigned_uncompleted：未知", "returned_assigned_uncompleted：1", "assigned_uncompleted_count：未知"}},
		{"fallback below cap", map[string]any{"team_id": "4", "page_size": 1}, []string{"total_assigned_uncompleted：未知", "returned_assigned_uncompleted：1", "assigned_uncompleted_warning：", "incomplete_reason："}},
		{"team cap and error", map[string]any{"page_size": 1}, []string{"teams_checked：10", "teams_available：12", "teams_skipped：2", "teams_failed：1", "total_assigned_uncompleted：未知", "returned_assigned_uncompleted：3", "assigned_uncompleted_error：Tower API 返回 HTTP 403", "incomplete_reason："}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, failed := executeMyTodoOverview(t.Context(), testConfig(tower.URL), "", &StoredAccount{}, tt.args)
			if failed {
				t.Fatal(got)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Fatalf("missing %q: %s", want, got)
				}
			}
		})
	}

	index := map[string]mcpToolSpec{}
	for _, spec := range towerMCPTools {
		index[spec.Name] = spec
	}
	cfg := testConfig(tower.URL)
	args := map[string]any{"member_id": "1", "page": 2, "page_size": 2}
	got, failed := executeMCPTool(t.Context(), cfg, "", &StoredAccount{}, index, "list_member_assigned_uncompleted_todos", args)
	if failed || !strings.Contains(got, "ID 3") || !strings.Contains(got, "total：3") || !strings.Contains(got, "returned：1") || strings.Contains(got, "ID 4") {
		t.Fatalf("unfinished list page: %v, %s", failed, got)
	}
	got, failed = executeMCPTool(t.Context(), cfg, "", &StoredAccount{}, index, "list_member_assigned_todos", args)
	if failed || !strings.Contains(got, "ID 4") || !strings.Contains(got, "total：5") || !strings.Contains(got, "next_page：3") {
		t.Fatalf("member's complete list page: %v, %s", failed, got)
	}
}

func TestMemberTodoFallbackFollowsLinksAndKeepsRelatedResources(t *testing.T) {
	path := "/api/v1/members/target/assigned_todos"
	fetch := func(path, query string) (int, []byte, error) {
		switch {
		case strings.HasSuffix(path, "/assigned_todos"):
			return 200, []byte(`{"data":{"id":"caller","relationships":{"todos":{"data":[]}}}}`), nil
		case strings.HasSuffix(path, "/assigned_uncompleted_todos"):
			return 200, []byte(`{"data":[{"id":"active","relationships":{"assignee":{"data":{"type":"members","id":"target"}}}}],"included":[{"id":"target","type":"members","attributes":{"nickname":"Target"}}]}`), nil
		case query == "":
			return 200, []byte(`{"data":[],"links":{"next":"?page%5Bnumber%5D=3"}}`), nil
		case query == "page%5Bnumber%5D=3":
			return 200, []byte(`{"data":[{"id":"done","relationships":{"assignee":{"data":{"type":"members","id":"target"}},"project":{"data":{"type":"projects","id":"p"}}}},{"id":"other","relationships":{"assignee":{"data":{"type":"members","id":"caller"}}}}],"links":{"next":"?page%5Bnumber%5D=5"},"included":[{"id":"p","type":"projects","attributes":{"name":"Target project"}},{"id":"target","type":"members","attributes":{"nickname":"Target"}},{"id":"caller","type":"members","attributes":{"nickname":"Caller"}}]}`), nil
		case query == "page%5Bnumber%5D=5":
			return 200, []byte(`{"data":[{"id":"done","relationships":{"assignee":{"data":{"type":"members","id":"target"}}}}],"links":{"next":null}}`), nil
		default:
			t.Fatalf("unexpected query %q", query)
			return 0, nil, nil
		}
	}
	code, body, err := fetchScopedMemberTodos(fetch, path, "all")
	doc, rows := decodeMCPList(body)
	meta, _ := doc["meta"].(map[string]any)
	included, _ := doc["included"].([]any)
	if err != nil || code != http.StatusOK || len(rows) != 2 || len(included) != 2 || meta["scope_complete"] != false || meta["total"] != nil {
		t.Fatalf("invalid fallback: %d %v %s", code, err, body)
	}
	if strings.Contains(string(body), "caller") || strings.Contains(string(body), "other") {
		t.Fatalf("returned a different member's task: %s", body)
	}
}
