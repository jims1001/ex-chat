package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CreationWorkflowPlan struct {
	WorkflowID     string           `json:"workflow_id"`
	AccountID      uint             `json:"account_id"`
	ConversationID uint             `json:"conversation_id"`
	CreatedVersion int              `json:"created_version"`
	Messages       []domain.Message `json:"messages"`
}

// The persisted JSON stays unchanged when consumed by the application coordinator.
type conversationCreationPlan = CreationWorkflowPlan

type CreationWorkflowRef struct {
	AccountID      uint
	ConversationID uint
	WorkflowID     string
}

type conversationCreationStep struct {
	ChangeID  string `json:"change_id"`
	MessageID uint   `json:"message_id"`
	Before    int    `json:"before"`
	After     int    `json:"after"`
	Target    int    `json:"target"`
}

type conversationCreationCompletion struct {
	PlanDigest    string                     `json:"plan_digest"`
	TargetVersion int                        `json:"target_version"`
	Steps         []conversationCreationStep `json:"steps"`
}

func creationPlanDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func creationCompletionID(workflowID string) string {
	return "con-create-done-" + workflowID
}

// CreateWithInitialMessages is an internal owner workflow, not a request DTO.
// Its planned messages replace the legacy local create-before-callback steps.
func (r *ConversationRepository) CreateWithInitialMessages(c *domain.Conversation, messages []domain.Message) error {
	if !r.AutomationCaptureEnabled() || c == nil || c.ID != 0 || len(messages) > 2 {
		return gorm.ErrInvalidData
	}
	for _, msg := range messages {
		if msg.ID != 0 || msg.ConversationID != 0 || (msg.AccountID != 0 && msg.AccountID != c.AccountID) || !msg.CreatedAt.IsZero() || !msg.UpdatedAt.IsZero() || msg.Deleted || msg.DeletedAt != nil || msg.EditedAt != nil || msg.Sender != nil {
			return gorm.ErrInvalidData
		}
	}
	return r.createWithInitialMessages(c, messages)
}

func (r *ConversationRepository) recordCreationPlan(tx *gorm.DB, c *domain.Conversation, messages []domain.Message) (string, error) {
	plan := conversationCreationPlan{WorkflowID: foundation.GenerateUUID(), AccountID: c.AccountID, ConversationID: c.ID, CreatedVersion: c.ObjectVersion, Messages: append([]domain.Message(nil), messages...)}
	for i := range plan.Messages {
		plan.Messages[i].AccountID = c.AccountID
		plan.Messages[i].ConversationID = c.ID
		if plan.Messages[i].MessageType == "" {
			plan.Messages[i].MessageType = domain.MessageTypeIncoming
		}
		if plan.Messages[i].ContentType == "" {
			plan.Messages[i].ContentType = domain.ContentTypeText
		}
		if plan.Messages[i].Status == "" {
			plan.Messages[i].Status = domain.MessageStatusSent
		}
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	row := domain.LocalChangeJournal{ChangeID: plan.WorkflowID, SourceModule: "CON", AccountID: c.AccountID, ObjectType: "Conversation", ObjectID: c.ID, ObjectVersionAfter: c.ObjectVersion, Action: "creation_workflow", AfterState: string(raw), IdempotencyKeyHash: creationPlanDigest(string(raw)), ActorType: "system", SourceType: "conversation_creation", Status: "pending", Result: "pending"}
	if err := NewJournalRepository(tx).RecordLocalChange(tx, &row); err != nil {
		return "", err
	}
	return plan.WorkflowID, nil
}

func validateCreationPlan(row domain.LocalChangeJournal) (conversationCreationPlan, error) {
	var plan conversationCreationPlan
	if json.Unmarshal([]byte(row.AfterState), &plan) != nil || row.SourceModule != "CON" || row.ObjectType != "Conversation" || row.Action != "creation_workflow" || plan.WorkflowID != row.ChangeID || len(plan.WorkflowID) != 32 || plan.AccountID != row.AccountID || plan.ConversationID != row.ObjectID || plan.CreatedVersion != row.ObjectVersionAfter || plan.CreatedVersion != 1 || len(plan.Messages) == 0 || len(plan.Messages) > 2 || row.IdempotencyKeyHash != creationPlanDigest(row.AfterState) {
		return plan, gorm.ErrInvalidData
	}
	if _, err := hex.DecodeString(plan.WorkflowID); err != nil {
		return plan, gorm.ErrInvalidData
	}
	for _, msg := range plan.Messages {
		if msg.AccountID != plan.AccountID || msg.ConversationID != plan.ConversationID || msg.ID != 0 || !msg.CreatedAt.IsZero() || !msg.UpdatedAt.IsZero() || msg.Deleted || msg.DeletedAt != nil || msg.EditedAt != nil || msg.Sender != nil {
			return plan, gorm.ErrInvalidData
		}
	}
	return plan, nil
}

func (r *ConversationRepository) loadCreationPlan(ctx context.Context, accountID, conversationID uint, workflowID string) (conversationCreationPlan, error) {
	var row domain.LocalChangeJournal
	if err := r.db.WithContext(ctx).Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ?", workflowID, "CON", accountID, "Conversation", conversationID, "creation_workflow").First(&row).Error; err != nil {
		return conversationCreationPlan{}, err
	}
	return validateCreationPlan(row)
}

