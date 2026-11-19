package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebProjectFields(t *testing.T) {
	const id = "70a43881f303cd705574fc7b5446ffa9"
	const base = "/projects/" + id
	for _, mode := range []string{"fields", "empty", "unavailable", "options unavailable", "hidden"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case base + "/labels":
					fmt.Fprint(w, `<div class="project-label-list"><div id="label_41" class="label-item"><div class="name">待 &amp;办</div></div></div><div class="team-label-list"></div>`)
				case base + "/list/todos":
					if mode == "unavailable" {
						fmt.Fprint(w, `<form action="/users/sign_in"></form>`)
						return
					}
					fmt.Fprintf(w, `<tr-todo-combo-filter><a href="%s/filters/7/lazy_loaders">筛选</a></tr-todo-combo-filter><template id="tpl-default-todolist-todo-row"><tr-grid-todo-row>`, base)
					if mode != "empty" && mode != "hidden" {
						fmt.Fprintf(w, `<tr-grid-select-cell cache-key="field_prototypes/49-0" key="select_k8" name="平台" editor-url="%s/list/todo_cell_editor?cell_type=select&amp;field_key=select_k8"></tr-grid-select-cell>`, base)
					}
					fmt.Fprint(w, `</tr-grid-todo-row></template>`)
				case base + "/list/todo_cell_editor":
					if r.URL.Query().Get("field_key") != "select_k8" {
						t.Error(r.URL)
					}
					if mode == "options unavailable" {
						http.Error(w, "", http.StatusForbidden)
						return
					}
					fmt.Fprint(w, `<tr-selector-field><select><option value=""></option><option value="blank">无</option><option value="14406524">Web</option></select></tr-selector-field>`)
				case base + "/filters/7/lazy_loaders.js":
					fmt.Fprint(w, "(function() {\n  var fresh = '<tr-project-todo-advance-filter>")
					if mode == "hidden" {
						fmt.Fprint(w, `<select><optgroup label=\"任务自定义字段\"><option value=\"custom_field_values.select_hidden\">隐藏字段<\/option><\/optgroup><\/select>\n<template id=\"tpl-todo-filter-custom_field_values__select_hidden\"><select name=\"projects_filter[conditions_attributes][8][form_value][]\"><option value=\"42aabbcc\">选项<\/option><\/select><\/template>`)
					}
					fmt.Fprint(w, "</tr-project-todo-advance-filter>'\n  var old = null;\n})();")
				default:
					t.Error(r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client, err := newWebClient(server.URL, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.projectFields(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			check := func(key, want string) {
				t.Helper()
				got, err := json.Marshal(result[key])
				if err != nil || string(got) != want {
					t.Errorf("%s = %s, want %s, error %v", key, got, want, err)
				}
			}
			check("labels", `[{"id":41,"name":"待 \u0026办","scope":"project"}]`)
			switch mode {
			case "fields":
				check("custom_fields", `[{"key":"select_k8","name":"平台","field_type":"select","options":[{"id":"14406524","name":"Web"}]}]`)
			case "empty":
				check("custom_fields", `[]`)
			case "unavailable":
				check("custom_fields", `null`)
			case "options unavailable":
				check("custom_fields", `[{"key":"select_k8","name":"平台","field_type":"select","options":null}]`)
			case "hidden":
				check("custom_fields", `[{"key":"select_hidden","name":"隐藏字段","options":[{"id":"42aabbcc","name":"选项"}]}]`)
			}
			check("complete", fmt.Sprint(mode == "fields" || mode == "empty"))
		})
	}
}
