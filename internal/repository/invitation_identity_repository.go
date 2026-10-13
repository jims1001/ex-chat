package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvitationSignInRequired = errors.New("sign in to the intended account to accept this invitation")

// FindInvitationIdentityByEmail uses exact normalized mailbox identity, without
// treating aliases or display names as equivalent. Ambiguous legacy rows fail.
func (r *UserRepository) FindInvitationIdentityByEmail(email string) (*domain.User, error) {
	var users []domain.User
	if err := r.db.Where("LOWER(email) = ?", email).Limit(2).Find(&users).Error; err != nil {
		return nil, err
	}
	if len(users) > 1 {
		return nil, ErrInvitationInvalid
	}
	if len(users) == 0 {
		return nil, nil
	}
	return &users[0], nil
}

func invitationOperationKey(digest string) string {
	sum := sha256.Sum256([]byte("ex-chat:invitation-activation:v1:" + digest))
	return hex.EncodeToString(sum[:])
}

// ActivateInvitedIdentity creates one IAM identity and its immutable operation
// binding in one IAM transaction. Email equality never reuses another identity.
// Retries may reuse only the identity created by this exact mailbox proof. This
// method grants neither a session nor tenant membership and creates no workspace.
func (r *UserRepository) ActivateInvitedIdentity(invite *domain.AgentInvitation, name, passwordHash string) (*domain.User, error) {
	if invite == nil || !validInvitationDigest(invite.TokenDigest) || invite.IntendedUserID != nil || strings.TrimSpace(name) == "" || len(name) > 255 {
		return nil, ErrInvitationSignInRequired
	}
	if _, err := bcrypt.Cost([]byte(passwordHash)); err != nil {
		return nil, ErrInvitationInvalid
	}
	key := invitationOperationKey(invite.TokenDigest)
	var result domain.User
	loadBinding := func(db *gorm.DB) (bool, error) {
		var binding domain.InvitationIdentity
		err := db.Where("operation_key = ?", key).First(&binding).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if binding.Email != invite.Email {
			return false, ErrInvitationInvalid
		}
		if err := db.First(&result, binding.UserID).Error; err != nil {
			return false, ErrInvitationSignInRequired
		}
		if result.Email != binding.Email || result.AuthVersion != 0 || result.ConfirmedAt == nil || result.IsSuperAdmin() {
			return false, ErrInvitationSignInRequired
		}
		var profile domain.MFAProfile
		err = db.Where("user_id = ?", result.ID).First(&profile).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return false, err
		}
		if profile.Enabled {
			return false, ErrInvitationSignInRequired
		}
		return true, nil
	}
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.InvitationIdentity{}).Where("operation_key = ?", key).UpdateColumn("id", gorm.Expr("id")).Error; err != nil {
			return err
		}
		found, err := loadBinding(tx)
		if err != nil || found {
			return err
		}
		var existing int64
		if err := tx.Model(&domain.User{}).Where("LOWER(email) = ?", invite.Email).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return ErrInvitationSignInRequired
		}
		now := time.Now().UTC()
		result = domain.User{Name: strings.TrimSpace(name), Email: invite.Email, PasswordHash: passwordHash, Role: domain.RoleAgent, Type: "User", ConfirmedAt: &now, Availability: domain.AvailabilityOnline}
		if err := tx.Create(&result).Error; err != nil {
			return err
		}
		return tx.Create(&domain.InvitationIdentity{OperationKey: key, UserID: result.ID, Email: invite.Email}).Error
	})
	if err != nil {
		// A competing identical operation may have won the unique identity/binding
		// insert. Reconcile its committed result; never fall back to an email match.
		found, lookupErr := loadBinding(r.db)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if !found {
			// A competing ordinary registration or different invite may have won
			// normalized email uniqueness. Report sign-in, never reuse that user.
			existing, lookupErr := r.FindInvitationIdentityByEmail(invite.Email)
			if lookupErr != nil {
				return nil, lookupErr
			}
			if existing != nil {
				return nil, ErrInvitationSignInRequired
			}
			return nil, err
		}
	}
	return &result, nil
}

// AuthorizeInvitationAcceptance checks the current IAM identity and requires a
// fresh factor if MFA is enabled. A five-minute receipt in IAM's replay map is
// bound to this invite, credential version and current MFA secret. It supports a
// retry after a lost TEN result without consuming another backup code. No secret
// is returned or changed. This is an IAM attestation, not a cross-store lock.
func (r *UserRepository) AuthorizeInvitationAcceptance(userID uint, authVersion int64, email, tokenDigest, code string) error {
	if !validInvitationDigest(tokenDigest) || userID == 0 {
		return ErrInvitationInvalid
	}
	var verifyErr error
	returnErr := r.db.Transaction(func(tx *gorm.DB) error {
		// Follow the existing IAM MFA lock order: profile, then user.
		locked := tx.Model(&domain.MFAProfile{}).Where("user_id = ?", userID).UpdateColumn("auth_version", gorm.Expr("auth_version + 1"))
		if locked.Error != nil {
			return locked.Error
		}
		var user domain.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		if user.Email != email || user.AuthVersion != authVersion {
			return ErrInvitationInvalid
		}
		if locked.RowsAffected == 0 {
			return nil
		}
		var profile domain.MFAProfile
		if err := tx.Where("user_id = ?", userID).First(&profile).Error; err != nil {
			return err
		}
		if !profile.Enabled {
			return nil
		}
		now := time.Now().UTC()
		keyMaterial, _ := json.Marshal([]any{"invitation-mfa-v1", tokenDigest, userID, authVersion, profile.Secret})
		keySum := sha256.Sum256(keyMaterial)
		key := "invitation:" + hex.EncodeToString(keySum[:])
		used := map[string]time.Time{}
		if profile.UsedChallenges != "" {
			if err := json.Unmarshal([]byte(profile.UsedChallenges), &used); err != nil {
				return err
			}
		}
		if used == nil {
			used = map[string]time.Time{}
		}
		for k, expires := range used {
			if !expires.After(now) {
				delete(used, k)
			}
		}
		if expires, ok := used[key]; ok && expires.After(now) {
			return nil
		}
		if accountMFAAttempt(&profile, now) {
			verifyErr = ErrMFARateLimited
		} else {
			verifyErr = consumeMFAFactor(&profile, strings.TrimSpace(code), now)
		}
		if verifyErr == nil {
			used[key] = now.Add(5 * time.Minute)
		}
		encoded, err := json.Marshal(used)
		if err != nil {
			return err
		}
		return tx.Model(&domain.MFAProfile{}).Where("id = ?", profile.ID).Updates(map[string]any{
			"backup_codes": profile.BackupCodes, "last_totp_step": profile.LastTOTPStep, "used_challenges": string(encoded),
			"verification_window_started_at": profile.VerificationWindowStartedAt, "verification_attempts": profile.VerificationAttempts,
		}).Error
	})
	if returnErr != nil {
		return returnErr
	}
	return verifyErr
}
