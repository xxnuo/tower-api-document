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

func TestProjectTodoFilterCreationDate(t *testing.T) {
	for _, tc := range []struct {
		name, op, date string
		value          any
		want           bool
	}{
		{name: "equal on UTC+8 day", op: "eq", date: "2026-11-19", value: "2026-11-18T16:00:00Z", want: true},
		{name: "greater than earlier day", op: "gt", date: "2026-11-18", value: "2026-11-18T16:00:00Z", want: true},
		{name: "greater than same day", op: "gt", date: "2026-11-19", value: "2026-11-18T16:00:00Z"},
		{name: "less than next day", op: "lt", date: "2026-11-19", value: "2026-11-18T15:59:59.999Z", want: true},
		{name: "less than same day", op: "lt", date: "2026-11-19", value: "2026-11-19T23:59:59+08:00"},
		{name: "date-only value", op: "eq", date: "2026-11-19", value: "2026-11-19", want: true},
		{name: "malformed comparison", op: "lt", date: "2026-11-19", value: "2026-02-30"},
		{name: "date present", op: "not_be_nil", value: "2026-11-19", want: true},
		{name: "date malformed", op: "not_be_nil", value: "上游未提供"},
		{name: "date wrong type", op: "not_be_nil", value: float64(123)},
		{name: "date absent", op: "not_be_nil"},
		{name: "empty date", op: "be_nil", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			condition := map[string]any{"field": "created_at", "operator": tc.op}
			if tc.date != "" {
				condition["value"] = tc.date
			}
			filter, err := newProjectTodoFilter(map[string]any{"conditions": []any{condition}})
			if err != nil {
				t.Fatal(err)
			}
			match, err := filter.matches(map[string]any{"created_at": tc.value}, nil)
			if err != nil || match != tc.want {
				t.Fatalf("match = %v, err = %v, want %v", match, err, tc.want)
			}
		})
	}
	for _, value := range []any{"2026-02-30", "2026-11-19T00:00:00+08:00", []any{"2026-11-19", "2026-11-20"}} {
		_, err := newProjectTodoFilter(map[string]any{"conditions": []any{
			map[string]any{"field": "created_at", "operator": "eq", "value": value},
		}})
		if !errors.Is(err, errWebArguments) {
			t.Errorf("value = %v, err = %v", value, err)
		}
	}
}

func TestProjectTodoFilterReadsMissingCreationDateFromDetail(t *testing.T) {
	filter, err := newProjectTodoFilter(map[string]any{"conditions": []any{
		map[string]any{"field": "created_at", "operator": "eq", "value": "2026-11-19"},
		map[string]any{"field": "due_at", "operator": "eq", "value": "2026-11-19"},
		map[string]any{"field": "start_at", "operator": "lt", "value": "2026-11-19"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	detail := func() (map[string]any, error) {
		calls++
		return map[string]any{"created_at": "2026-11-18T16:00:00Z", "start_at": "2026-11-18"}, nil
	}
	if match, err := filter.matches(map[string]any{"due_at": "2026-11-18"}, detail); err != nil || match || calls != 0 {
		t.Fatalf("match = %v, calls = %d, err = %v", match, calls, err)
	}
	item := map[string]any{"due_at": "2026-11-19"}
	if match, err := filter.matches(item, detail); err != nil || !match || calls != 1 || item["created_at"] != "2026-11-18T16:00:00Z" {
		t.Fatalf("match = %v, calls = %d, err = %v, item = %v", match, calls, err, item)
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

func TestProjectTodoFilterPagesCountMatches(t *testing.T) {
	const projectID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const firstMatch = 15
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
			rows := make([]map[string]any, 0, firstMatch+2)
			for i := 0; i < firstMatch+2; i++ {
				rows = append(rows, map[string]any{
					"id":            fmt.Sprintf("task-%d", i),
					"attributes":    map[string]any{"content": "任务", "is_completed": i == 0, "due_at": nil},
					"relationships": map[string]any{"closer": map[string]any{"data": nil}},
				})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": rows, "links": map[string]any{"next": nil}})
		default:
			value := "Native"
			if r.URL.Path == fmt.Sprintf("/api/v1/todos/task-%d", firstMatch) || r.URL.Path == fmt.Sprintf("/api/v1/todos/task-%d", firstMatch+1) {
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
		map[string]any{"field": "closer_id", "operator": "be_nil"},
	}}
	first, err := filterProjectTodos(t.Context(), cfg, account, args)
	if err != nil || first["has_more"] != true || first["returned"] != 1 || first["items"].([]any)[0].(map[string]any)["id"] != fmt.Sprintf("task-%d", firstMatch) {
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
	if item["id"] != fmt.Sprintf("task-%d", firstMatch+1) || item["todolist_id"] != "l1" {
		t.Fatal(item)
	}
}

func TestProjectTodoFilterMemberRelationships(t *testing.T) {
	for _, tc := range []struct {
		field, op string
		value     any
		list      map[string]any
		detail    map[string]any
		want      bool
		wantErr   bool
	}{
		{field: "assignee_id", op: "eq", value: "m1", list: map[string]any{"assignee": map[string]any{"id": "m1", "nickname": "高畅"}}, want: true},
		{field: "creator_id", op: "eq", value: "m1", list: map[string]any{"creator": "m2"}},
		{field: "closer_id", op: "in", value: []any{"m1", "m2"}, list: map[string]any{"closer": "m2"}, want: true},
		{field: "closer_id", op: "eq", value: []any{"m1", "m2"}, wantErr: true},
		{field: "closer_id", op: "eq", value: "m1", detail: map[string]any{"closer": "m1"}, want: true},
		{field: "closer_id", op: "be_nil", list: map[string]any{"closer": nil}, want: true},
		{field: "closer_id", op: "be_nil", detail: map[string]any{"closer": nil}, want: true},
		{field: "closer_id", op: "be_nil", detail: map[string]any{"closer": map[string]any{"nickname": "未知"}}, wantErr: true},
		{field: "closer_id", op: "not_in", value: "m1", detail: map[string]any{}, wantErr: true},
	} {
		t.Run(tc.field+"/"+tc.op, func(t *testing.T) {
			filter, err := newProjectTodoFilter(map[string]any{"conditions": []any{
				map[string]any{"field": tc.field, "operator": tc.op, "value": tc.value},
			}})
			match := false
			if err == nil {
				var detail func() (map[string]any, error)
				if tc.detail != nil {
					detail = func() (map[string]any, error) { return tc.detail, nil }
				}
				match, err = filter.matches(tc.list, detail)
			}
			if match != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("match = %v, err = %v, want match = %v, want error = %v", match, err, tc.want, tc.wantErr)
			}
		})
	}
}
