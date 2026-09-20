package foundation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

var PluginLifecycleStates = map[string]bool{
	"inactive": true, "active": true, "paused": true, "upgrading": true,
	"rolling_back": true, "uninstalling": true, "uninstalled": true,
}

var PluginInvocationResults = map[string]bool{
	"accepted": true, "succeeded": true, "failed_retryable": true,
	"failed_permanent": true, "timed_out": true, "result_unknown": true, "cancelled": true,
}

type PluginExtensionPolicy struct {
	Capabilities map[string]bool
	HighRisk     bool
}

var PluginExtensionPolicies = map[string]PluginExtensionPolicy{
	"channel_adapter":         {Capabilities: stringSet("channel.authorize", "message.receive", "message.send", "channel.health")},
	"ai_provider":             {Capabilities: stringSet("model.list", "generation.create", "embedding.create", "content.moderate", "usage.record")},
	"knowledge_source":        {Capabilities: stringSet("knowledge.sync", "knowledge.version", "knowledge.publish")},
	"automation_condition":    {Capabilities: stringSet("condition.evaluate")},
	"automation_action":       {Capabilities: stringSet("action.execute", "action.compensate"), HighRisk: true},
	"notification_channel":    {Capabilities: stringSet("notification.deliver", "delivery.status")},
	"integration_connector":   {Capabilities: stringSet("connector.query", "connector.command", "external_reference.record"), HighRisk: true},
	"custom_tool":             {Capabilities: stringSet("tool.invoke", "tool.result.query", "tool.compensate"), HighRisk: true},
	"import_source":           {Capabilities: stringSet("source.read", "record.transform", "import.checkpoint")},
	"message_content_handler": {Capabilities: stringSet("content.validate", "content.render", "attachment.reference")},
	"authentication_provider": {Capabilities: stringSet("identity.assert", "identity.challenge"), HighRisk: true},
	"report_exporter":         {Capabilities: stringSet("report.export", "export.status")},
}

func stringSet(values ...string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		result[value] = true
	}
	return result
}

type PluginManifest struct {
	PluginID             string         `json:"plugin_id"`
	ExtensionPoint       string         `json:"extension_point"`
	Name                 string         `json:"name"`
	Publisher            string         `json:"publisher"`
	Version              string         `json:"version"`
	ContractVersion      string         `json:"contract_version"`
	MinSystemVersion     string         `json:"min_system_version"`
	MaxSystemVersion     string         `json:"max_system_version"`
	FoundationVersion    string         `json:"foundation_version"`
	RequiredFeatures     []string       `json:"required_features"`
	RequiredScopes       []string       `json:"required_scopes"`
	OptionalScopes       []string       `json:"optional_scopes"`
	DataClasses          []string       `json:"data_classes"`
	ObjectRanges         []string       `json:"object_ranges"`
	ProvidedCapabilities []string       `json:"provided_capabilities"`
	Commands             []string       `json:"commands"`
	Queries              []string       `json:"queries"`
	Subscriptions        []string       `json:"subscriptions"`
	Callbacks            []string       `json:"callbacks"`
	ConfigurationSchema  map[string]any `json:"configuration_schema"`
	SecretFields         []string       `json:"secret_fields"`
	AllowedDomains       []string       `json:"allowed_domains"`
	Protocols            []string       `json:"protocols"`
	TimeoutMS            int            `json:"timeout_ms"`
	Concurrency          int            `json:"concurrency"`
	RateLimitPerMinute   int            `json:"rate_limit_per_minute"`
	PayloadSizeBytes     int            `json:"payload_size_bytes"`
	Quota                int            `json:"quota"`
	Lifecycle            []string       `json:"lifecycle"`
	PrivateDataPolicy    string         `json:"private_data_policy"`
	RetentionDays        int            `json:"retention_days"`
	ExportPolicy         string         `json:"export_policy"`
	DeletionPolicy       string         `json:"deletion_policy"`
	DataLocation         string         `json:"data_location"`
	HealthURL            string         `json:"health_url"`
	SupportURL           string         `json:"support_url"`
	Signature            string         `json:"signature"`
	HighRiskConfirmation bool           `json:"high_risk_confirmation"`
	Compensation         string         `json:"compensation"`
}

