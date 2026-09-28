package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type ContactRepository struct {
	db            *gorm.DB
	conversations *ConversationRepository
	messages      *MessageRepository
	tickets       *TicketRepository
	orders        *OrderRepository
	campaigns     *CampaignRepository
	projections   contactMergeProjectionPort
}

type contactMergeProjectionPort interface {
	ReassignContact(accountID, fromContactID, toContactID uint) error
}

func NewContactRepository(db *gorm.DB) *ContactRepository {
	r := NewContactRepositoryWithOwnerPorts(db, &ConversationRepository{db: db}, &MessageRepository{db: db}, NewTicketRepository(db), &OrderRepository{db: db}, NewCampaignRepository(db))
	r.projections = newCombinedContactMergeProjection(db)
	return r
}

func NewContactRepositoryWithOwnerPorts(db *gorm.DB, conversations *ConversationRepository, messages *MessageRepository, tickets *TicketRepository, orders *OrderRepository, campaigns *CampaignRepository) *ContactRepository {
	return &ContactRepository{db: db, conversations: conversations, messages: messages, tickets: tickets, orders: orders, campaigns: campaigns}
}

func (r *ContactRepository) Create(contact *domain.Contact) error {
	return r.db.Create(contact).Error
}

func (r *ContactRepository) FindByID(accountID, id uint) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Preload("Company").Preload("Labels").Where("account_id = ? AND id = ?", accountID, id).First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) FindAccountIDByGlobalID(id uint) (*uint, error) {
	var accountID uint
	err := r.db.Model(&domain.Contact{}).Where("id = ?", id).Pluck("account_id", &accountID).Error
	if err != nil {
		return nil, err
	}
	if accountID == 0 {
		return nil, nil
	}
	return &accountID, nil
}

func (r *ContactRepository) Exists(accountID, id uint) (bool, error) {
	var count int64
	err := r.db.Model(&domain.Contact{}).Where("account_id = ? AND id = ?", accountID, id).Count(&count).Error
	return count == 1, err
}

func (r *ContactRepository) FindSummaryByID(accountID, id uint) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&contact).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &contact, err
}

func (r *ContactRepository) VisitorTokenMatches(accountID, contactID, inboxID uint, token string) bool {
	contact, err := r.FindByID(accountID, contactID)
	if err == nil && contact != nil && contact.PubsubToken != "" && token == contact.PubsubToken {
		return true
	}
	var contactInbox domain.ContactInbox
	return r.db.Where("contact_id = ? AND inbox_id = ? AND source_id = ?", contactID, inboxID, token).First(&contactInbox).Error == nil
}

func (r *ContactRepository) FindByEmail(accountID uint, email string) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Where("account_id = ? AND email = ?", accountID, email).First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) EmailExists(accountID uint, email string) (bool, error) {
	var count int64
	err := r.db.Model(&domain.Contact{}).
		Where("account_id = ? AND LOWER(email) = ?", accountID, strings.ToLower(strings.TrimSpace(email))).
		Count(&count).Error
	return count > 0, err
}

func (r *ContactRepository) FindByIdentifier(accountID uint, identifier string) (*domain.Contact, error) {
	var contact domain.Contact
	err := r.db.Where("account_id = ? AND identifier = ?", accountID, identifier).First(&contact).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) FindImportMatch(accountID uint, email, identifier, phone, name string) (*domain.Contact, error) {
	var contact domain.Contact
	query := r.db.Where("account_id = ?", accountID)
	switch {
	case email != "" && identifier != "":
		query = query.Where("email = ? OR identifier = ?", email, identifier)
	case email != "":
		query = query.Where("email = ?", email)
	case identifier != "":
		query = query.Where("identifier = ?", identifier)
	case phone != "":
		query = query.Where("phone_number = ?", phone)
	default:
		query = query.Where("name = ?", name)
	}
	if err := query.First(&contact).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &contact, nil
}

