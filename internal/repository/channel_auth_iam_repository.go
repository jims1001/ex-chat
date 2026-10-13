package repository

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type channelAuthIAMRepository struct{ db *gorm.DB }

var (
	ErrMFARateLimited       = errors.New("too many MFA verification attempts")
	ErrMFAInvalidCode       = errors.New("invalid MFA verification code")
	ErrMFAReplay            = errors.New("MFA verification has already been used")
	ErrMFANotEnabled        = errors.New("MFA is not enabled")
	ErrMFAInvalidPassword   = errors.New("current password is invalid")
	ErrMFAReauthUnsupported = errors.New("local password reauthentication is unavailable for this account")
	ErrMFAAlreadyEnabled    = errors.New("MFA is already enabled")
	ErrMFASetupRequired     = errors.New("MFA setup is required")
	ErrMFAInvalidAction     = errors.New("invalid MFA management action")
)

const (
	mfaVerificationLimit  = 5
	mfaVerificationWindow = 5 * time.Minute
)

func (r *channelAuthIAMRepository) GetSAMLSetting(accountID uint) (*domain.SAMLSetting, error) {
	var setting domain.SAMLSetting
	err := r.db.Where("account_id = ?", accountID).First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &setting, nil
}

func (r *channelAuthIAMRepository) SaveSAMLSetting(setting *domain.SAMLSetting) error {
	var existing domain.SAMLSetting
	err := r.db.Where("account_id = ?", setting.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(setting).Error
		}
		return err
	}
	setting.ID = existing.ID
	return r.db.Save(setting).Error
}

func (r *channelAuthIAMRepository) DeleteSAMLSetting(accountID uint) error {
	return r.db.Where("account_id = ?", accountID).Delete(&domain.SAMLSetting{}).Error
}

func (r *channelAuthIAMRepository) DisableSAMLSetting(accountID uint) (*domain.SAMLSetting, error) {
	var setting domain.SAMLSetting
	err := r.db.Where("account_id = ?", accountID).First(&setting).Error
	if err != nil {
		return nil, err
	}
	setting.Enabled = false
	if err := r.db.Save(&setting).Error; err != nil {
		return nil, err
	}
	return &setting, nil
}

func (r *channelAuthIAMRepository) CreateSession(sess *domain.UserSession) error {
	return r.db.Create(sess).Error
}

func (r *channelAuthIAMRepository) ListSessions(userID uint) ([]domain.UserSession, error) {
	var sessions []domain.UserSession
	err := r.db.Where("user_id = ?", userID).Order("id DESC").Find(&sessions).Error
	return sessions, err
}

func (r *channelAuthIAMRepository) DeleteSession(userID uint, sessionID uint) error {
	return r.db.Where("user_id = ? AND id = ?", userID, sessionID).Delete(&domain.UserSession{}).Error
}

func (r *channelAuthIAMRepository) GetMFAProfile(userID uint) (*domain.MFAProfile, error) {
	var profile domain.MFAProfile
	err := r.db.Where("user_id = ?", userID).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &profile, nil
}

// SaveMFAProfile is a trusted provisioning primitive. User-facing MFA settings
// must use ManageMFA so that configuration writes require reauthentication.
func (r *channelAuthIAMRepository) SaveMFAProfile(profile *domain.MFAProfile) error {
	if profile == nil || profile.UserID == 0 {
		return errors.New("invalid MFA profile")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// The UPDATE obtains the same row lock used by verification. Do not use
		// Save here: a stale profile could restore a consumed backup code or
		// erase rate limits and replay state. Only configuration is writable.
		result := tx.Model(&domain.MFAProfile{}).Where("user_id = ?", profile.UserID).
			Updates(map[string]any{"secret": profile.Secret, "enabled": profile.Enabled})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			created := domain.MFAProfile{
				UserID: profile.UserID, Secret: profile.Secret,
				Enabled: profile.Enabled, BackupCodes: profile.BackupCodes,
			}
			if err := tx.Create(&created).Error; err != nil {
				return err
			}
			*profile = created
			return nil
		}
		return tx.Where("user_id = ?", profile.UserID).First(profile).Error
	})
}

