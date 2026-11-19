package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebSearchQueryAllowsTagOnly(t *testing.T) {
	query, err := webSearchQuery(map[string]any{"tag": "用户反馈", "page": 2})
	if err != nil || query.Get("tag") != "#用户反馈" || query.Get("keyword") != "" || query.Get("page") != "2" {
		t.Fatalf("query = %v, err = %v", query, err)
	}
	if _, err := webSearchQuery(map[string]any{"tag": "# "}); err == nil {
		t.Fatal("accepted an empty tag")
	}
}

func TestProjectTodoFilterConditions(t *testing.T) {
	conditions := []any{
		map[string]any{"field": "label", "operator": "all_in", "value": []any{"用户反馈", "待办"}},
		map[string]any{"field": "due_at", "operator": "eq", "value": "2026-11-19"},
		map[string]any{"field": "assignee_id", "operator": "in", "value": "m1"},
	}
	args := map[string]any{"conditions": conditions, "status": "uncompleted"}
	filter, err := newProjectTodoFilter(args)
	if err != nil {
		t.Fatal(err)
	}
	item := map[string]any{
		"labels": []any{"用户反馈", "待办", "其他"}, "due_at": "2026-11-18T17:00:00Z",
		"assignee": "m1", "is_completed": false,
	}
	if match, err := filter.matches(item, nil); err != nil || !match {
		t.Fatalf("expected all conditions to match, got %v, %v", match, err)
	}
	item["assignee"] = "m2"
	if match, err := filter.matches(item, nil); err != nil || match {
		t.Fatalf("expected all conditions to reject, got %v, %v", match, err)
	}
	args["match"] = "any"
	filter, err = newProjectTodoFilter(args)
	if err != nil {
		t.Fatal(err)
	}
	if match, err := filter.matches(item, nil); err != nil || !match {
		t.Fatalf("expected any condition to match, got %v, %v", match, err)
	}
	item["is_completed"] = true
	if match, err := filter.matches(item, nil); err != nil || match {
		t.Fatalf("completion status should still apply, got %v, %v", match, err)
	}
}

