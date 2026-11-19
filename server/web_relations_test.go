package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const (
	relationSource  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	relationTarget  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	relationProject = "cccccccccccccccccccccccccccccccc"
)

func TestWebTodoDependencies(t *testing.T) {
	ctx := context.Background()
	pagePath := "/projects/" + relationProject + "/todos/" + relationSource
	depPath := "/todos/" + relationSource + "/dependencies"
	linked := false
	mutations := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == pagePath && r.Method == http.MethodGet:
			row := ""
			if linked {
				row = fmt.Sprintf(`<tr-grid-dependency-row guid="%s" dependency-type="parent"><span class="todo-rest">Follow &amp; verify</span><a data-method="delete" href="%s/789"></a></tr-grid-dependency-row>`, relationTarget, depPath)
			}
			fmt.Fprintf(w, `<meta name="csrf-token" content="session-token"><div class="todo-page-main"><tr-todo-page-main guid="%s"></tr-todo-page-main><tr-todo-page-dependencies>%s</tr-todo-page-dependencies></div>`, relationSource, row)
		case r.URL.Path == depPath && r.Method == http.MethodPost:
			if r.ParseForm() != nil || r.Form.Get("todos_dependency[todo_id]") != relationTarget ||
				r.Form.Get("todos_dependency[category]") != "parent" ||
				r.Header.Get("X-CSRF-Token") != "session-token" || linked {
				t.Error("invalid dependency creation")
			}
			mutations++
			linked = true
		case r.URL.Path == depPath+"/789" && r.Method == http.MethodDelete:
			if r.Header.Get("X-CSRF-Token") != "session-token" || !linked {
				t.Error("invalid dependency deletion")
			}
			mutations++
			linked = false
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	args := map[string]any{"dependency_todo_id": relationTarget, "direction": "parent"}
	for _, step := range []struct {
		name    string
		already bool
	}{
		{"add_todo_dependency", false},
		{"add_todo_dependency", true},
		{"remove_todo_dependency", false},
		{"remove_todo_dependency", true},
	} {
		result, err := client.runTodoRelation(ctx, Config{}, nil, step.name, pagePath, relationSource, args)
		if err != nil || (result["already"] == true) != step.already {
			t.Fatalf("%s: result=%v error=%v", step.name, result, err)
		}
		items, err := client.todoDependencies(ctx, pagePath, relationSource)
		want := 0
		if step.name == "add_todo_dependency" {
			want = 1
		}
		if err != nil || len(items) != want {
			t.Fatalf("items=%v error=%v", items, err)
		}
		if want == 1 {
			if _, err := client.mutateTodoDependency(ctx, pagePath, relationSource, relationTarget, "child", true); err == nil {
				t.Fatal("accepted a direct dependency cycle")
			}
		}
		if want == 1 && (items[0].TodoID != relationTarget || items[0].Direction != "parent" || items[0].Title != "Follow & verify") {
			t.Fatalf("unexpected dependency: %+v", items[0])
		}
	}
	if mutations != 2 {
		t.Fatal(mutations)
	}
	if _, err := readTodoDependencyPage(`<tr-todo-page-dependencies></tr-todo-page-dependencies>`, relationSource); err == nil {
		t.Fatal("accepted page without matching task")
	}
}

