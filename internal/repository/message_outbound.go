package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

var (
	ErrOutboundConflict  = errors.New("outbound attempt binding or state conflict")
	ErrOutboundClaimLost = errors.New("outbound attempt claim is no longer valid")
	ErrOutboundCancelled = errors.New("outbound attempt cancelled because its message changed or was deleted")
)

// Snapshot SQL contains message bodies and private endpoint configuration.
// Suppress statement logging even when the application enables SQL tracing.
func (r *MessageRepository) outboundDB(ctx context.Context) *gorm.DB {
	return r.db.WithContext(ctx).Session(&gorm.Session{Logger: r.db.Logger.LogMode(gormlogger.Silent)})
}

// CreateOutboundMessage commits the ordinary MSG create journal and its private
// delivery snapshot together. No provider call or cross-owner query is in the
// transaction. Existing create/activity recovery semantics are preserved.
func (r *MessageRepository) CreateOutboundMessage(ctx context.Context, msg *domain.Message, attempt *domain.OutboundMessageAttempt) (bool, error) {
	if msg == nil || attempt == nil || msg.ID != 0 || attempt.MessageID != 0 || !outboundMessageMatches(*msg, *attempt, false) || msg.Status != domain.MessageStatusQueued {
		return false, gorm.ErrInvalidData
	}
	if attempt.ID == "" {
		attempt.ID = foundation.GenerateUUID()
	}
	if !validRetryUUID(attempt.ID) {
		return false, gorm.ErrInvalidData
	}
	if exists, err := r.outboundCreateReplay(ctx, msg, attempt); exists || err != nil {
		return false, err
	}
	err := r.createWithOutbound(ctx, msg, nil, attempt)
	if err == nil {
		return true, nil
	}
	// A concurrent insert with the same opaque intent ID rolls back its message
	// and journal. Reload only an exact committed winner; never send twice.
	if exists, replayErr := r.outboundCreateReplay(ctx, msg, attempt); exists {
		return false, replayErr
	}
	if isUniqueConstraintViolation(err) {
		return false, ErrOutboundConflict
	}
	return false, err
}

func (r *MessageRepository) outboundCreateReplay(ctx context.Context, msg *domain.Message, supplied *domain.OutboundMessageAttempt) (bool, error) {
	var a domain.OutboundMessageAttempt
	err := r.outboundDB(ctx).Where("account_id = ? AND id = ?", supplied.AccountID, supplied.ID).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if a.Sequence != 1 || !outboundRetryBindingMatches(a, *supplied) || a.CallbackURL != supplied.CallbackURL {
		return true, ErrOutboundConflict
	}
	var current domain.Message
	if err := r.outboundDB(ctx).Where("account_id = ? AND conversation_id = ? AND id = ?", a.AccountID, a.ConversationID, a.MessageID).First(&current).Error; err != nil {
		return true, err
	}
	if !outboundMessageMatches(current, a, true) || !outboundMessageMatches(*msg, a, false) {
		return true, ErrOutboundConflict
	}
	*msg, *supplied = current, a
	return true, nil
}

func (r *MessageRepository) RetryOutboundMessage(ctx context.Context, accountID, conversationID, messageID uint, attempt *domain.OutboundMessageAttempt) (*domain.Message, bool, error) {
	if attempt == nil || attempt.AccountID != accountID || attempt.ConversationID != conversationID || (attempt.MessageID != 0 && attempt.MessageID != messageID) {
		return nil, false, gorm.ErrInvalidData
	}
	return r.retryMessageAccepted(ctx, accountID, conversationID, messageID, attempt)
}

