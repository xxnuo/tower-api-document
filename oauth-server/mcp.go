package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	defaultListPageSize = 20
	maxListPageSize     = 50
)

type mcpParam struct {
	Name     string
	WireName string // optional override for query/body key (e.g. page -> page[number])
	Type     string // "string", "integer", "number", "boolean", "string_array", "int_array", "object"
	In       string // "path", "query", "body", "merge_body"
	Required bool
	Desc     string
}

type mcpToolSpec struct {
	Name         string
	Title        string
	Desc         string
	Method       string
	PathTemplate string
	BodyEnvelope string
	Params       []mcpParam
}

func pPath(name, desc string) mcpParam {
	return mcpParam{Name: name, Type: "string", In: "path", Required: true, Desc: desc}
}

func pQuery(name, wireName, typ, desc string) mcpParam {
	return mcpParam{Name: name, WireName: wireName, Type: typ, In: "query", Desc: desc}
}

func pPage() mcpParam {
	return pQuery("page", "page[number]", "integer", "分页页码，默认 1")
}

func pPageSize() mcpParam {
	return pQuery("page_size", "page[size]", "integer", "每页返回条数，默认 20，最大 50")
}

func pLocalPage() mcpParam {
	return mcpParam{Name: "page", Type: "integer", In: "page_only", Desc: "分页页码，默认 1"}
}

func pLocalPageSize() mcpParam {
	return mcpParam{Name: "page_size", Type: "integer", In: "limit_only", Desc: "每页返回条数，默认 20，最大 50"}
}

func pBody(name, typ string, required bool, desc string) mcpParam {
	return mcpParam{Name: name, Type: typ, In: "body", Required: required, Desc: desc}
}