func TestProjectTodoFilterReadsDetailsOnlyForCandidates(t *testing.T) {
	filter, err := newProjectTodoFilter(map[string]any{"conditions": []any{
		map[string]any{"field": "custom.select_oeM6vDNt", "operator": "in", "value": "Web"},
		map[string]any{"field": "start_at", "operator": "gt", "value": "2026-11-18"},
		map[string]any{"field": "title", "operator": "in", "value": "Test"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if matched, err := filter.matches(map[string]any{"content": "其他"}, nil); err != nil || matched {
		t.Fatalf("expected rejected row to skip detail, got %v %v", matched, err)
	}
	matched, err := filter.matches(map[string]any{"content": "test 123"}, func() (map[string]any, error) {
		return map[string]any{
			"start_at":      "2026-11-19",
			"custom_fields": map[string]any{"select_oeM6vDNt": map[string]any{"value": "Web"}},
		}, nil
	})
	if err != nil || !matched {
		t.Fatalf("expected title, custom option and date to match, got %v %v", matched, err)
	}
}

func TestWebSearchTagPagination(t *testing.T) {
	const team = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	mux := http.NewServeMux()
	mux.HandleFunc("GET /launchpad/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `<input id="team-guid" value="%s">`, team)
	})
	mux.HandleFunc("GET /teams/"+team+"/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("tag") != "#标签" || q.Get("keyword") != "" {
			t.Error(r.URL)
		}
		n := "cccccccccccccccccccccccccccccccc"
		if q.Get("page") == "2" {
			n = "dddddddddddddddddddddddddddddddd"
		}
		fmt.Fprintf(w, `<span class="result-count">21</span><li data-category="Todo"><p class="result-title"><a href="/todos/%s">任务</a></p></li>`, n)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client, err := newWebClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	query, _ := webSearchQuery(map[string]any{"tag": "标签"})
	first, err := client.searchTodos(t.Context(), query)
	if err != nil || first["next_page"] != 2 || first["tag"] != "#标签" {
		t.Fatalf("first = %v, err = %v", first, err)
	}
	query.Set("page", "2")
	second, err := client.searchTodos(t.Context(), query)
	if err != nil || second["has_more"] != false || second["next_page"] != nil {
		t.Fatalf("second = %v, err = %v", second, err)
	}
	id := func(page map[string]any) any {
		return page["items"].([]any)[0].(map[string]any)["id"]
	}
	if id(first) == id(second) {
		t.Fatalf("pages repeated a result: %v", id(first))
	}
}

func TestProjectTodoFilterCursorResumesAfterRequestLimit(t *testing.T) {
	const projectID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	requests := 0
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Error("project filter must only read upstream")
		}
		switch r.URL.Path {
		case "/api/v1/projects/" + projectID + "/todolists":
			fmt.Fprint(w, `{"data":[{"id":"l1"}],"links":{"next":null}}`)
		case "/api/v1/todolists/l1/todos":
			rows := make([]map[string]any, 0, projectFilterRequestLimit+2)
			for i := 0; i < projectFilterRequestLimit+2; i++ {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("task-%d", i), "attributes": map[string]any{"content": "任务", "is_completed": i == 0, "due_at": nil}})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": rows, "links": map[string]any{"next": nil}})
		default:
			value := "Native"
			if r.URL.Path == fmt.Sprintf("/api/v1/todos/task-%d", projectFilterRequestLimit) {
				value = "Web"
			}
			fmt.Fprintf(w, `{"data":{"attributes":{"start_at":"2026-11-19"},"relationships":{"custom_field_value":{"data":{"id":"v1","type":"todos_custom_field_values"}}}},"included":[{"id":"v2","type":"todos_custom_field_values","attributes":{"custom_fields":{"platform":{"value":"unrelated"}}}},{"id":"v1","type":"todos_custom_field_values","attributes":{"custom_fields":{"platform":{"value":%q}}}}]}`, value)
		}
	}))
	defer tower.Close()

	cfg := testConfig(tower.URL)
	account := &StoredAccount{MCPAPIKey: "account-a"}
	args := map[string]any{"project_id": projectID, "page_size": 1, "status": "uncompleted", "conditions": []any{
		map[string]any{"field": "custom.platform", "operator": "in", "value": "Web"},
		map[string]any{"field": "start_at", "operator": "eq", "value": "2026-11-19"},
		map[string]any{"field": "due_at", "operator": "be_nil"},
	}}
	first, err := filterProjectTodos(t.Context(), cfg, account, args)
	if err != nil || first["has_more"] != true || first["returned"] != 0 || requests != projectFilterRequestLimit {
		t.Fatalf("first = %v, requests = %d, err = %v", first, requests, err)
	}
	args["cursor"] = first["next_cursor"]
	args["match"] = "any"
	if _, err := filterProjectTodos(t.Context(), cfg, account, args); !errors.Is(err, errWebArguments) {
		t.Fatalf("cursor leaked across conditions: %v", err)
	}
	delete(args, "match")
	other := &StoredAccount{MCPAPIKey: "account-b"}
	if _, err := filterProjectTodos(t.Context(), cfg, other, args); !errors.Is(err, errWebArguments) {
		t.Fatalf("cursor leaked across accounts: %v", err)
	}
	last, err := filterProjectTodos(t.Context(), cfg, account, args)
	if err != nil || last["has_more"] != false || last["next_cursor"] != nil || last["returned"] != 1 {
		t.Fatalf("last = %v, err = %v", last, err)
	}
	item := last["items"].([]any)[0].(map[string]any)
	if item["id"] != fmt.Sprintf("task-%d", projectFilterRequestLimit) || item["todolist_id"] != "l1" {
		t.Fatal(item)
	}
}