func persistOutboundIntent(tx *gorm.DB, msg *domain.Message, attempt *domain.OutboundMessageAttempt, sequence int) error {
	if attempt == nil || msg == nil || sequence < 1 || !outboundMessageMatches(*msg, *attempt, false) || msg.Status != domain.MessageStatusQueued || (attempt.MessageID != 0 && attempt.MessageID != msg.ID) || (attempt.Sequence != 0 && attempt.Sequence != sequence) {
		return gorm.ErrInvalidData
	}
	if attempt.ID == "" {
		attempt.ID = foundation.GenerateUUID()
	}
	if !validRetryUUID(attempt.ID) || attempt.InboxID == 0 || attempt.ContactID == 0 || attempt.ConnectionID == 0 || attempt.Provider == "" || attempt.ProviderAccountID == "" || attempt.SenderID == "" || attempt.RecipientID == "" || attempt.ConfigFingerprint == "" || attempt.APIVersion == "" || attempt.CallbackURL == "" || (attempt.State != "" && attempt.State != domain.OutboundStateQueued) || (attempt.Status != "" && attempt.Status != domain.MessageStatusQueued) || attempt.ProviderMessageID != "" || attempt.ClaimToken != "" || attempt.ClaimUntil != nil || attempt.DispatchStartedAt != nil || attempt.RetrySafe || attempt.FailureObserved {
		return gorm.ErrInvalidData
	}
	attempt.MessageID, attempt.Sequence = msg.ID, sequence
	attempt.State, attempt.Status = domain.OutboundStateQueued, domain.MessageStatusQueued
	attempt.CreatedAt, attempt.UpdatedAt = msg.UpdatedAt, msg.UpdatedAt
	if attempt.CreatedAt.IsZero() {
		attempt.CreatedAt = time.Now().UTC().Truncate(time.Microsecond)
		attempt.UpdatedAt = attempt.CreatedAt
	}
	if err := tx.Create(attempt).Error; err != nil {
		return err
	}
	return recordOutboundAudit(tx, *attempt, "outbound_attempt", "", domain.MessageStatusQueued, attempt.CreatedAt)
}

// checkOutboundRetry closes the legacy-retry bypass and permits replay only of
// an unchanged queued intent. Every previous dispatched attempt must be a
// definite rejection before accepting another physical send.
func checkOutboundRetry(db *gorm.DB, message domain.Message, supplied *domain.OutboundMessageAttempt) (bool, int, error) {
	// Legacy isolated schemas without transport retain their old retry behavior.
	if supplied == nil && !db.Migrator().HasTable(&domain.OutboundMessageAttempt{}) {
		return false, 1, nil
	}
	var latest domain.OutboundMessageAttempt
	err := db.Where("account_id = ? AND message_id = ?", message.AccountID, message.ID).Order("sequence DESC").First(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, 0, err
	}
	if supplied == nil {
		if err == nil {
			return false, 0, ErrMessageNotRetryable
		}
		return false, 1, nil
	}
	if message.Deleted || !outboundMessageMatches(message, *supplied, false) {
		return false, 0, ErrMessageNotRetryable
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, 1, nil
	}
	if latest.ConversationID != message.ConversationID {
		return false, 0, ErrOutboundConflict
	}
	if message.Status == domain.MessageStatusQueued && (latest.State == domain.OutboundStateQueued || latest.State == domain.OutboundStateClaimed || latest.State == domain.OutboundStateSending) && outboundMessageMatches(message, latest, true) && outboundRetryBindingMatches(latest, *supplied) {
		*supplied = latest
		return true, latest.Sequence, nil
	}
	if message.Status != domain.MessageStatusFailed || latest.State != domain.OutboundStateSettled || latest.Status != domain.MessageStatusFailed || !latest.RetrySafe || latest.ProviderMessageID != "" || latest.DispatchStartedAt == nil {
		return false, 0, ErrMessageNotRetryable
	}
	var unsafe int64
	if err := db.Model(&domain.OutboundMessageAttempt{}).Where("account_id = ? AND message_id = ? AND retry_safe = ?", message.AccountID, message.ID, false).Count(&unsafe).Error; err != nil {
		return false, 0, err
	}
	if unsafe > 0 {
		return false, 0, ErrMessageNotRetryable
	}
	return false, latest.Sequence + 1, nil
}

func outboundMessageMatches(m domain.Message, a domain.OutboundMessageAttempt, requireID bool) bool {
	return m.AccountID > 0 && m.ConversationID > 0 && m.AccountID == a.AccountID && m.ConversationID == a.ConversationID && (!requireID || m.ID == a.MessageID) && !m.Deleted && !m.Private && !a.Private && m.MessageType == domain.MessageTypeOutgoing && a.MessageType == m.MessageType && m.ContentType == domain.ContentTypeText && a.ContentType == m.ContentType && m.SenderID > 0 && m.SenderID == a.ActorID && m.SenderType != "" && m.SenderType == a.SenderType && strings.TrimSpace(m.Content) != "" && m.Content == a.Body
}