func (r *ContactRepository) ResolvePublic(accountID, inboxID uint, idOrSourceID string) (*domain.Contact, *domain.ContactInbox, error) {
	value := strings.TrimSpace(idOrSourceID)
	if value == "" {
		return nil, nil, nil
	}
	if numericID, err := strconv.ParseUint(value, 10, 64); err == nil && numericID > 0 {
		contact, err := r.FindByID(accountID, uint(numericID))
		if err != nil {
			return nil, nil, err
		}
		if contact != nil {
			var relation domain.ContactInbox
			result := r.db.Where("contact_id = ? AND inbox_id = ?", contact.ID, inboxID).First(&relation)
			if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, nil, result.Error
			}
			return contact, &relation, nil
		}
	}
	var relation domain.ContactInbox
	if err := r.db.Where("inbox_id = ? AND source_id = ?", inboxID, value).First(&relation).Error; err == nil {
		contact, err := r.FindByID(accountID, relation.ContactID)
		return contact, &relation, err
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil, err
	}
	contact, err := r.FindByIdentifier(accountID, value)
	if err != nil || contact == nil {
		return contact, nil, err
	}
	result := r.db.Where("contact_id = ? AND inbox_id = ?", contact.ID, inboxID).First(&relation)
	if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil, result.Error
	}
	return contact, &relation, nil
}

func (r *ContactRepository) UpdatePublic(accountID, contactID uint, updates, customAttrs map[string]any) (*domain.Contact, error) {
	contact, err := r.FindByID(accountID, contactID)
	if err != nil {
		return nil, err
	}
	if contact == nil {
		return nil, gorm.ErrRecordNotFound
	}
	if updates == nil {
		updates = make(map[string]any)
	}
	if customAttrs != nil {
		existing := make(map[string]any)
		if strings.TrimSpace(contact.CustomAttributes) != "" {
			_ = json.Unmarshal([]byte(contact.CustomAttributes), &existing)
		}
		for key, value := range customAttrs {
			if value == nil {
				delete(existing, key)
			} else {
				existing[key] = value
			}
		}
		encoded, err := json.Marshal(existing)
		if err != nil {
			return nil, err
		}
		updates["custom_attributes"] = string(encoded)
	}
	if len(updates) > 0 {
		if err := r.db.Model(&domain.Contact{}).Where("account_id = ? AND id = ?", accountID, contactID).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	return r.FindByID(accountID, contactID)
}

func (r *ContactRepository) List(accountID uint, page, pageSize int, search string) ([]domain.Contact, int64, error) {
	return r.ListFiltered(accountID, page, pageSize, search, "", nil)
}

func (r *ContactRepository) CountForReport(accountID uint, since, until *time.Time) (int64, error) {
	query := r.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)
	if since != nil {
		query = query.Where("created_at >= ?", *since)
	}
	if until != nil {
		query = query.Where("created_at <= ?", *until)
	}
	var count int64
	err := query.Count(&count).Error
	return count, err
}

func (r *ContactRepository) ListFiltered(accountID uint, page, pageSize int, search, label string, companyID *uint) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("contacts.account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(contacts.name) LIKE ? OR LOWER(contacts.email) LIKE ? OR contacts.phone_number LIKE ? OR contacts.identifier LIKE ?", s, s, s, s)
	}

	if companyID != nil && *companyID > 0 {
		query = query.Where("contacts.company_id = ?", *companyID)
	}

	if strings.TrimSpace(label) != "" {
		lbl := strings.TrimSpace(label)
		query = query.Joins("JOIN contact_labels ON contact_labels.contact_id = contacts.id").
			Joins("JOIN labels ON labels.id = contact_labels.label_id").
			Where("labels.account_id = ? AND (LOWER(labels.title) = ? OR labels.id = ?)", accountID, strings.ToLower(lbl), lbl).
			Distinct()
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").Offset(offset).Limit(pageSize).Order("contacts.id DESC").Find(&contacts).Error
	return contacts, total, err
}

func (r *ContactRepository) Update(contact *domain.Contact) error {
	return r.db.Save(contact).Error
}

func (r *ContactRepository) CreateNote(note *domain.ContactNote) error {
	return r.db.Create(note).Error
}

func (r *ContactRepository) UpdateNote(note *domain.ContactNote) error {
	return r.db.Save(note).Error
}

func (r *ContactRepository) DeleteNote(accountID, contactID, noteID uint) error {
	return r.db.Where("account_id = ? AND contact_id = ? AND id = ?", accountID, contactID, noteID).Delete(&domain.ContactNote{}).Error
}

