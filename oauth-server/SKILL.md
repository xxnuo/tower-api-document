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
    "tower": {
      "type": "http",
      "url": "{{MCP_URL}}",
      "headers": {
        "AGENTS_TOWER_API_KEY": "<在 AgentsTower 账号与接入获取的 at_sk-... 密钥>"
      }
    }
  }
}
```

- 服务端会自动管理并续期底层 Tower OAuth Token，`AGENTS_TOWER_API_KEY` 长期有效（除非用户在控制台手动重置）。

按关键词或标签查询用 `search_todos`，须提供非空的 `keyword` 或 `tag`（标签可带 `#`）。提供有效标签时会忽略空白关键词。`member_guid`（即 Tower API 成员 ID）用于附加筛选，传入时只搜索该成员所属团队。可选参数还包括 `target_gid`、`category`、`sort`、`exclude_trash`。每次返回各搜索团队的一页，收到 `next_page` 时保留筛选条件并将其作为 `page` 继续。要按 API 项目 ID、团队或完成状态扫描时使用 `advanced_search_todos`，它通过 Tower API 限量扫描任务。

网页搜索的 `tag` 会命中 Tower 索引中的 `#标签` 内容；查询任务上的结构化标签请使用下面的 `label` 条件。

项目内条件查询用 `filter_project_todos`（Multi-API）。例如传 `conditions: [{"field":"label","operator":"in","value":"bug"},{"field":"custom.<字段 key>","operator":"in","value":"Web"}]`、`status: "uncompleted"` 和 `project_id`。字段筛选使用选项显示名称，任务写入使用选项 ID。日期按北京时间的自然日比较。例如 `{"field":"created_at","operator":"eq","value":"2026-11-19"}` 查询当天新增的任务。每页按匹配数计，仅在确认还有匹配时返回 `next_cursor`。

登录 Tower 网页后还可使用以下工具。

- `get_project_task_fields(project_id)` 查询当前项目的标签 ID、自定义字段 key 和选项。用返回值调用已有的 `create_todo`、`update_todo`。
- `move_todo_in_project(todo_id, project_id, todolist_id)` 将已有任务在指定项目内移到目标清单。跨项目添加映射使用 `add_todo_project`，指定父任务使用 `update_todo.parent_id`。
- `list_todo_history(todo_id)` 按时间从早到晚返回变更记录与评论。每页默认 20 条，可传 `page_size`（1–50），收到 `next_offset` 时将其作为 `offset` 继续。

## 2. 标准 ID 查询链路

Tower API 采用层级资源结构。除复合快捷工具外，常规操作请按以下顺序获取前置 ID：

1. **快速汇总我的待办（推荐首选）**：
   - 直接调用 `my_todo_overview`（无需预先知道 `team_id` 或 `member_id`，自动返回当前账号在各团队的 `team_id`、`member_id` 及未完成待办列表）。
2. **团队 -> 项目 / 成员**：
   - 调用 `list_teams` 获取 `team_id`。
   - 调用 `list_projects`（传 `team_id`）获取 `project_id`。
   - 调用 `list_team_members` 或 `get_my_team_member`（传 `team_id`）获取 `member_id`（指派任务或填报工时时使用）。
3. **项目 -> 任务清单 -> 任务（Todo）**：
   - 调用 `list_todolists`（传 `project_id`）获取 `todolist_id`。
   - 调用 `list_todos`（传 `todolist_id`）查询任务，或调用 `create_todo` 创建任务。
   - 任务后续操作（评论、指派/移除负责人 `assign_todo` / `unassign_todo`、设置截止时间 `update_todo_due`、完成任务 `complete_todo`、重新打开任务 `reopen_todo`）均使用 `todo_id`，默认对所有成员开放。
4. **一步上传项目文件**：
   - 优先调用复合工具 `upload_file`（传入 `team_id`、`project_id`、`filename` 以及 `content` 文本或 `content_base64`），服务端会自动完成计算 MD5、申请阿里云 OSS 签名、直传表单并关联到项目。

