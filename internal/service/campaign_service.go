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
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type CampaignService struct {
	db           *gorm.DB
	convRepo     *repository.ConversationRepository
	msgRepo      *repository.MessageRepository
	contactRepo  *repository.ContactRepository
	campaignRepo *repository.CampaignRepository
	hub          *ws.Hub
}

func (s *CampaignService) SetHub(hub *ws.Hub) {
	s.hub = hub
}

func NewCampaignService(
	db *gorm.DB,
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	contactRepo *repository.ContactRepository,
) *CampaignService {
	return &CampaignService{
		db:           db,
		convRepo:     convRepo,
		msgRepo:      msgRepo,
		contactRepo:  contactRepo,
		campaignRepo: repository.NewCampaignRepository(db),
	}
}

// GetCampaignWithStats retrieves a campaign by ID enriched with delivery statistics
func (s *CampaignService) GetCampaignWithStats(accountID, campaignID uint) (*domain.Campaign, error) {
	camp, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID)
	if err != nil {
		return nil, err
	}

	stats, err := s.campaignRepo.GetDeliveryStats(context.Background(), accountID, campaignID)
	if err == nil {
		camp.DeliveriesCount = stats["total_deliveries"]
		camp.SentCount = stats["sent"]
		camp.DeliveredCount = stats["delivered"]
		camp.FailedCount = stats["failed"]
	}

	return camp, nil
}

// GetCampaignMetrics aggregates metrics for a campaign
func (s *CampaignService) GetCampaignMetrics(accountID, campaignID uint) (map[string]any, error) {
	camp, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID)
	if err != nil {
		return nil, err
	}

	stats, err := s.campaignRepo.GetDeliveryStats(context.Background(), accountID, campaignID)
	if err != nil {
		return nil, err
	}

	var deliveryRate float64
	total := stats["total_deliveries"]
	if total > 0 {
		deliveryRate = float64(stats["sent"]+stats["delivered"]) / float64(total)
	}

	return map[string]any{
		"campaign_id":      campaignID,
		"title":            camp.Title,
		"status":           camp.Status,
		"campaign_type":    camp.CampaignType,
		"deliveries_count": total,
		"sent_count":       stats["sent"],
		"delivered_count":  stats["delivered"],
		"failed_count":     stats["failed"],
		"delivery_rate":    deliveryRate,
	}, nil
}

// ListCampaignDeliveries returns paginated delivery records for a campaign
func (s *CampaignService) ListCampaignDeliveries(accountID, campaignID uint, page, pageSize int) ([]domain.CampaignDelivery, int64, error) {
	// Verify campaign existence and tenant ownership
	if _, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID); err != nil {
		return nil, 0, err
	}
	return s.campaignRepo.GetDeliveries(context.Background(), accountID, campaignID, page, pageSize)
}

// PauseCampaign pauses an active or scheduled campaign
func (s *CampaignService) PauseCampaign(accountID, campaignID uint) error {
	camp, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID)
	if err != nil {
		return err
	}

	if camp.Status == "completed" || camp.Status == "cancelled" {
		return errors.New("cannot pause completed or cancelled campaign")
	}

	if err := s.campaignRepo.UpdateStatus(context.Background(), accountID, campaignID, "paused"); err != nil {
		return err
	}

	logger.WithComponent("campaign").Info("campaign paused",
		"campaign_id", campaignID,
		"account_id", accountID,
		"previous_status", camp.Status,
	)
	return nil
}

// ResumeCampaign resumes a paused campaign back to active or scheduled
func (s *CampaignService) ResumeCampaign(accountID, campaignID uint) error {
	camp, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID)
	if err != nil {
		return err
	}

	if camp.Status != "paused" {
		return errors.New("only paused campaign can be resumed")
	}

	newStatus := "active"
	if camp.ScheduledAt != nil && camp.ScheduledAt.After(time.Now().UTC()) {
		newStatus = "scheduled"
	}

	if err := s.campaignRepo.UpdateStatus(context.Background(), accountID, campaignID, newStatus); err != nil {
		return err
	}

	logger.WithComponent("campaign").Info("campaign resumed",
		"campaign_id", campaignID,
		"account_id", accountID,
		"new_status", newStatus,
	)
	return nil
}