func (r *ContactRepository) FindNote(accountID, contactID, noteID uint) (*domain.ContactNote, error) {
	var note domain.ContactNote
	err := r.db.Where("account_id = ? AND contact_id = ? AND id = ?", accountID, contactID, noteID).First(&note).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &note, nil
}

func (r *ContactRepository) ListNotes(accountID, contactID uint) ([]domain.ContactNote, error) {
	notes := make([]domain.ContactNote, 0)
	err := r.db.Where("account_id = ? AND contact_id = ?", accountID, contactID).Order("id DESC").Find(&notes).Error
	return notes, err
}

var ErrContactHasActiveConversations = errors.New("cannot delete contact with active conversations")

func (r *ContactRepository) Delete(accountID, id uint) error {
	if r.conversations == nil {
		return gorm.ErrInvalidData
	}
	if err := r.conversations.DetachContactIfInactive(accountID, id); err != nil {
		return err
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// CUS-owned relations are removed only after CON accepts the detach command.
		_ = tx.Where("contact_id = ?", id).Delete(&domain.ContactInbox{}).Error
		_ = tx.Where("contact_id = ?", id).Delete(&domain.ContactLabel{}).Error
		_ = tx.Where("account_id = ? AND contact_id = ?", accountID, id).Delete(&domain.ContactNote{}).Error
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Contact{}).Error
	})
}

func (r *ContactRepository) CreateContactInbox(ci *domain.ContactInbox) error {
	return r.db.Create(ci).Error
}

func (r *ContactRepository) FindOrCreateContactInbox(contactID, inboxID uint, sourceID string) (*domain.ContactInbox, error) {
	var ci domain.ContactInbox
	err := r.db.Where("inbox_id = ? AND source_id = ?", inboxID, sourceID).First(&ci).Error
	if err == nil {
		return &ci, nil
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		ci = domain.ContactInbox{
			ContactID: contactID,
			InboxID:   inboxID,
			SourceID:  sourceID,
		}
		if err := r.db.Create(&ci).Error; err != nil {
			return nil, err
		}
		return &ci, nil
	}

	return nil, err
}

func (r *ContactRepository) FindContactBySourceID(inboxID uint, sourceID string) (*domain.Contact, error) {
	var ci domain.ContactInbox
	err := r.db.Preload("Contact").Where("inbox_id = ? AND source_id = ?", inboxID, sourceID).First(&ci).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return ci.Contact, nil
}

