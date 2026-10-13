package repository

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
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SetAutomationCapture is selected once by application composition before use.
func (r *MessageRepository) SetAutomationCapture(enabled bool) { r.automationCapture = enabled }
func (r *MessageRepository) captureAutomationMessage(tx *gorm.DB, msg *domain.Message, changeID string) error {
	if !r.automationCapture {
		return nil
	}
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: foundation.GenerateUUID(), EventType: "msg.message.created.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: msg.AccountID, AggregateType: "Message", AggregateID: strconv.Itoa(int(msg.ID)), AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "monolith", AuthSource: "module"}, CorrelationID: changeID, CausationID: changeID, ChangedFields: []string{"created"}, Payload: map[string]any{"message_id": msg.ID, "conversation_id": msg.ConversationID, "event_name": "message_created", "message_change_id": changeID, "message_type": msg.MessageType, "private": msg.Private, "sender_type": msg.SenderType, "sender_id": msg.SenderID, "content_type": msg.ContentType}, DataClassification: "internal"}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return NewJournalRepository(tx).RecordLocalChange(tx, &domain.LocalChangeJournal{ChangeID: event.EventID, SourceModule: "MSG", AccountID: msg.AccountID, ObjectType: "Message", ObjectID: msg.ID, ObjectVersionAfter: 1, Action: "automation_trigger", AfterState: string(raw), EventID: event.EventID, CausationID: changeID, Status: "pending", Result: "pending", OccurredAt: now})
}

type messageAutomationActivityPort interface {
	AutomationMessageActivityVersion(uint, uint, string) (int, error)
}

// A transition is optional for older CON adapters. When available, the durable
// activity receipt supplies it; a later live conversation status is not evidence.
type messageAutomationActivityTransitionPort interface {
	AutomationMessageActivityTransition(uint, uint, string) (string, string, error)
}

// PendingAutomationFacts consumes only MSG-owned facts and receipts. Runtime
// outbox state belongs to its dispatcher, so automation acknowledges/quarantines
// it separately. Ordinary facts are selected first: a waiting origin batch must
// not fill the page with descendants and starve an unrelated incoming message.
func (r *MessageRepository) PendingAutomationFacts(ctx context.Context, limit int) ([]foundation.EventEnvelope, error) {
	facts, _, err := r.PendingAutomationFactPage(ctx, limit, "")
	return facts, err
}

