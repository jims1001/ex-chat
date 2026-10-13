package modruntime

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

const messageFromCannedPreview = "msg.message.create_from_canned_preview.v1"

// Source hints bind the explicit confirmation intent; they are not proof that
// OPS preview was called or that the referenced template is still current.
func parseConfirmedCannedPreview(c foundation.CommandEnvelope) (uint, uint, string, error) {
	invalid := func() (uint, uint, string, error) {
		return 0, 0, "", &ContractFailure{Type: "validation_failed", Message: "confirmed canned preview shape is unsupported"}
	}
	actorID, actorOK := cannedResponseID(c.Actor.ID)
	if c.Actor.Type != "User" || c.Actor.AuthSource != "module" || !actorOK {
		return 0, 0, "", &ContractFailure{Type: "permission_denied", Message: "confirmed canned preview requires a trusted module User actor"}
	}
	conversationID, valid := cannedResponseID(c.Target.ID)
	if c.Validate() != nil || c.CommandType != messageFromCannedPreview || uint64(c.AccountID) > math.MaxInt64 || !valid || c.Target.Type != "conversation" || c.Target.ExpectedVersion != 0 || len(c.Input) != 3 || strings.TrimSpace(c.IdempotencyKey) != c.IdempotencyKey {
		return invalid()
	}
	source, ok := c.Input["canned_response_id"].(string)
	if _, valid := cannedResponseID(source); !ok || !valid {
		return invalid()
	}
	version, ok := c.Input["object_version"].(string)
	if !ok {
		return invalid()
	}
	n, err := strconv.ParseInt(version, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != version {
		return invalid()
	}
	raw, ok := c.Input["resolved_content"].(string)
	if !ok || len(raw) > 100000 || !utf8.ValidString(raw) {
		return invalid()
	}
	content := strings.TrimSpace(raw)
	if content == "" {
		return invalid()
	}
	return actorID, conversationID, content, nil
}

func (r *Runtime) authorizeConfirmedCannedPreview(ctx context.Context, c foundation.CommandEnvelope) error {
	if _, _, _, err := parseConfirmedCannedPreview(c); err != nil {
		return err
	}
	return r.requireMessageCreateActor(ctx, c)
}

func (r *Runtime) registerConfirmedCannedPreview() error {
	r.Contracts.authorizers[messageFromCannedPreview] = r.authorizeConfirmedCannedPreview
	return r.Contracts.RegisterCommand(messageFromCannedPreview, func(ctx context.Context, c foundation.CommandEnvelope) (any, error) {
		if err := r.authorizeConfirmedCannedPreview(ctx, c); err != nil {
			return nil, err
		}
		actorID, conversationID, content, err := parseConfirmedCannedPreview(c)
		if err != nil {
			return nil, err
		}
		tx, ok := ctx.Value(transactionKey{}).(*gorm.DB)
		if !ok || tx == nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "confirmed message transaction is unavailable"}
		}
		dep, ok := r.Config.Dependencies["CON"]
		if !ok {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation validation is unavailable"}
		}
		query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: c.AccountID, Actor: c.Actor, Filter: map[string]any{"id": c.Target.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "contact_id", "status"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		var conversation conversationView
		if err := newModuleClient("MSG", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
			return nil, err
		}
		if conversation.ID != conversationID || conversation.AccountID != c.AccountID || conversation.Status == domain.ConversationStatusDeleting {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		now := time.Now().UTC()
		message := domain.Message{AccountID: c.AccountID, ConversationID: conversationID, SenderType: domain.SenderTypeUser, SenderID: actorID, MessageType: domain.MessageTypeOutgoing, ContentType: domain.ContentTypeText, Content: content, Status: domain.MessageStatusQueued, CreatedAt: now, UpdatedAt: now}
		if err := tx.WithContext(ctx).Create(&message).Error; err != nil {
			return nil, &ContractFailure{Type: "state_conflict", Message: "message could not be created"}
		}
		if err := r.appendBusinessEvent(ctx, c, "msg.message.created.v1", "Message", strconv.FormatUint(uint64(message.ID), 10), []string{"created"}, map[string]any{"message_id": message.ID, "conversation_id": conversationID, "sender_type": message.SenderType, "sender_id": actorID, "message_type": message.MessageType, "content_type": message.ContentType, "private": false, "status": message.Status}); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return messageFromModel(message), nil
	})
}
