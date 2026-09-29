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
	"net/mail"
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

type messageView struct {
	ID             uint       `json:"id"`
	AccountID      uint       `json:"account_id"`
	ConversationID uint       `json:"conversation_id"`
	SenderType     string     `json:"sender_type"`
	SenderID       uint       `json:"sender_id"`
	MessageType    string     `json:"message_type"`
	ContentType    string     `json:"content_type"`
	Content        string     `json:"content"`
	Private        bool       `json:"private"`
	Status         string     `json:"status"`
	Deleted        bool       `json:"deleted"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	EditedAt       *time.Time `json:"edited_at,omitempty"`
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

type teamView struct {
	AccountID uint `json:"account_id"`
	TeamID    uint `json:"team_id"`
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

type ticketView struct {
	ID             uint       `json:"id"`
	AccountID      uint       `json:"account_id"`
	TicketNumber   string     `json:"ticket_number"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Status         string     `json:"status"`
	Priority       string     `json:"priority"`
	AssignedGroup  string     `json:"assigned_group"`
	AssigneeID     *uint      `json:"assignee_id,omitempty"`
	ContactID      *uint      `json:"contact_id,omitempty"`
	ConversationID *uint      `json:"conversation_id,omitempty"`
	CreatorID      *uint      `json:"creator_id,omitempty"`
	DueAt          *time.Time `json:"due_at,omitempty"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
	SLAStatus      string     `json:"sla_status"`
	Version        int        `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type csatSurveyView struct {
	ID              uint       `json:"id"`
	AccountID       uint       `json:"account_id"`
	ConversationID  uint       `json:"conversation_id"`
	Rating          int        `json:"rating"`
	FeedbackText    string     `json:"feedback_text"`
	AssignedAgentID *uint      `json:"assigned_agent_id,omitempty"`
	ReviewStatus    string     `json:"review_status"`
	ReviewerID      *uint      `json:"reviewer_id,omitempty"`
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type qualityReviewView struct {
	ID                 uint       `json:"id"`
	AccountID          uint       `json:"account_id"`
	TaskNumber         string     `json:"task_number"`
	TargetType         string     `json:"target_type"`
	TargetID           uint       `json:"target_id"`
	TargetRef          string     `json:"target_ref"`
	ScorecardID        uint       `json:"scorecard_id"`
	ScorecardName      string     `json:"scorecard_name"`
	InspectorID        *uint      `json:"inspector_id,omitempty"`
	AgentID            *uint      `json:"agent_id,omitempty"`
	SamplingRuleID     *uint      `json:"sampling_rule_id,omitempty"`
	DueAt              *time.Time `json:"due_at,omitempty"`
	CompletedAt        *time.Time `json:"completed_at,omitempty"`
	Status             string     `json:"status"`
	TotalScore         int        `json:"total_score"`
	Result             string     `json:"result"`
	HasFatalError      bool       `json:"has_fatal_error"`
	Feedback           string     `json:"feedback"`
	RectificationPlan  string     `json:"rectification_plan"`
	RectifiedAt        *time.Time `json:"rectified_at,omitempty"`
	SecondInspectorID  *uint      `json:"second_inspector_id,omitempty"`
	SecondReviewStatus string     `json:"second_review_status"`
	SecondReviewNotes  string     `json:"second_review_notes,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

type portalView struct {
	ID           uint      `json:"id"`
	AccountID    uint      `json:"account_id"`
	Name         string    `json:"name"`
	Slug         string    `json:"slug"`
	CustomDomain string    `json:"custom_domain"`
	Color        string    `json:"color"`
	HeaderText   string    `json:"header_text"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type articleView struct {
	ID         uint      `json:"id"`
	PortalID   uint      `json:"portal_id"`
	CategoryID uint      `json:"category_id"`
	AccountID  uint      `json:"account_id"`
	AuthorID   uint      `json:"author_id"`
	Title      string    `json:"title"`
	Slug       string    `json:"slug"`
	Content    string    `json:"content"`
	Status     string    `json:"status"`
	Views      int64     `json:"views"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
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
			if _, exists := query.Filter["team_id"]; exists {
				teamID, err := uintFilter(query.Filter, "team_id")
				if err != nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "permission team_id is invalid"}
				}
				team, err := repository.NewTeamRepository(r.DB).FindSummaryByID(query.AccountID, teamID)
				if err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "team store is unavailable"}
				}
				return teamView{AccountID: query.AccountID, TeamID: teamID, Allowed: team != nil}, nil
			}
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
		if err := r.Contracts.RegisterQuery("con.status_history.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			conversationID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation id is required"}
			}
			var exists int64
			if err := r.DB.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", query.AccountID, conversationID).Count(&exists).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			if exists == 0 {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			var rows []domain.LocalChangeJournal
			if err := r.DB.Where("account_id = ? AND action = ? AND object_type = ? AND object_display_id = ?", query.AccountID, "event_outbox", "Conversation", strconv.FormatUint(uint64(conversationID), 10)).Order("object_version_after ASC, occurred_at ASC").Find(&rows).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation history is unavailable"}
			}
			history := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				var event foundation.EventEnvelope
				if json.Unmarshal([]byte(row.AfterState), &event) != nil || !strings.HasPrefix(event.EventType, "con.conversation") {
					continue
				}
				history = append(history, map[string]any{"event_id": event.EventID, "event_type": event.EventType, "version": event.AggregateVersion, "occurred_at": event.OccurredAt, "changed_fields": event.ChangedFields})
			}
			return map[string]any{"conversation_id": conversationID, "items": history}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("con.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			owned := r.DB.Model(&domain.Conversation{}).Select("id").Where("account_id = ?", query.AccountID)
			counts := map[string]int64{}
			statements := map[string]*gorm.DB{
				"conversation_count": r.DB.Model(&domain.Conversation{}).Where("account_id = ?", query.AccountID),
				"label_link_count":   r.DB.Model(&domain.ConversationLabel{}).Where("conversation_id IN (?)", owned),
				"participant_count":  r.DB.Model(&domain.ConversationParticipant{}).Where("conversation_id IN (?)", owned),
			}
			for name, statement := range statements {
				var count int64
				if err := statement.Count(&count).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation deletion status is unavailable"}
				}
				counts[name] = count
			}
			counts["account_id"] = int64(query.AccountID)
			return counts, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.conversation.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			inboxID, inboxErr := uintFilter(command.Input, "inbox_id")
			contactID, contactErr := uintFilter(command.Input, "contact_id")
			if inboxErr != nil || contactErr != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox_id and contact_id are required"}
			}
			tenDependency, tenOK := r.Config.Dependencies["TEN"]
			chnDependency, chnOK := r.Config.Dependencies["CHN"]
			cusDependency, cusOK := r.Config.Dependencies["CUS"]
			if !tenOK || !chnOK || !cusOK {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation reference validation is unavailable"}
			}
			if _, err := newModuleClient("CON", tenDependency, r.Config.ContractVersion, r.Config.MaxTimeout).getAccount(ctx, command.AccountID, command.Actor); err != nil {
				return nil, err
			}
			parent := foundation.QueryEnvelope{AccountID: command.AccountID, Actor: command.Actor, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
			var inbox inboxView
			parent.QueryType, parent.Filter = "chn.inbox.get.v1", map[string]any{"id": inboxID}
			if err := newModuleClient("CON", chnDependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, parent, &inbox); err != nil {
				return nil, err
			}
			var contact contactView
			parent.QueryType, parent.Filter = "cus.contact.get.v1", map[string]any{"id": contactID}
			if err := newModuleClient("CON", cusDependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, parent, &contact); err != nil {
				return nil, err
			}
			if inbox.AccountID != command.AccountID || contact.AccountID != command.AccountID {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation reference was not found"}
			}
			priority, _ := command.Input["priority"].(string)
			priority = strings.TrimSpace(strings.ToLower(priority))
			if priority == "" {
				priority = "medium"
			}
			if priority != "low" && priority != "medium" && priority != "high" && priority != "urgent" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation priority is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			var maxDisplayID uint
			if err := tx.Model(&domain.Conversation{}).Where("account_id = ?", command.AccountID).Select("COALESCE(MAX(display_id),0)").Scan(&maxDisplayID).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation sequence is unavailable"}
			}
			now := time.Now().UTC()
			conversation := domain.Conversation{AccountID: command.AccountID, DisplayID: maxDisplayID + 1, InboxID: inboxID, ContactID: contactID, Status: domain.ConversationStatusOpen, Priority: priority, ObjectVersion: 1, LastActivityAt: now, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&conversation).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation.created.v1", "Conversation", strconv.FormatUint(uint64(conversation.ID), 10), []string{"created"}, map[string]any{"conversation_id": conversation.ID, "display_id": conversation.DisplayID, "inbox_id": inboxID, "contact_id": contactID, "status": conversation.Status, "priority": conversation.Priority}); err != nil {
				return nil, err
			}
			return conversationFromModel(tx, &conversation)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.conversation.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			operation, _ := command.Input["operation"].(string)
			if strings.EqualFold(strings.TrimSpace(operation), "delete") {
				tx := Transaction(ctx, r.DB)
				var conversation domain.Conversation
				if err := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).First(&conversation).Error; errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
				} else if err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
				}
				if err := tx.Where("conversation_id = ?", conversationID).Delete(&domain.ConversationLabel{}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "conversation labels could not be deleted"}
				}
				if err := tx.Where("conversation_id = ?", conversationID).Delete(&domain.ConversationParticipant{}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "conversation participants could not be deleted"}
				}
				if result := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).Delete(&domain.Conversation{}); result.Error != nil || result.RowsAffected != 1 {
					return nil, &ContractFailure{Type: "state_conflict", Message: "conversation could not be deleted"}
				}
				if err := r.appendBusinessEvent(ctx, command, "con.conversation.deleted.v1", "Conversation", command.Target.ID, []string{"deleted"}, map[string]any{"conversation_id": conversationID, "display_id": conversation.DisplayID, "operation": "deleted"}); err != nil {
					return nil, err
				}
				return map[string]any{"conversation_id": conversationID, "status": "deleted"}, nil
			}
			updates := map[string]any{}
			changed := make([]string, 0, 2)
			if value, ok := command.Input["muted"]; ok {
				muted, valid := value.(bool)
				if !valid {
					return nil, &ContractFailure{Type: "validation_failed", Message: "muted must be boolean"}
				}
				updates["muted"], changed = muted, append(changed, "muted")
			}
			if _, ok := command.Input["inbox_id"]; ok {
				inboxID, err := uintFilter(command.Input, "inbox_id")
				if err != nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "inbox_id is invalid"}
				}
				dependency, exists := r.Config.Dependencies["CHN"]
				if !exists {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "inbox validation is unavailable"}
				}
				query := foundation.QueryEnvelope{QueryType: "chn.inbox.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": inboxID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
				var inbox inboxView
				if err := newModuleClient("CON", dependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &inbox); err != nil {
					return nil, err
				}
				if inbox.AccountID != command.AccountID {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
				}
				updates["inbox_id"], changed = inboxID, append(changed, "inbox_id")
			}
			if len(updates) == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation update is empty"}
			}
			updates["object_version"] = gorm.Expr("object_version + 1")
			updates["last_activity_at"], updates["updated_at"] = time.Now().UTC(), time.Now().UTC()
			tx := Transaction(ctx, r.DB)
			result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND status <> ?", command.AccountID, conversationID, domain.ConversationStatusDeleting).Updates(updates)
			if result.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation could not be updated"}
			}
			if result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			var conversation domain.Conversation
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).First(&conversation).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation.changed.v1", "Conversation", command.Target.ID, changed, map[string]any{"conversation_id": conversation.ID, "object_version": conversation.ObjectVersion, "changed_fields": changed}); err != nil {
				return nil, err
			}
			return conversationFromModel(tx, &conversation)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.assignment.change.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			updates := map[string]any{}
			changed := make([]string, 0, 2)
			dependency, exists := r.Config.Dependencies["TEN"]
			if !exists {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "assignment validation is unavailable"}
			}
			client := newModuleClient("CON", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
			if value, ok := command.Input["assignee_id"]; ok {
				assigneeID, err := optionalUint(value)
				if err != nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "assignee_id is invalid"}
				}
				if assigneeID != nil {
					if err := client.requireMember(ctx, command.AccountID, command.Actor, *assigneeID); err != nil {
						return nil, err
					}
				}
				updates["assignee_id"], changed = assigneeID, append(changed, "assignee_id")
			}
			if value, ok := command.Input["team_id"]; ok {
				teamID, err := optionalUint(value)
				if err != nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "team_id is invalid"}
				}
				if teamID != nil {
					query := foundation.QueryEnvelope{QueryType: "ten.permissions.check.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"team_id": *teamID}, Sort: []string{"team_id"}, Page: "1", Fields: []string{"account_id", "team_id", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
					var team teamView
					if err := client.query(ctx, query, &team); err != nil {
						return nil, err
					}
					if team.AccountID != command.AccountID || team.TeamID != *teamID || !team.Allowed {
						return nil, &ContractFailure{Type: "resource_not_found", Message: "team was not found"}
					}
				}
				updates["team_id"], changed = teamID, append(changed, "team_id")
			}
			if len(updates) == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "assignment change is empty"}
			}
			updates["object_version"] = gorm.Expr("object_version + 1")
			updates["last_activity_at"], updates["updated_at"] = time.Now().UTC(), time.Now().UTC()
			tx := Transaction(ctx, r.DB)
			result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND status <> ?", command.AccountID, conversationID, domain.ConversationStatusDeleting).Updates(updates)
			if result.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "assignment could not be changed"}
			}
			if result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			var conversation domain.Conversation
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).First(&conversation).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation.changed.v1", "Conversation", command.Target.ID, changed, map[string]any{"conversation_id": conversation.ID, "object_version": conversation.ObjectVersion, "changed_fields": changed}); err != nil {
				return nil, err
			}
			return conversationFromModel(tx, &conversation)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.status.change.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			status, _ := command.Input["status"].(string)
			status = strings.TrimSpace(strings.ToLower(status))
			if status != domain.ConversationStatusOpen && status != domain.ConversationStatusPending && status != domain.ConversationStatusResolved && status != domain.ConversationStatusSnoozed {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation status is invalid"}
			}
			updates := map[string]any{"status": status, "snoozed_until": nil, "object_version": gorm.Expr("object_version + 1"), "last_activity_at": time.Now().UTC(), "updated_at": time.Now().UTC()}
			if status == domain.ConversationStatusSnoozed {
				raw, ok := command.Input["snoozed_until"].(string)
				until, err := time.Parse(time.RFC3339, raw)
				if !ok || err != nil || !until.After(time.Now().UTC()) {
					return nil, &ContractFailure{Type: "validation_failed", Message: "future snoozed_until is required"}
				}
				updates["snoozed_until"] = until.UTC()
			}
			tx := Transaction(ctx, r.DB)
			result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND status <> ?", command.AccountID, conversationID, domain.ConversationStatusDeleting).Updates(updates)
			if result.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation status could not be changed"}
			}
			if result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			var conversation domain.Conversation
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).First(&conversation).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation_status.changed.v1", "Conversation", command.Target.ID, []string{"status", "snoozed_until"}, map[string]any{"conversation_id": conversation.ID, "status": conversation.Status, "snoozed_until": conversation.SnoozedUntil, "object_version": conversation.ObjectVersion}); err != nil {
				return nil, err
			}
			return conversationFromModel(tx, &conversation)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.priority.change.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			priority, _ := command.Input["priority"].(string)
			priority = strings.TrimSpace(strings.ToLower(priority))
			if priority != "low" && priority != "medium" && priority != "high" && priority != "urgent" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation priority is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			result := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ? AND status <> ?", command.AccountID, conversationID, domain.ConversationStatusDeleting).Updates(map[string]any{"priority": priority, "object_version": gorm.Expr("object_version + 1"), "last_activity_at": time.Now().UTC(), "updated_at": time.Now().UTC()})
			if result.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation priority could not be changed"}
			}
			if result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			var conversation domain.Conversation
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, conversationID).First(&conversation).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation store is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation.changed.v1", "Conversation", command.Target.ID, []string{"priority"}, map[string]any{"conversation_id": conversation.ID, "priority": conversation.Priority, "object_version": conversation.ObjectVersion}); err != nil {
				return nil, err
			}
			return conversationFromModel(tx, &conversation)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.participant.manage.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			operation, _ := command.Input["operation"].(string)
			operation = strings.TrimSpace(strings.ToLower(operation))
			if operation != "add" && operation != "remove" && operation != "sync" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "participant operation is invalid"}
			}
			userIDs, err := uintSlice(command.Input["user_ids"])
			if err != nil || (operation != "sync" && len(userIDs) == 0) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "participant user_ids are invalid"}
			}
			dependency, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "participant validation is unavailable"}
			}
			client := newModuleClient("CON", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
			for _, userID := range userIDs {
				if err := client.requireMember(ctx, command.AccountID, command.Actor, userID); err != nil {
					return nil, err
				}
			}
			tx := Transaction(ctx, r.DB)
			repo := repository.NewConversationRepositoryWithReadPorts(tx, nil, nil, nil, nil, nil, nil, nil)
			var added, removed []uint
			switch operation {
			case "add":
				added, err = repo.AddParticipants(command.AccountID, conversationID, userIDs)
			case "remove":
				var count int64
				count, err = repo.RemoveParticipants(command.AccountID, conversationID, userIDs)
				if count > 0 {
					removed = userIDs
				}
			case "sync":
				var result *repository.ParticipantSyncResult
				result, err = repo.SyncParticipants(command.AccountID, conversationID, userIDs)
				if result != nil {
					added, removed = result.Added, result.Removed
				}
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			if err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "participants could not be changed"}
			}
			if err := tx.Model(&domain.Conversation{}).Where("account_id = ? AND id = ?", command.AccountID, conversationID).Updates(map[string]any{"object_version": gorm.Expr("object_version + 1"), "updated_at": time.Now().UTC()}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation version could not be changed"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation.changed.v1", "Conversation", command.Target.ID, []string{"participants"}, map[string]any{"conversation_id": conversationID, "operation": operation, "added_user_ids": added, "removed_user_ids": removed}); err != nil {
				return nil, err
			}
			return map[string]any{"conversation_id": conversationID, "operation": operation, "added_user_ids": added, "removed_user_ids": removed}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("con.account_conversations.anonymize.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			tx := Transaction(ctx, r.DB)
			owned := tx.Model(&domain.Conversation{}).Select("id").Where("account_id = ?", command.AccountID)
			if err := tx.Where("conversation_id IN (?)", owned).Delete(&domain.ConversationParticipant{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation participants could not be anonymized"}
			}
			if err := tx.Where("conversation_id IN (?)", owned).Delete(&domain.ConversationLabel{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation labels could not be anonymized"}
			}
			result := tx.Model(&domain.Conversation{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"contact_id": 0, "assignee_id": nil, "team_id": nil, "custom_attributes": "", "agent_last_seen_at": nil, "contact_last_seen_at": nil, "assignee_last_seen_at": nil, "object_version": gorm.Expr("object_version + 1"), "updated_at": time.Now().UTC()})
			if result.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversations could not be anonymized"}
			}
			if err := r.appendBusinessEvent(ctx, command, "con.conversation.changed.v1", "AccountConversations", command.Target.ID, []string{"anonymized"}, map[string]any{"account_id": command.AccountID, "conversation_count": result.RowsAffected, "operation": "anonymized"}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "conversation_count": result.RowsAffected, "status": "anonymized"}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterEvent("cus.contact.merged.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
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
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterEvent("msg.message.created.v1", func(ctx context.Context, event foundation.EventEnvelope) error {
			conversationID, err := uintFilter(event.Payload, "conversation_id")
			if err != nil {
				return &ContractFailure{Type: "validation_failed", Message: "conversation_id is required"}
			}
			senderID, _ := uintFilter(event.Payload, "sender_id")
			messageType, _ := event.Payload["message_type"].(string)
			incoming := messageType == domain.MessageTypeIncoming
			if err := repository.NewConversationRepositoryWithReadPorts(Transaction(ctx, r.DB), nil, nil, nil, nil, nil, nil, nil).ApplyMessageActivity(event.AccountID, conversationID, senderID, incoming, event.OccurredAt, event.EventID); errors.Is(err, gorm.ErrRecordNotFound) {
				return &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			} else if err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "conversation activity could not be applied"}
			}
			return nil
		})
	case "MSG":
		if err := r.Contracts.RegisterCommand("msg.message.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, err := uintFilter(command.Input, "conversation_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is required"}
			}
			if command.Target.Type != "conversation" || command.Target.ID != strconv.FormatUint(uint64(conversationID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation target must match conversation_id"}
			}
			content, _ := command.Input["content"].(string)
			content = strings.TrimSpace(content)
			if content == "" || len(content) > 100000 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message content is invalid"}
			}
			conDependency, ok := r.Config.Dependencies["CON"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation validation is unavailable"}
			}
			query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "contact_id", "status"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
			var conversation conversationView
			if err := newModuleClient("MSG", conDependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
				return nil, err
			}
			if conversation.AccountID != command.AccountID || conversation.Status == domain.ConversationStatusDeleting {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
			}
			senderType, _ := command.Input["sender_type"].(string)
			senderID, senderErr := uintFilter(command.Input, "sender_id")
			messageType, _ := command.Input["message_type"].(string)
			messageType = strings.ToLower(strings.TrimSpace(messageType))
			switch strings.ToLower(strings.TrimSpace(senderType)) {
			case "contact":
				senderType = domain.SenderTypeContact
				if senderErr != nil || senderID != conversation.ContactID || messageType != domain.MessageTypeIncoming {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "contact sender was not found"}
				}
			case "user":
				senderType = domain.SenderTypeUser
				if senderErr != nil || messageType != domain.MessageTypeOutgoing {
					return nil, &ContractFailure{Type: "validation_failed", Message: "outgoing user sender is invalid"}
				}
				tenDependency, exists := r.Config.Dependencies["TEN"]
				if !exists {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "sender validation is unavailable"}
				}
				if err := newModuleClient("MSG", tenDependency, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, senderID); err != nil {
					return nil, err
				}
			default:
				return nil, &ContractFailure{Type: "validation_failed", Message: "sender_type is invalid"}
			}
			contentType, _ := command.Input["content_type"].(string)
			contentType = strings.TrimSpace(contentType)
			if contentType == "" {
				contentType = "text"
			}
			private, _ := command.Input["private"].(bool)
			if private && messageType != domain.MessageTypeOutgoing {
				return nil, &ContractFailure{Type: "validation_failed", Message: "incoming message cannot be private"}
			}
			echoID, _ := command.Input["echo_id"].(string)
			echoID = strings.TrimSpace(echoID)
			tx := Transaction(ctx, r.DB)
			if echoID != "" {
				var existing domain.Message
				if err := tx.Where("account_id = ? AND conversation_id = ? AND echo_id = ?", command.AccountID, conversationID, echoID).First(&existing).Error; err == nil {
					return messageFromModel(existing), nil
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
				}
			}
			now := time.Now().UTC()
			message := domain.Message{AccountID: command.AccountID, ConversationID: conversationID, SenderType: senderType, SenderID: senderID, MessageType: messageType, ContentType: contentType, Content: content, Private: private, Status: "sent", EchoID: echoID, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&message).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "message could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "msg.message.created.v1", "Message", strconv.FormatUint(uint64(message.ID), 10), []string{"created"}, map[string]any{"message_id": message.ID, "conversation_id": conversationID, "sender_type": senderType, "sender_id": senderID, "message_type": messageType, "content_type": contentType, "private": private, "status": message.Status}); err != nil {
				return nil, err
			}
			return messageFromModel(message), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("msg.message.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "message" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message target is required"}
			}
			parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsed == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message target is invalid"}
			}
			messageID := uint(parsed)
			operation, _ := command.Input["operation"].(string)
			operation = strings.ToLower(strings.TrimSpace(operation))
			if operation != "edit" && operation != "delete" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message update operation is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			var message domain.Message
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, messageID).First(&message).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "message was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			if message.Deleted {
				return nil, &ContractFailure{Type: "state_conflict", Message: "deleted message cannot be changed"}
			}
			now := time.Now().UTC()
			eventType, changed := "msg.message.changed.v1", []string{"content", "edited_at"}
			if operation == "edit" {
				content, _ := command.Input["content"].(string)
				content = strings.TrimSpace(content)
				if content == "" || len(content) > 100000 {
					return nil, &ContractFailure{Type: "validation_failed", Message: "message content is invalid"}
				}
				if err := tx.Model(&domain.Message{}).Where("account_id = ? AND id = ? AND deleted = ?", command.AccountID, messageID, false).Updates(map[string]any{"content": content, "edited_at": now, "updated_at": now}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "message could not be edited"}
				}
				message.Content, message.EditedAt, message.UpdatedAt = content, &now, now
			} else {
				if err := tx.Where("account_id = ? AND message_id = ?", command.AccountID, messageID).Delete(&domain.Attachment{}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "message attachments could not be deleted"}
				}
				if err := tx.Model(&domain.Message{}).Where("account_id = ? AND id = ? AND deleted = ?", command.AccountID, messageID, false).Updates(map[string]any{"deleted": true, "deleted_at": now, "content": "", "translations": "", "content_attributes": "", "updated_at": now}).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "message could not be deleted"}
				}
				message.Deleted, message.DeletedAt, message.Content, message.UpdatedAt = true, &now, "", now
				eventType, changed = "msg.message.deleted.v1", []string{"deleted"}
			}
			if err := r.appendBusinessEvent(ctx, command, eventType, "Message", command.Target.ID, changed, map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "operation": operation, "deleted": message.Deleted}); err != nil {
				return nil, err
			}
			return messageFromModel(message), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("msg.provider_status.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			messageID, failure := messageCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			status, _ := command.Input["status"].(string)
			status = strings.ToLower(strings.TrimSpace(status))
			if status != domain.MessageStatusSent && status != domain.MessageStatusDelivered && status != domain.MessageStatusRead && status != domain.MessageStatusFailed {
				return nil, &ContractFailure{Type: "validation_failed", Message: "provider message status is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			var message domain.Message
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, messageID).First(&message).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "message was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			if message.Deleted {
				return nil, &ContractFailure{Type: "state_conflict", Message: "deleted message status cannot change"}
			}
			if message.Status == status {
				return messageFromModel(message), nil
			}
			allowed := map[string]map[string]bool{domain.MessageStatusSent: {domain.MessageStatusDelivered: true, domain.MessageStatusRead: true, domain.MessageStatusFailed: true}, domain.MessageStatusDelivered: {domain.MessageStatusRead: true}, domain.MessageStatusFailed: {}, domain.MessageStatusRead: {}}
			if !allowed[message.Status][status] {
				return nil, &ContractFailure{Type: "state_conflict", Message: "provider message status cannot move backward"}
			}
			if err := tx.Model(&domain.Message{}).Where("account_id = ? AND id = ? AND status = ?", command.AccountID, messageID, message.Status).Updates(map[string]any{"status": status, "updated_at": time.Now().UTC()}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "provider message status could not be changed"}
			}
			message.Status, message.UpdatedAt = status, time.Now().UTC()
			if err := r.appendBusinessEvent(ctx, command, "msg.message_status.changed.v1", "Message", command.Target.ID, []string{"status"}, map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "status": status}); err != nil {
				return nil, err
			}
			return messageFromModel(message), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("msg.message.retry.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			messageID, failure := messageCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			tx := Transaction(ctx, r.DB)
			var message domain.Message
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, messageID).First(&message).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "message was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			if message.Deleted || message.MessageType != domain.MessageTypeOutgoing || message.Status != domain.MessageStatusFailed {
				return nil, &ContractFailure{Type: "state_conflict", Message: "only failed outgoing messages can be retried"}
			}
			if err := tx.Model(&domain.Message{}).Where("account_id = ? AND id = ? AND status = ?", command.AccountID, messageID, domain.MessageStatusFailed).Updates(map[string]any{"status": domain.MessageStatusSent, "updated_at": time.Now().UTC()}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "message retry could not be scheduled"}
			}
			message.Status, message.UpdatedAt = domain.MessageStatusSent, time.Now().UTC()
			if err := r.appendBusinessEvent(ctx, command, "msg.message_status.changed.v1", "Message", command.Target.ID, []string{"status"}, map[string]any{"message_id": message.ID, "conversation_id": message.ConversationID, "status": message.Status, "operation": "retry"}); err != nil {
				return nil, err
			}
			return messageFromModel(message), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("msg.read_position.update.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "conversation" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation target is required"}
			}
			parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsed == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation target is invalid"}
			}
			conversationID := uint(parsed)
			messageID, messageErr := uintFilter(command.Input, "message_id")
			userID, userErr := uintFilter(command.Input, "user_id")
			if messageErr != nil || userErr != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message_id and user_id are required"}
			}
			tenDependency, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "reader validation is unavailable"}
			}
			if err := newModuleClient("MSG", tenDependency, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, userID); err != nil {
				return nil, err
			}
			tx := Transaction(ctx, r.DB)
			var message domain.Message
			if err := tx.Select("id", "account_id", "conversation_id").Where("account_id = ? AND conversation_id = ? AND id = ?", command.AccountID, conversationID, messageID).First(&message).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "message was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			aggregateID := fmt.Sprintf("%d:%d", conversationID, userID)
			var rows []domain.LocalChangeJournal
			if err := tx.Where("source_module = ? AND action = ? AND object_type = ? AND object_display_id = ?", "MSG", "event_outbox", "ReadPosition", aggregateID).Order("object_version_after DESC").Limit(1).Find(&rows).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "read position store is unavailable"}
			}
			if len(rows) == 1 {
				var prior foundation.EventEnvelope
				if json.Unmarshal([]byte(rows[0].AfterState), &prior) == nil {
					if previous, err := uintFilter(prior.Payload, "message_id"); err == nil && previous > messageID {
						return nil, &ContractFailure{Type: "state_conflict", Message: "read position cannot move backward"}
					} else if err == nil && previous == messageID {
						return map[string]any{"account_id": command.AccountID, "conversation_id": conversationID, "user_id": userID, "message_id": messageID}, nil
					}
				}
			}
			if err := r.appendBusinessEvent(ctx, command, "msg.read_position.changed.v1", "ReadPosition", aggregateID, []string{"message_id"}, map[string]any{"conversation_id": conversationID, "user_id": userID, "message_id": messageID}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "conversation_id": conversationID, "user_id": userID, "message_id": messageID}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("msg.email.send.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			toEmail, _ := command.Input["to_email"].(string)
			fromEmail, _ := command.Input["from_email"].(string)
			subject, _ := command.Input["subject"].(string)
			htmlBody, _ := command.Input["content_html"].(string)
			textBody, _ := command.Input["content_text"].(string)
			toEmail, fromEmail, subject = strings.TrimSpace(toEmail), strings.TrimSpace(fromEmail), strings.TrimSpace(subject)
			if address, err := mail.ParseAddress(toEmail); err != nil || address.Address != toEmail {
				return nil, &ContractFailure{Type: "validation_failed", Message: "to_email is invalid"}
			}
			if address, err := mail.ParseAddress(fromEmail); err != nil || address.Address != fromEmail {
				return nil, &ContractFailure{Type: "validation_failed", Message: "from_email is invalid"}
			}
			if subject == "" || len(subject) > 512 || (strings.TrimSpace(htmlBody) == "" && strings.TrimSpace(textBody) == "") {
				return nil, &ContractFailure{Type: "validation_failed", Message: "email subject and content are required"}
			}
			emailType, _ := command.Input["email_type"].(string)
			emailType = strings.ToLower(strings.TrimSpace(emailType))
			if emailType != "transcript" && emailType != "confirmation" && emailType != "reset_password" && emailType != "notification" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "email_type is invalid"}
			}
			var conversationID *uint
			if value, exists := command.Input["conversation_id"]; exists {
				parsed, err := optionalUint(value)
				if err != nil || parsed == nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is invalid"}
				}
				conversationID = parsed
				conDependency, ok := r.Config.Dependencies["CON"]
				if !ok {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "conversation validation is unavailable"}
				}
				query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": *conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
				var conversation conversationView
				if err := newModuleClient("MSG", conDependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
					return nil, err
				}
			}
			if command.Target.Type == "conversation" {
				if conversationID == nil || command.Target.ID != strconv.FormatUint(uint64(*conversationID), 10) {
					return nil, &ContractFailure{Type: "validation_failed", Message: "conversation target must match conversation_id"}
				}
			} else if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "email target is invalid"}
			}
			var userID *uint
			if value, exists := command.Input["user_id"]; exists {
				parsed, err := optionalUint(value)
				if err != nil || parsed == nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "user_id is invalid"}
				}
				userID = parsed
				tenDependency, ok := r.Config.Dependencies["TEN"]
				if !ok {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "email user validation is unavailable"}
				}
				if err := newModuleClient("MSG", tenDependency, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, *userID); err != nil {
					return nil, err
				}
			}
			now := time.Now().UTC()
			email := domain.EmailLog{AccountID: command.AccountID, ConversationID: conversationID, UserID: userID, EmailType: emailType, ToEmail: toEmail, FromEmail: fromEmail, Subject: subject, ContentHTML: htmlBody, ContentText: textBody, Status: "pending", DeliveryStatus: "pending", MaxRetries: 3, CreatedAt: now, UpdatedAt: now}
			tx := Transaction(ctx, r.DB)
			if err := tx.Create(&email).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "email delivery could not be queued"}
			}
			if err := r.appendBusinessEvent(ctx, command, "msg.email_delivery.changed.v1", "EmailLog", strconv.FormatUint(uint64(email.ID), 10), []string{"delivery_status"}, map[string]any{"email_log_id": email.ID, "conversation_id": email.ConversationID, "email_type": email.EmailType, "delivery_status": email.DeliveryStatus}); err != nil {
				return nil, err
			}
			return map[string]any{"id": email.ID, "account_id": email.AccountID, "conversation_id": email.ConversationID, "email_type": email.EmailType, "status": email.Status, "delivery_status": email.DeliveryStatus, "retry_count": email.RetryCount, "max_retries": email.MaxRetries, "created_at": email.CreatedAt, "updated_at": email.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("msg.account_messages.anonymize.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
			}
			tx := Transaction(ctx, r.DB)
			if err := tx.Where("account_id = ?", command.AccountID).Delete(&domain.Attachment{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "message attachments could not be anonymized"}
			}
			if err := tx.Where("account_id = ?", command.AccountID).Delete(&domain.DraftMessage{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "message drafts could not be anonymized"}
			}
			messages := tx.Model(&domain.Message{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"sender_id": 0, "content": "", "translations": "", "content_attributes": "", "echo_id": "", "updated_at": time.Now().UTC()})
			if messages.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "messages could not be anonymized"}
			}
			emails := tx.Model(&domain.EmailLog{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"conversation_id": nil, "user_id": nil, "to_email": "", "from_email": "", "subject": "", "content_html": "", "content_text": "", "bounce_reason": "", "error": "", "updated_at": time.Now().UTC()})
			if emails.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "email logs could not be anonymized"}
			}
			if err := r.appendBusinessEvent(ctx, command, "msg.message.changed.v1", "AccountMessages", command.Target.ID, []string{"anonymized"}, map[string]any{"account_id": command.AccountID, "message_count": messages.RowsAffected, "email_log_count": emails.RowsAffected, "operation": "anonymized"}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "message_count": messages.RowsAffected, "email_log_count": emails.RowsAffected, "status": "anonymized"}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("msg.message.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			messageID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message id is required"}
			}
			var message domain.Message
			if err := r.DB.Where("account_id = ? AND id = ?", query.AccountID, messageID).First(&message).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "message was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			return messageFromModel(message), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("msg.messages.list.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			conversationID, err := uintFilter(query.Filter, "conversation_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is required"}
			}
			statement := r.DB.Where("account_id = ? AND conversation_id = ?", query.AccountID, conversationID)
			if includePrivate, ok := query.Filter["include_private"].(bool); !ok || !includePrivate {
				statement = statement.Where("private = ?", false)
			}
			var total int64
			if err := statement.Model(&domain.Message{}).Count(&total).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			page := 1
			if parsed, err := strconv.Atoi(query.Page); err == nil && parsed > 0 {
				page = parsed
			}
			var messages []domain.Message
			if err := statement.Order("id ASC").Offset((page - 1) * 50).Limit(50).Find(&messages).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			items := make([]messageView, 0, len(messages))
			for _, message := range messages {
				items = append(items, messageFromModel(message))
			}
			return map[string]any{"items": items, "total": total, "page": page, "page_size": 50}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("msg.delivery_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			messageID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "message id is required"}
			}
			var message domain.Message
			if err := r.DB.Select("id", "account_id", "conversation_id", "status", "deleted", "created_at", "updated_at").Where("account_id = ? AND id = ?", query.AccountID, messageID).First(&message).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "message was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message store is unavailable"}
			}
			return map[string]any{"id": message.ID, "account_id": message.AccountID, "conversation_id": message.ConversationID, "status": message.Status, "deleted": message.Deleted, "created_at": message.CreatedAt, "updated_at": message.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("msg.email_delivery.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			emailID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "email delivery id is required"}
			}
			var email domain.EmailLog
			if err := r.DB.Select("id", "account_id", "conversation_id", "email_type", "status", "delivery_status", "retry_count", "max_retries", "next_retry_at", "last_attempt_at", "created_at", "updated_at").Where("account_id = ? AND id = ?", query.AccountID, emailID).First(&email).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "email delivery was not found"}
			} else if err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "email delivery store is unavailable"}
			}
			return map[string]any{"id": email.ID, "account_id": email.AccountID, "conversation_id": email.ConversationID, "email_type": email.EmailType, "status": email.Status, "delivery_status": email.DeliveryStatus, "retry_count": email.RetryCount, "max_retries": email.MaxRetries, "next_retry_at": email.NextRetryAt, "last_attempt_at": email.LastAttemptAt, "created_at": email.CreatedAt, "updated_at": email.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("msg.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			counts := map[string]int64{}
			for name, statement := range map[string]*gorm.DB{"message_count": r.DB.Model(&domain.Message{}).Where("account_id = ?", query.AccountID), "attachment_count": r.DB.Model(&domain.Attachment{}).Where("account_id = ?", query.AccountID), "draft_count": r.DB.Model(&domain.DraftMessage{}).Where("account_id = ?", query.AccountID), "email_log_count": r.DB.Model(&domain.EmailLog{}).Where("account_id = ?", query.AccountID)} {
				var count int64
				if err := statement.Count(&count).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "message deletion status is unavailable"}
				}
				counts[name] = count
			}
			counts["account_id"] = int64(query.AccountID)
			return counts, nil
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
			messages := repository.NewMessageRepositoryWithPorts(Transaction(ctx, r.DB), nil, nil, nil)
			if err := messages.ReassignContactSender(event.AccountID, mergedID, baseID); err != nil {
				return &ContractFailure{Type: "state_conflict", Message: "message sender projection could not be reassigned"}
			}
			return nil
		})
	case "RTE":
		conDependency, conOK := r.Config.Dependencies["CON"]
		tenDependency, tenOK := r.Config.Dependencies["TEN"]
		chnDependency, chnOK := r.Config.Dependencies["CHN"]
		if !conOK || !tenOK || !chnOK {
			return errors.New("RTE business contracts require CON, TEN and CHN dependencies")
		}
		conClient := newModuleClient("RTE", conDependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		tenClient := newModuleClient("RTE", tenDependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		chnClient := newModuleClient("RTE", chnDependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		loadConversation := func(ctx context.Context, query foundation.QueryEnvelope, conversationID uint) (*conversationView, error) {
			query.QueryType, query.Filter = "con.conversation.get.v1", map[string]any{"id": conversationID}
			query.Sort, query.Page, query.Fields, query.Consistency = []string{"id"}, "1", []string{"id", "account_id", "inbox_id", "assignee_id", "team_id", "status", "priority"}, "strong"
			var conversation conversationView
			if err := conClient.query(ctx, query, &conversation); err != nil {
				return nil, err
			}
			return &conversation, nil
		}
		changeAssignment := func(ctx context.Context, command foundation.CommandEnvelope, operation string) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			query := foundation.QueryEnvelope{AccountID: command.AccountID, Actor: command.Actor, RequestedAt: command.RequestedAt}
			if _, err := loadConversation(ctx, query, conversationID); err != nil {
				return nil, err
			}
			assignment := map[string]any{}
			switch operation {
			case "claim":
				value, exists := command.Input["assignee_id"]
				if !exists {
					value = command.Actor.ID
				}
				assigneeID, err := optionalUint(value)
				if err != nil || assigneeID == nil {
					return nil, &ContractFailure{Type: "validation_failed", Message: "assignee_id is required"}
				}
				assignment["assignee_id"], assignment["team_id"] = *assigneeID, nil
			case "transfer":
				for _, field := range []string{"assignee_id", "team_id"} {
					if value, exists := command.Input[field]; exists {
						parsed, err := optionalUint(value)
						if err != nil {
							return nil, &ContractFailure{Type: "validation_failed", Message: field + " is invalid"}
						}
						assignment[field] = parsed
					}
				}
				if len(assignment) == 0 {
					return nil, &ContractFailure{Type: "validation_failed", Message: "assignment transfer is empty"}
				}
			case "release":
				assignment["assignee_id"], assignment["team_id"] = nil, nil
			}
			downstream := foundation.CommandEnvelope{
				CommandID: command.CommandID + ":con-assignment", CommandType: "con.assignment.change.v1", AccountID: command.AccountID,
				Actor: command.Actor, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversationID), 10)},
				Input: assignment, IdempotencyKey: command.IdempotencyKey + ":con-assignment", CorrelationID: command.CorrelationID,
				CausationID: command.CommandID, RequestedAt: command.RequestedAt,
				TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: command.TimeoutPolicy.TimeoutMS, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"},
			}
			var updated conversationView
			if err := conClient.command(ctx, downstream, &updated); err != nil {
				return nil, err
			}
			if err := r.appendBusinessEvent(ctx, command, "rte.assignment.changed.v1", "RoutingAssignment", command.Target.ID, []string{"assignee_id", "team_id"}, map[string]any{"conversation_id": conversationID, "inbox_id": updated.InboxID, "assignee_id": updated.AssigneeID, "team_id": updated.TeamID, "operation": operation}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "conversation_id": conversationID, "inbox_id": updated.InboxID, "assignee_id": updated.AssigneeID, "team_id": updated.TeamID, "operation": operation}, nil
		}
		for capability, operation := range map[string]string{"rte.assignment.claim.v1": "claim", "rte.assignment.transfer.v1": "transfer", "rte.assignment.release.v1": "release"} {
			if err := r.Contracts.RegisterCommand(capability, func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
				return changeAssignment(ctx, command, operation)
			}); err != nil {
				return err
			}
		}
		if err := r.Contracts.RegisterCommand("rte.capacity.manage.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			userID, err := uintFilter(command.Input, "user_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "user_id is required"}
			}
			limit64, err := int64Filter(command.Input, "conversation_limit")
			if err != nil || limit64 < 0 || limit64 > 100000 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_limit is invalid"}
			}
			if err := tenClient.requireMember(ctx, command.AccountID, command.Actor, userID); err != nil {
				return nil, err
			}
			tx := Transaction(ctx, r.DB)
			var policy domain.CapacityPolicy
			result := tx.Where("account_id = ? AND user_id = ?", command.AccountID, userID).First(&policy)
			if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "capacity store is unavailable"}
			}
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				policy = domain.CapacityPolicy{AccountID: command.AccountID, UserID: userID, ConversationLimit: int(limit64)}
				if err := tx.Create(&policy).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "capacity policy could not be created"}
				}
			} else {
				if err := tx.Model(&policy).Update("conversation_limit", int(limit64)).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "capacity policy could not be updated"}
				}
				policy.ConversationLimit = int(limit64)
			}
			if err := r.appendBusinessEvent(ctx, command, "rte.capacity.changed.v1", "CapacityPolicy", strconv.FormatUint(uint64(policy.ID), 10), []string{"conversation_limit"}, map[string]any{"policy_id": policy.ID, "user_id": userID, "conversation_limit": policy.ConversationLimit}); err != nil {
				return nil, err
			}
			if err := r.appendBusinessEvent(ctx, command, "rte.availability.changed.v1", "RoutingAvailability", strconv.FormatUint(uint64(userID), 10), []string{"accepting_new_conversations"}, map[string]any{"user_id": userID, "accepting_new_conversations": policy.ConversationLimit != 0, "reason": "capacity_limit_changed"}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "policy_id": policy.ID, "user_id": userID, "conversation_limit": policy.ConversationLimit}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("rte.queue.enter.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			query := foundation.QueryEnvelope{AccountID: command.AccountID, Actor: command.Actor, RequestedAt: command.RequestedAt}
			conversation, err := loadConversation(ctx, query, conversationID)
			if err != nil {
				return nil, err
			}
			if conversation.Status != domain.ConversationStatusOpen && conversation.Status != domain.ConversationStatusPending {
				return nil, &ContractFailure{Type: "state_conflict", Message: "conversation cannot enter the queue in its current status"}
			}
			downstream := foundation.CommandEnvelope{
				CommandID: command.CommandID + ":con-assignment", CommandType: "con.assignment.change.v1", AccountID: command.AccountID,
				Actor: command.Actor, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversationID), 10)},
				Input: map[string]any{"assignee_id": nil, "team_id": nil}, IdempotencyKey: command.IdempotencyKey + ":con-assignment",
				CorrelationID: command.CorrelationID, CausationID: command.CommandID, RequestedAt: command.RequestedAt,
				TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: command.TimeoutPolicy.TimeoutMS, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"},
			}
			var updated conversationView
			if err := conClient.command(ctx, downstream, &updated); err != nil {
				return nil, err
			}
			if err := r.appendBusinessEvent(ctx, command, "rte.queue.changed.v1", "RoutingQueue", command.Target.ID, []string{"assignee_id", "team_id", "queued"}, map[string]any{"conversation_id": conversationID, "inbox_id": updated.InboxID, "queued": true}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "conversation_id": conversationID, "inbox_id": updated.InboxID, "status": updated.Status, "queued": true}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("rte.assignment.get.v1", func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
			conversationID, err := uintFilter(query.Filter, "conversation_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is required"}
			}
			conversation, err := loadConversation(ctx, query, conversationID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"account_id": query.AccountID, "conversation_id": conversation.ID, "inbox_id": conversation.InboxID, "assignee_id": conversation.AssigneeID, "team_id": conversation.TeamID, "assigned": conversation.AssigneeID != nil || conversation.TeamID != nil}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("rte.queue.get.v1", func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
			conversationID, err := uintFilter(query.Filter, "conversation_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is required"}
			}
			conversation, err := loadConversation(ctx, query, conversationID)
			if err != nil {
				return nil, err
			}
			queued := conversation.AssigneeID == nil && conversation.TeamID == nil && (conversation.Status == domain.ConversationStatusOpen || conversation.Status == domain.ConversationStatusPending)
			return map[string]any{"account_id": query.AccountID, "conversation_id": conversation.ID, "inbox_id": conversation.InboxID, "status": conversation.Status, "priority": conversation.Priority, "queued": queued, "last_activity_at": conversation.LastActivityAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("rte.capacity.get.v1", func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
			userID, err := uintFilter(query.Filter, "user_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "user_id is required"}
			}
			if err := tenClient.requireMember(ctx, query.AccountID, query.Actor, userID); err != nil {
				return nil, err
			}
			var policy domain.CapacityPolicy
			result := r.DB.Where("account_id = ? AND user_id = ?", query.AccountID, userID).First(&policy)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				result = r.DB.Where("account_id = ? AND user_id = 0", query.AccountID).First(&policy)
			}
			if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "capacity store is unavailable"}
			}
			limit := 10
			policyID := uint(0)
			if result.Error == nil {
				limit, policyID = policy.ConversationLimit, policy.ID
			}
			return map[string]any{"account_id": query.AccountID, "user_id": userID, "policy_id": policyID, "conversation_limit": limit}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("rte.candidates.list.v1", func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
			inboxID, err := uintFilter(query.Filter, "inbox_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inbox_id is required"}
			}
			inboxQuery := query
			inboxQuery.QueryType, inboxQuery.Filter, inboxQuery.Sort, inboxQuery.Page, inboxQuery.Fields, inboxQuery.Consistency = "chn.inbox.get.v1", map[string]any{"id": inboxID}, []string{"id"}, "1", []string{"id", "account_id"}, "strong"
			var inbox inboxView
			if err := chnClient.query(ctx, inboxQuery, &inbox); err != nil {
				return nil, err
			}
			membersQuery := query
			membersQuery.QueryType, membersQuery.Filter, membersQuery.Sort, membersQuery.Page, membersQuery.Fields, membersQuery.Consistency = "ten.members.list.v1", map[string]any{}, []string{"user_id"}, "1", []string{"user_id", "role", "availability"}, "strong"
			var members []memberView
			if err := tenClient.query(ctx, membersQuery, &members); err != nil {
				return nil, err
			}
			items := make([]map[string]any, 0, len(members))
			for _, member := range members {
				if member.AccountID != query.AccountID || member.UserID == 0 {
					continue
				}
				var policy domain.CapacityPolicy
				result := r.DB.Where("account_id = ? AND user_id = ?", query.AccountID, member.UserID).First(&policy)
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					result = r.DB.Where("account_id = ? AND user_id = 0", query.AccountID).First(&policy)
				}
				if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "capacity store is unavailable"}
				}
				limit := 10
				if result.Error == nil {
					limit = policy.ConversationLimit
				}
				active := int64(0)
				for _, status := range []string{domain.ConversationStatusOpen, domain.ConversationStatusPending} {
					listQuery := query
					listQuery.QueryType, listQuery.Filter, listQuery.Sort, listQuery.Page, listQuery.Fields, listQuery.Consistency = "con.conversations.list.v1", map[string]any{"inbox_id": inboxID, "assignee_id": member.UserID, "status": status}, []string{"id"}, "1", []string{"id"}, "strong"
					var page struct {
						Total int64 `json:"total"`
					}
					if err := conClient.query(ctx, listQuery, &page); err != nil {
						return nil, err
					}
					active += page.Total
				}
				available := member.Availability != "offline" && (limit <= 0 || active < int64(limit))
				items = append(items, map[string]any{"user_id": member.UserID, "role": member.Role, "availability": member.Availability, "active_conversations": active, "conversation_limit": limit, "available": available})
			}
			return map[string]any{"account_id": query.AccountID, "inbox_id": inboxID, "items": items}, nil
		}); err != nil {
			return err
		}
		return nil
	case "TKT":
		loadTicket := func(db *gorm.DB, accountID, ticketID uint) (*domain.Ticket, error) {
			var ticket domain.Ticket
			result := db.Where("account_id = ? AND id = ?", accountID, ticketID).First(&ticket)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "ticket was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "ticket store is unavailable"}
			}
			return &ticket, nil
		}
		if err := r.Contracts.RegisterCommand("tkt.ticket.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			title, _ := command.Input["title"].(string)
			title = strings.TrimSpace(title)
			if title == "" || len(title) > 255 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "title is required"}
			}
			priority, _ := command.Input["priority"].(string)
			priority = strings.ToLower(strings.TrimSpace(priority))
			if priority == "" {
				priority = domain.PriorityHigh
			}
			if priority != domain.PriorityLow && priority != domain.PriorityMedium && priority != domain.PriorityHigh && priority != domain.PriorityUrgent {
				return nil, &ContractFailure{Type: "validation_failed", Message: "priority is invalid"}
			}
			dependency := func(module string) (*moduleClient, error) {
				configured, ok := r.Config.Dependencies[module]
				if !ok {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: module + " validation is unavailable"}
				}
				return newModuleClient("TKT", configured, r.Config.ContractVersion, r.Config.MaxTimeout), nil
			}
			tenClient, err := dependency("TEN")
			if err != nil {
				return nil, err
			}
			creatorID, parseErr := strconv.ParseUint(command.Actor.ID, 10, 64)
			if parseErr != nil || creatorID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "numeric creator actor is required"}
			}
			if err := tenClient.requireMember(ctx, command.AccountID, command.Actor, uint(creatorID)); err != nil {
				return nil, err
			}
			assigneeID, err := optionalUint(command.Input["assignee_id"])
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "assignee_id is invalid"}
			}
			if assigneeID != nil {
				if err := tenClient.requireMember(ctx, command.AccountID, command.Actor, *assigneeID); err != nil {
					return nil, err
				}
			}
			contactID, err := optionalUint(command.Input["contact_id"])
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "contact_id is invalid"}
			}
			if contactID != nil {
				cusClient, depErr := dependency("CUS")
				if depErr != nil {
					return nil, depErr
				}
				query := foundation.QueryEnvelope{QueryType: "cus.contact.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": *contactID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id"}, Consistency: "strong", RequestedAt: command.RequestedAt}
				var contact contactView
				if err := cusClient.query(ctx, query, &contact); err != nil {
					return nil, err
				}
			}
			conversationID, err := optionalUint(command.Input["conversation_id"])
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "conversation_id is invalid"}
			}
			if conversationID != nil {
				conClient, depErr := dependency("CON")
				if depErr != nil {
					return nil, depErr
				}
				query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": *conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "contact_id"}, Consistency: "strong", RequestedAt: command.RequestedAt}
				var conversation conversationView
				if err := conClient.query(ctx, query, &conversation); err != nil {
					return nil, err
				}
				if contactID != nil && conversation.ContactID != *contactID {
					return nil, &ContractFailure{Type: "state_conflict", Message: "conversation contact does not match ticket contact"}
				}
			}
			description, _ := command.Input["description"].(string)
			number := "TKT-" + strings.ToUpper(strings.ReplaceAll(foundation.GenerateUUID(), "-", "")[:12])
			now := time.Now().UTC()
			creator := uint(creatorID)
			ticket := domain.Ticket{AccountID: command.AccountID, TicketNumber: number, Title: title, Description: description, Status: "open", Priority: priority, AssigneeID: assigneeID, ContactID: contactID, ConversationID: conversationID, CreatorID: &creator, SLAStatus: "normal", Version: 1, CreatedAt: now, UpdatedAt: now}
			tx := Transaction(ctx, r.DB)
			if err := tx.Create(&ticket).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket could not be created"}
			}
			if err := tx.Create(&domain.TicketActivity{AccountID: command.AccountID, TicketID: ticket.ID, UserID: &creator, Action: "created", Details: `{}`, CreatedAt: now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket activity could not be created"}
			}
			if err := tx.Create(&domain.TicketStatusHistory{AccountID: command.AccountID, TicketID: ticket.ID, FromStatus: "", ToStatus: "open", Reason: "created", UserID: &creator, CreatedAt: now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket status history could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "tkt.ticket.created.v1", "Ticket", strconv.FormatUint(uint64(ticket.ID), 10), []string{"created"}, map[string]any{"ticket_id": ticket.ID, "ticket_number": ticket.TicketNumber, "status": ticket.Status, "priority": ticket.Priority}); err != nil {
				return nil, err
			}
			return ticketFromModel(&ticket), nil
		}); err != nil {
			return err
		}
		transitionTicket := func(ctx context.Context, command foundation.CommandEnvelope, reopen bool) (any, error) {
			ticketID, failure := ticketCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			status, _ := command.Input["status"].(string)
			status = strings.ToLower(strings.TrimSpace(status))
			if reopen {
				status = "open"
			}
			if status != "open" && status != "pending" && status != "resolved" && status != "closed" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "status is invalid"}
			}
			configured, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN validation is unavailable"}
			}
			actorID, err := strconv.ParseUint(command.Actor.ID, 10, 64)
			if err != nil || actorID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "numeric actor is required"}
			}
			if err := newModuleClient("TKT", configured, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, uint(actorID)); err != nil {
				return nil, err
			}
			tx := Transaction(ctx, r.DB)
			ticket, err := loadTicket(tx, command.AccountID, ticketID)
			if err != nil {
				return nil, err
			}
			allowed := map[string]map[string]bool{"open": {"pending": true, "resolved": true, "closed": true}, "pending": {"open": true, "resolved": true, "closed": true}, "resolved": {"open": true, "closed": true}, "closed": {"open": true}}
			if reopen && ticket.Status != "resolved" && ticket.Status != "closed" {
				return nil, &ContractFailure{Type: "state_conflict", Message: "only resolved or closed tickets can be reopened"}
			}
			if ticket.Status == status || !allowed[ticket.Status][status] {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket status transition is not allowed"}
			}
			fromStatus := ticket.Status
			expectedVersion := ticket.Version
			if command.Target.ExpectedVersion > 0 && uint64(ticket.Version) != command.Target.ExpectedVersion {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket version changed"}
			}
			now := time.Now().UTC()
			reason, _ := command.Input["reason"].(string)
			updates := map[string]any{"status": status, "status_reason": reason, "version": gorm.Expr("version + 1"), "updated_at": now}
			if status == "resolved" {
				updates["resolved_at"] = now
			} else if status == "closed" {
				updates["closed_at"] = now
			} else if status == "open" {
				updates["resolved_at"], updates["closed_at"] = nil, nil
			}
			result := tx.Model(&domain.Ticket{}).Where("account_id = ? AND id = ? AND version = ?", command.AccountID, ticketID, expectedVersion).Updates(updates)
			if result.Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket status could not be changed"}
			}
			if result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket version changed"}
			}
			userID := uint(actorID)
			if err := tx.Create(&domain.TicketStatusHistory{AccountID: command.AccountID, TicketID: ticketID, FromStatus: fromStatus, ToStatus: status, Reason: reason, UserID: &userID, CreatedAt: now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket status history could not be created"}
			}
			details, _ := json.Marshal(map[string]any{"from_status": fromStatus, "to_status": status})
			if err := tx.Create(&domain.TicketActivity{AccountID: command.AccountID, TicketID: ticketID, UserID: &userID, Action: "status_changed", Details: string(details), CreatedAt: now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket activity could not be created"}
			}
			if err := tx.Where("account_id = ? AND id = ?", command.AccountID, ticketID).First(ticket).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "ticket store is unavailable"}
			}
			if err := r.appendBusinessEvent(ctx, command, "tkt.ticket_status.changed.v1", "Ticket", command.Target.ID, []string{"status"}, map[string]any{"ticket_id": ticketID, "from_status": fromStatus, "to_status": status, "version": ticket.Version}); err != nil {
				return nil, err
			}
			return ticketFromModel(ticket), nil
		}
		if err := r.Contracts.RegisterCommand("tkt.status.change.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			return transitionTicket(ctx, command, false)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("tkt.ticket.reopen.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			return transitionTicket(ctx, command, true)
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("tkt.comment.add.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			ticketID, failure := ticketCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			content, _ := command.Input["content"].(string)
			content = strings.TrimSpace(content)
			if content == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "comment content is required"}
			}
			actorID, err := strconv.ParseUint(command.Actor.ID, 10, 64)
			if err != nil || actorID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "numeric actor is required"}
			}
			configured, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN validation is unavailable"}
			}
			if err := newModuleClient("TKT", configured, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, uint(actorID)); err != nil {
				return nil, err
			}
			tx := Transaction(ctx, r.DB)
			if _, err := loadTicket(tx, command.AccountID, ticketID); err != nil {
				return nil, err
			}
			isPrivate := true
			if value, exists := command.Input["is_private"].(bool); exists {
				isPrivate = value
			}
			userID := uint(actorID)
			now := time.Now().UTC()
			comment := domain.TicketComment{AccountID: command.AccountID, TicketID: ticketID, UserID: &userID, Content: content, IsPrivate: isPrivate, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&comment).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket comment could not be created"}
			}
			if err := tx.Create(&domain.TicketActivity{AccountID: command.AccountID, TicketID: ticketID, UserID: &userID, Action: "commented", Details: `{}`, CreatedAt: now}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket activity could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "tkt.ticket.commented.v1", "Ticket", command.Target.ID, []string{"commented"}, map[string]any{"ticket_id": ticketID, "comment_id": comment.ID, "is_private": comment.IsPrivate}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "ticket_id": ticketID, "comment_id": comment.ID, "is_private": comment.IsPrivate, "created_at": comment.CreatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("tkt.account_tickets.anonymize.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "account target is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			if err := tx.Model(&domain.Ticket{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"title": "Anonymized ticket", "description": "", "assigned_group": "", "assignee_id": nil, "contact_id": nil, "conversation_id": nil, "creator_id": nil, "custom_attributes": "", "waiting_reason": "", "status_reason": "", "updated_at": time.Now().UTC()}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "tickets could not be anonymized"}
			}
			if err := tx.Model(&domain.TicketComment{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"user_id": nil, "contact_id": nil, "content": "[redacted]", "updated_at": time.Now().UTC()}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket comments could not be anonymized"}
			}
			if err := tx.Model(&domain.TicketActivity{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"user_id": nil, "details": `{}`}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket activities could not be anonymized"}
			}
			if err := tx.Model(&domain.TicketStatusHistory{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"user_id": nil, "reason": "", "waiting_reason": ""}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket histories could not be anonymized"}
			}
			if err := tx.Model(&domain.TicketAttachment{}).Where("account_id = ?", command.AccountID).Updates(map[string]any{"file_name": "redacted", "file_type": "", "data_url": ""}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket attachments could not be anonymized"}
			}
			if err := tx.Where("account_id = ?", command.AccountID).Delete(&domain.TicketWatcher{}).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "ticket watchers could not be removed"}
			}
			return map[string]any{"account_id": command.AccountID, "anonymized": true}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("tkt.ticket.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			ticketID, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "ticket id is required"}
			}
			ticket, err := loadTicket(r.DB, query.AccountID, ticketID)
			if err != nil {
				return nil, err
			}
			return ticketFromModel(ticket), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("tkt.workflow.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			ticketID, err := uintFilter(query.Filter, "ticket_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "ticket_id is required"}
			}
			ticket, err := loadTicket(r.DB, query.AccountID, ticketID)
			if err != nil {
				return nil, err
			}
			transitions := map[string][]string{"open": {"pending", "resolved", "closed"}, "pending": {"open", "resolved", "closed"}, "resolved": {"open", "closed"}, "closed": {"open"}}
			return map[string]any{"account_id": query.AccountID, "ticket_id": ticket.ID, "status": ticket.Status, "version": ticket.Version, "allowed_transitions": transitions[ticket.Status]}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("tkt.history.list.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			ticketID, err := uintFilter(query.Filter, "ticket_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "ticket_id is required"}
			}
			if _, err := loadTicket(r.DB, query.AccountID, ticketID); err != nil {
				return nil, err
			}
			var statuses []domain.TicketStatusHistory
			if err := r.DB.Where("account_id = ? AND ticket_id = ?", query.AccountID, ticketID).Order("id ASC").Find(&statuses).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "ticket history store is unavailable"}
			}
			var activities []domain.TicketActivity
			if err := r.DB.Where("account_id = ? AND ticket_id = ?", query.AccountID, ticketID).Order("id ASC").Find(&activities).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "ticket activity store is unavailable"}
			}
			return map[string]any{"account_id": query.AccountID, "ticket_id": ticketID, "status_history": statuses, "activities": activities}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("tkt.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			counts := map[string]int64{}
			for name, statement := range map[string]*gorm.DB{
				"ticket_count":     r.DB.Model(&domain.Ticket{}).Where("account_id = ?", query.AccountID),
				"comment_count":    r.DB.Model(&domain.TicketComment{}).Where("account_id = ?", query.AccountID),
				"activity_count":   r.DB.Model(&domain.TicketActivity{}).Where("account_id = ?", query.AccountID),
				"history_count":    r.DB.Model(&domain.TicketStatusHistory{}).Where("account_id = ?", query.AccountID),
				"attachment_count": r.DB.Model(&domain.TicketAttachment{}).Where("account_id = ?", query.AccountID),
				"watcher_count":    r.DB.Model(&domain.TicketWatcher{}).Where("account_id = ?", query.AccountID),
			} {
				var count int64
				if err := statement.Count(&count).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "ticket deletion status is unavailable"}
				}
				counts[name] = count
			}
			piiCounts := map[string]int64{}
			for name, statement := range map[string]*gorm.DB{
				"ticket_pii_count":     r.DB.Model(&domain.Ticket{}).Where("account_id = ? AND (title <> ? OR description <> '' OR assigned_group <> '' OR assignee_id IS NOT NULL OR contact_id IS NOT NULL OR conversation_id IS NOT NULL OR creator_id IS NOT NULL OR custom_attributes <> '' OR waiting_reason <> '' OR status_reason <> '')", query.AccountID, "Anonymized ticket"),
				"comment_pii_count":    r.DB.Model(&domain.TicketComment{}).Where("account_id = ? AND (user_id IS NOT NULL OR contact_id IS NOT NULL OR content <> ?)", query.AccountID, "[redacted]"),
				"activity_pii_count":   r.DB.Model(&domain.TicketActivity{}).Where("account_id = ? AND (user_id IS NOT NULL OR details <> ?)", query.AccountID, `{}`),
				"history_pii_count":    r.DB.Model(&domain.TicketStatusHistory{}).Where("account_id = ? AND (user_id IS NOT NULL OR reason <> '' OR waiting_reason <> '')", query.AccountID),
				"attachment_pii_count": r.DB.Model(&domain.TicketAttachment{}).Where("account_id = ? AND (file_name <> ? OR file_type <> '' OR data_url <> '')", query.AccountID, "redacted"),
				"watcher_pii_count":    r.DB.Model(&domain.TicketWatcher{}).Where("account_id = ?", query.AccountID),
			} {
				var count int64
				if err := statement.Count(&count).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "ticket deletion status is unavailable"}
				}
				piiCounts[name] = count
			}
			piiCount := piiCounts["ticket_pii_count"] + piiCounts["comment_pii_count"] + piiCounts["activity_pii_count"] + piiCounts["history_pii_count"] + piiCounts["attachment_pii_count"] + piiCounts["watcher_pii_count"]
			return map[string]any{"account_id": query.AccountID, "ticket_count": counts["ticket_count"], "comment_count": counts["comment_count"], "activity_count": counts["activity_count"], "history_count": counts["history_count"], "attachment_count": counts["attachment_count"], "watcher_count": counts["watcher_count"], "pii_count": piiCount, "ready": piiCount == 0}, nil
		}); err != nil {
			return err
		}
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
	case "QLT":
		loadReview := func(accountID, taskID uint) (*domain.QATask, error) {
			var task domain.QATask
			result := r.DB.Where("account_id = ? AND id = ?", accountID, taskID).First(&task)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "quality review was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "quality review store is unavailable"}
			}
			return &task, nil
		}
		loadConversation := func(ctx context.Context, command foundation.CommandEnvelope) (*conversationView, error) {
			conversationID, failure := conversationCommandTarget(command)
			if failure != nil {
				return nil, failure
			}
			configured, ok := r.Config.Dependencies["CON"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "CON validation is unavailable"}
			}
			query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": conversationID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "account_id", "assignee_id"}, Consistency: "strong", RequestedAt: command.RequestedAt}
			var conversation conversationView
			if err := newModuleClient("QLT", configured, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
				return nil, err
			}
			return &conversation, nil
		}
		if err := r.Contracts.RegisterCommand("qlt.survey.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversation, err := loadConversation(ctx, command)
			if err != nil {
				return nil, err
			}
			tx := Transaction(ctx, r.DB)
			var survey domain.CSATSurvey
			result := tx.Where("account_id = ? AND conversation_id = ?", command.AccountID, conversation.ID).First(&survey)
			if result.Error == nil {
				return &csatSurveyView{ID: survey.ID, AccountID: survey.AccountID, ConversationID: survey.ConversationID, Rating: survey.Rating, FeedbackText: survey.FeedbackText, AssignedAgentID: survey.AssignedAgentID, ReviewStatus: survey.ReviewStatus, ReviewerID: survey.ReviewerID, ReviewedAt: survey.ReviewedAt, CreatedAt: survey.CreatedAt, UpdatedAt: survey.UpdatedAt}, nil
			}
			if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "CSAT store is unavailable"}
			}
			now := time.Now().UTC()
			survey = domain.CSATSurvey{AccountID: command.AccountID, ConversationID: conversation.ID, Rating: 0, AssignedAgentID: conversation.AssigneeID, ReviewStatus: "pending", CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&survey).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "CSAT survey could not be created"}
			}
			return &csatSurveyView{ID: survey.ID, AccountID: survey.AccountID, ConversationID: survey.ConversationID, Rating: survey.Rating, AssignedAgentID: survey.AssignedAgentID, ReviewStatus: survey.ReviewStatus, CreatedAt: survey.CreatedAt, UpdatedAt: survey.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("qlt.csat.submit.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			conversation, err := loadConversation(ctx, command)
			if err != nil {
				return nil, err
			}
			rating, ratingErr := uintFilter(command.Input, "rating")
			if ratingErr != nil || rating < 1 || rating > 5 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "rating must be between 1 and 5"}
			}
			feedback, _ := command.Input["feedback_text"].(string)
			feedback = strings.TrimSpace(feedback)
			if len(feedback) > 4000 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "feedback_text is too long"}
			}
			tx := Transaction(ctx, r.DB)
			var survey domain.CSATSurvey
			result := tx.Where("account_id = ? AND conversation_id = ?", command.AccountID, conversation.ID).First(&survey)
			now := time.Now().UTC()
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				survey = domain.CSATSurvey{AccountID: command.AccountID, ConversationID: conversation.ID, Rating: int(rating), FeedbackText: feedback, AssignedAgentID: conversation.AssigneeID, ReviewStatus: "pending", CreatedAt: now, UpdatedAt: now}
				if err := tx.Create(&survey).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "CSAT response could not be created"}
				}
			} else if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "CSAT store is unavailable"}
			} else {
				updates := map[string]any{"rating": int(rating), "feedback_text": feedback, "review_status": "pending", "reviewer_id": nil, "review_notes": "", "reviewed_at": nil, "updated_at": now}
				if survey.AssignedAgentID == nil {
					updates["assigned_agent_id"] = conversation.AssigneeID
				}
				if err := tx.Model(&survey).Updates(updates).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "CSAT response could not be updated"}
				}
				if err := tx.First(&survey, survey.ID).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "CSAT store is unavailable"}
				}
			}
			if err := r.appendBusinessEvent(ctx, command, "qlt.csat.received.v1", "CSATSurvey", strconv.FormatUint(uint64(survey.ID), 10), []string{"rating", "feedback_text", "review_status"}, map[string]any{"survey_id": survey.ID, "conversation_id": survey.ConversationID, "rating": survey.Rating, "review_status": survey.ReviewStatus}); err != nil {
				return nil, err
			}
			return &csatSurveyView{ID: survey.ID, AccountID: survey.AccountID, ConversationID: survey.ConversationID, Rating: survey.Rating, FeedbackText: survey.FeedbackText, AssignedAgentID: survey.AssignedAgentID, ReviewStatus: survey.ReviewStatus, CreatedAt: survey.CreatedAt, UpdatedAt: survey.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("qlt.review.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			targetType, _ := command.Input["target_type"].(string)
			targetType = strings.ToLower(strings.TrimSpace(targetType))
			if targetType != "conversation" && targetType != "ticket" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "target_type must be conversation or ticket"}
			}
			targetID, err := uintFilter(command.Input, "target_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "target_id is required"}
			}
			scorecardID, err := uintFilter(command.Input, "scorecard_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "scorecard_id is required"}
			}
			var scorecard domain.QAScorecard
			result := r.DB.Where("account_id = ? AND id = ? AND status = ?", command.AccountID, scorecardID, "active").First(&scorecard)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "active scorecard was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "scorecard store is unavailable"}
			}
			inspectorID, err := optionalUint(command.Input["inspector_id"])
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "inspector_id is invalid"}
			}
			if inspectorID == nil {
				parsed, parseErr := strconv.ParseUint(command.Actor.ID, 10, 64)
				if parseErr != nil || parsed == 0 {
					return nil, &ContractFailure{Type: "validation_failed", Message: "numeric inspector actor is required"}
				}
				value := uint(parsed)
				inspectorID = &value
			}
			tenConfig, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN validation is unavailable"}
			}
			if err := newModuleClient("QLT", tenConfig, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, *inspectorID); err != nil {
				return nil, err
			}
			var agentID *uint
			var targetRef string
			dependency, ok := r.Config.Dependencies[strings.ToUpper(map[string]string{"conversation": "CON", "ticket": "TKT"}[targetType])]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: strings.ToUpper(targetType) + " validation is unavailable"}
			}
			if targetType == "conversation" {
				query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": targetID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "display_id", "assignee_id"}, Consistency: "strong", RequestedAt: command.RequestedAt}
				var conversation conversationView
				if err := newModuleClient("QLT", dependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &conversation); err != nil {
					return nil, err
				}
				agentID = conversation.AssigneeID
				targetRef = fmt.Sprintf("Conversation #%d", conversation.DisplayID)
			} else {
				query := foundation.QueryEnvelope{QueryType: "tkt.ticket.get.v1", AccountID: command.AccountID, Actor: command.Actor, Filter: map[string]any{"id": targetID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "ticket_number", "assignee_id"}, Consistency: "strong", RequestedAt: command.RequestedAt}
				var ticket ticketView
				if err := newModuleClient("QLT", dependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &ticket); err != nil {
					return nil, err
				}
				agentID = ticket.AssigneeID
				targetRef = ticket.TicketNumber
			}
			dueAt := time.Now().UTC().Add(24 * time.Hour)
			if raw, exists := command.Input["due_at"].(string); exists && strings.TrimSpace(raw) != "" {
				parsed, parseErr := time.Parse(time.RFC3339, raw)
				if parseErr != nil || !parsed.After(time.Now().UTC()) {
					return nil, &ContractFailure{Type: "validation_failed", Message: "due_at must be a future RFC3339 timestamp"}
				}
				dueAt = parsed.UTC()
			}
			now := time.Now().UTC()
			task := domain.QATask{AccountID: command.AccountID, TaskNumber: "QA-" + strings.ToUpper(strings.ReplaceAll(foundation.GenerateUUID(), "-", "")[:12]), TargetType: targetType, TargetID: targetID, TargetRef: targetRef, ScorecardID: scorecard.ID, ScorecardName: scorecard.Name, InspectorID: inspectorID, AgentID: agentID, DueAt: &dueAt, Status: "pending", Result: "待评分", SecondReviewStatus: "none", CreatedAt: now, UpdatedAt: now}
			if err := Transaction(ctx, r.DB).Create(&task).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "quality review could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "qlt.quality_review.changed.v1", "QATask", strconv.FormatUint(uint64(task.ID), 10), []string{"created"}, map[string]any{"task_id": task.ID, "task_number": task.TaskNumber, "target_type": task.TargetType, "target_id": task.TargetID, "status": task.Status}); err != nil {
				return nil, err
			}
			return qualityReviewFromModel(&task), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("qlt.appeal.review.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "appeal" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "appeal target is required"}
			}
			parsedID, parseErr := strconv.ParseUint(command.Target.ID, 10, 64)
			if parseErr != nil || parsedID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "appeal target is invalid"}
			}
			reviewer, parseErr := strconv.ParseUint(command.Actor.ID, 10, 64)
			if parseErr != nil || reviewer == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "numeric reviewer actor is required"}
			}
			tenConfig, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN validation is unavailable"}
			}
			if err := newModuleClient("QLT", tenConfig, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, uint(reviewer)); err != nil {
				return nil, err
			}
			tx := Transaction(ctx, r.DB)
			var appeal domain.QAAppeal
			result := tx.Where("account_id = ? AND id = ?", command.AccountID, uint(parsedID)).First(&appeal)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "quality appeal was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "quality appeal store is unavailable"}
			}
			if appeal.Status == "upheld" || appeal.Status == "adjusted" || appeal.Status == "rejected" {
				return nil, &ContractFailure{Type: "state_conflict", Message: "quality appeal is already closed"}
			}
			task, err := loadReview(command.AccountID, appeal.TaskID)
			if err != nil {
				return nil, err
			}
			if task.InspectorID != nil && *task.InspectorID == uint(reviewer) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "original inspector cannot review the appeal"}
			}
			action, _ := command.Input["action"].(string)
			action = strings.ToLower(strings.TrimSpace(action))
			if action != "adjusted" && action != "upheld" && action != "rejected" && action != "need_evidence" && action != "under_review" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "appeal review action is invalid"}
			}
			adjustedScore, scoreErr := optionalUint(command.Input["adjusted_score"])
			if action == "adjusted" && (scoreErr != nil || adjustedScore == nil || *adjustedScore > 100) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "adjusted_score between 0 and 100 is required"}
			}
			comments, _ := command.Input["comments"].(string)
			comments = strings.TrimSpace(comments)
			if len(comments) > 4000 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "comments is too long"}
			}
			now := time.Now().UTC()
			reviewerID := uint(reviewer)
			appeal.ReviewerID, appeal.ReviewComments, appeal.ReviewedAt, appeal.UpdatedAt = &reviewerID, comments, &now, now
			appeal.Status = action
			appeal.EvidenceFrozen = action != "need_evidence"
			if action == "adjusted" {
				score := int(*adjustedScore)
				appeal.AdjustedScore = &score
				appeal.RevokeFatal, _ = command.Input["revoke_fatal"].(bool)
				task.TotalScore = score
				if appeal.RevokeFatal || score >= 85 {
					task.HasFatalError = false
				}
			}
			if action == "upheld" || action == "rejected" {
				task.TotalScore, task.HasFatalError = appeal.OriginalScore, appeal.OriginalFatal
			}
			if action == "adjusted" || action == "upheld" || action == "rejected" {
				if task.HasFatalError {
					task.Result, task.Status = "致命错误", "rectifying"
				} else if task.TotalScore >= 95 {
					task.Result, task.Status = "优秀", "completed"
				} else if task.TotalScore >= 85 {
					task.Result, task.Status = "合格", "completed"
				} else {
					task.Result, task.Status = "需改进", "rectifying"
				}
			}
			task.UpdatedAt = now
			if err := tx.Save(task).Error; err != nil || tx.Save(&appeal).Error != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "quality appeal could not be reviewed"}
			}
			activity := domain.QAAppealActivity{AccountID: command.AccountID, AppealID: appeal.ID, ActorID: reviewerID, Action: appeal.Status, Description: comments, CreatedAt: now}
			if err := tx.Create(&activity).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "quality appeal activity could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "qlt.appeal.changed.v1", "QAAppeal", command.Target.ID, []string{"status", "reviewer_id"}, map[string]any{"appeal_id": appeal.ID, "task_id": appeal.TaskID, "status": appeal.Status, "adjusted_score": appeal.AdjustedScore, "revoke_fatal": appeal.RevokeFatal}); err != nil {
				return nil, err
			}
			return map[string]any{"id": appeal.ID, "account_id": appeal.AccountID, "task_id": appeal.TaskID, "appeal_number": appeal.AppealNumber, "status": appeal.Status, "reviewer_id": appeal.ReviewerID, "adjusted_score": appeal.AdjustedScore, "revoke_fatal": appeal.RevokeFatal, "reviewed_at": appeal.ReviewedAt, "task": qualityReviewFromModel(task)}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("qlt.csat.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			var survey domain.CSATSurvey
			statement := r.DB.Where("account_id = ?", query.AccountID)
			if id, err := uintFilter(query.Filter, "id"); err == nil {
				statement = statement.Where("id = ?", id)
			} else if conversationID, conversationErr := uintFilter(query.Filter, "conversation_id"); conversationErr == nil {
				statement = statement.Where("conversation_id = ?", conversationID).Order("id DESC")
			} else {
				return nil, &ContractFailure{Type: "validation_failed", Message: "id or conversation_id is required"}
			}
			result := statement.First(&survey)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "CSAT survey was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "CSAT store is unavailable"}
			}
			return &csatSurveyView{ID: survey.ID, AccountID: survey.AccountID, ConversationID: survey.ConversationID, Rating: survey.Rating, FeedbackText: survey.FeedbackText, AssignedAgentID: survey.AssignedAgentID, ReviewStatus: survey.ReviewStatus, ReviewerID: survey.ReviewerID, ReviewedAt: survey.ReviewedAt, CreatedAt: survey.CreatedAt, UpdatedAt: survey.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("qlt.review.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			taskID, err := uintFilter(query.Filter, "id")
			if err != nil {
				taskID, err = uintFilter(query.Filter, "task_id")
			}
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "id or task_id is required"}
			}
			task, err := loadReview(query.AccountID, taskID)
			if err != nil {
				return nil, err
			}
			return qualityReviewFromModel(task), nil
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterQuery("qlt.score.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			taskID, err := uintFilter(query.Filter, "task_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "task_id is required"}
			}
			task, err := loadReview(query.AccountID, taskID)
			if err != nil {
				return nil, err
			}
			var scores []domain.QAEvaluationScore
			if err := r.DB.Where("task_id = ?", task.ID).Order("id ASC").Find(&scores).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "quality score store is unavailable"}
			}
			return map[string]any{"account_id": query.AccountID, "task_id": task.ID, "total_score": task.TotalScore, "result": task.Result, "has_fatal_error": task.HasFatalError, "items": scores}, nil
		})
	case "KB":
		loadPortal := func(accountID, portalID uint) (*domain.Portal, error) {
			var portal domain.Portal
			result := r.DB.Where("account_id = ? AND id = ?", accountID, portalID).First(&portal)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "portal was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "portal store is unavailable"}
			}
			return &portal, nil
		}
		loadArticle := func(accountID, articleID uint) (*domain.Article, error) {
			var article domain.Article
			result := r.DB.Where("account_id = ? AND id = ?", accountID, articleID).First(&article)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "article was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "article store is unavailable"}
			}
			return &article, nil
		}
		if err := r.Contracts.RegisterCommand("kb.article.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			portalID, err := uintFilter(command.Input, "portal_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "portal_id is required"}
			}
			categoryID, err := uintFilter(command.Input, "category_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "category_id is required"}
			}
			portal, err := loadPortal(command.AccountID, portalID)
			if err != nil {
				return nil, err
			}
			var category domain.Category
			result := r.DB.Where("account_id = ? AND portal_id = ? AND id = ?", command.AccountID, portal.ID, categoryID).First(&category)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "category was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "category store is unavailable"}
			}
			author, parseErr := strconv.ParseUint(command.Actor.ID, 10, 64)
			if parseErr != nil || author == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "numeric author actor is required"}
			}
			tenConfig, ok := r.Config.Dependencies["TEN"]
			if !ok {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN validation is unavailable"}
			}
			if err := newModuleClient("KB", tenConfig, r.Config.ContractVersion, r.Config.MaxTimeout).requireMember(ctx, command.AccountID, command.Actor, uint(author)); err != nil {
				return nil, err
			}
			title, _ := command.Input["title"].(string)
			slug, _ := command.Input["slug"].(string)
			content, _ := command.Input["content"].(string)
			title, slug = strings.TrimSpace(title), strings.ToLower(strings.TrimSpace(slug))
			if title == "" || len(title) > 255 || slug == "" || len(slug) > 255 || content == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "title, slug and content are required"}
			}
			var duplicate int64
			if err := r.DB.Model(&domain.Article{}).Where("account_id = ? AND portal_id = ? AND slug = ?", command.AccountID, portal.ID, slug).Count(&duplicate).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "article store is unavailable"}
			}
			if duplicate > 0 {
				return nil, &ContractFailure{Type: "state_conflict", Message: "article slug already exists"}
			}
			now := time.Now().UTC()
			article := domain.Article{AccountID: command.AccountID, PortalID: portal.ID, CategoryID: category.ID, AuthorID: uint(author), Title: title, Slug: slug, Content: content, Status: "draft", CreatedAt: now, UpdatedAt: now}
			if err := Transaction(ctx, r.DB).Create(&article).Error; err != nil {
				return nil, &ContractFailure{Type: "state_conflict", Message: "article could not be created"}
			}
			if err := r.appendBusinessEvent(ctx, command, "kb.knowledge.changed.v1", "Article", strconv.FormatUint(uint64(article.ID), 10), []string{"created"}, map[string]any{"article_id": article.ID, "portal_id": article.PortalID, "category_id": article.CategoryID, "status": article.Status}); err != nil {
				return nil, err
			}
			return articleFromModel(&article), nil
		}); err != nil {
			return err
		}
		changeArticleStatus := func(ctx context.Context, command foundation.CommandEnvelope, status, eventType string) (any, error) {
			if command.Target.Type != "article" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "article target is required"}
			}
			parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
			if err != nil || parsed == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "article target is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			article, err := loadArticle(command.AccountID, uint(parsed))
			if err != nil {
				return nil, err
			}
			if article.Status == status {
				return nil, &ContractFailure{Type: "state_conflict", Message: "article already has requested status"}
			}
			if status == "published" && article.Status != "draft" && article.Status != "archived" {
				return nil, &ContractFailure{Type: "state_conflict", Message: "article cannot be published"}
			}
			now := time.Now().UTC()
			result := tx.Model(&domain.Article{}).Where("account_id = ? AND id = ? AND status = ?", command.AccountID, article.ID, article.Status).Updates(map[string]any{"status": status, "updated_at": now})
			if result.Error != nil || result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "state_conflict", Message: "article status changed concurrently"}
			}
			article.Status, article.UpdatedAt = status, now
			if err := r.appendBusinessEvent(ctx, command, eventType, "Article", command.Target.ID, []string{"status"}, map[string]any{"article_id": article.ID, "portal_id": article.PortalID, "status": article.Status}); err != nil {
				return nil, err
			}
			return articleFromModel(article), nil
		}
		if err := r.Contracts.RegisterCommand("kb.content.publish.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			return changeArticleStatus(ctx, command, "published", "kb.knowledge.published.v1")
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("kb.content.archive.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			return changeArticleStatus(ctx, command, "archived", "kb.knowledge.archived.v1")
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("kb.document.delete.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "article" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "article target is required"}
			}
			articleID, parseErr := strconv.ParseUint(command.Target.ID, 10, 64)
			if parseErr != nil || articleID == 0 {
				return nil, &ContractFailure{Type: "validation_failed", Message: "article target is invalid"}
			}
			tx := Transaction(ctx, r.DB)
			var article domain.Article
			result := tx.Where("account_id = ? AND id = ?", command.AccountID, uint(articleID)).First(&article)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "article was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "article store is unavailable"}
			}
			if result := tx.Where("account_id = ? AND id = ?", command.AccountID, article.ID).Delete(&domain.Article{}); result.Error != nil || result.RowsAffected != 1 {
				return nil, &ContractFailure{Type: "state_conflict", Message: "article could not be deleted"}
			}
			if err := r.appendBusinessEvent(ctx, command, "kb.knowledge.deleted.v1", "Article", command.Target.ID, []string{"deleted"}, map[string]any{"article_id": article.ID, "portal_id": article.PortalID, "category_id": article.CategoryID}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "article_id": article.ID, "deleted": true}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("kb.portal.manage.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			action, _ := command.Input["action"].(string)
			action = strings.ToLower(strings.TrimSpace(action))
			name, _ := command.Input["name"].(string)
			slug, _ := command.Input["slug"].(string)
			name, slug = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(slug))
			tx := Transaction(ctx, r.DB)
			var portal domain.Portal
			switch action {
			case "create":
				if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) || name == "" || slug == "" || len(name) > 255 || len(slug) > 128 {
					return nil, &ContractFailure{Type: "validation_failed", Message: "account target, name and slug are required"}
				}
				portal = domain.Portal{AccountID: command.AccountID, Name: name, Slug: slug, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
				if err := tx.Create(&portal).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "portal could not be created"}
				}
			case "update":
				if command.Target.Type != "portal" {
					return nil, &ContractFailure{Type: "validation_failed", Message: "portal target is required"}
				}
				portalID, parseErr := strconv.ParseUint(command.Target.ID, 10, 64)
				if parseErr != nil || portalID == 0 {
					return nil, &ContractFailure{Type: "validation_failed", Message: "portal target is invalid"}
				}
				result := tx.Where("account_id = ? AND id = ?", command.AccountID, uint(portalID)).First(&portal)
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					return nil, &ContractFailure{Type: "resource_not_found", Message: "portal was not found"}
				}
				if result.Error != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "portal store is unavailable"}
				}
				updates := map[string]any{"updated_at": time.Now().UTC()}
				if name != "" && len(name) <= 255 {
					updates["name"] = name
				}
				if slug != "" && len(slug) <= 128 {
					updates["slug"] = slug
				}
				for _, field := range []string{"custom_domain", "color", "header_text"} {
					if value, ok := command.Input[field].(string); ok {
						updates[field] = strings.TrimSpace(value)
					}
				}
				if len(updates) == 1 {
					return nil, &ContractFailure{Type: "validation_failed", Message: "portal updates are required"}
				}
				if err := tx.Model(&domain.Portal{}).Where("account_id = ? AND id = ?", command.AccountID, portal.ID).Updates(updates).Error; err != nil {
					return nil, &ContractFailure{Type: "state_conflict", Message: "portal could not be updated"}
				}
				if err := tx.Where("account_id = ? AND id = ?", command.AccountID, portal.ID).First(&portal).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "portal store is unavailable"}
				}
			default:
				return nil, &ContractFailure{Type: "validation_failed", Message: "portal action must be create or update"}
			}
			if err := r.appendBusinessEvent(ctx, command, "kb.portal.changed.v1", "Portal", strconv.FormatUint(uint64(portal.ID), 10), []string{action}, map[string]any{"portal_id": portal.ID, "action": action, "slug": portal.Slug}); err != nil {
				return nil, err
			}
			return portalFromModel(&portal), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterCommand("kb.account_content.purge.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
			if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
				return nil, &ContractFailure{Type: "validation_failed", Message: "matching account target is required"}
			}
			tx := Transaction(ctx, r.DB)
			counts := map[string]int64{}
			for _, item := range []struct {
				name  string
				model any
			}{{"articles", &domain.Article{}}, {"categories", &domain.Category{}}, {"portals", &domain.Portal{}}} {
				result := tx.Where("account_id = ?", command.AccountID).Delete(item.model)
				if result.Error != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "knowledge content could not be purged"}
				}
				counts[item.name] = result.RowsAffected
			}
			if err := r.appendBusinessEvent(ctx, command, "kb.knowledge.deleted.v1", "Account", command.Target.ID, []string{"purged"}, map[string]any{"article_count": counts["articles"], "category_count": counts["categories"], "portal_count": counts["portals"]}); err != nil {
				return nil, err
			}
			return map[string]any{"account_id": command.AccountID, "article_count": counts["articles"], "category_count": counts["categories"], "portal_count": counts["portals"], "ready": true}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("kb.content.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "article id is required"}
			}
			article, err := loadArticle(query.AccountID, id)
			if err != nil {
				return nil, err
			}
			return articleFromModel(article), nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("kb.version.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, err := uintFilter(query.Filter, "article_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "article_id is required"}
			}
			article, err := loadArticle(query.AccountID, id)
			if err != nil {
				return nil, err
			}
			hash := sha256.Sum256([]byte(article.Title + "\x00" + article.Content + "\x00" + article.Status))
			return map[string]any{"account_id": article.AccountID, "article_id": article.ID, "status": article.Status, "content_version": hex.EncodeToString(hash[:]), "updated_at": article.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("kb.public_view.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			portalSlug, _ := query.Filter["portal_slug"].(string)
			articleSlug, _ := query.Filter["article_slug"].(string)
			portalSlug, articleSlug = strings.TrimSpace(portalSlug), strings.TrimSpace(articleSlug)
			if portalSlug == "" || articleSlug == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "portal_slug and article_slug are required"}
			}
			var portal domain.Portal
			if result := r.DB.Where("account_id = ? AND slug = ?", query.AccountID, portalSlug).First(&portal); errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "public article was not found"}
			} else if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "portal store is unavailable"}
			}
			var article domain.Article
			if result := r.DB.Where("account_id = ? AND portal_id = ? AND slug = ? AND status = ?", query.AccountID, portal.ID, articleSlug, "published").First(&article); errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "public article was not found"}
			} else if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "article store is unavailable"}
			}
			return map[string]any{"portal": portalFromModel(&portal), "article": articleFromModel(&article)}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("kb.portal.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			if id, err := uintFilter(query.Filter, "id"); err == nil {
				portal, loadErr := loadPortal(query.AccountID, id)
				if loadErr != nil {
					return nil, loadErr
				}
				return portalFromModel(portal), nil
			}
			slug, _ := query.Filter["slug"].(string)
			slug = strings.TrimSpace(slug)
			if slug == "" {
				return nil, &ContractFailure{Type: "validation_failed", Message: "portal id or slug is required"}
			}
			var portal domain.Portal
			result := r.DB.Where("account_id = ? AND slug = ?", query.AccountID, slug).First(&portal)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "portal was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "portal store is unavailable"}
			}
			return portalFromModel(&portal), nil
		}); err != nil {
			return err
		}
		return r.Contracts.RegisterQuery("kb.account_deletion_status.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			counts := map[string]int64{}
			for name, model := range map[string]any{"portal_count": &domain.Portal{}, "category_count": &domain.Category{}, "article_count": &domain.Article{}} {
				var count int64
				if err := r.DB.Model(model).Where("account_id = ?", query.AccountID).Count(&count).Error; err != nil {
					return nil, &ContractFailure{Type: "dependency_unavailable", Message: "knowledge deletion status is unavailable"}
				}
				counts[name] = count
			}
			return map[string]any{"account_id": query.AccountID, "portal_count": counts["portal_count"], "category_count": counts["category_count"], "article_count": counts["article_count"], "ready": counts["portal_count"]+counts["category_count"]+counts["article_count"] == 0}, nil
		})
	case "BIL":
		if err := r.Contracts.RegisterQuery("bil.order.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, idErr := uintFilter(query.Filter, "id")
			orderID, _ := query.Filter["order_id"].(string)
			trackingNumber, _ := query.Filter["tracking_number"].(string)
			var order domain.Order
			ownerQuery := r.DB.Where("account_id = ?", query.AccountID)
			switch {
			case idErr == nil:
				ownerQuery = ownerQuery.Where("id = ?", id)
			case strings.TrimSpace(orderID) != "":
				ownerQuery = ownerQuery.Where("order_id = ?", strings.TrimSpace(orderID))
			case strings.TrimSpace(trackingNumber) != "":
				ownerQuery = ownerQuery.Where("tracking_number = ?", strings.TrimSpace(trackingNumber))
			default:
				return nil, &ContractFailure{Type: "validation_failed", Message: "order id, order_id or tracking_number is required"}
			}
			result := ownerQuery.First(&order)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "order was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "order store is unavailable"}
			}
			return map[string]any{"id": order.ID, "account_id": order.AccountID, "order_id": order.OrderID, "contact_id": order.ContactID, "conversation_id": order.ConversationID, "amount_yuan": order.AmountYuan, "order_status": order.OrderStatus, "carrier": order.Carrier, "tracking_number": order.TrackingNumber, "warehouse_synced": order.WarehouseSynced, "created_at": order.CreatedAt, "updated_at": order.UpdatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("bil.payment.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			id, err := uintFilter(query.Filter, "id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "payment id is required"}
			}
			var payment domain.AccountBilling
			result := r.DB.Where("account_id = ? AND id = ?", query.AccountID, id).First(&payment)
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil, &ContractFailure{Type: "resource_not_found", Message: "payment was not found"}
			}
			if result.Error != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "payment store is unavailable"}
			}
			return map[string]any{"id": payment.ID, "account_id": payment.AccountID, "type": payment.ActionType, "amount": payment.Amount, "currency": payment.Currency, "description": payment.Description, "created_at": payment.CreatedAt}, nil
		}); err != nil {
			return err
		}
		if err := r.Contracts.RegisterQuery("bil.balance.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
			var rows []domain.AccountBilling
			if err := r.DB.Where("account_id = ?", query.AccountID).Order("id ASC").Find(&rows).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "billing store is unavailable"}
			}
			balance, currency := float64(0), "USD"
			for _, row := range rows {
				if row.Currency != "" {
					currency = row.Currency
				}
				if row.ActionType == "deposit" {
					balance += row.Amount
				} else if row.ActionType == "deduction" {
					balance -= row.Amount
				}
			}
			return map[string]any{"account_id": query.AccountID, "balance": balance, "currency": currency, "entry_count": len(rows)}, nil
		}); err != nil {
			return err
		}
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

func messageFromModel(message domain.Message) messageView {
	return messageView{ID: message.ID, AccountID: message.AccountID, ConversationID: message.ConversationID, SenderType: message.SenderType, SenderID: message.SenderID, MessageType: message.MessageType, ContentType: message.ContentType, Content: message.Content, Private: message.Private, Status: message.Status, Deleted: message.Deleted, DeletedAt: message.DeletedAt, EditedAt: message.EditedAt, CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt}
}

func ticketFromModel(ticket *domain.Ticket) ticketView {
	return ticketView{ID: ticket.ID, AccountID: ticket.AccountID, TicketNumber: ticket.TicketNumber, Title: ticket.Title, Description: ticket.Description, Status: ticket.Status, Priority: ticket.Priority, AssignedGroup: ticket.AssignedGroup, AssigneeID: ticket.AssigneeID, ContactID: ticket.ContactID, ConversationID: ticket.ConversationID, CreatorID: ticket.CreatorID, DueAt: ticket.DueAt, ResolvedAt: ticket.ResolvedAt, ClosedAt: ticket.ClosedAt, SLAStatus: ticket.SLAStatus, Version: ticket.Version, CreatedAt: ticket.CreatedAt, UpdatedAt: ticket.UpdatedAt}
}

func qualityReviewFromModel(task *domain.QATask) *qualityReviewView {
	return &qualityReviewView{ID: task.ID, AccountID: task.AccountID, TaskNumber: task.TaskNumber, TargetType: task.TargetType, TargetID: task.TargetID, TargetRef: task.TargetRef, ScorecardID: task.ScorecardID, ScorecardName: task.ScorecardName, InspectorID: task.InspectorID, AgentID: task.AgentID, SamplingRuleID: task.SamplingRuleID, DueAt: task.DueAt, CompletedAt: task.CompletedAt, Status: task.Status, TotalScore: task.TotalScore, Result: task.Result, HasFatalError: task.HasFatalError, Feedback: task.Feedback, RectificationPlan: task.RectificationPlan, RectifiedAt: task.RectifiedAt, SecondInspectorID: task.SecondInspectorID, SecondReviewStatus: task.SecondReviewStatus, SecondReviewNotes: task.SecondReviewNotes, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt}
}

func portalFromModel(portal *domain.Portal) *portalView {
	return &portalView{ID: portal.ID, AccountID: portal.AccountID, Name: portal.Name, Slug: portal.Slug, CustomDomain: portal.CustomDomain, Color: portal.Color, HeaderText: portal.HeaderText, CreatedAt: portal.CreatedAt, UpdatedAt: portal.UpdatedAt}
}

func articleFromModel(article *domain.Article) *articleView {
	return &articleView{ID: article.ID, PortalID: article.PortalID, CategoryID: article.CategoryID, AccountID: article.AccountID, AuthorID: article.AuthorID, Title: article.Title, Slug: article.Slug, Content: article.Content, Status: article.Status, Views: article.Views, CreatedAt: article.CreatedAt, UpdatedAt: article.UpdatedAt}
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

func conversationCommandTarget(command foundation.CommandEnvelope) (uint, *ContractFailure) {
	if command.Target.Type != "conversation" {
		return 0, &ContractFailure{Type: "validation_failed", Message: "conversation target is required"}
	}
	parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, &ContractFailure{Type: "validation_failed", Message: "conversation target is invalid"}
	}
	return uint(parsed), nil
}

func messageCommandTarget(command foundation.CommandEnvelope) (uint, *ContractFailure) {
	if command.Target.Type != "message" {
		return 0, &ContractFailure{Type: "validation_failed", Message: "message target is required"}
	}
	parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, &ContractFailure{Type: "validation_failed", Message: "message target is invalid"}
	}
	return uint(parsed), nil
}

func ticketCommandTarget(command foundation.CommandEnvelope) (uint, *ContractFailure) {
	if command.Target.Type != "ticket" {
		return 0, &ContractFailure{Type: "validation_failed", Message: "ticket target is required"}
	}
	parsed, err := strconv.ParseUint(command.Target.ID, 10, 64)
	if err != nil || parsed == 0 {
		return 0, &ContractFailure{Type: "validation_failed", Message: "ticket target is invalid"}
	}
	return uint(parsed), nil
}

func optionalUint(value any) (*uint, error) {
	if value == nil {
		return nil, nil
	}
	var parsed uint
	switch typed := value.(type) {
	case float64:
		if typed < 0 || typed != float64(uint(typed)) {
			return nil, errors.New("value is invalid")
		}
		parsed = uint(typed)
	case uint:
		parsed = typed
	case int:
		if typed < 0 {
			return nil, errors.New("value is invalid")
		}
		parsed = uint(typed)
	case string:
		value, err := strconv.ParseUint(typed, 10, 64)
		if err != nil {
			return nil, errors.New("value is invalid")
		}
		parsed = uint(value)
	default:
		return nil, errors.New("value is invalid")
	}
	if parsed == 0 {
		return nil, nil
	}
	return &parsed, nil
}

func uintSlice(value any) ([]uint, error) {
	raw, ok := value.([]any)
	if !ok {
		if typed, valid := value.([]uint); valid {
			raw = make([]any, len(typed))
			for index := range typed {
				raw[index] = typed[index]
			}
		} else {
			return nil, errors.New("value is invalid")
		}
	}
	result := make([]uint, 0, len(raw))
	seen := map[uint]bool{}
	for _, item := range raw {
		parsed, err := optionalUint(item)
		if err != nil || parsed == nil {
			return nil, errors.New("value is invalid")
		}
		if !seen[*parsed] {
			result, seen[*parsed] = append(result, *parsed), true
		}
	}
	return result, nil
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

func (c *moduleClient) query(ctx context.Context, envelope foundation.QueryEnvelope, output any) error {
	correlationID := foundation.GenerateUUID()
	body, _ := json.Marshal(envelope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contracts/queries/"+envelope.QueryType, bytes.NewReader(body))
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "dependency request could not be created"}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(headerCaller, c.caller)
	request.Header.Set(headerContractVersion, c.version)
	request.Header.Set(headerCorrelationID, correlationID)
	response, err := c.client.Do(request)
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency returned an invalid response"}
	}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || len(payload.Data) == 0 {
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency returned invalid data"}
	}
	if err := json.Unmarshal(payload.Data, output); err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency returned incompatible data"}
	}
	return nil
}

func (c *moduleClient) command(ctx context.Context, envelope foundation.CommandEnvelope, output any) error {
	body, _ := json.Marshal(envelope)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/contracts/commands/"+envelope.CommandType, bytes.NewReader(body))
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "dependency request could not be created"}
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set(headerCaller, c.caller)
	request.Header.Set(headerContractVersion, c.version)
	request.Header.Set(headerCorrelationID, envelope.CorrelationID)
	response, err := c.client.Do(request)
	if err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency is unavailable"}
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		var failure foundation.ContractError
		if json.NewDecoder(response.Body).Decode(&failure) == nil && failure.Validate() == nil {
			return &ContractFailure{Type: failure.Type, Message: failure.Message, RetryAfterMS: failure.RetryAfterMS, Details: failure.Details}
		}
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency returned an invalid response"}
	}
	var payload struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil || len(payload.Data) == 0 {
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency returned invalid data"}
	}
	if err := json.Unmarshal(payload.Data, output); err != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "module dependency returned incompatible data"}
	}
	return nil
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
