package main

import "strings"

// Fields describe the primary record for this operation. A star preserves
// extensible detail payloads, including custom fields and upload signatures.
type resultProfile struct{ heading, fields string }

var resultProfiles = map[string]resultProfile{
	"get_current_datetime":                   {"当前日期时间", "datetime weekday timezone"},
	"get_current_user":                       {"当前账号", "id nickname email gavatar"},
	"list_teams":                             {"可访问的团队", "id name"},
	"get_team":                               {"团队详情", "id name is_pro is_trial remaining_days_for_pro *"},
	"create_team":                            {"团队已创建", "id name"},
	"rename_team":                            {"团队已更名", "id name"},
	"get_team_resource":                      {"团队编号对应的资源", "id type *"},
	"list_projects":                          {"团队项目", "id name is_archived"},
	"get_project":                            {"项目详情", "id name desc todolists *"},
	"create_project":                         {"项目已创建", "id name desc team members"},
	"update_project":                         {"项目已更新", "id name desc members member_ids"},
	"delete_project":                         {"项目已删除", "id name"},
	"list_project_members":                   {"项目成员", "id nickname role is_active"},
	"list_team_members":                      {"团队成员", "id nickname role groups is_active"},
	"get_my_team_member":                     {"我在团队中的身份", "id nickname role team groups is_active"},
	"get_member":                             {"成员档案", "id nickname role groups *"},
	"list_member_assigned_todos":             {"指派给成员的任务", "id content team_wide_id is_completed completed created_at updated_at due_at project priority"},
	"list_member_assigned_uncompleted_todos": {"成员待办", "id content team_wide_id created_at updated_at due_at priority project box"},
	"list_member_assigned_completed_todos":   {"成员已完成的指派任务", "id content team_wide_id created_at updated_at due_at completed_at closed_at closer project"},
	"list_member_created_todos":              {"成员创建的任务", "id content team_wide_id is_completed completed assignee created_at updated_at due_at project"},
	"list_member_created_uncompleted_todos":  {"成员创建的未完成任务", "id content team_wide_id assignee created_at updated_at due_at priority project box"},
	"list_member_created_completed_todos":    {"成员创建的已完成任务", "id content team_wide_id created_at updated_at due_at assignee completed_at closed_at closer project"},
	"list_todolists":                         {"项目任务清单", "id name is_archived is_default"},
	"get_todolist":                           {"清单详情及任务", "id name desc todos *"},
	"create_todolist":                        {"清单已创建", "id name desc project"},
	"update_todolist":                        {"清单已更新", "id name desc"},
	"delete_todolist":                        {"清单已删除", "id name"},
	"list_project_todos":                     {"项目任务", "project_id page_size returned has_more next_cursor items"},
	"list_todos":                             {"清单中的任务", "id content team_wide_id is_completed completed assignee created_at updated_at due_at priority labels"},
	"create_todo":                            {"任务已创建", "id content team_wide_id assignee start_at due_at parent labels attachments custom_fields custom_field_value"},
	"get_todo":                               {"任务详情", "id content team_wide_id desc assignee *"},
	"get_todo_by_number":                     {"按团队编号找到的任务", "team_wide_id id content desc assignee *"},
	"update_todo":                            {"任务已更新", "id content team_wide_id desc assignee start_at due_at labels custom_fields custom_field_value"},
	"delete_todo":                            {"任务已删除", "id content team_wide_id"},
	"complete_todo":                          {"任务已完成", "id content team_wide_id is_completed completed completed_at closed_at closer"},
	"reopen_todo":                            {"任务已重新打开", "id content team_wide_id is_completed completed reopened assignee due_at"},
	"list_todo_comments":                     {"任务评论", "id content creator created_at attachments"},
	"create_todo_comment":                    {"评论已发布", "id content creator created_at attachments"},
	"assign_todo":                            {"任务负责人已设置", "id content assignee assignee_id"},
	"unassign_todo":                          {"任务负责人已移除", "id content"},
	"update_todo_due":                        {"任务截止日期已更新", "id content due_at"},
	"update_todo_desc":                       {"任务描述已更新", "id content desc"},
	"list_todo_cc_members":                   {"任务通知成员", "id nickname"},
	"update_todo_cc_members":                 {"更新后的通知成员", "id nickname"},
	"list_topics":                            {"项目讨论", "id title subject creator created_at is_archived"},
	"get_topic":                              {"讨论正文及回复", "id title subject content comments *"},
	"create_topic":                           {"讨论已发布", "id title subject content creator attachments"},
	"update_topic":                           {"讨论已更新", "id title subject content attachments updated_at"},
	"delete_topic":                           {"讨论已删除", "id title subject"},
	"list_uploads":                           {"项目文件", "id name filename attachment creator created_at"},
	"create_upload":                          {"文件已关联到项目", "id name filename attachment"},
	"get_upload":                             {"项目文件详情", "id name desc attachment *"},
	"delete_upload":                          {"项目文件已删除", "id name filename"},
	"get_attachment_url":                     {"附件下载地址", "url download_url filename id"},
	"create_direct_upload":                   {"OSS 直传凭证", "guid directUpload *"},
	"list_project_time_logs":                 {"项目工时（含所属任务）", "id duration starts_at ends_at creator todo desc"},
	"list_todo_time_logs":                    {"任务工时", "id duration starts_at ends_at creator desc"},
	"create_time_log":                        {"工时已登记", "id duration starts_at ends_at creator todo desc"},
	"update_time_log":                        {"工时已更新", "id duration starts_at ends_at creator desc"},
	"delete_time_log":                        {"工时记录已删除", "id duration"},
	"list_events":                            {"团队动态", "id action title creator created_at ancestor_type ancestor_name"},
	"list_notifications":                     {"团队通知", "id read progress target_guid notifyable_type created_at subject_str action_str content_str extra_info"},
	"advanced_search_todos":                  {"高级任务搜索结果", "keyword scope status matched_count scanned_count incomplete_reason items truncated warnings"},
	"search_todos":                           {"任务搜索结果", "keyword tag page count has_more next_page items"},
	"list_todo_dependencies":                 {"任务依赖", "*"},
	"add_todo_dependency":                    {"任务依赖已添加", "*"},
	"remove_todo_dependency":                 {"任务依赖已解除", "*"},
	"copy_todo":                              {"任务复制结果", "*"},
	"get_todo_schedule":                      {"任务排期", "*"},
	"set_todo_estimated_time":                {"任务预估工时", "*"},
	"set_todo_milestone":                     {"任务里程碑", "*"},
	"set_todo_recurrence":                    {"任务循环规则", "*"},
	"list_repositories":                      {"团队知识库", "*"},
	"list_repository_items":                  {"目录内容", "*"},
	"read_repository_item":                   {"知识库条目", "*"},
	"edit_my_todo_comment":                   {"自己的评论已更新", "*"},
	"mark_notification_read":                 {"通知已读", "*"},
	"list_todo_history":                      {"任务历史", "offset total has_more next_offset items"},
	"move_todo_in_project":                   {"项目内任务移动", "*"},
	"get_project_task_fields":                {"项目字段与标签", "*"},
	"filter_project_todos":                   {"项目筛选结果", "*"},
	"add_todo_project":                       {"所属项目已添加", "todo_id project_id todolist_id todolist section_link_id already"},
	"remove_todo_project":                    {"所属项目已移除", "todo_id project_id section_link_id"},
	"my_todo_overview":                       {"我的待办概览", "teams_checked teams_available teams_skipped teams_failed page_size total_assigned_uncompleted returned_assigned_uncompleted total_created_uncompleted returned_created_uncompleted incomplete_reason teams"},
	"upload_file":                            {"文件已上传", "filename attfile_guid byte_size md5 upload"},
	"raw_request":                            {"Tower API 响应", "*"},
}

func (p resultProfile) selectFields(record map[string]any) map[string]any {
	out := make(map[string]any)
	for _, key := range strings.Fields(p.fields) {
		if key == "*" {
			return record
		}
		if v, ok := record[key]; ok {
			out[key] = v
		}
	}
	if len(out) == 0 {
		return record
	} // Preserve an unfamiliar upstream response.
	return out
}

func (p resultProfile) text(value any) string {
	if record, ok := value.(map[string]any); ok {
		if _, items := record["items"]; items && !strings.Contains(p.fields, "items") {
			return renderAgentValue(record, "items", "meta", "next")
		}
		return renderAgentValue(p.selectFields(record), strings.Fields(p.fields)...)
	}
	return renderAgentValue(value)
}