// ValidateCreationWorkflowMessage binds the entire normalized MSG command to
// a preexisting owner plan before MSG is allowed to suppress a standalone fact.
func (r *ConversationRepository) ValidateCreationWorkflowMessage(ctx context.Context, workflowID string, step int, msg *domain.Message, expectedVersion int) error {
	if msg == nil {
		return gorm.ErrInvalidData
	}
	plan, err := r.loadCreationPlan(ctx, msg.AccountID, msg.ConversationID, workflowID)
	if err != nil {
		return err
	}
	if step < 0 || step >= len(plan.Messages) || expectedVersion != plan.CreatedVersion+step {
		return ErrCreationWorkflowMessageConflict
	}
	normalize := func(value domain.Message) domain.Message {
		if value.MessageType == "" {
			value.MessageType = domain.MessageTypeIncoming
		}
		if value.ContentType == "" {
			value.ContentType = domain.ContentTypeText
		}
		if value.Status == "" {
			value.Status = domain.MessageStatusSent
		}
		return value
	}
	want, err := json.Marshal(normalize(plan.Messages[step]))
	if err != nil {
		return err
	}
	got, err := json.Marshal(normalize(*msg))
	if err != nil {
		return err
	}
	if string(want) != string(got) {
		return ErrCreationWorkflowMessageConflict
	}
	return nil
}

func (r *ConversationRepository) creationStepReceipt(ctx context.Context, plan conversationCreationPlan, step int) (domain.LocalChangeJournal, int, error) {
	var receipt domain.LocalChangeJournal
	key := creationMessageChangeID(plan.WorkflowID, step)
	if err := r.db.WithContext(ctx).Where("source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ? AND source_type = ? AND command_id = ?", "CON", plan.AccountID, "Conversation", plan.ConversationID, "update", "msg.message.created.v1", key).First(&receipt).Error; err != nil {
		return receipt, 0, err
	}
	var state struct {
		messageReopenIntent
		Target int `json:"automation_target_version"`
	}
	expectedReceiptID := "con-activity-" + key
	if len(expectedReceiptID) > 64 {
		expectedReceiptID = creationPlanDigest(expectedReceiptID)
	}
	if receipt.ChangeID != expectedReceiptID || receipt.ActorID != plan.Messages[step].SenderID || receipt.CausationID != key || json.Unmarshal([]byte(receipt.AfterState), &state) != nil || state.CreationWorkflowID != plan.WorkflowID || state.CreationWorkflowStep != step || state.ExpectedVersion != plan.CreatedVersion+step || state.Reopen || receipt.ObjectVersionAfter != receipt.ObjectVersionBefore+1 {
		return receipt, 0, gorm.ErrInvalidData
	}
	if receipt.Reason == "creation_version_stale" {
		if receipt.ObjectVersionBefore <= state.ExpectedVersion || state.Target != state.ExpectedVersion {
			return receipt, 0, gorm.ErrInvalidData
		}
	} else if state.ExpectedVersion != receipt.ObjectVersionBefore || state.Target != receipt.ObjectVersionAfter {
		return receipt, 0, gorm.ErrInvalidData
	}
	return receipt, state.Target, nil
}