func outboundRetryBindingMatches(a, b domain.OutboundMessageAttempt) bool {
	return a.AccountID == b.AccountID && a.ConversationID == b.ConversationID && a.InboxID == b.InboxID && a.ContactID == b.ContactID && a.ConnectionID == b.ConnectionID && a.Provider == b.Provider && a.ProviderAccountID == b.ProviderAccountID && a.SenderID == b.SenderID && a.RecipientID == b.RecipientID && a.ConfigFingerprint == b.ConfigFingerprint && a.APIVersion == b.APIVersion && a.Body == b.Body && a.ContentType == b.ContentType && a.MessageType == b.MessageType && a.SenderType == b.SenderType && a.ActorID == b.ActorID && a.Private == b.Private
}

func outboundImmutableMatches(a, b domain.OutboundMessageAttempt) bool {
	return a.ID == b.ID && a.MessageID == b.MessageID && a.Sequence == b.Sequence && a.CallbackURL == b.CallbackURL && a.CreatedAt.Equal(b.CreatedAt) && outboundRetryBindingMatches(a, b)
}

func (r *MessageRepository) GetOutboundAttempt(ctx context.Context, accountID uint, attemptID string) (*domain.OutboundMessageAttempt, error) {
	var a domain.OutboundMessageAttempt
	if accountID == 0 || !validRetryUUID(attemptID) {
		return nil, gorm.ErrInvalidData
	}
	err := r.outboundDB(ctx).Where("account_id = ? AND id = ?", accountID, attemptID).First(&a).Error
	return &a, err
}

func (r *MessageRepository) LatestOutboundAttempt(ctx context.Context, accountID, conversationID, messageID uint) (*domain.OutboundMessageAttempt, error) {
	if accountID == 0 || conversationID == 0 || messageID == 0 {
		return nil, gorm.ErrInvalidData
	}
	var a domain.OutboundMessageAttempt
	err := r.outboundDB(ctx).Where("account_id = ? AND conversation_id = ? AND message_id = ?", accountID, conversationID, messageID).Order("sequence DESC").First(&a).Error
	return &a, err
}

func (r *MessageRepository) FindOutboundAttemptByProvider(ctx context.Context, accountID uint, provider, providerAccountID, senderID, recipientID, providerID string) (*domain.OutboundMessageAttempt, error) {
	if accountID == 0 || provider == "" || providerAccountID == "" || senderID == "" || recipientID == "" || providerID == "" {
		return nil, gorm.ErrInvalidData
	}
	var rows []domain.OutboundMessageAttempt
	if err := r.outboundDB(ctx).Where("account_id = ? AND provider = ? AND provider_account_id = ? AND sender_id = ? AND recipient_id = ? AND provider_message_id = ? AND dispatch_started_at IS NOT NULL", accountID, provider, providerAccountID, senderID, recipientID, providerID).Limit(2).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if len(rows) != 1 {
		return nil, ErrOutboundConflict
	}
	return &rows[0], nil
}

// ClaimNextOutbound never reclaims a dispatched attempt. CAS is used rather
// than process-local locks so separate workers/repositories cannot both send.
func (r *MessageRepository) ClaimNextOutbound(ctx context.Context, now time.Time, lease time.Duration) (*domain.OutboundMessageAttempt, error) {
	if now.IsZero() || lease <= 0 {
		return nil, gorm.ErrInvalidData
	}
	now = now.UTC().Truncate(time.Microsecond)
	if err := r.expireOutboundDispatches(ctx, now); err != nil {
		return nil, err
	}
	db := r.outboundDB(ctx)
	for {
		var a domain.OutboundMessageAttempt
		err := db.Where("dispatch_started_at IS NULL AND (state = ? OR (state = ? AND claim_until <= ?))", domain.OutboundStateQueued, domain.OutboundStateClaimed, now).Order("created_at ASC, id ASC").First(&a).Error
		if err != nil {
			return nil, err
		}
		token, until := foundation.GenerateUUID(), now.Add(lease)
		result := db.Model(&domain.OutboundMessageAttempt{}).Where("account_id = ? AND id = ? AND dispatch_started_at IS NULL AND (state = ? OR (state = ? AND claim_until <= ?))", a.AccountID, a.ID, domain.OutboundStateQueued, domain.OutboundStateClaimed, now).Updates(map[string]any{"state": domain.OutboundStateClaimed, "claim_token": token, "claim_until": until, "updated_at": now})
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			continue
		}
		a.State, a.ClaimToken, a.ClaimUntil, a.UpdatedAt = domain.OutboundStateClaimed, token, &until, now
		return &a, nil
	}
}

