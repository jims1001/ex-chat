package domain

import (
	"encoding/json"
	"strings"
)

// PermissionChineseToBackendMap maps frontend Chinese resource:capability strings to backend permission constants
var PermissionChineseToBackendMap = map[string][]string{
	"会话:查看":   {PermissionConversationManage, PermissionConversationParticipatingManage, PermissionConversationUnassignedManage, "conversation_view"},
	"会话:处理":   {PermissionConversationManage, PermissionConversationParticipatingManage, PermissionConversationUnassignedManage},
	"会话:管理":   {PermissionConversationManage, PermissionConversationParticipatingManage, PermissionConversationUnassignedManage},
	"会话:导出":   {PermissionConversationManage, "conversation_export"},

	"客户:查看":   {PermissionContactManage, "contact_view"},
	"客户:处理":   {PermissionContactManage},
	"客户:管理":   {PermissionContactManage},
	"客户:导出":   {PermissionContactManage, "contact_export"},

	"工单:查看":   {PermissionTicketView},
	"工单:处理":   {PermissionTicketManage},
	"工单:管理":   {PermissionTicketManage},
	"工单:导出":   {PermissionTicketManage},

	"自动化:查看":  {PermissionAutomationManage, "automation_view"},
	"自动化:处理":  {PermissionAutomationManage},
	"自动化:管理":  {PermissionAutomationManage},

	"报表:查看":   {PermissionReportView, PermissionReportManage},
	"报表:处理":   {PermissionReportView, PermissionReportManage},
	"报表:管理":   {PermissionReportView, PermissionReportManage},
	"报表:导出":   {PermissionReportView, PermissionReportManage},

	"知识:查看":   {PermissionSettingsManage, PermissionKnowledgeBaseManage, "knowledge_view"},
	"知识:处理":   {PermissionSettingsManage, PermissionKnowledgeBaseManage, "knowledge_manage"},
	"知识:管理":   {PermissionSettingsManage, PermissionKnowledgeBaseManage, "knowledge_manage"},

	"AI 助手:查看": {PermissionAIManage},
	"AI 助手:处理": {PermissionAIManage},
	"AI 助手:管理": {PermissionAIManage},

	"集成:查看":   {PermissionSettingsManage, PermissionInboxManage},
	"集成:处理":   {PermissionSettingsManage, PermissionInboxManage},
	"集成:管理":   {PermissionSettingsManage, PermissionInboxManage},

	"开放接口:查看": {PermissionSettingsManage},
	"开放接口:处理": {PermissionSettingsManage},
	"开放接口:管理": {PermissionSettingsManage},

	"工作区设置:查看": {PermissionSettingsManage},
	"工作区设置:处理": {PermissionSettingsManage},
	"工作区设置:管理": {
		PermissionSettingsManage,
		PermissionAgentManage,
		PermissionCustomRoleManage,
		PermissionInboxManage,
		PermissionTeamManage,
		PermissionBillingManage,
		PermissionAuditManage,
	},

	"质检:查看":   {PermissionQAView},
	"质检:处理":   {PermissionQAEvaluate, PermissionQAAppeal},
	"质检:管理":   {PermissionQAManage, PermissionQAAppealReview, PermissionQAEvaluate, PermissionQAAppeal, PermissionQAView},
	"质检:导出":   {PermissionQAView},
	"质检:评分":   {PermissionQAEvaluate},
	"质检:申诉":   {PermissionQAAppeal},
	"质检:复核":   {PermissionQAAppealReview},
}

