package database

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// This upgrade deliberately invalidates legacy plaintext/indefinite proof. It
// never hashes old tokens into valid credentials: callers must request a resend.
// Cleanup is owner-local, idempotent, and fatal on failure during startup.
func migrateConfirmationSecurity(db *gorm.DB, module string) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if module == "" || module == "IAM" {
			if err := tx.Model(&domain.User{}).Where("confirmation_token <> '' AND (confirmation_token NOT LIKE ? OR LENGTH(confirmation_token) <> 71 OR confirmation_email = '' OR confirmation_email IS NULL OR confirmation_sent_at IS NULL OR confirmation_expires_at IS NULL)", "sha256:%").Updates(map[string]any{
				"confirmation_token": "", "confirmation_email": "", "confirmation_auth_version": 0,
				"confirmation_sent_at": nil, "confirmation_expires_at": nil, "confirmation_issued_at": nil,
			}).Error; err != nil {
				return err
			}
		}
		if module == "" || module == "MSG" {
			// Old bodies and provider errors may contain complete bearer links. Keep
			// non-secret audit metadata and delivery status, but disable all retries.
			if err := tx.Model(&domain.EmailLog{}).Where("email_type = ? AND (content_html <> '' OR content_text <> '' OR error <> '' OR bounce_reason <> '' OR subject <> ? OR max_retries <> 0 OR next_retry_at IS NOT NULL)", "confirmation", "Email confirmation (legacy content removed)").Updates(map[string]any{
				"content_html": "", "content_text": "", "error": "", "bounce_reason": "", "subject": "Email confirmation (legacy content removed)",
				"max_retries": 0, "next_retry_at": nil,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
