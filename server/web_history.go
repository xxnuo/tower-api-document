package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type historyEntry struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Time   string `json:"time,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Action string `json:"action,omitempty"`
	Text   string `json:"text,omitempty"`
}

func webHasClass(n *html.Node, class string) bool {
	for _, word := range strings.Fields(attr(n, "class")) {
		if word == class {
			return true
		}
	}
	return false
}

func webNodeText(n *html.Node) string {
	var b strings.Builder
	for child := range n.Descendants() {
		if child.Type == html.TextNode {
			b.WriteString(child.Data)
		} else if child.Data == "br" || child.Data == "p" {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func readHistoryEntry(n *html.Node) (historyEntry, bool) {
	item := historyEntry{ID: attr(n, "id")}
	switch {
	case webHasClass(n, "event"):
		item.Kind = "event"
	case webHasClass(n, "comment"):
		item.Kind = "comment"
	default:
		return item, false
	}
	for child := range n.Descendants() {
		switch {
		case webHasClass(child, "event-actor"), webHasClass(child, "author"):
			item.Actor = webNodeText(child)
		case webHasClass(child, "event-action"):
			item.Action = webNodeText(child)
		case webHasClass(child, "event-text"), webHasClass(child, "comment-content"):
			item.Text = webNodeText(child)
		case webHasClass(child, "event-created-at"):
			item.Time = attr(child, "data-created-at")
		case item.Kind == "comment" && attr(child, "data-readable-time") != "":
			if item.Time == "" {
				item.Time = attr(child, "data-readable-time")
			}
		}
	}
	return item, item.ID != ""
}

// Tower returns the requested range as an escaped HTML fragment inside JavaScript.
var historyFragment = regexp.MustCompile(`var\s+list\s*=\s*'((?:\\.|[^'\\])*)';`)
var webJSUnescape = strings.NewReplacer(`\\`, `\`, `\'`, "'", `\"`, `"`, `\/`, "/", `\n`, "\n", `\r`, "\r", `\t`, "\t", `\u2028`, "\u2028", `\u2029`, "\u2029", `\$`, "$", "\\`", "`")

func (client *webClient) historyRange(ctx context.Context, path string, start, end int) ([]historyEntry, error) {
	u, err := url.Parse(path)
	if err != nil || u.Path != "/comment_streams" || u.IsAbs() || u.Host != "" || u.Query().Get("target_gid") == "" {
		return nil, errors.New("无效的 Tower 历史区间链接")
	}
	q := u.Query()
	q.Set("start", strconv.Itoa(start))
	q.Set("end", strconv.Itoa(end))
	status, body, err := client.request(ctx, "GET", client.resolve("/comment_streams?"+q.Encode()), "", "application/x-www-form-urlencoded")
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("读取任务历史区间失败 HTTP %d", status)
	}
	match := historyFragment.FindStringSubmatch(body)
	if match == nil {
		return nil, errors.New("无法读取 Tower 历史区间")
	}
	doc, err := html.Parse(strings.NewReader(webJSUnescape.Replace(match[1])))
	if err != nil {
		return nil, err
	}
	items := make([]historyEntry, 0, end-start)
	for n := range doc.Descendants() {
		if item, ok := readHistoryEntry(n); ok {
			items = append(items, item)
		}
	}
	if len(items) != end-start {
		return nil, fmt.Errorf("Tower 历史区间应有 %d 项，实际解析到 %d 项，请重新查询", end-start, len(items))
	}
	return items, nil
}

func (client *webClient) todoHistory(ctx context.Context, pagePath string, offset, size int) (map[string]any, error) {
	if offset < 0 || size < 1 || size > 50 {
		return nil, errors.New("offset 须为非负整数，page_size 须在 1 到 50 之间")
	}
	page, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return nil, err
	}
	var stream *html.Node
	for n := range doc.Descendants() {
		if n.Data == "tr-comments" {
			stream = n
			break
		}
	}
	if stream == nil {
		return nil, errors.New("未找到任务历史，可能没有查看权限")
	}
	items := make([]historyEntry, 0, size)
	index := 0
	for n := stream.FirstChild; n != nil; n = n.NextSibling {
		if n.Type != html.ElementNode {
			continue
		}
		if bounds := attr(n, "data-comment-streams-range"); bounds != "" {
			a, b, _ := strings.Cut(bounds, ",")
			start, e1 := strconv.Atoi(a)
			end, e2 := strconv.Atoi(b)
			if e1 != nil || e2 != nil || start != index || end < start {
				return nil, errors.New("Tower 历史区间已变化，请重新查询")
			}
			lo := max(start, offset)
			if lo < end && len(items) < size {
				hi := lo + min(end-lo, size-len(items))
				rows, err := client.historyRange(ctx, attr(n, "data-url"), lo, hi)
				if err != nil {
					return nil, err
				}
				items = append(items, rows...)
			}
			index = end
		} else if row, ok := readHistoryEntry(n); ok {
			if index >= offset && len(items) < size {
				items = append(items, row)
			}
			index++
		}
	}
	more := offset < index && len(items) < index-offset
	result := map[string]any{"items": items, "offset": offset, "total": index, "has_more": more}
	if more {
		result["next_offset"] = offset + len(items)
	}
	return result, nil
}
