package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrConversationVersionConflict = errors.New("conversation has been modified; reload before updating")

// SetAutomationCapture is opt-in: legacy callers need no journal migration.
// Enabling capture makes a journal failure fail the owner mutation atomically.
func (r *ConversationRepository) AutomationCaptureEnabled() bool { return r.automationCapture.Load() }

func (r *ConversationRepository) SetAutomationCapture(enabled bool) {
	if enabled {
		r.automationCaptureStarted.CompareAndSwap(0, time.Now().UTC().UnixNano())
	}
	r.automationCapture.Store(enabled)
}

func (r *ConversationRepository) updateWithAutomation(accountID, id uint, expectedVersion *int, updates map[string]any) (*domain.Conversation, error) {
	if accountID == 0 || id == 0 {
		return nil, gorm.ErrInvalidData
	}
	var current domain.Conversation
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("account_id = ? AND id = ? AND status <> ?", accountID, id, domain.ConversationStatusDeleting).
			First(&current).Error; err != nil {
			return err
		}
		if expectedVersion != nil && current.ObjectVersion != *expectedVersion {
			return ErrConversationVersionConflict
		}
		return r.applyConversationAutomationUpdate(tx, &current, updates, nil)
	})
	if err != nil {
		return nil, err
	}
	return &current, nil
}

// Call only after taking the conversation's row lock in the same transaction.
func (r *ConversationRepository) applyConversationAutomationUpdate(tx *gorm.DB, current *domain.Conversation, updates map[string]any, changed []string) error {
	statusBefore := current.Status
	if changed == nil {
		changed = make([]string, 0, len(updates))
		for field := range updates {
			changed = append(changed, field)
		}
	}
	sort.Strings(changed)
	updates["object_version"] = current.ObjectVersion + 1
	updates["updated_at"] = time.Now().UTC()
	result := tx.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ? AND object_version = ?", current.AccountID, current.ID, current.ObjectVersion).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrConversationVersionConflict
	}
	if err := tx.Where("account_id = ? AND id = ?", current.AccountID, current.ID).First(current).Error; err != nil {
		return err
	}
	return r.captureAutomationFactWithTransition(tx, current, false, changed, "", statusBefore)
}

func (r *ConversationRepository) changeLabelWithAutomation(accountID, conversationID, labelID uint, attach bool) error {
	if accountID == 0 || conversationID == 0 || labelID == 0 {
		return gorm.ErrInvalidData
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var current domain.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ? AND status <> ?", accountID, conversationID, domain.ConversationStatusDeleting).First(&current).Error; err != nil {
			return err
		}
		relation := domain.ConversationLabel{ConversationID: conversationID, LabelID: labelID}
		var result *gorm.DB
		if attach {
			result = tx.Where(relation).FirstOrCreate(&relation)
		} else {
			result = tx.Where("conversation_id = ? AND label_id = ?", conversationID, labelID).Delete(&domain.ConversationLabel{})
		}
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return r.applyConversationAutomationUpdate(tx, &current, map[string]any{"last_activity_at": time.Now().UTC()}, []string{"labels"})
	})
}

func (r *ConversationRepository) mergeAttributesWithAutomation(accountID, id uint, attrs map[string]any) (map[string]any, error) {
	merged := make(map[string]any)
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var current domain.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ? AND status <> ?", accountID, id, domain.ConversationStatusDeleting).First(&current).Error; err != nil {
			return err
		}
		if current.CustomAttributes != "" {
			_ = unmarshalCustomAttributeObject(current.CustomAttributes, &merged)
		}
		if merged == nil {
			merged = make(map[string]any)
		}
		for key, value := range attrs {
			merged[key] = value
		}
		payload, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		return r.applyConversationAutomationUpdate(tx, &current, map[string]any{
			"custom_attributes": string(payload), "last_activity_at": time.Now().UTC(),
		}, []string{"custom_attributes"})
	})
	if err != nil {
		return nil, err
	}
	return merged, nil
}

func (r *ConversationRepository) captureAutomationFact(tx *gorm.DB, conversation *domain.Conversation, created bool, changed []string) error {
	return r.captureAutomationFactWithWorkflow(tx, conversation, created, changed, "")
}

