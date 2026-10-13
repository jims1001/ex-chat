package modruntime

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

// AccountTranscriptBridge is application composition for the existing MSG
// contract, not a remote cutover or a new SMTP implementation. The mapping lives
// on the MSG intent, so no second private transcript survives owner cleanup.
type AccountTranscriptBridge struct {
	db            *gorm.DB
	runtime       *Runtime
	statusHandler http.Handler
	enabled       bool
	inboxEnabled  bool
	sqliteMu      sync.Mutex
}

func NewAccountTranscriptBridge(db *gorm.DB, getenv func(string) string) (*AccountTranscriptBridge, error) {
	flag := strings.TrimSpace(getenv("ACCOUNT_TRANSCRIPT_QUEUE_ENABLED"))
	if flag != "" && flag != "true" && flag != "false" {
		return nil, errors.New("ACCOUNT_TRANSCRIPT_QUEUE_ENABLED must be true or false")
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
	return &AccountTranscriptBridge{db: db, runtime: runtimes["MSG"], statusHandler: runtimes["MSG"].Handler(), enabled: flag == "true", inboxEnabled: inboxEnabled}, nil
}

func validAccountTranscriptKey(key string) bool {
	if len(key) < 8 || len(key) > 128 {
		return false
	}
	for _, ch := range key {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("._:-", ch)) {
			return false
		}
	}
	return true
}

// Journal command/correlation fields are varchar(64) on PostgreSQL. Encode all
// 256 scope bits while retaining a reserved producer discriminator (62 bytes).
func accountTranscriptCommandID(scope string) string {
	raw, _ := hex.DecodeString(scope)
	return "acct-transcript-v1:" + base64.RawURLEncoding.EncodeToString(raw)
}

func accountTranscriptIdentity(req domain.AccountTranscriptRequest) (string, string) {
	scope, _ := json.Marshal([]any{"account-transcript-v1", req.AccountID, req.UserID, req.ConversationID, req.Key})
	input, _ := json.Marshal(struct{ Email string }{req.Email})
	return ordinaryPayloadHash(string(scope)), ordinaryPayloadHash(string(input))
}

func accountTranscriptReplay(db *gorm.DB, req domain.AccountTranscriptRequest, scope, input string) (*domain.AccountTranscriptAdmission, bool, error) {
	var item domain.OrdinaryEmailIntent
	err := db.Select("email_log_id", "request_input_hash", "transcript_messages", "anonymized").Where("account_id = ? AND request_scope_hash = ?", req.AccountID, scope).First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, domain.ErrAccountTranscriptUnavailable
	}
	// Keep the hashed scope as a no-resend tombstone, without returning scrubbed
	// recipients or treating an anonymized key as permission for a fresh send.
	if item.Anonymized || item.RequestInputHash != input {
		return nil, true, domain.ErrAccountTranscriptConflict
	}
	var log domain.EmailLog
	err = db.Select("id", "to_email").Where("account_id = ? AND id = ? AND managed_queue = ?", req.AccountID, item.EmailLogID, ordinaryEmailQueue).First(&log).Error
	if err != nil {
		return nil, true, domain.ErrAccountTranscriptUnavailable
	}
	return &domain.AccountTranscriptAdmission{EmailLogID: log.ID, Email: log.ToEmail, MessagesCount: item.TranscriptMessages}, true, nil
}

