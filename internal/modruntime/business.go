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
		dependency, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return nil
		}
		client := newModuleClient("CHN", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		return r.Contracts.RegisterCommand("chn.inbox.create.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
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

func (c *moduleClient) getIdentities(ctx context.Context, parent foundation.QueryEnvelope, userIDs []uint) (map[uint]*identityView, error) {
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
