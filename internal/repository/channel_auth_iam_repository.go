package repository

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthIAMRepository struct{ db *gorm.DB }

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

func (r *channelAuthIAMRepository) CreatePasswordResetToken(token *domain.PasswordResetToken) error {
	return r.db.Create(token).Error
}

func (r *channelAuthIAMRepository) GetValidPasswordResetToken(tokenStr string) (*domain.PasswordResetToken, error) {
	var token domain.PasswordResetToken
	err := r.db.Where("token = ? AND used = ? AND expires_at > ?", tokenStr, false, time.Now()).First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *channelAuthIAMRepository) MarkPasswordResetTokenUsed(tokenID uint) error {
	return r.db.Model(&domain.PasswordResetToken{}).Where("id = ?", tokenID).Update("used", true).Error
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

func (r *channelAuthIAMRepository) SaveMFAProfile(profile *domain.MFAProfile) error {
	var existing domain.MFAProfile
	err := r.db.Where("user_id = ?", profile.UserID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(profile).Error
		}
		return err
	}
	profile.ID = existing.ID
	return r.db.Save(profile).Error
}

func (r *channelAuthIAMRepository) GenerateMFABackupCodes(userID uint) ([]string, error) {
	profile, err := r.GetMFAProfile(userID)
	if err != nil || profile == nil {
		return nil, errors.New("mfa profile not found")
	}
	rawCodes, err := auth.GenerateBackupCodes(8)
	if err != nil {
		return nil, err
	}
	bBytes, _ := json.Marshal(rawCodes)
	profile.BackupCodes = string(bBytes)
	if err := r.SaveMFAProfile(profile); err != nil {
		return nil, err
	}
	return rawCodes, nil
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

func (r *channelAuthIAMRepository) ConfirmUserByToken(tokenStr string) (*domain.User, error) {
	if tokenStr == "" {
		return nil, errors.New("invalid confirmation token")
	}
	var user domain.User
	err := r.db.Where("confirmation_token = ?", tokenStr).First(&user).Error
	if err != nil {
		return nil, err
	}
	now := time.Now()
	user.ConfirmedAt = &now
	user.ConfirmationToken = ""
	if err := r.db.Save(&user).Error; err != nil {
		return nil, err
	}
	return &user, nil
}

func (r *channelAuthIAMRepository) GenerateConfirmationToken(userID uint) (string, error) {
	bytes := make([]byte, 20)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)
	now := time.Now()
	err := r.db.Model(&domain.User{}).Where("id = ?", userID).Updates(map[string]any{
		"confirmation_token":   token,
		"confirmation_sent_at": &now,
	}).Error
	if err != nil {
		return "", err
	}
	return token, nil
}
