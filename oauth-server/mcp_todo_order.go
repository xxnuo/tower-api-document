package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

func listTodosByCreation(ctx context.Context, cfg Config, key string, acc *StoredAccount, path string, completed bool, page, size int) (string, bool) {
	var entries []map[string]any
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
			seen[id] = true
			entries = append(entries, row)
		}
	}
	if err := sortTodosByCreation(entries); err != nil {
		return err.Error(), true
	}
	total := len(entries)
	var items []any
	more := false
	if total > 0 && page-1 <= (total-1)/size {
		start := (page - 1) * size
		end := start + min(size, total-start)
		for _, e := range entries[start:end] {
			items = append(items, e)
		}
		more = end < total
	}
	meta := map[string]any{"page": page, "page_size": size, "returned": len(items), "total": total, "has_more": more}
	if more {
		meta["next_page"] = page + 1
	}
	return resultProfiles["list_todos"].text(map[string]any{"items": items, "meta": meta}), false
}

func sortTodosByCreation(rows []map[string]any) error {
	created := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		id := row["id"].(string)
		stamp, err := time.Parse(time.RFC3339Nano, stringifyArg(row["created_at"]))
		if err != nil {
			return fmt.Errorf("任务 %s 缺少有效的 created_at，无法保证按创建时间排序", id)
		}
		created[id] = stamp
	}
	slices.SortFunc(rows, func(a, b map[string]any) int {
		left, right := a["id"].(string), b["id"].(string)
		if c := created[right].Compare(created[left]); c != 0 {
			return c
		}
		return strings.Compare(left, right)
	})
	return nil
}