var towerMCPTools = []mcpToolSpec{
	// 用户
	{
		Name: "get_current_user", Title: "获取当前账号信息",
		Desc:   "获取当前鉴权成员的 Tower 用户信息（ID、昵称、邮箱、头像）",
		Method: http.MethodGet, PathTemplate: "/api/v1/user",
	},

	// 团队
	{
		Name: "list_teams", Title: "获取团队列表",
		Desc:   "获取当前成员加入的所有 Tower 团队列表（返回各团队 team_id 与名称）",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams",
		Params: []mcpParam{pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "get_team", Title: "获取团队详情",
		Desc:   "根据 team_id 获取指定 Tower 团队详情",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}",
		Params: []mcpParam{pPath("team_id", "团队 ID")},
	},
	{
		Name: "create_team", Title: "创建团队",
		Desc:   "创建一个新的 Tower 团队",
		Method: http.MethodPost, PathTemplate: "/api/v1/teams", BodyEnvelope: "team",
		Params: []mcpParam{pBody("name", "string", true, "新团队名称")},
	},
	{
		Name: "rename_team", Title: "更改团队名称",
		Desc:   "修改指定 Tower 团队的名称",
		Method: http.MethodPatch, PathTemplate: "/api/v1/teams/{team_id}", BodyEnvelope: "team",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pBody("name", "string", true, "新的团队名称"),
		},
	},
	{
		Name: "get_team_resource", Title: "按团队资源编号查询资源",
		Desc:   "根据团队 ID 与资源短编号查询对应 Tower 资源信息",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/resources/{resource_id}",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pPath("resource_id", "团队内资源编号"),
		},
	},

	// 项目
	{
		Name: "list_projects", Title: "获取团队项目列表",
		Desc:   "获取指定团队下的全部项目列表（返回各项目 project_id 与名称）",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/projects",
		Params: []mcpParam{pPath("team_id", "团队 ID"), pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "get_project", Title: "获取项目详情",
		Desc:   "获取指定 Tower 项目的详细信息（包含关联清单等）",
		Method: http.MethodGet, PathTemplate: "/api/v1/projects/{project_id}",
		Params: []mcpParam{pPath("project_id", "项目 ID")},
	},
	{
		Name: "create_project", Title: "创建项目",
		Desc:   "普通成员也可在团队中创建项目",
		Method: http.MethodPost, PathTemplate: "/api/v1/teams/{team_id}/projects", BodyEnvelope: "project",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pBody("name", "string", true, "项目名称"),
			pBody("desc", "string", false, "项目描述"),
			pBody("member_ids", "string_array", false, "初始成员 ID 列表"),
			pBody("color_id", "integer", false, "项目图标颜色编号（默认 0）"),
			pBody("icon_id", "integer", false, "项目图标编号（默认 0）"),
		},
	},
	{
		Name: "update_project", Title: "更新项目信息",
		Desc:   "普通成员可更新自己创建或有管理权限的项目；仅参与他人创建的项目时，修改设置需要项目管理权限",
		Method: http.MethodPatch, PathTemplate: "/api/v1/projects/{project_id}", BodyEnvelope: "project",
		Params: []mcpParam{
			pPath("project_id", "项目 ID"),
			pBody("name", "string", false, "项目名称"),
			pBody("desc", "string", false, "项目描述"),
			pBody("member_ids", "string_array", false, "项目成员 ID 列表"),
		},
	},
	{
		Name: "delete_project", Title: "删除项目",
		Desc:   "普通成员可删除自己创建或有管理权限的项目；仅参与项目不足以删除。受 MCP 硬删除保护",
		Method: http.MethodDelete, PathTemplate: "/api/v1/projects/{project_id}",
		Params: []mcpParam{pPath("project_id", "项目 ID")},
	},
	{
		Name: "list_project_members", Title: "获取项目成员",
		Desc:   "获取指定项目的成员列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/projects/{project_id}/members",
		Params: []mcpParam{pPath("project_id", "项目 ID"), pPage(), pPageSize()},
	},

	// 成员
	{
		Name: "list_team_members", Title: "获取团队成员列表",
		Desc:   "获取指定团队下的全部成员（返回各成员 member_id、昵称、角色等）",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/members",
		Params: []mcpParam{pPath("team_id", "团队 ID"), pPage(), pPageSize()},
	},
	{
		Name: "get_my_team_member", Title: "获取当前账号在团队中的成员信息",
		Desc:   "获取当前登录用户在指定团队中的成员档案（含对应的 member_id）",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/member",
		Params: []mcpParam{pPath("team_id", "团队 ID")},
	},
	{
		Name: "get_member", Title: "获取成员详情",
		Desc:   "根据 member_id 获取成员详细信息",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}",
		Params: []mcpParam{pPath("member_id", "成员 ID")},
	},
	{
		Name: "list_member_assigned_todos", Title: "获取指派给成员的全部任务",
		Desc:   "获取指派给指定成员的全部任务（含已完成和未完成）",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/assigned_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "list_member_assigned_uncompleted_todos", Title: "获取指派给成员的未完成任务",
		Desc:   "获取指派给指定成员的未完成任务。设置 overdue=true 或 due_before 时先筛选。省略 page 和 page_size 则返回全部已确认匹配的任务。指定任一分页参数则分页，筛选时默认每页 50 条。scope_complete 表示是否能确认筛选范围完整。",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/assigned_uncompleted_todos",
		Params: []mcpParam{
			pPath("member_id", "成员 ID"), pLocalPage(),
			{Name: "page_size", Type: "integer", In: "limit_only", Desc: "每页返回条数，最大 50。普通查询默认 20，按截止日期筛选并指定 page 时默认 50。"},
			{Name: "overdue", Type: "boolean", Desc: "仅返回截止日期早于北京时间今天的任务"},
			{Name: "due_before", Type: "string", Desc: "仅返回截止日期早于 YYYY-MM-DD 的任务，不含该日。与 overdue 同用时两者均需满足。"},
		},
	},
	{
		Name: "list_member_assigned_completed_todos", Title: "获取指派给成员的已完成任务",
		Desc:   "获取指派给指定成员且已完成的任务列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/assigned_completed_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pPage(), pPageSize()},
	},
	{
		Name: "list_member_created_todos", Title: "获取成员创建的全部任务",
		Desc:   "获取由指定成员创建的全部任务",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/created_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "list_member_created_uncompleted_todos", Title: "获取成员创建的未完成任务",
		Desc:   "获取由指定成员创建且未完成的任务",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/created_uncompleted_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "list_member_created_completed_todos", Title: "获取成员创建的已完成任务",
		Desc:   "获取由指定成员创建且已完成的任务",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/created_completed_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pPage(), pPageSize()},
	},

	// 任务清单
	{
		Name: "list_todolists", Title: "获取项目任务清单列表",
		Desc:   "获取指定项目下的所有任务清单（返回 todolist_id 与清单名称）",
		Method: http.MethodGet, PathTemplate: "/api/v1/projects/{project_id}/todolists",
		Params: []mcpParam{pPath("project_id", "项目 ID"), pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "get_todolist", Title: "获取任务清单详情",
		Desc:   "获取指定任务清单的详细信息及其下属任务概览",
		Method: http.MethodGet, PathTemplate: "/api/v1/todolists/{todolist_id}",
		Params: []mcpParam{pPath("todolist_id", "任务清单 ID")},
	},
	{
		Name: "create_todolist", Title: "创建任务清单",
		Desc:   "在指定项目下新建任务清单",
		Method: http.MethodPost, PathTemplate: "/api/v1/projects/{project_id}/todolists", BodyEnvelope: "todolist",
		Params: []mcpParam{
			pPath("project_id", "项目 ID"),
			pBody("name", "string", true, "任务清单名称"),
			pBody("desc", "string", false, "任务清单描述"),
		},
	},
	{
		Name: "update_todolist", Title: "更新任务清单",
		Desc:   "更新指定任务清单的名称或描述",
		Method: http.MethodPatch, PathTemplate: "/api/v1/todolists/{todolist_id}", BodyEnvelope: "todolist",
		Params: []mcpParam{
			pPath("todolist_id", "任务清单 ID"),
			pBody("name", "string", false, "任务清单名称"),
			pBody("desc", "string", false, "任务清单描述"),
		},
	},
	{
		Name: "delete_todolist", Title: "删除任务清单",
		Desc:   "删除指定任务清单（危险操作）",
		Method: http.MethodDelete, PathTemplate: "/api/v1/todolists/{todolist_id}",
		Params: []mcpParam{pPath("todolist_id", "任务清单 ID")},
	},

	// 任务
	{
		Name: "list_todos", Title: "获取清单下的任务列表",
		Desc:   "获取指定任务清单中的任务列表，可通过 completed_todo 控制是否包含已完成任务",
		Method: http.MethodGet, PathTemplate: "/api/v1/todolists/{todolist_id}/todos",
		Params: []mcpParam{
			pPath("todolist_id", "任务清单 ID"),
			pPage(),
			pPageSize(),
			{Name: "order", Type: "string", Desc: "传 -created_at 时，按整份清单的创建时间倒序排列后分页。"},
			pQuery("completed_todo", "completed_todo", "boolean", "是否包含已完成任务（默认 false）"),
		},
	},
	{
		Name: "create_todo", Title: "创建任务",
		Desc:   "在指定任务清单下创建新任务，支持指派负责人、起止日期、父任务与自定义字段",
		Method: http.MethodPost, PathTemplate: "/api/v1/todolists/{todolist_id}/todos", BodyEnvelope: "todo",
		Params: []mcpParam{
			pPath("todolist_id", "任务清单 ID"),
			pBody("content", "string", true, "任务标题内容"),
			pBody("desc", "string", false, "任务详细描述"),
			pBody("assignee_id", "string", false, "负责人 member_id"),
			pBody("start_at", "string", false, "开始日期（YYYY-MM-DD）"),
			pBody("due_at", "string", false, "截止日期（YYYY-MM-DD）"),
			pBody("parent_id", "string", false, "父任务 todo_id（创建子任务时传入）"),
			pBody("label_ids", "int_array", false, "标签 ID 数组"),
			pBody("attfile_guids", "string_array", false, "附件 GUID 数组"),
			{Name: "custom_fields", Type: "object", In: "merge_body", Desc: "Tower 自定义字段键值对（会直接合并进 todo 对象）"},
		},
	},
	{
		Name: "get_todo", Title: "获取任务详情",
		Desc:   "根据 todo_id 获取任务完整信息（含检查项、评论、负责人等关联数据）",
		Method: http.MethodGet, PathTemplate: "/api/v1/todos/{todo_id}",
		Params: []mcpParam{pPath("todo_id", "任务 ID")},
	},
	{
		Name: "get_todo_by_number", Title: "按团队内任务编号获取任务",
		Desc:   "根据 team_id 和团队内任务短编号（team_wide_id，如 #123 中的 123）获取任务详情",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/todos/{team_wide_id}",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pPath("team_wide_id", "团队内任务短编号"),
		},
	},
	{
		Name: "update_todo", Title: "更新任务信息",
		Desc:   "更新指定任务的标题、描述、负责人、起止日期、标签或自定义字段",
		Method: http.MethodPatch, PathTemplate: "/api/v1/todos/{todo_id}", BodyEnvelope: "todo",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("content", "string", false, "任务标题内容"),
			pBody("desc", "string", false, "任务详细描述"),
			pBody("assignee_id", "string", false, "负责人 member_id"),
			pBody("start_at", "string", false, "开始日期（YYYY-MM-DD）"),
			pBody("due_at", "string", false, "截止日期（YYYY-MM-DD）"),
			pBody("parent_id", "string", false, "父任务 todo_id"),
			pBody("label_ids", "int_array", false, "标签 ID 数组"),
			pBody("attfile_guids", "string_array", false, "附件 GUID 数组"),
			{Name: "custom_fields", Type: "object", In: "merge_body", Desc: "Tower 自定义字段键值对（直接合并进 todo 对象）"},
		},
	},
	{
		Name: "delete_todo", Title: "删除任务",
		Desc:   "删除指定任务（危险操作）",
		Method: http.MethodDelete, PathTemplate: "/api/v1/todos/{todo_id}",
		Params: []mcpParam{pPath("todo_id", "任务 ID")},
	},
	{
		Name: "complete_todo", Title: "完成任务",
		Desc:   "将指定任务标记为已完成",
		Method: http.MethodPost, PathTemplate: "/api/v1/todos/{todo_id}/completion",
		Params: []mcpParam{pPath("todo_id", "任务 ID")},
	},
	{
		Name: "reopen_todo", Title: "重新打开已完成任务",
		Desc:   "取消任务的完成状态，将其重新打开为未完成",
		Method: http.MethodDelete, PathTemplate: "/api/v1/todos/{todo_id}/completion",
		Params: []mcpParam{pPath("todo_id", "任务 ID")},
	},
	{
		Name: "list_todo_comments", Title: "获取任务评论列表",
		Desc:   "获取指定任务下的评论列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/todos/{todo_id}/comments",
		Params: []mcpParam{pPath("todo_id", "任务 ID"), pPage(), pPageSize()},
	},
	{
		Name: "create_todo_comment", Title: "发布任务评论",
		Desc:   "在指定任务下发表新评论",
		Method: http.MethodPost, PathTemplate: "/api/v1/todos/{todo_id}/comments", BodyEnvelope: "comment",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("content", "string", true, "评论内容（支持富文本/HTML 或纯文本）"),
		},
	},
	{
		Name: "assign_todo", Title: "指派任务负责人",
		Desc:   "将指定任务指派给某个成员",
		Method: http.MethodPatch, PathTemplate: "/api/v1/todos/{todo_id}/assignment", BodyEnvelope: "todos_assignment",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("assignee_id", "string", true, "负责人 member_id"),
		},
	},
	{
		Name: "unassign_todo", Title: "移除任务负责人",
		Desc:   "取消当前任务的负责人指派",
		Method: http.MethodDelete, PathTemplate: "/api/v1/todos/{todo_id}/assignment",
		Params: []mcpParam{pPath("todo_id", "任务 ID")},
	},
	{
		Name: "update_todo_due", Title: "更新任务截止日期",
		Desc:   "单独修改指定任务的截止日期（due_at）",
		Method: http.MethodPatch, PathTemplate: "/api/v1/todos/{todo_id}/due", BodyEnvelope: "todos_due",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("due_at", "string", true, "截止日期，如 2026-12-31"),
		},
	},
	{
		Name: "update_todo_desc", Title: "更新任务描述",
		Desc:   "单独更新指定任务的详细描述内容",
		Method: http.MethodPatch, PathTemplate: "/api/v1/todos/{todo_id}/desc", BodyEnvelope: "todos_desc",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("desc", "string", true, "任务描述内容"),
		},
	},
	{
		Name: "list_todo_cc_members", Title: "获取任务通知成员列表",
		Desc:   "查询指定任务当前关注/抄送通知的成员列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/todos/{todo_id}/cc_members",
		Params: []mcpParam{pPath("todo_id", "任务 ID"), pLocalPage(), pLocalPageSize()},
	},
	{
		Name: "update_todo_cc_members", Title: "更新任务通知成员列表",
		Desc:   "设置指定任务的关注/抄送通知成员列表",
		Method: http.MethodPatch, PathTemplate: "/api/v1/todos/{todo_id}/cc_members",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("guids", "string_array", true, "更新后的完整通知成员 ID 列表；传空数组清空"),
		},
	},

	// 讨论
	{
		Name: "list_topics", Title: "获取项目讨论列表",
		Desc:   "获取指定项目下的讨论主题列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/projects/{project_id}/topics",
		Params: []mcpParam{pPath("project_id", "项目 ID"), pPage(), pPageSize()},
	},
	{
		Name: "get_topic", Title: "获取讨论详情",
		Desc:   "获取指定讨论主题的内容与回复",
		Method: http.MethodGet, PathTemplate: "/api/v1/topics/{topic_id}",
		Params: []mcpParam{pPath("topic_id", "讨论 ID")},
	},
	{
		Name: "create_topic", Title: "创建项目讨论",
		Desc:   "在指定项目下发起新讨论",
		Method: http.MethodPost, PathTemplate: "/api/v1/projects/{project_id}/topics", BodyEnvelope: "message",
		Params: []mcpParam{
			pPath("project_id", "项目 ID"),
			pBody("subject", "string", true, "讨论标题"),
			pBody("content", "string", true, "讨论正文内容"),
		},
	},
	{
		Name: "update_topic", Title: "更新讨论",
		Desc:   "修改指定讨论的标题或正文内容",
		Method: http.MethodPatch, PathTemplate: "/api/v1/topics/{topic_id}", BodyEnvelope: "message",
		Params: []mcpParam{
			pPath("topic_id", "讨论 ID"),
			pBody("subject", "string", false, "讨论标题"),
			pBody("content", "string", false, "讨论正文内容"),
		},
	},
	{
		Name: "delete_topic", Title: "删除讨论",
		Desc:   "删除指定项目讨论（危险操作）",
		Method: http.MethodDelete, PathTemplate: "/api/v1/topics/{topic_id}",
		Params: []mcpParam{pPath("topic_id", "讨论 ID")},
	},

	// 文件与直传
	{
		Name: "list_uploads", Title: "获取项目文件列表",
		Desc:   "获取指定项目中的上传文件列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/projects/{project_id}/uploads",
		Params: []mcpParam{pPath("project_id", "项目 ID"), pPage(), pPageSize()},
	},
	{
		Name: "create_upload", Title: "关联创建项目文件",
		Desc:   "使用已上传完成的附件 attfile_guid 在项目中创建文件记录",
		Method: http.MethodPost, PathTemplate: "/api/v1/projects/{project_id}/uploads",
		Params: []mcpParam{
			pPath("project_id", "项目 ID"),
			pBody("attfile_guid", "string", true, "直传完成后获得的附件 GUID"),
		},
	},
	{
		Name: "get_upload", Title: "获取文件详情",
		Desc:   "获取指定项目文件的详细信息",
		Method: http.MethodGet, PathTemplate: "/api/v1/uploads/{upload_id}",
		Params: []mcpParam{pPath("upload_id", "文件 ID")},
	},
	{
		Name: "delete_upload", Title: "删除文件",
		Desc:   "删除指定项目文件（危险操作）",
		Method: http.MethodDelete, PathTemplate: "/api/v1/uploads/{upload_id}",
		Params: []mcpParam{pPath("upload_id", "文件 ID")},
	},
	{
		Name: "get_attachment_url", Title: "获取附件下载地址",
		Desc:   "根据附件 ID 获取原始下载链接信息",
		Method: http.MethodGet, PathTemplate: "/api/v1/attfiles/{attfile_id}",
		Params: []mcpParam{pPath("attfile_id", "附件 ID")},
	},
	{
		Name: "create_direct_upload", Title: "获取客户端直传 OSS 签名",
		Desc:   "获取向 Tower 阿里云 OSS 直传文件所需的签名凭证与回调参数",
		Method: http.MethodPost, PathTemplate: "/api/v1/teams/{team_id}/direct_uploads",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pBody("filename", "string", true, "文件名（如 report.pdf）"),
			pBody("byte_size", "integer", true, "文件字节大小"),
			pBody("md5", "string", true, "文件内容的 MD5 签名"),
		},
	},

	// 工时
	{
		Name: "list_project_time_logs", Title: "获取项目工时列表",
		Desc:   "获取指定项目下的所有工时记录，支持按成员与起止日期筛选。若返回 403，查询单个任务的工时可使用 list_todo_time_logs",
		Method: http.MethodGet, PathTemplate: "/api/v1/projects/{project_id}/time_logs",
		Params: []mcpParam{
			pPath("project_id", "项目 ID"),
			pPage(),
			pPageSize(),
			pQuery("creator_id", "creator_id", "string", "按成员 member_id 筛选"),
			pQuery("starts_date", "starts_date", "string", "开始日期（YYYY-MM-DD）"),
			pQuery("ends_date", "ends_date", "string", "结束日期（YYYY-MM-DD）"),
		},
	},
	{
		Name: "list_todo_time_logs", Title: "获取任务工时列表",
		Desc:   "获取指定任务下的所有工时记录",
		Method: http.MethodGet, PathTemplate: "/api/v1/todos/{todo_id}/time_logs",
		Params: []mcpParam{pPath("todo_id", "任务 ID"), pPage(), pPageSize()},
	},
	{
		Name: "create_time_log", Title: "添加任务工时",
		Desc:   "为指定任务添加一条工时记录",
		Method: http.MethodPost, PathTemplate: "/api/v1/todos/{todo_id}/time_logs", BodyEnvelope: "time_log",
		Params: []mcpParam{
			pPath("todo_id", "任务 ID"),
			pBody("starts_at", "string", true, "开始时间，如 2026-11-18T14:00"),
			pBody("ends_at", "string", true, "结束时间，如 2026-11-18T16:00"),
			pBody("creator_id", "string", true, "工时记录人 member_id"),
			pBody("desc", "string", false, "工时描述说明"),
		},
	},
	{
		Name: "update_time_log", Title: "更新工时信息",
		Desc:   "修改指定工时记录的起止时间、记录人或描述",
		Method: http.MethodPatch, PathTemplate: "/api/v1/time_logs/{time_log_id}", BodyEnvelope: "time_log",
		Params: []mcpParam{
			pPath("time_log_id", "工时记录 ID"),
			pBody("starts_at", "string", false, "开始时间，如 2026-11-18T14:00"),
			pBody("ends_at", "string", false, "结束时间，如 2026-11-18T16:00"),
			pBody("creator_id", "string", false, "工时记录人 member_id"),
			pBody("desc", "string", false, "工时描述说明"),
		},
	},
	{
		Name: "delete_time_log", Title: "删除工时记录",
		Desc:   "删除指定工时记录（危险操作）",
		Method: http.MethodDelete, PathTemplate: "/api/v1/time_logs/{time_log_id}",
		Params: []mcpParam{pPath("time_log_id", "工时记录 ID")},
	},

	// 动态与通知
	{
		Name: "list_events", Title: "获取团队动态列表",
		Desc:   "获取指定团队的活动动态流。将响应的 meta.next_cursor 传给 last_item_id 继续，不使用页码",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/events",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pQuery("last_item_id", "last_item_id", "integer", "上一页响应的 meta.next_cursor"),
			pQuery("page_size", "limit", "integer", "每页返回条数，默认 20，最大 50"),
		},
	},
	{
		Name: "list_notifications", Title: "获取团队通知列表",
		Desc:   "获取当前成员在指定团队中的通知列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/notifications",
		Params: []mcpParam{pPath("team_id", "团队 ID"), pPage(), pPageSize()},
	},
}