func (r *ConversationRepository) captureAutomationFactWithWorkflow(tx *gorm.DB, conversation *domain.Conversation, created bool, changed []string, workflowID string) error {
	return r.captureAutomationFactWithTransition(tx, conversation, created, changed, workflowID, "")
}

func (r *ConversationRepository) captureAutomationFactWithTransition(tx *gorm.DB, conversation *domain.Conversation, created bool, changed []string, workflowID, statusBefore string) error {
	if !r.automationCapture.Load() {
		return nil
	}
	if _, err := r.conversationAutomationActivation(tx); err != nil {
		return err
	}
	eventType, eventName := "con.conversation.changed.v1", "conversation_updated"
	if created {
		eventType, eventName = "con.conversation.created.v1", "conversation_created"
	}
	now, eventID := time.Now().UTC(), foundation.GenerateUUID()
	event := foundation.EventEnvelope{
		EventID: eventID, EventType: eventType, EventVersion: "v1", SourceModule: "CON",
		AccountID: conversation.AccountID, AggregateType: "Conversation",
		AggregateID: strconv.FormatUint(uint64(conversation.ID), 10), AggregateVersion: uint64(conversation.ObjectVersion),
		OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "CON", AuthSource: "module"},
		CorrelationID: eventID, CausationID: eventID, ChangedFields: changed,
		Payload:            map[string]any{"conversation_id": conversation.ID, "object_version": conversation.ObjectVersion, "event_name": eventName},
		DataClassification: domain.ClassificationInternal,
	}
	// A status alias belongs to this same immutable owner fact and owner version.
	// Creation and repeated writes do not manufacture opened/resolved transitions.
	if !created && statusBefore != conversation.Status && conversationAutomationStatus(statusBefore) && conversationAutomationStatus(conversation.Status) {
		event.Payload["status_before"] = statusBefore
		event.Payload["status_after"] = conversation.Status
	}
	if workflowID != "" {
		event.Payload["creation_workflow_id"] = workflowID
	}
	if err := event.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return NewJournalRepository(tx).RecordLocalChange(tx, &domain.LocalChangeJournal{
		ChangeID: eventID, SourceModule: "CON", AccountID: conversation.AccountID,
		ObjectType: "Conversation", ObjectID: conversation.ID, ObjectDisplayID: event.AggregateID,
		ObjectVersionBefore: conversation.ObjectVersion - 1, ObjectVersionAfter: conversation.ObjectVersion,
		Action: "automation_trigger", ChangedFields: strings.Join(changed, ","), AfterState: string(payload),
		ActorType: event.Actor.Type, SourceType: event.EventType, EventID: eventID,
		CorrelationID: eventID, CausationID: eventID, OccurredAt: now, Result: "pending", Status: "pending",
	})
}

// PendingAutomationFacts reads only CON-owned conversation facts. Runtime
// outbox facts retain their envelope (including automation ancestry) verbatim.
// A separate CON receipt keeps this consumer independent of external fanout.
func (r *ConversationRepository) PendingAutomationFacts(ctx context.Context, limit int) ([]foundation.EventEnvelope, error) {
	facts, _, err := r.PendingAutomationFactPage(ctx, limit, "")
	return facts, err
}

