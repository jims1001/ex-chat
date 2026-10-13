package modruntime

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

// WidgetTranscriptBridge is an authenticated application boundary for Widget
// transcript admission and status. Generic MSG commands do not authorize visitors.
// The mapping lives on the MSG intent; no second private body survives cleanup.
type WidgetTranscriptBridge struct {
	db           *gorm.DB
	runtime      *Runtime
	enabled      bool
	inboxEnabled bool
	sqliteMu     sync.Mutex
}

func NewWidgetTranscriptBridge(db *gorm.DB, getenv func(string) string) (*WidgetTranscriptBridge, error) {
	flag := strings.TrimSpace(getenv("WIDGET_TRANSCRIPT_QUEUE_ENABLED"))
	if flag != "" && flag != "true" && flag != "false" {
		return nil, errors.New("WIDGET_TRANSCRIPT_QUEUE_ENABLED must be true or false")
	}
	inboxEnabled, err := loadInboxTranscriptAdmission(getenv)
	if err != nil {
		return nil, err
	}
	mail, err := LoadOrdinaryEmailConfig(getenv)
	if err != nil {
		return nil, err
	}
	runtimes := map[string]*Runtime{}
	for _, owner := range []string{"IAM", "TEN", "CUS", "CON", "MSG"} {
		cfg := Config{ModuleID: owner, ContractVersion: "v1", AcceptedVersions: map[string]bool{"v1": true}, AuthToken: foundation.GenerateUUID(), AllowedCallers: map[string]bool{"API": true, "MSG": true, "CON": true, "TEN": true}, MaxTimeout: 5 * time.Second, MaxRequestBytes: 1 << 20, EnableBusiness: true, Dependencies: map[string]DependencyConfig{}, OrdinaryEmail: mail}
		dependencies := map[string][]string{"TEN": {"IAM"}, "CON": {"TEN", "CUS"}, "MSG": {"CON", "TEN"}}
		for _, target := range dependencies[owner] {
			r := runtimes[target]
			cfg.Dependencies[target] = DependencyConfig{URL: "http://local-" + target, Token: r.Config.AuthToken, LocalHandler: r.Handler()}
		}
		r := &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry(owner)}
		if err := r.RegisterBusinessContracts(); err != nil {
			return nil, err
		}
		runtimes[owner] = r
	}
	return &WidgetTranscriptBridge{db: db, runtime: runtimes["MSG"], enabled: flag == "true", inboxEnabled: inboxEnabled}, nil
}

// Journal command/correlation fields are varchar(64) on PostgreSQL. Encode all
// 256 scope bits while retaining a reserved producer discriminator (61 bytes).
func widgetTranscriptCommandID(scope string) string {
	raw, _ := hex.DecodeString(scope)
	return "wdg-transcript-v1:" + base64.RawURLEncoding.EncodeToString(raw)
}

func widgetTranscriptIdentity(req domain.WidgetTranscriptRequest) (string, string) {
	scope, _ := json.Marshal([]any{"widget-transcript-v1", req.AccountID, req.InboxID, req.ContactID, req.SourceID, req.ConversationID, req.Key})
	input, _ := json.Marshal(struct{ Email string }{req.Email})
	return ordinaryPayloadHash(string(scope)), ordinaryPayloadHash(string(input))
}

func widgetTranscriptReplay(db *gorm.DB, req domain.WidgetTranscriptRequest, scope, input string) (*domain.WidgetTranscriptAdmission, bool, error) {
	var item domain.OrdinaryEmailIntent
	err := db.Select("email_log_id", "request_input_hash", "transcript_messages", "anonymized", "command_id").Where("account_id = ? AND request_scope_hash = ?", req.AccountID, scope).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, domain.ErrAccountTranscriptUnavailable
	}
	// Keep the hashed scope as a no-resend tombstone, without returning scrubbed
	// recipients or treating an anonymized key as permission for a fresh send.
	if !ordinaryWidgetTranscriptIdentity(item.CommandID, scope) {
		return nil, true, domain.ErrAccountTranscriptNotFound
	}
	if item.Anonymized || item.RequestInputHash != input {
		return nil, true, domain.ErrAccountTranscriptConflict
	}
	var log domain.EmailLog
	err = db.Select("id", "to_email").Where("account_id = ? AND id = ? AND conversation_id = ? AND managed_queue = ? AND user_id IS NULL", req.AccountID, item.EmailLogID, req.ConversationID, ordinaryEmailQueue).First(&log).Error
	if err != nil {
		return nil, true, domain.ErrAccountTranscriptUnavailable
	}
	return &domain.WidgetTranscriptAdmission{EmailLogID: log.ID, Email: log.ToEmail, MessagesCount: item.TranscriptMessages}, true, nil
}

