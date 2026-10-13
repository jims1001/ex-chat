package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

type automationMessageValidationKey struct{}
type automationMessageValidation struct {
	Digest string
	Grant  automationMessageAuthorizationView
}

func (r *Runtime) registerAutomationMessageActions() error {
	if err := r.Contracts.RegisterCommand("msg.automation.message.create.v1", r.createAutomationMessage); err != nil {
		return err
	}
	return r.Contracts.RegisterCommandPreflight("msg.automation.message.create.v1", r.preflightAutomationMessage)
}
func (r *Runtime) preflightAutomationMessage(ctx context.Context, command foundation.CommandEnvelope) (context.Context, error) {
	origin, depth, err := automationOwnerProvenance(command)
	if err != nil {
		return ctx, err
	}
	conversationID, failure := conversationCommandTarget(command)
	if failure != nil {
		return ctx, failure
	}
	content, contentOK := command.Input["content"].(string)
	private, privateOK := command.Input["private"].(bool)
	prepareID, prepareOK := command.Input["prepare_command_id"].(string)
	if !contentOK || strings.TrimSpace(content) == "" || len(content) > 100000 || !privateOK || !prepareOK || len(prepareID) != 64 || command.Target.ExpectedVersion == 0 {
		return ctx, &ContractFailure{Type: "validation_failed", Message: "frozen message content, visibility and authorization are required"}
	}
	if _, err := hex.DecodeString(prepareID); err != nil {
		return ctx, &ContractFailure{Type: "validation_failed", Message: "message authorization is invalid"}
	}
	consumed, err := readAutomationMessageConsumption(r.DB.WithContext(ctx), command.AccountID, prepareID)
	if err != nil {
		return ctx, err
	}
	var grant automationMessageAuthorizationView
	if consumed != nil {
		grant = consumed.Grant
	} else {
		dep, ok := r.Config.Dependencies["CON"]
		if !ok {
			return ctx, &ContractFailure{Type: "dependency_unavailable", Message: "conversation authorization is unavailable"}
		}
		query := foundation.QueryEnvelope{QueryType: "con.automation.message_authorization.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"prepare_command_id": prepareID, "conversation_id": conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"account_id", "conversation_id", "object_version", "content_hash", "private", "origin_execution_id", "automation_depth", "actor_id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		if err := newModuleClient("MSG", dep, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &grant); err != nil {
			return ctx, err
		}
	}
	hash := sha256.Sum256([]byte(content))
	if grant.PrepareCommandID != prepareID || grant.AccountID != command.AccountID || grant.ConversationID != conversationID || uint64(grant.ObjectVersion) != command.Target.ExpectedVersion || grant.ContentHash != hex.EncodeToString(hash[:]) || grant.Private != private || grant.OriginExecutionID != origin || grant.AutomationDepth != depth || grant.ActorID != command.Actor.ID {
		return ctx, &ContractFailure{Type: "permission_denied", Message: "message authorization does not match the frozen command"}
	}
	digest, err := commandDigest(command)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, automationMessageValidationKey{}, automationMessageValidation{Digest: digest, Grant: grant}), nil
}
func (r *Runtime) createAutomationMessage(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
	if _, _, err := automationOwnerProvenance(command); err != nil {
		return nil, err
	}
	proof, ok := ctx.Value(automationMessageValidationKey{}).(automationMessageValidation)
	digest, err := commandDigest(command)
	if !ok || err != nil || proof.Digest != digest {
		return nil, &ContractFailure{Type: "permission_denied", Message: "message owner authorization is required"}
	}

	tx := Transaction(ctx, r.DB)
	if err := repository.LockMessageConversation(tx, command.AccountID, proof.Grant.ConversationID, false); errors.Is(err, repository.ErrMessageConversationDeleted) {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "message conversation was deleted"}
	} else if err != nil {
		return nil, err
	}
	consumed, err := readAutomationMessageConsumption(tx, command.AccountID, proof.Grant.PrepareCommandID)
	if err != nil {
		return nil, err
	}
	if consumed != nil {
		if !automationJSONEqual(consumed.Grant, proof.Grant) {
			return nil, &ContractFailure{Type: "state_conflict", Message: "message authorization was already consumed differently"}
		}
		return consumed.Message, nil
	}
	content, _ := command.Input["content"].(string)
	status := domain.MessageStatusQueued
	if proof.Grant.Private {
		status = domain.MessageStatusSaved
	}
	now := time.Now().UTC()
	message := domain.Message{AccountID: command.AccountID, ConversationID: proof.Grant.ConversationID, SenderType: domain.SenderTypeUser, SenderID: 0, MessageType: domain.MessageTypeOutgoing, ContentType: domain.ContentTypeText, Content: content, Private: proof.Grant.Private, Status: status, EchoID: "automation:" + command.CommandID, CreatedAt: now, UpdatedAt: now}
	if err := Transaction(ctx, r.DB).Create(&message).Error; err != nil {
		return nil, &ContractFailure{Type: "state_conflict", Message: "automation message could not be saved"}
	}
	// Only metadata enters the event stream; private-note bodies stay MSG-owned.
	// A queued row is not provider delivery and no provider command is emitted.
	payload := map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "object_version": proof.Grant.ObjectVersion, "sender_type": message.SenderType, "sender_id": message.SenderID, "message_type": message.MessageType, "content_type": message.ContentType, "private": message.Private, "status": message.Status, "local_outcome": status, "origin_execution_id": proof.Grant.OriginExecutionID, "automation_depth": proof.Grant.AutomationDepth, "conversation_activity_prepared": proof.Grant.PrepareCommandID, "content_hash": proof.Grant.ContentHash}
	if err := r.appendBusinessEvent(ctx, command, "msg.message.created.v1", "Message", strconv.FormatUint(uint64(message.ID), 10), []string{"created"}, payload); err != nil {
		return nil, err
	}

	view := messageFromModel(message)
	raw, err := json.Marshal(automationMessageConsumption{Grant: proof.Grant, Message: &view})
	if err != nil {
		return nil, err
	}
	journal := domain.LocalChangeJournal{ChangeID: automationMessageConsumptionKey(command.AccountID, proof.Grant.PrepareCommandID), SourceModule: "MSG", AccountID: command.AccountID, Action: "automation_message_consumed", ObjectType: "Message", ObjectID: message.ID, AfterState: string(raw), CommandID: command.CommandID, CorrelationID: command.CorrelationID, OccurredAt: now, CreatedAt: now, Result: "applied", Status: "published", DataClassification: domain.ClassificationInternal, RetentionClass: domain.RetentionBusinessChange}
	if err := tx.Create(&journal).Error; err != nil {
		return nil, err
	}
	return view, nil
}

