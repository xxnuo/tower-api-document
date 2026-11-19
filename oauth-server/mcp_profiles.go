package main

import "strings"

// Fields describe the primary record for this operation. A star preserves
// extensible detail payloads, including custom fields and upload signatures.
type resultProfile struct{ heading, fields string }

var resultProfiles = map[string]resultProfile{
	"tower_get_current_user":                       {"当前账号", "id nickname email gavatar"},
	"tower_list_teams":                             {"可访问的团队", "id name"},
	"tower_get_team":                               {"团队详情", "id name is_pro is_trial remaining_days_for_pro *"},
	"tower_create_team":                            {"团队已创建", "id name"},
	"tower_rename_team":                            {"团队已更名", "id name"},
	"tower_get_team_resource":                      {"团队编号对应的资源", "id type *"},
	"tower_list_projects":                          {"团队项目", "id name is_archived"},
	"tower_get_project":                            {"项目详情", "id name desc todolists *"},
	"tower_create_project":                         {"项目已创建", "id name desc team members"},
	"tower_update_project":                         {"项目已更新", "id name desc members member_ids"},
	"tower_delete_project":                         {"项目已删除", "id name"},
	"tower_list_project_members":                   {"项目成员", "id nickname role is_active"},
	"tower_list_team_members":                      {"团队成员", "id nickname role groups is_active"},
	"tower_get_my_team_member":                     {"我在团队中的身份", "id nickname role team groups is_active"},
	"tower_get_member":                             {"成员档案", "id nickname role groups *"},
	"tower_list_member_assigned_todos":             {"指派给成员的任务", "id content team_wide_id is_completed completed due_at project priority"},
	"tower_list_member_assigned_uncompleted_todos": {"成员待办", "id content team_wide_id due_at priority project"},
	"tower_list_member_assigned_completed_todos":   {"成员已完成的指派任务", "id content team_wide_id completed_at closed_at closer project"},
	"tower_list_member_created_todos":              {"成员创建的任务", "id content team_wide_id is_completed completed assignee due_at project"},
	"tower_list_member_created_uncompleted_todos":  {"成员创建的未完成任务", "id content team_wide_id assignee due_at priority project"},
	"tower_list_member_created_completed_todos":    {"成员创建的已完成任务", "id content team_wide_id assignee completed_at closed_at closer project"},
	"tower_list_todolists":                         {"项目任务清单", "id name is_archived is_default"},
	"tower_get_todolist":                           {"清单详情及任务", "id name desc todos *"},
	"tower_create_todolist":                        {"清单已创建", "id name desc project"},
	"tower_update_todolist":                        {"清单已更新", "id name desc"},
	"tower_delete_todolist":                        {"清单已删除", "id name"},
	"tower_list_todos":                             {"清单中的任务", "id content team_wide_id is_completed completed assignee due_at priority labels"},
	"tower_create_todo":                            {"任务已创建", "id content team_wide_id assignee start_at due_at parent labels attachments custom_fields custom_field_value"},
	"tower_get_todo":                               {"任务详情", "id content team_wide_id desc assignee *"},
	"tower_get_todo_by_number":                     {"按团队编号找到的任务", "team_wide_id id content desc assignee *"},
	"tower_update_todo":                            {"任务已更新", "id content team_wide_id desc assignee start_at due_at labels custom_fields custom_field_value"},
	"tower_delete_todo":                            {"任务已删除", "id content team_wide_id"},
	"tower_complete_todo":                          {"任务已完成", "id content team_wide_id is_completed completed completed_at closed_at closer"},
	"tower_reopen_todo":                            {"任务已重新打开", "id content team_wide_id is_completed completed reopened assignee due_at"},
	"tower_list_todo_comments":                     {"任务评论", "id content creator created_at attachments"},
	"tower_create_todo_comment":                    {"评论已发布", "id content creator created_at attachments"},
	"tower_assign_todo":                            {"任务负责人已设置", "id content assignee assignee_id"},
	"tower_unassign_todo":                          {"任务负责人已移除", "id content"},
	"tower_update_todo_due":                        {"任务截止日期已更新", "id content due_at"},
	"tower_update_todo_desc":                       {"任务描述已更新", "id content desc"},
	"tower_list_todo_cc_members":                   {"任务通知成员", "id nickname"},
	"tower_update_todo_cc_members":                 {"更新后的通知成员", "id nickname"},
	"tower_list_topics":                            {"项目讨论", "id title subject creator created_at is_archived"},
	"tower_get_topic":                              {"讨论正文及回复", "id title subject content comments *"},
	"tower_create_topic":                           {"讨论已发布", "id title subject content creator attachments"},
	"tower_update_topic":                           {"讨论已更新", "id title subject content attachments updated_at"},
	"tower_delete_topic":                           {"讨论已删除", "id title subject"},
	"tower_list_uploads":                           {"项目文件", "id name filename attachment creator created_at"},
	"tower_create_upload":                          {"文件已关联到项目", "id name filename attachment"},
	"tower_get_upload":                             {"项目文件详情", "id name desc attachment *"},
	"tower_delete_upload":                          {"项目文件已删除", "id name filename"},
	"tower_get_attachment_url":                     {"附件下载地址", "url download_url filename id"},
	"tower_create_direct_upload":                   {"OSS 直传凭证", "guid directUpload *"},
	"tower_list_project_time_logs":                 {"项目工时（含所属任务）", "id duration starts_at ends_at creator todo desc"},
	"tower_list_todo_time_logs":                    {"任务工时", "id duration starts_at ends_at creator desc"},
	"tower_create_time_log":                        {"工时已登记", "id duration starts_at ends_at creator todo desc"},
	"tower_update_time_log":                        {"工时已更新", "id duration starts_at ends_at creator desc"},
	"tower_delete_time_log":                        {"工时记录已删除", "id duration"},
	"tower_list_events":                            {"团队动态（末条 ID 可用于 last_item_id）", "id action title creator created_at ancestor_type ancestor_name"},
	"tower_list_notifications":                     {"团队通知", "id read progress target_guid notifyable_type created_at subject_str action_str content_str extra_info"},
	"tower_search_todos":                           {"任务搜索结果", "keyword scope status matched_count scanned_count items truncated warnings"},
	"tower_my_todo_overview":                       {"我的待办概览", "teams_checked page_size total_assigned_uncompleted total_created_uncompleted teams"},
	"tower_upload_file":                            {"文件已上传", "filename attfile_guid byte_size md5 upload"},
	"tower_raw_request":                            {"Tower API 响应", "*"},
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
