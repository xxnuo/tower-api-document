package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

var activityTools = []mcpToolSpec{
	{Name: "edit_my_todo_comment", Title: "编辑自己的任务评论", Desc: "更新当前账号在该任务下发表的评论，回读保存的正文。需要网页登录。", Method: "PATCH", PathTemplate: "/api/v1/todos/{todo_id}/edit_my_comment",
		Params: []mcpParam{pPath("todo_id", "任务 GUID"), pBody("comment_id", "string", true, "评论 GUID"), pBody("content", "string", true, "完整新正文，支持纯文本和 HTML")}},
	{Name: "mark_notification_read", Title: "将一条通知标记为已读", Desc: "将 list_notifications 返回的指定通知标记为已读。需要网页登录。", Method: "POST", PathTemplate: "/api/v1/teams/{team_id}/mark_notification_read",
		Params: []mcpParam{pPath("team_id", "团队 GUID"), pBody("notification_id", "string", true, "通知记录的 id（与 target_guid 不同）")}},
}

type activityRecord struct {
	ID         string
	Attributes struct {
		Content *string
		Read    *bool
	}
	Relationships struct {
		Creator struct{ Data struct{ ID string } }
	}
}

// Follow Tower's page numbers, never an upstream-supplied URL with our bearer token.
func findActivity(ctx context.Context, cfg Config, acc *StoredAccount, path, id string, page int) (activityRecord, int, error) {
	for {
		q := url.Values{"page[number]": {strconv.Itoa(page)}, "page[size]": {"50"}}
		status, raw, err := doTowerHTTPWithRefresh(ctx, cfg, acc.MCPAPIKey, acc, http.MethodGet, path, q.Encode(), nil)
		if err != nil {
			return activityRecord{}, 0, err
		}
		if status != http.StatusOK {
			return activityRecord{}, 0, fmt.Errorf("读取 Tower 记录失败 HTTP %d", status)
		}
		var doc struct {
			Data  []activityRecord
			Links struct{ Next any }
		}
		if json.Unmarshal(raw, &doc) != nil || doc.Data == nil {
			return activityRecord{}, 0, errors.New("无法解析 Tower 记录列表")
		}
		for _, record := range doc.Data {
			if record.ID == id {
				return record, page, nil
			}
		}
		next := mcpLinkedPage(doc.Links.Next)
		if next <= page {
			return activityRecord{}, 0, errors.New("当前账号在指定列表中找不到该记录")
		}
		page = next
	}
}

func (client *webClient) editMyComment(ctx context.Context, cfg Config, acc *StoredAccount, pagePath, todoID, commentID string, content any) (map[string]any, error) {
	body, ok := content.(string)
	if !ok || strings.TrimSpace(body) == "" {
		return nil, fmt.Errorf("%w：content 须为非空正文", errWebArguments)
	}
	page, err := client.get(ctx, pagePath)
	if err != nil {
		return nil, err
	}
	member := hiddenValues(page)["member-guid"]
	if !guidPattern.MatchString(member) || client.csrf == "" {
		return nil, errors.New("任务页未能确认当前成员及登录状态")
	}
	path := "/api/v1/todos/" + todoID + "/comments"
	record, number, err := findActivity(ctx, cfg, acc, path, commentID, 1)
	if err != nil {
		return nil, err
	}
	if record.Relationships.Creator.Data.ID != member {
		return nil, errors.New("只能编辑当前成员自己的评论")
	}
	result := map[string]any{"todo_id": todoID, "comment_id": commentID, "content": body}
	if record.Attributes.Content != nil && *record.Attributes.Content == body {
		result["already"] = true
		return result, nil
	}
	form := url.Values{"comment[content]": {body}}
	status, _, err := client.request(ctx, http.MethodPut, client.resolve("/comments/"+commentID+".js?is_html=1"), form.Encode(), "application/x-www-form-urlencoded; charset=UTF-8")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("编辑评论失败 HTTP %d", status)
	}
	record, _, err = findActivity(ctx, cfg, acc, path, commentID, number)
	if err != nil || record.Attributes.Content == nil || *record.Attributes.Content != body {
		return nil, errors.New("已提交评论修改，回读未能确认相同正文，请重新查询该评论")
	}
	return result, nil
}

func (client *webClient) markNotificationRead(ctx context.Context, cfg Config, acc *StoredAccount, teamID, id string) (map[string]any, error) {
	path := "/teams/" + teamID + "/notifications"
	record, page, err := findActivity(ctx, cfg, acc, "/api/v1"+path, id, 1)
	if err != nil {
		return nil, err
	}
	if record.Attributes.Read == nil {
		return nil, errors.New("通知缺少已读状态")
	}
	result := map[string]any{"notification_id": id, "read": true}
	if *record.Attributes.Read {
		result["already"] = true
		return result, nil
	}
	if _, err = client.get(ctx, path); err != nil {
		return nil, err
	}
	if client.csrf == "" {
		return nil, errors.New("通知页未提供 CSRF 令牌")
	}
	status, raw, err := client.request(ctx, http.MethodPost, client.resolve(path+"/"+id+"/read"), "", "application/x-www-form-urlencoded; charset=UTF-8")
	if err != nil {
		return nil, err
	}
	var reply struct{ Success bool }
	if status != http.StatusOK || json.Unmarshal([]byte(raw), &reply) != nil || !reply.Success {
		return nil, fmt.Errorf("Tower 未确认通知标记已读 HTTP %d", status)
	}
	record, _, err = findActivity(ctx, cfg, acc, "/api/v1"+path, id, page)
	if err != nil || record.Attributes.Read == nil || !*record.Attributes.Read {
		return nil, errors.New("已提交通知修改，回读未能确认已读状态")
	}
	return result, nil
}
