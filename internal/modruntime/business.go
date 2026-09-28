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
			return inboxFromModel(inbox), nil
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
			return map[string]any{"inbox_id": inbox.ID, "authorized": true}, nil
		})
	default:
		return fmt.Errorf("business contracts are not implemented for %s", r.Config.ModuleID)
	}
}

func accountFromModel(account *domain.Account) *accountView {
	return &accountView{ID: account.ID, Name: account.Name, Locale: account.Locale, Status: account.Status, Timezone: account.Timezone}
}

func inboxFromModel(inbox *domain.Inbox) *inboxView {
	return &inboxView{ID: inbox.ID, AccountID: inbox.AccountID, Name: inbox.Name, ChannelType: inbox.ChannelType, AvatarURL: inbox.AvatarURL, GreetingMessage: inbox.GreetingMessage, GreetingEnabled: inbox.GreetingEnabled, WorkingHoursEnabled: inbox.WorkingHoursEnabled, OutOfOfficeMessage: inbox.OutOfOfficeMessage, Timezone: inbox.Timezone, WorkingHours: inbox.WorkingHours, CSATSurveyEnabled: inbox.CSATSurveyEnabled}
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