// StopCampaign stops/cancels an active, paused, or scheduled campaign
func (s *CampaignService) StopCampaign(accountID, campaignID uint) error {
	camp, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID)
	if err != nil {
		return err
	}

	if camp.Status == "completed" {
		return errors.New("cannot stop already completed campaign")
	}

	if err := s.campaignRepo.UpdateStatus(context.Background(), accountID, campaignID, "cancelled"); err != nil {
		return err
	}

	logger.WithComponent("campaign").Info("campaign stopped",
		"campaign_id", campaignID,
		"account_id", accountID,
		"previous_status", camp.Status,
	)
	return nil
}

// CompleteCampaign manually marks an ongoing campaign as completed
func (s *CampaignService) CompleteCampaign(accountID, campaignID uint) error {
	camp, err := s.campaignRepo.GetByID(context.Background(), accountID, campaignID)
	if err != nil {
		return err
	}

	if err := s.campaignRepo.UpdateStatus(context.Background(), accountID, campaignID, "completed"); err != nil {
		return err
	}

	logger.WithComponent("campaign").Info("campaign marked as completed",
		"campaign_id", campaignID,
		"account_id", accountID,
		"previous_status", camp.Status,
	)
	return nil
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
	if campaign.Status == "cancelled" {
		return 0, errors.New("cannot trigger cancelled campaign")
	}
	if campaign.Status == "paused" {
		return 0, errors.New("cannot trigger paused campaign")
	}

	// Fetch all contacts belonging to the account via pagination
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
		// Ongoing campaigns deduplication check: do not deliver twice to same contact
		if campaign.CampaignType == "ongoing" {
			hasDelivered, _ := s.campaignRepo.HasDeliveredToContact(context.Background(), accountID, campaign.ID, contact.ID)
			if hasDelivered {
				continue
			}
		}

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
		senderID := uint(0)
		if campaign.SenderID != nil {
			senderID = *campaign.SenderID
		}
		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       senderID,
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

			if s.hub != nil {
				s.hub.Broadcast(&ws.Event{
					Name:           ws.EventMessageCreated,
					AccountID:      accountID,
					ConversationID: conv.ID,
					Data:           msg,
				})
			}
		}
	}

	// Update campaign status
	if campaign.CampaignType == "ongoing" {
		campaign.Status = "active"
	} else {
		campaign.Status = "completed"
	}
	_ = s.db.Save(&campaign)

	logger.WithComponent("campaign").Info("campaign triggered",
		"campaign_id", campaign.ID,
		"account_id", accountID,
		"campaign_type", campaign.CampaignType,
		"status", campaign.Status,
		"target_contacts", len(targetContacts),
		"sent_count", sentCount,
	)

	return sentCount, nil
}

// TriggerOngoingCampaignForContact matches active ongoing campaigns for an incoming contact on an inbox
func (s *CampaignService) TriggerOngoingCampaignForContact(accountID, inboxID, contactID, conversationID uint) (int, error) {
	if inboxID == 0 || contactID == 0 {
		return 0, nil
	}

	ongoingCampaigns, err := s.campaignRepo.ListActiveOngoing(context.Background(), accountID, inboxID)
	if err != nil || len(ongoingCampaigns) == 0 {
		return 0, nil
	}

	contact, err := s.contactRepo.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		return 0, nil
	}

	triggeredCount := 0
	now := time.Now().UTC()

	for _, camp := range ongoingCampaigns {
		// Anti-spam deduplication: has this contact already received this campaign?
		hasDelivered, err := s.campaignRepo.HasDeliveredToContact(context.Background(), accountID, camp.ID, contactID)
		if err != nil || hasDelivered {
			continue
		}

		// Check audience filter if specified
		if camp.Audience != "" && camp.Audience != "all" {
			if !s.contactMatchesAudience(accountID, contact, camp.Audience) {
				continue
			}
		}

		senderID := uint(0)
		if camp.SenderID != nil {
			senderID = *camp.SenderID
		}

		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conversationID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       senderID,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        camp.Message,
			Status:         domain.MessageStatusSent,
		}

		if err := s.msgRepo.Create(&msg); err == nil {
			del := domain.CampaignDelivery{
				AccountID:      accountID,
				CampaignID:     camp.ID,
				ContactID:      contactID,
				ConversationID: conversationID,
				Status:         "sent",
				SentAt:         now,
			}
			_ = s.db.Create(&del)
			triggeredCount++

			if s.hub != nil {
				s.hub.Broadcast(&ws.Event{
					Name:           ws.EventMessageCreated,
					AccountID:      accountID,
					ConversationID: conversationID,
					Data:           msg,
				})
			}

			logger.WithComponent("campaign").Info("ongoing campaign delivered to contact",
				"campaign_id", camp.ID,
				"account_id", accountID,
				"contact_id", contactID,
				"conversation_id", conversationID,
			)
		}
	}

	return triggeredCount, nil
}

