package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/billingportal"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BillingCustomerRepository owns only BIL customer identity and compatibility
// records. It must never receive TEN or ENT database handles.
type BillingCustomerRepository struct{ db *gorm.DB }

func NewBillingCustomerRepository(bilDB *gorm.DB) *BillingCustomerRepository {
	return &BillingCustomerRepository{db: bilDB}
}

var _ billingportal.Store = (*BillingCustomerRepository)(nil)

func billingCustomerCurrency(currency string) bool {
	if len(currency) != 3 {
		return false
	}
	for i := 0; i < len(currency); i++ {
		if currency[i] < 'a' || currency[i] > 'z' {
			return false
		}
	}
	return true
}

func billingCustomerIdentityValid(row *domain.BillingCustomer) bool {
	if row == nil || row.ID == 0 || row.AccountID == 0 || !billingCustomerCurrency(row.Currency) {
		return false
	}
	switch row.State {
	case "selected":
		return row.IntentID == nil && row.IntentCreatedAt == nil && row.ProviderScope == "" && row.CustomerID == nil
	case "creating", "ready":
		if row.IntentID == nil || row.IntentCreatedAt == nil || row.IntentCreatedAt.IsZero() || row.ProviderScope == "" || len(row.ProviderScope) > 64 {
			return false
		}
		if _, err := uuid.Parse(*row.IntentID); err != nil {
			return false
		}
		if row.State == "creating" {
			return row.CustomerID == nil
		}
		return row.CustomerID != nil && billingCustomerIDValid(*row.CustomerID)
	default:
		return false
	}
}

func billingCustomerIDValid(id string) bool {
	return strings.HasPrefix(id, "cus_") && len(id) > 4 && len(id) <= 255 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\r\n\t /\\")
}

// compatibility reads only the explicitly referenced BIL subscription/plan. It
// deliberately avoids GetAccountSubscription, which creates default records.
func billingCustomerCompatibility(tx *gorm.DB, account uint) error {
	var sub domain.AccountSubscription
	err := tx.Where("account_id = ?", account).First(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sub.Status != "active" || sub.CancelAtPeriodEnd || sub.StripeCustomerID != "" || sub.PlanID == 0 {
		return billingportal.ErrConflict
	}
	var plan domain.SubscriptionPlan
	if err := tx.First(&plan, sub.PlanID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return billingportal.ErrConflict
		}
		return err
	}
	if plan.Slug != "free" || plan.PriceCents != 0 {
		return billingportal.ErrConflict
	}
	return nil
}

func (r *BillingCustomerRepository) SelectCurrency(ctx context.Context, account uint, currency string) error {
	if account == 0 || !billingCustomerCurrency(currency) {
		return billingportal.ErrInvalidRequest
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		draft := domain.BillingCustomer{AccountID: account, Currency: currency, State: "selected"}
		// A conditional UPSERT obtains the same account row's lock even when the
		// existing identity is immutable. Preserve existing data until every guard
		// passes, so malformed rows are never silently repaired by a selection.
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "account_id"}},
			DoUpdates: clause.Assignments(map[string]any{"currency": gorm.Expr("billing_customers.currency")}),
			Where:     clause.Where{Exprs: []clause.Expression{clause.Eq{Column: clause.Column{Table: "billing_customers", Name: "state"}, Value: "selected"}}},
		}).Create(&draft).Error; err != nil {
			return billingCustomerConflict(err)
		}
		var current domain.BillingCustomer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", account).First(&current).Error; err != nil {
			return err
		}
		if err := billingCustomerCompatibility(tx, account); err != nil {
			return err
		}
		if !billingCustomerIdentityValid(&current) {
			return billingportal.ErrConflict
		}
		if current.Currency == currency {
			return nil
		}
		if current.State != "selected" {
			return billingportal.ErrConflict
		}
		return tx.Model(&current).Update("currency", currency).Error
	})
}