// SetWidgetUser resolves or creates a CUS-owned contact and its ContactInbox mapping.
func (r *ContactRepository) SetWidgetUser(accountID, inboxID uint, identifier, name, email, phone, avatarURL string, customAttrs map[string]any, sourceID string) (*domain.Contact, error) {
	identifier = strings.TrimSpace(identifier)
	email = strings.ToLower(strings.TrimSpace(email))
	phone = strings.TrimSpace(phone)
	name = strings.TrimSpace(name)
	avatarURL = strings.TrimSpace(avatarURL)
	sourceID = strings.TrimSpace(sourceID)

	var contact *domain.Contact
	var err error
	if identifier != "" {
		contact, err = r.FindByIdentifier(accountID, identifier)
		if err != nil {
			return nil, err
		}
	}
	if contact == nil && email != "" {
		var found domain.Contact
		err = r.db.Where("account_id = ? AND email = ?", accountID, email).First(&found).Error
		if err == nil {
			contact = &found
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	if contact == nil && sourceID != "" {
		contact, err = r.FindContactBySourceID(inboxID, sourceID)
		if err != nil {
			return nil, err
		}
	}
	if contact == nil {
		displayName := name
		if displayName == "" {
			switch {
			case email != "":
				displayName = strings.Split(email, "@")[0]
			case identifier != "":
				displayName = "User " + identifier
			default:
				displayName = "Visitor"
			}
		}
		contact = &domain.Contact{AccountID: accountID, Name: displayName, Email: email, PhoneNumber: phone, Identifier: identifier, AvatarURL: avatarURL}
		if customAttrs != nil {
			encoded, marshalErr := json.Marshal(customAttrs)
			if marshalErr != nil {
				return nil, marshalErr
			}
			contact.CustomAttributes = string(encoded)
		}
		if err := r.db.Create(contact).Error; err != nil {
			return nil, err
		}
	} else {
		updates := map[string]any{}
		if name != "" {
			updates["name"] = name
		}
		if email != "" {
			updates["email"] = email
		}
		if phone != "" {
			updates["phone_number"] = phone
		}
		if identifier != "" {
			updates["identifier"] = identifier
		}
		if avatarURL != "" {
			updates["avatar_url"] = avatarURL
		}
		contact, err = r.UpdatePublic(accountID, contact.ID, updates, customAttrs)
		if err != nil {
			return nil, err
		}
	}

	effectiveSourceID := sourceID
	if effectiveSourceID == "" {
		if identifier != "" {
			effectiveSourceID = identifier
		} else {
			effectiveSourceID = fmt.Sprintf("src_%d_%d", inboxID, contact.ID)
		}
	}
	var relation domain.ContactInbox
	err = r.db.Where("inbox_id = ? AND contact_id = ?", inboxID, contact.ID).First(&relation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		relation = domain.ContactInbox{InboxID: inboxID, ContactID: contact.ID, SourceID: effectiveSourceID}
		if err := r.db.Create(&relation).Error; err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else if effectiveSourceID != "" && relation.SourceID != effectiveSourceID {
		if err := r.db.Model(&relation).Update("source_id", effectiveSourceID).Error; err != nil {
			return nil, err
		}
	}
	return contact, nil
}

func (r *ContactRepository) MergeContacts(accountID, baseID, mergeeID uint) error {
	if baseID == mergeeID {
		return gorm.ErrInvalidData
	}
	var exists int64
	if err := r.db.Model(&domain.Contact{}).Where("account_id = ? AND id IN ?", accountID, []uint{baseID, mergeeID}).Count(&exists).Error; err != nil {
		return err
	}
	if exists != 2 {
		return gorm.ErrRecordNotFound
	}
	hasOwnerPort := r.conversations != nil || r.messages != nil || r.tickets != nil || r.orders != nil || r.campaigns != nil
	if hasOwnerPort {
		if r.conversations == nil || r.messages == nil || r.tickets == nil || r.orders == nil || r.campaigns == nil {
			return gorm.ErrInvalidData
		}
		commands := []func() error{
			func() error { return r.conversations.ReassignContact(accountID, mergeeID, baseID) },
			func() error { return r.messages.ReassignContactSender(accountID, mergeeID, baseID) },
			func() error { return r.tickets.ReassignContact(accountID, mergeeID, baseID) },
			func() error { return r.orders.ReassignContact(accountID, mergeeID, baseID) },
			func() error { return r.campaigns.ReassignContact(context.Background(), accountID, mergeeID, baseID) },
		}
		for _, command := range commands {
			if err := command(); err != nil {
				return err
			}
		}
	}
	if r.projections != nil {
		if err := r.projections.ReassignContact(accountID, mergeeID, baseID); err != nil {
			return err
		}
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 0. Fetch base and mergee contacts
		var baseContact domain.Contact
		if err := tx.Where("account_id = ? AND id = ?", accountID, baseID).First(&baseContact).Error; err != nil {
			return err
		}

		var mergeeContact domain.Contact
		if err := tx.Where("account_id = ? AND id = ?", accountID, mergeeID).First(&mergeeContact).Error; err != nil {
			return err
		}

		// 1. Merge attributes: give preference to base contact attributes; populate missing fields from mergee
		updates := make(map[string]any)
		if baseContact.Name == "" && mergeeContact.Name != "" {
			updates["name"] = mergeeContact.Name
		}
		if baseContact.Email == "" && mergeeContact.Email != "" {
			updates["email"] = mergeeContact.Email
		}
		if baseContact.PhoneNumber == "" && mergeeContact.PhoneNumber != "" {
			updates["phone_number"] = mergeeContact.PhoneNumber
		}
		if baseContact.Identifier == "" && mergeeContact.Identifier != "" {
			updates["identifier"] = mergeeContact.Identifier
		}
		if baseContact.AvatarURL == "" && mergeeContact.AvatarURL != "" {
			updates["avatar_url"] = mergeeContact.AvatarURL
		}
		if baseContact.CompanyID == nil && mergeeContact.CompanyID != nil {
			updates["company_id"] = mergeeContact.CompanyID
		}
		if !baseContact.Blocked && mergeeContact.Blocked {
			updates["blocked"] = true
		}

		// Merge CustomAttributes JSON
		baseCustomAttrs := make(map[string]any)
		if strings.TrimSpace(baseContact.CustomAttributes) != "" {
			_ = json.Unmarshal([]byte(baseContact.CustomAttributes), &baseCustomAttrs)
		}
		mergeeCustomAttrs := make(map[string]any)
		if strings.TrimSpace(mergeeContact.CustomAttributes) != "" {
			_ = json.Unmarshal([]byte(mergeeContact.CustomAttributes), &mergeeCustomAttrs)
		}

		mergedAttrs := make(map[string]any)
		for k, v := range mergeeCustomAttrs {
			mergedAttrs[k] = v
		}
		// Base attributes take precedence
		for k, v := range baseCustomAttrs {
			mergedAttrs[k] = v
		}
		if len(mergedAttrs) > 0 {
			mergedJSON, err := json.Marshal(mergedAttrs)
			if err == nil {
				updates["custom_attributes"] = string(mergedJSON)
			}
		}
		updates["object_version"] = gorm.Expr("object_version + 1")

		if err := tx.Model(&domain.Contact{}).
			Where("account_id = ? AND id = ?", accountID, baseID).
			Updates(updates).Error; err != nil {
			return err
		}

		// 2. Handle ContactInboxes with duplicate resolution
		var mergeeInboxes []domain.ContactInbox
		if err := tx.Where("contact_id = ?", mergeeID).Find(&mergeeInboxes).Error; err != nil {
			return err
		}

		for _, mi := range mergeeInboxes {
			var count int64
			if err := tx.Model(&domain.ContactInbox{}).
				Where("contact_id = ? AND inbox_id = ?", baseID, mi.InboxID).
				Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				// Base contact already has identity in this inbox, remove mergee's duplicate
				if err := tx.Delete(&domain.ContactInbox{}, mi.ID).Error; err != nil {
					return err
				}
			} else {
				// Move identity to base contact
				if err := tx.Model(&domain.ContactInbox{}).
					Where("id = ?", mi.ID).
					Update("contact_id", baseID).Error; err != nil {
					return err
				}
			}
		}

		// Migrate CUS-owned contact notes.
		if err := tx.Model(&domain.ContactNote{}).
			Where("account_id = ? AND contact_id = ?", accountID, mergeeID).
			Update("contact_id", baseID).Error; err != nil {
			return err
		}

		// Migrate CUS-owned contact labels.
		var mergeeLabels []domain.ContactLabel
		if err := tx.Where("contact_id = ?", mergeeID).Find(&mergeeLabels).Error; err == nil {
			for _, ml := range mergeeLabels {
				var existing domain.ContactLabel
				if err := tx.Where("contact_id = ? AND label_id = ?", baseID, ml.LabelID).First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
					_ = tx.Create(&domain.ContactLabel{ContactID: baseID, LabelID: ml.LabelID}).Error
				}
			}
			_ = tx.Where("contact_id = ?", mergeeID).Delete(&domain.ContactLabel{}).Error
		}

		// Delete mergee only after every owner command succeeded.
		if err := tx.Where("account_id = ? AND id = ?", accountID, mergeeID).
			Delete(&domain.Contact{}).Error; err != nil {
			return err
		}
		changeID := foundation.GenerateUUID()
		return NewJournalRepository(tx).RecordLocalChange(tx, &domain.LocalChangeJournal{
			ChangeID: changeID, SourceModule: "CUS", AccountID: accountID,
			ObjectType: "Contact", ObjectID: baseID, Action: "contact_merge",
			ChangedFields: fmt.Sprintf("merged_contact_id:%d", mergeeID), ActorType: "system",
			Reason:     fmt.Sprintf("Merged contact #%d into #%d", mergeeID, baseID),
			SourceType: "cus.contact.merged.v1", CommandID: changeID, CorrelationID: changeID,
			Status: "pending", OccurredAt: time.Now().UTC(),
		})
	})
}

