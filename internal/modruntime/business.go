package modruntime

import (
	"bytes"
	"context"
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
	case "CON":
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