// ReadCreationWorkflow returns a validated immutable CON plan. It performs no
// cross-owner work; absence means the conversation has no planned local messages.
func (r *ConversationRepository) ReadCreationWorkflow(ctx context.Context, accountID, conversationID uint) (*CreationWorkflowPlan, error) {
	var rows []domain.LocalChangeJournal
	if err := r.db.WithContext(ctx).Where("source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ?", "CON", accountID, "Conversation", conversationID, "creation_workflow").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, gorm.ErrInvalidData
	}
	plan, err := validateCreationPlan(rows[0])
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

// FinalizeCreationWorkflow proves completion exclusively from CON's own
// activity receipts. The application coordinator supplies MSG identities only;
// it cannot supply or rebase an owner version.
func (r *ConversationRepository) FinalizeCreationWorkflow(ctx context.Context, accountID, conversationID uint, workflowID string, messageIDs []uint) error {
	var row domain.LocalChangeJournal
	if err := r.db.WithContext(ctx).Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ?", workflowID, "CON", accountID, "Conversation", conversationID, "creation_workflow").First(&row).Error; err != nil {
		return err
	}
	plan, err := validateCreationPlan(row)
	if err != nil {
		return err
	}
	if len(messageIDs) != len(plan.Messages) {
		return gorm.ErrInvalidData
	}
	completion := conversationCreationCompletion{PlanDigest: row.IdempotencyKeyHash, TargetVersion: plan.CreatedVersion + len(plan.Messages)}
	for i := range plan.Messages {
		if messageIDs[i] == 0 {
			return gorm.ErrInvalidData
		}
		receipt, target, err := r.creationStepReceipt(ctx, plan, i)
		if err != nil {
			return err
		}
		if receipt.Reason == "creation_version_stale" {
			completion.TargetVersion = plan.CreatedVersion
		}
		completion.Steps = append(completion.Steps, conversationCreationStep{ChangeID: receipt.CommandID, MessageID: messageIDs[i], Before: receipt.ObjectVersionBefore, After: receipt.ObjectVersionAfter, Target: target})
	}
	raw, err := json.Marshal(completion)
	if err != nil {
		return err
	}
	done := domain.LocalChangeJournal{ChangeID: creationCompletionID(plan.WorkflowID), SourceModule: "CON", AccountID: accountID, ObjectType: "Conversation", ObjectID: conversationID, ObjectVersionBefore: plan.CreatedVersion, ObjectVersionAfter: completion.TargetVersion, Action: "creation_workflow_completed", CommandID: plan.WorkflowID, AfterState: string(raw), IdempotencyKeyHash: row.IdempotencyKeyHash, IntegrityHash: creationPlanDigest(string(raw)), SourceType: "conversation_creation", ActorType: "system", Status: "delivered", Result: "applied", OccurredAt: time.Now().UTC()}
	if completion.TargetVersion == plan.CreatedVersion {
		done.Reason = "creation_version_stale"
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&done).Error; err != nil {
			return err
		}
		var stored domain.LocalChangeJournal
		if err := tx.Where("change_id = ?", done.ChangeID).First(&stored).Error; err != nil {
			return err
		}
		if stored.AccountID != accountID || stored.ObjectID != conversationID || stored.Action != done.Action || stored.AfterState != done.AfterState || stored.CommandID != done.CommandID {
			return gorm.ErrInvalidData
		}
		return tx.Model(&domain.LocalChangeJournal{}).Where("id = ? AND status = ?", row.ID, "pending").Updates(map[string]any{"status": "delivered", "result": "applied"}).Error
	})
	return err
}

