package foundation

import (
	"fmt"
	"strings"
)

type ExternalInterfacePolicy struct {
	Identity       string
	DataScope      string
	AllowedPurpose string
	ForbiddenUse   string
}

var ExternalInterfacePolicies = map[string]ExternalInterfacePolicy{
	"account_api":           {"account_member", "authorized_account_objects", "workspace_and_account_operations", "installation_management_or_customer_impersonation"},
	"widget_api":            {"widget_session", "one_inbox_contact_and_allowed_conversations", "website_chat", "member_management_or_cross_customer_reads"},
	"public_api":            {"inbox_customer", "current_customer_resources_in_one_inbox", "custom_customer_interfaces", "account_management_or_cross_customer_reads"},
	"platform_api":          {"platform_app", "granted_accounts_users_and_relations", "multi_tenant_control_plane", "message_reads_without_scope"},
	"admin_api":             {"installation_admin", "installation_and_global_resources", "configuration_version_and_diagnostics", "account_audit_bypass"},
	"plugin_capability_api": {"plugin_installation_and_actor", "manifest_scope_and_object_grants", "public_capability_invocation", "module_storage_access"},
	"provider_callback":     {"verified_provider", "bound_connection_and_provider_event", "inbound_facts_and_results", "arbitrary_business_queries"},
	"webhook_delivery":      {"system_signature", "minimal_subscribed_event_view", "outbound_fact_delivery", "external_commands"},
	"realtime":              {"subscription_identity", "visible_customer_or_member_events", "live_updates_and_cursor_ack", "business_state_writes"},
}

var ExternalLayerErrors = map[string][]string{
	"E1": {"request_too_large", "unsupported_protocol", "rate_limited"},
	"E2": {"authentication_failed", "signature_invalid", "session_invalid"},
	"E3": {"route_invalid", "version_unsupported", "validation_failed", "pagination_invalid"},
	"E4": {"permission_denied", "feature_unavailable", "quota_exceeded", "object_scope_denied"},
	"E5": {"process_conflict", "dependency_unavailable", "result_unknown"},
	"E6": {"state_conflict", "unique_constraint", "permanent_external_failure"},
	"E7": {"side_effect_delayed", "projection_stale", "delivery_partial"},
}

var ExternalVersionRules = []string{
	"optional_addition_compatible",
	"removal_requires_major",
	"semantic_change_requires_major",
	"unknown_enum_tolerated",
	"permission_change_requires_reauthorization",
	"side_effect_change_requires_major",
}

type ExternalWriteActionPolicy struct {
	ExternalActionID string
	ActionType       string
	Action           string
}

var ExternalWriteDispatchOverrides = map[string]ExternalWriteActionPolicy{
	"accountHandler.CreateAccount": {ActionType: "command", Action: "ten.account.create.v1"},
	"accountHandler.AddAgent":      {ActionType: "command", Action: "ten.membership.add.v1"},
	"accountHandler.UpdateAgent":   {ActionType: "command", Action: "ten.role.change.v1"},
	"contactHandler.CreateContact": {ActionType: "command", Action: "cus.contact.create.v1"},
	"contactHandler.UpdateContact": {ActionType: "command", Action: "cus.contact.update.v1"},
	"contactHandler.DeleteContact": {ActionType: "command", Action: "cus.contact.delete.v1"},
	"contactHandler.MergeContact":  {ActionType: "command", Action: "cus.contact.merge.v1"},
	"emailLogHandler.Retry":        {ActionType: "command", Action: "msg.message.retry.v1"},
	"emailLogHandler.Bounce":       {ActionType: "command", Action: "msg.provider_status.update.v1"},
	"ticketHandler.Create":         {ActionType: "command", Action: "tkt.ticket.create.v1"},
	"webhookHandler.Create":        {ActionType: "command", Action: "ext.webhook.manage.v1"},
}

func ResolveExternalWriteAction(handlerMethod string) ExternalWriteActionPolicy {
	policy, ok := ExternalWriteDispatchOverrides[handlerMethod]
	if !ok {
		policy = ExternalWriteActionPolicy{ActionType: "orchestration", Action: "orc.process.start.v1"}
	}
	policy.ExternalActionID = "api.external." + strings.ToLower(strings.ReplaceAll(handlerMethod, ".", "_")) + ".v1"
	return policy
}

type ExternalActionContract struct {
	Family           string
	ExternalActionID string
	AccountID        uint
	Actor            ContractActor
	ContractVersion  string
	CorrelationID    string
	ActionType       string
	Action           string
	IdempotencyKey   string
	SensitiveView    string
	RateLimitKey     string
}

func (c ExternalActionContract) Validate() error {
	if _, ok := ExternalInterfacePolicies[c.Family]; !ok {
		return fmt.Errorf("unknown external interface family %s", c.Family)
	}
	if c.AccountID == 0 || !validActor(c.Actor) || c.ContractVersion == "" || c.CorrelationID == "" || c.ExternalActionID == "" || c.Action == "" || c.SensitiveView == "" || c.RateLimitKey == "" {
		return fmt.Errorf("incomplete external action contract")
	}
	if c.ActionType != "command" && c.ActionType != "orchestration" && c.ActionType != "query" && c.ActionType != "event_delivery" && c.ActionType != "realtime_subscription" {
		return fmt.Errorf("unsupported external action type")
	}
	if (c.ActionType == "command" || c.ActionType == "orchestration") && strings.TrimSpace(c.IdempotencyKey) == "" {
		return fmt.Errorf("external writes require idempotency")
	}
	return nil
}

type AggregateFieldSource struct {
	FieldGroup      string
	OwnerModule     string
	StableReference string
	Projection      bool
	UpdatedAt       string
	Version         string
}

func (s AggregateFieldSource) Validate() error {
	if s.FieldGroup == "" || s.OwnerModule == "" || s.StableReference == "" {
		return fmt.Errorf("aggregate field provenance is required")
	}
	if s.Projection && s.UpdatedAt == "" && s.Version == "" {
		return fmt.Errorf("projection freshness is required")
	}
	return nil
}

type ProviderCallbackContract struct {
	Provider          string
	ConnectionID      string
	ExternalEventID   string
	SignatureVerified bool
	WithinTimeWindow  bool
	ReceiptStored     bool
	StandardFact      string
	Compensation      string
}

func (c ProviderCallbackContract) Validate() error {
	if c.Provider == "" || c.ConnectionID == "" || c.ExternalEventID == "" || !c.SignatureVerified || !c.WithinTimeWindow || !c.ReceiptStored || c.StandardFact == "" || c.Compensation == "" {
		return fmt.Errorf("invalid provider callback contract")
	}
	return nil
}