func (r *MessageRepository) expireOutboundDispatches(ctx context.Context, now time.Time) error {
	var expired []domain.OutboundMessageAttempt
	if err := r.outboundDB(ctx).Where("state = ? AND dispatch_started_at IS NOT NULL AND claim_until <= ?", domain.OutboundStateSending, now).Find(&expired).Error; err != nil {
		return err
	}
	for _, candidate := range expired {
		if err := r.outboundDB(ctx).Transaction(func(tx *gorm.DB) error {
			var a domain.OutboundMessageAttempt
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", candidate.AccountID, candidate.ID).First(&a).Error; err != nil {
				return err
			}
			if a.State != domain.OutboundStateSending || a.DispatchStartedAt == nil || a.ClaimUntil == nil || a.ClaimUntil.After(now) {
				return nil
			}
			return persistOutboundResult(tx, a, "", domain.MessageStatusUnknown, false, false, now)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (r *MessageRepository) StartOutboundDispatch(ctx context.Context, accountID uint, attemptID, token string, now time.Time) (*domain.OutboundMessageAttempt, error) {
	var a domain.OutboundMessageAttempt
	cancelled := false
	if accountID == 0 || !validRetryUUID(attemptID) || !validRetryUUID(token) || now.IsZero() {
		return nil, gorm.ErrInvalidData
	}
	err := r.outboundDB(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", accountID, attemptID).First(&a).Error; err != nil {
			return err
		}
		if a.State != domain.OutboundStateClaimed || a.ClaimToken != token || a.ClaimUntil == nil || !a.ClaimUntil.After(now) || a.DispatchStartedAt != nil {
			return ErrOutboundClaimLost
		}
		var m domain.Message
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND conversation_id = ? AND id = ?", accountID, a.ConversationID, a.MessageID).First(&m).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if errors.Is(err, gorm.ErrRecordNotFound) || !outboundMessageMatches(m, a, true) || m.Status != domain.MessageStatusQueued {
			if err := persistOutboundResult(tx, a, "", domain.MessageStatusCancelled, false, false, now); err != nil {
				return err
			}
			a.State, a.Status, a.UpdatedAt = domain.OutboundStateCancelled, domain.MessageStatusCancelled, now
			cancelled = true
			return nil // Commit cancellation before returning the terminal error.
		}
		result := tx.Model(&domain.OutboundMessageAttempt{}).Where("account_id = ? AND id = ? AND claim_token = ? AND state = ? AND dispatch_started_at IS NULL AND claim_until > ?", accountID, attemptID, token, domain.OutboundStateClaimed, now).Updates(map[string]any{"state": domain.OutboundStateSending, "dispatch_started_at": now, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrOutboundClaimLost
		}
		a.State, a.DispatchStartedAt, a.UpdatedAt = domain.OutboundStateSending, &now, now
		return nil
	})
	if err == nil && cancelled {
		err = ErrOutboundCancelled
	}
	return &a, err
}

func (r *MessageRepository) CancelClaimedOutbound(ctx context.Context, accountID uint, attemptID, token string, now time.Time) error {
	if accountID == 0 || !validRetryUUID(attemptID) || !validRetryUUID(token) || now.IsZero() {
		return gorm.ErrInvalidData
	}
	return r.outboundDB(ctx).Transaction(func(tx *gorm.DB) error {
		var a domain.OutboundMessageAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", accountID, attemptID).First(&a).Error; err != nil {
			return err
		}
		if a.State != domain.OutboundStateClaimed || a.ClaimToken != token || a.DispatchStartedAt != nil || a.ClaimUntil == nil || !a.ClaimUntil.After(now) {
			return ErrOutboundClaimLost
		}
		return persistOutboundResult(tx, a, "", domain.MessageStatusCancelled, false, false, now)
	})
}

