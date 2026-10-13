package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var ErrPasswordResetInvalid = errors.New("password reset token is invalid or expired")

// PasswordResetTokenDigest accepts only new 256-bit opaque tokens. Prefixing
// stored digests distinguishes the format from legacy plaintext tokens, which
// fail closed rather than continuing to authenticate after this migration.
func PasswordResetTokenDigest(rawToken string) (string, error) {
	if len(rawToken) != 64 || strings.ToLower(rawToken) != rawToken {
		return "", ErrPasswordResetInvalid
	}
	if _, err := hex.DecodeString(rawToken); err != nil {
		return "", ErrPasswordResetInvalid
	}
	digest := sha256.Sum256([]byte(rawToken))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// CreatePasswordResetToken creates a pending token. A raw token is never
// accepted or persisted; only ActivatePasswordResetToken can make it usable.
func (r *channelAuthIAMRepository) CreatePasswordResetToken(token *domain.PasswordResetToken) error {
	now := time.Now()
	if token == nil || token.UserID == 0 || token.Email == "" || token.Used || token.IssuedAt != nil ||
		!strings.HasPrefix(token.Token, "sha256:") || len(token.Token) != 71 ||
		!token.ExpiresAt.After(now) || token.ExpiresAt.After(now.Add(2*time.Hour)) {
		return ErrPasswordResetInvalid
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(token.Token, "sha256:")); err != nil {
		return ErrPasswordResetInvalid
	}
	return r.db.Create(token).Error
}

// ActivatePasswordResetToken must only follow successful email delivery. A
// failed/ambiguous delivery leaves a pending record, never a usable reset link.
func (r *channelAuthIAMRepository) ActivatePasswordResetToken(tokenID uint) error {
	now := time.Now()
	result := r.db.Model(&domain.PasswordResetToken{}).
		Where("id = ? AND used = ? AND issued_at IS NULL AND expires_at > ? AND token LIKE ?", tokenID, false, now, "sha256:%").
		Update("issued_at", now)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrPasswordResetInvalid
	}
	return nil
}

// ConsumePasswordResetToken serializes resets for the same user, including
// different sibling tokens and different application/database handles. A single
// transaction updates only the password and consumes every reset token. MFA
// settings are deliberately untouched. Any storage error rolls everything back.
func (r *channelAuthIAMRepository) ConsumePasswordResetToken(rawToken, passwordHash string) error {
	digest, err := PasswordResetTokenDigest(rawToken)
	if err != nil {
		return err
	}
	if _, err := bcrypt.Cost([]byte(passwordHash)); err != nil {
		return errors.New("invalid replacement password hash")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Take the user lock before reading token state. On SQLite this starts
		// a write transaction; on PostgreSQL it locks the matching user row.
		// Every competing sibling reset uses the same lock order.
		candidate := tx.Model(&domain.PasswordResetToken{}).Select("user_id").
			Where("token = ? AND used = ? AND issued_at IS NOT NULL AND expires_at > ?", digest, false, time.Now())
		locked := tx.Model(&domain.User{}).Where("id IN (?)", candidate).
			UpdateColumn("password_hash", gorm.Expr("password_hash"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return ErrPasswordResetInvalid
		}
		var token domain.PasswordResetToken
		if err := tx.Where("token = ? AND used = ? AND issued_at IS NOT NULL AND expires_at > ?", digest, false, time.Now()).First(&token).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPasswordResetInvalid
			}
			return err
		}
		var user domain.User
		if err := tx.First(&user, token.UserID).Error; err != nil {
			return err
		}
		if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err != nil || user.Email != token.Email {
			return ErrPasswordResetInvalid
		}
		// An expiry boundary after the locking/read steps is still rejected.
		consumed := tx.Model(&domain.PasswordResetToken{}).
			Where("id = ? AND used = ? AND issued_at IS NOT NULL AND expires_at > ?", token.ID, false, time.Now()).Update("used", true)
		if consumed.Error != nil {
			return consumed.Error
		}
		if consumed.RowsAffected != 1 {
			return ErrPasswordResetInvalid
		}
		updated := tx.Model(&domain.User{}).Where("id = ? AND password_hash = ?", user.ID, user.PasswordHash).
			Updates(map[string]any{"password_hash": passwordHash, "auth_version": gorm.Expr("auth_version + 1")})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected != 1 {
			return ErrPasswordResetInvalid
		}
		if err := tx.Where("user_id = ?", user.ID).Delete(&domain.UserSession{}).Error; err != nil {
			return err
		}
		// Includes pending siblings: an in-flight email cannot reactivate a
		// reset issued before this successful password change.
		return tx.Model(&domain.PasswordResetToken{}).
			Where("user_id = ? AND used = ?", user.ID, false).Update("used", true).Error
	})
}