func TestWebCopyTodo(t *testing.T) {
	ctx := context.Background()
	project := "dddddddddddddddddddddddddddddddd"
	list := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	pagePath := "/projects/" + relationProject + "/todos/" + relationSource
	action := pagePath + "/copy"
	formPath := "/todos/" + relationSource + "/copy/new"
	reply := `{"success":true}`
	posts := 0
	ambiguous := false
	sourceTitle := "unique task title"
	created := "ffffffffffffffffffffffffffffffff"
	colliding := "11111111111111111111111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/todos/"+relationSource:
			fmt.Fprintf(w, `{"data":{"id":%q,"attributes":{"content":%q}}}`, relationSource, sourceTitle)
		case r.URL.Path == "/api/v1/todolists/"+list+"/todos":
			rows := []string{fmt.Sprintf(`{"id":%q,"attributes":{"content":%q}}`, relationTarget, sourceTitle)}
			if posts > 0 && reply == `{"success":true}` {
				rows = append(rows, fmt.Sprintf(`{"id":%q,"attributes":{"content":%q}}`, created, sourceTitle))
			}
			if posts > 0 && ambiguous {
				rows = append(rows, fmt.Sprintf(`{"id":%q,"attributes":{"content":%q}}`, colliding, sourceTitle))
			}
			fmt.Fprintf(w, `{"data":[%s],"links":{"next":null}}`, strings.Join(rows, ","))
		case r.URL.Path == "/projects/"+project+"/lists/uncompleted/" && r.Method == http.MethodGet:
			fmt.Fprintf(w, `[{"label":"Target","guid":%q}]`, list)
		case r.URL.Path == formPath && r.Method == http.MethodGet:
			if r.URL.Query().Get("project_guid") != relationProject || r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
				t.Error("wrong copy form request")
			}
			form := fmt.Sprintf(`<form class="form-todo-copy" method="post" action="%s"><input name="authenticity_token" value="form+token"><select name="copy[tpuid]"><option value="%s">Target</option></select></form>`, action, project)
			fmt.Fprintf(w, "showActionForm({template: '%s'});", strings.ReplaceAll(form, `"`, `\"`))
		case r.URL.Path == action && r.Method == http.MethodPost:
			posts++
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			want := url.Values{"authenticity_token": {"form+token"}, "copy[count]": {"1"}, "copy[tpuid]": {project}, "copy[tluid]": {list}, "copy[copy_desc]": {"1"}, "copy[copy_assignee]": {"0"}, "copy[copy_todos_dependencies]": {"1"}, "copy[copy_due_at]": {"1"}, "copy[copy_sub_todos]": {"1"}, "copy[copy_attachments]": {"1"}, "copy[copy_labels]": {"1"}, "copy[copy_cc_members]": {"1"}}
			for key := range want {
				if r.Form.Get(key) != want.Get(key) {
					t.Errorf("%s = %q", key, r.Form.Get(key))
				}
			}
			if reply == "redirect" {
				http.Redirect(w, r, action+"/other", http.StatusTemporaryRedirect)
				return
			}
			fmt.Fprint(w, reply)
		case r.URL.Path == action+"/other":
			t.Error("replayed copy POST")
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{BaseURL: server.URL, HTTPClient: server.Client()}
	acc := &StoredAccount{}
	args := map[string]any{
		"project_id": project, "todolist_id": list, "copy_assignee": false, "copy_due_at": true,
		"copy_sub_todos": true, "copy_attachments": true, "copy_labels": true, "copy_cc_members": true,
		"copy_todos_dependencies": true,
	}
	for _, step := range []struct {
		reply, identity, copyID string
		ambiguous               bool
	}{
		{`{"success":true}`, "unique_candidate", created, false},
		{`{"success":true}`, "ambiguous", "", true},
		{`{"success":false}`, "unconfirmed", "", false},
		{"redirect", "unconfirmed", "", false},
	} {
		reply, posts, ambiguous = step.reply, 0, step.ambiguous
		result, err := client.copyTodo(ctx, cfg, acc, pagePath, relationSource, args)
		if err != nil || result["identity_status"] != step.identity || stringifyArg(result["candidate_todo_id"]) != step.copyID || posts != 1 {
			t.Fatalf("result=%v error=%v POSTs=%d", result, err, posts)
		}
	}
	args["todolist_id"] = relationTarget
	if _, err := client.copyTodo(ctx, cfg, acc, pagePath, relationSource, args); err == nil || posts != 1 {
		t.Fatalf("invalid list error=%v POSTs=%d", err, posts)
	}
}