// CompleteOutboundDispatch accepts a late response for the same dispatch token
// even after expiry. A verified callback can settle first and is never undone.
func (r *MessageRepository) CompleteOutboundDispatch(ctx context.Context, accountID uint, attemptID, token, providerID, status, disposition string) error {
	if accountID == 0 || !validRetryUUID(attemptID) || !validRetryUUID(token) {
		return gorm.ErrInvalidData
	}
	switch disposition {
	case "accepted":
		if providerID == "" || (outboundProgress(status) == 0 && status != domain.MessageStatusFailed) {
			return gorm.ErrInvalidData
		}
	case "rejected":
		if providerID != "" || status != domain.MessageStatusFailed {
			return gorm.ErrInvalidData
		}
	case "unknown":
		if status != domain.MessageStatusUnknown {
			return gorm.ErrInvalidData
		}
	default:
		return gorm.ErrInvalidData
	}
	return r.outboundDB(ctx).Transaction(func(tx *gorm.DB) error {
		var a domain.OutboundMessageAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", accountID, attemptID).First(&a).Error; err != nil {
			return err
		}
		if a.ClaimToken != token || a.DispatchStartedAt == nil || (a.State != domain.OutboundStateSending && a.State != domain.OutboundStateUnknown && a.State != domain.OutboundStateSettled) {
			return ErrOutboundClaimLost
		}
		// Only a first, definite rejection is retry-safe. Callback failures and
		// ambiguous network/expiry results cannot be upgraded to safe rejection.
		retrySafe := disposition == "rejected" && a.ProviderMessageID == "" && (a.State == domain.OutboundStateSending || (a.State == domain.OutboundStateSettled && a.RetrySafe))
		return persistOutboundResult(tx, a, providerID, status, retrySafe, disposition == "accepted", time.Now().UTC().Truncate(time.Microsecond))
	})
}

// ApplyOutboundStatus is called only after service-level signature validation.
// Compare every immutable field to the previously verified snapshot again in
// the MSG transaction; mutable state may legitimately have advanced meanwhile.
func (r *MessageRepository) ApplyOutboundStatus(ctx context.Context, expected domain.OutboundMessageAttempt, providerID, status string) error {
	if expected.AccountID == 0 || !validRetryUUID(expected.ID) || providerID == "" || (outboundProgress(status) == 0 && status != domain.MessageStatusFailed) {
		return gorm.ErrInvalidData
	}
	return r.outboundDB(ctx).Transaction(func(tx *gorm.DB) error {
		var a domain.OutboundMessageAttempt
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", expected.AccountID, expected.ID).First(&a).Error; err != nil {
			return err
		}
		if !outboundImmutableMatches(a, expected) || a.DispatchStartedAt == nil || (a.State != domain.OutboundStateSending && a.State != domain.OutboundStateUnknown && a.State != domain.OutboundStateSettled) {
			return ErrOutboundConflict
		}
		return persistOutboundResult(tx, a, providerID, status, false, true, time.Now().UTC().Truncate(time.Microsecond))
	})
}

func outboundProgress(status string) int {
	switch status {
	case domain.MessageStatusProviderAccepted:
		return 1
	case domain.MessageStatusSent:
		return 2
	case domain.MessageStatusDelivered:
		return 3
	case domain.MessageStatusRead:
		return 4
	}
	return 0
}

func mergeOutboundStatus(current, incoming string) string {
	if outboundProgress(current) > 0 && outboundProgress(current) >= outboundProgress(incoming) {
		return current
	}
	if current == domain.MessageStatusFailed && incoming == domain.MessageStatusUnknown {
		return current
	}
	return incoming
}

