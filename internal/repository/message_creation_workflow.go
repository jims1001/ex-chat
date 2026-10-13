package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrCreationWorkflowMessageConflict = errors.New("creation workflow message conflicts with durable receipt")

func creationMessageChangeID(workflowID string, step int) string {
	return creationMessageHash("creation-message:" + workflowID + ":" + strconv.Itoa(step))
}

func creationMessageHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// CreateCreationWorkflowMessage is an internal CON-to-MSG owner command. Only
// messages in CON's committed creation plan use this path; no ordinary message
// request can opt out of its automation fact. The plan is never mutated, so it
// can be retried unchanged after either owner restarts.
func (r *MessageRepository) CreateCreationWorkflowMessage(ctx context.Context, workflowID string, step int, msg *domain.Message, expectedVersion int) (*domain.Message, error) {
	if ctx == nil || r.db == nil || r.conversation == nil || strings.TrimSpace(workflowID) == "" || step < 0 || expectedVersion < 1 || msg == nil || msg.AccountID == 0 || msg.ConversationID == 0 {
		return nil, gorm.ErrInvalidData
	}
	if _, ok := r.conversation.(messageCreationActivityPort); !ok {
		return nil, gorm.ErrInvalidData
	}
	planOwner, ok := r.conversation.(interface {
		ValidateCreationWorkflowMessage(context.Context, string, int, *domain.Message, int) error
	})
	if !ok {
		return nil, gorm.ErrInvalidData
	}
	// IDs, timestamps and edit/delete state are assigned by MSG, never by the
	// creation plan. Enrichment is a read concern and must not enter the intent.
	if msg.ID != 0 || !msg.CreatedAt.IsZero() || !msg.UpdatedAt.IsZero() || msg.Deleted || msg.DeletedAt != nil || msg.EditedAt != nil || msg.Sender != nil {
		return nil, gorm.ErrInvalidData
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	created := *msg
	if created.MessageType == "" {
		created.MessageType = domain.MessageTypeIncoming
	}
	if created.ContentType == "" {
		created.ContentType = domain.ContentTypeText
	}
	if created.Status == "" {
		created.Status = domain.MessageStatusSent
	}
	// CON must confirm the complete committed plan before MSG can suppress its
	// ordinary automation trigger. A workflow ID alone is not that authority.
	if err := planOwner.ValidateCreationWorkflowMessage(ctx, workflowID, step, &created, expectedVersion); err != nil {
		return nil, err
	}
	// Serialize the complete normalized creation intent, including tenant and
	// conversation. Later edits to the stored message do not rewrite this receipt.
	intent := struct {
		WorkflowID      string         `json:"workflow_id"`
		Step            int            `json:"step"`
		ExpectedVersion int            `json:"expected_version"`
		Message         domain.Message `json:"message"`
	}{workflowID, step, expectedVersion, created}
	raw, err := json.Marshal(intent)
	if err != nil {
		return nil, err
	}
	digest := creationMessageHash(string(raw))
	changeID := creationMessageChangeID(workflowID, step)
	receiptID := creationMessageHash("creation-message-receipt:" + workflowID + ":" + strconv.Itoa(step))
	exists, err := r.conversation.MessageTargetExists(created.AccountID, created.ConversationID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, gorm.ErrRecordNotFound
	}
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now().UTC()
		receipt := domain.LocalChangeJournal{
			ChangeID: receiptID, SourceModule: "MSG", AccountID: created.AccountID,
			ObjectType: "Message", EntityType: "Message", Action: "creation_message_receipt",
			AfterState: string(raw), IdempotencyKeyHash: digest,
			ActorType: created.SenderType, ActorID: created.SenderID,
			SourceType: "con.creation.message.v1", CommandID: changeID, CorrelationID: changeID,
			Status: "published", OccurredAt: now, RecordedAt: now, IngestedAt: now,
		}
		// Acquire the unique receipt before reading or creating a message. In
		// PostgreSQL, a concurrent retry waits for the first transaction to commit;
		// SQLite's write lock provides the same serialization across connections.
		insert := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "change_id"}}, DoNothing: true}).Create(&receipt)
		if insert.Error != nil {
			return insert.Error
		}
		if insert.RowsAffected == 0 {
			var existing domain.LocalChangeJournal
			if err := tx.Where("change_id = ?", receiptID).First(&existing).Error; err != nil {
				return err
			}
			if existing.SourceModule != "MSG" || existing.SourceType != "con.creation.message.v1" || existing.Action != "creation_message_receipt" || existing.ObjectType != "Message" || existing.AccountID != created.AccountID || existing.ObjectID == 0 || existing.CommandID != changeID || existing.IdempotencyKeyHash != digest || existing.AfterState != string(raw) {
				return ErrCreationWorkflowMessageConflict
			}
			// Never recreate missing/deleted content from an old workflow receipt.
			return tx.Where("account_id = ? AND conversation_id = ? AND id = ?", created.AccountID, created.ConversationID, existing.ObjectID).First(&created).Error
		}
		created.CreatedAt, created.UpdatedAt = now, now
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		if err := tx.Model(&domain.LocalChangeJournal{}).Where("id = ?", receipt.ID).Updates(map[string]any{"object_id": created.ID, "entity_id": created.ID}).Error; err != nil {
			return err
		}
		activity, err := json.Marshal(struct {
			ExpectedVersion int    `json:"expected_version"`
			Reopen          bool   `json:"reopen"`
			WorkflowID      string `json:"creation_workflow_id"`
			Step            int    `json:"creation_workflow_step"`
		}{expectedVersion, false, workflowID, step})
		if err != nil {
			return err
		}
		change := domain.LocalChangeJournal{
			ChangeID: changeID, SourceModule: "MSG", AccountID: created.AccountID,
			ObjectType: "Message", ObjectID: created.ID, ObjectVersionAfter: 1,
			Action: "create", ChangedFields: "message", ActorType: created.SenderType, ActorID: created.SenderID,
			SourceType: "msg.message.create.v1", CommandID: changeID, CorrelationID: changeID,
			AfterState: string(activity), Status: "pending", OccurredAt: created.CreatedAt,
		}
		// The completed creation workflow emits CON's one immutable creation
		// fact. Its planned messages must not emit independent MSG automation facts.
		return NewJournalRepository(tx).RecordLocalChange(tx, &change)
	})
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return &created, err
	}
	// Unlike ordinary create, delivery failure is observable by the CON workflow.
	// The durable receipt makes both direct retries and journal replay safe.
	if err := r.applyMessageActivity(&created, changeID); err != nil {
		return &created, err
	}
	return &created, nil
}
