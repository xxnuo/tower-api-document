package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCompactTodoList(t *testing.T) {
	raw := []byte(`{"data":[{"id":"t","type":"todos","attributes":{"content":"task","desc":"long body","is_completed":false},"relationships":{"assignee":{"data":{"type":"members","id":"m"}}}}],"included":[{"id":"m","type":"members","attributes":{"nickname":"Alice","gavatar":"image"}}],"links":{"next":"page2"}}`)
	out, err := json.Marshal(compactTowerBody(raw, "list_todos"))
	if err != nil || string(out) != `{"items":[{"assignee":{"id":"m","nickname":"Alice"},"content":"task","created_at":"上游未提供","due_at":"上游未提供","id":"t","is_completed":false,"updated_at":"上游未提供"}],"next":"page2"}` {
		t.Fatal(string(out), err)
	}
}

func TestEveryRegisteredToolHasAResultProfile(t *testing.T) {
	defs := listMCPToolDefinitions()
	if len(defs) != len(resultProfiles) {
		t.Fatalf("%d registered tools, %d profiles", len(defs), len(resultProfiles))
	}
	for _, d := range defs {
		name := d["name"].(string)
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(name, "tower_") {
				t.Fatal("unexpected tool prefix")
			}
			p := resultProfiles[name]
			if p.heading == "" || p.fields == "" {
				t.Fatal("missing result profile")
			}
			fields := strings.Fields(p.fields)
			record := map[string]any{"extra_upstream_field": "extra-value"}
			for _, f := range fields {
				if f != "*" {
					record[f] = "value-for-" + f + "!"
				}
			}
			text := p.text(record)
			for _, f := range fields {
				if f != "*" && !strings.Contains(text, "value-for-"+f+"!") {
					t.Errorf("missing %s in %s", f, text)
				}
			}
			if strings.Contains(text, "extra-value") != slices.Contains(fields, "*") {
				t.Fatalf("unexpected extension field handling: %s", text)
			}
		})
	}
}

func TestMemberTodoViews(t *testing.T) {
	raw := []byte(`{"data":[{"id":"T1","type":"todos","attributes":{"content":"发布","due_at":"2026-11-20","completed_at":"周一","is_completed":true},"relationships":{"assignee":{"data":{"id":"M1","type":"members"}},"creator":{"data":{"id":"M2","type":"members"}},"closer":{"data":{"id":"M3","type":"members"}}}}]}`)
	for _, tc := range []struct {
		tool, role string
		completed  bool
	}{
		{"list_member_created_uncompleted_todos", "M2", false},
		{"list_member_assigned_uncompleted_todos", "M1", false},
		{"list_member_assigned_completed_todos", "M3", true},
	} {
		text := resultProfiles[tc.tool].text(compactTowerBody(raw, tc.tool))
		if !strings.Contains(text, tc.role) || !strings.Contains(text, "2026-11-20") || strings.Contains(text, "周一") != tc.completed {
			t.Errorf("%s: %s", tc.tool, text)
		}
	}
}

func TestLinkedDetailResults(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       []string
	}{
		{"get_topic", `{"data":{"id":"discussion","type":"topics","attributes":{"title":"发布计划","content":"周五上线"},"relationships":{"comments":{"data":[{"type":"comments","id":"reply"}]}}},"included":[{"type":"comments","id":"reply","attributes":{"content":"已确认"}}]}`, []string{"id：discussion", "title：发布计划", "content：周五上线", "comments：\n  - ID reply | 已确认"}},
		{"get_upload", `{"data":{"id":"file","type":"uploads","attributes":{"name":"会议记录"},"relationships":{"attachment":{"data":{"type":"attachments","id":"attachment-id"}}}},"included":[{"type":"attachments","id":"attachment-id","attributes":{"filename":"notes.md","byte_size":20,"icon_url":"discarded-thumbnail"}}]}`, []string{"attachment：", "id：attachment-id", "filename：notes.md", "byte_size：20"}},
		{"get_attachment_url", `{"url":"https://example.org/file?signature=a%2Bb&expires=123"}`, []string{"url：https://example.org/file?signature=a%2Bb&expires=123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := resultProfiles[tc.name].text(compactTowerBody([]byte(tc.body), tc.name))
			for _, want := range tc.want {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			if strings.Contains(text, "discarded-thumbnail") {
				t.Fatal(text)
			}
		})
	}
}
