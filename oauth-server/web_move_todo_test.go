package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMoveTodoUpdatesOnlyExistingProjectMapping(t *testing.T) {
	const todo = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const project = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	const otherProject = "cccccccccccccccccccccccccccccccc"
	const original = "dddddddddddddddddddddddddddddddd"
	const destination = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	const page = "/projects/" + project + "/todos/" + todo
	const link = "/todos/" + todo + "/section_links/123"
	current, writes := original, 0
	renderLink := func(w http.ResponseWriter, path, projectID, listID string) {
		fmt.Fprintf(w, `<tr-todo-section-links-field-detail-item data-url="%s?x=1&amp;y=2">
<tr-selector-field class="container-select"><select><option selected value="%s"></option></select></tr-selector-field>
<tr-selector-field class="sectionable-select"><select><option selected value="%s"></option></select></tr-selector-field>
</tr-todo-section-links-field-detail-item>`, path, projectID, listID)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+page, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<meta name="csrf-token" content="csrf"><tr-todo-page-main guid="%s"></tr-todo-page-main><tr-todo-page-breadcrumbs><a href="/parent">父任务</a></tr-todo-page-breadcrumbs>`, todo)
		renderLink(w, link, project, current)
		renderLink(w, "/todos/"+todo+"/section_links/124", otherProject, original)
	})
	mux.HandleFunc("GET /projects/"+project+"/lists/uncompleted/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"guid":%q,"label":"目的清单"}]`, destination)
	})
	mux.HandleFunc("PUT "+link, func(w http.ResponseWriter, r *http.Request) {
		writes++
		if r.Header.Get("X-CSRF-Token") != "csrf" || r.URL.Query().Get("y") != "2" ||
			r.FormValue("project_guid") != project || r.FormValue("todolist_guid") != destination {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		current = destination
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := newWebClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := client.moveTodo(t.Context(), page, todo, project, destination)
		if err != nil || result["section_link_id"] != "123" || result["todolist_id"] != destination || (result["already"] == true) != (i == 1) {
			t.Fatalf("move %d = %#v, %v", i, result, err)
		}
	}
	if current != destination || writes != 1 {
		t.Fatalf("list = %s, writes = %d", current, writes)
	}
}
