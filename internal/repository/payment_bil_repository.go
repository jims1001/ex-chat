package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PaymentBILRepository accesses only BIL-owned payment and accounting records.
// Never pass an ENT connection to this constructor.
type PaymentBILRepository struct{ db *gorm.DB }

func NewPaymentBILRepository(bilDB *gorm.DB) *PaymentBILRepository {
	return &PaymentBILRepository{db: bilDB}
}

func (r *PaymentBILRepository) Prepare(ctx context.Context, draft *domain.PaymentCheckoutIntent) (*domain.PaymentCheckoutIntent, error) {
	if err := r.db.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account_id"}, {Name: "request_key_hash"}}, DoNothing: true}).Create(draft).Error; err != nil {
		return nil, err
	}
	var intent domain.PaymentCheckoutIntent
	if err := r.db.WithContext(ctx).Where("account_id = ? AND request_key_hash = ?", draft.AccountID, draft.RequestKeyHash).First(&intent).Error; err != nil {
		return nil, err
	}
	if intent.RequestHash != draft.RequestHash {
		return nil, payment.ErrConflict
	}
	return &intent, nil
}
func (r *PaymentBILRepository) BindSession(ctx context.Context, id string, session *payment.Session) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var intent domain.PaymentCheckoutIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("intent_id = ?", id).First(&intent).Error; err != nil {
			return err
		}
		if intent.SessionID != nil {
			if *intent.SessionID != session.ID {
				return payment.ErrConflict
			}
			return nil
		}
		return tx.Model(&intent).Updates(map[string]any{"session_id": session.ID, "checkout_url": session.URL, "status": "pending"}).Error
	})
}
func (r *PaymentBILRepository) FindSession(ctx context.Context, id string) (*domain.PaymentCheckoutIntent, error) {
	var intent domain.PaymentCheckoutIntent
	if err := r.db.WithContext(ctx).Where("session_id = ?", id).First(&intent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, payment.ErrMismatch
		}
		return nil, err
	}
	return &intent, nil
}

func (r *PaymentBILRepository) RecordEvent(ctx context.Context, event *payment.Event, session *payment.Session, now time.Time) (*domain.PaymentCheckoutIntent, error) {
	var result *domain.PaymentCheckoutIntent
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var intent domain.PaymentCheckoutIntent
		if session != nil {
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("session_id = ?", session.ID).First(&intent).Error; err != nil {
				return err
			}
		}
		receipt := domain.PaymentWebhookReceipt{EventID: event.ID, Digest: event.Digest, Status: "received"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "event_id"}}, DoNothing: true}).Create(&receipt).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("event_id = ?", event.ID).First(&receipt).Error; err != nil {
			return err
		}
		if receipt.Digest != event.Digest {
			return payment.ErrConflict
		}
		if receipt.Status == "fulfilled" || receipt.Status == "ignored" {
			return nil
		}
		if session == nil {
			return tx.Model(&receipt).Update("status", "ignored").Error
		}
		// Revalidate at the owning transaction boundary, not just in the caller.
		if intent.IntentID != session.ReferenceID || intent.Kind != "topup" || intent.Product != session.Product || intent.AmountMinor != session.AmountMinor || intent.Currency != session.Currency || session.Quantity != 1 || session.Mode != "payment" {
			return payment.ErrMismatch
		}
		if session.Status != "complete" || session.PaymentStatus != "paid" {
			// completed can precede asynchronous success; a new successful event may
			// later grant, but this unpaid event is permanently non-fulfilling.
			return tx.Model(&receipt).Updates(map[string]any{"intent_id": intent.IntentID, "status": "ignored"}).Error
		}
		if intent.Status == "fulfilled" {
			return tx.Model(&receipt).Updates(map[string]any{"intent_id": intent.IntentID, "status": "fulfilled"}).Error
		}
		if intent.BillingID == 0 {
			billing := domain.AccountBilling{AccountID: intent.AccountID, ActionType: "topup", Amount: float64(intent.AmountMinor) / 100, Currency: strings.ToUpper(intent.Currency), Description: fmt.Sprintf("Verified top-up %d credits; checkout %s", intent.CreditUnits, intent.IntentID), CreatedAt: now}
			if err := tx.Create(&billing).Error; err != nil {
				return err
			}
			intent.BillingID = billing.ID
			// Journal and financial projection commit atomically in BIL. The integer
			// money source remains the intent, not the compatibility float projection.
			change := domain.LocalChangeJournal{ChangeID: "payment-billing-" + intent.IntentID, SourceModule: "BIL", AccountID: intent.AccountID, ObjectType: "AccountBilling", ObjectID: billing.ID, Action: "create", ActorType: "system", SourceType: "verified_payment", SourceID: intent.IntentID, OccurredAt: now}
			if err := NewJournalRepository(tx).RecordLocalChange(tx, &change); err != nil {
				return err
			}
		}
		intent.Status = "paid_pending"
		if err := tx.Model(&intent).Updates(map[string]any{"status": intent.Status, "billing_id": intent.BillingID}).Error; err != nil {
			return err
		}
		if err := tx.Model(&receipt).Updates(map[string]any{"intent_id": intent.IntentID, "status": "pending"}).Error; err != nil {
			return err
		}
		result = &intent
		return nil
	})
	return result, err
}
func (r *PaymentBILRepository) MarkFulfilled(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.PaymentCheckoutIntent{}).Where("intent_id = ? AND status = ?", id, "paid_pending").Update("status", "fulfilled").Error; err != nil {
			return err
		}
		return tx.Model(&domain.PaymentWebhookReceipt{}).Where("intent_id = ? AND status = ?", id, "pending").Update("status", "fulfilled").Error
	})
}
func (r *PaymentBILRepository) Pending(ctx context.Context, limit int) ([]domain.PaymentCheckoutIntent, error) {
	var intents []domain.PaymentCheckoutIntent
	err := r.db.WithContext(ctx).Where("status = ?", "paid_pending").Order("id").Limit(limit).Find(&intents).Error
	return intents, err
}
