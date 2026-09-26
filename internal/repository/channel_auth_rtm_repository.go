package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthRTMRepository struct{ db *gorm.DB }

func (r *channelAuthRTMRepository) CreateCall(call *domain.Call) error {
	return r.db.Create(call).Error
}

func (r *channelAuthRTMRepository) ListCalls(accountID uint) ([]domain.Call, error) {
	var calls []domain.Call
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&calls).Error
	return calls, err
}

func (r *channelAuthRTMRepository) GetCall(accountID uint, id uint) (*domain.Call, error) {
	var call domain.Call
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&call).Error
	if err != nil {
		return nil, err
	}
	return &call, nil
}

func (r *channelAuthRTMRepository) UpdateCall(call *domain.Call) error {
	return r.db.Save(call).Error
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
	var call domain.Call
	err := r.db.Where("account_id = ? AND provider_call_id = ?", accountID, providerCallID).First(&call).Error
	if err != nil {
		return nil, err
	}
	return &call, nil
}

func (r *channelAuthRTMRepository) CreateCallCandidate(cand *domain.CallICECandidate) error {
	return r.db.Create(cand).Error
}

func (r *channelAuthRTMRepository) ListCallCandidates(callID uint) ([]domain.CallICECandidate, error) {
	var list []domain.CallICECandidate
	err := r.db.Where("call_id = ?", callID).Order("id ASC").Find(&list).Error
	return list, err
}
