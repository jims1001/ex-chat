package modruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

type identityView struct {
	ID                 uint   `json:"id"`
	Name               string `json:"name"`
	DisplayName        string `json:"display_name"`
	Email              string `json:"email"`
	Role               string `json:"role"`
	Type               string `json:"type"`
	Availability       string `json:"availability"`
	AvailabilityStatus string `json:"availability_status"`
	AvatarURL          string `json:"avatar_url"`
	Timezone           string `json:"timezone"`
}

type memberView struct {
	ID           uint          `json:"id"`
	AccountID    uint          `json:"account_id"`
	UserID       uint          `json:"user_id"`
	Role         string        `json:"role"`
	Availability string        `json:"availability"`
	Identity     *identityView `json:"identity"`
}

type mediaAssetView struct {
	ID            uint      `json:"id"`
	AccountID     uint      `json:"account_id"`
	FileName      string    `json:"file_name"`
	ContentType   string    `json:"content_type"`
	FileSize      int64     `json:"file_size"`
	Checksum      string    `json:"checksum,omitempty"`
	Status        string    `json:"status"`
	SecurityState string    `json:"security_state"`
	ObjectVersion int       `json:"object_version"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type mediaUploadView struct {
	Asset       mediaAssetView `json:"asset"`
	UploadID    uint           `json:"upload_id"`
	UploadToken string         `json:"upload_token"`
	ExpiresAt   time.Time      `json:"expires_at"`
}

type conversationView struct {
	ID             uint       `json:"id"`
	UUID           string     `json:"uuid"`
	DisplayID      uint       `json:"display_id"`
	AccountID      uint       `json:"account_id"`
	InboxID        uint       `json:"inbox_id"`
	ContactID      uint       `json:"contact_id"`
	AssigneeID     *uint      `json:"assignee_id,omitempty"`
	TeamID         *uint      `json:"team_id,omitempty"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	ObjectVersion  int        `json:"object_version"`
	SnoozedUntil   *time.Time `json:"snoozed_until,omitempty"`
	LastActivityAt time.Time  `json:"last_activity_at"`
	UnreadCount    int        `json:"unread_count"`
	Muted          bool       `json:"muted"`
	LabelIDs       []uint     `json:"label_ids"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type accountView struct {
	ID       uint   `json:"id"`
	Name     string `json:"name"`
	Locale   string `json:"locale"`
	Status   string `json:"status"`
	Timezone string `json:"timezone"`
}

type membershipCheckView struct {
	AccountID uint `json:"account_id"`
	UserID    uint `json:"user_id"`
	Allowed   bool `json:"allowed"`
}

type capabilityCheckView struct {
	AccountID  uint   `json:"account_id"`
	Capability string `json:"capability"`
	Allowed    bool   `json:"allowed"`
}

type inboxView struct {
	ID                  uint   `json:"id"`
	AccountID           uint   `json:"account_id"`
	Name                string `json:"name"`
	ChannelType         string `json:"channel_type"`
	AvatarURL           string `json:"avatar_url"`
	GreetingMessage     string `json:"greeting_message"`
	GreetingEnabled     bool   `json:"greeting_enabled"`
	WorkingHoursEnabled bool   `json:"working_hours_enabled"`
	OutOfOfficeMessage  string `json:"out_of_office_message"`
	Timezone            string `json:"timezone"`
	WorkingHours        string `json:"working_hours"`
	CSATSurveyEnabled   bool   `json:"csat_survey_enabled"`
}

type channelCapabilitiesView struct {
	InboxID          uint     `json:"inbox_id"`
	ChannelType      string   `json:"channel_type"`
	Capabilities     []string `json:"capabilities"`
	AuthorizationSet bool     `json:"authorization_set"`
}

type connectionStatusView struct {
	InboxID     uint      `json:"inbox_id"`
	Status      string    `json:"status"`
	Configured  bool      `json:"configured"`
	WebhookSet  bool      `json:"webhook_set"`
	HMACEnabled bool      `json:"hmac_enabled"`
	CheckedAt   time.Time `json:"checked_at,omitempty"`
}

type templateView struct {
	ID         uint   `json:"id"`
	AccountID  uint   `json:"account_id"`
	InboxID    uint   `json:"inbox_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Category   string `json:"category"`
	Language   string `json:"language"`
	Components string `json:"components"`
}

type contactView struct {
	ID            uint   `json:"id"`
	AccountID     uint   `json:"account_id"`
	CompanyID     *uint  `json:"company_id,omitempty"`
	Name          string `json:"name"`
	Email         string `json:"email"`
	PhoneNumber   string `json:"phone_number"`
	Identifier    string `json:"identifier"`
	AvatarURL     string `json:"avatar_url"`
	Blocked       bool   `json:"blocked"`
	ObjectVersion int    `json:"object_version"`
}

type companyView struct {
	ID            uint   `json:"id"`
	AccountID     uint   `json:"account_id"`
	Name          string `json:"name"`
	Domain        string `json:"domain"`
	Industry      string `json:"industry"`
	Description   string `json:"description"`
	AvatarURL     string `json:"avatar_url"`
	ContactsCount int64  `json:"contacts_count"`
}

