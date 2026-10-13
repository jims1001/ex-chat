package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func campaignPrerequisiteHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "campaign-prerequisite-http-key-12345678", JWTExpirationHours: 24}
}
func TestCampaignPrerequisiteHTTP(t *testing.T) {
	cfg := campaignPrerequisiteHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "campaign-http.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runCampaignPrerequisiteHTTP(t, cfg, db)
}
func runCampaignPrerequisiteHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: "Campaign fixture"}).Error)
	}
	tokens := map[string]string{}
	for _, principal := range []string{"admin", "automation", "agent", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@campaign-http.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		member := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		if principal == "admin" || principal == "foreign" {
			member.Role = domain.RoleAdministrator
		}
		if principal == "foreign" {
			member.AccountID = 92
		}
		if principal == "automation" {
			role := domain.CustomRole{AccountID: 91, Name: "campaign automation", Permissions: `["automation_manage"]`}
			require.NoError(t, db.Create(&role).Error)
			member.CustomRoleID = &role.ID
		}
		require.NoError(t, db.Create(&member).Error)
		var err error
		tokens[principal], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	inbox := domain.Inbox{AccountID: 91, Name: "Campaign inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "campaign-preread-widget"}
	require.NoError(t, db.Create(&inbox).Error)
	company := domain.Company{AccountID: 91, Name: "Campaign company"}
	require.NoError(t, db.Create(&company).Error)
	contact := domain.Contact{AccountID: 91, Name: "Campaign contact", CompanyID: &company.ID}
	require.NoError(t, db.Create(&contact).Error)
	relation := domain.ContactInbox{ContactID: contact.ID, InboxID: inbox.ID, SourceID: "campaign-visitor-source"}
	require.NoError(t, db.Create(&relation).Error)
	visitor, err := auth.GenerateVisitorToken(inbox.ID, contact.ID, relation.SourceID, cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	campaign := domain.Campaign{AccountID: 91, InboxID: inbox.ID, Title: "Trigger", Message: "optional campaign", CampaignType: "ongoing", Status: "active", Audience: "all"}
	require.NoError(t, db.Create(&campaign).Error)
	const marker = "private-campaign-http-diagnostic"
	fault := ""
	hits := 0
	effects := 0
	require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("campaign_http_read", func(tx *gorm.DB) {
		_, count := tx.Statement.Dest.(*int64)
		match := (fault == "campaign" && tx.Statement.Table == "campaigns") ||
			(fault == "contact_count" && tx.Statement.Table == "contacts" && count) ||
			(fault == "contact_rows" && tx.Statement.Table == "contacts" && !count) ||
			(fault == "preload" && tx.Statement.Table == "companies") ||
			(fault == "dedup" && tx.Statement.Table == "campaign_deliveries" && count)
		if match {
			hits++
			_ = tx.AddError(errors.New(marker))
		}
	}))
	for _, db := range []*gorm.DB{db} {
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register("campaign_http_create", func(tx *gorm.DB) {
			if fault != "" {
				effects++
			}
		}))
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register("campaign_http_update", func(tx *gorm.DB) {
			if fault != "" {
				effects++
			}
		}))
	}
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove("campaign_http_read"))
		require.NoError(t, db.Callback().Create().Remove("campaign_http_create"))
		require.NoError(t, db.Callback().Update().Remove("campaign_http_update"))
	})
	request := func(path, principal string, payload any, readFault string) *httptest.ResponseRecorder {
		raw, err := json.Marshal(payload)
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw)))
		req.Header.Set("Content-Type", "application/json")
		if principal == "visitor" {
			req.Header.Set("X-Visitor-Token", visitor)
		} else if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		fault, hits, effects = readFault, 0, 0
		defer func() { fault = "" }()
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	path := fmt.Sprintf("/api/v1/accounts/91/campaigns/%d/trigger", campaign.ID)
	t.Run("generic_prerequisite_failures", func(t *testing.T) {
		for _, read := range []string{"campaign", "contact_count", "contact_rows", "preload", "dedup"} {
			t.Run(read, func(t *testing.T) {
				out := request(path, "admin", nil, read)
				require.Equal(t, 500, out.Code, out.Body.String())
				require.JSONEq(t, `{"success":false,"error":"Campaign prerequisite unavailable"}`, out.Body.String())
				require.NotContains(t, out.Body.String(), marker)
				require.Positive(t, hits)
				require.Zero(t, effects)
				var stored domain.Campaign
				require.NoError(t, db.First(&stored, campaign.ID).Error)
				require.Equal(t, "active", stored.Status)
			})
		}
	})
	t.Run("authority_before_campaign_read", func(t *testing.T) {
		for _, tc := range []struct {
			name, principal string
			status          int
		}{{"unauthenticated", "", 401}, {"agent", "agent", 403}, {"foreign_membership", "foreign", 403}} {
			t.Run(tc.name, func(t *testing.T) {
				out := request(path, tc.principal, nil, "campaign")
				require.Equal(t, tc.status, out.Code, out.Body.String())
				require.Zero(t, hits)
				require.Zero(t, effects)
			})
		}
	})
	t.Run("custom_automation_permission", func(t *testing.T) {
		out := request(path, "automation", nil, "campaign")
		require.Equal(t, 500, out.Code, out.Body.String())
		require.Equal(t, 1, hits)
		require.Zero(t, effects)
	})
	t.Run("foreign_campaign_and_business_rejections", func(t *testing.T) {
		foreign := domain.Campaign{AccountID: 92, InboxID: inbox.ID, Message: "foreign", Status: "active", CampaignType: "ongoing"}
		require.NoError(t, db.Create(&foreign).Error)
		out := request(fmt.Sprintf("/api/v1/accounts/91/campaigns/%d/trigger", foreign.ID), "admin", nil, "")
		require.Equal(t, 400, out.Code, out.Body.String())
		require.NotContains(t, out.Body.String(), foreign.Message)
		for _, status := range []string{"paused", "cancelled", "completed"} {
			require.NoError(t, db.Model(&campaign).Update("status", status).Error)
			out = request(path, "admin", nil, "")
			require.Equal(t, 400, out.Code, out.Body.String())
		}
		require.NoError(t, db.Model(&campaign).Update("status", "active").Error)
	})
	t.Run("optional_failure_preserves_primary_creation", func(t *testing.T) {
		for _, route := range []string{"agent", "widget", "public"} {
			t.Run(route, func(t *testing.T) {
				var beforeCon, beforeMsg, beforeDel int64
				require.NoError(t, db.Model(&domain.Conversation{}).Count(&beforeCon).Error)
				require.NoError(t, db.Model(&domain.Message{}).Count(&beforeMsg).Error)
				require.NoError(t, db.Model(&domain.CampaignDelivery{}).Count(&beforeDel).Error)
				endpoint := "/api/v1/accounts/91/conversations"
				principal := "admin"
				payload := map[string]any{"inbox_id": inbox.ID, "contact_id": contact.ID}
				wantMessages := int64(0)
				if route == "widget" {
					endpoint = "/api/v1/widget/conversations?website_token=" + inbox.WebsiteToken
					principal = "visitor"
					payload = map[string]any{"message": "primary incoming", "source_id": relation.SourceID}
					wantMessages = 1
				}
				if route == "public" {
					endpoint = fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d/conversations", inbox.WebsiteToken, contact.ID)
					principal = "visitor"
					payload = map[string]any{}
				}
				out := request(endpoint, principal, payload, "campaign")
				require.Equal(t, 201, out.Code, out.Body.String())
				require.NotContains(t, out.Body.String(), marker)
				require.Positive(t, hits)
				var afterCon, afterMsg, afterDel int64
				require.NoError(t, db.Model(&domain.Conversation{}).Count(&afterCon).Error)
				require.NoError(t, db.Model(&domain.Message{}).Count(&afterMsg).Error)
				require.NoError(t, db.Model(&domain.CampaignDelivery{}).Count(&afterDel).Error)
				require.Equal(t, beforeCon+1, afterCon)
				require.Equal(t, beforeMsg+wantMessages, afterMsg)
				require.Equal(t, beforeDel, afterDel)
			})
		}
	})
	t.Run("healthy_trigger_envelope_is_unchanged", func(t *testing.T) {
		out := request(path, "admin", nil, "")
		require.Equal(t, 200, out.Code, out.Body.String())
		var body struct {
			Success bool `json:"success"`
			Data    struct {
				Status string `json:"status"`
				Sent   int    `json:"sent_count"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
		require.True(t, body.Success)
		require.Equal(t, "completed", body.Data.Status)
		require.Equal(t, 1, body.Data.Sent)
		var stored domain.Campaign
		require.NoError(t, db.First(&stored, campaign.ID).Error)
		require.Equal(t, "active", stored.Status)
	})
}