func buildToolSchema(spec mcpToolSpec) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, param := range spec.Params {
		prop := map[string]any{"description": param.Desc}
		switch param.Type {
		case "integer":
			prop["type"] = "integer"
		case "number":
			prop["type"] = "number"
		case "boolean":
			prop["type"] = "boolean"
		case "string_array":
			prop["type"] = "array"
			prop["items"] = map[string]any{"type": "string"}
		case "int_array":
			prop["type"] = "array"
			prop["items"] = map[string]any{"type": "integer"}
		case "object":
			prop["type"] = "object"
		default:
			prop["type"] = "string"
		}
		props[param.Name] = prop
		if param.Required {
			required = append(required, param.Name)
		}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func totalMCPToolCount() int {
	return len(towerMCPTools) + 8
}

func isSafeTodoStateDelete(apiPath string) bool {
	todoPath, isTodo := strings.CutPrefix(apiPath, "/api/v1/todos/")
	id, action, _ := strings.Cut(strings.TrimRight(todoPath, "/"), "/")
	return isTodo && id != "" && id != "." && id != ".." && !strings.Contains(action, "/") && (action == "completion" || action == "assignment")
}

func listMCPToolDefinitions() []map[string]any {
	out := make([]map[string]any, 0, totalMCPToolCount())
	for _, spec := range towerMCPTools {
		isReadOnly := spec.Method == http.MethodGet
		isDestructive := spec.Method == http.MethodDelete && !isSafeTodoStateDelete(spec.PathTemplate)
		out = append(out, map[string]any{
			"name":        spec.Name,
			"description": spec.Desc,
			"inputSchema": buildToolSchema(spec),
			"annotations": map[string]any{
				"title":           spec.Title,
				"readOnlyHint":    isReadOnly,
				"destructiveHint": isDestructive,
				"idempotentHint":  isReadOnly || spec.Method == http.MethodDelete,
				"openWorldHint":   false,
			},
		})
	}
	out = append(out,
		map[string]any{
			"name":        "get_current_datetime",
			"description": "获取当前北京时间（UTC+08:00）和星期，用于判断今天、明天等相对日期。",
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			"annotations": map[string]any{
				"title":           "获取当前日期时间",
				"readOnlyHint":    true,
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   false,
			},
		},
		map[string]any{
			"name":        "my_todo_overview",
			"description": "聚合当前账号未完成任务。不指定团队时最多查询 10 个团队，每类任务分别报告本次返回数和可确认的总数。使用成员任务关系统计活跃、未完成任务；关系数据不可用时，总数未知。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"team_id":         map[string]any{"type": "string", "description": "可选团队 ID；不传则自动检查当前账号所属的前 10 个团队"},
					"include_created": map[string]any{"type": "boolean", "description": "是否同时拉取自己创建的未完成任务（默认 false，仅拉取指派给自己的未完成任务）"},
					"page_size":       map[string]any{"type": "integer", "description": "每个列表最多返回的任务条数（默认 20，最大 50）"},
				},
			},
			"annotations": map[string]any{
				"title":           "聚合查询我的待办汇总",
				"readOnlyHint":    true,
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   false,
			},
		},
		map[string]any{
			"name":        "search_todos",
			"description": "使用 Tower 网页搜索按关键词查找任务。需要该账号已完成网页登录。在已加入的团队中搜索。result_time 为 Tower 网页展示的结果时间。实测任务与评论均对应各自的创建时间。",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"page":          map[string]any{"type": "integer", "minimum": 1, "description": "页码，默认 1。保留其他参数，按 next_page 继续。每团队每页 20 条。"},
					"category":      map[string]any{"type": "integer", "description": "Tower 网页内容类型，默认 2 任务。-1 全部、1 清单、4 文件或附件、7 评论、15 项目、18 知识库文档。"},
					"target_gid":    map[string]any{"type": "string", "description": "网页项目或知识库 GID，如 gid://tower2/Project/123。省略时搜索所有项目。"},
					"member_guid":   map[string]any{"type": "string", "description": "网页成员筛选器中的成员 GUID。"},
					"sort":          map[string]any{"type": "integer", "enum": []int{1, 2}, "description": "1 时间顺序（默认），2 相关度。"},
					"exclude_trash": map[string]any{"type": "boolean", "default": true, "description": "隐藏回收站内容。"},
					"keyword":       map[string]any{"type": "string", "description": "搜索关键词"},
				},
				"required": []string{"keyword"},
			},
			"annotations": map[string]any{
				"title":           "搜索任务",
				"readOnlyHint":    true,
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   false,
			},
		}, map[string]any{
			"name":        "advanced_search_todos",
			"description": "按关键词聚合搜索任务（受限于 Tower Open API，按成员/项目/团队范围拉取后本地过滤，内置频率、并发与上游请求数限流）",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"keyword":    map[string]any{"type": "string", "description": "搜索关键词（匹配任务标题、描述或编号）"},
					"member_id":  map[string]any{"type": "string", "description": "可选成员 ID（优先使用，速度最快）"},
					"project_id": map[string]any{"type": "string", "description": "可选项目 ID（扫描该项目下的任务清单）"},
					"team_id":    map[string]any{"type": "string", "description": "可选团队 ID（扫描该团队前 5 个项目；不传任何 ID 时默认搜索当前账号跨团队待办）"},
					"status":     map[string]any{"type": "string", "description": "任务状态：all（默认）/ uncompleted / completed"},
					"limit":      map[string]any{"type": "integer", "description": "最多返回条数（默认 20，最大 50）"},
				},
				"required": []string{"keyword"},
			},
			"annotations": map[string]any{
				"title":           "高级搜索任务",
				"readOnlyHint":    true,
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   false,
			},
		},
		map[string]any{
			"name":        "upload_file",
			"description": "一步上传文件到阿里云 OSS 并关联创建为 Tower 项目文件（自动计算字节大小与 MD5、获取直传签名、上传表单并写入项目文件列表）",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"team_id":        map[string]any{"type": "string", "description": "团队 ID"},
					"project_id":     map[string]any{"type": "string", "description": "目标项目 ID"},
					"filename":       map[string]any{"type": "string", "description": "文件名（如 report.md、diagram.png）"},
					"content":        map[string]any{"type": "string", "description": "文本文件内容（与 content_base64 二选一）"},
					"content_base64": map[string]any{"type": "string", "description": "Base64 编码的二进制文件内容（与 content 二选一）"},
					"content_type":   map[string]any{"type": "string", "description": "可选 MIME 类型（如 text/markdown、image/png）"},
				},
				"required": []string{"team_id", "project_id", "filename"},
			},
			"annotations": map[string]any{
				"title":           "一步上传并关联项目文件",
				"readOnlyHint":    false,
				"destructiveHint": false,
				"idempotentHint":  false,
				"openWorldHint":   false,
			},
		},
	)
	out = append(out, map[string]any{
		"name":        "add_todo_project",
		"description": "把任务添加到另一个项目的清单（网页「添加所属项目」）。不改变任务原有项目。省略 todolist_id 时使用目标项目的默认清单。需要网页登录。",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todo_id":     map[string]any{"type": "string", "description": "任务 GUID"},
				"project_id":  map[string]any{"type": "string", "description": "要添加的项目 GUID"},
				"todolist_id": map[string]any{"type": "string", "description": "目标项目中的清单 GUID；省略时用默认清单"},
			},
			"required": []string{"todo_id", "project_id"},
		},
		"annotations": map[string]any{
			"title": "添加任务所属项目", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false,
		},
	}, map[string]any{
		"name":        "remove_todo_project",
		"description": "移除任务与某个所属项目的关联。不删除任务或项目。需要网页登录。",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"todo_id":    map[string]any{"type": "string", "description": "任务 GUID"},
				"project_id": map[string]any{"type": "string", "description": "要移除的项目 GUID"},
			},
			"required": []string{"todo_id", "project_id"},
		},
		"annotations": map[string]any{
			"title": "移除任务所属项目", "readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false,
		},
	})
	out = append(out, projectTodosToolDefinition())
	out = append(out, map[string]any{
		"name":        "raw_request",
		"description": "向 Tower /api/v1/* 发送自定义 HTTP 请求（用于自定义筛选参数或未单独封装的高级接口）",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"method":    map[string]any{"type": "string", "description": "HTTP 方法：GET, POST, PATCH, PUT, DELETE"},
				"path":      map[string]any{"type": "string", "description": "以 /api/v1/ 开头的接口路径"},
				"query":     map[string]any{"type": "string", "description": "可选 URL 查询字符串"},
				"page":      map[string]any{"type": "integer", "description": "可选分页页码（从 1 开始）"},
				"page_size": map[string]any{"type": "integer", "description": "可选每页返回条数（默认 20，最大 50）"},
				"body":      map[string]any{"type": "object", "description": "可选 JSON 请求体对象"},
				"data_only": map[string]any{"type": "boolean", "description": "只返回响应的 data，省略 included 和分页元数据"},
				"fields":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": 1, "description": "仅保留 data 中指定的字段，如 [\"id\", \"content\", \"created_at\"]，同时省略 included"},
			},
			"required": []string{"method", "path"},
		},
		"annotations": map[string]any{
			"title":           "自定义 Tower API 请求",
			"readOnlyHint":    false,
			"destructiveHint": true,
			"openWorldHint":   false,
		},
	})
	for _, tool := range out {
		source := "API"
		switch tool["name"] {
		case "search_todos", "add_todo_project", "remove_todo_project":
			source = "Web"
		case "get_current_datetime":
			source = "Gateway"
		}
		meta := map[string]any{"agents-tower/source": source}
		if tool["name"] == "advanced_search_todos" || tool["name"] == "list_project_todos" {
			meta["agents-tower/tags"] = []string{"Multi-API"}
		}
		tool["_meta"] = meta
	}
	return out
}

