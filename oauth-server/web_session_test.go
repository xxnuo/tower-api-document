package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
)

func TestWebEmailLoginAndSearchParse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/account_sign_in", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "pre", Value: "1", Path: "/"})
		w.Write([]byte(`<meta name="csrf-token" content="tok">`))
	})
	mux.HandleFunc("POST /users/account_sign_in", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("authenticity_token") != "tok" || r.Form.Get("email") != "a@b.c" || r.Form.Get("password") != "secret" {
			http.Error(w, "bad", http.StatusUnprocessableEntity)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "_tower2_session", Value: "sess", Path: "/", HttpOnly: true})
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("GET /launchpad/", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("_tower2_session")
		if err != nil || cookie.Value != "sess" {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`<input id="user-email" value="a@b.c"><a href="/teams/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">团队</a>`))
	})
	mux.HandleFunc("GET /teams/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("keyword") != "76083" || r.URL.Query().Get("category") != "2" {
			http.Error(w, "query", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`<span class="result-count">1</span><ul class="results"><li data-category="Todo"><p class="result-title" data-completed="true"><a href="/projects/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/todos/cccccccccccccccccccccccccccccccc">任务 76083</a></p><span class="result-time" title="2026-11-19 11:18"></span></li></ul>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	email, err := client.loginWithEmail(context.Background(), "a@b.c", "secret")
	if err != nil || email != "a@b.c" {
		t.Fatal(email, err)
	}
	restored, err := newWebClient(server.URL, client.jar.export())
	if err != nil {
		t.Fatal(err)
	}
	result, err := restored.searchTodos(context.Background(), url.Values{"keyword": {"76083"}, "page": {"1"}, "category": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := result["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "cccccccccccccccccccccccccccccccc" {
		t.Fatal(result)
	}
}

func TestParseSectionLink(t *testing.T) {
	page := `<tr-todo-page-section-links create-path="/todos/cccccccccccccccccccccccccccccccc/section_links?current_container_sgid=abc"><tr-todo-section-links-field-detail-item data-url="/todos/cccccccccccccccccccccccccccccccc/section_links/42"><a href="/projects/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb">浏览器</a></tr-todo-section-links-field-detail-item></tr-todo-page-section-links>`
	if firstMatch(createPathPattern, page) == "" {
		t.Fatal("create path")
	}
	links := parseSectionLinks(page)
	if len(links) != 1 || links[0].ID != "42" || links[0].ProjectID != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatal(links)
	}
}

func TestSearchIncludesCurrentTeamAndTeamSwitcher(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /launchpad/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<input id="team-id" value="1"><input id="team-guid" value="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"><a href="/teams/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb">其他</a>`))
	})
	var currentSearched bool
	mux.HandleFunc("GET /teams/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/search", func(w http.ResponseWriter, r *http.Request) {
		currentSearched = true
		w.Write([]byte(`<span class="result-count">0</span>`))
	})
	mux.HandleFunc("GET /teams/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<span class="result-count">1</span><ul class="results"><li data-category="Todo"><p class="result-title" data-completed="false"><a href="/projects/cccccccccccccccccccccccccccccccc/todos/dddddddddddddddddddddddddddddddd">命中</a></p></li></ul>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.searchTodos(context.Background(), url.Values{"keyword": {"76083"}, "page": {"1"}, "category": {"2"}})
	if err != nil {
		t.Fatal(err)
	}
	items, _ := result["items"].([]any)
	if !currentSearched || len(items) != 1 || items[0].(map[string]any)["id"] != "dddddddddddddddddddddddddddddddd" {
		t.Fatal(result)
	}
}

func TestWebSearchRequiresKeywordOrTag(t *testing.T) {
	const member = "cccccccccccccccccccccccccccccccc"
	if _, err := webSearchQuery(map[string]any{"keyword": "  ", "member_guid": member}); err == nil {
		t.Fatal("expected an error for a member filter without a keyword or tag")
	}
	query, err := webSearchQuery(map[string]any{"keyword": "  ", "tag": "#测试", "member_guid": member})
	if err != nil || query.Has("keyword") || query.Get("tag") != "#测试" || query.Get("member_guid") != member {
		t.Fatalf("query = %v, err = %v", query, err)
	}
	if _, err := webSearchQuery(map[string]any{"keyword": "测试", "member_guid": "invalid"}); err == nil {
		t.Fatal("expected an error for a malformed member GUID")
	}
}