func (b *AccountTranscriptBridge) Submit(ctx context.Context, req domain.AccountTranscriptRequest, prepare func(context.Context) (domain.AccountTranscriptPrepared, error)) (result *domain.AccountTranscriptAdmission, handled bool, err error) {
	if req.AccountID == 0 || req.UserID == 0 || req.ConversationID == 0 || prepare == nil {
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
	scope, input := accountTranscriptIdentity(req)
	// A lookup failure is never evidence that legacy SMTP is safe.
	handled = true
	err = ordinaryEmailPrivateDB(b.db.WithContext(ctx)).Transaction(func(tx *gorm.DB) error {
		if err := repository.LockOrdinaryEmailAccount(tx, req.AccountID); err != nil {
			return err
		}
		if req.Key != "" {
			replay, found, err := accountTranscriptReplay(tx, req, scope, input)
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
			if prepared.InboxRoute == nil || domain.ValidateInboxSMTPBinding(*prepared.InboxRoute) != nil || prepared.InboxRoute.AccountID != strconv.FormatUint(uint64(req.AccountID), 10) {
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
		messages, _, err := repository.NewMessageRepositoryWithPorts(tx, repository.NewConversationRepository(b.db), repository.NewUserRepository(b.db), repository.NewContactRepository(b.db)).ListByConversation(req.AccountID, req.ConversationID, true, 1, 200)
		if err != nil {
			return domain.ErrAccountTranscriptUnavailable
		}
		html, text := prepared.Render(messages)
		key := "account-transcript-v1:" + scope
		command := foundation.CommandEnvelope{CommandID: accountTranscriptCommandID(scope), CommandType: "msg.email.send.v1", AccountID: req.AccountID, Actor: foundation.ContractActor{Type: "User", ID: strconv.FormatUint(uint64(req.UserID), 10), AuthSource: "jwt"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(req.ConversationID), 10)}, Input: map[string]any{"conversation_id": req.ConversationID, "user_id": req.UserID, "email_type": "transcript", "to_email": prepared.To, "from_email": prepared.From, "subject": prepared.Subject, "content_html": html, "content_text": text}, IdempotencyKey: key, CorrelationID: accountTranscriptCommandID(scope), CausationID: accountTranscriptCommandID(scope), RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 5000, OnTimeout: "query", StatusQuery: "msg.email_delivery.get.v1"}}
		if prepared.InboxSelected {
			command.CommandID = accountInboxTranscriptCommandID(scope)
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
		result = &domain.AccountTranscriptAdmission{EmailLogID: admission.ID, Email: strings.TrimSpace(prepared.To), MessagesCount: len(messages)}
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
		if replay, found, lookupErr := accountTranscriptReplay(ordinaryEmailPrivateDB(b.db.WithContext(readCtx)), req, scope, input); found && lookupErr == nil {
			return replay, true, nil
		}
	}
	return nil, true, accountTranscriptError(err)
}
func accountTranscriptError(err error) error {
	for _, known := range []error{domain.ErrAccountTranscriptInvalid, domain.ErrAccountTranscriptConflict, domain.ErrAccountTranscriptNotFound} {
		if errors.Is(err, known) {
			return known
		}
	}
	var failure *ContractFailure
	if errors.As(err, &failure) {
		switch failure.Type {
		case "validation_failed":
			return domain.ErrAccountTranscriptInvalid
		case "state_conflict":
			return domain.ErrAccountTranscriptConflict
		case "resource_not_found":
			return domain.ErrAccountTranscriptNotFound
		}
	}
	return domain.ErrAccountTranscriptUnavailable
}
func (b *AccountTranscriptBridge) Status(ctx context.Context, accountID, userID, logID uint) (map[string]any, error) {
	query := foundation.QueryEnvelope{QueryType: "msg.email_delivery.get.v1", AccountID: accountID, Actor: foundation.ContractActor{Type: "User", ID: strconv.FormatUint(uint64(userID), 10), AuthSource: "jwt"}, Filter: map[string]any{"id": logID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if query.Validate() != nil {
		return nil, domain.ErrAccountTranscriptInvalid
	}
	var result map[string]any
	dependency := DependencyConfig{URL: "http://local-MSG", Token: b.runtime.Config.AuthToken, LocalHandler: b.statusHandler}
	if err := newModuleClient("API", dependency, "v1", b.runtime.Config.MaxTimeout).query(ctx, query, &result); err != nil {
		return nil, accountTranscriptError(err)
	}
	return result, nil
}