func extractMCPKey(req *http.Request) string {
	for _, header := range []string{"AGENTS_TOWER_API_KEY", "X-API-Key", "X-Agents-Tower-Key"} {
		if val := strings.TrimSpace(req.Header.Get(header)); val != "" {
			return val
		}
	}
	auth := strings.TrimSpace(req.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

type mcpAccountKey struct{}

func setMCPHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization, AGENTS_TOWER_API_KEY, X-API-Key, X-Agents-Tower-Key, Mcp-Session-Id, Mcp-Protocol-Version, Mcp-Method, Mcp-Name")
	writer.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
}

func handleMCP(cfg Config) http.HandlerFunc {
	toolIndex := make(map[string]mcpToolSpec, len(towerMCPTools))
	for _, spec := range towerMCPTools {
		toolIndex[spec.Name] = spec
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "AgentsTower", Version: "1.0.0"}, &mcp.ServerOptions{
		Instructions: "完整分页枚举项目任务请用 list_project_todos。网页筛选与翻页请用 search_todos，按团队、项目 ID 或完成状态聚合扫描请用 advanced_search_todos。快速查询当前成员跨团队待办请直接调用 my_todo_overview；一步上传文件到项目请调用 upload_file。常规层级顺序：list_teams 获取 team_id -> list_projects 获取 project_id（或 list_team_members 获取 member_id）-> list_todolists 获取 todolist_id -> 查询、创建或完成任务（Tower 按目标任务权限执行）。标准列表工具默认每页 20 条（最大 50），按 meta.next_page 翻页；list_events 将 meta.next_cursor 传给 last_item_id。meta.returned 是本次返回数，meta.total 仅在可确认时提供。成员待办基于活跃且未完成的任务，任务关系不可用时可能回退到部分列表。my_todo_overview 未指定团队时最多检查 10 个团队。服务端默认开启硬删除保护（仅拦截删除项目/清单/任务/讨论/文件/工时等资源删除操作）。",
	})
	for _, def := range listMCPToolDefinitions() {
		data, err := json.Marshal(def)
		if err != nil {
			panic(err)
		}
		var tool mcp.Tool
		if err := json.Unmarshal(data, &tool); err != nil {
			panic(err)
		}
		server.AddTool(&tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			acc := ctx.Value(mcpAccountKey{}).(*StoredAccount)
			args := map[string]any{}
			if len(req.Params.Arguments) > 0 {
				if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
					return nil, err
				}
			}
			name := req.Params.Name
			text, isErr := executeMCPTool(ctx, cfg, acc.MCPAPIKey, acc, toolIndex, name, args)
			if !isErr {
				heading := resultProfiles[name].heading
				var scope []string
				for _, param := range toolIndex[name].Params {
					if param.In == "path" {
						scope = append(scope, param.Name+"="+stringifyArg(args[param.Name]))
					}
				}
				if len(scope) > 0 {
					heading += "（" + strings.Join(scope, "，") + "）"
				}
				text = heading + "\n" + text
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isErr}, nil
		})
	}
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if call, ok := req.GetParams().(*mcp.CallToolParamsRaw); method == "tools/call" && ok && call != nil {
				name := call.Name
				if _, known := resultProfiles[name]; !known {
					name = "未知工具"
				}
				acc := ctx.Value(mcpAccountKey{}).(*StoredAccount)
				return cfg.Store.auditMCPCall(acc, name, func() (mcp.Result, error) { return next(ctx, method, req) })
			}
			result, err := next(ctx, method, req)
			if list, ok := result.(*mcp.ListToolsResult); ok && err == nil {
				acc := ctx.Value(mcpAccountKey{}).(*StoredAccount)
				for i, t := range list.Tools {
					if t.Name == "rename_team" && !ownsAnyTeam(ctx, cfg, acc.AccessToken) {
						list.Tools = append(list.Tools[:i:i], list.Tools[i+1:]...)
						break
					}
				}
			}
			return result, err
		}
	})
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	return func(writer http.ResponseWriter, req *http.Request) {
		setMCPHeaders(writer)
		switch req.Method {
		case http.MethodOptions:
			writer.WriteHeader(http.StatusNoContent)
			return
		case http.MethodDelete:
			writer.WriteHeader(http.StatusNoContent)
			return
		case http.MethodGet:
			writer.Header().Set("Allow", "POST, DELETE, OPTIONS")
			http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		case http.MethodPost:
		default:
			http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		if cfg.Store == nil {
			writeJSON(writer, http.StatusInternalServerError, map[string]any{
				"jsonrpc": "2.0",
				"error":   map[string]any{"code": -32603, "message": "storage not initialized"},
			})
			return
		}

		mcpKey := extractMCPKey(req)
		if mcpKey == "" {
			writeJSON(writer, http.StatusUnauthorized, map[string]any{
				"jsonrpc": "2.0",
				"error":   map[string]any{"code": -32001, "message": "缺少鉴权密钥，请在请求头携带 AGENTS_TOWER_API_KEY"},
			})
			return
		}
		acc, err := cfg.Store.EnsureFreshToken(cfg, mcpKey, false)
		if err != nil {
			status := http.StatusUnauthorized
			msg := "无效的 AGENTS_TOWER_API_KEY，请在 AgentsTower 网页重新获取"
			if !errors.Is(err, ErrAccountNotFound) {
				msg = err.Error()
			}
			writeJSON(writer, status, map[string]any{
				"jsonrpc": "2.0",
				"error":   map[string]any{"code": -32001, "message": msg},
			})
			return
		}

		// Older curl examples and the web inspector omit transport negotiation headers.
		if accept := req.Header.Get("Accept"); accept == "" || accept == "*/*" || accept == "application/json" {
			req.Header.Set("Accept", "application/json, text/event-stream")
		}
		if req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Body = http.MaxBytesReader(writer, req.Body, 2<<20)
		transport.ServeHTTP(writer, req.WithContext(context.WithValue(req.Context(), mcpAccountKey{}, acc)))
	}
}

