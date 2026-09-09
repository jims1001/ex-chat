package service

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type MigrationService struct {
	db *gorm.DB
}

func NewMigrationService(db *gorm.DB) *MigrationService {
	return &MigrationService{db: db}
}

func toIDString(v any) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case float64:
		return fmt.Sprintf("%.0f", val)
	case int:
		return strconv.Itoa(val)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// Migrate parses legacy data payloads and imports contacts, conversations, messages, and attachments
func (s *MigrationService) Migrate(ctx context.Context, accountID uint, resourceType string, rawData string) (*MigrationStats, error) {
	stats := &MigrationStats{Errors: make([]string, 0)}
	trimmed := strings.TrimSpace(rawData)
	if trimmed == "" {
		return stats, nil
	}

	legacyContactMap := make(map[string]uint)
	legacyConvMap := make(map[string]uint)
	legacyMsgMap := make(map[string]uint)

	// Check if JSON object or array
	if strings.HasPrefix(trimmed, "{") {
		// Try parsing as MigrationBundle
		var bundle MigrationBundle
		if err := json.Unmarshal([]byte(trimmed), &bundle); err == nil && (len(bundle.Contacts) > 0 || len(bundle.Conversations) > 0 || len(bundle.Messages) > 0 || len(bundle.Attachments) > 0) {
			s.importContacts(accountID, bundle.Contacts, legacyContactMap, stats)
			s.importConversations(accountID, bundle.Conversations, legacyContactMap, legacyConvMap, legacyMsgMap, stats)
			s.importMessages(accountID, bundle.Messages, legacyConvMap, legacyMsgMap, stats)
			s.importAttachments(accountID, bundle.Attachments, legacyMsgMap, stats)
			return stats, nil
		}

		// Try parsing as single object
		var singleMap map[string]any
		if err := json.Unmarshal([]byte(trimmed), &singleMap); err == nil {
			s.dispatchSingleObject(accountID, resourceType, singleMap, legacyContactMap, legacyConvMap, legacyMsgMap, stats)
			return stats, nil
		}
	} else if strings.HasPrefix(trimmed, "[") {
		// Array of JSON items
		var rawItems []map[string]any
		if err := json.Unmarshal([]byte(trimmed), &rawItems); err != nil {
			return stats, fmt.Errorf("invalid JSON array: %w", err)
		}

		rt := strings.ToLower(strings.TrimSpace(resourceType))
		switch rt {
		case "conversations", "conversation":
			var convItems []MigrationConversationItem
			_ = json.Unmarshal([]byte(trimmed), &convItems)
			s.importConversations(accountID, convItems, legacyContactMap, legacyConvMap, legacyMsgMap, stats)
		case "messages", "message":
			var msgItems []MigrationMessageItem
			_ = json.Unmarshal([]byte(trimmed), &msgItems)
			s.importMessages(accountID, msgItems, legacyConvMap, legacyMsgMap, stats)
		case "attachments", "attachment":
			var attItems []MigrationAttachmentItem
			_ = json.Unmarshal([]byte(trimmed), &attItems)
			s.importAttachments(accountID, attItems, legacyMsgMap, stats)
		default:
			// Auto-detect or default to contacts
			if len(rawItems) > 0 {
				first := rawItems[0]
				if _, hasMessages := first["messages"]; hasMessages || first["contact_email"] != nil || first["inbox_id"] != nil {
					var convItems []MigrationConversationItem
					_ = json.Unmarshal([]byte(trimmed), &convItems)
					s.importConversations(accountID, convItems, legacyContactMap, legacyConvMap, legacyMsgMap, stats)
					return stats, nil
				}
				if first["content"] != nil || first["message_type"] != nil {
					var msgItems []MigrationMessageItem
					_ = json.Unmarshal([]byte(trimmed), &msgItems)
					s.importMessages(accountID, msgItems, legacyConvMap, legacyMsgMap, stats)
					return stats, nil
				}
				if first["data_url"] != nil || first["file_url"] != nil {
					var attItems []MigrationAttachmentItem
					_ = json.Unmarshal([]byte(trimmed), &attItems)
					s.importAttachments(accountID, attItems, legacyMsgMap, stats)
					return stats, nil
				}
			}
			var contactItems []MigrationContactItem
			_ = json.Unmarshal([]byte(trimmed), &contactItems)
			s.importContacts(accountID, contactItems, legacyContactMap, stats)
		}
		return stats, nil
	}

	// CSV fallback
	return s.migrateCSV(accountID, resourceType, trimmed, legacyContactMap, legacyConvMap, stats)
}

