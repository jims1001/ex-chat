package modruntime

import (
	"context"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm/clause"
	"time"
)

// Anonymization fences work before clearing the private intent. SMTP in progress
// cannot be retracted: reject the whole owner command until it settles/expires.
func (r Runtime) anonymizeOrdinaryEmail(ctx context.Context, accountID uint) error {
	tx := ordinaryEmailPrivateDB(Transaction(ctx, r.DB))
	if err := repository.LockOrdinaryEmailAccount(tx, accountID); err != nil {
		return errOrdinaryEmailStore
	}
	now := time.Now().UTC()
	var rows []domain.OrdinaryEmailIntent
	q := tx.Where("account_id = ?", accountID).Order("id ASC")
	if tx.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.Find(&rows).Error; err != nil {
		return errOrdinaryEmailStore
	}
	for _, item := range rows {
		if item.State == "submitting" && item.LeaseUntil != nil && item.LeaseUntil.After(now) {
			return &ContractFailure{Type: "state_conflict", Message: "email submission is still in flight; query delivery status before anonymizing"}
		}
	}
	for _, item := range rows {
		if item.State == "pending" || item.State == "leased" || item.State == "submitting" {
			state, code := "cancelled", "account_anonymized_before_submission"
			if item.State == "submitting" {
				state, code = "unknown", "submission_outcome_unknown"
			}
			if err := r.finishOrdinaryTx(tx, &item, state, code, false, now); err != nil {
				return err
			}
		}
		u := tx.Model(&domain.OrdinaryEmailIntent{}).Where("id = ? AND account_id = ?", item.ID, accountID).Updates(map[string]any{"request_input_hash": "", "transcript_messages": 0, "payload": "", "payload_hash": "", "transport_key": "", "route_kind": "", "route_account_id": "", "route_inbox_id": "", "route_created_at": "", "route_fingerprint": "", "anonymized": true, "lease_token": "", "lease_until": nil, "next_attempt_at": nil, "updated_at": now})
		if u.Error != nil {
			return errOrdinaryEmailStore
		}
	}
	return nil
}