func stringifyArg(val any) string {
	switch typed := val.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func executeMCPTool(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	toolIndex map[string]mcpToolSpec,
	name string,
	args map[string]any,
) (string, bool) {
	if name == "get_current_datetime" {
		now := time.Now().In(towerTimeZone)
		return resultProfiles[name].text(map[string]any{
			"datetime": now.Format(time.RFC3339),
			"weekday":  now.Weekday().String(),
			"timezone": now.Location().String(),
		}), false
	}
	if name == "my_todo_overview" {
		return executeMyTodoOverview(ctx, cfg, mcpKey, acc, args)
	}
	if name == "advanced_search_todos" {
		return executeAdvancedSearchTodos(ctx, cfg, mcpKey, acc, args)
	}
	if name == "search_todos" {
		return executeSearchTodos(ctx, cfg, mcpKey, acc, args)
	}
	if name == "add_todo_project" {
		return executeAddTodoProject(ctx, cfg, acc, args)
	}
	if name == "remove_todo_project" {
		return executeRemoveTodoProject(ctx, cfg, acc, args)
	}
	if name == "upload_file" {
		return executeUploadFile(ctx, cfg, mcpKey, acc, args)
	}
	if name == "list_project_todos" {
		return executeListProjectTodos(ctx, cfg, mcpKey, acc, args)
	}
	if name == "raw_request" {
		return executeRawRequest(ctx, cfg, mcpKey, acc, args)
	}

	spec, ok := toolIndex[name]
	if !ok {
		return "未知工具: " + name, true
	}

	resolvedPath := spec.PathTemplate
	queryVals := url.Values{}
	bodyMap := map[string]any{}
	pageNum := 1
	maxItems := 0
	hasPageParam := false
	hasPageSizeParam := false
	localSlice := false

	for _, param := range spec.Params {
		if param.Name == "page" {
			hasPageParam = true
			if param.In == "page_only" {
				localSlice = true
			}
		}
		if param.Name == "page_size" {
			hasPageSizeParam = true
			if param.In == "limit_only" {
				localSlice = true
			}
		}
		rawVal, exists := args[param.Name]
		strVal := stringifyArg(rawVal)
		if param.Required && (!exists || rawVal == nil || (param.Type == "string" && strVal == "")) {
			return fmt.Sprintf("缺少必填参数: %s", param.Name), true
		}
		if !exists || rawVal == nil {
			continue
		}
		switch param.In {
		case "path":
			if strVal == "" {
				return fmt.Sprintf("路径参数 %s 不能为空", param.Name), true
			}
			resolvedPath = strings.ReplaceAll(resolvedPath, "{"+param.Name+"}", url.PathEscape(strVal))
		case "query":
			// Tower treats any nonempty completed_todo query value as true.
			if spec.Name == "list_todos" && param.Name == "completed_todo" && strVal == "false" {
				continue
			}
			if strVal == "" {
				continue
			}
			wire := param.WireName
			if wire == "" {
				wire = param.Name
			}
			if param.Name == "page_size" {
				clamped := clampPageSize(strVal)
				maxItems = clamped
				strVal = strconv.Itoa(clamped)
			} else if param.Name == "page" {
				pageNum = parsePositivePage(strVal)
				if pageNum < 1 {
					pageNum = 1
				}
				strVal = strconv.Itoa(pageNum)
			}
			queryVals.Set(wire, strVal)
		case "page_only":
			if p := parsePositivePage(strVal); p >= 1 {
				pageNum = p
			}
		case "limit_only":
			if strVal != "" {
				maxItems = clampPageSize(strVal)
			}
		case "body":
			wire := param.WireName
			if wire == "" {
				wire = param.Name
			}
			bodyMap[wire] = rawVal
		case "merge_body":
			if customMap, ok := rawVal.(map[string]any); ok {
				for k, v := range customMap {
					bodyMap[k] = v
				}
			}
		}
	}

	if name == "list_todos" && args["order"] != nil {
		if stringifyArg(args["order"]) != "-created_at" {
			return "list_todos 仅支持 order=-created_at", true
		}
		if maxItems == 0 {
			maxItems = defaultListPageSize
		}
		return listTodosByCreation(ctx, cfg, mcpKey, acc, resolvedPath, queryVals.Has("completed_todo"), pageNum, maxItems)
	}

	if name == "list_events" && parsePositivePage(stringifyArg(args["page"])) > 1 {
		return "list_events 使用游标分页，请将上一页的 meta.next_cursor 传给 last_item_id", true
	}

	if hasPageParam && queryVals.Get("page[number]") == "" {
		for _, param := range spec.Params {
			if param.Name == "page" && param.In == "query" {
				queryVals.Set("page[number]", strconv.Itoa(pageNum))
			}
		}
	}
	if hasPageSizeParam {
		if maxItems <= 0 {
			maxItems = defaultListPageSize
		}
		for _, param := range spec.Params {
			if param.Name == "page_size" && param.In == "query" && queryVals.Get(param.WireName) == "" {
				queryVals.Set(param.WireName, strconv.Itoa(maxItems))
			}
		}
	} else if spec.Method == http.MethodGet && strings.HasPrefix(spec.Name, "list_") {
		maxItems = maxListPageSize
	}

	if name == "list_member_assigned_uncompleted_todos" {
		cutoff, err := todoDueCutoff(args, time.Now())
		if err != nil {
			return err.Error(), true
		}
		if cutoff != "" {
			if args["page"] == nil && args["page_size"] == nil {
				maxItems = 0
			} else if args["page_size"] == nil {
				maxItems = maxListPageSize
			}
			return executeDueTodoList(ctx, cfg, mcpKey, acc, name, resolvedPath, cutoff, pageNum, maxItems)
		}
	}

	var bodyBytes []byte
	if spec.Method != http.MethodGet && spec.Method != http.MethodDelete {
		var payload any = bodyMap
		if spec.BodyEnvelope != "" {
			payload = map[string]any{spec.BodyEnvelope: bodyMap}
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "构造请求体失败: " + err.Error(), true
		}
		bodyBytes = encoded
	}

	return callTowerForMCP(ctx, cfg, mcpKey, acc, name, spec.Method, resolvedPath, queryVals.Encode(), bodyBytes, pageNum, maxItems, localSlice, nil)
}

func parsePositivePage(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 {
		return 0
	}
	return n
}

func clampPageSize(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return defaultListPageSize
	}
	if n > maxListPageSize {
		return maxListPageSize
	}
	return n
}

