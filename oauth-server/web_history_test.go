package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTodoHistoryCrossesCollapsedRange(t *testing.T) {
	const row = `<div class="event" id="event-1"><span class="event-action">创建了任务</span></div>`
	const comment = `<div class="comment" id="c"><div class="comment-content">说明</div></div>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/todo":
			fmt.Fprintf(w, `<tr-comments>%s<div data-comment-streams-range="1,3" data-url="/comment_streams?target_gid=abc"></div>%s</tr-comments>`, row, comment)
		case "/comment_streams":
			if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				http.Error(w, "expected AJAX", 400)
				return
			}
			if r.URL.Query().Get("start") != "2" || r.URL.Query().Get("end") != "3" || r.URL.Query().Get("target_gid") != "abc" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, `var list = '<div><div class=\"event\" id=\"event-3\"><b class=\"event-action\">改为 A\'B<\/b><\/div><\/div>\n';`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, _ := newWebClient(server.URL, nil)
	got, err := c.todoHistory(context.Background(), "/todo", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if got["has_more"] != false || !strings.Contains(fmt.Sprint(got["items"]), "改为 A'B") || !strings.Contains(fmt.Sprint(got["items"]), "说明") {
		t.Fatal(got)
	}
}
