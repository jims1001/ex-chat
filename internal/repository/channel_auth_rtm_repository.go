package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type channelAuthRTMRepository struct{ db *gorm.DB }

func (r *channelAuthRTMRepository) CreateCall(call *domain.Call) error {
	return r.db.Create(call).Error
}

func (r *channelAuthRTMRepository) ListCalls(accountID uint) ([]domain.Call, error) {
	// Do not log caller identifiers or raw dependency errors from Call reads.
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var calls []domain.Call
	err := db.Where("account_id = ?", accountID).Order("id DESC").Find(&calls).Error
	return calls, err
}

func (r *channelAuthRTMRepository) GetCall(accountID uint, id uint) (*domain.Call, error) {
	// Do not log caller identifiers or raw dependency errors from Call reads.
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var call domain.Call
	err := db.Where("account_id = ? AND id = ?", accountID, id).First(&call).Error
	if err != nil {
		return nil, err
	}
	return &call, nil
}

func (r *channelAuthRTMRepository) UpdateCall(call *domain.Call) error {
	if call == nil || call.ID == 0 || call.AccountID == 0 {
		return gorm.ErrRecordNotFound
	}
	// Lifecycle updates do not own recording fields. A call loaded before an
	// upload must not erase bytes-linked metadata when its status changes later.
	result := r.db.Model(&domain.Call{}).Where("id = ? AND account_id = ?", call.ID, call.AccountID).
		Select("*").Omit("ID", "AccountID", "RecordingURL", "RecordingSID").Updates(call)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *channelAuthRTMRepository) CreateConference(conf *domain.Conference) error {
	return r.db.Create(conf).Error
}

func (r *channelAuthRTMRepository) GetConference(accountID uint, inboxID uint) (*domain.Conference, error) {
	var conf domain.Conference
	err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Order("id DESC").First(&conf).Error
	if err != nil {
		return nil, err
	}
	return &conf, nil
}

func (r *channelAuthRTMRepository) GetConferenceByID(accountID uint, id uint) (*domain.Conference, error) {
	var conf domain.Conference
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&conf).Error
	if err != nil {
		return nil, err
	}
	return &conf, nil
}

func (r *channelAuthRTMRepository) DeleteConference(accountID uint, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Conference{}).Error
}

func (r *channelAuthRTMRepository) DeleteConferenceByInbox(accountID uint, inboxID uint) error {
	return r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Delete(&domain.Conference{}).Error
}

func (r *channelAuthRTMRepository) GetCallByProviderCallID(accountID uint, providerCallID string) (*domain.Call, error) {
	// Do not log caller identifiers or raw dependency errors from Call reads.
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var call domain.Call
	err := db.Where("account_id = ? AND provider_call_id = ?", accountID, providerCallID).First(&call).Error
	if err != nil {
		return nil, err
	}
	return &call, nil
}

func (r *channelAuthRTMRepository) CreateCallCandidate(cand *domain.CallICECandidate) error {
	return r.db.Create(cand).Error
}

func (r *channelAuthRTMRepository) ListCallCandidates(callID uint) ([]domain.CallICECandidate, error) {
	// Do not log caller identifiers or raw dependency errors from Call reads.
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var list []domain.CallICECandidate
	err := db.Where("call_id = ?", callID).Order("id ASC").Find(&list).Error
	return list, err
}