func (s *MigrationService) dispatchSingleObject(accountID uint, resourceType string, obj map[string]any, legacyContactMap map[string]uint, legacyConvMap map[string]uint, legacyMsgMap map[string]uint, stats *MigrationStats) {
	bytes, _ := json.Marshal(obj)
	rt := strings.ToLower(strings.TrimSpace(resourceType))
	if rt == "conversations" || obj["messages"] != nil || obj["contact_email"] != nil {
		var item MigrationConversationItem
		_ = json.Unmarshal(bytes, &item)
		s.importConversations(accountID, []MigrationConversationItem{item}, legacyContactMap, legacyConvMap, legacyMsgMap, stats)
	} else if rt == "messages" || obj["content"] != nil {
		var item MigrationMessageItem
		_ = json.Unmarshal(bytes, &item)
		s.importMessages(accountID, []MigrationMessageItem{item}, legacyConvMap, legacyMsgMap, stats)
	} else if rt == "attachments" || obj["data_url"] != nil || obj["file_url"] != nil {
		var item MigrationAttachmentItem
		_ = json.Unmarshal(bytes, &item)
		s.importAttachments(accountID, []MigrationAttachmentItem{item}, legacyMsgMap, stats)
	} else {
		var item MigrationContactItem
		_ = json.Unmarshal(bytes, &item)
		s.importContacts(accountID, []MigrationContactItem{item}, legacyContactMap, stats)
	}
}

func (s *MigrationService) importContacts(accountID uint, contacts []MigrationContactItem, legacyMap map[string]uint, stats *MigrationStats) {
	for _, item := range contacts {
		name := strings.TrimSpace(item.Name)
		email := strings.TrimSpace(item.Email)
		phone := strings.TrimSpace(item.PhoneNumber)
		if phone == "" {
			phone = strings.TrimSpace(item.Phone)
		}
		identifier := strings.TrimSpace(item.Identifier)
		legacyID := toIDString(item.ID)

		if name == "" && email == "" && phone == "" && identifier == "" {
			continue
		}
		if name == "" && email != "" {
			name = strings.Split(email, "@")[0]
		}
		if name == "" {
			name = "Imported Contact"
		}

		var contact domain.Contact
		query := s.db.Where("account_id = ?", accountID)
		if email != "" && identifier != "" {
			query = query.Where("email = ? OR identifier = ?", email, identifier)
		} else if email != "" {
			query = query.Where("email = ?", email)
		} else if identifier != "" {
			query = query.Where("identifier = ?", identifier)
		} else if phone != "" {
			query = query.Where("phone_number = ?", phone)
		} else {
			query = query.Where("name = ?", name)
		}

		createdAt := time.Now().UTC()
		if item.CreatedAt != nil && !item.CreatedAt.IsZero() {
			createdAt = *item.CreatedAt
		}

		if err := query.First(&contact).Error; err != nil {
			contact = domain.Contact{
				AccountID:        accountID,
				Name:             name,
				Email:            email,
				PhoneNumber:      phone,
				Identifier:       identifier,
				CustomAttributes: item.CustomAttributes,
				CreatedAt:        createdAt,
			}
			if err := s.db.Create(&contact).Error; err == nil {
				stats.ContactsCount++
			} else {
				stats.Errors = append(stats.Errors, err.Error())
				continue
			}
		} else {
			contact.Name = name
			if phone != "" {
				contact.PhoneNumber = phone
			}
			if item.CustomAttributes != "" {
				contact.CustomAttributes = item.CustomAttributes
			}
			_ = s.db.Save(&contact).Error
			stats.ContactsCount++
		}

		if legacyID != "" {
			legacyMap[legacyID] = contact.ID
		}
		if email != "" {
			legacyMap[email] = contact.ID
		}
		if identifier != "" {
			legacyMap[identifier] = contact.ID
		}
	}
}