// RoleMatchesPermission checks if a collection of granted permissions matches the target permission
func RoleMatchesPermission(grantedPerms []string, targetPerm string) bool {
	if targetPerm == "" {
		return true
	}

	for _, p := range grantedPerms {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// 1. Direct match or Super Admin
		if p == PermissionAdministrator || p == targetPerm {
			return true
		}

		// 2. Chatwoot Standard Permission Hierarchy
		// conversation_manage grants conversation_unassigned_manage and conversation_participating_manage
		if p == PermissionConversationManage {
			if targetPerm == PermissionConversationUnassignedManage || targetPerm == PermissionConversationParticipatingManage || targetPerm == "conversation_view" {
				return true
			}
		}
		if p == PermissionReportManage && (targetPerm == PermissionReportView || targetPerm == "report_view") {
			return true
		}
		if p == PermissionKnowledgeBaseManage && (targetPerm == PermissionSettingsManage || targetPerm == "knowledge_view" || targetPerm == "knowledge_manage") {
			return true
		}
		if targetPerm == PermissionKnowledgeBaseManage && (p == "knowledge_manage" || p == PermissionSettingsManage) {
			return true
		}

		// 3. System Extended Permission Hierarchy
		if targetPerm == PermissionTicketView && (p == PermissionTicketManage || p == PermissionQAManage) {
			return true
		}
		if targetPerm == PermissionQAView && (p == PermissionQAManage || p == PermissionQAEvaluate || p == PermissionQAAppealReview) {
			return true
		}
		if targetPerm == PermissionQAEvaluate && p == PermissionQAManage {
			return true
		}
		if targetPerm == PermissionQAAppeal && p == PermissionQAManage {
			return true
		}
		if targetPerm == PermissionQAAppealReview && p == PermissionQAManage {
			return true
		}

		// 4. Match from Chinese representation
		if backendPerms, ok := PermissionChineseToBackendMap[p]; ok {
			for _, bp := range backendPerms {
				if bp == targetPerm {
					return true
				}
				if targetPerm == PermissionTicketView && bp == PermissionTicketManage {
					return true
				}
				if targetPerm == PermissionQAView && (bp == PermissionQAManage || bp == PermissionQAEvaluate) {
					return true
				}
				if bp == PermissionConversationManage && (targetPerm == PermissionConversationUnassignedManage || targetPerm == PermissionConversationParticipatingManage) {
					return true
				}
			}
		}

		// 5. In case the stored permission is backend constant, but check was against alias
		if targetPerm == PermissionConversationManage && (p == "conversation_manage" || p == "conversation_view") {
			return true
		}
	}

	return false
}

// CheckPermissionInJSON checks permissions stored in JSON array string
func CheckPermissionInJSON(permsJSON string, targetPerm string) bool {
	if permsJSON == "" || permsJSON == "[]" {
		return false
	}
	var perms []string
	if err := json.Unmarshal([]byte(permsJSON), &perms); err != nil {
		return false
	}
	return RoleMatchesPermission(perms, targetPerm)
}

// NormalizePermissions deduplicates and expands permission tokens between Chinese keys and backend constants
func NormalizePermissions(input []string) []string {
	seen := make(map[string]bool)
	var result []string

	add := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}

	for _, p := range input {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		add(p)
		// Expand Chinese to backend tokens
		if backendPerms, ok := PermissionChineseToBackendMap[p]; ok {
			for _, bp := range backendPerms {
				add(bp)
			}
		}
	}

	return result
}

// FormatPermissionsJSON unmarshals and normalizes arbitrary permissions input to a JSON array string
func FormatPermissionsJSON(val any) string {
	if val == nil {
		return "[]"
	}
	var perms []string
	switch v := val.(type) {
	case string:
		v = strings.TrimSpace(v)
		if v == "" || v == "[]" {
			return "[]"
		}
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			_ = json.Unmarshal([]byte(v), &perms)
		} else {
			perms = []string{v}
		}
	case []string:
		perms = v
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				perms = append(perms, s)
			}
		}
	default:
		bytes, err := json.Marshal(v)
		if err != nil {
			return "[]"
		}
		_ = json.Unmarshal(bytes, &perms)
	}

	normalized := NormalizePermissions(perms)
	bytes, err := json.Marshal(normalized)
	if err != nil {
		return "[]"
	}
	return string(bytes)
}