## 3. 分页与负载限制规范

- 标准 `list_*` 工具默认每页 20 条，最大 50 条。按 `meta.next_page` 继续请求，保留原有筛选参数与 `page_size`。
- `list_events` 使用 Tower 游标分页。将 `meta.next_cursor` 传给下一次调用的 `last_item_id`，保留原 `page_size`。
- `list_todos(order="-created_at")` 先读取清单的所有任务页，按创建时间倒序排列，再返回请求的页。
- 列表在 `meta.incomplete_reason` 中说明无法取全的原因。待办概览和高级搜索在顶层返回该字段。
- `meta.returned` 是本次返回条数。仅在能确认总数时提供 `meta.total`。`meta.has_more` 为「未知」时，Tower 未确认是否还有下一页，若同时提供下一页参数，可继续请求确认。正常分页不会标记为截断。
- `list_project_todos` 遍历指定项目的全部清单。默认包含已完成任务，传 `completed_todo=false` 仅取未完成任务。该工具和 `filter_project_todos` 均支持 `order="-created_at"` 获取整个项目最新的匹配任务。排序首屏需要扫描全项目。翻页时保留筛选和排序条件，将 `next_cursor` 传给 `cursor`，游标在当前进程内有效 15 分钟。
- 使用 `list_member_assigned_uncompleted_todos(overdue=true)` 查询北京时间今天之前已到期的未完成任务，也可用 `due_before=YYYY-MM-DD` 指定日期，截止当天不计入。省略 `page` 和 `page_size` 时一次返回所有可确认的匹配项。指定任一分页参数时按页返回，筛选时默认每页 50 条。若 `scope_complete` 为「否」，匹配数仅反映已扫描到且能确认截止日期的记录。
- `list_member_created_todos` 和 `list_member_assigned_todos` 支持 `order="-created_at"`，先对已核验归属的任务排序再分页。跨成员查询若返回 `scope_complete=false`，只确认返回的任务及其顺序，总量未知。
- 成员未完成任务列表优先通过成员任务关系查询活跃且未完成的任务。关系数据不可用时回退到 Tower 的专用列表，专用列表可能省略任务，此时总数未知。
- `my_todo_overview` 未指定 `team_id` 时最多检查 10 个团队。每个团队的每类任务默认返回 20 条，最多 50 条。检查失败、跳过团队或仅得到部分任务时，对应总数未知。
- `raw_request` 对识别出的分页路径使用现有的默认页大小及上限。可传 `data_only=true` 只返回当前页数据，或传 `fields=["id","content","created_at"]` 选择字段。

## 4. 危险操作与硬删除保护

- **任务完成与状态操作默认放行**：完成任务（`complete_todo`）、重新打开已完成任务（`reopen_todo`）以及指派/移除任务负责人（`assign_todo` / `unassign_todo`）属于常规协作操作，标记为非破坏性写入，由 Tower 按目标任务权限执行。普通成员执行常规任务操作无需额外确认。更新通知成员时，向 `update_todo_cc_members` 传入完整成员列表；空数组清空列表。
- 项目工时列表返回 403 时，若需查询某个任务的工时，可调用 `list_todo_time_logs`。成员已实测可在自己的子任务上登记、读取和更新工时。
- **服务端硬删除保护**：AgentsTower 默认仅拦截真正删除资源的 `DELETE` 硬删除工具（删除项目、删除清单、删除任务、删除讨论、删除文件、删除工时）。若确实需要执行硬删除，需由系统管理员在 AgentsTower「成员管理」后台开启“允许 MCP 硬删除”开关，且执行前**必须先向用户列出目标资源名称与 ID 并获得明确确认**。

## 5. 真实任务测试

每轮都在[测试父任务 #76083](https://tower.im/teams/750316/todos/76083/)下新建子任务，在本轮子任务上执行写操作。父子关系核对、收尾方式和历次实测结果见 [MCP 真实任务测试](README.md#mcp-真实任务测试)。
