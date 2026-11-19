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
