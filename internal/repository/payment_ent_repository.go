package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PaymentENTRepository is the only port that applies verified credit units.
// It accesses only ENT-owned limits and its own local atomic change journal.
type PaymentENTRepository struct{ db *gorm.DB }

func NewPaymentENTRepository(entDB *gorm.DB) *PaymentENTRepository {
	return &PaymentENTRepository{db: entDB}
}

func (r *PaymentENTRepository) ApplyPaymentGrant(ctx context.Context, g payment.Grant) error {
	if g.IntentID == "" || g.AccountID == 0 || g.Kind != "topup" || g.CreditUnits <= 0 || g.CreditUnits > 10000 {
		return payment.ErrInvalidRequest
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// The owner account row serializes both repeated and distinct purchases.
		// Unique journal IDs are the durable dedup boundary, never process memory.
		limit := domain.AccountLimit{AccountID: g.AccountID, Credits: 1000, Currency: "USD"}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "account_id"}}, DoNothing: true}).Create(&limit).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", g.AccountID).First(&limit).Error; err != nil {
			return err
		}
		encoded, _ := json.Marshal(g)
		changeID := "payment-credit-" + g.IntentID
		var existing domain.LocalChangeJournal
		err := tx.Where("change_id = ?", changeID).First(&existing).Error
		if err == nil {
			if existing.AccountID != g.AccountID || existing.AfterState != string(encoded) {
				return payment.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// Keep existing default credit policy, without treating price dollars as
		// units. Atomic SQL addition prevents lost updates across different orders.
		if math.IsNaN(limit.Credits) || math.IsInf(limit.Credits, 0) || limit.Credits > float64(1<<53)-float64(g.CreditUnits) {
			return payment.ErrConflict
		}
		if err := tx.Model(&domain.AccountLimit{}).Where("account_id = ?", g.AccountID).UpdateColumn("credits", gorm.Expr("credits + ?", g.CreditUnits)).Error; err != nil {
			return err
		}
		receipt := domain.LocalChangeJournal{ChangeID: changeID, SourceModule: "ENT", AccountID: g.AccountID, ObjectType: "AccountLimit", ObjectID: limit.ID, Action: "update", ChangedFields: "credits", ActorType: "system", SourceType: "verified_payment", SourceID: g.IntentID, AfterState: string(encoded), OccurredAt: time.Now().UTC()}
		return NewJournalRepository(tx).RecordLocalChange(tx, &receipt)
	})
}
