package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SAMLRequestMaxTTL = 5 * time.Minute

var (
	ErrSAMLRequestInvalid     = errors.New("SAML request is invalid, expired, or already consumed")
	ErrSAMLStorageUnavailable = errors.New("SAML request storage is unavailable")
	samlRequestIDPattern      = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,255}$`)
)

// SAMLConfigurationBinding must come from a trusted, currently enabled
// configuration. It is deliberately explicit: this isolated primitive does not
// implement configuration lifecycle fencing or read legacy SAML settings.
// A caller must atomically fence lifecycle changes before enabling login.
type SAMLConfigurationBinding struct {
	AccountID     uint
	ConfigVersion uint64
	ConfigDigest  string
}

// SAMLRequestBinding is non-secret correlation data for the verifier. A successful
// lookup does not authenticate a caller, consume a request, or verify an assertion.
type SAMLRequestBinding struct {
	SAMLConfigurationBinding
	RequestID string
	ExpiresAt time.Time
}

// SAMLPreparedRequest returns fresh 256-bit secrets exactly once. The caller must
// bind BrowserSecret to the initiating browser using a secure HttpOnly cookie;
// RelayState alone must never authorize a callback. This type must not be logged.
type SAMLPreparedRequest struct {
	SAMLRequestBinding
	RelayState    string
	BrowserSecret string
}

// SAMLRequestRepository uses an IAM database and its durable unique constraints.
// It has no in-memory security fallback and does not issue tokens or bind users.
type SAMLRequestRepository struct {
	db     *gorm.DB
	now    func() time.Time
	random io.Reader
}

func NewSAMLRequestRepository(db *gorm.DB) *SAMLRequestRepository {
	return &SAMLRequestRepository{db: db, now: time.Now, random: rand.Reader}
}

func (r *SAMLRequestRepository) available(ctx context.Context) bool {
	return r != nil && r.db != nil && r.now != nil && r.random != nil && ctx != nil
}

func validSAMLConfiguration(config SAMLConfigurationBinding) bool {
	if config.AccountID == 0 || config.ConfigVersion == 0 || len(config.ConfigDigest) != 64 || strings.ToLower(config.ConfigDigest) != config.ConfigDigest {
		return false
	}
	_, err := hex.DecodeString(config.ConfigDigest)
	return err == nil
}

func samlSecretDigest(secret string) (string, error) {
	// Reject noncanonical encodings as well as undersized/oversized secrets.
	if len(secret) != 43 {
		return "", ErrSAMLRequestInvalid
	}
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != secret {
		return "", ErrSAMLRequestInvalid
	}
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:]), nil
}

func samlReplayDigest(kind, issuer, id string) string {
	// Separators remove ambiguous concatenations; NUL inputs are rejected.
	// The kind separates assertion/response namespaces even for identical IDs.
	hash := sha256.New()
	_, _ = hash.Write([]byte(kind + "\x00"))
	_, _ = hash.Write([]byte(issuer))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(id))
	return hex.EncodeToString(hash.Sum(nil))
}

// Prepare persists a provider-generated AuthnRequest ID and independently random
// relay/browser secrets. Secrets are returned only after a successful DB write.
func (r *SAMLRequestRepository) Prepare(ctx context.Context, requestID string, config SAMLConfigurationBinding, ttl time.Duration) (*SAMLPreparedRequest, error) {
	if !r.available(ctx) {
		return nil, ErrSAMLStorageUnavailable
	}
	if !samlRequestIDPattern.MatchString(requestID) || !validSAMLConfiguration(config) || ttl <= 0 || ttl > SAMLRequestMaxTTL {
		return nil, ErrSAMLRequestInvalid
	}
	secrets := make([]byte, 64)
	if _, err := io.ReadFull(r.random, secrets); err != nil {
		return nil, err
	}
	relay := base64.RawURLEncoding.EncodeToString(secrets[:32])
	browser := base64.RawURLEncoding.EncodeToString(secrets[32:])
	relayDigest, _ := samlSecretDigest(relay)
	browserDigest, _ := samlSecretDigest(browser)
	now := r.now().UTC()
	row := domain.SAMLAuthnRequest{
		RequestID: requestID, AccountID: config.AccountID,
		ConfigVersion: config.ConfigVersion, ConfigDigest: config.ConfigDigest,
		RelayDigest: relayDigest, BrowserSecretDigest: browserDigest,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	if err := r.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}
	return &SAMLPreparedRequest{
		SAMLRequestBinding: SAMLRequestBinding{SAMLConfigurationBinding: config, RequestID: row.RequestID, ExpiresAt: row.ExpiresAt},
		RelayState:         relay, BrowserSecret: browser,
	}, nil
}

// Lookup requires both browser-bound secrets and returns only an unconsumed,
// unexpired pending request. Its result must still be checked by Consume.
func (r *SAMLRequestRepository) Lookup(ctx context.Context, relayState, browserSecret string) (*SAMLRequestBinding, error) {
	if !r.available(ctx) {
		return nil, ErrSAMLStorageUnavailable
	}
	relayDigest, err := samlSecretDigest(relayState)
	if err != nil {
		return nil, err
	}
	browserDigest, err := samlSecretDigest(browserSecret)
	if err != nil {
		return nil, err
	}
	var row domain.SAMLAuthnRequest
	err = r.db.WithContext(ctx).Where("relay_digest = ? AND browser_secret_digest = ? AND consumed_at IS NULL AND expires_at > ?", relayDigest, browserDigest, r.now().UTC()).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrSAMLRequestInvalid
	}
	if err != nil {
		return nil, err
	}
	if !row.ExpiresAt.After(r.now().UTC()) {
		return nil, ErrSAMLRequestInvalid
	}
	return &SAMLRequestBinding{
		SAMLConfigurationBinding: SAMLConfigurationBinding{AccountID: row.AccountID, ConfigVersion: row.ConfigVersion, ConfigDigest: row.ConfigDigest},
		RequestID:                row.RequestID, ExpiresAt: row.ExpiresAt,
	}, nil
}

// Consume atomically consumes a browser-bound pending request and both durable
// replay IDs. Only an immutable capability produced by the trusted cryptographic
// verifier is accepted; this function never accepts XML, a subject, or claims.
// The supplied current configuration must be trusted and lifecycle-fenced by the
// future integration layer. No current database SAML configuration check is made.
func (r *SAMLRequestRepository) Consume(ctx context.Context, relayState, browserSecret string, current SAMLConfigurationBinding, verified *auth.SAMLVerifiedAssertion) error {
	if !r.available(ctx) {
		return ErrSAMLStorageUnavailable
	}
	if verified == nil || !validSAMLConfiguration(current) ||
		verified.AccountID() != current.AccountID || verified.ConfigVersion() != current.ConfigVersion || verified.ConfigDigest() != current.ConfigDigest ||
		!samlRequestIDPattern.MatchString(verified.RequestID()) || verified.AssertionID() == "" || verified.ResponseID() == "" || verified.Issuer() == "" ||
		strings.ContainsRune(verified.Issuer(), 0) || strings.ContainsRune(verified.AssertionID(), 0) || strings.ContainsRune(verified.ResponseID(), 0) ||
		!verified.ExpiresAt().After(r.now().UTC()) {
		return ErrSAMLRequestInvalid
	}
	relayDigest, err := samlSecretDigest(relayState)
	if err != nil {
		return err
	}
	browserDigest, err := samlSecretDigest(browserSecret)
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := r.now().UTC()
		// Write first: SQLite acquires its writer lock before any reads, while
		// PostgreSQL serializes consumers on this request row. A loser cannot
		// overwrite the winner because the pending predicate is re-evaluated.
		claimed := tx.Model(&domain.SAMLAuthnRequest{}).
			Where("request_id = ? AND relay_digest = ? AND browser_secret_digest = ? AND account_id = ? AND config_version = ? AND config_digest = ? AND consumed_at IS NULL AND expires_at > ?",
				verified.RequestID(), relayDigest, browserDigest, current.AccountID, current.ConfigVersion, current.ConfigDigest, now).
			UpdateColumn("consumed_at", now)
		if claimed.Error != nil {
			return claimed.Error
		}
		if claimed.RowsAffected != 1 {
			return ErrSAMLRequestInvalid
		}
		var request domain.SAMLAuthnRequest
		if err := tx.Where("request_id = ?", verified.RequestID()).First(&request).Error; err != nil {
			return err
		}
		now = r.now().UTC()
		if !request.ExpiresAt.After(now) || !verified.ExpiresAt().After(now) {
			return ErrSAMLRequestInvalid
		}
		retainUntil := verified.ExpiresAt().UTC()
		if request.ExpiresAt.After(retainUntil) {
			retainUntil = request.ExpiresAt
		}
		replay := domain.SAMLReplay{
			AssertionDigest: samlReplayDigest("assertion", verified.Issuer(), verified.AssertionID()),
			ResponseDigest:  samlReplayDigest("response", verified.Issuer(), verified.ResponseID()),
			CreatedAt:       now, RetainUntil: retainUntil,
		}
		// ON CONFLICT DO NOTHING is atomic for either unique replay key on
		// SQLite/PostgreSQL and does not hide infrastructure errors. A conflict
		// rolls back the preceding pending-request consumption as well.
		inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&replay)
		if inserted.Error != nil {
			return inserted.Error
		}
		if inserted.RowsAffected != 1 {
			return ErrSAMLRequestInvalid
		}
		now = r.now().UTC()
		if !request.ExpiresAt.After(now) || !verified.ExpiresAt().After(now) {
			return ErrSAMLRequestInvalid
		}
		return nil
	})
}