// PendingAutomationFactPage scans one fixed owner-journal horizon. The cursor
// carries the ordinary/runtime stage so pending descendants cannot prevent a
// caller from visiting unrelated runtime facts on subsequent pages.
func (r *MessageRepository) PendingAutomationFactPage(ctx context.Context, limit int, cursor string) ([]foundation.EventEnvelope, string, error) {
	if !r.automationCapture {
		return nil, "", nil
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	scan, err := beginAutomationFactScan(ctx, r.db, "MSG", 2, cursor)
	if err != nil {
		return nil, "", err
	}
	events := make([]foundation.EventEnvelope, 0, limit)
	for scan.Stage < 2 && scan.Horizon > 0 {
		action := []string{"automation_trigger", "event_outbox"}[scan.Stage]
		for len(events) < limit {
			var rows []domain.LocalChangeJournal
			query := r.db.WithContext(ctx).Table("local_change_journals AS fact").
				Where("fact.source_module = ? AND fact.action = ? AND fact.id > ? AND fact.id <= ?", "MSG", action, scan.AfterID, scan.Horizon).
				Where(`NOT EXISTS (SELECT 1 FROM local_change_journals AS receipt
					WHERE receipt.source_module = fact.source_module AND receipt.account_id = fact.account_id
					AND receipt.action = ? AND receipt.event_id = fact.event_id)`, "automation_delivery").
				Where(`NOT EXISTS (SELECT 1 FROM local_change_journals AS quarantine
					WHERE quarantine.source_module = fact.source_module AND quarantine.account_id = fact.account_id
					AND quarantine.action = ? AND quarantine.entity_type = ? AND quarantine.entity_id = fact.id AND quarantine.command_id = fact.change_id)`, "automation_quarantine", "LocalChangeJournal")
			if action == "automation_trigger" {
				query = query.Where("fact.status = ?", "pending")
			} else {
				query = query.Where("fact.object_type = ? AND fact.status NOT IN ?", "Message", []string{"cancelled", "superseded"})
			}
			if err := query.Order("fact.id ASC").Limit(100).Find(&rows).Error; err != nil {
				return nil, "", err
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				scan.AfterID = row.ID
				event, supported, err := messageAutomationEnvelope(row)
				if err != nil {
					if err := r.quarantineAutomationMessage(ctx, row); err != nil {
						return nil, "", err
					}
					continue
				}
				if !supported || event.OccurredAt.After(time.Now().UTC()) {
					continue // valid clock-skewed facts remain pending
				}
				var ready bool
				if action == "event_outbox" {
					err = validateAutomationMessageConsumption(r.db.WithContext(ctx), row, event)
					if err == nil {
						// Local deletion may have happened after external fanout. Do not
						// resurrect an already deleted message from its immutable receipt.
						messageID, _ := messageAutomationUint(event.Payload["message_id"])
						conversationID, _ := messageAutomationUint(event.Payload["conversation_id"])
						var msg domain.Message
						err = r.db.WithContext(ctx).Where("account_id = ? AND id = ? AND conversation_id = ? AND deleted = ?", row.AccountID, messageID, conversationID, false).First(&msg).Error
						ready = err == nil
					}
				} else {
					if event.EventType == "msg.message_status.changed.v1" {
						ready, err = r.prepareRetryAutomationMessage(ctx, row, &event)
						if err != nil && !errors.Is(err, gorm.ErrInvalidData) && !errors.Is(err, gorm.ErrRecordNotFound) {
							if ctx.Err() != nil {
								return nil, "", ctx.Err()
							}
							logger.WithComponent("message").Error("retry activity recovery pending", "message_id", row.ObjectID, "error", err.Error())
							// A temporarily unavailable retry owner must not hide unrelated facts
							// later in this fixed-horizon pass. The next pass retries this intent.
							continue
						}
					} else {
						ready, err = r.prepareOrdinaryAutomationMessage(ctx, row, &event)
					}
				}
				if errors.Is(err, gorm.ErrInvalidData) || errors.Is(err, gorm.ErrRecordNotFound) {
					if err := r.quarantineAutomationMessage(ctx, row); err != nil {
						return nil, "", err
					}
					continue
				}
				if err != nil {
					return nil, "", err
				}
				if ready {
					events = append(events, event)
					if len(events) == limit {
						return events, scan.next(2), nil
					}
				}
			}
		}
		scan.Stage++
		scan.AfterID = 0
	}
	return events, "", nil
}

func messageAutomationEnvelope(row domain.LocalChangeJournal) (foundation.EventEnvelope, bool, error) {
	var event foundation.EventEnvelope
	if json.Unmarshal([]byte(row.AfterState), &event) != nil {
		return event, false, gorm.ErrInvalidData
	}
	if event.EventType == "msg.message_status.changed.v1" && row.Action == "automation_trigger" {
		if row.SourceModule != "MSG" || row.ObjectType != "Message" || row.AccountID != event.AccountID || row.EventID != event.EventID || row.ChangeID != event.EventID || row.CausationID != event.CausationID || event.Payload["retry_profile"] != messageRetryProfile || event.Validate() != nil {
			return event, false, gorm.ErrInvalidData
		}
		return event, true, nil
	}
	if row.Action == "event_outbox" {
		if event.EventType != "msg.message.created.v1" {
			return event, false, nil
		}
		_, origin := event.Payload["origin_execution_id"]
		_, depth := event.Payload["automation_depth"]
		_, prepared := event.Payload["conversation_activity_prepared"]
		if !origin && !depth && !prepared && !strings.HasPrefix(event.Actor.ID, "automation:") {
			return event, false, nil // unrelated legacy/runtime message creation
		}
	}
	messageID, idErr := messageAutomationUint(event.Payload["message_id"])
	_, conversationErr := messageAutomationUint(event.Payload["conversation_id"])
	if row.SourceModule != "MSG" || row.ObjectType != "Message" || event.SourceModule != "MSG" || event.EventType != "msg.message.created.v1" || event.EventVersion != "v1" || event.AccountID != row.AccountID || event.EventID != row.ChangeID || event.EventID != row.EventID || event.CausationID != row.CausationID || event.AggregateType != "Message" || idErr != nil || conversationErr != nil || event.AggregateID != strconv.FormatUint(uint64(messageID), 10) || row.ObjectVersionAfter < 1 || event.AggregateVersion != uint64(row.ObjectVersionAfter) || event.Validate() != nil {
		return event, false, gorm.ErrInvalidData
	}
	if row.Action == "automation_trigger" {
		if event.AggregateVersion != 1 || row.ObjectID != messageID || event.Payload["message_change_id"] != row.CausationID || event.Payload["event_name"] != "message_created" || len(event.ChangedFields) != 1 || event.ChangedFields[0] != "created" {
			return event, false, gorm.ErrInvalidData
		}
		// Only a verified activity receipt may enrich an ordinary creation fact
		// with a status transition. Ordinary capture never carries AUT ancestry.
		for _, key := range []string{"status_before", "status_after", "origin_execution_id", "automation_depth", "conversation_activity_prepared"} {
			if _, present := event.Payload[key]; present {
				return event, false, gorm.ErrInvalidData
			}
		}
	} else if row.Action != "event_outbox" || row.ObjectDisplayID != event.AggregateID || (row.ObjectID != 0 && row.ObjectID != messageID) || row.CorrelationID != event.CorrelationID {
		return event, false, gorm.ErrInvalidData
	}
	return event, true, nil
}

func (r *MessageRepository) prepareOrdinaryAutomationMessage(ctx context.Context, row domain.LocalChangeJournal, event *foundation.EventEnvelope) (bool, error) {
	var activity domain.LocalChangeJournal
	if err := r.db.WithContext(ctx).Where("source_module = ? AND account_id = ? AND change_id = ? AND action = ? AND object_type = ? AND object_id = ?", "MSG", row.AccountID, event.CausationID, "create", "Message", row.ObjectID).First(&activity).Error; err != nil {
		return false, err
	}
	if activity.Status == "cancelled" || (activity.Status == "superseded" && activity.Result == "rejected") {
		return false, r.db.WithContext(ctx).Model(&domain.LocalChangeJournal{}).Where("id = ? AND source_module = ?", row.ID, "MSG").Updates(map[string]any{"status": "cancelled", "result": "orphaned_message"}).Error
	}
	if activity.Status != "delivered" {
		return false, nil
	}
	var msg domain.Message
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ? AND deleted = ?", row.AccountID, row.ObjectID, false).First(&msg).Error; err != nil {
		return false, err
	}
	conversationID, _ := messageAutomationUint(event.Payload["conversation_id"])
	if conversationID != msg.ConversationID {
		return false, gorm.ErrInvalidData
	}
	// Older ordinary capture rows did not include these immutable MSG fields.
	// Enrich their returned envelope without rewriting the published source body.
	for key, value := range map[string]any{"message_type": msg.MessageType, "private": msg.Private, "sender_type": msg.SenderType, "sender_id": msg.SenderID, "content_type": msg.ContentType} {
		if existing, present := event.Payload[key]; present {
			left, leftErr := json.Marshal(existing)
			right, rightErr := json.Marshal(value)
			if leftErr != nil || rightErr != nil || string(left) != string(right) {
				return false, gorm.ErrInvalidData
			}
		} else {
			event.Payload[key] = value
		}
	}
	if msg.MessageType != domain.MessageTypeIncoming && msg.MessageType != domain.MessageTypeOutgoing && msg.MessageType != domain.MessageTypeActivity {
		return false, gorm.ErrInvalidData
	}
	owner, ok := r.conversation.(messageAutomationActivityPort)
	if !ok {
		return false, fmt.Errorf("CON automation activity receipt query is unavailable")
	}
	version, err := owner.AutomationMessageActivityVersion(row.AccountID, msg.ConversationID, event.CausationID)
	if err != nil {
		return false, err
	}
	if version < 1 {
		return false, gorm.ErrInvalidData
	}
	event.Payload["object_version"] = version
	if transition, ok := r.conversation.(messageAutomationActivityTransitionPort); ok {
		before, after, err := transition.AutomationMessageActivityTransition(row.AccountID, msg.ConversationID, event.CausationID)
		if err != nil {
			return false, err
		}
		if before != after {
			if before == "" || after == "" {
				return false, gorm.ErrInvalidData
			}
			event.Payload["status_before"], event.Payload["status_after"] = before, after
			if !messageAutomationHasField(event.ChangedFields, "status") {
				event.ChangedFields = append(event.ChangedFields, "status")
			}
		}
	}
	return true, nil
}