type labelView struct {
	ID            uint   `json:"id"`
	AccountID     uint   `json:"account_id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Color         string `json:"color"`
	ShowOnSidebar bool   `json:"show_on_sidebar"`
}

type contactDeletionStatusView struct {
	AccountID         uint  `json:"account_id"`
	ContactCount      int64 `json:"contact_count"`
	IdentityLinkCount int64 `json:"identity_link_count"`
	PIIContactCount   int64 `json:"pii_contact_count"`
	Ready             bool  `json:"ready"`
}

func (r *Runtime) RegisterBusinessContracts() error {
	if r.Contracts == nil || r.DB == nil {
		return errors.New("module runtime is incomplete")
	}
	switch r.Config.ModuleID {
	case "IAM":
		users := repository.NewUserRepository(r.DB)
		return r.Contracts.RegisterQuery("iam.identity.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			if ids, ok := uintSliceFilter(query.Filter, "ids"); ok {
				found, err := users.FindByIDs(ids)
				if err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "identity store is unavailable"}
				}
				views := make([]*identityView, 0, len(found))
				for i := range found {
					views = append(views, identityFromUser(&found[i]))
				}
				return views, nil
			}
			id, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "identity id is required"}
			}
			user, err := users.FindByID(id)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "identity store is unavailable"}
			}
			if user == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "identity was not found"}
			}
			return identityFromUser(user), nil
		})
	case "TEN":
		dependency, ok := r.Config.Dependencies["IAM"]
		if !ok {
			return errors.New("TEN business contracts require IAM dependency")
		}
		accounts := repository.NewAccountRepositoryWithUserPort(r.DB, nil)
		client := newModuleClient("TEN", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		if err := r.Contracts.RegisterQuery("ten.account.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			account, err := accounts.FindByID(query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "account store is unavailable"}
			}
			if account == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "account was not found"}
			}
			return accountFromModel(account), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			userID, err := uintFilter(query.Filter, "user_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "permission user_id is required"}
			}
			members, err := accounts.ListMemberRecords(query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "membership store is unavailable"}
			}
			allowed := false
			for _, member := range members {
				if member.UserID == userID {
					allowed = true
					break
				}
			}
			return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: allowed}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("ten.members.list.v1", func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
			members, err := accounts.ListMemberRecords(query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "membership store is unavailable"}
			}
			ids := make([]uint, 0, len(members))
			for _, member := range members {
				ids = append(ids, member.UserID)
			}
			identities, err := client.getIdentities(ctx, query, ids)
			if err != nil {
				return nil, err
			}
			result := make([]memberView, 0, len(members))
			for _, member := range members {
				identity := identities[member.UserID]
				if identity == nil {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "member identity was not found"}
				}
				result = append(result, memberView{ID: member.ID, AccountID: member.AccountID, UserID: member.UserID, Role: member.Role, Availability: member.Availability, Identity: identity})
			}
			return result, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("ten.membership.add.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "membership target must match account"}
			}
			userID, err := uintFilter(command.Input, "user_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "membership user_id is required"}
			}
			identity, err := client.getIdentity(ctx, foundation.QueryEnvelope{AccountID: command.AccountID, Actor: command.Actor, Consistency: "strong"}, userID)
			if err != nil {
				return nil, err
			}
			role, _ := command.Input["role"].(string)
			txAccounts := repository.NewAccountRepositoryWithUserPort(Transaction(ctx, r.DB), nil)
			member, err := txAccounts.CreateMemberRecord(command.AccountID, userID, role)
			if err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "membership could not be created"}
			}
			return memberView{ID: member.ID, AccountID: member.AccountID, UserID: member.UserID, Role: member.Role, Availability: member.Availability, Identity: identity}, nil
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterEvent("iam.identity.changed.v1", func(context.Context, foundation.EventEnvelope) error { return nil })
	case "ENT":
		enterprise := repository.NewEnterpriseRepository(r.DB)
		return r.Contracts.RegisterQuery("ent.capability.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			capability, _ := query.Filter["capability"].(string)
			capability = strings.TrimSpace(capability)
			if capability == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "capability is required"}
			}
			features, err := enterprise.GetAccountFeatures(query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "entitlement store is unavailable"}
			}
			return capabilityCheckView{AccountID: query.AccountID, Capability: capability, Allowed: features[capability]}, nil
		})
	case "CUS":
		contacts := repository.NewContactRepositoryWithOwnerPorts(r.DB, nil, nil, nil, nil, nil)
		companies := repository.NewCompanyRepository(r.DB)
		labels := repository.NewLabelRepositoryWithConversationPort(r.DB, nil)
		if err := r.Contracts.RegisterQuery("cus.contact.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			contactID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact id is required"}
			}
			contact, err := contacts.FindSummaryByID(query.AccountID, contactID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact store is unavailable"}
			}
			if contact == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "contact was not found"}
			}
			return contactFromModel(contact), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("cus.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			status, err := contactDeletionStatus(r.DB, query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact deletion status is unavailable"}
			}
			return status, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("cus.label.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			if labelID, err := uintFilter(query.Filter, "id"); err == nil {
				label, err := labels.FindByID(query.AccountID, labelID)
				if err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "label store is unavailable"}
				}
				if label == nil {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "label was not found"}
				}
				return labelFromModel(label), nil
			}
			found, err := labels.List(query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "label store is unavailable"}
			}
			views := make([]labelView, 0, len(found))
			for i := range found {
				views = append(views, *labelFromModel(&found[i]))
			}
			return views, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("cus.company.get.v1", func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
			companyID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "company id is required"}
			}
			company, err := companies.GetByID(ctx, query.AccountID, companyID)
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "company was not found"}
			}
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "company store is unavailable"}
			}
			return companyFromModel(company), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("cus.identity.resolve.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			email, _ := query.Filter["email"].(string)
			identifier, _ := query.Filter["identifier"].(string)
			phone, _ := query.Filter["phone_number"].(string)
			name, _ := query.Filter["name"].(string)
			email, identifier, phone, name = strings.TrimSpace(email), strings.TrimSpace(identifier), strings.TrimSpace(phone), strings.TrimSpace(name)
			if email == "" && identifier == "" && phone == "" && name == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact identity is required"}
			}
			contact, err := contacts.FindImportMatch(query.AccountID, email, identifier, phone, name)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact identity store is unavailable"}
			}
			if contact == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "contact identity was not found"}
			}
			return contactFromModel(contact), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("cus.contact.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			name, _ := command.Input["name"].(string)
			email, _ := command.Input["email"].(string)
			phone, _ := command.Input["phone_number"].(string)
			identifier, _ := command.Input["identifier"].(string)
			avatarURL, _ := command.Input["avatar_url"].(string)
			if strings.TrimSpace(name) == "" && strings.TrimSpace(email) == "" && strings.TrimSpace(phone) == "" && strings.TrimSpace(identifier) == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact identity is required"}
			}
			contact := &domain.Contact{AccountID: command.AccountID, Name: strings.TrimSpace(name), Email: strings.TrimSpace(email), PhoneNumber: strings.TrimSpace(phone), Identifier: strings.TrimSpace(identifier), AvatarURL: strings.TrimSpace(avatarURL), ObjectVersion: 1}
			txRepo := repository.NewContactRepositoryWithOwnerPorts(Transaction(ctx, r.DB), nil, nil, nil, nil, nil)
			if err := txRepo.Create(contact); err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contact could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "cus.contact.created.v1", "Contact", strconv.FormatUint(uint64(contact.ID), 10), []string{"created"}, map[string]any{"contact_id": contact.ID, "operation": "created"}); err != nil {
				return nil, err
			}
			return contactFromModel(contact), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("cus.contact.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "contact" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact target is required"}
			}
			parsedID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsedID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact target is invalid"}
			}
			contactID := uint(parsedID)
			tx := Transaction(ctx, r.DB)
			txRepo := repository.NewContactRepositoryWithOwnerPorts(tx, nil, nil, nil, nil, nil)
			contact, err := txRepo.FindSummaryByID(command.AccountID, contactID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact store is unavailable"}
			}
			if contact == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "contact was not found"}
			}
			allowed := map[string]string{"name": "name", "email": "email", "phone_number": "phone_number", "identifier": "identifier", "avatar_url": "avatar_url", "blocked": "blocked"}
			updates := map[string]any{}
			changed := make([]string, 0, len(command.Input))
			for key, value := range command.Input {
				column, ok := allowed[key]
				if !ok {
					return nil, &ContractFailure{Type: "validation_failed", Message: "contact field is not writable"}
				}
				updates[column] = value
				changed = append(changed, key)
			}
			if len(updates) == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact update is empty"}
			}
			updates["object_version"] = gorm.Expr("object_version + 1")
			if err := tx.Model(&domain.Contact{}).Where("account_id = ? AND id = ?", command.AccountID, contactID).Updates(updates).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contact could not be updated"}
			}
			contact, err = txRepo.FindSummaryByID(command.AccountID, contactID)
			if err != nil || contact == nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "updated contact is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "cus.contact.changed.v1", "Contact", command.Target.ID, changed, map[string]any{"contact_id": contact.ID, "operation": "updated", "object_version": contact.ObjectVersion}); err != nil {
				return nil, err
			}
			return contactFromModel(contact), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("cus.contact.delete.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "contact" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact target is required"}
			}
			parsedID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsedID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact target is invalid"}
			}
			contactID := uint(parsedID)
			tx := Transaction(ctx, r.DB)
			txContacts := repository.NewContactRepositoryWithOwnerPorts(tx, nil, nil, nil, nil, nil)
			contact, err := txContacts.FindSummaryByID(command.AccountID, contactID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact store is unavailable"}
			}
			if contact == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "contact was not found"}
			}
			if err := tx.Where("contact_id = ?", contactID).Delete(&domain.ContactInbox{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contact identities could not be removed"}
			}
			if err := tx.Where("contact_id = ?", contactID).Delete(&domain.ContactLabel{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contact labels could not be removed"}
			}
			if err := tx.Where("account_id = ? AND contact_id = ?", command.AccountID, contactID).Delete(&domain.ContactNote{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contact notes could not be removed"}
			}
			result := tx.Where("account_id = ? AND id = ?", command.AccountID, contactID).Delete(&domain.Contact{})
			if result.Error != nil || result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contact could not be deleted"}
			}
			if err := r.appendBusinessEvent(ctx, command, "cus.contact.deleted.v1", "Contact", command.Target.ID, []string{"deleted"}, map[string]any{"contact_id": contactID, "operation": "deleted"}); err != nil {
				return nil, err
			}
			return map[string]any{"contact_id": contactID, "deleted": true}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("cus.company.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			action, _ := command.Input["action"].(string)
			tx := Transaction(ctx, r.DB)
			txCompanies := repository.NewCompanyRepository(tx)
			allowed := map[string]string{"name": "name", "domain": "domain", "industry": "industry", "description": "description", "avatar_url": "avatar_url", "custom_attributes": "custom_attributes", "additional_attributes": "additional_attributes"}
			updates := map[string]any{}
			changed := make([]string, 0, len(command.Input))
			for key, value := range command.Input {
				if key == "action" {
					continue
				}
				column, ok := allowed[key]
				if !ok {
					return nil, &ContractFailure{Type: "validation_failed", Message: "company field is not writable"}
				}
				if (key == "custom_attributes" || key == "additional_attributes") && !validJSONObject(value) {
					return nil, &ContractFailure{Type: "validation_failed", Message: "company attributes must be a JSON object"}
				}
				if key == "custom_attributes" || key == "additional_attributes" {
					if encoded, ok := value.(map[string]any); ok {
						valueJSON, _ := json.Marshal(encoded)
						value = string(valueJSON)
					}
				}
				updates[column] = value
				changed = append(changed, key)
			}
			if action == "create" {
				if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
					return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
				}
				name, _ := updates["name"].(string)
				if strings.TrimSpace(name) == "" {
					return nil, &ContractFailure{Type: "validation_failed", Message: "company name is required"}
				}
				company := &domain.Company{AccountID: command.AccountID, Name: name}
				company.Domain, _ = updates["domain"].(string)
				company.Industry, _ = updates["industry"].(string)
				company.Description, _ = updates["description"].(string)
				company.AvatarURL, _ = updates["avatar_url"].(string)
				company.CustomAttributes, _ = updates["custom_attributes"].(string)
				company.AdditionalAttributes, _ = updates["additional_attributes"].(string)
				if err := tx.Create(company).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "company could not be created"}
				}
				company, err := txCompanies.GetByID(ctx, command.AccountID, company.ID)
				if err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "created company is unavailable"}
				}
				if err := r.appendBusinessEvent(ctx, command, "cus.company.changed.v1", "Company", strconv.FormatUint(uint64(company.ID), 10), []string{"created"}, map[string]any{"company_id": company.ID, "operation": "created"}); err != nil {
					return nil, err
				}
				return companyFromModel(company), nil
			}
			if action != "update" || command.Target.Type != "company" || len(updates) == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "company update is invalid"}
			}
			parsedID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsedID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "company target is invalid"}
			}
			companyID := uint(parsedID)
			if _, err := txCompanies.GetByID(ctx, command.AccountID, companyID); errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "company was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "company store is unavailable"}
			}
			if err := tx.Model(&domain.Company{}).Where("account_id = ? AND id = ?", command.AccountID, companyID).Updates(updates).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "company could not be updated"}
			}
			company, err := txCompanies.GetByID(ctx, command.AccountID, companyID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "updated company is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "cus.company.changed.v1", "Company", command.Target.ID, changed, map[string]any{"company_id": company.ID, "operation": "updated"}); err != nil {
				return nil, err
			}
			return companyFromModel(company), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("cus.label.manage.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			action, _ := command.Input["action"].(string)
			tx := Transaction(ctx, r.DB)
			txLabels := repository.NewLabelRepositoryWithConversationPort(tx, nil)
			if action == "create" {
				if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
					return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
				}
				title, _ := command.Input["title"].(string)
				if strings.TrimSpace(title) == "" {
					return nil, &ContractFailure{Type: "validation_failed", Message: "label title is required"}
				}
				label := &domain.Label{AccountID: command.AccountID, Title: strings.TrimSpace(title), Color: "#1f93ff"}
				label.Description, _ = command.Input["description"].(string)
				if color, ok := command.Input["color"].(string); ok && strings.TrimSpace(color) != "" {
					label.Color = strings.TrimSpace(color)
				}
				label.ShowOnSidebar, _ = command.Input["show_on_sidebar"].(bool)
				if err := txLabels.Create(label); err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "label could not be created"}
				}
				if err := r.appendBusinessEvent(ctx, command, "cus.label.changed.v1", "Label", strconv.FormatUint(uint64(label.ID), 10), []string{"created"}, map[string]any{"label_id": label.ID, "operation": "created"}); err != nil {
					return nil, err
				}
				return labelFromModel(label), nil
			}
			if action != "update" || command.Target.Type != "label" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "label action is invalid"}
			}
			parsedID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsedID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "label target is invalid"}
			}
			labelID := uint(parsedID)
			label, err := txLabels.FindByID(command.AccountID, labelID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "label store is unavailable"}
			}
			if label == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "label was not found"}
			}
			allowed := map[string]string{"title": "title", "description": "description", "color": "color", "show_on_sidebar": "show_on_sidebar"}
			updates := map[string]any{}
			changed := make([]string, 0, len(command.Input))
			for key, value := range command.Input {
				if key == "action" {
					continue
				}
				column, ok := allowed[key]
				if !ok {
					return nil, &ContractFailure{Type: "validation_failed", Message: "label field is not writable"}
				}
				updates[column] = value
				changed = append(changed, key)
			}
			if len(updates) == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "label update is empty"}
			}
			if err := tx.Model(&domain.Label{}).Where("account_id = ? AND id = ?", command.AccountID, labelID).Updates(updates).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "label could not be updated"}
			}
			label, _ = txLabels.FindByID(command.AccountID, labelID)
			if err := r.appendBusinessEvent(ctx, command, "cus.label.changed.v1", "Label", command.Target.ID, changed, map[string]any{"label_id": label.ID, "operation": "updated"}); err != nil {
				return nil, err
			}
			return labelFromModel(label), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("cus.account_contacts.anonymize.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			tx := Transaction(ctx, r.DB)
			var contactIDs []uint
			if err := tx.Model(&domain.Contact{}).Where("account_id = ?", command.AccountID).Pluck("id", &contactIDs).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact store is unavailable"}
			}
			if len(contactIDs) > 0 {
				if err := tx.Where("contact_id IN ?", contactIDs).Delete(&domain.ContactInbox{}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "contact identities could not be removed"}
				}
				if err := tx.Where("contact_id IN ?", contactIDs).Delete(&domain.ContactLabel{}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "contact labels could not be removed"}
				}
				if err := tx.Where("account_id = ? AND contact_id IN ?", command.AccountID, contactIDs).Delete(&domain.ContactNote{}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "contact notes could not be removed"}
				}
			}
			updates := map[string]any{"company_id": nil, "name": "Deleted Contact", "email": "", "phone_number": "", "identifier": "", "avatar_url": "", "pubsub_token": "", "custom_attributes": "", "blocked": true, "object_version": gorm.Expr("object_version + 1")}
			if err := tx.Model(&domain.Contact{}).Where("account_id = ?", command.AccountID).Updates(updates).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contacts could not be anonymized"}
			}
			status, err := contactDeletionStatus(tx, command.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "contact deletion status is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "cus.contact.changed.v1", "AccountContacts", command.Target.ID, []string{"anonymized"}, map[string]any{"account_id": command.AccountID, "operation": "anonymized", "contact_count": status.ContactCount}); err != nil {
				return nil, err
			}
			return status, nil
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterCommand("cus.contact.merge.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "contact" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "base contact target is required"}
			}
			baseID64, err := strconv.ParseUint(command.Target.ID, 10, 64)
			mergeeID, mergeeErr := uintFilter(command.Input, "mergee_id")
			if err != nil || baseID64 == 0 || mergeeErr != nil || uint(baseID64) == mergeeID {
				return nil, &ContractFailure{Type: "validation_failed", Message: "distinct base and mergee contacts are required"}
			}
			baseID := uint(baseID64)
			txRepo := repository.NewContactRepositoryWithOwnerPorts(Transaction(ctx, r.DB), nil, nil, nil, nil, nil)
			if err := txRepo.MergeContacts(command.AccountID, baseID, mergeeID); errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "merge contact was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "contacts could not be merged"}
			}
			base, err := txRepo.FindSummaryByID(command.AccountID, baseID)
			if err != nil || base == nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "merged contact is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "cus.contact.merged.v1", "Contact", command.Target.ID, []string{"merged"}, map[string]any{"base_contact_id": baseID, "merged_contact_id": mergeeID}); err != nil {
				return nil, err
			}
			return contactFromModel(base), nil
		})
	case "MED":
		if err := r.Contracts.RegisterQuery("med.asset.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			assetID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "asset id is required"}
			}
			var asset domain.MediaAsset
			if err := r.DB.Where("account_id = ? AND id = ?", query.AccountID, assetID).First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "asset was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			return mediaAssetFromModel(&asset), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("med.access.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			assetID, err := uintFilter(query.Filter, "id")
			subjectType, _ := query.Filter["subject_type"].(string)
			subjectID, _ := query.Filter["subject_id"].(string)
			scope, _ := query.Filter["scope"].(string)
			subjectType, subjectID, scope = strings.TrimSpace(subjectType), strings.TrimSpace(subjectID), strings.TrimSpace(scope)
			if err != nil || subjectType == "" || subjectID == "" || scope == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "asset access subject and scope are required"}
			}
			var asset domain.MediaAsset
			if err := r.DB.Where("account_id = ? AND id = ?", query.AccountID, assetID).First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "asset was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			now := time.Now().UTC()
			var grants int64
			if err := r.DB.Model(&domain.AccessGrant{}).Where("account_id = ? AND asset_id = ? AND subject_type = ? AND subject_id = ? AND scope = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", query.AccountID, assetID, subjectType, subjectID, scope, now).Count(&grants).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media access grants are unavailable"}
			}
			allowed := asset.Status == "ready" && asset.SecurityState == "clean" && grants > 0
			return map[string]any{"asset_id": assetID, "allowed": allowed, "status": asset.Status, "security_state": asset.SecurityState}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("med.security_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			assetID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "asset id is required"}
			}
			var asset domain.MediaAsset
			if err := r.DB.Where("account_id = ? AND id = ?", query.AccountID, assetID).First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "asset was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			var scan domain.SecurityScan
			scanResult := ""
			var scannedAt *time.Time
			if err := r.DB.Where("account_id = ? AND asset_id = ?", query.AccountID, assetID).Order("scanned_at DESC, id DESC").First(&scan).Error; err == nil {
				scanResult, scannedAt = scan.Status, &scan.ScannedAt
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "security scans are unavailable"}
			}
			return map[string]any{"asset_id": assetID, "status": asset.Status, "security_state": asset.SecurityState, "latest_scan_result": scanResult, "scanned_at": scannedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("med.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			now := time.Now().UTC()
			counts := map[string]int64{}
			queries := map[string]*gorm.DB{
				"asset_count":           r.DB.Model(&domain.MediaAsset{}).Where("account_id = ? AND status <> ?", query.AccountID, "deleted"),
				"pending_upload_count":  r.DB.Model(&domain.UploadSession{}).Where("account_id = ? AND status = ? AND expires_at > ?", query.AccountID, "pending", now),
				"active_grant_count":    r.DB.Model(&domain.AccessGrant{}).Where("account_id = ? AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)", query.AccountID, now),
				"security_record_count": r.DB.Model(&domain.SecurityScan{}).Where("account_id = ?", query.AccountID),
			}
			for name, statement := range queries {
				var count int64
				if err := statement.Count(&count).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media deletion status is unavailable"}
				}
				counts[name] = count
			}
			counts["account_id"] = int64(query.AccountID)
			return counts, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("med.upload.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			fileName, _ := command.Input["file_name"].(string)
			contentType, _ := command.Input["content_type"].(string)
			checksum, _ := command.Input["checksum"].(string)
			fileSize, err := int64Filter(command.Input, "file_size")
			fileName, contentType, checksum = strings.TrimSpace(fileName), strings.TrimSpace(contentType), strings.TrimSpace(checksum)
			if fileName == "" || contentType == "" || err != nil || fileSize <= 0 || fileSize > 30<<20 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "valid file metadata is required"}
			}
			now := time.Now().UTC()
			token := foundation.GenerateUUID()
			tokenDigest := sha256.Sum256([]byte(token))
			asset := domain.MediaAsset{AccountID: command.AccountID, FileName: fileName, ContentType: contentType, FileSize: fileSize, Checksum: checksum, StorageKey: strconv.FormatUint(uint64(command.AccountID), 10) + "/" + foundation.GenerateUUID(), Status: "pending_upload", SecurityState: "pending", ObjectVersion: 1, CreatedAt: now, UpdatedAt: now}
			tx := Transaction(ctx, r.DB)
			if err := tx.Create(&asset).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "media asset could not be created"}
			}
			upload := domain.UploadSession{AccountID: command.AccountID, AssetID: asset.ID, TokenHash: hex.EncodeToString(tokenDigest[:]), Status: "pending", ExpiresAt: now.Add(15 * time.Minute), CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&upload).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "upload session could not be created"}
			}
			return mediaUploadView{Asset: *mediaAssetFromModel(&asset), UploadID: upload.ID, UploadToken: token, ExpiresAt: upload.ExpiresAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("med.asset.confirm.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			assetID, failure := mediaCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			uploadID, err := uintFilter(command.Input, "upload_id")
			token, _ := command.Input["upload_token"].(string)
			if err != nil || strings.TrimSpace(token) == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "upload confirmation is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			var asset domain.MediaAsset
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, assetID).First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "asset was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			var upload domain.UploadSession
			if err := tx.Where("account_id = ? AND asset_id = ? AND id = ?", command.AccountID, assetID, uploadID).First(&upload).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "upload session was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "upload session is unavailable"}
			}
			digest := sha256.Sum256([]byte(strings.TrimSpace(token)))
			if upload.Status != "pending" || time.Now().UTC().After(upload.ExpiresAt) || subtle.ConstantTimeCompare([]byte(upload.TokenHash), []byte(hex.EncodeToString(digest[:]))) != 1 {
				return nil, &ContractFailure{Type: "state_conflict", Message: "upload session cannot be confirmed"}
			}
			if checksum, ok := command.Input["checksum"].(string); ok && asset.Checksum != "" && strings.TrimSpace(checksum) != asset.Checksum {
				return nil, &ContractFailure{Type: "state_conflict", Message: "upload checksum does not match"}
			}
			now := time.Now().UTC()
			if err := tx.Model(&domain.UploadSession{}).Where("id = ?", upload.ID).Updates(map[string]any{"status": "confirmed", "updated_at": now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "upload session could not be confirmed"}
			}
			if err := tx.Model(&domain.MediaAsset{}).Where("account_id = ? AND id = ? AND status = ?", command.AccountID, assetID, "pending_upload").Updates(map[string]any{"status": "pending_scan", "object_version": gorm.Expr("object_version + 1"), "updated_at": now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset could not be confirmed"}
			}
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, assetID).First(&asset).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "confirmed asset is unavailable"}
			}
			return mediaAssetFromModel(&asset), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("med.asset.scan.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			assetID, failure := mediaCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			result, _ := command.Input["result"].(string)
			threat, _ := command.Input["threat"].(string)
			result, threat = strings.ToLower(strings.TrimSpace(result)), strings.TrimSpace(threat)
			if result != "clean" && result != "rejected" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "scan result must be clean or rejected"}
			}
			tx := Transaction(ctx, r.DB)
			var asset domain.MediaAsset
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, assetID).First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "asset was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			if asset.Status != "pending_scan" || asset.SecurityState != "pending" {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset is not awaiting a security scan"}
			}
			now := time.Now().UTC()
			scan := domain.SecurityScan{AccountID: command.AccountID, AssetID: assetID, Status: result, Threat: threat, ScannedAt: now, CreatedAt: now}
			if err := tx.Create(&scan).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "security scan could not be recorded"}
			}
			status, eventType := "ready", "med.media.ready.v1"
			if result == "rejected" {
				status, eventType = "rejected", "med.media.rejected.v1"
			}
			if err := tx.Model(&domain.MediaAsset{}).Where("account_id = ? AND id = ?", command.AccountID, assetID).Updates(map[string]any{"status": status, "security_state": result, "object_version": gorm.Expr("object_version + 1"), "updated_at": now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset security state could not be updated"}
			}
			if err := r.appendBusinessEvent(ctx, command, eventType, "MediaAsset", command.Target.ID, []string{"status", "security_state"}, map[string]any{"asset_id": assetID, "status": status, "content_type": asset.ContentType, "file_size": asset.FileSize}); err != nil {
				return nil, err
			}
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, assetID).First(&asset).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "scanned asset is unavailable"}
			}
			return mediaAssetFromModel(&asset), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("med.asset.delete.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			assetID, failure := mediaCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			tx := Transaction(ctx, r.DB)
			var asset domain.MediaAsset
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, assetID).First(&asset).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "asset was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			if asset.Status == "deleted" {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset is already deleted"}
			}
			now := time.Now().UTC()
			if err := tx.Model(&domain.UploadSession{}).Where("account_id = ? AND asset_id = ? AND status = ?", command.AccountID, assetID, "pending").Updates(map[string]any{"status": "cancelled", "updated_at": now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset upload sessions could not be cancelled"}
			}
			if err := tx.Model(&domain.AccessGrant{}).Where("account_id = ? AND asset_id = ? AND revoked_at IS NULL", command.AccountID, assetID).Update("revoked_at", now).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset grants could not be revoked"}
			}
			if err := tx.Model(&domain.MediaAsset{}).Where("account_id = ? AND id = ? AND status <> ?", command.AccountID, assetID, "deleted").Updates(map[string]any{"status": "deleted", "object_version": gorm.Expr("object_version + 1"), "updated_at": now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "asset could not be deleted"}
			}
			if err := r.appendBusinessEvent(ctx, command, "med.media.deleted.v1", "MediaAsset", command.Target.ID, []string{"status"}, map[string]any{"asset_id": assetID, "status": "deleted"}); err != nil {
				return nil, err
			}
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, assetID).First(&asset).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "deleted asset is unavailable"}
			}
			return mediaAssetFromModel(&asset), nil
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterCommand("med.account_assets.purge.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			tx := Transaction(ctx, r.DB)
			var assetIDs []uint
			if err := tx.Model(&domain.MediaAsset{}).Where("account_id = ?", command.AccountID).Pluck("id", &assetIDs).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "media store is unavailable"}
			}
			for _, model := range []any{&domain.AccessGrant{}, &domain.UploadSession{}, &domain.SecurityScan{}, &domain.MediaAsset{}} {
				if err := tx.Where("account_id = ?", command.AccountID).Delete(model).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "account media could not be purged"}
				}
			}
			if err := r.appendBusinessEvent(ctx, command, "med.media.deleted.v1", "AccountAssets", command.Target.ID, []string{"purged"}, map[string]any{"account_id": command.AccountID, "deleted_count": len(assetIDs), "status": "purged"}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "deleted_count": len(assetIDs), "ready": true}, nil
		})
	case "CON":
		if err := r.Contracts.RegisterQuery("con.conversation.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			conversationID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation id is required"}
			}
			var conversation domain.Conversation
			if err := r.DB.Where("account_id = ? AND id = ?", query.AccountID, conversationID).First(&conversation).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			view, err := conversationFromModel(r.DB, &conversation)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation labels are unavailable"}
			}
			return view, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("con.conversations.list.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			statement := r.DB.Where("account_id = ?", query.AccountID)
			for key, column := range map[string]string{"inbox_id": "inbox_id", "contact_id": "contact_id", "assignee_id": "assignee_id", "team_id": "team_id"} {
				if _, exists := query.Filter[key]; exists {
					value, err := uintFilter(query.Filter, key)
					if err != nil {
						return nil, &ContractFailure{Type: "validation_failed", Message: "conversation list filter is invalid"}
					}
					statement = statement.Where(column+" = ?", value)
				}
			}
			if status, ok := query.Filter["status"].(string); ok && strings.TrimSpace(status) != "" {
				statement = statement.Where("status = ?", strings.TrimSpace(status))
			}
			var total int64
			if err := statement.Model(&domain.Conversation{}).Count(&total).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			page := 1
			if parsed, err := strconv.Atoi(query.Page); err == nil && parsed > 0 {
				page = parsed
			}
			var conversations []domain.Conversation
			if err := statement.Order("id DESC").Offset((page - 1) * 50).Limit(50).Find(&conversations).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			items := make([]conversationView, 0, len(conversations))
			for index := range conversations {
				view, err := conversationFromModel(r.DB, &conversations[index])
				if err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation labels are unavailable"}
				}
				items = append(items, *view)
			}
			return map[string]any{"items": items, "total": total, "page": page, "page_size": 50}, nil
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterEvent("cus.contact.merged.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
			baseID, err := uintFilter(event.Payload, "base_contact_id")
			if err != nil {
				return &ContractFailure{Type: "validation_failed", Message: "base_contact_id is required"}
			}
			mergedID, err := uintFilter(event.Payload, "merged_contact_id")
			if err != nil || baseID == mergedID {
				return &ContractFailure{Type: "validation_failed", Message: "merged_contact_id is invalid"}
			}
			if err := repository.NewConversationRepositoryWithReadPorts(Transaction(ctx, r.DB), nil, nil, nil, nil, nil, nil, nil).ReassignContact(event.AccountID, mergedID, baseID); err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "conversation contact projection could not be reassigned"}
			}
			return nil
		})
	case "MSG":
		return r.Contracts.RegisterEvent("cus.contact.merged.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
			baseID, err := uintFilter(event.Payload, "base_contact_id")
			if err != nil {
				return &ContractFailure{Type: "validation_failed", Message: "base_contact_id is required"}
			}
			mergedID, err := uintFilter(event.Payload, "merged_contact_id")
			if err != nil || baseID == mergedID {
				return &ContractFailure{Type: "validation_failed", Message: "merged_contact_id is invalid"}
			}
			messages := repository.NewMessageRepositoryWithPorts(Transaction(ctx, r.DB), nil, nil, nil)
			if err := messages.ReassignContactSender(event.AccountID, mergedID, baseID); err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "message sender projection could not be reassigned"}
			}
			return nil
		})
	case "TKT":
		return r.Contracts.RegisterEvent("cus.contact.merged.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
			baseID, mergedID, failure := contactMergeEventIDs(event)
			if failure != nil {
				return failure
			}
			tickets := repository.NewTicketRepositoryWithOwnerPorts(Transaction(ctx, r.DB), nil, nil, nil, nil)
			if err := tickets.ReassignContact(event.AccountID, mergedID, baseID); err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "ticket contact projection could not be reassigned"}
			}
			return nil
		})
	case "BIL":
		return r.Contracts.RegisterEvent("cus.contact.merged.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
			baseID, mergedID, failure := contactMergeEventIDs(event)
			if failure != nil {
				return failure
			}
			orders := repository.NewOrderRepositoryWithConversationPort(Transaction(ctx, r.DB), nil)
			if err := orders.ReassignContact(event.AccountID, mergedID, baseID); err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "order contact projection could not be reassigned"}
			}
			return nil
		})
	case "OPS":
		return r.Contracts.RegisterEvent("cus.contact.merged.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
			baseID, mergedID, failure := contactMergeEventIDs(event)
			if failure != nil {
				return failure
			}
			campaigns := repository.NewCampaignRepository(Transaction(ctx, r.DB))
			if err := campaigns.ReassignContact(ctx, event.AccountID, mergedID, baseID); err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "campaign contact projection could not be reassigned"}
			}
			return nil
		})
	case "CHN":
		inboxes := repository.NewInboxRepositoryWithAccountPort(r.DB, nil)
		if err := r.Contracts.RegisterQuery("chn.inbox.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox id is required"}
			}
			inbox, err := inboxes.FindSummaryByID(query.AccountID, id)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			return inboxFromModel(inbox), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("chn.capabilities.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox id is required"}
			}
			inbox, err := inboxes.FindSummaryByID(query.AccountID, id)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			capabilities := []string{"messages", "attachments", "webhooks"}
			if inbox.ChannelType == "Channel::WebWidget" {
				capabilities = append(capabilities, "widget", "hmac")
			}
			if inbox.ChannelType == "Channel::Whatsapp" {
				capabilities = append(capabilities, "templates")
			}
			return channelCapabilitiesView{InboxID: inbox.ID, ChannelType: inbox.ChannelType, Capabilities: capabilities, AuthorizationSet: strings.TrimSpace(inbox.ProviderConfig) != ""}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("chn.connection.status.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox id is required"}
			}
			inbox, err := inboxes.FindSummaryByID(query.AccountID, id)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			view := connectionStatusView{InboxID: inbox.ID, Status: "unknown", Configured: strings.TrimSpace(inbox.ProviderConfig) != "", WebhookSet: strings.TrimSpace(inbox.WebhookURL) != "", HMACEnabled: inbox.HMACMandatory}
			var provider map[string]any
			if json.Unmarshal([]byte(inbox.ProviderConfig), &provider) == nil {
				if health, ok := provider["_health"].(map[string]any); ok {
					if status, ok := health["status"].(string); ok {
						view.Status = status
					}
					if checked, ok := health["checked_at"].(string); ok {
						view.CheckedAt, _ = time.Parse(time.RFC3339Nano, checked)
					}
				}
			}
			return view, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("chn.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			status, err := inboxes.AccountDeletionStatus(query.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "channel deletion status is unavailable"}
			}
			return status, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("chn.templates.list.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			inboxID, err := uintFilter(query.Filter, "inbox_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox_id is required"}
			}
			inbox, err := inboxes.FindSummaryByID(query.AccountID, inboxID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			templates, err := inboxes.ListStoredMessageTemplates(query.AccountID, inboxID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "template store is unavailable"}
			}
			views := make([]templateView, 0, len(templates))
			for i := range templates {
				views = append(views, templateFromModel(&templates[i]))
			}
			return views, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("chn.template.manage.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "inbox" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is required"}
			}
			parsedInboxID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsedInboxID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is invalid"}
			}
			inboxID := uint(parsedInboxID)
			txRepo := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil)
			inbox, err := txRepo.FindSummaryByID(command.AccountID, inboxID)
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			action, _ := command.Input["action"].(string)
			if action == "delete" {
				templateID, err := uintFilter(command.Input, "template_id")
				if err != nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "template_id is required"}
				}
				if _, err := txRepo.GetMessageTemplate(command.AccountID, inboxID, templateID); err != nil {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "template was not found"}
				}
				if err := txRepo.DeleteMessageTemplate(command.AccountID, inboxID, templateID); err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "template could not be deleted"}
				}
				if err := r.appendBusinessEvent(ctx, command, "chn.template.changed.v1", "InboxMessageTemplate", strconv.FormatUint(uint64(templateID), 10), []string{"deleted"}, map[string]any{"inbox_id": inbox.ID, "template_id": templateID, "operation": "deleted"}); err != nil {
					return nil, err
				}
				return map[string]any{"inbox_id": inbox.ID, "template_id": templateID, "deleted": true}, nil
			}
			if action != "upsert" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "template action must be upsert or delete"}
			}
			name, _ := command.Input["name"].(string)
			components, _ := command.Input["components"].(string)
			if strings.TrimSpace(name) == "" || !json.Valid([]byte(components)) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "template name and JSON components are required"}
			}
			template := &domain.InboxMessageTemplate{AccountID: command.AccountID, InboxID: inbox.ID, Name: strings.TrimSpace(name), Components: components}
			template.Status, _ = command.Input["status"].(string)
			template.Category, _ = command.Input["category"].(string)
			template.Language, _ = command.Input["language"].(string)
			if err := txRepo.CreateMessageTemplate(template); err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "template could not be saved"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.template.changed.v1", "InboxMessageTemplate", strconv.FormatUint(uint64(template.ID), 10), []string{"template"}, map[string]any{"inbox_id": inbox.ID, "template_id": template.ID, "operation": "upserted", "name": template.Name}); err != nil {
				return nil, err
			}
			return templateFromModel(template), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("chn.inbox.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "inbox" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is required"}
			}
			inboxID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || inboxID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is invalid"}
			}
			for key := range command.Input {
				switch key {
				case "name", "avatar_url", "greeting_message", "greeting_enabled", "working_hours_enabled", "out_of_office_message", "timezone", "working_hours", "csat_survey_enabled":
				default:
					return nil, &ContractFailure{Type: "validation_failed", Message: "inbox update contains an unsupported field"}
				}
			}
			txRepo := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil)
			inbox, err := txRepo.FindSummaryByID(command.AccountID, uint(inboxID))
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			if value, ok := command.Input["name"].(string); ok && strings.TrimSpace(value) != "" {
				inbox.Name = strings.TrimSpace(value)
			}
			if value, ok := command.Input["avatar_url"].(string); ok {
				inbox.AvatarURL = value
			}
			if value, ok := command.Input["greeting_message"].(string); ok {
				inbox.GreetingMessage = value
			}
			if value, ok := command.Input["greeting_enabled"].(bool); ok {
				inbox.GreetingEnabled = value
			}
			if value, ok := command.Input["working_hours_enabled"].(bool); ok {
				inbox.WorkingHoursEnabled = value
			}
			if value, ok := command.Input["out_of_office_message"].(string); ok {
				inbox.OutOfOfficeMessage = value
			}
			if value, ok := command.Input["timezone"].(string); ok && strings.TrimSpace(value) != "" {
				inbox.Timezone = strings.TrimSpace(value)
			}
			if value, ok := command.Input["working_hours"].(string); ok {
				inbox.WorkingHours = value
			}
			if value, ok := command.Input["csat_survey_enabled"].(bool); ok {
				inbox.CSATSurveyEnabled = value
			}
			if err := txRepo.Update(inbox); err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "inbox could not be updated"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.inbox.changed.v1", "Inbox", strconv.FormatUint(uint64(inbox.ID), 10), []string{"configuration"}, map[string]any{"inbox_id": inbox.ID, "operation": "updated"}); err != nil {
				return nil, err
			}
			return inboxFromModel(inbox), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("chn.health.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "inbox" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is required"}
			}
			inboxID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || inboxID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is invalid"}
			}
			status, _ := command.Input["status"].(string)
			if status != "healthy" && status != "degraded" && status != "unhealthy" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "channel health status is invalid"}
			}
			checkedValue, _ := command.Input["checked_at"].(string)
			checkedAt, err := time.Parse(time.RFC3339Nano, checkedValue)
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "channel health checked_at is invalid"}
			}
			details, _ := command.Input["details"].(map[string]any)
			if details == nil {
				details = map[string]any{}
			}
			txRepo := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil)
			if inbox, err := txRepo.FindSummaryByID(command.AccountID, uint(inboxID)); err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			} else if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			if err := txRepo.UpdateChannelHealth(command.AccountID, uint(inboxID), status, checkedAt, details); err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "channel health could not be updated"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.channel_health.changed.v1", "Inbox", command.Target.ID, []string{"health"}, map[string]any{"inbox_id": uint(inboxID), "status": status, "checked_at": checkedAt.UTC()}); err != nil {
				return nil, err
			}
			return map[string]any{"inbox_id": uint(inboxID), "status": status, "checked_at": checkedAt.UTC()}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("chn.account_channels.revoke.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "channel revoke target must match account"}
			}
			count, err := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil).RevokeAccountChannels(command.AccountID)
			if err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "account channels could not be revoked"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.inbox.changed.v1", "AccountChannels", command.Target.ID, []string{"authorization", "members"}, map[string]any{"account_id": command.AccountID, "operation": "revoked", "inbox_count": count}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "revoked_inboxes": count}, nil
		}); err != nil {
			return err
		}
		dependency, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return nil
		}
		client := newModuleClient("CHN", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		if err := r.Contracts.RegisterCommand("chn.inbox.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target must match account"}
			}
			if _, err := client.getAccount(ctx, command.AccountID, command.Actor); err != nil {
				return nil, err
			}
			name, _ := command.Input["name"].(string)
			if strings.TrimSpace(name) == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox name is required"}
			}
			channelType, _ := command.Input["channel_type"].(string)
			if channelType == "" {
				channelType = "Channel::WebWidget"
			}
			inbox := &domain.Inbox{AccountID: command.AccountID, Name: strings.TrimSpace(name), ChannelType: channelType, WebsiteToken: foundation.GenerateUUID(), Timezone: "UTC", GreetingEnabled: true}
			if err := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil).Create(inbox); err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "inbox could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.inbox.changed.v1", "Inbox", strconv.FormatUint(uint64(inbox.ID), 10), []string{"created"}, map[string]any{"inbox_id": inbox.ID, "operation": "created", "channel_type": inbox.ChannelType}); err != nil {
				return nil, err
			}
			return inboxFromModel(inbox), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("chn.member.bind.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "inbox" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is required"}
			}
			inboxID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || inboxID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is invalid"}
			}
			userID, err := uintFilter(command.Input, "user_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "member user_id is required"}
			}
			operation, _ := command.Input["operation"].(string)
			if operation != "bind" && operation != "unbind" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "member operation must be bind or unbind"}
			}
			if err := client.requireMember(ctx, command.AccountID, command.Actor, userID); err != nil {
				return nil, err
			}
			txRepo := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil)
			inbox, err := txRepo.FindSummaryByID(command.AccountID, uint(inboxID))
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			if operation == "bind" {
				err = txRepo.AddMember(inbox.ID, userID)
			} else {
				err = txRepo.RemoveMember(inbox.ID, userID)
			}
			if err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "inbox membership could not be changed"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.inbox.changed.v1", "Inbox", command.Target.ID, []string{"members"}, map[string]any{"inbox_id": inbox.ID, "user_id": userID, "operation": operation}); err != nil {
				return nil, err
			}
			return map[string]any{"inbox_id": inbox.ID, "user_id": userID, "operation": operation}, nil
		}); err != nil {
			return err
		}
		entDependency, ok := r.Config.Dependencies["ENT"]
		if !ok {
			return nil
		}
		entClient := newModuleClient("CHN", entDependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		return r.Contracts.RegisterCommand("chn.channel.authorize.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "inbox" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is required"}
			}
			inboxID, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || inboxID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox target is invalid"}
			}
			if err := entClient.requireCapability(ctx, command.AccountID, command.Actor, "channel_authorization"); err != nil {
				return nil, err
			}
			provider, ok := command.Input["provider_config"].(map[string]any)
			if !ok || len(provider) == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "provider_config is required"}
			}
			encoded, err := json.Marshal(provider)
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "provider_config is invalid"}
			}
			txRepo := repository.NewInboxRepositoryWithAccountPort(Transaction(ctx, r.DB), nil)
			inbox, err := txRepo.FindSummaryByID(command.AccountID, uint(inboxID))
			if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox store is unavailable"}
			}
			if inbox == nil {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
			}
			inbox.ProviderConfig = string(encoded)
			if value, ok := command.Input["webhook_url"].(string); ok {
				inbox.WebhookURL = strings.TrimSpace(value)
			}
			if value, ok := command.Input["hmac_mandatory"].(bool); ok {
				inbox.HMACMandatory = value
			}
			if err := txRepo.Update(inbox); err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "channel authorization could not be saved"}
			}
			if err := r.appendBusinessEvent(ctx, command, "chn.inbox.changed.v1", "Inbox", command.Target.ID, []string{"authorization"}, map[string]any{"inbox_id": inbox.ID, "authorized": true}); err != nil {
				return nil, err
			}
			return map[string]any{"inbox_id": inbox.ID, "authorized": true}, nil
		})
	default:
		return fmt.Errorf("business contracts are not implemented for %s", r.Config.ModuleID)
	}
}

func contactFromModel(contact *domain.Contact) *contactView {
	if contact == nil {
		return nil
	}
	return &contactView{ID: contact.ID, AccountID: contact.AccountID, CompanyID: contact.CompanyID, Name: contact.Name, Email: contact.Email, PhoneNumber: contact.PhoneNumber, Identifier: contact.Identifier, AvatarURL: contact.AvatarURL, Blocked: contact.Blocked, ObjectVersion: contact.ObjectVersion}
}

func companyFromModel(company *domain.Company) *companyView {
	if company == nil {
		return nil
	}
	return &companyView{ID: company.ID, AccountID: company.AccountID, Name: company.Name, Domain: company.Domain, Industry: company.Industry, Description: company.Description, AvatarURL: company.AvatarURL, ContactsCount: company.ContactsCount}
}

func labelFromModel(label *domain.Label) *labelView {
	if label == nil {
		return nil
	}
	return &labelView{ID: label.ID, AccountID: label.AccountID, Title: label.Title, Description: label.Description, Color: label.Color, ShowOnSidebar: label.ShowOnSidebar}
}

func validJSONObject(value any) bool {
	if _, ok := value.(map[string]any); ok {
		return true
	}
	raw, ok := value.(string)
	if !ok {
		return false
	}
	var object map[string]any
	return json.Unmarshal([]byte(raw), &object) == nil && object != nil
}

func contactDeletionStatus(db *gorm.DB, accountID uint) (contactDeletionStatusView, error) {
	status := contactDeletionStatusView{AccountID: accountID}
	if err := db.Model(&domain.Contact{}).Where("account_id = ?", accountID).Count(&status.ContactCount).Error; err != nil {
		return status, err
	}
	if err := db.Model(&domain.Contact{}).Where("account_id = ? AND (email <> '' OR phone_number <> '' OR identifier <> '' OR avatar_url <> '' OR pubsub_token <> '' OR custom_attributes <> '' OR name <> ? OR blocked = ?)", accountID, "Deleted Contact", false).Count(&status.PIIContactCount).Error; err != nil {
		return status, err
	}
	if err := db.Model(&domain.ContactInbox{}).Joins("JOIN contacts ON contacts.id = contact_inboxes.contact_id").Where("contacts.account_id = ?", accountID).Count(&status.IdentityLinkCount).Error; err != nil {
		return status, err
	}
	status.Ready = status.PIIContactCount == 0 && status.IdentityLinkCount == 0
	return status, nil
}

func contactMergeEventIDs(event foundation.EventEnvelope) (uint, uint, *ContractFailure) {
	baseID, err := uintFilter(event.Payload, "base_contact_id")
	if err != nil {
		return 0, 0, &ContractFailure{Type: "validation_failed", Message: "base_contact_id is required"}
	}
	mergedID, err := uintFilter(event.Payload, "merged_contact_id")
	if err != nil || baseID == mergedID {
		return 0, 0, &ContractFailure{Type: "validation_failed", Message: "merged_contact_id is invalid"}
	}
	return baseID, mergedID, nil
}

func accountFromModel(account *domain.Account) *accountView {
	return &accountView{ID: account.ID, Name: account.Name, Locale: account.Locale, Status: account.Status, Timezone: account.Timezone}
}

func (r *Runtime) appendBusinessEvent(ctx context.Context, command foundation.CommandEnvelope, eventType, aggregateType, aggregateID string, changed []string, payload map[string]any) error {
	tx := Transaction(ctx, r.DB)
	var latest int64
	if err := tx.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ? AND object_type = ? AND object_display_id = ?", r.Config.ModuleID, "event_outbox", aggregateType, aggregateID).Select("COALESCE(MAX(object_version_after),0)").Scan(&latest).Error; err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "event outbox is unavailable"}
	}
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: foundation.GenerateUUID(), EventType: eventType, EventVersion: r.Config.ContractVersion, SourceModule: r.Config.ModuleID, AccountID: command.AccountID, AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: uint64(latest + 1), OccurredAt: now, PublishedAt: now, Actor: command.Actor, CorrelationID: command.CorrelationID, CausationID: command.CommandID, ChangedFields: changed, Payload: payload, DataClassification: "internal"}
	if err := NewEventOutbox(r.DB, r.Config.ModuleID).Append(tx, event); err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "event outbox write failed"}
	}
	return nil
}

func inboxFromModel(inbox *domain.Inbox) *inboxView {
	return &inboxView{ID: inbox.ID, AccountID: inbox.AccountID, Name: inbox.Name, ChannelType: inbox.ChannelType, AvatarURL: inbox.AvatarURL, GreetingMessage: inbox.GreetingMessage, GreetingEnabled: inbox.GreetingEnabled, WorkingHoursEnabled: inbox.WorkingHoursEnabled, OutOfOfficeMessage: inbox.OutOfOfficeMessage, Timezone: inbox.Timezone, WorkingHours: inbox.WorkingHours, CSATSurveyEnabled: inbox.CSATSurveyEnabled}
}

func templateFromModel(template *domain.InboxMessageTemplate) templateView {
	return templateView{ID: template.ID, AccountID: template.AccountID, InboxID: template.InboxID, Name: template.Name, Status: template.Status, Category: template.Category, Language: template.Language, Components: template.Components}
}

func identityFromUser(user *domain.User) *identityView {
	return &identityView{ID: user.ID, Name: user.Name, DisplayName: user.DisplayName, Email: user.Email, Role: user.Role, Type: user.Type, Availability: user.Availability, AvailabilityStatus: user.AvailabilityStatus, AvatarURL: user.AvatarURL, Timezone: user.Timezone}
}

func mediaAssetFromModel(asset *domain.MediaAsset) *mediaAssetView {
	return &mediaAssetView{ID: asset.ID, AccountID: asset.AccountID, FileName: asset.FileName, ContentType: asset.ContentType, FileSize: asset.FileSize, Checksum: asset.Checksum, Status: asset.Status, SecurityState: asset.SecurityState, ObjectVersion: asset.ObjectVersion, CreatedAt: asset.CreatedAt, UpdatedAt: asset.UpdatedAt}
}

func conversationFromModel(db *gorm.DB, conversation *domain.Conversation) (*conversationView, error) {
	view := &conversationView{ID: conversation.ID, UUID: conversation.UUID, DisplayID: conversation.DisplayID, AccountID: conversation.AccountID, InboxID: conversation.InboxID, ContactID: conversation.ContactID, AssigneeID: conversation.AssigneeID, TeamID: conversation.TeamID, Status: conversation.Status, Priority: conversation.Priority, ObjectVersion: conversation.ObjectVersion, SnoozedUntil: conversation.SnoozedUntil, LastActivityAt: conversation.LastActivityAt, UnreadCount: conversation.UnreadCount, Muted: conversation.Muted, LabelIDs: []uint{}, CreatedAt: conversation.CreatedAt, UpdatedAt: conversation.UpdatedAt}
	if err := db.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conversation.ID).Order("label_id ASC").Pluck("label_id", &view.LabelIDs).Error; err != nil {
		return nil, err
	}
	return view, nil
}

func mediaCommandTarget(command foundation.CommandEnvelope) (uint, *ContractFailure) {
	if command.Target.Type != "asset" {
		return 0, &ContractFailure{Type: "validation_failed", Message: "asset target is required"}
	}
	parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, &ContractFailure{Type: "validation_failed", Message: "asset target is invalid"}
	}
	return uint(parsed), nil
}

func int64Filter(filter map[string]any, key string) (int64, error) {
	value, ok := filter[key]
	if !ok {
		return 0, errors.New("filter is missing")
	}
	switch typed := value.(type) {
	case float64:
		if typed != float64(int64(typed)) {
			return 0, errors.New("filter is invalid")
		}
		return int64(typed), nil
	case int64:
		return typed, nil
	case int:
		return int64(typed), nil
	case uint:
		if uint64(typed) > uint64(^uint64(0)>>1) {
			return 0, errors.New("filter is invalid")
		}
		return int64(typed), nil
	default:
		return 0, errors.New("filter is invalid")
	}
}

func uintFilter(filter map[string]any, key string) (uint, error) {
	value, ok := filter[key]
	if !ok {
		return 0, errors.New("filter is missing")
	}
	switch typed := value.(type) {
	case float64:
		if typed <= 0 || typed != float64(uint(typed)) {
			return 0, errors.New("filter is invalid")
		}
		return uint(typed), nil
	case uint:
		if typed == 0 {
			return 0, errors.New("filter is invalid")
		}
		return typed, nil
	case int:
		if typed <= 0 {
			return 0, errors.New("filter is invalid")
		}
		return uint(typed), nil
	case string:
		parsed, err := strconv.ParseUint(typed, 10, 64)
		if err != nil || parsed == 0 {
			return 0, errors.New("filter is invalid")
		}
		return uint(parsed), nil
	default:
		return 0, errors.New("filter is invalid")
	}
}

func uintSliceFilter(filter map[string]any, key string) ([]uint, bool) {
	raw, ok := filter[key]
	if !ok {
		return nil, false
	}
	values, ok := raw.([]any)
	if !ok {
		if typed, direct := raw.([]uint); direct && len(typed) > 0 {
			return typed, true
		}
		return nil, false
	}
	ids := make([]uint, 0, len(values))
	for _, value := range values {
		id, err := uintFilter(map[string]any{"id": value}, "id")
		if err != nil {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, len(ids) > 0
}

type moduleClient struct {
	caller, baseURL, token, version string
	client                          *http.Client
}

func newModuleClient(caller string, dependency DependencyConfig, version string, timeout time.Duration) *moduleClient {
	return &moduleClient{caller: caller, baseURL: strings.TrimRight(dependency.URL, "/"), token: dependency.Token, version: version, client: &http.Client{Timeout: timeout}}
}

func (c *moduleClient) getIdentity(ctx context.Context, parent foundation.QueryEnvelope, userID uint) (*identityView, error) {
	identities, err := c.getIdentities(ctx, parent, []uint{userID})
	if err != nil {
		return nil, err
	}
	identity := identities[userID]
	if identity == nil {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "identity was not found"}
	}
	return identity, nil
}

func (c *moduleClient) getAccount(ctx context.Context, accountID uint, actor foundation.ContractActor) (*accountView, error) {
	correlationID := foundation.GenerateUUID()
	envelope := foundation.QueryEnvelope{QueryType: "ten.account.get.v1", AccountID: accountID, Actor: actor, Filter: map[string]any{}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name", "locale", "status", "timezone"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	body, _ := json.Marshal(envelope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contracts/queries/ten.account.get.v1", bytes.NewReader(body))
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN request could not be created"}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(headerCaller, c.caller)
	request.Header.Set(headerContractVersion, c.version)
	request.Header.Set(headerCorrelationID, correlationID)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN dependency is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return nil, &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN returned an invalid response"}
	}
	var payload struct {
		Data accountView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || payload.Data.ID != accountID {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN returned an invalid account"}
	}
	return &payload.Data, nil
}

func (c *moduleClient) requireMember(ctx context.Context, accountID uint, actor foundation.ContractActor, userID uint) error {
	correlationID := foundation.GenerateUUID()
	envelope := foundation.QueryEnvelope{QueryType: "ten.permissions.check.v1", AccountID: accountID, Actor: actor, Filter: map[string]any{"user_id": userID}, Sort: []string{"user_id"}, Page: "1", Fields: []string{"account_id", "user_id", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	body, _ := json.Marshal(envelope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contracts/queries/ten.permissions.check.v1", bytes.NewReader(body))
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "TEN request could not be created"}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(headerCaller, c.caller)
	request.Header.Set(headerContractVersion, c.version)
	request.Header.Set(headerCorrelationID, correlationID)
	response, err := c.client.Do(request)
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "TEN dependency is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return &ContractFailure{Type: "dependency_unavailable", Message: "TEN returned an invalid response"}
	}
	var payload struct {
		Data membershipCheckView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "TEN returned invalid members"}
	}
	if payload.Data.AccountID == accountID && payload.Data.UserID == userID && payload.Data.Allowed {
		return nil
	}
	return &ContractFailure{Type: "resource_not_found", Message: "account member was not found"}
}

func (c *moduleClient) requireCapability(ctx context.Context, accountID uint, actor foundation.ContractActor, capability string) error {
	correlationID := foundation.GenerateUUID()
	envelope := foundation.QueryEnvelope{QueryType: "ent.capability.check.v1", AccountID: accountID, Actor: actor, Filter: map[string]any{"capability": capability}, Sort: []string{"capability"}, Page: "1", Fields: []string{"account_id", "capability", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	body, _ := json.Marshal(envelope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contracts/queries/ent.capability.check.v1", bytes.NewReader(body))
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "ENT request could not be created"}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(headerCaller, c.caller)
	request.Header.Set(headerContractVersion, c.version)
	request.Header.Set(headerCorrelationID, correlationID)
	response, err := c.client.Do(request)
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "ENT dependency is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return &ContractFailure{Type: "dependency_unavailable", Message: "ENT returned an invalid response"}
	}
	var payload struct {
		Data capabilityCheckView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "ENT returned an invalid capability"}
	}
	if payload.Data.AccountID != accountID || payload.Data.Capability != capability || !payload.Data.Allowed {
		return &ContractFailure{Type: "feature_unavailable", Message: "channel authorization is not enabled"}
	}
	return nil
}

func (c *moduleClient) getIdentities(ctx context.Context, parent foundation.QueryEnvelope, userIDs []uint) (map[uint]*identityView, error) {
	if len(userIDs) == 0 {
		return map[uint]*identityView{}, nil
	}
	correlationID := foundation.GenerateUUID()
	envelope := foundation.QueryEnvelope{QueryType: "iam.identity.get.v1", AccountID: parent.AccountID, Actor: parent.Actor, Filter: map[string]any{"ids": userIDs}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name", "display_name", "email", "role", "type", "availability", "availability_status", "avatar_url", "timezone"}, Consistency: parent.Consistency, RequestedAt: time.Now().UTC()}
	body, _ := json.Marshal(envelope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contracts/queries/iam.identity.get.v1", bytes.NewReader(body))
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "IAM request could not be created"}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(headerCaller, c.caller)
	request.Header.Set(headerContractVersion, c.version)
	request.Header.Set(headerCorrelationID, correlationID)
	response, err := c.client.Do(request)
	if err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "IAM dependency is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return nil, &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "IAM returned an invalid response"}
	}
	var payload struct {
		Data []identityView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "IAM returned invalid identities"}
	}
	result := make(map[uint]*identityView, len(payload.Data))
	for i := range payload.Data {
		identity := payload.Data[i]
		result[identity.ID] = &identity
	}
	return result, nil
}
