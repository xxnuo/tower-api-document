package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

var gatewayTaskTools = slices.Concat(projectTaskTools, activityTools, gatewayLibraryTools, gatewayScheduleTools, gatewayRelationTools)

var projectTaskTools = []mcpToolSpec{
	{
		Name: "get_project_task_fields", Title: "查询项目标签和自定义字段",
		Desc:   "列出项目可用的标签 ID、自定义字段键与选项值，供 create_todo 和 update_todo 使用。需要网页登录。",
		Method: "GET", PathTemplate: "/api/v1/projects/{project_id}/task_fields",
		Params: []mcpParam{
			pPath("project_id", "项目 GUID"),
		},
	},
	{
		Name: "list_todo_history", Title: "读取任务变更历史",
		Desc:   "按时间从早到晚分页读取 Tower 任务动态和评论。将 next_offset 作为下一次的 offset。需要网页登录。",
		Method: "GET", PathTemplate: "/api/v1/todos/{todo_id}/history",
		Params: []mcpParam{
			pPath("todo_id", "任务 GUID"),
			pQuery("offset", "", "integer", "起始位置，默认 0"),
			pQuery("page_size", "", "integer", "每页 1 至 50 条，默认 20"),
		},
	},
	{
		Name: "move_todo_in_project", Title: "移动任务到同项目清单",
		Desc:   "把任务在指定项目中的归属移到目标清单。保留任务 ID、父任务和其他项目关联。需要网页登录。",
		Method: "PATCH", PathTemplate: "/api/v1/todos/{todo_id}/move_in_project",
		Params: []mcpParam{
			pPath("todo_id", "任务 GUID"),
			pBody("project_id", "string", true, "要调整的所属项目 GUID"),
			pBody("todolist_id", "string", true, "同项目内的目标清单 GUID"),
		},
	},
	{
		Name: "filter_project_todos", Title: "按项目字段筛选任务",
		Desc:   "在整个项目内筛选，分页只统计匹配的任务。需要最新匹配时传 order=-created_at。按返回的 next_cursor 继续。custom.<字段 key> 使用字段的展示值。无需网页登录。",
		Method: "POST", PathTemplate: "/api/v1/projects/{project_id}/filter_todos",
		Params: []mcpParam{
			pPath("project_id", "项目 GUID"),
			pBody("conditions", "conditions", true, "1–20 项，每项传 field、operator、value。成员字段 assignee_id（负责人）、creator_id（创建人）、closer_id（完成人）支持 eq（单个 member_id）、in、not_in（单个或数组）。成员 ID 可从 list_team_members 获取。assignee_id、closer_id 还支持 be_nil、not_be_nil。title 用 in；priority 用 in、not_in、be_nil、not_be_nil；todolist_id 用 in、not_in；label 和 custom.<字段 key> 用 in、not_in、all_in、eq、be_nil、not_be_nil。start_at、due_at、closed_at、created_at 用北京时间 YYYY-MM-DD，支持 eq、lt、gt、be_nil，created_at 还支持 not_be_nil。lt 和 gt 不含当天，判空操作省略 value。"),
			pBody("match", "string", false, "all（全部满足）或 any（任一满足）"),
			pBody("status", "string", false, "all、completed 或 uncompleted"),
			pBody("order", "string", false, "可选 -created_at，按整个项目的创建时间倒序。默认按清单顺序。"),
			pBody("page_size", "integer", false, "每页最多 50 条"),
			pBody("cursor", "string", false, "上一页 next_cursor，保留相同查询条件"),
		},
	},
}

func gatewayTaskSpec(name string) (mcpToolSpec, bool) {
	for _, spec := range gatewayTaskTools {
		if spec.Name == name {
			return spec, true
		}
	}
	return mcpToolSpec{}, false
}

var errWebArguments = errors.New("参数错误")

