package main

import (
	"bytes"
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
)

const (
	defaultMCPProtocolVersion = "2025-03-26"
	defaultListPageSize       = 20
	maxListPageSize           = 50
)

var supportedMCPVersions = map[string]bool{
	"2025-06-18": true,
	"2025-03-26": true,
	"2024-11-05": true,
}

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
		Desc:   "在指定团队下创建新项目",
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
		Desc:   "更新指定项目的名称、描述或成员列表",
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
		Desc:   "删除指定 Tower 项目（危险操作，会删除真实数据）",
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
		Params: []mcpParam{pPath("member_id", "成员 ID"), pPage(), pPageSize()},
	},
	{
		Name: "list_member_assigned_uncompleted_todos", Title: "获取指派给成员的未完成任务",
		Desc:   "获取指派给指定成员且尚未完成的任务列表",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/assigned_uncompleted_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pPage(), pPageSize()},
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
		Params: []mcpParam{pPath("member_id", "成员 ID"), pPage(), pPageSize()},
	},
	{
		Name: "list_member_created_uncompleted_todos", Title: "获取成员创建的未完成任务",
		Desc:   "获取由指定成员创建且未完成的任务",
		Method: http.MethodGet, PathTemplate: "/api/v1/members/{member_id}/created_uncompleted_todos",
		Params: []mcpParam{pPath("member_id", "成员 ID"), pPage(), pPageSize()},
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
			pBody("guids", "string_array", true, "需要通知的成员 member_id 列表"),
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
		Desc:   "获取指定项目下的所有工时记录，支持按成员与起止日期筛选",
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
		Desc:   "获取指定团队的活动动态流，可通过 last_item_id 游标向后翻页",
		Method: http.MethodGet, PathTemplate: "/api/v1/teams/{team_id}/events",
		Params: []mcpParam{
			pPath("team_id", "团队 ID"),
			pQuery("last_item_id", "last_item_id", "integer", "上一页最后一条动态 ID，用于游标分页"),
			pLocalPage(),
			pLocalPageSize(),
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
	return len(towerMCPTools) + 3
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
			"name":        "my_todo_overview",
			"description": "聚合获取当前账号跨团队的未完成任务汇总（自动查询所属团队、解析当前成员 member_id 并拉取指派给自己或自己创建的未完成任务，无需分步串联 ID）",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"team_id":         map[string]any{"type": "string", "description": "可选团队 ID；不传则自动汇总当前账号所属的全部团队"},
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
				"title":           "聚合搜索任务（限流）",
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

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func setMCPHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Authorization, AGENTS_TOWER_API_KEY, X-API-Key, X-Agents-Tower-Key, Mcp-Session-Id, MCP-Protocol-Version")
	writer.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id")
}

func handleMCP(cfg Config) http.HandlerFunc {
	toolIndex := make(map[string]mcpToolSpec, len(towerMCPTools))
	for _, spec := range towerMCPTools {
		toolIndex[spec.Name] = spec
	}
	toolDefs := listMCPToolDefinitions()

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

		if sessionID := req.Header.Get("Mcp-Session-Id"); sessionID != "" {
			writer.Header().Set("Mcp-Session-Id", sessionID)
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(req.Body, 2<<20))
		if err != nil {
			writeJSON(writer, http.StatusBadRequest, rpcError(nil, -32700, "failed to read body"))
			return
		}
		trimmed := bytes.TrimSpace(bodyBytes)
		if len(trimmed) == 0 {
			writeJSON(writer, http.StatusBadRequest, rpcError(nil, -32700, "empty request body"))
			return
		}

		isBatch := trimmed[0] == '['
		var rpcReqs []jsonRPCRequest
		if isBatch {
			if err := json.Unmarshal(trimmed, &rpcReqs); err != nil || len(rpcReqs) == 0 {
				writeJSON(writer, http.StatusBadRequest, rpcError(nil, -32700, "invalid JSON-RPC batch"))
				return
			}
		} else {
			var single jsonRPCRequest
			if err := json.Unmarshal(trimmed, &single); err != nil {
				writeJSON(writer, http.StatusBadRequest, rpcError(nil, -32700, "invalid JSON-RPC request"))
				return
			}
			rpcReqs = []jsonRPCRequest{single}
		}

		responses := make([]map[string]any, 0, len(rpcReqs))
		for _, rpcReq := range rpcReqs {
			if isNotification(rpcReq) {
				continue
			}
			resp := dispatchMCPMethod(req.Context(), writer, cfg, mcpKey, acc, toolIndex, toolDefs, rpcReq)
			responses = append(responses, resp)
		}

		if len(responses) == 0 {
			writer.WriteHeader(http.StatusAccepted)
			return
		}
		if isBatch {
			writeJSON(writer, http.StatusOK, responses)
			return
		}
		writeJSON(writer, http.StatusOK, responses[0])
	}
}