func (r *BillingCustomerRepository) Prepare(ctx context.Context, account uint, scope, intentID string, now time.Time, allowedCurrencies []string) (*domain.BillingCustomer, error) {
	if account == 0 || scope == "" || len(scope) > 64 || now.IsZero() {
		return nil, billingportal.ErrInvalidRequest
	}
	if _, err := uuid.Parse(intentID); err != nil {
		return nil, billingportal.ErrInvalidRequest
	}
	var result *domain.BillingCustomer
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// SQLite needs its write lock before any read, not a deferred read-to-write
		// upgrade. PostgreSQL also needs the SELECT FOR UPDATE below: this UPDATE
		// may see no row, while a concurrent selector inserts one before the SELECT.
		if err := tx.Model(&domain.BillingCustomer{}).Where("account_id = ?", account).UpdateColumn("currency", gorm.Expr("currency")).Error; err != nil {
			return err
		}
		var current domain.BillingCustomer
		findErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", account).First(&current).Error
		if err := billingCustomerCompatibility(tx, account); err != nil {
			return err
		}
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			return billingportal.ErrCurrencyRequired
		}
		if findErr != nil {
			return findErr
		}
		if !billingCustomerIdentityValid(&current) {
			return billingportal.ErrConflict
		}
		allowed := false
		for _, currency := range allowedCurrencies {
			if billingCustomerCurrency(currency) && currency == current.Currency {
				allowed = true
				break
			}
		}
		if !allowed {
			return billingportal.ErrInvalidRequest
		}
		if current.State != "selected" {
			if current.ProviderScope != scope {
				return billingportal.ErrConflict
			}
			result = &current
			return nil
		}
		first := now.UTC()
		if err := tx.Model(&current).Updates(map[string]any{"state": "creating", "intent_id": intentID, "intent_created_at": first, "provider_scope": scope}).Error; err != nil {
			return billingCustomerConflict(err)
		}
		// Read back database precision, so Bind compares the durable identity exactly.
		if err := tx.First(&current, current.ID).Error; err != nil {
			return err
		}
		result = &current
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (r *BillingCustomerRepository) Find(ctx context.Context, account uint) (*domain.BillingCustomer, error) {
	if account == 0 {
		return nil, billingportal.ErrInvalidRequest
	}
	var current domain.BillingCustomer
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := billingCustomerCompatibility(tx, account); err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", account).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return billingportal.ErrCustomerRequired
			}
			return err
		}
		if !billingCustomerIdentityValid(&current) {
			return billingportal.ErrConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &current, nil
}

func billingCustomerSameIntent(a, b *domain.BillingCustomer) bool {
	return a.ID == b.ID && a.AccountID == b.AccountID && a.Currency == b.Currency &&
		a.ProviderScope == b.ProviderScope && a.IntentID != nil && b.IntentID != nil && *a.IntentID == *b.IntentID &&
		a.IntentCreatedAt != nil && b.IntentCreatedAt != nil && a.IntentCreatedAt.Equal(*b.IntentCreatedAt)
}

func billingCustomerConflict(err error) error {
	if err == nil {
		return nil
	}
	// GORM's translated error is optional. Preserve transaction failures, mapping
	// only concrete uniqueness violations to the public conflict sentinel.
	if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(err.Error(), "SQLSTATE 23505") || strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return billingportal.ErrConflict
	}
	return err
}

func billingCustomerJournal(tx *gorm.DB, row *domain.BillingCustomer, now time.Time) error {
	before, _ := json.Marshal(map[string]string{"state": "creating", "currency": row.Currency})
	after, _ := json.Marshal(map[string]string{"state": "ready", "currency": row.Currency})
	change := domain.LocalChangeJournal{
		ChangeID: fmt.Sprintf("billing-customer-%d", row.ID), SourceModule: "BIL", AccountID: row.AccountID,
		ObjectType: "BillingCustomer", ObjectID: row.ID, Action: "update", ActorType: "system", SourceType: "billing_customer",
		ChangedFields: `["state"]`, BeforeState: string(before), AfterState: string(after), OccurredAt: now,
	}
	return NewJournalRepository(tx).RecordLocalChange(tx, &change)
}

func (r *BillingCustomerRepository) Bind(ctx context.Context, expected *domain.BillingCustomer, customer *billingportal.Customer, now time.Time) error {
	if !billingCustomerIdentityValid(expected) || expected.State == "selected" || customer == nil || now.IsZero() {
		return billingportal.ErrConflict
	}
	if !billingCustomerIDValid(customer.ID) || customer.BindingRef != *expected.IntentID || customer.Currency != expected.Currency || customer.Scope != expected.ProviderScope {
		return billingportal.ErrConflict
	}
	if expected.CustomerID != nil && *expected.CustomerID != customer.ID {
		return billingportal.ErrConflict
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.BillingCustomer{}).Where("account_id = ?", expected.AccountID).UpdateColumn("currency", gorm.Expr("currency")).Error; err != nil {
			return err
		}
		var current domain.BillingCustomer
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ?", expected.AccountID).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return billingportal.ErrConflict
			}
			return err
		}
		if err := billingCustomerCompatibility(tx, expected.AccountID); err != nil {
			return err
		}
		if !billingCustomerIdentityValid(&current) || !billingCustomerSameIntent(&current, expected) {
			return billingportal.ErrConflict
		}
		if expected.State == "ready" && current.State != "ready" {
			return billingportal.ErrConflict
		}
		if current.State == "ready" {
			if *current.CustomerID != customer.ID {
				return billingportal.ErrConflict
			}
			return nil
		}
		if current.State != "creating" {
			return billingportal.ErrConflict
		}
		if err := tx.Model(&current).Updates(map[string]any{"state": "ready", "customer_id": customer.ID, "updated_at": now.UTC()}).Error; err != nil {
			return billingCustomerConflict(err)
		}
		return billingCustomerJournal(tx, &current, now.UTC())
	})
}