func (m PluginManifest) Validate() error {
	required := map[string]string{"plugin_id": m.PluginID, "extension_point": m.ExtensionPoint, "name": m.Name, "publisher": m.Publisher, "version": m.Version, "contract_version": m.ContractVersion, "min_system_version": m.MinSystemVersion, "max_system_version": m.MaxSystemVersion, "foundation_version": m.FoundationVersion, "private_data_policy": m.PrivateDataPolicy, "export_policy": m.ExportPolicy, "deletion_policy": m.DeletionPolicy, "data_location": m.DataLocation, "signature": m.Signature, "support_url": m.SupportURL, "health_url": m.HealthURL}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	policy, exists := PluginExtensionPolicies[m.ExtensionPoint]
	if !exists {
		return fmt.Errorf("unknown extension_point %s", m.ExtensionPoint)
	}
	for _, capability := range m.ProvidedCapabilities {
		if !policy.Capabilities[capability] {
			return fmt.Errorf("capability %s is not allowed for %s", capability, m.ExtensionPoint)
		}
	}
	if len(m.RequiredFeatures) == 0 || len(m.RequiredScopes) == 0 || len(m.OptionalScopes) == 0 || len(m.DataClasses) == 0 || len(m.ObjectRanges) == 0 || len(m.ProvidedCapabilities) == 0 || len(m.Callbacks) == 0 || len(m.ConfigurationSchema) == 0 || len(m.SecretFields) == 0 || len(m.AllowedDomains) == 0 || len(m.Protocols) == 0 || len(m.Lifecycle) == 0 {
		return fmt.Errorf("scope, data, object, capability and lifecycle declarations are required")
	}
	if len(m.Commands)+len(m.Queries)+len(m.Subscriptions) == 0 {
		return fmt.Errorf("at least one command, query or subscription is required")
	}
	if m.TimeoutMS <= 0 || m.Concurrency <= 0 || m.RateLimitPerMinute <= 0 || m.PayloadSizeBytes <= 0 || m.Quota <= 0 {
		return fmt.Errorf("resource limits must be positive")
	}
	for _, state := range m.Lifecycle {
		if !PluginLifecycleStates[state] {
			return fmt.Errorf("unsupported lifecycle state %s", state)
		}
	}
	for _, scope := range append(append([]string{}, m.RequiredScopes...), m.OptionalScopes...) {
		if strings.Contains(scope, "*") {
			return fmt.Errorf("wildcard scope is forbidden")
		}
	}
	for _, protocol := range m.Protocols {
		if protocol != "https" {
			return fmt.Errorf("unsupported protocol %s", protocol)
		}
	}
	for _, raw := range m.AllowedDomains {
		if strings.Contains(raw, "*") {
			return fmt.Errorf("wildcard network target is forbidden")
		}
	}
	allowed := map[string]bool{}
	for _, domain := range m.AllowedDomains {
		allowed[strings.ToLower(domain)] = true
	}
	for field, raw := range map[string]string{"health_url": m.HealthURL, "support_url": m.SupportURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || !allowed[strings.ToLower(u.Hostname())] {
			return fmt.Errorf("%s must be https on an allowed domain", field)
		}
	}
	for _, raw := range m.Callbacks {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || !allowed[strings.ToLower(u.Hostname())] {
			return fmt.Errorf("callback must be https on an allowed domain")
		}
	}
	if m.RetentionDays <= 0 {
		return fmt.Errorf("retention_days must be positive")
	}
	if policy.HighRisk && (!m.HighRiskConfirmation || strings.TrimSpace(m.Compensation) == "") {
		return fmt.Errorf("high-risk capability requires compensation")
	}
	if err := validateSHA256Signature(m.Signature); err != nil {
		return err
	}
	return nil
}

