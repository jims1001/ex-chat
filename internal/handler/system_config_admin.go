package handler

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// AdminSystemConfig is exclusive to the existing SuperAdmin REST endpoint.
// Keep the full owner model unchanged for private reads and backups.
type AdminSystemConfig struct {
	ID         uint      `json:"id"`
	ConfigKey  string    `json:"config_key"`
	Value      string    `json:"value"`
	UpdatedAt  time.Time `json:"updated_at"`
	Redacted   bool      `json:"redacted"`
	Configured bool      `json:"configured"`
}

func adminSystemConfig(config domain.SystemConfig) AdminSystemConfig {
	readable := adminConfigValueReadable(config.ConfigKey)
	value := ""
	if readable {
		value = config.Value
	}
	return AdminSystemConfig{
		ID: config.ID, ConfigKey: config.ConfigKey, Value: value,
		UpdatedAt: config.UpdatedAt, Redacted: !readable, Configured: config.Value != "",
	}
}

// Exact positive allowlist from sections 8–15 of document 24, the installation
// configuration dictionary: 102 keys minus its 29 sensitive keys. It grants no
// public read or write authority. Unknown names and case variants fail closed;
// adding a readable key requires an explicit reviewed catalog change.
func adminConfigValueReadable(key string) bool {
	switch key {
	case "INSTALLATION_NAME", "LOGO_THUMBNAIL", "LOGO":
		return true
	case "LOGO_DARK", "BRAND_URL", "WIDGET_BRAND_URL":
		return true
	case "BRAND_NAME", "TERMS_URL", "PRIVACY_URL":
		return true
	case "DISPLAY_MANIFEST", "ENABLE_ACCOUNT_SIGNUP", "CREATE_NEW_ACCOUNT_FROM_DASHBOARD":
		return true
	case "HCAPTCHA_SITE_KEY", "INSTALLATION_EVENTS_WEBHOOK_URL", "WEBHOOK_TIMEOUT":
		return true
	case "DIRECT_UPLOADS_ENABLED", "MAXIMUM_FILE_UPLOAD_SIZE", "MAILER_INBOUND_EMAIL_DOMAIN":
		return true
	case "MAILER_SUPPORT_EMAIL", "ACCOUNT_EMAILS_LIMIT", "ACCOUNT_EMAILS_PLAN_LIMITS":
		return true
	case "FB_APP_ID", "FACEBOOK_API_VERSION", "ENABLE_MESSENGER_CHANNEL_HUMAN_AGENT":
		return true
	case "WHATSAPP_APP_ID", "WHATSAPP_CONFIGURATION_ID", "WHATSAPP_API_VERSION":
		return true
	case "AZURE_APP_ID", "INSTAGRAM_APP_ID", "ENABLE_INSTAGRAM_CHANNEL_HUMAN_AGENT":
		return true
	case "INSTAGRAM_API_VERSION", "TIKTOK_API_VERSION", "TIKTOK_APP_ID":
		return true
	case "GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_REDIRECT_URI", "ENABLE_GOOGLE_OAUTH_LOGIN":
		return true
	case "ENABLE_SAML_SSO_LOGIN", "INACTIVE_WHATSAPP_NUMBERS", "CAPTAIN_OPEN_AI_MODEL":
		return true
	case "CAPTAIN_OPEN_AI_ENDPOINT", "CAPTAIN_EMBEDDING_MODEL", "CAPTAIN_CLOUD_PLAN_LIMITS":
		return true
	case "CAPTAIN_DOCUMENT_AUTO_SYNC_INTERVALS", "CAPTAIN_DOCUMENT_AUTO_SYNC_PER_ACCOUNT_BATCH_LIMIT", "CAPTAIN_DOCUMENT_AUTO_SYNC_GLOBAL_BATCH_LIMIT":
		return true
	case "CAPTAIN_TOPUP_OPTIONS", "OTEL_PROVIDER", "LANGFUSE_BASE_URL":
		return true
	case "LINEAR_CLIENT_ID", "NOTION_CLIENT_ID", "NOTION_VERSION":
		return true
	case "SLACK_CLIENT_ID", "CLOUDFLARE_ZONE_ID", "OG_IMAGE_CDN_URL":
		return true
	case "FIREBASE_PROJECT_ID", "WIDGET_TOKEN_EXPIRY", "CHATWOOT_CLOUD_PLANS":
		return true
	case "ENABLE_MULTI_CURRENCY_BILLING", "MARKETING_CONVERSION_TRACKING_CONFIG", "CHATWOOT_CLOUD_PLAN_FEATURES":
		return true
	case "DEPLOYMENT_ENV", "DASHBOARD_SCRIPTS", "BLOCKED_EMAIL_DOMAINS":
		return true
	case "SKIP_INCOMING_BCC_PROCESSING", "INSTALLATION_PRICING_PLAN", "INSTALLATION_PRICING_PLAN_QUANTITY":
		return true
	case "CHATWOOT_SUPPORT_SCRIPT_URL", "ACCOUNT_SECURITY_NOTIFICATION_WEBHOOK_URL", "CHATWOOT_INSTANCE_ADMIN_EMAIL":
		return true
	case "API_CHANNEL_NAME", "API_CHANNEL_THUMBNAIL", "LOGOUT_REDIRECT_LINK":
		return true
	case "DISABLE_USER_PROFILE_UPDATE":
		return true
	default:
		return false
	}
}
