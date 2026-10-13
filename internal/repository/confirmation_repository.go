package repository

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

const ConfirmationTokenLifetime = 24 * time.Hour

var ErrConfirmationInvalid = errors.New("confirmation token is invalid, expired or superseded")

func ConfirmationTokenDigest(raw string) (string, error) {
	if len(raw) != 64 || strings.ToLower(raw) != raw {
		return "", ErrConfirmationInvalid
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", ErrConfirmationInvalid
	}
	digest := sha256.Sum256([]byte("ex-chat:confirmation:v1:" + raw))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// GenerateConfirmationToken replaces any earlier attempt under the user lock.
// The returned immutable snapshot is the sole mail recipient/expiry source.
// Pending proofs cannot confirm an account until this exact attempt is activated.
func (r *channelAuthIAMRepository) GenerateConfirmationToken(userID uint) (*domain.User, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return nil, "", err
	}
	raw := hex.EncodeToString(bytes)
	digest, _ := ConfirmationTokenDigest(raw)
	var user domain.User
	err := r.db.Transaction(func(tx *gorm.DB) error {
		locked := tx.Model(&domain.User{}).Where("id = ? AND confirmed_at IS NULL", userID).UpdateColumn("id", gorm.Expr("id"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return ErrConfirmationInvalid
		}
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		if user.ConfirmedAt != nil || user.Email == "" {
			return ErrConfirmationInvalid
		}
		now := time.Now().UTC()
		expires := now.Add(ConfirmationTokenLifetime)
		result := tx.Model(&domain.User{}).Where("id = ? AND confirmed_at IS NULL AND email = ? AND auth_version = ?", user.ID, user.Email, user.AuthVersion).Updates(map[string]any{
			"confirmation_token": digest, "confirmation_email": user.Email, "confirmation_auth_version": user.AuthVersion,
			"confirmation_sent_at": now, "confirmation_expires_at": expires, "confirmation_issued_at": nil,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConfirmationInvalid
		}
		user.ConfirmationToken = digest
		user.ConfirmationEmail = user.Email
		user.ConfirmationAuthVersion = user.AuthVersion
		user.ConfirmationSentAt = &now
		user.ConfirmationExpiresAt = &expires
		user.ConfirmationIssuedAt = nil
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return &user, raw, nil
}

// ActivateConfirmationToken is called only after a trustworthy sender success.
// The digest is the attempt fence: a late send completion cannot activate a
// newer resend, undo a confirmation, or survive a credential/mailbox change.
func (r *channelAuthIAMRepository) ActivateConfirmationToken(userID uint, raw string) error {
	digest, err := ConfirmationTokenDigest(raw)
	if err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		locked := tx.Model(&domain.User{}).Where("id = ? AND confirmation_token = ? AND confirmed_at IS NULL AND confirmation_issued_at IS NULL", userID, digest).UpdateColumn("id", gorm.Expr("id"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return ErrConfirmationInvalid
		}
		var user domain.User
		if err := tx.First(&user, userID).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if user.Email != user.ConfirmationEmail || user.AuthVersion != user.ConfirmationAuthVersion || user.ConfirmationSentAt == nil || user.ConfirmationSentAt.After(now) || user.ConfirmationExpiresAt == nil || !user.ConfirmationExpiresAt.After(now) || user.ConfirmationExpiresAt.After(user.ConfirmationSentAt.Add(ConfirmationTokenLifetime)) {
			return ErrConfirmationInvalid
		}
		result := tx.Model(&domain.User{}).Where("id = ? AND confirmation_token = ? AND confirmed_at IS NULL AND confirmation_issued_at IS NULL AND confirmation_expires_at > ? AND confirmation_email = email AND confirmation_auth_version = auth_version", userID, digest, time.Now().UTC()).Update("confirmation_issued_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConfirmationInvalid
		}
		return nil
	})
}

// ConfirmUserByToken consumes only an activated, current proof in a transaction.
// A no-op write serializes SQLite and PostgreSQL alike. The returned user comes
// from inside that lock and retains the exact verified credential version; never
// reload outside the transaction and upgrade an old proof to a newer session.
func (r *channelAuthIAMRepository) ConfirmUserByToken(raw string) (*domain.User, error) {
	digest, err := ConfirmationTokenDigest(raw)
	if err != nil {
		return nil, err
	}
	var user domain.User
	err = r.db.Transaction(func(tx *gorm.DB) error {
		locked := tx.Model(&domain.User{}).Where("confirmation_token = ? AND confirmed_at IS NULL", digest).UpdateColumn("id", gorm.Expr("id"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return ErrConfirmationInvalid
		}
		if err := tx.Where("confirmation_token = ?", digest).First(&user).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		if user.ConfirmedAt != nil || user.Email != user.ConfirmationEmail || user.AuthVersion != user.ConfirmationAuthVersion || user.ConfirmationIssuedAt == nil || user.ConfirmationSentAt == nil || user.ConfirmationExpiresAt == nil || !user.ConfirmationExpiresAt.After(now) || user.ConfirmationSentAt.After(*user.ConfirmationIssuedAt) || user.ConfirmationIssuedAt.After(now) || user.ConfirmationExpiresAt.After(user.ConfirmationSentAt.Add(ConfirmationTokenLifetime)) {
			return ErrConfirmationInvalid
		}
		result := tx.Model(&domain.User{}).Where("id = ? AND confirmation_token = ? AND confirmed_at IS NULL AND confirmation_expires_at > ? AND email = ? AND auth_version = ?", user.ID, digest, time.Now().UTC(), user.ConfirmationEmail, user.ConfirmationAuthVersion).Updates(map[string]any{
			"confirmed_at": now, "confirmation_token": "", "confirmation_email": "", "confirmation_auth_version": 0,
			"confirmation_sent_at": nil, "confirmation_expires_at": nil, "confirmation_issued_at": nil,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrConfirmationInvalid
		}
		user.ConfirmedAt = &now
		user.ConfirmationToken = ""
		user.ConfirmationEmail = ""
		user.ConfirmationAuthVersion = 0
		user.ConfirmationSentAt = nil
		user.ConfirmationExpiresAt = nil
		user.ConfirmationIssuedAt = nil
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &user, nil
}