// contactMatchesAudience validates if a contact matches an audience specification
func (s *CampaignService) contactMatchesAudience(accountID uint, contact *domain.Contact, audienceStr string) bool {
	audienceStr = strings.TrimSpace(audienceStr)
	if audienceStr == "" || audienceStr == "all" {
		return true
	}

	var rawMap map[string]any
	if err := json.Unmarshal([]byte(audienceStr), &rawMap); err == nil {
		if rawIDs, ok := rawMap["contact_ids"].([]any); ok {
			for _, r := range rawIDs {
				switch v := r.(type) {
				case float64:
					if uint(v) == contact.ID {
						return true
					}
				case string:
					if id, err := strconv.ParseUint(v, 10, 64); err == nil && uint(id) == contact.ID {
						return true
					}
				}
			}
			return false
		}
		if rawLabels, ok := rawMap["labels"].([]any); ok {
			var labelNames []string
			for _, l := range rawLabels {
				if s, ok := l.(string); ok {
					labelNames = append(labelNames, s)
				}
			}
			var count int64
			s.db.Table("conversations").
				Joins("JOIN conversation_labels ON conversation_labels.conversation_id = conversations.id").
				Joins("JOIN labels ON labels.id = conversation_labels.label_id").
				Where("conversations.account_id = ? AND conversations.contact_id = ? AND labels.title IN (?)", accountID, contact.ID, labelNames).
				Count(&count)
			return count > 0
		}
	}

	// Fallback to label match
	var count int64
	s.db.Table("conversations").
		Joins("JOIN conversation_labels ON conversation_labels.conversation_id = conversations.id").
		Joins("JOIN labels ON labels.id = conversation_labels.label_id").
		Where("conversations.account_id = ? AND conversations.contact_id = ? AND (labels.title = ? OR labels.title LIKE ?)", accountID, contact.ID, audienceStr, "%"+audienceStr+"%").
		Count(&count)
	return count > 0
}

// StartScheduledCampaignWorker periodically checks and triggers scheduled and ongoing campaigns
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
				s.ProcessOngoingCampaigns()
			}
		}
	}()
}

// ProcessScheduledCampaigns finds due scheduled campaigns and dispatches or activates them
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

// ProcessOngoingCampaigns evaluates active ongoing campaigns for new contacts
func (s *CampaignService) ProcessOngoingCampaigns() int {
	var ongoing []domain.Campaign
	err := s.db.Where("status = ? AND campaign_type = ?", "active", "ongoing").Find(&ongoing).Error
	if err != nil || len(ongoing) == 0 {
		return 0
	}

	totalDispatched := 0
	for _, c := range ongoing {
		if count, err := s.TriggerCampaign(c.AccountID, c.ID); err == nil {
			totalDispatched += count
		}
	}

	if totalDispatched > 0 {
		logger.WithComponent("campaign").Info("processed ongoing campaigns batch",
			"active_ongoing_count", len(ongoing),
			"new_contacts_dispatched", totalDispatched,
		)
	}

	return totalDispatched
}