func isNotification(rpcReq jsonRPCRequest) bool {
	if len(rpcReq.ID) == 0 || string(rpcReq.ID) == "null" {
		return strings.HasPrefix(rpcReq.Method, "notifications/")
	}
	return false
}

func rpcResult(id json.RawMessage, result any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      rawOrNull(id),
		"result":  result,
	}
}

func rpcError(id json.RawMessage, code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      rawOrNull(id),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
}

func rawOrNull(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}

func dispatchMCPMethod(
	ctx context.Context,
	writer http.ResponseWriter,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	toolIndex map[string]mcpToolSpec,
	toolDefs []map[string]any,
	rpcReq jsonRPCRequest,
) map[string]any {
	switch rpcReq.Method {
	case "initialize":
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(rpcReq.Params, &initParams)
		version := defaultMCPProtocolVersion
		if supportedMCPVersions[initParams.ProtocolVersion] {
			version = initParams.ProtocolVersion
		}
		if writer.Header().Get("Mcp-Session-Id") == "" {
			if sid, err := randomState(); err == nil {
				writer.Header().Set("Mcp-Session-Id", sid)
			}
		}
		return rpcResult(rpcReq.ID, map[string]any{
			"protocolVersion": version,
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    "AgentsTower",
				"version": "1.0.0",
			},
			"instructions": "AgentsTower 提供 Tower.im 团队协作平台的完整操作工具。快速查询当前成员跨团队待办请直接调用 my_todo_overview；一步上传文件到项目请调用 upload_file。常规层级顺序：list_teams 获取 team_id -> list_projects 获取 project_id（或 list_team_members 获取 member_id）-> list_todolists 获取 todolist_id -> 查询、创建或完成任务（complete_todo / reopen_todo 默认允许所有成员调用）。所有列表工具默认每页限制返回 20 条（最大 50）。服务端默认开启硬删除保护（仅拦截删除项目/清单/任务/讨论/文件/工时等资源删除操作）。",
		})

	case "ping":
		return rpcResult(rpcReq.ID, map[string]any{})

	case "tools/list":
		if !ownsAnyTeam(ctx, cfg, acc.AccessToken) {
			visible := make([]map[string]any, 0, len(toolDefs))
			for _, def := range toolDefs {
				if def["name"] != "rename_team" {
					visible = append(visible, def)
				}
			}
			toolDefs = visible
		}
		return rpcResult(rpcReq.ID, map[string]any{
			"tools": toolDefs,
		})

	case "tools/call":
		var callParams struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(rpcReq.Params, &callParams); err != nil || callParams.Name == "" {
			return rpcError(rpcReq.ID, -32602, "invalid tools/call params")
		}
		if callParams.Arguments == nil {
			callParams.Arguments = map[string]any{}
		}
		text, isErr := executeMCPTool(ctx, cfg, mcpKey, acc, toolIndex, callParams.Name, callParams.Arguments)
		if !isErr {
			profile := resultProfiles[callParams.Name]
			heading := profile.heading
			if spec, ok := toolIndex[callParams.Name]; ok {
				var scope []string
				for _, param := range spec.Params {
					if param.In == "path" {
						scope = append(scope, param.Name+"="+stringifyArg(callParams.Arguments[param.Name]))
					}
				}
				if len(scope) > 0 {
					heading += "（" + strings.Join(scope, "，") + "）"
				}
			}
			text = heading + "\n" + text
		}
		return rpcResult(rpcReq.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": text},
			},
			"isError": isErr,
		})

	default:
		return rpcError(rpcReq.ID, -32601, "Method not found: "+rpcReq.Method)
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
	if name == "my_todo_overview" {
		return executeMyTodoOverview(ctx, cfg, mcpKey, acc, args)
	}
	if name == "search_todos" {
		return executeSearchTodos(ctx, cfg, mcpKey, acc, args)
	}
	if name == "upload_file" {
		return executeUploadFile(ctx, cfg, mcpKey, acc, args)
	}
	if name == "raw_request" {
		method := strings.ToUpper(stringifyArg(args["method"]))
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete:
		default:
			return "不支持的 HTTP 方法: " + method, true
		}
		apiPath := stringifyArg(args["path"])
		if !strings.HasPrefix(apiPath, "/api/v1/") {
			return "path 必须以 /api/v1/ 开头", true
		}
		rawQuery := strings.TrimPrefix(stringifyArg(args["query"]), "?")
		rawPage := parsePositivePage(stringifyArg(args["page"]))
		if rawPage > 0 || stringifyArg(args["page_size"]) != "" {
			qVals, qErr := url.ParseQuery(rawQuery)
			if qErr == nil {
				if rawPage > 0 {
					qVals.Set("page[number]", strconv.Itoa(rawPage))
				}
				if ps := stringifyArg(args["page_size"]); ps != "" {
					qVals.Set("page[size]", strconv.Itoa(clampPageSize(ps)))
				}
				rawQuery = qVals.Encode()
			}
		}
		normalizedQuery, maxItems := normalizeListQuery(method, apiPath, rawQuery)
		if rawPage <= 0 {
			rawPage = 1
		}
		var bodyBytes []byte
		if bodyObj, ok := args["body"]; ok && bodyObj != nil && method != http.MethodGet && method != http.MethodDelete {
			encoded, err := json.Marshal(bodyObj)
			if err != nil {
				return "序列化请求体失败: " + err.Error(), true
			}
			bodyBytes = encoded
		}
		return callTowerForMCP(ctx, cfg, mcpKey, acc, name, method, apiPath, normalizedQuery, bodyBytes, rawPage, maxItems, false)
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
			if param.Name == "page_size" && param.In == "query" && queryVals.Get("page[size]") == "" {
				queryVals.Set("page[size]", strconv.Itoa(maxItems))
			}
		}
	} else if spec.Method == http.MethodGet && strings.HasPrefix(spec.Name, "list_") {
		maxItems = maxListPageSize
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

	return callTowerForMCP(ctx, cfg, mcpKey, acc, name, spec.Method, resolvedPath, queryVals.Encode(), bodyBytes, pageNum, maxItems, localSlice)
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
		if !isEvents {
			if vals.Get("page[number]") == "" && vals.Get("page") == "" {
				vals.Set("page[number]", "1")
				mutated = true
			}
			if vals.Get("page[size]") == "" && vals.Get("per_page") == "" {
				vals.Set("page[size]", strconv.Itoa(limit))
				mutated = true
			}
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
	trimmed := bytes.TrimSpace(respBytes)
	if len(trimmed) == 0 {
		return respBytes
	}
	if trimmed[0] == '[' {
		var arr []any
		if json.Unmarshal(trimmed, &arr) == nil && (len(arr) > maxItems || (localSlice && page > 1)) {
			sliced := slicePageItems(arr, page, maxItems, localSlice)
			if out, err := json.Marshal(sliced); err == nil {
				return out
			}
		}
		return respBytes
	}
	if trimmed[0] != '{' {
		return respBytes
	}
	var doc map[string]any
	if json.Unmarshal(trimmed, &doc) != nil {
		return respBytes
	}
	dataSlice, ok := doc["data"].([]any)
	if !ok {
		return respBytes
	}
	_, hasServerLinks := doc["links"]
	useOffset := localSlice || (!hasServerLinks && page > 1 && len(dataSlice) > maxItems)
	if len(dataSlice) <= maxItems && (!useOffset || page == 1) {
		return respBytes
	}
	origCount := len(dataSlice)
	sliced := slicePageItems(dataSlice, page, maxItems, useOffset)
	doc["data"] = sliced
	meta, _ := doc["meta"].(map[string]any)
	if meta == nil {
		meta = map[string]any{}
	}
	meta["truncated"] = origCount > len(sliced)
	meta["page"] = page
	meta["page_size"] = maxItems
	meta["returned"] = len(sliced)
	meta["original_count"] = origCount
	if useOffset {
		meta["has_more"] = page*maxItems < origCount
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
	start := (page - 1) * pageSize
	if start >= len(items) {
		return []any{}
	}
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
) (string, bool) {
	if teamWriteDenied(ctx, cfg, acc.AccessToken, method, apiPath) {
		return "HTTP 403: " + renameDenied, true
	}
	if method == http.MethodDelete && !isSafeTodoStateDelete(apiPath) {
		if cfg.Store == nil || !cfg.Store.GetSettings().AllowMCPDelete {
			return "服务端已启用硬删除保护：禁止通过 MCP 删除项目、清单、任务或文件资源（仅允许取消任务完成状态或移除负责人；管理员可在「成员管理」后台开启硬删除权限）", true
		}
	}
	statusCode, respBytes, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, method, apiPath, rawQuery, bodyBytes)
	if err != nil {
		return "请求 Tower API 失败: " + err.Error(), true
	}
	if statusCode >= 200 && statusCode < 300 && maxItems > 0 {
		respBytes = capJSONListBytes(respBytes, page, maxItems, localSlice)
	}

	if statusCode >= 400 {
		return fmt.Sprintf("Tower API 返回错误 HTTP %d:\n%s", statusCode, agentText(respBytes)), true
	}
	if len(bytes.TrimSpace(respBytes)) == 0 {
		return fmt.Sprintf("操作成功 (HTTP %d)", statusCode), false
	}
	if tool != "raw_request" {
		return resultProfiles[tool].text(compactTowerBody(respBytes, tool)), false
	}
	return agentText(respBytes), false
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
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, respBytes, nil
}

func extractJSONAPIDataItems(raw []byte) []map[string]any {
	var doc struct {
		Data []map[string]any `json:"data"`
	}
	if json.Unmarshal(raw, &doc) == nil && doc.Data != nil {
		return doc.Data
	}
	return []map[string]any{}
}

func executeMyTodoOverview(
	ctx context.Context,
	cfg Config,
	mcpKey string,
	acc *StoredAccount,
	args map[string]any,
) (string, bool) {
	targetTeamID := stringifyArg(args["team_id"])
	includeCreated := strings.EqualFold(stringifyArg(args["include_created"]), "true")
	limit := clampPageSize(stringifyArg(args["page_size"]))

	type teamEntry struct {
		ID   string
		Name string
	}
	var teams []teamEntry
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
		for _, item := range extractJSONAPIDataItems(body) {
			id, _ := item["id"].(string)
			if id == "" {
				continue
			}
			name := id
			if attrs, ok := item["attributes"].(map[string]any); ok {
				if n, ok := attrs["name"].(string); ok && n != "" {
					name = n
				}
			}
			teams = append(teams, teamEntry{ID: id, Name: name})
			if len(teams) >= 10 {
				break
			}
		}
	}

	teamResults := make([]map[string]any, 0, len(teams))
	totalAssigned := 0
	totalCreated := 0

	for _, tm := range teams {
		mPath := "/api/v1/teams/" + url.PathEscape(tm.ID) + "/member"
		mCode, mBody, err := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, mPath, "", nil)
		if err != nil || mCode >= 400 {
			teamResults = append(teamResults, map[string]any{
				"team_id":   tm.ID,
				"team_name": tm.Name,
				"error":     fmt.Sprintf("获取团队成员身份失败 (HTTP %d)", mCode),
			})
			continue
		}
		var memberDoc struct {
			Data struct {
				ID         string         `json:"id"`
				Attributes map[string]any `json:"attributes"`
			} `json:"data"`
		}
		if json.Unmarshal(mBody, &memberDoc) != nil || memberDoc.Data.ID == "" {
			continue
		}
		memberID := memberDoc.Data.ID
		nickname, _ := memberDoc.Data.Attributes["nickname"].(string)
		q := fmt.Sprintf("page[number]=1&page[size]=%d", limit)

		aPath := "/api/v1/members/" + url.PathEscape(memberID) + "/assigned_uncompleted_todos"
		_, aBody, _ := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, aPath, q, nil)
		assignedItems := extractJSONAPIDataItems(capJSONListBytes(aBody, 1, limit, false))
		totalAssigned += len(assignedItems)

		entry := map[string]any{
			"team_id":                    tm.ID,
			"team_name":                  tm.Name,
			"member_id":                  memberID,
			"nickname":                   nickname,
			"assigned_uncompleted_count": len(assignedItems),
			"assigned_uncompleted_todos": compactTodoItems(assignedItems),
		}
		if includeCreated {
			cPath := "/api/v1/members/" + url.PathEscape(memberID) + "/created_uncompleted_todos"
			_, cBody, _ := doTowerHTTPWithRefresh(ctx, cfg, mcpKey, acc, http.MethodGet, cPath, q, nil)
			createdItems := extractJSONAPIDataItems(capJSONListBytes(cBody, 1, limit, false))
			totalCreated += len(createdItems)
			entry["created_uncompleted_count"] = len(createdItems)
			entry["created_uncompleted_todos"] = compactTodoItems(createdItems)
		}
		teamResults = append(teamResults, entry)
	}

	summary := map[string]any{
		"teams_checked":              len(teamResults),
		"page_size":                  limit,
		"total_assigned_uncompleted": totalAssigned,
		"teams":                      teamResults,
	}
	if includeCreated {
		summary["total_created_uncompleted"] = totalCreated
	}
	out, _ := json.Marshal(summary)
	return resultProfiles["my_todo_overview"].compositeText(out), false
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