// ListActive retrieves contacts with ongoing/active (non-resolved) conversations
func (r *ContactRepository) ListActive(accountID uint, page, pageSize int, search string) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	if r.conversations == nil {
		return nil, 0, gorm.ErrInvalidData
	}
	activeIDs, err := r.conversations.ActiveContactIDs(accountID)
	if err != nil {
		return nil, 0, err
	}

	query := r.db.Model(&domain.Contact{}).
		Where("account_id = ? AND id IN (?)", accountID, activeIDs)

	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ?", s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err = query.Preload("Company").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("id DESC").
		Find(&contacts).Error

	return contacts, total, err
}

// Search retrieves contacts matching query across name, email, phone, identifier, and custom_attributes
func (r *ContactRepository) Search(accountID uint, search string, page, pageSize int) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ? OR phone_number LIKE ? OR identifier LIKE ? OR LOWER(custom_attributes) LIKE ?", s, s, s, s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("id DESC").
		Find(&contacts).Error

	return contacts, total, err
}

func (r *ContactRepository) SearchForGlobal(accountID uint, search string, limit int) ([]domain.Contact, error) {
	if limit <= 0 {
		limit = 20
	}
	pattern := "%" + search + "%"
	var contacts []domain.Contact
	err := r.db.Where("account_id = ? AND (name LIKE ? OR email LIKE ? OR phone_number LIKE ?)", accountID, pattern, pattern, pattern).
		Limit(limit).Find(&contacts).Error
	return contacts, err
}

