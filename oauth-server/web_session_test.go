package main

import (
	"context"
	"net/http"
	"net/http/httptest"
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
		w.Write([]byte(`<ul class="results"><li data-category="Todo"><p class="result-title" data-completed="true"><a href="/projects/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/todos/cccccccccccccccccccccccccccccccc">任务 76083</a></p><span class="result-time" title="2026-11-19 11:18"></span></li></ul>`))
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
	result, err := restored.searchTodos(context.Background(), "76083")
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
	mux.HandleFunc("GET /teams/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa/search", func(w http.ResponseWriter, r *http.Request) { currentSearched = true })
	mux.HandleFunc("GET /teams/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb/search", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<ul class="results"><li data-category="Todo"><p class="result-title" data-completed="false"><a href="/projects/cccccccccccccccccccccccccccccccc/todos/dddddddddddddddddddddddddddddddd">命中</a></p></li></ul>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.searchTodos(context.Background(), "76083")
	if err != nil {
		t.Fatal(err)
	}
	items, _ := result["items"].([]any)
	if !currentSearched || len(items) != 1 || items[0].(map[string]any)["id"] != "dddddddddddddddddddddddddddddddd" {
		t.Fatal(result)
	}
}