// PendingAutomationFactPage keeps one immutable local-journal horizon across
// pages. Its opaque cursor is pass-local, so blocked ancestry does not hide later
// facts or mutate another bridge worker's traversal state.
func (r *ConversationRepository) PendingAutomationFactPage(ctx context.Context, limit int, cursor string) ([]foundation.EventEnvelope, string, error) {
	facts := make([]foundation.EventEnvelope, 0)
	if !r.automationCapture.Load() {
		return facts, "", nil
	}
	var enabledAt time.Time
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		enabledAt, err = r.conversationAutomationActivation(tx)
		return err
	}); err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	scan, err := beginAutomationFactScan(ctx, r.db, "CON", 1, cursor)
	if err != nil {
		return nil, "", err
	}
	for len(facts) < limit && scan.Horizon > 0 {
		var records []domain.LocalChangeJournal
		err := r.db.WithContext(ctx).Table("local_change_journals AS fact").
			Where("fact.source_module = ? AND fact.object_type = ? AND fact.action IN ? AND fact.id > ? AND fact.id <= ?", "CON", "Conversation", []string{"automation_trigger", "event_outbox"}, scan.AfterID, scan.Horizon).
			Where(`NOT EXISTS (SELECT 1 FROM local_change_journals AS receipt
				WHERE receipt.source_module = fact.source_module AND receipt.account_id = fact.account_id
				AND receipt.action = ? AND receipt.event_id = fact.event_id AND receipt.object_type = fact.object_type)`, "automation_delivery").
			Where(`NOT EXISTS (SELECT 1 FROM local_change_journals AS quarantine
				WHERE quarantine.source_module = fact.source_module AND quarantine.account_id = fact.account_id
				AND quarantine.action = ? AND quarantine.entity_type = ? AND quarantine.entity_id = fact.id AND quarantine.command_id = fact.change_id)`, "automation_quarantine", "LocalChangeJournal").
			Order("fact.id ASC").Limit(100).Find(&records).Error
		if err != nil {
			return nil, "", err
		}
		if len(records) == 0 {
			break
		}
		for _, record := range records {
			scan.AfterID = record.ID
			if record.Action == "event_outbox" && record.OccurredAt.Before(enabledAt) {
				continue
			}
			event, supported, err := conversationAutomationEnvelope(record)
			if err != nil {
				if err := r.quarantineConversationAutomationFact(ctx, record); err != nil {
					return nil, "", err
				}
				continue
			}
			if supported && !event.OccurredAt.After(time.Now().UTC()) {
				if value, present := event.Payload["creation_workflow_id"]; present {
					workflowID, ok := value.(string)
					if !ok || workflowID == "" || event.EventType != "con.conversation.created.v1" {
						if err := r.quarantineConversationAutomationFact(ctx, record); err != nil {
							return nil, "", err
						}
						continue
					}
					target, ready, err := r.creationWorkflowTarget(ctx, record.AccountID, record.ObjectID, workflowID, record.ObjectVersionAfter)
					if errors.Is(err, gorm.ErrInvalidData) || errors.Is(err, gorm.ErrRecordNotFound) {
						if err := r.quarantineConversationAutomationFact(ctx, record); err != nil {
							return nil, "", err
						}
						continue
					}
					if err != nil {
						return nil, "", err
					}
					if !ready {
						continue
					}
					event.Payload["object_version"] = target
				}
				facts = append(facts, event)
				if len(facts) == limit {
					return facts, scan.next(1), nil
				}
			}
		}
	}
	return facts, "", nil
}

func conversationAutomationEnvelope(record domain.LocalChangeJournal) (foundation.EventEnvelope, bool, error) {
	var event foundation.EventEnvelope
	if err := json.Unmarshal([]byte(record.AfterState), &event); err != nil {
		return event, false, fmt.Errorf("invalid CON automation fact %d: %w", record.ID, err)
	}
	if event.EventType == "" || event.SourceModule == "" {
		return event, false, fmt.Errorf("invalid CON automation fact %d: missing event identity", record.ID)
	}
	supported := event.EventType == "con.conversation.changed.v1" ||
		(record.Action == "event_outbox" && event.EventType == "con.conversation_status.changed.v1") ||
		(record.Action == "automation_trigger" && event.EventType == "con.conversation.created.v1")
	if !supported {
		if record.Action == "automation_trigger" {
			return event, false, fmt.Errorf("invalid CON automation trigger %d: unsupported event type", record.ID)
		}
		return event, false, nil
	}
	id, err := strconv.ParseUint(event.AggregateID, 10, 64)
	payloadID, validID := conversationAutomationPayloadNumber(event.Payload["conversation_id"])
	objectVersion, validVersion := conversationAutomationPayloadNumber(event.Payload["object_version"])
	if !validID || payloadID != id || !validVersion {
		return event, false, fmt.Errorf("invalid CON automation fact %d: invalid owner snapshot identity", record.ID)
	}
	if record.Action == "automation_trigger" {
		name := "conversation_updated"
		if event.EventType == "con.conversation.created.v1" {
			name = "conversation_created"
		}
		if objectVersion != event.AggregateVersion || event.Payload["event_name"] != name {
			return event, false, fmt.Errorf("invalid CON automation trigger %d: owner snapshot version or event name mismatch", record.ID)
		}
	}
	// Runtime AggregateVersion is the event stream sequence, whereas its
	// payload.object_version is the atomic owner-state snapshot. They may differ.
	if err != nil || id == 0 || event.Validate() != nil || event.SourceModule != "CON" ||
		event.AggregateType != "Conversation" || event.AccountID != record.AccountID || event.EventID != record.EventID ||
		event.EventID != record.ChangeID || event.AggregateVersion != uint64(record.ObjectVersionAfter) ||
		(record.ObjectID != 0 && uint64(record.ObjectID) != id) || (record.ObjectDisplayID != "" && record.ObjectDisplayID != event.AggregateID) {
		return event, false, fmt.Errorf("invalid CON automation fact %d: envelope does not match owner record", record.ID)
	}
	if !conversationAutomationTransitionValid(event) {
		return event, false, fmt.Errorf("invalid CON automation fact %d: invalid status transition", record.ID)
	}
	return event, true, nil
}