// GenerateMFABackupCodes is a trusted provisioning primitive. User-facing code
// must use ManageMFA to bind regeneration to fresh password and factor checks.
func (r *channelAuthIAMRepository) GenerateMFABackupCodes(userID uint) ([]string, error) {
	rawCodes, err := auth.GenerateBackupCodes(8)
	if err != nil {
		return nil, err
	}
	bBytes, err := json.Marshal(rawCodes)
	if err != nil {
		return nil, err
	}
	// One row UPDATE serializes regeneration with verification, without saving
	// a previously read profile or changing its replay/rate-limit state.
	result := r.db.Model(&domain.MFAProfile{}).Where("user_id = ?", userID).
		Update("backup_codes", string(bBytes))
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrMFANotEnabled
	}
	return rawCodes, nil
}

// ManageMFA reauthenticates and changes sensitive MFA settings in one
// transaction. All attempts share the login verification budget, including
// incorrect passwords and attempts made before an MFA profile exists. Expected
// authentication failures commit only attempt accounting; storage failures roll
// back the entire operation, including factor consumption.
//
// Only setup returns a pending secret. Existing secrets and backup codes are
// never returned; newly issued backup codes are returned separately once, after
// the transaction commits. Passwordless accounts need an explicit future
// provider reauthentication flow and cannot bypass local password verification.
func (r *channelAuthIAMRepository) ManageMFA(userID uint, currentPassword, action, code string) (*domain.MFAProfile, []string, error) {
	if action != "setup" && action != "enable" && action != "backup_codes" && action != "disable" {
		return nil, nil, ErrMFAInvalidAction
	}
	if userID == 0 {
		return nil, nil, ErrMFAInvalidPassword
	}
	var profile domain.MFAProfile
	var rawCodes []string
	var authenticationErr error
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// A unique user_id prevents competing first-time setup requests from
		// creating independent throttle budgets. This insert takes a write lock
		// on SQLite too, where SELECT FOR UPDATE alone would not serialize.
		bootstrap := domain.MFAProfile{UserID: userID}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&bootstrap).Error; err != nil {
			return err
		}
		locked := tx.Model(&domain.MFAProfile{}).Where("user_id = ?", userID).
			UpdateColumn("auth_version", gorm.Expr("auth_version + 1"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		if err := tx.Where("user_id = ?", userID).First(&profile).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		limited := accountMFAAttempt(&profile, now)
		if limited {
			authenticationErr = ErrMFARateLimited
		} else {
			// Read from this repository's IAM store. Locking the user row also
			// serializes the credential check with a concurrent password change.
			var user domain.User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
				return err
			}
			switch {
			case strings.TrimSpace(user.PasswordHash) == "":
				authenticationErr = ErrMFAReauthUnsupported
			case currentPassword == "" || !auth.CheckPasswordHash(currentPassword, user.PasswordHash):
				authenticationErr = ErrMFAInvalidPassword
			default:
				authenticationErr = manageMFAProfile(&profile, action, strings.TrimSpace(code), now, &rawCodes)
			}
		}
		if authenticationErr != nil && !isMFAAuthenticationError(authenticationErr) {
			return authenticationErr
		}
		updates := map[string]any{
			"verification_window_started_at": profile.VerificationWindowStartedAt,
			"verification_attempts":          profile.VerificationAttempts,
		}
		if authenticationErr == nil {
			updates["secret"] = profile.Secret
			updates["enabled"] = profile.Enabled
			updates["backup_codes"] = profile.BackupCodes
			updates["last_totp_step"] = profile.LastTOTPStep
		}
		result := tx.Model(&domain.MFAProfile{}).Where("id = ?", profile.ID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if authenticationErr != nil {
		return nil, nil, authenticationErr
	}
	profile.BackupCodes = ""
	if action != "setup" {
		profile.Secret = ""
	}
	return &profile, rawCodes, nil
}

func manageMFAProfile(profile *domain.MFAProfile, action, code string, now time.Time, rawCodes *[]string) error {
	switch action {
	case "setup":
		if profile.Enabled {
			return ErrMFAAlreadyEnabled
		}
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			return err
		}
		profile.Secret = secret
		profile.BackupCodes = ""
		profile.LastTOTPStep = 0
		return nil
	case "enable":
		if profile.Enabled {
			return ErrMFAAlreadyEnabled
		}
		if profile.Secret == "" {
			return ErrMFASetupRequired
		}
		// A pending profile must prove its TOTP secret, never an old backup.
		step, valid := auth.MatchTOTPStep(profile.Secret, code, now)
		if !valid {
			return ErrMFAInvalidCode
		}
		if step <= profile.LastTOTPStep {
			return ErrMFAReplay
		}
		profile.LastTOTPStep = step
		profile.Enabled = true
	case "backup_codes", "disable":
		if !profile.Enabled {
			return ErrMFANotEnabled
		}
		if err := consumeMFAFactor(profile, code, now); err != nil {
			return err
		}
		if action == "disable" {
			profile.Enabled = false
			profile.Secret = ""
			profile.BackupCodes = ""
			return nil
		}
	default:
		return ErrMFAInvalidAction
	}
	codes, err := auth.GenerateBackupCodes(8)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(codes)
	if err != nil {
		return err
	}
	profile.BackupCodes = string(encoded)
	*rawCodes = codes
	return nil
}