func isPaginatedListPath(apiPath string) bool {
	clean := strings.TrimRight(apiPath, "/")
	for _, suffix := range []string{
		"/members",
		"/assigned_todos",
		"/assigned_uncompleted_todos",
		"/assigned_completed_todos",
		"/created_todos",
		"/created_uncompleted_todos",
		"/created_completed_todos",
		"/todos",
		"/comments",
		"/topics",
		"/uploads",
		"/time_logs",
		"/notifications",
		"/events",
	} {
		if strings.HasSuffix(clean, suffix) {
			return true
		}
	}
	return false
}

func normalizeListQuery(method, apiPath, rawQuery string) (string, int) {
	if method != http.MethodGet {
		return rawQuery, 0
	}
	paginated := isPaginatedListPath(apiPath)
	isEvents := strings.HasSuffix(strings.TrimRight(apiPath, "/"), "/events")
	if rawQuery == "" && !paginated {
		return "", 0
	}
	vals, err := url.ParseQuery(rawQuery)
	if err != nil {
		if paginated {
			return rawQuery, defaultListPageSize
		}
		return rawQuery, 0
	}

	if isEvents {
		limit := clampPageSize(cmp.Or(vals.Get("page[size]"), vals.Get("limit"), vals.Get("per_page")))
		vals.Del("page[size]")
		vals.Del("per_page")
		vals.Del("page[number]")
		vals.Del("page")
		vals.Set("limit", strconv.Itoa(limit))
		return vals.Encode(), limit
	}

	mutated := false
	limit := 0
	if ps := vals.Get("page[size]"); ps != "" {
		clamped := clampPageSize(ps)
		limit = clamped
		if strconv.Itoa(clamped) != ps {
			vals.Set("page[size]", strconv.Itoa(clamped))
			mutated = true
		}
	} else if pp := vals.Get("per_page"); pp != "" {
		clamped := clampPageSize(pp)
		limit = clamped
		if strconv.Itoa(clamped) != pp {
			vals.Set("per_page", strconv.Itoa(clamped))
			mutated = true
		}
	}

	if paginated {
		if limit <= 0 {
			limit = defaultListPageSize
		}
		if vals.Get("page[number]") == "" && vals.Get("page") == "" {
			vals.Set("page[number]", "1")
			mutated = true
		}
		if vals.Get("page[size]") == "" && vals.Get("per_page") == "" {
			vals.Set("page[size]", strconv.Itoa(limit))
			mutated = true
		}
	}
	if mutated {
		return vals.Encode(), limit
	}
	return rawQuery, limit
}

func capJSONListBytes(respBytes []byte, page, maxItems int, localSlice bool) []byte {
	if maxItems <= 0 || len(respBytes) == 0 {
		return respBytes
	}
	if page < 1 {
		page = 1
	}
	doc, items := decodeMCPList(respBytes)
	if items == nil {
		return respBytes
	}
	_, hasServerLinks := doc["links"]
	useOffset := localSlice || (!hasServerLinks && len(items) > maxItems)
	if len(items) <= maxItems && (!useOffset || page == 1) {
		return respBytes
	}
	sliced := slicePageItems(items, page, maxItems, useOffset)
	if bytes.TrimSpace(respBytes)[0] == '[' {
		out, _ := json.Marshal(sliced)
		return out
	}
	doc["data"] = sliced
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["page"] = page
	meta["page_size"] = maxItems
	meta["returned"] = len(sliced)
	meta["original_count"] = len(items)
	if useOffset {
		if page < (len(items)+maxItems-1)/maxItems {
			meta["has_more"] = true
			meta["next_page"] = page + 1
		} else if _, known := meta["has_more"]; !known {
			meta["has_more"] = nil
		}
	} else {
		meta["truncated"] = true
	}
	doc["meta"] = meta
	if out, err := json.Marshal(doc); err == nil {
		return out
	}
	return respBytes
}

