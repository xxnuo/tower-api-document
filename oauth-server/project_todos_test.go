package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestListProjectTodosContinuation(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page[number]")
		switch r.URL.Path {
		case "/api/v1/projects/p/todolists":
			first, last := 1, 17
			if page == "2" {
				first, last = 17, 18
			}
			lists := make([]map[string]any, 0, last-first)
			for i := first; i < last; i++ {
				lists = append(lists, map[string]any{"id": fmt.Sprint(i)})
			}
			var next any
			if page == "1" {
				next = "?page%5Bnumber%5D=2"
			}
			json.NewEncoder(w).Encode(map[string]any{"data": lists, "links": map[string]any{"next": next}})
		default:
			if r.URL.Query().Get("completed_todo") != "true" {
				t.Errorf("completed_todo missing: %s", r.URL)
			}
			switch {
			case r.URL.Path == "/api/v1/todolists/1/todos" && page == "1":
				fmt.Fprint(w, `{"data":[{"id":"a","attributes":{"content":"A","is_completed":false}}]}`)
			case r.URL.Path == "/api/v1/todolists/1/todos" && page == "2":
				fmt.Fprint(w, `{"data":[{"id":"b","attributes":{"content":"B","is_completed":true}}]}`)
			case r.URL.Path == "/api/v1/todolists/17/todos":
				fmt.Fprint(w, `{"data":[{"id":"a","attributes":{"content":"A"}},{"id":"c","attributes":{"content":"C"}}],"links":{"next":null}}`)
			default:
				fmt.Fprint(w, `{"data":[]}`)
			}
		}
	}))
	defer tower.Close()

	cfg := testConfig(tower.URL)
	args := map[string]any{"project_id": "p", "page_size": 1}
	call := func(key string) (string, bool) {
		return executeMCPTool(t.Context(), cfg, key, &StoredAccount{}, nil, "list_project_todos", args)
	}
	next := regexp.MustCompile(`(?m)^next_cursor：(.+)$`)
	ids := regexp.MustCompile(`(?m)^  - ID (\w+)\b`)
	got := make([]string, 0, 3)
	for range 3 {
		out, failed := call("account-key")
		if failed {
			t.Fatal(out)
		}
		rows := ids.FindAllStringSubmatch(out, -1)
		if len(rows) != 1 {
			t.Fatalf("expected one record: %s", out)
		}
		got = append(got, rows[0][1])
		cursor := next.FindStringSubmatch(out)
		if cursor == nil {
			if !strings.Contains(out, "has_more：否") {
				t.Fatalf("missing completion: %s", out)
			}
			break
		}
		args["cursor"] = cursor[1]
		if _, failed := call("another-account"); !failed {
			t.Fatal("another account accepted the cursor")
		}
		if len(got) == 2 {
			retry, failed := call("account-key")
			if failed || !strings.Contains(retry, "ID c | C") {
				t.Fatalf("retry returned unstable page: %s", retry)
			}
		}
	}
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("IDs %v", got)
	}
}

func TestListProjectTodosOpenAndRepeatedPage(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/todolists") {
			fmt.Fprint(w, `{"data":[{"id":"l"}],"links":{"next":null}}`)
			return
		}
		if r.URL.Query().Has("completed_todo") {
			t.Errorf("Tower includes completed tasks whenever the query parameter is present: %s", r.URL)
		}
		fmt.Fprint(w, `{"data":[{"id":"a","attributes":{"content":"A"}}],"meta":{"has_more":true}}`)
	}))
	defer tower.Close()
	out, failed := executeMCPTool(t.Context(), testConfig(tower.URL), "account-key", &StoredAccount{}, nil, "list_project_todos",
		map[string]any{"project_id": "p", "completed_todo": false, "page_size": 1})
	if !failed || !strings.Contains(out, "GET /api/v1/todolists/l/todos") || !strings.Contains(out, "重复返回相同记录") {
		t.Fatalf("expected reproducible pagination error: %s", out)
	}
}

func TestProjectTodoMatchingPagesAndGlobalOrder(t *testing.T) {
	const projectID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const nLists = 20
	requests := 0
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if strings.HasSuffix(r.URL.Path, "/todolists") {
			var lists []map[string]string
			for i := 1; i <= nLists; i++ {
				lists = append(lists, map[string]string{"id": fmt.Sprint(i)})
			}
			json.NewEncoder(w).Encode(map[string]any{"data": lists, "links": map[string]any{"next": nil}})
			return
		}
		var n int
		fmt.Sscanf(r.URL.Path, "/api/v1/todolists/%d/todos", &n)
		fmt.Fprintf(w, `{"data":[{"id":"%d","attributes":{"created_at":"2026-11-%02dT09:00:00+08:00"}}],"links":{"next":null}}`, n, n)
	}))
	defer tower.Close()
	cfg, acc := testConfig(tower.URL), &StoredAccount{MCPAPIKey: "account"}
	for _, sort := range []bool{false, true} {
		args := map[string]any{"project_id": projectID, "page_size": 4, "conditions": []any{map[string]any{"field": "created_at", "operator": "gt", "value": "2026-11-17"}}}
		firstID := "18"
		if sort {
			args["order"] = "-created_at"
			firstID = "20"
		}
		page, err := filterProjectTodos(t.Context(), cfg, acc, args)
		if err != nil || page["returned"] != 3 || page["has_more"] != false || page["next_cursor"] != nil || page["items"].([]any)[0].(map[string]any)["id"] != firstID {
			t.Fatalf("order %t: page = %v, err = %v", sort, page, err)
		}
	}
	args := map[string]any{"project_id": projectID, "order": "-created_at", "page_size": 3}
	first, err := scanProjectTodos(t.Context(), cfg, "account", acc, args, nil)
	if err != nil || first["items"].([]any)[0].(map[string]any)["id"] != "20" {
		t.Fatalf("%v, %v", first, err)
	}
	args["cursor"] = first["next_cursor"]
	before := requests
	for range 2 {
		page, err := scanProjectTodos(t.Context(), cfg, "account", acc, args, nil)
		if err != nil || page["items"].([]any)[0].(map[string]any)["id"] != "17" || requests != before {
			t.Fatalf("resuming sorted page: %v, %v", page, err)
		}
	}
	delete(args, "order")
	if _, err := scanProjectTodos(t.Context(), cfg, "account", acc, args, nil); err == nil {
		t.Fatal("cursor accepted a different order")
	}
	args["order"] = "not-supported"
	if _, err := scanProjectTodos(t.Context(), cfg, "account", acc, args, nil); err == nil {
		t.Fatal("accepted invalid order")
	}
}