func messageAutomationHasField(fields []string, field string) bool {
	for _, value := range fields {
		if value == field {
			return true
		}
	}
	return false
}

// MSG's consumption journal is the immutable source for an action-produced
// message. This local read deliberately does not call CON or AUT and never adopts
// a later conversation version. AUT will prove the originating batch's final
// target before admitting this exact event and its original object_version.
func validateAutomationMessageConsumption(db *gorm.DB, row domain.LocalChangeJournal, event foundation.EventEnvelope) error {
	prepare, prepareOK := event.Payload["conversation_activity_prepared"].(string)
	prepareBytes, prepareErr := hex.DecodeString(prepare)
	version, versionErr := messageAutomationUint(event.Payload["object_version"])
	origin, originErr := messageAutomationUint(event.Payload["origin_execution_id"])
	depth, depthErr := messageAutomationUint(event.Payload["automation_depth"])
	private, privateOK := event.Payload["private"].(bool)
	messageID, _ := messageAutomationUint(event.Payload["message_id"])
	conversationID, _ := messageAutomationUint(event.Payload["conversation_id"])
	senderID, senderErr := messageAutomationNonnegativeUint(event.Payload["sender_id"])
	actorRule, actorErr := strconv.ParseUint(strings.TrimPrefix(event.Actor.ID, "automation:"), 10, 64)
	// A runtime action creates a message only. Reopen transition metadata may
	// be added exclusively to ordinary facts from their CON activity receipt.
	for _, key := range []string{"content", "status_before", "status_after"} {
		if _, present := event.Payload[key]; present {
			return gorm.ErrInvalidData
		}
	}
	if !prepareOK || prepareErr != nil || len(prepareBytes) != sha256.Size || versionErr != nil || version < 2 || originErr != nil || depthErr != nil || depth > 3 || !privateOK || senderErr != nil || senderID != 0 || event.Payload["sender_type"] != domain.SenderTypeUser || event.Payload["message_type"] != domain.MessageTypeOutgoing || event.Payload["content_type"] != domain.ContentTypeText || event.Actor.Type != "System" || event.Actor.AuthSource != "module" || actorErr != nil || actorRule == 0 || event.Actor.ID != "automation:"+strconv.FormatUint(actorRule, 10) || len(event.ChangedFields) != 1 || event.ChangedFields[0] != "created" {
		return gorm.ErrInvalidData
	}
	status := domain.MessageStatusQueued
	if private {
		status = domain.MessageStatusSaved
	}
	if event.Payload["status"] != status || event.Payload["local_outcome"] != status {
		return gorm.ErrInvalidData
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", row.AccountID, prepare)))
	key := "msg-grant-" + hex.EncodeToString(sum[:])[:48]
	var journal domain.LocalChangeJournal
	if err := db.Where("change_id = ? AND source_module = ? AND account_id = ? AND action = ?", key, "MSG", row.AccountID, "automation_message_consumed").First(&journal).Error; err != nil {
		return err
	}
	var receipt struct {
		Grant struct {
			PrepareCommandID  string `json:"prepare_command_id"`
			AccountID         uint   `json:"account_id"`
			ConversationID    uint   `json:"conversation_id"`
			ObjectVersion     uint   `json:"object_version"`
			ContentHash       string `json:"content_hash"`
			Private           *bool  `json:"private"`
			OriginExecutionID uint   `json:"origin_execution_id"`
			AutomationDepth   uint   `json:"automation_depth"`
			ActorID           string `json:"actor_id"`
		} `json:"grant"`
		Message *struct {
			ID             uint   `json:"id"`
			AccountID      uint   `json:"account_id"`
			ConversationID uint   `json:"conversation_id"`
			Content        string `json:"content"`
			ContentType    string `json:"content_type"`
			SenderType     string `json:"sender_type"`
			SenderID       *uint  `json:"sender_id"`
			MessageType    string `json:"message_type"`
			Private        *bool  `json:"private"`
			Status         string `json:"status"`
			Deleted        *bool  `json:"deleted"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(journal.AfterState), &receipt) != nil || receipt.Message == nil {
		return gorm.ErrInvalidData
	}
	grant, msg := receipt.Grant, receipt.Message
	hash := sha256.Sum256([]byte(msg.Content))
	if journal.ObjectType != "Message" || journal.ObjectID != messageID || journal.CommandID != event.CausationID || journal.CorrelationID != event.CorrelationID || journal.Status != "published" || journal.Result != "applied" || grant.PrepareCommandID != prepare || grant.AccountID != row.AccountID || grant.ConversationID != conversationID || grant.ObjectVersion != version || grant.OriginExecutionID != origin || grant.AutomationDepth != depth || grant.ActorID != event.Actor.ID || grant.Private == nil || *grant.Private != private || grant.ContentHash != hex.EncodeToString(hash[:]) || event.Payload["content_hash"] != grant.ContentHash || strings.TrimSpace(msg.Content) == "" || len(msg.Content) > 100000 || msg.ID != messageID || msg.AccountID != row.AccountID || msg.ConversationID != conversationID || msg.Private == nil || *msg.Private != private || msg.MessageType != domain.MessageTypeOutgoing || msg.ContentType != domain.ContentTypeText || msg.SenderType != domain.SenderTypeUser || msg.SenderID == nil || *msg.SenderID != 0 || msg.Status != status || msg.Deleted == nil || *msg.Deleted {
		return gorm.ErrInvalidData
	}
	return nil
}

func messageAutomationUint(value any) (uint, error) {
	id, err := messageAutomationNonnegativeUint(value)
	if err != nil || id == 0 {
		return 0, gorm.ErrInvalidData
	}
	return id, nil
}

func messageAutomationNonnegativeUint(value any) (uint, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	id, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil || id > 1<<53 || uint64(uint(id)) != id {
		return 0, gorm.ErrInvalidData
	}
	return uint(id), nil
}

func (r *MessageRepository) quarantineAutomationMessage(ctx context.Context, row domain.LocalChangeJournal) error {
	if row.Action == "automation_trigger" {
		return r.db.WithContext(ctx).Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ? AND id = ?", "MSG", "automation_trigger", row.ID).Updates(map[string]any{"status": "manual_action_required", "result": "invalid_automation_fact"}).Error
	}
	// Bind quarantine to the exact row, never a corrupt claimed event identity.
	sum := sha256.Sum256([]byte(fmt.Sprintf("MSG:automation_quarantine:%d:%d:%s", row.AccountID, row.ID, row.ChangeID)))
	now := time.Now().UTC()
	quarantine := domain.LocalChangeJournal{
		ChangeID: hex.EncodeToString(sum[:]), SourceModule: "MSG", AccountID: row.AccountID,
		EntityType: "LocalChangeJournal", EntityID: row.ID, ObjectType: "Message", ObjectID: row.ObjectID, ObjectDisplayID: row.ObjectDisplayID,
		Action: "automation_quarantine", SourceType: "AUT", ActorType: "System", CommandID: row.ChangeID, EventID: row.EventID,
		CorrelationID: row.CorrelationID, CausationID: row.CausationID, Reason: "invalid_automation_fact",
		OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "invalid_automation_fact", Status: "manual_action_required",
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&quarantine).Error
}

func (r *MessageRepository) MarkAutomationFactDelivered(ctx context.Context, eventID string) error {
	if strings.TrimSpace(eventID) == "" {
		return gorm.ErrInvalidData
	}
	// A durable owner receipt makes acknowledgements safe to replay, separately
	// from runtime event delivery to other subscribers.
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row domain.LocalChangeJournal
		if err := tx.Where("source_module = ? AND action IN ? AND change_id = ? AND event_id = ?", "MSG", []string{"automation_trigger", "event_outbox"}, eventID, eventID).First(&row).Error; err != nil {
			return err
		}
		event, supported, err := messageAutomationEnvelope(row)
		if err != nil {
			return err
		}
		if !supported {
			return gorm.ErrInvalidData
		}
		if event.EventType == "msg.message_status.changed.v1" {
			var attempt domain.LocalChangeJournal
			if err := tx.Where("source_module = ? AND account_id = ? AND action = ? AND change_id = ?", "MSG", row.AccountID, "retry_attempt", event.CausationID).First(&attempt).Error; err != nil {
				return err
			}
			intent, err := parseRetryAttempt(attempt)
			if err != nil {
				return err
			}
			expected, _ := json.Marshal(retryAttemptEvent(intent))
			actual, _ := json.Marshal(event)
			if attempt.Status != "delivered" || string(expected) != string(actual) {
				return gorm.ErrInvalidData
			}
		}

		if row.Action == "event_outbox" {
			if err := validateAutomationMessageConsumption(tx, row, event); err != nil {
				return err
			}
		}
		sum := sha256.Sum256([]byte("MSG:automation:" + eventID))
		id := "aut-delivery-" + hex.EncodeToString(sum[:])[:48]
		receipt := domain.LocalChangeJournal{ChangeID: id, SourceModule: "MSG", AccountID: row.AccountID, ObjectType: "Message", ObjectID: row.ObjectID, ObjectDisplayID: event.AggregateID, Action: "automation_delivery", EventID: eventID, Status: "delivered", Result: "applied"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&receipt).Error; err != nil {
			return err
		}
		if row.Action == "automation_trigger" {
			return tx.Model(&domain.LocalChangeJournal{}).Where("id = ? AND source_module = ?", row.ID, "MSG").Update("status", "delivered").Error
		}
		return nil
	})
}