type automationMessageConsumption struct {
	Grant   automationMessageAuthorizationView `json:"grant"`
	Message *messageView                       `json:"message"`
}

func automationMessageConsumptionKey(account uint, prepare string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", account, prepare)))
	return "msg-grant-" + hex.EncodeToString(sum[:])[:48]
}
func readAutomationMessageConsumption(db *gorm.DB, account uint, prepare string) (*automationMessageConsumption, error) {
	var journal domain.LocalChangeJournal
	if err := db.Where("change_id = ? AND source_module = ? AND account_id = ? AND action = ?", automationMessageConsumptionKey(account, prepare), "MSG", account, "automation_message_consumed").First(&journal).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var value automationMessageConsumption
	if json.Unmarshal([]byte(journal.AfterState), &value) != nil || value.Message == nil || value.Grant.AccountID != account || value.Grant.PrepareCommandID != prepare || value.Message.ID != journal.ObjectID || value.Message.AccountID != account || value.Message.ConversationID != value.Grant.ConversationID || value.Message.Private != value.Grant.Private {
		return nil, &ContractFailure{Type: "state_conflict", Message: "message consumption receipt is invalid"}
	}
	hash := sha256.Sum256([]byte(value.Message.Content))
	status := domain.MessageStatusQueued
	if value.Grant.Private {
		status = domain.MessageStatusSaved
	}
	if value.Grant.ObjectVersion < 2 || value.Grant.OriginExecutionID == 0 || value.Grant.AutomationDepth == 0 || value.Grant.AutomationDepth > automationMaxDepth || value.Grant.ActorID == "" || value.Grant.ContentHash != hex.EncodeToString(hash[:]) || value.Message.ID == 0 || value.Message.Status != status || value.Message.MessageType != domain.MessageTypeOutgoing || value.Message.SenderType != domain.SenderTypeUser || value.Message.SenderID != 0 || value.Message.Deleted || journal.Result != "applied" || journal.Status != "published" {
		return nil, &ContractFailure{Type: "state_conflict", Message: "message consumption receipt is invalid"}
	}
	return &value, nil
}
