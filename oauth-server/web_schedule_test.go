package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTodoSchedule(t *testing.T) {
	const id = "4236c437d93cdf2b916708295f71e93b"
	const project = "70a43881f303cd705574fc7b5446ffa9"
	const path = "/projects/" + project + "/todos/" + id
	cycle := todoRecurrence{Unit: "day", Interval: 1, Selected: []any{}}
	milestone, estimated := false, 0
	date := "2026-11-20 00:00:00 +0800,2026-11-21 23:59:59 +0800"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Header.Get("X-CSRF-Token") != "csrf" {
			t.Error("missing CSRF token")
		}
		switch r.URL.Path {
		case path:
			action := "post"
			if milestone {
				action = "delete"
			}
			fmt.Fprintf(w, `<meta name="csrf-token" content="csrf"><tr-todo-page-main guid="%s" edit-url="%s/edit">
<a href="/todos/151013226/date_time_selector" class="date-time-selector" data-access="true"><input type="hidden" value="%s"></a>
</tr-todo-page-main><a href="/todos/%s/milestone" class="milestone-button" data-method="%s" data-access="true"></a><tr-react-task-page-working-hours work-hours='{"todo_guid":"%s","can_edit_todo":true,"estimated_time":%d}'></tr-react-task-page-working-hours>`, id, path, date, id, action, id, estimated)
		case "/todos/151013226/date_time_selector":
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("selector must be requested as AJAX")
			}
			raw, _ := json.Marshal(cycle)
			fmt.Fprintf(w, `tower.setOuterHTML(link, '<tr-date-time-picker-field-list cycle-setting=\"%s\"><\/tr-date-time-picker-field-list>');`, html.EscapeString(string(raw)))
		case path + "/edit":
			var edit struct {
				Values map[string]json.RawMessage `json:"todos_management"`
			}
			if r.Method != "PATCH" || json.NewDecoder(r.Body).Decode(&edit) != nil || len(edit.Values) != 1 ||
				json.Unmarshal(edit.Values["cycle_setting"], &cycle) != nil {
				t.Error("unexpected date update")
				w.WriteHeader(400)
			}
		case "/todos/" + id + "/milestone":
			milestone = r.Method == "POST"
		case "/todos/" + id + "/estimated_times":
			if r.Method != "PUT" || r.FormValue("estimated_time") != "150" {
				t.Error("unexpected estimate update")
			}
			estimated = 150
		default:
			t.Error("unexpected request path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := client.todoSchedule(context.Background(), path, id, name, args)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	call("set_todo_estimated_time", map[string]any{"estimated_minutes": 150})
	call("set_todo_milestone", map[string]any{"enabled": true})
	call("set_todo_recurrence", map[string]any{"enabled": true, "unit": "week", "interval": 2, "selected_days": []any{"5", "1"}})
	if !cycle.Active || cycle.Interval != 2 || stringifyArg(cycle.Selected[0]) != "1" || !milestone || estimated != 150 {
		t.Fatalf("unexpected saved state: %+v, %v, %d", cycle, milestone, estimated)
	}
	result := call("get_todo_schedule", nil)
	if result["estimated_minutes"] != 150 || result["milestone"] != true {
		t.Fatalf("unexpected readback: %+v", result)
	}
	call("set_todo_recurrence", map[string]any{"enabled": false})
	call("set_todo_milestone", map[string]any{"enabled": false})
	if cycle.Active || milestone {
		t.Fatal("settings were not cleared")
	}
	// Guard against accidentally applying a mutation to another task's page.
	if _, err := client.todoSchedule(context.Background(), path, project, "set_todo_milestone", map[string]any{"enabled": true}); err == nil {
		t.Fatal("accepted mismatched task identity")
	}
}