// Filter applies structured rules to filter contacts dynamically
func (r *ContactRepository) Filter(accountID uint, filters []domain.FilterRule, page, pageSize int) ([]domain.Contact, int64, error) {
	var contacts []domain.Contact
	var total int64

	query := r.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)

	for _, rule := range filters {
		op := strings.ToLower(rule.FilterOperator)
		key := strings.ToLower(rule.AttributeKey)

		switch key {
		case "name":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("LOWER(name) = ?", strings.ToLower(valStr))
				} else if op == "not_equal_to" {
					query = query.Where("LOWER(name) != ?", strings.ToLower(valStr))
				} else if op == "contains" {
					query = query.Where("LOWER(name) LIKE ?", "%"+strings.ToLower(valStr)+"%")
				} else if op == "does_not_contain" {
					query = query.Where("LOWER(name) NOT LIKE ?", "%"+strings.ToLower(valStr)+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("name IS NOT NULL AND name != ''")
			} else if op == "is_not_present" {
				query = query.Where("name IS NULL OR name = ''")
			}

		case "email":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("LOWER(email) = ?", strings.ToLower(valStr))
				} else if op == "not_equal_to" {
					query = query.Where("LOWER(email) != ?", strings.ToLower(valStr))
				} else if op == "contains" {
					query = query.Where("LOWER(email) LIKE ?", "%"+strings.ToLower(valStr)+"%")
				} else if op == "does_not_contain" {
					query = query.Where("LOWER(email) NOT LIKE ?", "%"+strings.ToLower(valStr)+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("email IS NOT NULL AND email != ''")
			} else if op == "is_not_present" {
				query = query.Where("email IS NULL OR email = ''")
			}

		case "phone_number", "phone":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("phone_number = ?", valStr)
				} else if op == "not_equal_to" {
					query = query.Where("phone_number != ?", valStr)
				} else if op == "contains" {
					query = query.Where("phone_number LIKE ?", "%"+valStr+"%")
				} else if op == "does_not_contain" {
					query = query.Where("phone_number NOT LIKE ?", "%"+valStr+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("phone_number IS NOT NULL AND phone_number != ''")
			} else if op == "is_not_present" {
				query = query.Where("phone_number IS NULL OR phone_number = ''")
			}

		case "identifier":
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if op == "equal_to" {
					query = query.Where("identifier = ?", valStr)
				} else if op == "not_equal_to" {
					query = query.Where("identifier != ?", valStr)
				} else if op == "contains" {
					query = query.Where("identifier LIKE ?", "%"+valStr+"%")
				}
			}
			if op == "is_present" {
				query = query.Where("identifier IS NOT NULL AND identifier != ''")
			} else if op == "is_not_present" {
				query = query.Where("identifier IS NULL OR identifier = ''")
			}

		case "company_id":
			if op == "equal_to" {
				query = query.Where("company_id IN (?)", rule.Values)
			} else if op == "not_equal_to" {
				query = query.Where("company_id NOT IN (?)", rule.Values)
			} else if op == "is_present" {
				query = query.Where("company_id IS NOT NULL")
			} else if op == "is_not_present" {
				query = query.Where("company_id IS NULL")
			}

		case "labels":
			if op == "equal_to" || op == "contains" {
				query = query.Where("id IN (SELECT contact_id FROM contact_labels JOIN labels ON contact_labels.label_id = labels.id WHERE labels.title IN (?))", rule.Values)
			}

		default:
			// custom attribute or generic field
			for _, v := range rule.Values {
				valStr := fmt.Sprintf("%v", v)
				if strings.HasPrefix(key, "custom_attributes.") {
					attrKey := strings.TrimPrefix(key, "custom_attributes.")
					query = query.Where("custom_attributes LIKE ? AND custom_attributes LIKE ?", "%"+attrKey+"%", "%"+valStr+"%")
				} else {
					query = query.Where("custom_attributes LIKE ?", "%"+valStr+"%")
				}
			}
		}
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("Company").Preload("Labels").
		Offset(offset).Limit(pageSize).
		Order("id DESC").
		Find(&contacts).Error

	return contacts, total, err
}

