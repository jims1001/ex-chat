package modruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type EventOutbox struct {
	db       *gorm.DB
	moduleID string
}

func NewEventOutbox(db *gorm.DB, moduleID string) *EventOutbox {
	return &EventOutbox{db: db, moduleID: moduleID}
}

func (o *EventOutbox) Append(tx *gorm.DB, event foundation.EventEnvelope) error {
	if event.Validate() != nil || event.SourceModule != o.moduleID {
		return &ContractFailure{Type: "validation_failed", Message: "event envelope is invalid"}
	}
	if tx == nil {
		tx = o.db
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	return tx.Create(&domain.LocalChangeJournal{ChangeID: event.EventID, SourceModule: o.moduleID, AccountID: event.AccountID, ObjectType: event.AggregateType, ObjectDisplayID: event.AggregateID, ObjectVersionAfter: int(event.AggregateVersion), Action: "event_outbox", AfterState: string(payload), ActorType: event.Actor.Type, EventID: event.EventID, CorrelationID: event.CorrelationID, CausationID: event.CausationID, OccurredAt: event.OccurredAt, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "pending", Status: "pending"}).Error
}
func (o *EventOutbox) MarkDelivered(eventID string) error {
	return o.db.Model(&domain.LocalChangeJournal{}).Where("change_id = ? AND source_module = ? AND action = ?", eventID, o.moduleID, "event_outbox").Updates(map[string]any{"status": "delivered", "result": "applied"}).Error
}

type EventDispatcher struct {
	outbox                    *EventOutbox
	targetURL, token, version string
	client                    *http.Client
}

func NewEventDispatcher(db *gorm.DB, sourceModule string, dependency DependencyConfig, version string, timeout time.Duration) *EventDispatcher {
	return &EventDispatcher{outbox: NewEventOutbox(db, sourceModule), targetURL: strings.TrimRight(dependency.URL, "/"), token: dependency.Token, version: version, client: &http.Client{Timeout: timeout}}
}
func (d *EventDispatcher) DeliverPending(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	var records []domain.LocalChangeJournal
	if err := d.outbox.db.Where("source_module = ? AND action = ? AND status = ?", d.outbox.moduleID, "event_outbox", "pending").Order("id ASC").Limit(limit).Find(&records).Error; err != nil {
		return 0, err
	}
	delivered := 0
	for _, record := range records {
		var event foundation.EventEnvelope
		if err := json.Unmarshal([]byte(record.AfterState), &event); err != nil {
			return delivered, err
		}
		if err := d.deliver(ctx, event); err != nil {
			return delivered, err
		}
		if err := d.outbox.MarkDelivered(event.EventID); err != nil {
			return delivered, err
		}
		delivered++
	}
	return delivered, nil
}
func (d *EventDispatcher) deliver(ctx context.Context, event foundation.EventEnvelope) error {
	body, _ := json.Marshal(event)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.targetURL+"/contracts/events/"+event.EventType, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+d.token)
	request.Header.Set(headerCaller, d.outbox.moduleID)
	request.Header.Set(headerContractVersion, d.version)
	request.Header.Set(headerCorrelationID, event.CorrelationID)
	response, err := d.client.Do(request)
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "event consumer is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return &ContractFailure{Type: "dependency_unavailable", Message: "event consumer returned an invalid response"}
	}
	return nil
}

type EventInbox struct {
	db       *gorm.DB
	moduleID string
}

func NewEventInbox(db *gorm.DB, moduleID string) *EventInbox {
	return &EventInbox{db: db, moduleID: moduleID}
}

func (i *EventInbox) Consume(ctx context.Context, event foundation.EventEnvelope, apply func(context.Context) error) (any, error) {
	hash := sha256.Sum256([]byte(event.EventID))
	changeID := "in-" + hex.EncodeToString(hash[:])[:48]
	result := map[string]any{"event_id": event.EventID, "status": "applied"}
	err := i.db.Transaction(func(tx *gorm.DB) error {
		var existing int64
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", changeID).Count(&existing).Error; err != nil {
			return err
		}
		if existing > 0 {
			result["status"] = "duplicate"
			return nil
		}
		var latest int64
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ? AND source_type = ? AND object_type = ? AND object_display_id = ?", i.moduleID, "event_consumed", event.SourceModule, event.AggregateType, event.AggregateID).Select("COALESCE(MAX(object_version_after),0)").Scan(&latest).Error; err != nil {
			return err
		}
		if event.AggregateVersion != uint64(latest+1) {
			return &ContractFailure{Type: "state_conflict", Message: "event aggregate version has a gap", Details: map[string]any{"expected_version": latest + 1, "received_version": event.AggregateVersion}}
		}
		if err := apply(context.WithValue(ctx, transactionKey{}, tx)); err != nil {
			return err
		}
		now := time.Now().UTC()
		return tx.Create(&domain.LocalChangeJournal{ChangeID: changeID, SourceModule: i.moduleID, SourceType: event.SourceModule, AccountID: event.AccountID, ObjectType: event.AggregateType, ObjectDisplayID: event.AggregateID, ObjectVersionAfter: int(event.AggregateVersion), Action: "event_consumed", ActorType: event.Actor.Type, EventID: event.EventID, CorrelationID: event.CorrelationID, CausationID: event.CausationID, OccurredAt: event.OccurredAt, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "applied", Status: "delivered"}).Error
	})
	if err != nil {
		var failure *ContractFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "event inbox transaction failed"}
	}
	return result, nil
}