func slicePageItems(items []any, page, pageSize int, useOffset bool) []any {
	if !useOffset || page <= 1 {
		if len(items) <= pageSize {
			return items
		}
		return items[:pageSize]
	}
	if page-1 >= (len(items)+pageSize-1)/pageSize {
		return []any{}
	}
	start := (page - 1) * pageSize
	end := start + pageSize
	if end > len(items) {
		end = len(items)
	}
	return items[start:end]
}

func callTowerForMCP(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	tool, method, apiPath, rawQuery string,
	bodyBytes []byte,
	page, maxItems int,
	localSlice bool,
	rawOutput *rawMCPOutput,
) (string, bool) {
	if teamWriteDenied(ctx, cfg, acc.AccessToken, method, apiPath) {
		return fmt.Sprintf("%s %s 返回 HTTP 403: %s", method, apiPath, renameDenied), true
	}
	if method == http.MethodDelete && !isSafeTodoStateDelete(apiPath) {
		if cfg.Store == nil || !cfg.Store.GetSettings().AllowMCPDelete {
			return fmt.Sprintf("%s %s：服务端已启用硬删除保护。AgentsTower 系统管理员可在「成员管理」中开启「允许 MCP 硬删除」，开启后由 Tower 校验目标资源的删除权限。", method, apiPath), true
		}
	}
	var statusCode int
	var respBytes []byte
	var err error
	switch tool {
	case "list_member_assigned_uncompleted_todos", "list_member_created_uncompleted_todos":
		statusCode, respBytes, err = loadMemberUncompletedTodos(ctx, cfg, mcpKey, acc, apiPath)
	default:
		statusCode, respBytes, err = doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, method, apiPath, rawQuery, bodyBytes)
	}
	if err != nil {
		return towerMCPError(method, apiPath, statusCode, respBytes, err), true
	}
	if statusCode >= 200 && statusCode < 300 && maxItems > 0 {
		switch {
		case tool == "raw_request":
			respBytes = capJSONListBytes(respBytes, page, maxItems, localSlice)
		case tool == "list_events":
			cursorQuery, _ := url.ParseQuery(rawQuery)
			respBytes = paginateMCPList(respBytes, page, maxItems, false, cursorQuery)
		default:
			if tool == "list_member_assigned_todos" || tool == "list_member_created_todos" {
				respBytes, _ = unwrapMemberTodoList(respBytes)
				if _, rows := decodeMCPList(respBytes); rows == nil {
					return "Tower 返回的成员任务关系格式无效或超出响应大小限制", true
				}
			}
			respBytes = paginateMCPList(respBytes, page, maxItems, localSlice, nil)
		}
	}

	if statusCode >= 400 {
		return towerMCPError(method, apiPath, statusCode, respBytes, nil), true
	}
	if len(bytes.TrimSpace(respBytes)) == 0 {
		return fmt.Sprintf("操作成功 (HTTP %d)", statusCode), false
	}
	if rawOutput != nil {
		text, failed := rawOutput.text(respBytes)
		if failed {
			return fmt.Sprintf("%s %s：%s", method, apiPath, text), true
		}
		return text, false
	}
	return resultProfiles[tool].text(compactTowerBody(respBytes, tool)), false
}

func doTowerHTTPWithRefresh(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	method, apiPath, rawQuery string,
	bodyBytes []byte,
) (int, []byte, error) {
	statusCode, respBytes, err := doTowerHTTP(ctx, cfg, acc.AccessToken, method, apiPath, rawQuery, bodyBytes)
	if err != nil {
		return 0, nil, err
	}
	if statusCode == http.StatusUnauthorized && acc.RefreshToken != "" && cfg.Store != nil {
		refreshed, refreshErr := cfg.Store.EnsureFreshToken(cfg, mcpKey, true)
		if refreshErr == nil {
			acc.AccessToken = refreshed.AccessToken
			statusCode, respBytes, err = doTowerHTTP(ctx, cfg, refreshed.AccessToken, method, apiPath, rawQuery, bodyBytes)
			if err != nil {
				return 0, nil, err
			}
		}
	}
	return statusCode, respBytes, nil
}

func doTowerHTTP(
	ctx context.Context,
	cfg Config,
	accessToken, method, apiPath, rawQuery string,
	bodyBytes []byte,
) (int, []byte, error) {
	targetURL := cfg.BaseURL + apiPath
	if rawQuery != "" {
		targetURL += "?" + rawQuery
	}
	var bodyReader io.Reader
	if len(bodyBytes) > 0 {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	outReq, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	outReq.Header.Set("Authorization", "Bearer "+accessToken)
	outReq.Header.Set("Accept", "application/json")
	if len(bodyBytes) > 0 {
		outReq.Header.Set("Content-Type", "application/json")
	}
	resp, err := cfg.HTTPClient.Do(outReq)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	respBytes, err := readTowerResponse(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBytes, nil
}

func readTowerResponse(reader io.Reader) ([]byte, error) {
	const maxResponseSize = 2 << 20
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseSize+1))
	if err == nil && len(body) > maxResponseSize {
		return nil, errors.New("Tower 响应超过 2 MiB，无法读取完整结果")
	}
	return body, err
}