func runGatewayTask(ctx context.Context, cfg Config, acc *StoredAccount, spec mcpToolSpec, args map[string]any) (map[string]any, error) {
	for key := range args {
		if !slices.ContainsFunc(spec.Params, func(p mcpParam) bool { return p.Name == key }) {
			return nil, fmt.Errorf("%w：未知参数 %s", errWebArguments, key)
		}
	}
	for _, p := range spec.Params {
		value, present := args[p.Name]
		if p.Required && (!present || value == nil) {
			return nil, fmt.Errorf("%w：缺少 %s", errWebArguments, p.Name)
		}
		if present && strings.HasSuffix(p.Name, "_id") && !guidPattern.MatchString(stringifyArg(value)) {
			return nil, fmt.Errorf("%w：%s 须为 Tower GUID", errWebArguments, p.Name)
		}
	}
	if spec.Name == "filter_project_todos" {
		return filterProjectTodos(ctx, cfg, acc, args)
	}
	client, err := useWebClient(ctx, cfg, acc)
	if err != nil {
		return nil, err
	}
	defer client.save()
	switch spec.Name {
	case "list_repositories", "list_repository_items", "read_repository_item":
		return client.library(ctx, spec.Name, stringifyArg(args["team_id"]), stringifyArg(args["url"]))
	case "mark_notification_read":
		return client.markNotificationRead(ctx, cfg, acc, stringifyArg(args["team_id"]), stringifyArg(args["notification_id"]))
	}
	projectID := stringifyArg(args["project_id"])
	if spec.Name == "get_project_task_fields" {
		return client.projectFields(ctx, projectID)
	}
	todoID := stringifyArg(args["todo_id"])
	pagePath, err := todoWebPath(ctx, cfg, acc, todoID)
	if err != nil {
		return nil, err
	}
	switch spec.Name {
	case "list_todo_dependencies", "add_todo_dependency", "remove_todo_dependency", "copy_todo":
		return client.runTodoRelation(ctx, cfg, acc, spec.Name, pagePath, todoID, args)
	case "get_todo_schedule", "set_todo_estimated_time", "set_todo_milestone", "set_todo_recurrence":
		return client.todoSchedule(ctx, pagePath, todoID, spec.Name, args)
	case "edit_my_todo_comment":
		return client.editMyComment(ctx, cfg, acc, pagePath, todoID, stringifyArg(args["comment_id"]), args["content"])
	case "move_todo_in_project":
		return client.moveTodo(ctx, pagePath, todoID, projectID, stringifyArg(args["todolist_id"]))
	case "list_todo_history":
		size, offset := 20, 0
		for key, dst := range map[string]*int{"page_size": &size, "offset": &offset} {
			if v, ok := args[key]; ok {
				*dst, err = strconv.Atoi(stringifyArg(v))
				if err != nil {
					return nil, fmt.Errorf("%w：%s 须为整数", errWebArguments, key)
				}
			}
		}
		if size < 1 || size > 50 || offset < 0 {
			return nil, fmt.Errorf("%w：page_size 范围为 1 到 50，offset 须为非负数", errWebArguments)
		}
		return client.todoHistory(ctx, pagePath, offset, size)
	}
	return nil, errors.New("未知网页工具")
}

// Match the same route templates advertised by the MCP tools and API explorer.
func serveGatewayTaskProxy(w http.ResponseWriter, r *http.Request, cfg Config, method, path, query, body, key string) bool {
	parts := strings.Split(path, "/")
	if len(parts) != 6 {
		return false
	}
	for _, spec := range gatewayTaskTools {
		pattern := strings.Split(spec.PathTemplate, "/")
		if parts[3] != pattern[3] || parts[5] != pattern[5] {
			continue
		}
		start := time.Now()
		status := http.StatusOK
		var result map[string]any
		var err error
		args := map[string]any{}
		if method != spec.Method {
			status = http.StatusMethodNotAllowed
			err = errors.New("网关接口不支持该方法")
		} else if method == "GET" {
			var vals url.Values
			vals, err = url.ParseQuery(query)
			for k, v := range vals {
				args[k] = v[0]
			}
		} else if body != "" {
			err = json.Unmarshal([]byte(body), &args)
		}
		if err == nil && args != nil {
			args[strings.Trim(pattern[4], "{}")] = parts[4]
			var acc *StoredAccount
			if cfg.Store != nil {
				acc, err = cfg.Store.GetByAuth(key)
			}
			user := userFromContext(r.Context())
			if acc == nil || err != nil || (user != nil && acc.UserID != user.ID) {
				status = http.StatusNotFound
				err = errors.New("找不到对应 Tower 账号")
			} else {
				result, err = runGatewayTask(r.Context(), cfg, acc, spec, args)
				switch {
				case errors.Is(err, errWebArguments):
					status = http.StatusBadRequest
				case errors.Is(err, errNoWebSession):
					status = http.StatusConflict
				case err != nil:
					status = http.StatusBadGateway
				}
			}
		} else if status == http.StatusOK {
			status = http.StatusBadRequest
			err = errors.New("网关接口参数格式无效")
		}
		if err != nil {
			result = map[string]any{"error": err.Error()}
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": status, "status_text": fmt.Sprintf("%d %s", status, http.StatusText(status)), "duration_ms": time.Since(start).Milliseconds(), "body": result})
		return true
	}
	return false
}
