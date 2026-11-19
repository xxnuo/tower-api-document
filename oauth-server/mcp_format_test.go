package main

import (
	"encoding/json"
	"testing"
)

func TestCompactTodoList(t *testing.T) {
	raw := []byte(`{"data":[{"id":"t","type":"todos","attributes":{"content":"task","desc":"long body","is_completed":false},"relationships":{"assignee":{"data":{"type":"members","id":"m"}}}}],"included":[{"id":"m","type":"members","attributes":{"nickname":"Alice","gavatar":"image"}}],"links":{"next":"page2"}}`)
	out, err := json.Marshal(compactTowerBody(raw, "tower_list_todos"))
	if err != nil || string(out) != `{"items":[{"assignee":{"id":"m","nickname":"Alice"},"content":"task","id":"t","is_completed":false}],"next":"page2"}` {
		t.Fatal(string(out), err)
	}
}