func persistOutboundResult(tx *gorm.DB, a domain.OutboundMessageAttempt, providerID, status string, retrySafe, providerEvidence bool, now time.Time) error {
	if providerID != "" && a.ProviderMessageID != "" && providerID != a.ProviderMessageID {
		return ErrOutboundConflict
	}
	if providerID == "" {
		providerID = a.ProviderMessageID
	}
	failureObserved := a.FailureObserved
	if providerEvidence && status == domain.MessageStatusFailed && outboundProgress(a.Status) < 3 {
		failureObserved = true
	}
	if providerEvidence && status == domain.MessageStatusFailed && outboundProgress(a.Status) < 2 {
		status = domain.MessageStatusFailed
	} else if a.FailureObserved && a.Status == domain.MessageStatusFailed && status == domain.MessageStatusProviderAccepted {
		status = domain.MessageStatusFailed
	} else {
		status = mergeOutboundStatus(a.Status, status)
	}
	if outboundProgress(status) >= 3 {
		failureObserved = false // Delivery/read evidence supersedes a sent-stage failure.
	}
	if providerID != "" || failureObserved || outboundProgress(status) > 0 || status != domain.MessageStatusFailed {
		retrySafe = false
	}
	state := domain.OutboundStateSettled
	if status == domain.MessageStatusUnknown {
		state = domain.OutboundStateUnknown
	} else if status == domain.MessageStatusCancelled {
		state = domain.OutboundStateCancelled
	}
	if err := tx.Model(&domain.OutboundMessageAttempt{}).Where("account_id = ? AND id = ?", a.AccountID, a.ID).Updates(map[string]any{"state": state, "status": status, "provider_message_id": providerID, "retry_safe": retrySafe, "failure_observed": failureObserved, "updated_at": now}).Error; err != nil {
		if isUniqueConstraintViolation(err) {
			return ErrOutboundConflict
		}
		return err
	}
	if a.Status != status {
		if err := recordOutboundAudit(tx, a, "outbound_status", a.Status, status, now); err != nil {
			return err
		}
	} else if failureObserved && !a.FailureObserved {
		if err := recordOutboundAudit(tx, a, "outbound_failure", a.Status, domain.MessageStatusFailed, now); err != nil {
			return err
		}
	}
	// A replayed rejection/failure of an older attempt must not cancel or
	// overwrite a subsequently accepted explicit retry or its diagnostics.
	var newer int64
	if err := tx.Model(&domain.OutboundMessageAttempt{}).Where("account_id = ? AND message_id = ? AND sequence > ?", a.AccountID, a.MessageID, a.Sequence).Count(&newer).Error; err != nil {
		return err
	}
	if newer != 0 && outboundProgress(status) == 0 {
		return nil
	}
	var m domain.Message
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND conversation_id = ? AND id = ?", a.AccountID, a.ConversationID, a.MessageID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil // Preserve the audit result without resurrecting deleted content.
	}
	if err != nil {
		return err
	}
	if !outboundMessageMatches(m, a, true) {
		return nil // Edits/private conversion/deletion freeze the visible message.
	}
	messageStatus := mergeOutboundStatus(m.Status, status)
	if failureObserved && status == domain.MessageStatusFailed && outboundProgress(m.Status) < 2 {
		messageStatus = domain.MessageStatusFailed
	}
	messageFailure := m.DeliveryFailed
	if newer == 0 {
		messageFailure = failureObserved
	}
	if outboundProgress(messageStatus) >= 3 {
		messageFailure = false
	}
	if m.Status == messageStatus && m.DeliveryFailed == messageFailure {
		return nil
	}
	return tx.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ? AND id = ? AND deleted = ? AND private = ? AND content = ? AND content_type = ? AND message_type = ? AND sender_type = ? AND sender_id = ? AND status = ?", a.AccountID, a.ConversationID, a.MessageID, false, false, a.Body, a.ContentType, a.MessageType, a.SenderType, a.ActorID, m.Status).Updates(map[string]any{"status": messageStatus, "delivery_failed": messageFailure, "updated_at": now}).Error
}

// Bounded audit values are deliberately constructed rather than serializing the
// private attempt. These actions are local audit only, never AUT trigger facts.
func recordOutboundAudit(tx *gorm.DB, a domain.OutboundMessageAttempt, action, before, after string, now time.Time) error {
	type auditState struct {
		AttemptID string `json:"attempt_id"`
		MessageID uint   `json:"message_id"`
		Status    string `json:"status"`
	}
	raw, err := json.Marshal(auditState{AttemptID: a.ID, MessageID: a.MessageID, Status: after})
	if err != nil {
		return err
	}
	change := domain.LocalChangeJournal{
		ChangeID: foundation.GenerateUUID(), SourceModule: "MSG", AccountID: a.AccountID,
		ObjectType: "Message", ObjectID: a.MessageID, Action: action, ChangedFields: "status",
		ActorType: "System", SourceType: "msg.outbound_attempt.v1", CorrelationID: a.ID,
		AfterState: string(raw), Status: "published", Result: after, OccurredAt: now,
	}
	if before != "" {
		raw, err = json.Marshal(auditState{AttemptID: a.ID, MessageID: a.MessageID, Status: before})
		if err != nil {
			return err
		}
		change.BeforeState = string(raw)
	}
	return NewJournalRepository(tx).RecordLocalChange(tx, &change)
}

func (r *MessageRepository) updateUntrackedMessageStatus(id uint, status string) error {
	if !r.db.Migrator().HasTable(&domain.OutboundMessageAttempt{}) {
		return r.db.Model(&domain.Message{}).Where("id = ?", id).Update("status", status).Error
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		var message domain.Message
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&message, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		var n int64
		if err := tx.Model(&domain.OutboundMessageAttempt{}).Where("account_id = ? AND message_id = ?", message.AccountID, message.ID).Count(&n).Error; err != nil {
			return err
		}
		if n != 0 {
			return ErrOutboundConflict
		}
		return tx.Model(&domain.Message{}).Where("account_id = ? AND id = ?", message.AccountID, message.ID).Update("status", status).Error
	})
}
