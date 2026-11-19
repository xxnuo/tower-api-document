package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTodoListDateAvailability(t *testing.T) {
	raw := []byte(`{"data":[{"id":"T1","type":"todos","attributes":{"content":"有截止日","created_at":"2026-11-01T09:00:00.123+08:00","due_at":"2026-11-17"}},{"id":"T2","type":"todos","attributes":{"content":"没有截止日","due_at":null}},{"id":"T3","type":"todos","attributes":{"content":"未提供截止日"}}]}`)
	for _, tool := range []string{"list_todos", "list_member_assigned_completed_todos", "list_member_assigned_uncompleted_todos"} {
		got := resultProfiles[tool].text(compactTowerBody(raw, tool))
		for _, want := range []string{"created_at：2026-11-01T09:00:00.123+08:00", "updated_at：上游未提供", "due_at：2026-11-17", "due_at：无截止日期", "due_at：上游未提供"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s missing %q: %s", tool, want, got)
			}
		}
	}
}

func TestAssignedTodoDueFilterUsesFullSource(t *testing.T) {
	calls := 0
	rows := []map[string]any{}
	for i := range 210 {
		var due any = "2026-11-19"
		if i >= 185 {
			due = "2026-11-18T23:30:00+08:00"
		}
		rows = append(rows, map[string]any{"id": fmt.Sprintf("T%d", i), "type": "todos", "attributes": map[string]any{"content": "task", "due_at": due, "is_active": true, "is_completed": false}})
	}
	fallback := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.RawQuery != "" {
			t.Errorf("unexpected request: %s", r.URL)
		}
		switch r.URL.Path {
		case "/api/v1/members/m/assigned_todos":
			if fallback {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": rows})
		case "/api/v1/members/m/assigned_uncompleted_todos":
			json.NewEncoder(w).Encode(map[string]any{"data": rows[:200]})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	cfg := Config{BaseURL: upstream.URL, HTTPClient: upstream.Client()}
	index := map[string]mcpToolSpec{}
	for _, tool := range towerMCPTools {
		index[tool.Name] = tool
	}
	name := "list_member_assigned_uncompleted_todos"
	got, failed := executeMCPTool(context.Background(), cfg, "", &StoredAccount{}, index, name, map[string]any{"member_id": "m", "due_before": "2026-11-19"})
	if failed || calls != 1 || strings.Count(got, "- ID ") != 25 || !strings.Contains(got, "scope_complete：是") || strings.Contains(got, "- ID T184") || !strings.Contains(got, "- ID T209") {
		t.Fatal(got, calls)
	}
	got, failed = executeMCPTool(context.Background(), cfg, "", &StoredAccount{}, index, name, map[string]any{"member_id": "m", "due_before": "2026-11-19", "page": 2, "page_size": 20})
	if failed || calls != 2 || strings.Count(got, "- ID ") != 5 || !strings.Contains(got, "- ID T205") {
		t.Fatal(got, calls)
	}
	if _, failed := executeMCPTool(context.Background(), cfg, "", &StoredAccount{}, index, name, map[string]any{"member_id": "m", "due_before": "2026-02-30"}); !failed || calls != 2 {
		t.Fatal("invalid date reached upstream", calls)
	}
	fallback = true
	got, failed = executeMCPTool(context.Background(), cfg, "", &StoredAccount{}, index, name, map[string]any{"member_id": "m", "due_before": "2026-11-19"})
	if failed || calls != 4 || strings.Count(got, "- ID ") != 15 || !strings.Contains(got, "scope_complete：否") || !strings.Contains(got, "scope_warning：") {
		t.Fatal(got, calls)
	}
}

func TestSearchResultReadableTime(t *testing.T) {
	items := parseSearchItems(`<li data-category="Comment"><p class="result-title"><a href="/todo#comment">comment</a></p><span class="result-time" title="2026-11-19 11:18" data-readable-time="2026-11-19T11:18:01.123+08:00"></span></li>`)
	if len(items) != 1 || items[0]["result_time"] != "2026-11-19T11:18:01.123+08:00" || items[0]["created_at"] != nil || items[0]["updated_at"] != nil {
		t.Fatal(items)
	}
}

func TestTodoDueFilterBeijingDay(t *testing.T) {
	now := time.Date(2026, time.November, 18, 16, 1, 0, 0, time.UTC)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"overdue": true}, "2026-11-19"},
		{map[string]any{"overdue": false}, ""},
		{map[string]any{"overdue": true, "due_before": "2026-11-17"}, "2026-11-17"},
		{map[string]any{"overdue": true, "due_before": "2026-11-21"}, "2026-11-19"},
	} {
		got, err := todoDueCutoff(tc.args, now)
		if err != nil || got != tc.want {
			t.Errorf("%v: got %q, %v, want %q", tc.args, got, err, tc.want)
		}
	}
	if got := todoDueDay("2026-11-18T17:00:00Z"); got != "2026-11-19" {
		t.Fatal(got)
	}
}

func TestAgentTimestampPreservesUnroundedPrecision(t *testing.T) {
	got := agentText([]byte(`{"results":[{"created_at":"2026-11-18 16:00:00.123000+0800","due_at":"2026-11-19","content":"2026-11-18 16:00:00+0800"}]}`))
	for _, want := range []string{"created_at：2026-11-18T16:00:00.123000+08:00", "due_at：2026-11-19", "content：2026-11-18 16:00:00+0800"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q: %s", want, got)
		}
	}
}