func (s *MigrationService) importConversations(accountID uint, convs []MigrationConversationItem, legacyContactMap map[string]uint, legacyConvMap map[string]uint, legacyMsgMap map[string]uint, stats *MigrationStats) {
	for _, item := range convs {
		legacyConvID := toIDString(item.ID)

		// 1. Resolve Contact
		var contactID uint
		if item.ContactID != nil {
			strID := toIDString(item.ContactID)
			if cid, ok := legacyContactMap[strID]; ok {
				contactID = cid
			} else if parsed, err := strconv.ParseUint(strID, 10, 64); err == nil {
				contactID = uint(parsed)
			}
		}
		if contactID == 0 && item.ContactEmail != "" {
			if cid, ok := legacyContactMap[item.ContactEmail]; ok {
				contactID = cid
			} else {
				var c domain.Contact
				if err := s.db.Where("account_id = ? AND email = ?", accountID, item.ContactEmail).First(&c).Error; err == nil {
					contactID = c.ID
				}
			}
		}
		if contactID == 0 && item.ContactIdentifier != "" {
			if cid, ok := legacyContactMap[item.ContactIdentifier]; ok {
				contactID = cid
			} else {
				var c domain.Contact
				if err := s.db.Where("account_id = ? AND identifier = ?", accountID, item.ContactIdentifier).First(&c).Error; err == nil {
					contactID = c.ID
				}
			}
		}
		// Create on-the-fly contact if needed
		if contactID == 0 {
			name := item.ContactName
			if name == "" && item.ContactEmail != "" {
				name = strings.Split(item.ContactEmail, "@")[0]
			}
			if name == "" {
				name = "Imported Contact"
			}
			newContact := domain.Contact{
				AccountID:  accountID,
				Name:       name,
				Email:      item.ContactEmail,
				Identifier: item.ContactIdentifier,
				CreatedAt:  time.Now().UTC(),
			}
			if err := s.db.Create(&newContact).Error; err == nil {
				contactID = newContact.ID
				stats.ContactsCount++
			} else {
				stats.Errors = append(stats.Errors, "failed to create conversation contact: "+err.Error())
				continue
			}
		}

		// 2. Resolve Inbox
		var inboxID uint
		if item.InboxID != nil {
			strInbox := toIDString(item.InboxID)
			if parsed, err := strconv.ParseUint(strInbox, 10, 64); err == nil {
				inboxID = uint(parsed)
			}
		}
		if inboxID == 0 {
			inboxID, _ = s.ensureDefaultInbox(accountID)
		}

		// 3. Status & Priority
		status := item.Status
		if status == "" {
			status = domain.ConversationStatusOpen
		}
		priority := item.Priority
		if priority == "" {
			priority = domain.PriorityMedium
		}

		createdAt := time.Now().UTC()
		if item.CreatedAt != nil && !item.CreatedAt.IsZero() {
			createdAt = *item.CreatedAt
		}

		displayID := item.DisplayID
		if displayID == 0 {
			var count int64
			s.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID).Count(&count)
			displayID = uint(count + 1)
		}

		conv := domain.Conversation{
			AccountID:      accountID,
			InboxID:        inboxID,
			ContactID:      contactID,
			Status:         status,
			Priority:       priority,
			DisplayID:      displayID,
			CreatedAt:      createdAt,
			LastActivityAt: createdAt,
			UpdatedAt:      createdAt,
		}

		if err := s.db.Create(&conv).Error; err != nil {
			stats.Errors = append(stats.Errors, err.Error())
			continue
		}
		stats.ConversationsCount++

		if legacyConvID != "" {
			legacyConvMap[legacyConvID] = conv.ID
		}

		// 4. If conversation has nested messages, import them
		if len(item.Messages) > 0 {
			for i := range item.Messages {
				item.Messages[i].ConversationID = conv.ID
			}
			s.importMessages(accountID, item.Messages, legacyConvMap, legacyMsgMap, stats)
		}
	}
}

