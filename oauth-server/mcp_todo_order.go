package main

import (
	"context"
	"slices"
	"strings"
	"time"
)

func listTodosByCreation(ctx context.Context, cfg Config, key string, acc *StoredAccount, path string, completed bool, page, size int) (string, bool) {
	type entry struct {
		id      string
		created time.Time
		item    map[string]any
	}
	var entries []entry
	seen := map[string]bool{}
	for state := (upstreamProjectPage{number: 1}); state.number > 0; {
		rows, err := state.fetch(ctx, cfg, key, acc, path, true, completed)
		if err != nil {
			return err.Error(), true
		}
		for _, row := range rows {
			id := row["id"].(string)
			if seen[id] {
				continue
			}
			created, err := time.Parse(time.RFC3339Nano, stringifyArg(row["created_at"]))
			if err != nil {
				return "任务 " + id + " 缺少有效的 created_at，无法保证整份清单的排序", true
			}
			seen[id] = true
			entries = append(entries, entry{id, created, row})
		}
	}
	slices.SortFunc(entries, func(a, b entry) int {
		if c := b.created.Compare(a.created); c != 0 {
			return c
		}
		return strings.Compare(a.id, b.id)
	})
	total := len(entries)
	var items []any
	more := false
	if total > 0 && page-1 <= (total-1)/size {
		start := (page - 1) * size
		end := start + min(size, total-start)
		for _, e := range entries[start:end] {
			items = append(items, e.item)
		}
		more = end < total
	}
	meta := map[string]any{"page": page, "page_size": size, "returned": len(items), "total": total, "has_more": more}
	if more {
		meta["next_page"] = page + 1
	}
	return resultProfiles["list_todos"].text(map[string]any{"items": items, "meta": meta}), false
}