func accountMFAAttempt(profile *domain.MFAProfile, now time.Time) bool {
	if profile.VerificationWindowStartedAt == nil || !now.Before(profile.VerificationWindowStartedAt.Add(mfaVerificationWindow)) {
		profile.VerificationWindowStartedAt = &now
		profile.VerificationAttempts = 0
	}
	limited := profile.VerificationAttempts >= mfaVerificationLimit
	if profile.VerificationAttempts < math.MaxInt64 {
		profile.VerificationAttempts++
	}
	return limited
}

func isMFAAuthenticationError(err error) bool {
	return errors.Is(err, ErrMFARateLimited) || errors.Is(err, ErrMFANotEnabled) ||
		errors.Is(err, ErrMFAInvalidCode) || errors.Is(err, ErrMFAReplay) ||
		errors.Is(err, ErrMFAInvalidPassword) || errors.Is(err, ErrMFAReauthUnsupported) ||
		errors.Is(err, ErrMFAAlreadyEnabled) || errors.Is(err, ErrMFASetupRequired) ||
		errors.Is(err, ErrMFAInvalidAction)
}

// VerifyMFALogin atomically accounts for an attempt, checks replay state, and
// consumes the second factor. A fixed per-user five-minute window permits five
// attempts total, including successes, invalid codes, and replays. Further
// attempts are counted but rejected until that window expires. New challenges
// and new application instances do not reset the window.
//
// The caller must validate the challenge's signature, purpose, user and expiry
// first. A blank challengeID with a zero expiry is reserved for the inline login
// path, where the caller has just verified the user's password.
func (r *channelAuthIAMRepository) VerifyMFALogin(userID uint, code, challengeID string, challengeExpiresAt time.Time) error {
	var verificationErr error
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Write before reading. PostgreSQL serializes this on the profile row;
		// SQLite serializes writers. SELECT FOR UPDATE alone is insufficient
		// because SQLite ignores it. Infrastructure errors fail closed.
		locked := tx.Model(&domain.MFAProfile{}).Where("user_id = ?", userID).
			UpdateColumn("auth_version", gorm.Expr("auth_version + 1"))
		if locked.Error != nil {
			return locked.Error
		}
		if locked.RowsAffected != 1 {
			verificationErr = ErrMFANotEnabled
			return nil
		}
		var profile domain.MFAProfile
		if err := tx.Where("user_id = ?", userID).First(&profile).Error; err != nil {
			return err
		}
		now := time.Now().UTC()
		limited := accountMFAAttempt(&profile, now)

		used := make(map[string]time.Time)
		if profile.UsedChallenges != "" {
			if err := json.Unmarshal([]byte(profile.UsedChallenges), &used); err != nil {
				return err
			}
		}
		if used == nil {
			used = make(map[string]time.Time)
		}
		for id, expiresAt := range used {
			if !expiresAt.After(now) {
				delete(used, id)
			}
		}

		switch {
		case limited:
			verificationErr = ErrMFARateLimited
		case !profile.Enabled:
			verificationErr = ErrMFANotEnabled
		case challengeID == "" && !challengeExpiresAt.IsZero():
			verificationErr = ErrMFAInvalidCode
		case challengeID != "" && (len(challengeID) > 128 || strings.TrimSpace(challengeID) != challengeID ||
			!challengeExpiresAt.After(now) || challengeExpiresAt.After(now.Add(mfaVerificationWindow))):
			verificationErr = ErrMFAInvalidCode
		default:
			if _, replayed := used[challengeID]; challengeID != "" && replayed {
				verificationErr = ErrMFAReplay
			} else {
				verificationErr = consumeMFAFactor(&profile, strings.TrimSpace(code), now)
				if verificationErr == nil && challengeID != "" {
					used[challengeID] = challengeExpiresAt
				}
			}
		}
		// Only expected authentication failures may commit attempt accounting.
		// A corrupt backup-code payload is an infrastructure error and rolls back.
		if verificationErr != nil && !errors.Is(verificationErr, ErrMFARateLimited) &&
			!errors.Is(verificationErr, ErrMFANotEnabled) && !errors.Is(verificationErr, ErrMFAInvalidCode) &&
			!errors.Is(verificationErr, ErrMFAReplay) {
			return verificationErr
		}
		usedJSON, err := json.Marshal(used)
		if err != nil {
			return err
		}
		// Returning an authentication error from the transaction would roll back
		// rate limits. Persist first, then return that error after COMMIT succeeds.
		return tx.Model(&domain.MFAProfile{}).Where("id = ?", profile.ID).Updates(map[string]any{
			"verification_window_started_at": profile.VerificationWindowStartedAt,
			"verification_attempts":          profile.VerificationAttempts,
			"last_totp_step":                 profile.LastTOTPStep,
			"used_challenges":                string(usedJSON),
			"backup_codes":                   profile.BackupCodes,
		}).Error
	})
	if err != nil {
		return err
	}
	return verificationErr
}

