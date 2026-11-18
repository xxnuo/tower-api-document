---
name: agents-tower
description: Use Tower.im team collaboration platform via AgentsTower Streamable HTTP MCP Server — query teams, projects, members, todolists, todos, topics, uploads, time logs, and notifications, or create/assign/complete tasks and upload project files.
---

# AgentsTower — Tower.im MCP Skill Guide

## 1. 接入配置 (`mcp.json`)

在 Agent 客户端（Claude Code / Codex / Cursor 等）中添加以下 Streamable HTTP MCP 服务配置：

```json
{
  "mcpServers": {
    "agents-tower": {
      "type": "http",
      "url": "{{MCP_URL}}",
      "headers": {
        "AGENTS_TOWER_API_KEY": "<在 AgentsTower 凭据中心获取的 at_sk-... 密钥>"
      }
    }
  }
}
```

- 服务端会自动管理并续期底层 Tower OAuth Token，`AGENTS_TOWER_API_KEY` 长期有效（除非用户在控制台手动重置）。

## 2. 标准 ID 查询链路

Tower API 采用层级资源结构。除复合快捷工具外，常规操作请按以下顺序获取前置 ID：

1. **快速汇总我的待办（推荐首选）**：
   - 直接调用 `tower_my_todo_overview`（无需预先知道 `team_id` 或 `member_id`，自动返回当前账号在各团队的 `team_id`、`member_id` 及未完成待办列表）。
2. **团队 -> 项目 / 成员**：
   - 调用 `tower_list_teams` 获取 `team_id`。
   - 调用 `tower_list_projects`（传 `team_id`）获取 `project_id`。
   - 调用 `tower_list_team_members` 或 `tower_get_my_team_member`（传 `team_id`）获取 `member_id`（指派任务或填报工时时使用）。
3. **项目 -> 任务清单 -> 任务（Todo）**：
   - 调用 `tower_list_todolists`（传 `project_id`）获取 `todolist_id`。
   - 调用 `tower_list_todos`（传 `todolist_id`）查询任务，或调用 `tower_create_todo` 创建任务。
   - 任务后续操作（评论、指派负责人 `tower_assign_todo`、设置截止时间 `tower_set_todo_due`、完成任务 `tower_complete_todo`）均使用 `todo_id`。
4. **一步上传项目文件**：
   - 优先调用复合工具 `tower_upload_file`（传入 `team_id`、`project_id`、`filename` 以及 `content` 文本或 `content_base64`），服务端会自动完成计算 MD5、申请阿里云 OSS 签名、直传表单并关联到项目。

## 3. 分页与负载限制规范

- 所有 `tower_list_*` 列表工具及 `tower_raw_request` 默认限制每页返回 `20` 条数据（`page=1, page_size=20`），单页最大允许 `page_size=50`。
- 非原生分页接口由 AgentsTower 服务端按 `(page, page_size)` 切片，并在响应 `meta` 字段中标注：
  - `truncated`: 是否发生截断
  - `page` / `page_size` / `returned` / `original_count`
  - `has_more`: 是否有下一页（为 `true` 时递增 `page` 继续拉取）
- 请勿一次性无限制拉取全量历史任务或动态，优先通过 `page` 与筛选参数按需翻页。

## 4. 危险操作与硬删除保护

- **服务端硬删除保护**：AgentsTower 默认拦截除 `tower_reopen_todo`（取消任务完成状态）和 `tower_unassign_todo`（移除任务负责人）之外的所有 `DELETE` 硬删除工具（如删除项目、删除清单、删除任务、删除文件）。若业务确实需要执行硬删除，需由系统管理员在 AgentsTower「成员管理」后台开启“允许 MCP 硬删除”开关。
- **人工确认要求**：即使管理员开启了硬删除权限，Agent 在调用任何 `DELETE` 类工具或修改他人任务状态前，**必须先向用户列出目标资源名称与 ID 并获得明确确认**。
