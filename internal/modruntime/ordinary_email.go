package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const ordinaryEmailLease = 60 * time.Second // Greater than the transport's whole-operation 30s deadline.
var errOrdinaryEmailStore = errors.New("ordinary email checkpoint is unavailable")
var errOrdinaryEmailFence = errors.New("ordinary email attempt no longer owns submission")

type ordinaryEmailPayload struct{ From, To, Subject, HTML, Text string }

func ordinaryPayloadHash(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
func ordinaryEmailType(kind string) bool { return kind == "transcript" || kind == "notification" }

func (r Runtime) enqueueOrdinaryEmail(ctx context.Context, cmd foundation.CommandEnvelope, log *domain.EmailLog) error {
	return r.enqueueOrdinaryEmailRoute(ctx, cmd, log, nil)
}

func (r Runtime) enqueueOrdinaryEmailRoute(ctx context.Context, cmd foundation.CommandEnvelope, log *domain.EmailLog, route *domain.InboxSMTPBinding) error {
	if !ordinaryEmailType(log.EmailType) {
		return nil
	}
	if !r.Config.OrdinaryEmail.Enabled || !r.Config.OrdinaryEmail.valid() {
		return &ContractFailure{Type: "dependency_unavailable", Message: "ordinary email global SMTP configuration is unavailable"}
	}
	if route == nil && log.FromEmail != r.Config.OrdinaryEmail.From {
		return &ContractFailure{Type: "validation_failed", Message: "from_email must match the configured global SMTP sender"}
	}
	if route != nil && (domain.ValidateInboxSMTPBinding(*route) != nil || route.AccountID != fmt.Sprint(log.AccountID) || log.EmailType != "transcript" || !validInboxTranscriptCommand(cmd)) {
		return &ContractFailure{Type: "validation_failed", Message: "invalid internal transcript route"}
	}
	if strings.ContainsAny(log.Subject, "\r\n\x00") {
		return &ContractFailure{Type: "validation_failed", Message: "email subject is invalid"}
	}
	payload, _ := json.Marshal(ordinaryEmailPayload{From: log.FromEmail, To: log.ToEmail, Subject: log.Subject, HTML: log.ContentHTML, Text: log.ContentText})
	log.ManagedQueue = ordinaryEmailQueue
	// Public audit/list models do not carry this queue's immutable private body.
	log.ContentHTML, log.ContentText = "", ""
	tx := ordinaryEmailPrivateDB(Transaction(ctx, r.DB))
	if err := repository.LockOrdinaryEmailAccount(tx, log.AccountID); err != nil {
		return errOrdinaryEmailStore
	}
	if log.ConversationID != nil {
		if err := repository.LockMessageConversation(tx, log.AccountID, *log.ConversationID, false); err != nil {
			if errors.Is(err, repository.ErrMessageConversationDeleted) {
				return &ContractFailure{Type: "state_conflict", Message: "email conversation was deleted"}
			}
			return errOrdinaryEmailStore
		}
	}

	if err := tx.Create(log).Error; err != nil {
		return errOrdinaryEmailStore
	}
	// A pre-upgrade legacy worker does not know managed_queue. Its existing
	// retry_count < max_retries predicate must also reject this new record.
	// GORM applies the model's default 3 on Create, so write zero explicitly.
	if err := tx.Model(log).UpdateColumn("max_retries", 0).Error; err != nil {
		return errOrdinaryEmailStore
	}
	log.MaxRetries = 0

	intent := domain.OrdinaryEmailIntent{AccountID: log.AccountID, EmailLogID: log.ID, EmailType: log.EmailType, Payload: string(payload), PayloadHash: ordinaryPayloadHash(string(payload)), TransportKey: r.Config.OrdinaryEmail.key(), CommandID: cmd.CommandID, CorrelationID: cmd.CorrelationID, State: "pending", MaxAttempts: 4, CreatedAt: log.CreatedAt, UpdatedAt: log.UpdatedAt}
	if route != nil {
		intent.TransportKey = ""
		intent.RouteKind, intent.RouteAccountID, intent.RouteInboxID = route.Kind, route.AccountID, route.InboxID
		intent.RouteCreatedAt, intent.RouteFingerprint = route.CreatedAt, route.Fingerprint
	}

	if err := tx.Create(&intent).Error; err != nil {
		return errOrdinaryEmailStore
	}
	return nil
}

func (r Runtime) RunOrdinaryEmail(ctx context.Context, onError func(error)) {
	if r.Config.ModuleID != "MSG" || !r.Config.OrdinaryEmail.Enabled {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := r.RunOrdinaryEmailOnce(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(errOrdinaryEmailStore)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// RunOrdinaryEmailOnce owns at most one bounded SMTP connection. It never adopts
// old EmailLogs. The only retried effects are proven to precede DATA submission.
func (r Runtime) RunOrdinaryEmailOnce(ctx context.Context) (bool, error) {
	if r.Config.ModuleID != "MSG" || !r.Config.OrdinaryEmail.Enabled {
		return false, nil
	}
	if r.DB == nil || r.OrdinaryEmailTransport == nil || !r.Config.OrdinaryEmail.valid() {
		return false, errOrdinaryEmailStore
	}
	intent, claimed, err := r.claimOrdinaryEmail(ctx)
	if err != nil || !claimed {
		return claimed, err
	}
	if intent.State != "leased" {
		return true, nil
	} // Recovered uncertain submission, never send.
	var body ordinaryEmailPayload
	if json.Unmarshal([]byte(intent.Payload), &body) != nil || !ordinaryEmailType(intent.EmailType) || ordinaryPayloadHash(intent.Payload) != intent.PayloadHash || intent.Anonymized {
		return true, r.completeOrdinaryEmail(ctx, intent, "failed", "invalid_intent_or_transport", false)
	}
	inbox := intent.RouteKind == "inbox_smtp_v1"
	if inbox {
		binding := ordinaryIntentInboxBinding(intent)
		if r.InboxTranscriptSMTP == nil || domain.ValidateInboxSMTPBinding(binding) != nil || binding.AccountID != fmt.Sprint(intent.AccountID) || intent.TransportKey != "" || intent.EmailType != "transcript" || intent.RequestScopeHash == nil || !ordinaryRequestHash(*intent.RequestScopeHash) || !ordinaryRequestHash(intent.RequestInputHash) || !ordinaryInboxTranscriptCommand(intent.CommandID) || (!ordinaryAccountTranscriptIdentity(intent.CommandID, *intent.RequestScopeHash) && !ordinaryWidgetTranscriptIdentity(intent.CommandID, *intent.RequestScopeHash)) || intent.CorrelationID != intent.CommandID {
			return true, r.completeOrdinaryEmail(ctx, intent, "failed", "invalid_intent_or_transport", false)
		}
	} else if !ordinaryIntentGlobalRoute(intent) || intent.TransportKey != r.Config.OrdinaryEmail.key() || body.From != r.Config.OrdinaryEmail.From {
		return true, r.completeOrdinaryEmail(ctx, intent, "failed", "invalid_intent_or_transport", false)
	}
	submissionStarted := false
	sendContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	beforeSubmit := func() error {
		if err := sendContext.Err(); err != nil {
			return err
		}
		if err := r.startOrdinarySubmission(sendContext, intent); err != nil {
			return err
		}
		submissionStarted = true
		return nil
	}
	var result domain.OrdinarySMTPResult
	if inbox {
		result = r.InboxTranscriptSMTP.SendInboxTranscript(sendContext, ordinaryIntentInboxBinding(intent), body.From, body.To, body.Subject, body.HTML, body.Text, beforeSubmit)
	} else {
		result = r.OrdinaryEmailTransport.SendOrdinary(sendContext, body.From, body.To, body.Subject, body.HTML, body.Text, beforeSubmit)
	}
	state, code, retry := "unknown", "submission_outcome_unknown", false
	switch result.Outcome {
	case domain.OrdinarySMTPAccepted:
		if submissionStarted {
			state, code = "sent", ""
		}
	case domain.OrdinarySMTPPreSubmissionTransient:
		if !submissionStarted {
			state, code, retry = "pending", "pre_submission_transient", true
		}
	case domain.OrdinarySMTPAuthenticationFailure:
		if !submissionStarted {
			state, code = "failed", "smtp_authentication_failed"
		}
	case domain.OrdinarySMTPPermanentFailure:
		if !submissionStarted {
			state, code = "failed", "smtp_permanent_failure"
		}
	case domain.OrdinarySMTPCancelledBeforeSubmission:
		if !submissionStarted {
			state, code, retry = "pending", "pre_submission_cancelled", true
		}
	}
	if retry && intent.AttemptCount >= intent.MaxAttempts {
		state, code, retry = "failed", "attempts_exhausted", false
	}
	// Cancellation stops the socket, not the outcome transaction. A separate short
	// context lets shutdown durably settle, while failed saves retain the marker.
	persistCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	return true, r.completeOrdinaryEmail(persistCtx, intent, state, code, retry)
}

func (r Runtime) claimOrdinaryEmail(ctx context.Context) (domain.OrdinaryEmailIntent, bool, error) {
	var item domain.OrdinaryEmailIntent
	claimed := false
	now := time.Now().UTC()
	err := ordinaryEmailPrivateDB(r.DB.WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		q := tx.Where("anonymized = ? AND ((state = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)) OR (state IN ? AND lease_until <= ?))", false, "pending", now, []string{"leased", "submitting"}, now).Order("id ASC")
		if r.InboxTranscriptSMTP == nil {
			// Capability filtering occurs in the claim query. A same-version
			// standalone worker cannot even recover/settle an Inbox attempt.
			q = q.Where("(route_kind IS NULL OR route_kind = '') AND (route_account_id IS NULL OR route_account_id = '') AND (route_inbox_id IS NULL OR route_inbox_id = '') AND (route_created_at IS NULL OR route_created_at = '') AND (route_fingerprint IS NULL OR route_fingerprint = '') AND transport_key <> '' AND (request_scope_hash IS NULL OR (command_id NOT LIKE ? AND command_id NOT LIKE ?))", "acct-inbox-v1:%", "wdg-inbox-v1:%")
		}
		if tx.Dialector.Name() == "postgres" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
		}
		if err := q.First(&item).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		if item.State == "submitting" {
			claimed = true
			return r.finishOrdinaryTx(tx, &item, "unknown", "worker_lost_after_submission", false, now)
		}
		if item.State == "leased" {
			u := tx.Model(&domain.OrdinaryEmailAttempt{}).Where("id = ? AND intent_id = ? AND account_id = ? AND lease_token = ? AND state = ?", item.LatestAttemptID, item.ID, item.AccountID, item.LeaseToken, "leased").Updates(map[string]any{"state": "abandoned", "completed_at": now, "error_code": "lease_expired_before_submission", "updated_at": now})
			if u.Error != nil || u.RowsAffected != 1 {
				return errOrdinaryEmailFence
			}
		}
		if !ordinaryEmailType(item.EmailType) || item.MaxAttempts != 4 || item.AttemptCount < 0 || item.AttemptCount >= item.MaxAttempts {
			claimed = true
			return r.finishOrdinaryTx(tx, &item, "failed", "attempts_exhausted", false, now)
		}
		token, id := foundation.GenerateUUID(), foundation.GenerateUUID()
		until := now.Add(ordinaryEmailLease)
		update := tx.Model(&domain.OrdinaryEmailIntent{}).Where("id = ? AND account_id = ? AND state = ? AND attempt_count = ? AND lease_token = ?", item.ID, item.AccountID, item.State, item.AttemptCount, item.LeaseToken).Updates(map[string]any{"state": "leased", "attempt_count": item.AttemptCount + 1, "latest_attempt_id": id, "lease_token": token, "lease_until": until, "next_attempt_at": nil, "error_code": "", "updated_at": now})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected != 1 {
			return nil
		}
		item.State = "leased"
		item.AttemptCount++
		item.LatestAttemptID = id
		item.LeaseToken = token
		item.LeaseUntil = &until
		item.NextAttemptAt = nil
		item.ErrorCode = ""
		item.UpdatedAt = now
		attempt := domain.OrdinaryEmailAttempt{ID: id, IntentID: item.ID, AccountID: item.AccountID, Sequence: item.AttemptCount, State: "leased", LeaseToken: token, LeaseUntil: until, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&attempt).Error; err != nil {
			return err
		}
		claimed = true
		return r.mirrorOrdinaryEmail(tx, item, now)
	})
	if err != nil {
		return item, false, errOrdinaryEmailStore
	}
	return item, claimed, nil
}

func (r Runtime) startOrdinarySubmission(ctx context.Context, item domain.OrdinaryEmailIntent) error {
	now := time.Now().UTC()
	err := ordinaryEmailPrivateDB(r.DB.WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		u := tx.Model(&domain.OrdinaryEmailIntent{}).Where("id = ? AND account_id = ? AND state = ? AND latest_attempt_id = ? AND lease_token = ? AND lease_until > ? AND anonymized = ?", item.ID, item.AccountID, "leased", item.LatestAttemptID, item.LeaseToken, now, false).Updates(map[string]any{"state": "submitting", "updated_at": now})
		if u.Error != nil || u.RowsAffected != 1 {
			return errOrdinaryEmailFence
		}
		u = tx.Model(&domain.OrdinaryEmailAttempt{}).Where("id = ? AND intent_id = ? AND account_id = ? AND lease_token = ? AND state = ?", item.LatestAttemptID, item.ID, item.AccountID, item.LeaseToken, "leased").Updates(map[string]any{"state": "submitting", "submitting_at": now, "updated_at": now})
		if u.Error != nil || u.RowsAffected != 1 {
			return errOrdinaryEmailFence
		}
		item.State = "submitting"
		return r.mirrorOrdinaryEmail(tx, item, now)
	})
	if err != nil {
		return errOrdinaryEmailFence
	}
	return nil
}

func (r Runtime) completeOrdinaryEmail(ctx context.Context, item domain.OrdinaryEmailIntent, state, code string, retry bool) error {
	err := ordinaryEmailPrivateDB(r.DB.WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		var current domain.OrdinaryEmailIntent
		q := tx.Where("id = ? AND account_id = ? AND latest_attempt_id = ? AND lease_token = ? AND state IN ?", item.ID, item.AccountID, item.LatestAttemptID, item.LeaseToken, []string{"leased", "submitting"})
		if tx.Dialector.Name() == "postgres" {
			q = q.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := q.First(&current).Error; err != nil {
			return err
		}
		// A lost marker-commit acknowledgement can make the caller believe the
		// hook failed even though submitting committed. Preserve that evidence.
		if current.State == "submitting" && (state != "sent" && state != "unknown") {
			state, code, retry = "unknown", "submission_outcome_unknown", false
		}
		return r.finishOrdinaryTx(tx, &current, state, code, retry, time.Now().UTC())
	})
	if err != nil {
		return errOrdinaryEmailStore
	}
	return nil
}

func (r Runtime) finishOrdinaryTx(tx *gorm.DB, item *domain.OrdinaryEmailIntent, state, code string, retry bool, now time.Time) error {
	var next *time.Time
	if retry {
		// Bounded exponential delay (5m,10m,20m) with a stable 0–30s attempt jitter.
		h := sha256.Sum256([]byte(item.LatestAttemptID))
		n := now.Add(time.Duration(5*(1<<max(0, min(item.AttemptCount-1, 2))))*time.Minute + time.Duration(h[0]%31)*time.Second)
		next = &n
	}
	prior := item.State
	u := tx.Model(&domain.OrdinaryEmailIntent{}).Where("id = ? AND account_id = ? AND state = ? AND latest_attempt_id = ? AND lease_token = ?", item.ID, item.AccountID, prior, item.LatestAttemptID, item.LeaseToken).Updates(map[string]any{"state": state, "lease_token": "", "lease_until": nil, "next_attempt_at": next, "error_code": code, "updated_at": now})
	if u.Error != nil || u.RowsAffected != 1 {
		return errOrdinaryEmailFence
	}
	if item.LatestAttemptID != "" && (prior == "leased" || prior == "submitting") {
		attemptState := state
		if retry {
			attemptState = "pre_submission_failed"
		}
		u = tx.Model(&domain.OrdinaryEmailAttempt{}).Where("id = ? AND intent_id = ? AND account_id = ? AND lease_token = ? AND state IN ?", item.LatestAttemptID, item.ID, item.AccountID, item.LeaseToken, []string{"leased", "submitting", "abandoned"}).Updates(map[string]any{"state": attemptState, "completed_at": now, "error_code": code, "updated_at": now})
		if u.Error != nil || u.RowsAffected != 1 {
			return errOrdinaryEmailFence
		}
	}
	item.State = state
	item.LeaseToken = ""
	item.LeaseUntil = nil
	item.NextAttemptAt = next
	item.ErrorCode = code
	item.UpdatedAt = now
	return r.mirrorOrdinaryEmail(tx, *item, now)
}

func (r Runtime) mirrorOrdinaryEmail(tx *gorm.DB, item domain.OrdinaryEmailIntent, now time.Time) error {
	changes := map[string]any{"max_retries": 0, "status": item.State, "delivery_status": item.State, "retry_count": max(0, item.AttemptCount-1), "next_retry_at": item.NextAttemptAt, "error": item.ErrorCode, "updated_at": now}
	if item.State == "leased" && item.AttemptCount > 0 {
		changes["last_attempt_at"] = now
	}
	u := tx.Model(&domain.EmailLog{}).Where("id = ? AND account_id = ? AND managed_queue = ?", item.EmailLogID, item.AccountID, ordinaryEmailQueue).Updates(changes)
	if u.Error != nil || u.RowsAffected != 1 {
		return errOrdinaryEmailStore
	}
	command := foundation.CommandEnvelope{AccountID: item.AccountID, CommandID: item.CommandID, CorrelationID: item.CorrelationID, Actor: foundation.ContractActor{Type: "System", ID: "ordinary-email-worker", AuthSource: "module"}}
	return r.appendBusinessEvent(context.WithValue(context.Background(), transactionKey{}, tx), command, "msg.email_delivery.changed.v1", "EmailLog", fmt.Sprint(item.EmailLogID), []string{"delivery_status"}, map[string]any{"email_log_id": item.EmailLogID, "email_type": item.EmailType, "delivery_status": item.State, "attempt_count": item.AttemptCount, "error_code": item.ErrorCode})
}

func (r Runtime) ordinaryEmailStatus(email domain.EmailLog) (map[string]any, error) {
	result := map[string]any{"id": email.ID, "account_id": email.AccountID, "conversation_id": email.ConversationID, "email_type": email.EmailType, "status": email.Status, "delivery_status": email.DeliveryStatus, "retry_count": email.RetryCount, "max_retries": email.MaxRetries, "next_retry_at": email.NextRetryAt, "last_attempt_at": email.LastAttemptAt, "created_at": email.CreatedAt, "updated_at": email.UpdatedAt}
	if email.ManagedQueue != "" {
		// One joined SQL statement observes one committed status version. A
		// concurrent outcome cannot mix an old log with a newer queue state.
		var view struct {
			ID              uint
			AccountID       uint
			ConversationID  *uint
			EmailType       string
			ManagedQueue    string
			State           string
			AttemptCount    int
			MaxAttempts     int
			LatestAttemptID string
			ErrorCode       string
			NextAttemptAt   *time.Time
			LastAttemptAt   *time.Time
			CreatedAt       time.Time
			UpdatedAt       time.Time
		}
		err := r.DB.Table("ordinary_email_intents AS i").Select("e.id, e.account_id, e.conversation_id, e.email_type, e.managed_queue, i.state, i.attempt_count, i.max_attempts, i.latest_attempt_id, i.error_code, i.next_attempt_at, e.last_attempt_at, i.created_at, i.updated_at").Joins("JOIN email_logs AS e ON e.id = i.email_log_id AND e.account_id = i.account_id").Where("i.account_id = ? AND i.email_log_id = ? AND e.managed_queue = ?", email.AccountID, email.ID, ordinaryEmailQueue).Take(&view).Error
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "email delivery checkpoint is unavailable"}
		}
		result = map[string]any{"id": view.ID, "account_id": view.AccountID, "conversation_id": view.ConversationID, "email_type": view.EmailType, "status": view.State, "delivery_status": view.State, "retry_count": max(0, view.AttemptCount-1), "max_retries": 0, "next_retry_at": view.NextAttemptAt, "last_attempt_at": view.LastAttemptAt, "created_at": view.CreatedAt, "updated_at": view.UpdatedAt, "managed_queue": view.ManagedQueue, "queue_state": view.State, "attempt_count": view.AttemptCount, "max_attempts": view.MaxAttempts, "latest_attempt_id": view.LatestAttemptID, "error_code": view.ErrorCode, "automatic_retry_scheduled": view.State == "pending" && view.NextAttemptAt != nil, "manual_retry_allowed": false}
	}
	return result, nil
}

// Expanded SQL may contain private MIME payload and recipient fields. Scope the
// silent logger to this new queue; caller-visible errors remain fixed codes.
func ordinaryEmailPrivateDB(db *gorm.DB) *gorm.DB {
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}
