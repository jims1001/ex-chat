package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// These independent expectations follow sections 8–15 of the installation
// dictionary (24-Feature开关版本与安装配置字典.md), including its combined rows.
var readableInstallationKeys = strings.Fields(`
INSTALLATION_NAME LOGO_THUMBNAIL LOGO LOGO_DARK BRAND_URL WIDGET_BRAND_URL BRAND_NAME TERMS_URL PRIVACY_URL DISPLAY_MANIFEST
ENABLE_ACCOUNT_SIGNUP CREATE_NEW_ACCOUNT_FROM_DASHBOARD HCAPTCHA_SITE_KEY INSTALLATION_EVENTS_WEBHOOK_URL WEBHOOK_TIMEOUT DIRECT_UPLOADS_ENABLED MAXIMUM_FILE_UPLOAD_SIZE
MAILER_INBOUND_EMAIL_DOMAIN MAILER_SUPPORT_EMAIL ACCOUNT_EMAILS_LIMIT ACCOUNT_EMAILS_PLAN_LIMITS
FB_APP_ID FACEBOOK_API_VERSION ENABLE_MESSENGER_CHANNEL_HUMAN_AGENT WHATSAPP_APP_ID WHATSAPP_CONFIGURATION_ID WHATSAPP_API_VERSION AZURE_APP_ID INSTAGRAM_APP_ID ENABLE_INSTAGRAM_CHANNEL_HUMAN_AGENT INSTAGRAM_API_VERSION TIKTOK_API_VERSION TIKTOK_APP_ID GOOGLE_OAUTH_CLIENT_ID GOOGLE_OAUTH_REDIRECT_URI ENABLE_GOOGLE_OAUTH_LOGIN ENABLE_SAML_SSO_LOGIN INACTIVE_WHATSAPP_NUMBERS
CAPTAIN_OPEN_AI_MODEL CAPTAIN_OPEN_AI_ENDPOINT CAPTAIN_EMBEDDING_MODEL CAPTAIN_CLOUD_PLAN_LIMITS CAPTAIN_DOCUMENT_AUTO_SYNC_INTERVALS CAPTAIN_DOCUMENT_AUTO_SYNC_PER_ACCOUNT_BATCH_LIMIT CAPTAIN_DOCUMENT_AUTO_SYNC_GLOBAL_BATCH_LIMIT CAPTAIN_TOPUP_OPTIONS OTEL_PROVIDER LANGFUSE_BASE_URL
LINEAR_CLIENT_ID NOTION_CLIENT_ID NOTION_VERSION SLACK_CLIENT_ID CLOUDFLARE_ZONE_ID OG_IMAGE_CDN_URL
FIREBASE_PROJECT_ID WIDGET_TOKEN_EXPIRY
CHATWOOT_CLOUD_PLANS ENABLE_MULTI_CURRENCY_BILLING MARKETING_CONVERSION_TRACKING_CONFIG CHATWOOT_CLOUD_PLAN_FEATURES DEPLOYMENT_ENV DASHBOARD_SCRIPTS BLOCKED_EMAIL_DOMAINS SKIP_INCOMING_BCC_PROCESSING INSTALLATION_PRICING_PLAN INSTALLATION_PRICING_PLAN_QUANTITY CHATWOOT_SUPPORT_SCRIPT_URL ACCOUNT_SECURITY_NOTIFICATION_WEBHOOK_URL CHATWOOT_INSTANCE_ADMIN_EMAIL API_CHANNEL_NAME API_CHANNEL_THUMBNAIL LOGOUT_REDIRECT_LINK DISABLE_USER_PROFILE_UPDATE
`)

var sensitiveInstallationKeys = strings.Fields(`
HCAPTCHA_SERVER_KEY FB_VERIFY_TOKEN FB_APP_SECRET IG_VERIFY_TOKEN WHATSAPP_APP_SECRET AZURE_APP_SECRET INSTAGRAM_APP_SECRET INSTAGRAM_VERIFY_TOKEN TIKTOK_APP_SECRET GOOGLE_OAUTH_CLIENT_SECRET
CAPTAIN_OPEN_AI_API_KEY CAPTAIN_FIRECRAWL_API_KEY CONTEXT_DEV_API_KEY LANGFUSE_PUBLIC_KEY LANGFUSE_SECRET_KEY
LINEAR_CLIENT_SECRET NOTION_CLIENT_SECRET SLACK_CLIENT_SECRET SHOPIFY_CLIENT_ID SHOPIFY_CLIENT_SECRET CLOUDFLARE_API_KEY OG_IMAGE_CLIENT_REF
FIREBASE_CREDENTIALS CHATWOOT_INBOX_TOKEN CHATWOOT_INBOX_HMAC_KEY CLOUD_ANALYTICS_TOKEN CLEARBIT_API_KEY CHATWOOT_SUPPORT_WEBSITE_TOKEN CHATWOOT_SUPPORT_IDENTIFIER_HASH
`)