func (b *WidgetTranscriptBridge) Submit(ctx context.Context, req domain.WidgetTranscriptRequest, authorize func(context.Context) error, prepare func(context.Context) (domain.WidgetTranscriptPrepared, error)) (result *domain.WidgetTranscriptAdmission, handled bool, err error) {
	if req.AccountID == 0 || req.InboxID == 0 || req.ContactID == 0 || req.SourceID == "" || authorize == nil || req.ConversationID == 0 || prepare == nil {
		return nil, true, domain.ErrAccountTranscriptInvalid
	}
	if req.Key != "" && (req.InvalidInput || !validAccountTranscriptKey(req.Key)) {
		return nil, true, domain.ErrAccountTranscriptInvalid
	}
	if !b.enabled && req.Key == "" {
		return nil, false, nil
	}
	if b.db.Dialector.Name() != "postgres" {
		b.sqliteMu.Lock()
		defer b.sqliteMu.Unlock()
	}
	ctx, cancel := context.WithTimeout(ctx, b.runtime.Config.MaxTimeout)
	defer cancel()
	scope, input := widgetTranscriptIdentity(req)
	// A lookup failure is never evidence that legacy SMTP is safe.
	handled = true
	err = ordinaryEmailPrivateDB(b.db.WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := repository.LockOrdinaryEmailAccount(tx, req.AccountID); err != nil {
			return err
		}
		if err := authorize(ctx); err != nil {
			return err
		}
		if req.Key != "" {
			replay, found, err := widgetTranscriptReplay(tx, req, scope, input)
			if found || err != nil {
				result = replay
				return err
			}
		}
		if !b.enabled {
			handled = false
			return nil
		}
		prepared, err := prepare(ctx)
		if err != nil {
			return err
		}
		if !prepared.Eligible || (prepared.InboxSelected && !b.inboxEnabled) {
			handled = false
			return nil
		}
		if req.InvalidInput || !validAccountTranscriptKey(req.Key) || prepared.Render == nil {
			return domain.ErrAccountTranscriptInvalid
		}
		if !b.runtime.Config.OrdinaryEmail.Enabled || !b.runtime.Config.OrdinaryEmail.valid() {
			return domain.ErrAccountTranscriptUnavailable
		}
		if prepared.InboxSelected {
			if prepared.InboxRoute == nil || domain.ValidateInboxSMTPBinding(*prepared.InboxRoute) != nil || prepared.InboxRoute.AccountID != strconv.FormatUint(uint64(req.AccountID), 10) || prepared.InboxRoute.InboxID != strconv.FormatUint(uint64(req.InboxID), 10) {
				return domain.ErrAccountTranscriptUnavailable
			}
		} else if prepared.InboxRoute != nil || prepared.From != b.runtime.Config.OrdinaryEmail.From {
			return domain.ErrAccountTranscriptUnavailable
		}
		if err := repository.LockMessageConversation(tx, req.AccountID, req.ConversationID, false); err != nil {
			return domain.ErrAccountTranscriptConflict
		}
		// Read MSG rows through the same fenced transaction. Enrichment keeps its
		// existing owner ports; this is not a cross-owner coherent snapshot claim.
		messages, _, err := repository.NewMessageRepositoryWithPorts(tx, repository.NewConversationRepository(b.db), repository.NewUserRepository(b.db), repository.NewContactRepository(b.db)).ListByConversation(req.AccountID, req.ConversationID, false, 1, 100)
		if err != nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		html, text := prepared.Render(messages)
		key := "widget-transcript-v1:" + scope
		command := foundation.CommandEnvelope{CommandID: widgetTranscriptCommandID(scope), CommandType: "msg.email.send.v1", AccountID: req.AccountID, Actor: foundation.ContractActor{Type: "Contact", ID: strconv.FormatUint(uint64(req.ContactID), 10), AuthSource: "visitor_session"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(req.ConversationID), 10)}, Input: map[string]any{"conversation_id": req.ConversationID, "email_type": "transcript", "to_email": prepared.To, "from_email": prepared.From, "subject": prepared.Subject, "content_html": html, "content_text": text}, IdempotencyKey: key, CorrelationID: widgetTranscriptCommandID(scope), CausationID: widgetTranscriptCommandID(scope), RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 5000, OnTimeout: "query", StatusQuery: "msg.email_delivery.get.v1"}}
		if prepared.InboxSelected {
			command.CommandID = widgetInboxTranscriptCommandID(scope)
			command.CorrelationID, command.CausationID = command.CommandID, command.CommandID
			command.Input[inboxTranscriptReceiptRoute] = *prepared.InboxRoute
		}

		if command.Validate() != nil {
			return domain.ErrAccountTranscriptInvalid
		}
		encoded, encodeErr := json.Marshal(command)
		if encodeErr != nil || int64(len(encoded)) > b.runtime.Config.MaxRequestBytes {
			return domain.ErrAccountTranscriptInvalid
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// A synchronous owner entry is deliberate: HTTP invoke returns on timeout
		// while its goroutine can still run. No detached work may outlive this tx.
		r := Runtime{Config: b.runtime.Config, DB: tx, Contracts: NewContractRegistry("MSG")}
		if err := r.RegisterBusinessContracts(); err != nil {
			return err
		}
		r.Contracts.UseCommandReceipts(tx)
		if failure := confirmationQueueRestriction(command); failure != nil {
			return failure
		}
		output, err := r.executeTranscriptAdmission(ctx, command, prepared.InboxRoute)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(output)
		if err != nil {
			return err
		}
		var admission struct {
			ID uint `json:"id"`
		}
		if json.Unmarshal(raw, &admission) != nil || admission.ID == 0 {
			return domain.ErrAccountTranscriptUnavailable
		}
		update := tx.Model(&domain.OrdinaryEmailIntent{}).Where("account_id = ? AND email_log_id = ? AND request_scope_hash IS NULL", req.AccountID, admission.ID).Updates(map[string]any{"request_scope_hash": scope, "request_input_hash": input, "transcript_messages": len(messages)})
		if update.Error != nil || update.RowsAffected != 1 {
			return domain.ErrAccountTranscriptUnavailable
		}
		result = &domain.WidgetTranscriptAdmission{EmailLogID: admission.ID, Email: strings.TrimSpace(prepared.To), MessagesCount: len(messages)}
		return nil
	})
	if err == nil {
		return result, handled, nil
	}
	// A lost commit acknowledgement can still leave one durable admission. Query
	// that identity only; never re-render, repeat admission or call direct SMTP.
	if req.Key != "" {
		readCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if authErr := authorize(readCtx); authErr != nil {
			return nil, true, authErr
		}
		if replay, found, lookupErr := widgetTranscriptReplay(ordinaryEmailPrivateDB(b.db.WithContext(readCtx)), req, scope, input); found && lookupErr == nil {
			return replay, true, nil
		}
	}
	if errors.Is(err, domain.ErrWidgetTranscriptForbidden) {
		return nil, true, err
	}
	return nil, true, accountTranscriptError(err)
}