func conversationAutomationStatus(status string) bool {
	switch status {
	case domain.ConversationStatusOpen, domain.ConversationStatusPending, domain.ConversationStatusResolved, domain.ConversationStatusSnoozed:
		return true
	default:
		return false
	}
}

func conversationAutomationTransitionValid(event foundation.EventEnvelope) bool {
	before, hasBefore := event.Payload["status_before"]
	after, hasAfter := event.Payload["status_after"]
	if !hasBefore && !hasAfter {
		return true // Facts captured before transition support stay replayable.
	}
	beforeStatus, beforeOK := before.(string)
	afterStatus, afterOK := after.(string)
	if !hasBefore || !hasAfter || !beforeOK || !afterOK || beforeStatus == afterStatus || !conversationAutomationStatus(beforeStatus) || !conversationAutomationStatus(afterStatus) || event.EventType == "con.conversation.created.v1" {
		return false
	}
	for _, field := range event.ChangedFields {
		if field == "status" {
			return true
		}
	}
	return false
}

func (r *ConversationRepository) MarkAutomationFactDelivered(ctx context.Context, eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return gorm.ErrInvalidData
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record domain.LocalChangeJournal
		if err := tx.Where("source_module = ? AND object_type = ? AND action IN ? AND event_id = ? AND change_id = ?", "CON", "Conversation", []string{"automation_trigger", "event_outbox"}, eventID, eventID).First(&record).Error; err != nil {
			return err
		}
		event, supported, err := conversationAutomationEnvelope(record)
		if err != nil {
			return err
		}
		if !supported {
			return gorm.ErrInvalidData
		}
		digest := sha256.Sum256([]byte("CON:automation_delivery:" + eventID))
		now := time.Now().UTC()
		receipt := domain.LocalChangeJournal{
			ChangeID: hex.EncodeToString(digest[:]), SourceModule: "CON", AccountID: event.AccountID,
			ObjectType: "Conversation", ObjectID: record.ObjectID, ObjectDisplayID: event.AggregateID,
			ObjectVersionAfter: int(event.AggregateVersion), Action: "automation_delivery", SourceType: "AUT",
			ActorType: "System", EventID: eventID, CorrelationID: event.CorrelationID, CausationID: event.CausationID,
			OccurredAt: event.OccurredAt, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "applied", Status: "delivered",
		}
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&receipt).Error
	})
}

// The initial activation is durable, so restarting the bridge neither replays
// historical legacy outbox records nor drops pending facts from its last run.
func (r *ConversationRepository) conversationAutomationActivation(tx *gorm.DB) (time.Time, error) {
	const markerID = "con-automation-activation-v1"
	var record domain.LocalChangeJournal
	err := tx.Where("change_id = ? AND source_module = ? AND action = ?", markerID, "CON", "automation_activation").First(&record).Error
	if err == nil {
		return record.OccurredAt, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, err
	}
	started := r.automationCaptureStarted.Load()
	if started == 0 {
		return time.Time{}, gorm.ErrInvalidData
	}
	now := time.Now().UTC()
	record = domain.LocalChangeJournal{
		ChangeID: markerID, SourceModule: "CON", ObjectType: "Conversation", Action: "automation_activation",
		ActorType: "System", SourceType: "AUT", OccurredAt: time.Unix(0, started).UTC(),
		RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "applied", Status: "published",
	}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&record).Error; err != nil {
		return time.Time{}, err
	}
	if err := tx.Where("change_id = ? AND source_module = ? AND action = ?", markerID, "CON", "automation_activation").First(&record).Error; err != nil {
		return time.Time{}, err
	}
	return record.OccurredAt, nil
}

