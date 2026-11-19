package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOwnCommentOnlyAndReadback(t *testing.T) {
	const member = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const comment = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	author, body, writes := "other", "original", 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/todo-page":
			fmt.Fprint(w, `<input id="member-guid" value="`+member+`"><meta name="csrf-token" content="csrf">`)
		case "/api/v1/todos/todo/comments":
			if r.URL.Query().Get("page[number]") == "1" {
				fmt.Fprint(w, `{"data":[],"links":{"next":"https://tower.im/example?page%5Bnumber%5D=2"}}`)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": comment, "attributes": map[string]string{"content": body}, "relationships": map[string]any{"creator": map[string]any{"data": map[string]string{"id": author}}}}}})
		case "/comments/" + comment + ".js":
			if r.Method != "PUT" || r.URL.Query().Get("is_html") != "1" || r.Header.Get("X-CSRF-Token") != "csrf" {
				t.Error("unexpected comment mutation")
			}
			writes++
			body = r.FormValue("comment[content]")
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	client, _ := newWebClient(upstream.URL, nil)
	edit := func() (map[string]any, error) {
		return client.editMyComment(context.Background(), cfg, &StoredAccount{}, "/todo-page", "todo", comment, "<p>changed</p>")
	}
	if _, err := edit(); err == nil || writes != 0 {
		t.Fatal("edited another member's comment")
	}
	author = member
	if _, err := edit(); err != nil || writes != 1 {
		t.Fatalf("edit failed: %v", err)
	}
	if result, err := edit(); err != nil || result["already"] != true || writes != 1 {
		t.Fatalf("replay: %v %v", result, err)
	}
}

func TestNotificationReadRequiresConfirmation(t *testing.T) {
	const path = "/teams/test/notifications"
	isRead, accept, writes := false, false, 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1" + path:
			fmt.Fprintf(w, `{"data":[{"id":"notice","attributes":{"read":%t}}]}`, isRead)
		case path:
			fmt.Fprint(w, `<meta name="csrf-token" content="csrf">`)
		case path + "/notice/read":
			if r.Method != http.MethodPost || r.Header.Get("X-CSRF-Token") != "csrf" {
				t.Error("invalid read action")
			}
			writes++
			isRead = accept
			fmt.Fprint(w, `{"success":true}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	cfg := testConfig(upstream.URL)
	client, _ := newWebClient(upstream.URL, nil)
	mark := func() (map[string]any, error) {
		return client.markNotificationRead(context.Background(), cfg, &StoredAccount{}, "test", "notice")
	}
	if _, err := mark(); err == nil || !strings.Contains(err.Error(), "回读") {
		t.Fatal(err)
	}
	accept = true
	if result, err := mark(); err != nil || result["read"] != true {
		t.Fatal(result, err)
	}
	if result, err := mark(); err != nil || result["already"] != true || writes != 2 {
		t.Fatal(result, err, writes)
	}
}