func consumeMFAFactor(profile *domain.MFAProfile, code string, now time.Time) error {
	if step, valid := auth.MatchTOTPStep(profile.Secret, code, now); valid {
		if step <= profile.LastTOTPStep {
			return ErrMFAReplay
		}
		profile.LastTOTPStep = step
		return nil
	}
	var codes []string
	if profile.BackupCodes != "" {
		if err := json.Unmarshal([]byte(profile.BackupCodes), &codes); err != nil {
			return err
		}
	}
	matched := false
	remaining := make([]string, 0, len(codes))
	for _, backup := range codes {
		if code != "" && subtle.ConstantTimeCompare([]byte(backup), []byte(code)) == 1 {
			// Remove every duplicate too, so even a generated collision is one-use.
			matched = true
		} else {
			remaining = append(remaining, backup)
		}
	}
	if !matched {
		return ErrMFAInvalidCode
	}
	encoded, err := json.Marshal(remaining)
	if err != nil {
		return err
	}
	profile.BackupCodes = string(encoded)
	return nil
}

func (r *channelAuthIAMRepository) RevokeToken(token string, userID uint, expiresAt time.Time) error {
	rev := domain.RevokedToken{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: time.Now(),
	}
	return r.db.Create(&rev).Error
}

func (r *channelAuthIAMRepository) IsTokenRevoked(token string) (bool, error) {
	var count int64
	err := r.db.Model(&domain.RevokedToken{}).Where("token = ? AND expires_at > ?", token, time.Now()).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