// ImportContacts handles batch contact import with smart deduplication/upsert
func (r *ContactRepository) ImportContacts(accountID uint, contacts []domain.Contact) (int, int, error) {
	importedCount := 0
	updatedCount := 0

	err := r.db.Transaction(func(tx *gorm.DB) error {
		for i := range contacts {
			c := contacts[i]
			c.AccountID = accountID

			var existing domain.Contact
			found := false

			if c.Email != "" {
				if err := tx.Where("account_id = ? AND LOWER(email) = ?", accountID, strings.ToLower(c.Email)).First(&existing).Error; err == nil {
					found = true
				}
			}
			if !found && c.Identifier != "" {
				if err := tx.Where("account_id = ? AND identifier = ?", accountID, c.Identifier).First(&existing).Error; err == nil {
					found = true
				}
			}
			if !found && c.PhoneNumber != "" {
				if err := tx.Where("account_id = ? AND phone_number = ?", accountID, c.PhoneNumber).First(&existing).Error; err == nil {
					found = true
				}
			}

			if found {
				if c.Name != "" {
					existing.Name = c.Name
				}
				if c.PhoneNumber != "" {
					existing.PhoneNumber = c.PhoneNumber
				}
				if c.Identifier != "" {
					existing.Identifier = c.Identifier
				}
				if c.CustomAttributes != "" {
					existing.CustomAttributes = c.CustomAttributes
				}
				if c.CompanyID != nil {
					existing.CompanyID = c.CompanyID
				}
				if err := tx.Save(&existing).Error; err != nil {
					return err
				}
				updatedCount++
			} else {
				if err := tx.Create(&c).Error; err != nil {
					return err
				}
				importedCount++
			}
		}
		return nil
	})

	return importedCount, updatedCount, err
}

// DeleteAvatar removes the avatar URL for a contact
func (r *ContactRepository) DeleteAvatar(accountID, contactID uint) error {
	return r.db.Model(&domain.Contact{}).
		Where("account_id = ? AND id = ?", accountID, contactID).
		Update("avatar_url", "").Error
}

// DestroyCustomAttributes strips designated key(s) from a contact's custom attributes JSON
func (r *ContactRepository) DestroyCustomAttributes(accountID, contactID uint, keys []string) (*domain.Contact, error) {
	contact, err := r.FindByID(accountID, contactID)
	if err != nil || contact == nil {
		return nil, err
	}

	attrs := make(map[string]any)
	if contact.CustomAttributes != "" {
		_ = json.Unmarshal([]byte(contact.CustomAttributes), &attrs)
	}

	for _, k := range keys {
		delete(attrs, k)
	}

	newBytes, _ := json.Marshal(attrs)
	contact.CustomAttributes = string(newBytes)
	contact.ObjectVersion++

	if err := r.db.Save(contact).Error; err != nil {
		return nil, err
	}

	return contact, nil
}