func (s *MigrationService) importMessages(accountID uint, messages []MigrationMessageItem, legacyConvMap map[string]uint, legacyMsgMap map[string]uint, stats *MigrationStats) {
	for _, item := range messages {
		legacyMsgID := toIDString(item.ID)

		// 1. Resolve Conversation
		var convID uint
		if item.ConversationID != nil {
			strConv := toIDString(item.ConversationID)
			if cid, ok := legacyConvMap[strConv]; ok {
				convID = cid
			} else if parsed, err := strconv.ParseUint(strConv, 10, 64); err == nil {
				convID = uint(parsed)
			}
		}
		if convID == 0 {
			stats.Errors = append(stats.Errors, "message missing valid conversation mapping")
			continue
		}

		// 2. Sender and Message Types
		msgType := item.MessageType
		if msgType == "" {
			msgType = domain.MessageTypeIncoming
		}
		senderType := item.SenderType
		if senderType == "" {
			if msgType == domain.MessageTypeIncoming {
				senderType = domain.SenderTypeContact
			} else {
				senderType = domain.SenderTypeUser
			}
		}

		contentType := item.ContentType
		if contentType == "" {
			contentType = domain.ContentTypeText
		}

		createdAt := time.Now().UTC()
		if item.CreatedAt != nil && !item.CreatedAt.IsZero() {
			createdAt = *item.CreatedAt
		}

		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: convID,
			SenderType:     senderType,
			SenderID:       item.SenderID,
			MessageType:    msgType,
			ContentType:    contentType,
			Content:        item.Content,
			Private:        item.Private,
			CreatedAt:      createdAt,
			UpdatedAt:      createdAt,
		}

		if err := s.db.Create(&msg).Error; err != nil {
			stats.Errors = append(stats.Errors, err.Error())
			continue
		}
		stats.MessagesCount++

		if legacyMsgID != "" {
			legacyMsgMap[legacyMsgID] = msg.ID
		}

		// 3. If message has nested attachments, import them
		if len(item.Attachments) > 0 {
			for i := range item.Attachments {
				item.Attachments[i].MessageID = msg.ID
			}
			s.importAttachments(accountID, item.Attachments, legacyMsgMap, stats)
		}
	}
}

func (s *MigrationService) importAttachments(accountID uint, attachments []MigrationAttachmentItem, legacyMsgMap map[string]uint, stats *MigrationStats) {
	for _, item := range attachments {
		var msgID uint
		if item.MessageID != nil {
			strMsg := toIDString(item.MessageID)
			if mid, ok := legacyMsgMap[strMsg]; ok {
				msgID = mid
			} else if parsed, err := strconv.ParseUint(strMsg, 10, 64); err == nil {
				msgID = uint(parsed)
			}
		}
		if msgID == 0 {
			stats.Errors = append(stats.Errors, "attachment missing valid message mapping")
			continue
		}

		dataURL := item.DataURL
		if dataURL == "" {
			dataURL = item.FileURL
		}
		if dataURL == "" {
			continue
		}

		fileType := item.FileType
		if fileType == "" {
			fileType = "file"
		}

		createdAt := time.Now().UTC()
		if item.CreatedAt != nil && !item.CreatedAt.IsZero() {
			createdAt = *item.CreatedAt
		}

		att := domain.Attachment{
			AccountID: accountID,
			MessageID: msgID,
			FileType:  fileType,
			DataURL:   dataURL,
			FileSize:  item.FileSize,
			CreatedAt: createdAt,
		}

		if err := s.db.Create(&att).Error; err != nil {
			stats.Errors = append(stats.Errors, err.Error())
			continue
		}
		stats.AttachmentsCount++
	}
}