type systemConfigRouteFixture struct {
	db     *gorm.DB
	router http.Handler
	token  string
	logs   bytes.Buffer
}

func newSystemConfigRouteFixture(t *testing.T) *systemConfigRouteFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "config-redaction-local-route-test-secret", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	// Enabling SQL tracing makes secret-bearing repository SQL visible to the
	// test if the repository ever loses its own silent logger.
	db = db.Session(&gorm.Session{Logger: logger.NewGORMLogger(time.Millisecond).LogMode(gormlogger.Info)})
	user := domain.User{Name: "Configuration administrator", Email: "config-admin@example.invalid", Role: domain.RoleAgent, Type: domain.UserTypeSuperAdmin}
	require.NoError(t, db.Create(&user).Error)
	token, err := auth.GenerateToken(&user, cfg.JWTSecret, 24)
	require.NoError(t, err)
	f := &systemConfigRouteFixture{db: db, router: router.SetupRouter(cfg, db, ws.NewHub()), token: token}
	logger.SetOutput(&f.logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	return f
}

func (f *systemConfigRouteFixture) request(method, body, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/super_admin/configs"+query, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func configRequestJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}

func assertConfigResponsePrivate(t *testing.T, w *httptest.ResponseRecorder, canaries ...string) {
	t.Helper()
	for _, canary := range canaries {
		require.NotContains(t, w.Body.String(), canary)
		for _, values := range w.Header() {
			for _, value := range values {
				require.NotContains(t, value, canary)
			}
		}
	}
}

func TestSystemConfigRedactionCatalog(t *testing.T) {
	f := newSystemConfigRouteFixture(t)
	require.Len(t, readableInstallationKeys, 73)
	require.Len(t, sensitiveInstallationKeys, 29)
	expected := map[string]bool{}
	for _, key := range readableInstallationKeys {
		require.NotContains(t, expected, key)
		expected[key] = false
	}
	for _, key := range sensitiveInstallationKeys {
		require.NotContains(t, expected, key)
		expected[key] = true
	}
	// Every readable key also has a differently cased stored counterpart.
	for _, key := range readableInstallationKeys {
		expected[strings.ToLower(key)] = true
	}
	for _, key := range []string{"SMTP_PASSWORD", "OPENAI_API_KEY", "JWT_SECRET", "CHANNEL_CREDENTIAL_KEY", "STRIPE_SECRET_KEY", "FUTURE_EXTENSION_SETTING", "ENABLE_GLOBAL_AI", "Installation_Name", "INSTALLATION_NAME ", " INSTALLATION_NAME"} {
		expected[key] = true
	}
	stored := map[string]domain.SystemConfig{}
	canaries := []string{}
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	for key, redacted := range expected {
		value := "  visible bytes " + key + "\n配置\t  "
		if redacted {
			value = fmt.Sprintf("PRIVATE-CONFIG-%03d-CANARY", len(canaries))
			canaries = append(canaries, value)
		}
		row := domain.SystemConfig{ConfigKey: key, Value: value, UpdatedAt: stamp}
		require.NoError(t, f.db.Create(&row).Error)
		stored[key] = row
	}
	f.logs.Reset()
	w := f.request(http.MethodGet, "", "?config_key=QUERY-CONFIG-CANARY&value=QUERY-VALUE-CANARY")
	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Success bool             `json:"success"`
		Data    []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.Len(t, envelope.Data, len(expected))
	seen := map[string]bool{}
	lastKey := ""
	for _, row := range envelope.Data {
		key, ok := row["config_key"].(string)
		require.True(t, ok)
		require.Contains(t, expected, key)
		require.False(t, seen[key], "duplicate key %q", key)
		seen[key] = true
		require.Greater(t, key, lastKey, "list ordering remains stable")
		lastKey = key
		require.Len(t, row, 6)
		require.Equal(t, float64(stored[key].ID), row["id"])
		require.Equal(t, stamp.Format(time.RFC3339), row["updated_at"])
		require.Equal(t, expected[key], row["redacted"], key)
		require.Equal(t, true, row["configured"], key)
		value := stored[key].Value
		if expected[key] {
			value = ""
		}
		require.Equal(t, value, row["value"], key)
	}
	assertConfigResponsePrivate(t, w, canaries...)
	for _, canary := range append(canaries, "QUERY-CONFIG-CANARY", "QUERY-VALUE-CANARY") {
		require.NotContains(t, f.logs.String(), canary)
	}
	require.Contains(t, f.logs.String(), `"component":"http"`, "log assertion must observe the actual router")

	// The HTTP projection must never rewrite the stored values or backup DTO.
	var persisted []domain.SystemConfig
	require.NoError(t, f.db.Find(&persisted).Error)
	for _, row := range persisted {
		require.Equal(t, stored[row.ConfigKey], row)
	}
	raw := configRequestJSON(t, stored["CAPTAIN_OPEN_AI_API_KEY"])
	var rawFields map[string]any
	require.NoError(t, json.Unmarshal([]byte(raw), &rawFields))
	require.Len(t, rawFields, 4)
	require.Equal(t, stored["CAPTAIN_OPEN_AI_API_KEY"].Value, rawFields["value"])
	require.NotContains(t, rawFields, "redacted")
	require.NotContains(t, rawFields, "configured")

	// Empty values keep the same classification, including unknown keys, but
	// explicitly report that no value is configured.
	require.NoError(t, f.db.Model(&domain.SystemConfig{}).Where("1 = 1").Update("value", "").Error)
	w = f.request(http.MethodGet, "", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Len(t, envelope.Data, len(expected))
	for _, row := range envelope.Data {
		require.Equal(t, "", row["value"])
		require.Equal(t, false, row["configured"])
		require.Equal(t, expected[row["config_key"].(string)], row["redacted"])
	}
}

func TestSystemConfigRedactionPreserveAndReplace(t *testing.T) {
	f := newSystemConfigRouteFixture(t)
	stamp := time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC)
	original := domain.SystemConfig{ConfigKey: "CAPTAIN_OPEN_AI_API_KEY", Value: "ORIGINAL-SECRET-CANARY", UpdatedAt: stamp}
	require.NoError(t, f.db.Create(&original).Error)
	readable := domain.SystemConfig{ConfigKey: "INSTALLATION_NAME", Value: "Original name", UpdatedAt: stamp}
	require.NoError(t, f.db.Create(&readable).Error)
	w := f.request(http.MethodGet, "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var listed struct {
		Data []map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	var roundtrip map[string]any
	var readableRoundtrip map[string]any
	for _, row := range listed.Data {
		if row["config_key"] == original.ConfigKey {
			roundtrip = row
		}
		if row["config_key"] == readable.ConfigKey {
			readableRoundtrip = row
		}
	}
	require.NotNil(t, roundtrip)
	require.NotNil(t, readableRoundtrip)
	require.Equal(t, false, readableRoundtrip["redacted"])
	w = f.request(http.MethodPost, configRequestJSON(t, readableRoundtrip), "")
	require.Equal(t, http.StatusBadRequest, w.Code, "a readable GET row still carries a response marker")
	var readableUnchanged domain.SystemConfig
	require.NoError(t, f.db.First(&readableUnchanged, readable.ID).Error)
	require.Equal(t, readable, readableUnchanged)

	// Guard every config operation. Authentication still queries its own
	// tables, but preserve input must not touch configuration persistence.
	touches := 0
	guard := func(tx *gorm.DB) {
		if tx.Statement.Table == "system_configs" {
			touches++
			tx.AddError(errors.New("PRESERVE-DB-ACCESS-CANARY"))
		}
	}
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("config_preserve_guard", guard))
	require.NoError(t, f.db.Callback().Create().Before("gorm:create").Register("config_preserve_guard", guard))
	require.NoError(t, f.db.Callback().Update().Before("gorm:update").Register("config_preserve_guard", guard))
	require.NoError(t, f.db.Callback().Delete().Before("gorm:delete").Register("config_preserve_guard", guard))
	for _, key := range []string{original.ConfigKey, readable.ConfigKey, "ABSENT_CONFIG"} {
		for _, value := range []string{"", `,"value":null`, `,"value":""`} {
			for _, marker := range []string{"", `,"redacted":true`, `,"redacted":false`} {
				body := fmt.Sprintf(`{"config_key":%q%s%s,"configured":true,"id":999999,"updated_at":"ignored"}`, key, value, marker)
				w := f.request(http.MethodPost, body, "")
				require.Equal(t, http.StatusOK, w.Code, body)
				require.JSONEq(t, fmt.Sprintf(`{"success":true,"data":{"updated":false,"key":%q}}`, key), w.Body.String())
			}
		}
	}
	w = f.request(http.MethodPost, configRequestJSON(t, roundtrip), "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"updated":false`)
	require.Zero(t, touches, "preserve requests must not query or write config rows")
	require.NoError(t, f.db.Callback().Query().Remove("config_preserve_guard"))
	require.NoError(t, f.db.Callback().Create().Remove("config_preserve_guard"))
	require.NoError(t, f.db.Callback().Update().Remove("config_preserve_guard"))
	require.NoError(t, f.db.Callback().Delete().Remove("config_preserve_guard"))
	var unchanged domain.SystemConfig
	require.NoError(t, f.db.First(&unchanged, original.ID).Error)
	require.Equal(t, original, unchanged)
	var count int64
	require.NoError(t, f.db.Model(&domain.SystemConfig{}).Count(&count).Error)
	require.EqualValues(t, 2, count, "preserve must not create missing rows")

	for _, replacement := range []string{"  REPLACEMENT-SECRET-CANARY\n配置\t  ", " ", "********"} {
		f.logs.Reset()
		w = f.request(http.MethodPost, configRequestJSON(t, map[string]any{"config_key": original.ConfigKey, "value": replacement, "configured": false, "id": 99999}), "?anything=WRITE-QUERY-CANARY")
		require.Equal(t, http.StatusOK, w.Code)
		require.JSONEq(t, fmt.Sprintf(`{"success":true,"data":{"updated":true,"key":%q}}`, original.ConfigKey), w.Body.String())
		assertConfigResponsePrivate(t, w, "REPLACEMENT-SECRET-CANARY")
		require.NotContains(t, f.logs.String(), "REPLACEMENT-SECRET-CANARY")
		require.NotContains(t, f.logs.String(), "WRITE-QUERY-CANARY")
		require.NotContains(t, f.logs.String(), original.ConfigKey)
		var saved domain.SystemConfig
		require.NoError(t, f.db.First(&saved, original.ID).Error)
		require.Equal(t, replacement, saved.Value, "nonempty values replace exact bytes")
		require.True(t, saved.UpdatedAt.After(stamp))
	}

	// A key can itself contain a credential. It remains the response key by
	// contract, but must never enter application or SQL logs.
	f.logs.Reset()
	w = f.request(http.MethodPost, `{"config_key":"KEY-SECRET-CANARY","value":"NEW-SECRET-CANARY"}`, "")
	require.Equal(t, http.StatusOK, w.Code)
	assertConfigResponsePrivate(t, w, "NEW-SECRET-CANARY")
	require.NotContains(t, f.logs.String(), "KEY-SECRET-CANARY")
	require.NotContains(t, f.logs.String(), "NEW-SECRET-CANARY")
	var inserted domain.SystemConfig
	require.NoError(t, f.db.Where("config_key = ?", "KEY-SECRET-CANARY").First(&inserted).Error)
	require.Equal(t, "NEW-SECRET-CANARY", inserted.Value)
}

func TestSystemConfigRedactionRejectsInvalidInput(t *testing.T) {
	f := newSystemConfigRouteFixture(t)
	original := domain.SystemConfig{ConfigKey: "SMTP_PASSWORD", Value: "UNCHANGED-SECRET-CANARY", UpdatedAt: time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)}
	require.NoError(t, f.db.Create(&original).Error)
	bodies := []string{
		``, `null`, `[]`, `{}`, `{"value":"PAYLOAD-SECRET-CANARY"}`,
		`{"config_key":null}`, `{"config_key":""}`, `{"config_key":123}`, `{"config_key":true}`, `{"config_key":{}}`,
		`{"config_key":"SMTP_PASSWORD","value":true}`, `{"config_key":"SMTP_PASSWORD","value":123}`, `{"config_key":"SMTP_PASSWORD","value":[]}`, `{"config_key":"SMTP_PASSWORD","value":{"PAYLOAD-SECRET-CANARY":true}}`,
		`{"config_key":"SMTP_PASSWORD","value":"PAYLOAD-SECRET-CANARY"`,
		`{"config_key":"SMTP_PASSWORD","value":"PAYLOAD-SECRET-CANARY"} {"redacted":true}`,
		`{"config_key":"SMTP_PASSWORD","value":"","REDACTED":null}`,
		`{"config_key":"SMTP_PASSWORD","value":"","Redacted":"PAYLOAD-SECRET-CANARY"}`,
	}
	for _, marker := range []string{`null`, `"PAYLOAD-SECRET-CANARY"`, `123`, `[]`, `{}`} {
		for _, value := range []string{`""`, `null`, `"PAYLOAD-SECRET-CANARY"`} {
			bodies = append(bodies, fmt.Sprintf(`{"config_key":"SMTP_PASSWORD","value":%s,"redacted":%s}`, value, marker))
		}
		bodies = append(bodies, fmt.Sprintf(`{"config_key":"SMTP_PASSWORD","redacted":%s,"redacted":false}`, marker))
	}
	for _, marker := range []string{`true`, `false`} {
		for _, value := range []string{`"PAYLOAD-SECRET-CANARY"`, `"********"`, `" "`} {
			bodies = append(bodies, fmt.Sprintf(`{"config_key":"SMTP_PASSWORD","value":%s,"redacted":%s}`, value, marker))
		}
	}
	for _, body := range bodies {
		f.logs.Reset()
		w := f.request(http.MethodPost, body, "?error=INVALID-QUERY-CANARY")
		require.Equal(t, http.StatusBadRequest, w.Code, body)
		require.JSONEq(t, `{"success":false,"error":"Invalid request payload"}`, w.Body.String())
		assertConfigResponsePrivate(t, w, "PAYLOAD-SECRET-CANARY", original.Value)
		for _, canary := range []string{"PAYLOAD-SECRET-CANARY", "INVALID-QUERY-CANARY", original.Value, original.ConfigKey} {
			require.NotContains(t, f.logs.String(), canary)
		}
		var unchanged domain.SystemConfig
		require.NoError(t, f.db.First(&unchanged, original.ID).Error)
		require.Equal(t, original, unchanged)
	}
	var count int64
	require.NoError(t, f.db.Model(&domain.SystemConfig{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSystemConfigRedactionDatabaseErrorsArePrivate(t *testing.T) {
	f := newSystemConfigRouteFixture(t)
	failed := func(tx *gorm.DB) {
		if tx.Statement.Table == "system_configs" {
			tx.AddError(errors.New("DB-ERROR-SECRET-CANARY including stored credential and SQL"))
		}
	}
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("config_failure", failed))
	// A real SQLite failure exercises both SQL interpolation and DB error text
	// during replacement, while the callback simulates a failing list query.
	require.NoError(t, f.db.Exec(`CREATE TRIGGER config_secret_failure BEFORE INSERT ON system_configs
		BEGIN SELECT RAISE(ABORT, 'DB-ERROR-SECRET-CANARY including stored credential and SQL'); END`).Error)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if method == http.MethodPost {
			require.NoError(t, f.db.Callback().Query().Remove("config_failure"))
		}
		f.logs.Reset()
		w := f.request(method, `{"config_key":"ERROR-KEY-SECRET-CANARY","value":"ERROR-VALUE-SECRET-CANARY"}`, "?filter=ERROR-QUERY-SECRET-CANARY")
		require.Equal(t, http.StatusInternalServerError, w.Code)
		message := "Failed to list system configurations"
		if method == http.MethodPost {
			message = "Failed to save system configuration"
		}
		require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, message), w.Body.String())
		canaries := []string{"DB-ERROR-SECRET-CANARY", "ERROR-KEY-SECRET-CANARY", "ERROR-VALUE-SECRET-CANARY", "ERROR-QUERY-SECRET-CANARY"}
		assertConfigResponsePrivate(t, w, canaries...)
		for _, canary := range canaries {
			require.NotContains(t, f.logs.String(), canary)
		}
		require.Contains(t, f.logs.String(), `"component":"system_config"`)
	}
}