// AutomationMessageActivityVersion returns the durable CON version established
// by one MSG activity delivery, never the conversation's later mutable version.
func (r *ConversationRepository) AutomationMessageActivityVersion(accountID, conversationID uint, messageChangeID string) (int, error) {
	if accountID == 0 || conversationID == 0 || strings.TrimSpace(messageChangeID) == "" {
		return 0, gorm.ErrInvalidData
	}
	changeID := conversationAutomationMessageActivityID(messageChangeID)
	var record domain.LocalChangeJournal
	err := r.db.Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ? AND source_type = ? AND command_id = ?",
		changeID, "CON", accountID, "Conversation", conversationID, "update", "msg.message.created.v1", messageChangeID).First(&record).Error
	if err != nil {
		return 0, err
	}
	if record.ObjectVersionAfter < 1 {
		return 0, gorm.ErrInvalidData
	}
	if record.AfterState != "" {
		var receipt struct {
			ExpectedVersion int `json:"expected_version"`
			TargetVersion   int `json:"automation_target_version"`
		}
		if json.Unmarshal([]byte(record.AfterState), &receipt) != nil || receipt.ExpectedVersion < 1 || receipt.TargetVersion < 1 {
			return 0, gorm.ErrInvalidData
		}
		if record.Reason == "reopen_version_stale" || record.Reason == "creation_version_stale" {
			if receipt.TargetVersion != receipt.ExpectedVersion || receipt.ExpectedVersion >= record.ObjectVersionAfter {
				return 0, gorm.ErrInvalidData
			}
			return receipt.TargetVersion, nil
		}
		if receipt.ExpectedVersion != record.ObjectVersionBefore || receipt.TargetVersion != record.ObjectVersionAfter {
			return 0, gorm.ErrInvalidData
		}
	}
	return record.ObjectVersionAfter, nil
}

func conversationAutomationMessageActivityID(messageChangeID string) string {
	changeID := "con-activity-" + messageChangeID
	if len(changeID) > 64 {
		digest := sha256.Sum256([]byte(changeID))
		changeID = hex.EncodeToString(digest[:])
	}
	return changeID
}

// Poison records remain intact for repair. The quarantine names the exact
// source journal row, so a corrupt event ID cannot suppress another fact.
func (r *ConversationRepository) quarantineConversationAutomationFact(ctx context.Context, record domain.LocalChangeJournal) error {
	identity := fmt.Sprintf("CON:automation_quarantine:%d:%d:%s", record.AccountID, record.ID, record.ChangeID)
	digest := sha256.Sum256([]byte(identity))
	now := time.Now().UTC()
	quarantine := domain.LocalChangeJournal{
		ChangeID: hex.EncodeToString(digest[:]), SourceModule: "CON", AccountID: record.AccountID,
		EntityType: "LocalChangeJournal", EntityID: record.ID,
		ObjectType: "Conversation", ObjectID: record.ObjectID, ObjectDisplayID: record.ObjectDisplayID,
		Action: "automation_quarantine", SourceType: "AUT", ActorType: "System",
		CommandID: record.ChangeID, EventID: record.EventID, Reason: "invalid_automation_fact",
		CorrelationID: record.CorrelationID, CausationID: record.CausationID,
		OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now,
		Result: "invalid_automation_fact", Status: "manual_action_required",
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&quarantine).Error
}

// JSON decoding produces float64; accept only exact positive integers. This
// rejects truncated/negative/fractional snapshot identities before bridge intake.
func conversationAutomationPayloadNumber(value any) (uint64, bool) {
	number, ok := value.(float64)
	if !ok || number <= 0 || number > 1<<53 || math.Trunc(number) != number {
		return 0, false
	}
	return uint64(number), true
}