// Status requires the original scoped key and exact conversation/log IDs. It
// authorizes first and returns only MSG's body-free projection, never replay data.
func (b *WidgetTranscriptBridge) Status(ctx context.Context, req domain.WidgetTranscriptRequest, logID uint, authorize func(context.Context) error) (map[string]any, error) {
	if req.AccountID == 0 || req.InboxID == 0 || req.ContactID == 0 || req.SourceID == "" || req.ConversationID == 0 || logID == 0 || authorize == nil || req.InvalidInput || !validAccountTranscriptKey(req.Key) {
		return nil, domain.ErrAccountTranscriptInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, b.runtime.Config.MaxTimeout)
	defer cancel()
	if err := authorize(ctx); err != nil {
		return nil, err
	}
	scope, _ := widgetTranscriptIdentity(req)
	var item domain.OrdinaryEmailIntent
	db := ordinaryEmailPrivateDB(b.db.WithContext(ctx))
	err := db.Select("email_log_id", "command_id", "anonymized").Where("account_id = ? AND request_scope_hash = ? AND email_log_id = ?", req.AccountID, scope, logID).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrAccountTranscriptNotFound
	}
	if err != nil {
		return nil, domain.ErrAccountTranscriptUnavailable
	}
	if item.Anonymized || !ordinaryWidgetTranscriptIdentity(item.CommandID, scope) {
		return nil, domain.ErrAccountTranscriptNotFound
	}
	var log domain.EmailLog
	err = db.Select("id", "account_id", "conversation_id", "email_type", "managed_queue", "status", "delivery_status", "retry_count", "max_retries", "next_retry_at", "last_attempt_at", "created_at", "updated_at").Where("account_id = ? AND id = ? AND conversation_id = ? AND managed_queue = ? AND user_id IS NULL", req.AccountID, logID, req.ConversationID, ordinaryEmailQueue).First(&log).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, domain.ErrAccountTranscriptNotFound
	}
	if err != nil {
		return nil, domain.ErrAccountTranscriptUnavailable
	}
	r := Runtime{DB: db, Config: b.runtime.Config}
	result, err := r.ordinaryEmailStatus(log)
	if err != nil {
		return nil, accountTranscriptError(err)
	}
	return result, nil
}