func validateSHA256Signature(signature string) error {
	parts := strings.SplitN(signature, ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" {
		return fmt.Errorf("signature must use sha256")
	}
	decoded, err := hex.DecodeString(parts[1])
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("signature digest is invalid")
	}
	return nil
}

type PluginInvocation struct {
	InvocationID       string         `json:"invocation_id"`
	InstallationID     string         `json:"installation_id"`
	AccountID          uint           `json:"account_id"`
	Actor              ContractActor  `json:"actor"`
	Capability         string         `json:"capability"`
	ContractVersion    string         `json:"contract_version"`
	IdempotencyKey     string         `json:"idempotency_key"`
	CorrelationID      string         `json:"correlation_id"`
	Deadline           time.Time      `json:"deadline"`
	Payload            map[string]any `json:"payload"`
	DataClassification string         `json:"data_classification"`
	Result             string         `json:"result"`
}

func (i PluginInvocation) Validate(now time.Time) error {
	if i.InvocationID == "" || i.InstallationID == "" || i.AccountID == 0 || i.Actor.ID == "" || i.Capability == "" || i.ContractVersion == "" || i.IdempotencyKey == "" || i.CorrelationID == "" || i.DataClassification == "" {
		return fmt.Errorf("incomplete plugin invocation")
	}
	if i.Deadline.IsZero() || !i.Deadline.After(now) {
		return fmt.Errorf("deadline must be in the future")
	}
	if !PluginInvocationResults[i.Result] {
		return fmt.Errorf("unsupported invocation result")
	}
	return nil
}

type PluginEventDelivery struct {
	InstallationID string    `json:"installation_id"`
	EventID        string    `json:"event_id"`
	DeliveryID     string    `json:"delivery_id"`
	EventVersion   string    `json:"event_version"`
	Signature      string    `json:"signature"`
	OccurredAt     time.Time `json:"occurred_at"`
}

func (d PluginEventDelivery) Validate() error {
	if d.InstallationID == "" || d.EventID == "" || d.DeliveryID == "" || d.EventVersion == "" || d.Signature == "" || d.OccurredAt.IsZero() {
		return fmt.Errorf("incomplete plugin event delivery")
	}
	return validateSHA256Signature(d.Signature)
}

type PluginAuthorization struct {
	InstallationAccountID uint
	RequestAccountID      uint
	InstallationState     string
	VersionSupported      bool
	FeatureAllowed        bool
	ActorAllowed          bool
	GrantedScopes         map[string]bool
	RequiredScope         string
	GrantedObjects        map[string]bool
	TargetObject          string
	AllowedDataClasses    map[string]bool
	DataClassification    string
	WithinQuota           bool
	HighRisk              bool
	Confirmed             bool
}

func (a PluginAuthorization) Validate() error {
	if a.InstallationAccountID == 0 || a.InstallationAccountID != a.RequestAccountID {
		return fmt.Errorf("account boundary denied")
	}
	if a.InstallationState != "active" || !a.VersionSupported || !a.FeatureAllowed || !a.ActorAllowed || !a.WithinQuota {
		return fmt.Errorf("installation or actor is not authorized")
	}
	if a.RequiredScope == "" || !a.GrantedScopes[a.RequiredScope] {
		return fmt.Errorf("scope denied")
	}
	if a.TargetObject == "" || !a.GrantedObjects[a.TargetObject] {
		return fmt.Errorf("object range denied")
	}
	if a.DataClassification == "" || !a.AllowedDataClasses[a.DataClassification] {
		return fmt.Errorf("data classification denied")
	}
	if a.HighRisk && !a.Confirmed {
		return fmt.Errorf("high-risk action requires confirmation")
	}
	return nil
}