func executeMyTodoOverview(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	args map[string]any,
) (string, bool) {
	const maxOverviewTeams = 10
	targetTeamID := stringifyArg(args["team_id"])
	includeCreated := strings.EqualFold(stringifyArg(args["include_created"]), "true")
	limit := clampPageSize(stringifyArg(args["page_size"]))

	type teamEntry struct{ ID, Name string }
	var teams []teamEntry
	var reasons []string
	teamListPartial := false
	if targetTeamID != "" {
		teams = []teamEntry{{ID: targetTeamID}}
	} else {
		code, body, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, "/api/v1/teams", "", nil)
		if err != nil {
			return "查询团队列表失败: " + err.Error(), true
		}
		if code >= 400 {
			return fmt.Sprintf("查询团队列表返回 HTTP %d:\n%s", code, agentText(body)), true
		}
		doc, items := decodeMCPList(body)
		if items == nil {
			return "Tower 团队列表响应缺少有效 data 列表", true
		}
		teamListPartial = mcpListHasOmittedRows(doc, len(items))
		if teamListPartial {
			reasons = append(reasons, "Tower 只返回了部分团队")
		}
		for _, item := range items {
			team, _ := item.(map[string]any)
			id := stringifyArg(team["id"])
			if id == "" {
				return "Tower 团队列表响应缺少团队 ID", true
			}
			attrs, _ := team["attributes"].(map[string]any)
			name := stringifyArg(attrs["name"])
			if name == "" {
				name = id
			}
			teams = append(teams, teamEntry{ID: id, Name: name})
		}
	}

	teamsAvailable := len(teams)
	if teamsAvailable > maxOverviewTeams {
		teams = teams[:maxOverviewTeams]
	}
	teamsSkipped := teamsAvailable - len(teams)
	if teamsSkipped > 0 {
		reasons = append(reasons, fmt.Sprintf("概览最多检查 %d 个团队，已跳过 %d 个团队", maxOverviewTeams, teamsSkipped))
	}
	teamResults := make([]map[string]any, 0, len(teams))
	type todoTotals struct {
		total, returned int
		known           bool
	}
	assigned := todoTotals{known: teamsSkipped == 0 && !teamListPartial}
	created := todoTotals{known: assigned.known}
	memberFallback, itemLimit := false, false

	loadTodos := func(entry map[string]any, memberID, list string, totals *todoTotals) {
		path := "/api/v1/members/" + url.PathEscape(memberID) + "/" + list + "_todos"
		code, body, err := loadMemberUncompletedTodos(ctx, cfg, mcpKey, acc, path)
		if err != nil || code < 200 || code >= 300 {
			totals.known = false
			entry[list+"_error"] = overviewUpstreamError(code, err)
			return
		}
		doc, items := decodeMCPList(body)
		if items == nil {
			totals.known = false
			entry[list+"_error"] = "Tower 返回的任务列表格式无效"
			return
		}
		todos := make([]map[string]any, 0, min(len(items), limit))
		for _, item := range items[:min(len(items), limit)] {
			todo, ok := item.(map[string]any)
			if !ok {
				totals.known = false
				entry[list+"_error"] = "Tower 返回的任务格式无效"
				return
			}
			todos = append(todos, todo)
		}
		entry[list+"_todos"] = compactTodoItems(todos)
		entry[list+"_returned"] = len(todos)
		itemLimit = itemLimit || len(items) > limit
		totals.returned += len(todos)

		meta, _ := doc["meta"].(map[string]any)
		total, known := mcpListTotal(meta)
		if warning := meta["scope_warning"]; warning != nil {
			entry[list+"_warning"] = warning
		}
		if reason := meta["incomplete_reason"]; reason != nil {
			memberFallback = true
			entry[list+"_incomplete_reason"] = reason
		}
		entry[list+"_count"] = "未知"
		if known {
			entry[list+"_count"] = total
			totals.total += total
		} else {
			totals.known = false
		}
	}

	teamsFailed := 0
	for _, tm := range teams {
		entry := map[string]any{"team_id": tm.ID, "team_name": tm.Name}
		mPath := "/api/v1/teams/" + url.PathEscape(tm.ID) + "/member"
		mCode, mBody, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, mPath, "", nil)
		var memberDoc struct {
			Data struct {
				ID         string         `json:"id"`
				Attributes map[string]any `json:"attributes"`
			} `json:"data"`
		}
		switch {
		case err != nil || mCode < 200 || mCode >= 300:
			entry["error"] = "获取团队成员身份失败：" + overviewUpstreamError(mCode, err)
		case json.Unmarshal(mBody, &memberDoc) != nil || memberDoc.Data.ID == "":
			entry["error"] = "Tower 成员响应缺少有效成员 ID"
		default:
			memberID := memberDoc.Data.ID
			entry["member_id"] = memberID
			entry["nickname"] = memberDoc.Data.Attributes["nickname"]
			loadTodos(entry, memberID, "assigned_uncompleted", &assigned)
			if includeCreated {
				loadTodos(entry, memberID, "created_uncompleted", &created)
			}
		}
		if entry["error"] != nil {
			assigned.known, created.known = false, false
		}
		if entry["error"] != nil || entry["assigned_uncompleted_error"] != nil || entry["created_uncompleted_error"] != nil {
			teamsFailed++
		}
		teamResults = append(teamResults, entry)
	}

	summary := map[string]any{
		"teams_checked":                 len(teamResults),
		"teams_available":               teamsAvailable,
		"teams_skipped":                 teamsSkipped,
		"teams_failed":                  teamsFailed,
		"page_size":                     limit,
		"total_assigned_uncompleted":    "未知",
		"returned_assigned_uncompleted": assigned.returned,
		"teams":                         teamResults,
	}
	if assigned.known {
		summary["total_assigned_uncompleted"] = assigned.total
	}
	if includeCreated {
		summary["total_created_uncompleted"] = "未知"
		summary["returned_created_uncompleted"] = created.returned
		if created.known {
			summary["total_created_uncompleted"] = created.total
		}
	}
	if teamsFailed > 0 {
		reasons = append(reasons, fmt.Sprintf("%d 个团队的成员身份或任务查询失败", teamsFailed))
	}
	if memberFallback {
		reasons = append(reasons, "部分任务来自可能省略记录的成员待办接口")
	}
	if itemLimit {
		reasons = append(reasons, fmt.Sprintf("每个团队的每类任务最多显示 %d 条", limit))
	}
	if len(reasons) > 0 {
		summary["incomplete_reason"] = strings.Join(reasons, "。")
	}
	out, _ := json.Marshal(summary)
	return resultProfiles["my_todo_overview"].compositeText(out), false
}

func overviewUpstreamError(code int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("Tower API 返回 HTTP %d", code)
}

func executeUploadFile(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	args map[string]any,
) (string, bool) {
	teamID := stringifyArg(args["team_id"])
	projectID := stringifyArg(args["project_id"])
	filename := stringifyArg(args["filename"])
	if teamID == "" || projectID == "" || filename == "" {
		return "缺少必填参数: team_id、project_id、filename 均不能为空", true
	}
	rawB64 := stringifyArg(args["content_base64"])
	rawText, hasText := args["content"]
	var fileBytes []byte
	if rawB64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(rawB64)
		if err != nil {
			return "content_base64 解码失败: " + err.Error(), true
		}
		fileBytes = decoded
	} else if hasText && rawText != nil {
		fileBytes = []byte(fmt.Sprint(rawText))
	} else {
		return "请提供 content（文本内容）或 content_base64（Base64 二进制内容）", true
	}
	if len(fileBytes) == 0 {
		return "上传文件内容不能为空", true
	}
	if len(fileBytes) > 10<<20 {
		return "单次上传文件大小不能超过 10MB", true
	}

	sum := md5.Sum(fileBytes)
	md5Hex := hex.EncodeToString(sum[:])
	signPayload, _ := json.Marshal(map[string]any{
		"filename":  filename,
		"byte_size": len(fileBytes),
		"md5":       md5Hex,
	})
	signPath := "/api/v1/teams/" + url.PathEscape(teamID) + "/direct_uploads"
	signCode, signBody, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodPost, signPath, "", signPayload)
	if err != nil {
		return "获取直传签名失败: " + err.Error(), true
	}
	if signCode >= 400 {
		return fmt.Sprintf("获取直传签名返回 HTTP %d:\n%s", signCode, agentText(signBody)), true
	}

	var signResp struct {
		GUID         string            `json:"guid"`
		DirectUpload map[string]string `json:"directUpload"`
	}
	if err := json.Unmarshal(signBody, &signResp); err != nil || signResp.GUID == "" || signResp.DirectUpload["url"] == "" {
		return "解析直传签名响应失败: " + agentText(signBody), true
	}

	var mpBuf bytes.Buffer
	mpWriter := multipart.NewWriter(&mpBuf)
	for _, field := range []struct{ k, v string }{
		{"key", signResp.DirectUpload["key"]},
		{"OSSAccessKeyId", signResp.DirectUpload["OSSAccessKeyId"]},
		{"policy", signResp.DirectUpload["policy"]},
		{"Signature", signResp.DirectUpload["Signature"]},
		{"callback", signResp.DirectUpload["callback"]},
		{"success_action_status", "200"},
	} {
		if field.v != "" {
			_ = mpWriter.WriteField(field.k, field.v)
		}
	}
	contentType := stringifyArg(args["content_type"])
	if contentType == "" {
		contentType = http.DetectContentType(fileBytes)
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filename))
	partHeader.Set("Content-Type", contentType)
	part, err := mpWriter.CreatePart(partHeader)
	if err != nil {
		return "构造文件上传表单失败: " + err.Error(), true
	}
	if _, err := part.Write(fileBytes); err != nil {
		return "写入文件上传表单失败: " + err.Error(), true
	}
	_ = mpWriter.Close()

	ossReq, err := http.NewRequestWithContext(ctx, http.MethodPost, signResp.DirectUpload["url"], &mpBuf)
	if err != nil {
		return "创建 OSS 直传请求失败: " + err.Error(), true
	}
	ossReq.Header.Set("Content-Type", mpWriter.FormDataContentType())
	ossResp, err := cfg.HTTPClient.Do(ossReq)
	if err != nil {
		return "上传文件至 OSS 失败: " + err.Error(), true
	}
	ossBytes, _ := io.ReadAll(io.LimitReader(ossResp.Body, 1<<20))
	ossResp.Body.Close()
	if ossResp.StatusCode >= 400 {
		return fmt.Sprintf("上传文件至 OSS 返回 HTTP %d:\n%s", ossResp.StatusCode, agentText(ossBytes)), true
	}

	createPayload, _ := json.Marshal(map[string]any{"attfile_guid": signResp.GUID})
	createPath := "/api/v1/projects/" + url.PathEscape(projectID) + "/uploads"
	cCode, cBody, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodPost, createPath, "", createPayload)
	if err != nil {
		return "关联创建项目文件失败: " + err.Error(), true
	}
	if cCode >= 400 {
		return fmt.Sprintf("关联创建项目文件返回 HTTP %d:\n%s", cCode, agentText(cBody)), true
	}

	result, _ := json.Marshal(map[string]any{
		"attfile_guid": signResp.GUID,
		"filename":     filename,
		"byte_size":    len(fileBytes),
		"md5":          md5Hex,
		"upload":       compactTowerBody(cBody, "create_upload"),
	})
	return resultProfiles["upload_file"].compositeText(result), false
}
