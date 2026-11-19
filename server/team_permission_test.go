package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRenameRequiresTargetTeamOwner(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected %s", r.Method)
		}
		switch r.URL.Path {
		case "/api/v1/teams":
			fmt.Fprint(w, `{"data":[{"id":"a"},{"id":"b"}]}`)
		case "/api/v1/teams/a/member":
			fmt.Fprint(w, `{"data":{"attributes":{"role":"admin"}}}`)
		case "/api/v1/teams/b/member":
			fmt.Fprint(w, `{"data":{"attributes":{"role":"owner"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := Config{BaseURL: server.URL, HTTPClient: server.Client()}
	ctx := context.Background()
	if !ownsAnyTeam(ctx, cfg, "token") {
		t.Fatal("missing owner tool")
	}
	for _, sample := range []struct {
		path   string
		denied bool
	}{{"a", true}, {"%61/", true}, {"b", false}, {"unknown", true}} {
		if got := teamWriteDenied(ctx, cfg, "token", "PATCH", "/api/v1/teams/"+sample.path); got != sample.denied {
			t.Errorf("%s denied=%v", sample.path, got)
		}
	}
}