func (s *MigrationService) ensureDefaultInbox(accountID uint) (uint, error) {
	var inbox domain.Inbox
	if err := s.db.Where("account_id = ?", accountID).First(&inbox).Error; err == nil {
		return inbox.ID, nil
	}

	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	token := hex.EncodeToString(bytes)

	inbox = domain.Inbox{
		AccountID:    accountID,
		Name:         "Default Web Widget",
		ChannelType:  domain.ChannelWebWidget,
		WebsiteToken: token,
		CreatedAt:    time.Now().UTC(),
	}
	if err := s.db.Create(&inbox).Error; err != nil {
		return 0, err
	}
	return inbox.ID, nil
}

func (s *MigrationService) migrateCSV(accountID uint, resourceType, rawCSV string, legacyContactMap, legacyConvMap map[string]uint, stats *MigrationStats) (*MigrationStats, error) {
	reader := csv.NewReader(strings.NewReader(rawCSV))
	records, err := reader.ReadAll()
	if err != nil {
		return stats, fmt.Errorf("invalid CSV: %w", err)
	}
	if len(records) <= 1 {
		return stats, nil
	}

	header := records[0]
	colMap := make(map[string]int)
	for idx, col := range header {
		colMap[strings.ToLower(strings.TrimSpace(col))] = idx
	}

	getVal := func(row []string, keys ...string) string {
		for _, k := range keys {
			if idx, ok := colMap[k]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
		}
		return ""
	}

	rt := strings.ToLower(strings.TrimSpace(resourceType))
	if rt == "conversations" {
		convItems := make([]MigrationConversationItem, 0, len(records)-1)
		for i := 1; i < len(records); i++ {
			row := records[i]
			convItems = append(convItems, MigrationConversationItem{
				ID:           getVal(row, "id", "conversation_id"),
				ContactEmail: getVal(row, "contact_email", "email"),
				ContactName:  getVal(row, "contact_name", "name"),
				Status:       getVal(row, "status"),
				Priority:     getVal(row, "priority"),
			})
		}
		s.importConversations(accountID, convItems, legacyContactMap, legacyConvMap, make(map[string]uint), stats)
	} else if rt == "messages" {
		msgItems := make([]MigrationMessageItem, 0, len(records)-1)
		for i := 1; i < len(records); i++ {
			row := records[i]
			msgItems = append(msgItems, MigrationMessageItem{
				ID:             getVal(row, "id", "message_id"),
				ConversationID: getVal(row, "conversation_id"),
				Content:        getVal(row, "content", "body", "text"),
				MessageType:    getVal(row, "message_type", "type"),
			})
		}
		s.importMessages(accountID, msgItems, legacyConvMap, make(map[string]uint), stats)
	} else {
		contactItems := make([]MigrationContactItem, 0, len(records)-1)
		for i := 1; i < len(records); i++ {
			row := records[i]
			contactItems = append(contactItems, MigrationContactItem{
				ID:          getVal(row, "id", "customer_id", "identifier"),
				Name:        getVal(row, "name", "full_name"),
				Email:       getVal(row, "email", "email_address"),
				PhoneNumber: getVal(row, "phone", "phone_number", "mobile"),
				Identifier:  getVal(row, "identifier"),
			})
		}
		s.importContacts(accountID, contactItems, legacyContactMap, stats)
	}

	return stats, nil
}