func TestWebSearchOnlySearchesMemberTeam(t *testing.T) {
	const member = "cccccccccccccccccccccccccccccccc"
	const unknownMember = "dddddddddddddddddddddddddddddddd"
	const forbiddenMember = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	const memberTeam = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const otherTeam = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/api/v1/members/" + member:
			if r.Header.Get("Authorization") != "Bearer token" {
				t.Error("missing account authorization")
			}
			w.Write([]byte(`{"data":{"id":"` + member + `","type":"members","relationships":{"team":{"data":{"id":"` + memberTeam + `","type":"teams"}}}}}`))
		case "/api/v1/members/" + unknownMember:
			http.NotFound(w, r)
		case "/api/v1/members/" + forbiddenMember:
			w.WriteHeader(http.StatusForbidden)
		case "/launchpad/":
			w.Write([]byte(`<input id="team-guid" value="` + memberTeam + `"><a href="/teams/` + otherTeam + `">其他团队</a>`))
		case "/teams/" + memberTeam + "/search", "/teams/" + otherTeam + "/search":
			if len(requests) == 2 && requests[0] == "/api/v1/members/"+member && r.URL.Query().Get("member_guid") != member {
				t.Error("member filter was not forwarded")
			}
			w.Write([]byte(`<span class="result-count">0</span>`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cfg := testConfig(server.URL)
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg.Store = store
	acc, err := store.SaveLogin(&UserInfo{ID: "test"}, TokenResponse{AccessToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWebSession(acc.ID, []webCookie{{Name: "session", Value: "fixture", Path: "/"}}, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		member string
		paths  []string
		fails  bool
	}{
		{member, []string{"/api/v1/members/" + member, "/teams/" + memberTeam + "/search"}, false},
		{unknownMember, []string{"/api/v1/members/" + unknownMember}, true},
		{forbiddenMember, []string{"/api/v1/members/" + forbiddenMember}, true},
		{"", []string{"/launchpad/", "/teams/" + memberTeam + "/search", "/teams/" + otherTeam + "/search"}, false},
	} {
		requests = nil
		query := url.Values{"keyword": {"测试"}, "page": {"1"}}
		if tc.member != "" {
			query.Set("member_guid", tc.member)
		}
		_, err := runWebSearch(t.Context(), cfg, acc, query)
		if (err != nil) != tc.fails || !slices.Equal(requests, tc.paths) {
			t.Errorf("member %q: requests = %v, err = %v", tc.member, requests, err)
		}
	}
	requests = nil
	scoped := url.Values{"keyword": {"Tower"}, "page": {"1"}, "team_id": {otherTeam}}
	result, err := runWebSearch(t.Context(), cfg, acc, scoped)
	if err != nil || !slices.Equal(requests, []string{"/teams/" + otherTeam + "/search"}) || result["message"] == nil {
		t.Fatal(requests, result, err)
	}
	scoped.Set("member_guid", member)
	requests = nil
	if _, err := runWebSearch(t.Context(), cfg, acc, scoped); err == nil || !slices.Equal(requests, []string{"/api/v1/members/" + member}) {
		t.Fatal(requests, err)
	}

}

func TestWebSearchLastPageWithFilters(t *testing.T) {
	query, err := webSearchQuery(map[string]any{"keyword": "测试", "page": 2, "member_guid": "cccccccccccccccccccccccccccccccc", "target_gid": "gid://tower2/Project/123", "sort": 2, "exclude_trash": false})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /launchpad/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<input id="team-guid" value="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">`))
	})
	mux.HandleFunc("GET /teams/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != query.Encode() || query.Get("exclude_trash") != "false" || query.Get("category") != "2" {
			t.Error(r.URL)
		}
		w.Write([]byte(`<span class="result-count">21</span><ul class="results" data-page="2"><li data-category="Todo"><p class="result-title"><a class="tag" href="/search">标签</a><a href="/todos/cccccccccccccccccccccccccccccccc">任务</a></p></li></ul><a id="btn-load-more">加载更多</a>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client, err := newWebClient(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.searchTodos(context.Background(), query)
	if err != nil || result["has_more"] != false || result["next_page"] != nil || result["items"].([]any)[0].(map[string]any)["title"] != "任务" {
		t.Fatal(result, err)
	}
}

func TestRepositoryCommentSearchIdentity(t *testing.T) {
	const path = "/teams/42/repository_documents/22"
	const id = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rows := parseSearchItems(`<li data-category="TrRepository::Document"><p class="result-title">评论： <a href="` + path + "#" + id + `">Document</a></p><p><a class="author">Alice</a><span>needle</span></p></li><li data-category="TrRepository::Document"><p class="result-title"><a href="` + path + `">Document</a></p></li>`)
	if len(rows) != 2 || rows[0]["id"] != id || rows[0]["category"] != "Comment" || rows[0]["parent_url"] != path || rows[0]["excerpt"] != "needle" {
		t.Fatal(rows)
	}
	if rows[1]["id"] != nil || rows[1]["resource_number"] != "22" || rows[1]["read_tool"] != "read_repository_item" {
		t.Fatal(rows[1])
	}
}

func TestTeamResourcePathMapping(t *testing.T) {
	const guid = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/teams/"+guid+"/projects" {
			t.Errorf("unexpected %s", r.URL.Path)
		}
		w.Write([]byte(`<input id="team-guid" value="` + guid + `"><input id="team-id" value="42">`))
	}))
	defer srv.Close()
	store, err := OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := testConfig(srv.URL)
	cfg.Store = store
	acc, err := store.SaveLogin(&UserInfo{ID: "test"}, TokenResponse{AccessToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	if err = store.SaveWebSession(acc.ID, []webCookie{{Name: "s", Value: "fixture", Path: "/"}}, "", ""); err != nil {
		t.Fatal(err)
	}
	path, err := resolveTeamResourcePath(t.Context(), cfg, acc, "/api/v1/teams/"+guid+"/resources/123")
	if err != nil || path != "/api/v1/teams/42/resources/123" {
		t.Fatal(path, err)
	}
}
