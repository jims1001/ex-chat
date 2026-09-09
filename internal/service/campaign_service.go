package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type CampaignService struct {
	db          *gorm.DB
	convRepo    *repository.ConversationRepository
	msgRepo     *repository.MessageRepository
	contactRepo *repository.ContactRepository
}

func NewCampaignService(
	db *gorm.DB,
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	contactRepo *repository.ContactRepository,
) *CampaignService {
	return &CampaignService{
		db:          db,
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		contactRepo: contactRepo,
	}
}

// TriggerCampaign dispatches messages to audience contacts and updates status
func (s *CampaignService) TriggerCampaign(accountID, campaignID uint) (int, error) {
	var campaign domain.Campaign
	err := s.db.Where("account_id = ? AND id = ?", accountID, campaignID).First(&campaign).Error
	if err != nil {
		return 0, err
	}

	if campaign.Status == "completed" {
		return 0, errors.New("campaign has already been completed")
	}

	// Fetch all contacts belonging to the account via pagination (not hardcoded to 100)
	var contacts []domain.Contact
	page := 1
	pageSize := 100
	for {
		batch, total, err := s.contactRepo.List(accountID, page, pageSize, "")
		if err != nil || len(batch) == 0 {
			break
		}
		contacts = append(contacts, batch...)
		if int64(len(contacts)) >= total || len(batch) < pageSize {
			break
		}
		page++
	}

	if len(contacts) == 0 {
		return 0, errors.New("no contacts found in account audience")
	}

	targetContacts := contacts
	if campaign.Audience != "" && campaign.Audience != "all" {
		var filtered []domain.Contact
		audienceStr := strings.TrimSpace(campaign.Audience)

		var rawMap map[string]any
		if err := json.Unmarshal([]byte(audienceStr), &rawMap); err == nil {
			if rawIDs, ok := rawMap["contact_ids"].([]any); ok {
				idMap := make(map[uint]bool)
				for _, r := range rawIDs {
					switch v := r.(type) {
					case float64:
						idMap[uint(v)] = true
					case string:
						if id, err := strconv.ParseUint(v, 10, 64); err == nil {
							idMap[uint(id)] = true
						}
					}
				}
				for _, c := range contacts {
					if idMap[c.ID] {
						filtered = append(filtered, c)
					}
				}
			} else if rawLabels, ok := rawMap["labels"].([]any); ok {
				var labelNames []string
				for _, l := range rawLabels {
					if s, ok := l.(string); ok {
						labelNames = append(labelNames, s)
					}
				}
				var matchedContactIDs []uint
				s.db.Table("conversations").
					Joins("JOIN conversation_labels ON conversation_labels.conversation_id = conversations.id").
					Joins("JOIN labels ON labels.id = conversation_labels.label_id").
					Where("conversations.account_id = ? AND labels.title IN (?)", accountID, labelNames).
					Pluck("DISTINCT conversations.contact_id", &matchedContactIDs)

				idMap := make(map[uint]bool)
				for _, cid := range matchedContactIDs {
					idMap[cid] = true
				}
				for _, c := range contacts {
					if idMap[c.ID] {
						filtered = append(filtered, c)
					}
				}
			} else if t, ok := rawMap["type"].(string); ok {
				if vals, ok := rawMap["values"].([]any); ok {
					var strVals []string
					for _, v := range vals {
						if sv, ok := v.(string); ok {
							strVals = append(strVals, sv)
						}
					}
					if t == "contact_ids" {
						idMap := make(map[uint]bool)
						for _, sv := range strVals {
							if id, err := strconv.ParseUint(sv, 10, 64); err == nil {
								idMap[uint(id)] = true
							}
						}
						for _, c := range contacts {
							if idMap[c.ID] {
								filtered = append(filtered, c)
							}
						}
					}
				}
			}
		} else {
			var matchedContactIDs []uint
			s.db.Table("conversations").
				Joins("JOIN conversation_labels ON conversation_labels.conversation_id = conversations.id").
				Joins("JOIN labels ON labels.id = conversation_labels.label_id").
				Where("conversations.account_id = ? AND (labels.title = ? OR labels.title LIKE ?)", accountID, audienceStr, "%"+audienceStr+"%").
				Pluck("DISTINCT conversations.contact_id", &matchedContactIDs)

			idMap := make(map[uint]bool)
			for _, cid := range matchedContactIDs {
				idMap[cid] = true
			}
			for _, c := range contacts {
				if idMap[c.ID] {
					filtered = append(filtered, c)
				}
			}
		}

		targetContacts = filtered
	}

	if len(targetContacts) == 0 {
		return 0, nil
	}

	sentCount := 0
	now := time.Now().UTC()

	for _, contact := range targetContacts {
		// Find or create conversation for this contact and inbox
		var conv domain.Conversation
		err := s.db.Where("account_id = ? AND contact_id = ? AND inbox_id = ?", accountID, contact.ID, campaign.InboxID).
			First(&conv).Error

		if err != nil {
			conv = domain.Conversation{
				AccountID: accountID,
				InboxID:   campaign.InboxID,
				ContactID: contact.ID,
				Status:    domain.ConversationStatusOpen,
				Priority:  domain.PriorityMedium,
			}
			if err := s.convRepo.Create(&conv); err != nil {
				continue
			}
		}

		// Send campaign message
		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       0,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        campaign.Message,
			Status:         domain.MessageStatusSent,
		}
		if err := s.msgRepo.Create(&msg); err == nil {
			del := domain.CampaignDelivery{
				AccountID:      accountID,
				CampaignID:     campaign.ID,
				ContactID:      contact.ID,
				ConversationID: conv.ID,
				Status:         "sent",
				SentAt:         now,
			}
			_ = s.db.Create(&del)
			sentCount++
		}
	}

	campaign.Status = "completed"
	_ = s.db.Save(&campaign)

	logger.WithComponent("campaign").Info("campaign triggered and completed",
		"campaign_id", campaign.ID,
		"account_id", accountID,
		"audience_count", len(contacts),
		"sent_count", sentCount,
	)

	return sentCount, nil
}

// StartScheduledCampaignWorker periodically checks and triggers scheduled campaigns
func (s *CampaignService) StartScheduledCampaignWorker(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.ProcessScheduledCampaigns()
			}
		}
	}()
}

// ProcessScheduledCampaigns finds due scheduled campaigns and dispatches them
func (s *CampaignService) ProcessScheduledCampaigns() int {
	var scheduled []domain.Campaign
	now := time.Now().UTC()
	err := s.db.Where("status = ? AND scheduled_at IS NOT NULL AND scheduled_at <= ?", "scheduled", now).
		Find(&scheduled).Error
	if err != nil || len(scheduled) == 0 {
		return 0
	}

	triggered := 0
	for _, c := range scheduled {
		if _, err := s.TriggerCampaign(c.AccountID, c.ID); err == nil {
			triggered++
		}
	}

	logger.WithComponent("campaign").Info("processed scheduled campaigns",
		"scheduled_count", len(scheduled),
		"triggered_count", triggered,
	)

	return triggered
}
