package modruntime

import (
	"context"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

// sendEmailCommand shares the existing MSG validation and atomic admission.
// A route can enter only through the typed local REST bridge, never JSON input.
func (r Runtime) sendEmailCommand(ctx context.Context, command foundation.CommandEnvelope, route *domain.InboxSMTPBinding) (any, error) {
	if failure := confirmationQueueRestriction(command); failure != nil {
		return nil, failure
	}
	toEmail, _ := command.Input["to_email"].(string)
	fromEmail, _ := command.Input["from_email"].(string)
	subject, _ := command.Input["subject"].(string)
	htmlBody, _ := command.Input["content_html"].(string)
	textBody, _ := command.Input["content_text"].(string)
	toEmail, fromEmail, subject = strings.TrimSpace(toEmail), strings.TrimSpace(fromEmail), strings.TrimSpace(subject)
	if address, err := mail.ParseAddress(toEmail); err != nil || address.Address != toEmail {
		return nil, &ContractFailure{Type: "validation_failed", Message: "to_email is invalid"}
	}
	if address, err := mail.ParseAddress(fromEmail); err != nil || address.Address != fromEmail {
		return nil, &ContractFailure{Type: "validation_failed", Message: "from_email is invalid"}
	}
	if subject == "" || len(subject) > 512 || (strings.TrimSpace(htmlBody) == "" && strings.TrimSpace(textBody) == "") {
		return nil, &ContractFailure{Type: "validation_failed", Message: "email subject and content are required"}
	}
	emailType, _ := command.Input["email_type"].(string)
	emailType = strings.ToLower(strings.TrimSpace(emailType))
	if emailType != "transcript" && emailType != "reset_password" && emailType != "notification" {
		return nil, &ContractFailure{Type: "validation_failed", Message: "email_type is invalid"}
	}
	var conversationID *uint
	if value, exists := command.Input["conversation_id"]; exists {
		parsed, err := optionalUint(value)
		if err != nil || parsed == nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is invalid"}
		}
		conversationID = parsed
		conDependency, ok := r.Config.Dependencies["CON"]
		if !ok {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation validation is unavailable"}
		}
		query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": *conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		var conversation conversationView
		if err := newModuleClient("MSG", conDependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
			return nil, err
		}
		if route != nil && (route.AccountID != strconv.FormatUint(uint64(conversation.AccountID), 10) || route.InboxID != strconv.FormatUint(uint64(conversation.InboxID), 10)) {
			return nil, &ContractFailure{Type: "state_conflict", Message: "transcript route does not match its conversation"}
		}
	}
	if route != nil && (emailType != "transcript" || conversationID == nil) {
		return nil, &ContractFailure{Type: "validation_failed", Message: "inbox route requires a transcript conversation"}
	}
	if command.Target.Type == "conversation" {
		if conversationID == nil || command.Target.ID != strconv.FormatUint(uint64(*conversationID), 10) {
			return nil, &ContractFailure{Type: "validation_failed", Message: "conversation target must match conversation_id"}
		}
	} else if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
		return nil, &ContractFailure{Type: "validation_failed", Message: "email target is invalid"}
	}
	var userID *uint
	if value, exists := command.Input["user_id"]; exists {
		parsed, err := optionalUint(value)
		if err != nil || parsed == nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user_id is invalid"}
		}
		userID = parsed
		tenDependency, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "email user validation is unavailable"}
		}
		if err := newModuleClient("MSG", tenDependency, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, *userID); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	email := domain.EmailLog{AccountID: command.AccountID, ConversationID: conversationID, UserID: userID, EmailType: emailType, ToEmail: toEmail, FromEmail: fromEmail, Subject: subject, ContentHTML: htmlBody, ContentText: textBody, Status: "pending", DeliveryStatus: "pending", MaxRetries: 3, CreatedAt: now, UpdatedAt: now}
	tx := Transaction(ctx, r.DB)
	if ordinaryEmailType(email.EmailType) {
		if err := r.enqueueOrdinaryEmailRoute(ctx, command, &email, route); err != nil {
			return nil, err
		}
	} else if err := tx.Create(&email).Error; err != nil {
		return nil, &ContractFailure{Type: "state_conflict", Message: "email delivery could not be queued"}
	}
	if err := r.appendBusinessEvent(ctx, command, "msg.email_delivery.changed.v1", "EmailLog", strconv.FormatUint(uint64(email.ID), 10), []string{"delivery_status"}, map[string]any{"email_log_id": email.ID, "conversation_id": email.ConversationID, "email_type": email.EmailType, "delivery_status": email.DeliveryStatus}); err != nil {
		return nil, err
	}
	return map[string]any{"id": email.ID, "account_id": email.AccountID, "conversation_id": email.ConversationID, "email_type": email.EmailType, "status": email.Status, "delivery_status": email.DeliveryStatus, "retry_count": email.RetryCount, "max_retries": email.MaxRetries, "created_at": email.CreatedAt, "updated_at": email.UpdatedAt}, nil
}