// PendingCreationWorkflows pages CON-owned references without invoking MSG.
// The rotating cursor is only a fairness hint; durable plan state owns recovery.
func (r *ConversationRepository) PendingCreationWorkflows(ctx context.Context, limit int) ([]CreationWorkflowRef, error) {
	if !r.AutomationCaptureEnabled() {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	r.creationReconcileMu.Lock()
	defer r.creationReconcileMu.Unlock()
	var rows []domain.LocalChangeJournal
	query := func(after uint) *gorm.DB {
		return r.db.WithContext(ctx).Where("source_module = ? AND action = ? AND status = ? AND id > ?", "CON", "creation_workflow", "pending", after).Order("id ASC").Limit(limit)
	}
	if err := query(r.creationReconcileID).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 && r.creationReconcileID != 0 {
		r.creationReconcileID = 0
		if err := query(0).Find(&rows).Error; err != nil {
			return nil, err
		}
	}
	if len(rows) > 0 {
		r.creationReconcileID = rows[len(rows)-1].ID
	}
	refs := make([]CreationWorkflowRef, 0, len(rows))
	for _, row := range rows {
		refs = append(refs, CreationWorkflowRef{AccountID: row.AccountID, ConversationID: row.ObjectID, WorkflowID: row.ChangeID})
	}
	return refs, nil
}

// RejectCreationWorkflow quarantines a permanent plan/step failure. The owner
// itself decides whether the target was deleted, without consulting other stores.
func (r *ConversationRepository) RejectCreationWorkflow(ctx context.Context, ref CreationWorkflowRef) error {
	status, reason := "manual_action_required", "invalid_creation_workflow"
	exists, err := r.MessageTargetExists(ref.AccountID, ref.ConversationID)
	if err != nil {
		return err
	}
	if !exists {
		status, reason = "cancelled", "target_unavailable"
	}
	return r.db.WithContext(ctx).Model(&domain.LocalChangeJournal{}).Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ? AND status = ?", ref.WorkflowID, "CON", ref.AccountID, "Conversation", ref.ConversationID, "creation_workflow", "pending").Updates(map[string]any{"status": status, "result": reason}).Error
}

func (r *ConversationRepository) creationWorkflowTarget(ctx context.Context, accountID, conversationID uint, workflowID string, originalVersion int) (int, bool, error) {
	var row domain.LocalChangeJournal
	if err := r.db.WithContext(ctx).Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ?", workflowID, "CON", accountID, "Conversation", conversationID, "creation_workflow").First(&row).Error; err != nil {
		return 0, false, err
	}
	plan, err := validateCreationPlan(row)
	if err != nil || plan.CreatedVersion != originalVersion || row.Status == "manual_action_required" {
		return 0, false, gorm.ErrInvalidData
	}
	if row.Status == "cancelled" && row.Result == "target_unavailable" {
		return originalVersion, true, nil
	}
	var done domain.LocalChangeJournal
	err = r.db.WithContext(ctx).Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ? AND command_id = ?", creationCompletionID(workflowID), "CON", accountID, "Conversation", conversationID, "creation_workflow_completed", workflowID).First(&done).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var completion conversationCreationCompletion
	if done.Status != "delivered" || done.Result != "applied" || done.SourceType != "conversation_creation" || done.IntegrityHash != creationPlanDigest(done.AfterState) || json.Unmarshal([]byte(done.AfterState), &completion) != nil || completion.PlanDigest != row.IdempotencyKeyHash || done.IdempotencyKeyHash != row.IdempotencyKeyHash || len(completion.Steps) != len(plan.Messages) || done.ObjectVersionBefore != originalVersion || completion.TargetVersion != done.ObjectVersionAfter {
		return 0, false, gorm.ErrInvalidData
	}
	target := originalVersion + len(plan.Messages)
	for i, step := range completion.Steps {
		receipt, receiptTarget, err := r.creationStepReceipt(ctx, plan, i)
		if err != nil {
			return 0, false, err
		}
		if step.ChangeID != receipt.CommandID || step.MessageID == 0 || step.Before != receipt.ObjectVersionBefore || step.After != receipt.ObjectVersionAfter || step.Target != receiptTarget {
			return 0, false, gorm.ErrInvalidData
		}
		if receipt.Reason == "creation_version_stale" {
			target = originalVersion
		}
	}
	if completion.TargetVersion != target || (target == originalVersion) != (done.Reason == "creation_version_stale") {
		return 0, false, gorm.ErrInvalidData
	}
	return target, true, nil
}
