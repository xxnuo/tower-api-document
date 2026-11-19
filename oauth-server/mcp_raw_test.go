package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawRequestProjectionOrderAndErrors(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/todolists/t/todos":
			if r.URL.Query().Has("order") {
				t.Errorf("unsupported order reached Tower: %s", r.URL)
			}
			fmt.Fprint(w, `{"data":[{"id":"1","type":"todos","attributes":{"content":"hello","created_at":"2026-01-01T00:00:00Z","owner":"person","url":"https://tower.im/signed"},"relationships":{"file":{"data":{"id":"s"}}}}],"included":[{"id":"s","attributes":{"url":"https://tower.im/signed"}}],"meta":{"total":3}}`)
		case "/api/v1/projects/p/todos", "/api/v1/unknown":
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"missing"}`)
		default:
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"permission denied"}`)
		}
	}))
	defer tower.Close()
	call := func(path string, args map[string]any) (string, bool) {
		args["method"], args["path"] = http.MethodGet, path
		return executeMCPTool(t.Context(), testConfig(tower.URL), "", &StoredAccount{}, nil, "raw_request", args)
	}
	path := "/api/v1/todolists/t/todos"
	fields := []any{"id", "content", "created_at"}
	raw, failed := call(path, map[string]any{})
	if failed || !strings.Contains(raw, "included") || !strings.Contains(raw, "signed") {
		t.Fatalf("raw default: %s", raw)
	}
	projected, failed := call(path, map[string]any{"fields": fields})
	if failed || !strings.Contains(projected, "ID 1 | hello") || !strings.Contains(projected, "created_at") || !strings.Contains(projected, "meta") {
		t.Fatalf("projected: %s", projected)
	}
	for _, unwanted := range []string{"signed", "included", "owner", "relationships"} {
		if strings.Contains(projected, unwanted) {
			t.Fatalf("projected contains %q: %s", unwanted, projected)
		}
	}
	data, failed := call(path, map[string]any{"data_only": true, "fields": fields})
	if failed || !strings.HasPrefix(data, "- ID 1 | hello") || strings.Contains(data, "meta") {
		t.Fatalf("data only: %s", data)
	}
	ordered, failed := call(path, map[string]any{"query": "order=-created_at"})
	if !failed || !strings.Contains(ordered, "GET "+path) || !strings.Contains(ordered, "order=-created_at") {
		t.Fatalf("unsupported order: %s", ordered)
	}
	for _, tt := range []struct {
		path, status, body string
		hint               bool
	}{
		{"/api/v1/projects/p/todos", "404", "missing", true},
		{"/api/v1/unknown", "404", "missing", false},
		{"/api/v1/projects/p/private", "403", "permission denied", false},
	} {
		out, failed := call(tt.path, map[string]any{})
		if !failed || !strings.Contains(out, "GET "+tt.path) || !strings.Contains(out, "HTTP "+tt.status) ||
			!strings.Contains(out, tt.body) || (strings.Contains(out, "list_project_todos") != tt.hint) {
			t.Fatalf("error for %s: %s", tt.path, out)
		}
	}
}

func TestRawEventsUsesTowerLimit(t *testing.T) {
	tower := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Get("limit") != "50" || q.Has("page[size]") {
			t.Errorf("expected native event limit: %s", r.URL)
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer tower.Close()
	got, failed := executeRawRequest(t.Context(), testConfig(tower.URL), "", &StoredAccount{}, map[string]any{"path": "/api/v1/teams/t/events", "method": "GET", "page_size": 50})
	if failed {
		t.Fatal(got)
	}
}
