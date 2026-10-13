package handler

import (
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// This is the same five-owner fixture used by the SQLite policy regressions,
// with independent disposable PostgreSQL schemas and no cross-owner tables.
func TestPostgresCallReadAuthorizationOwnerFacts(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL Call read regression")
	}
	cfg, err := pgx.ParseConfig(dsn)
	require.NoError(t, err)
	hosts := []string{cfg.Host}
	for _, fallback := range cfg.Fallbacks {
		hosts = append(hosts, fallback.Host)
	}
	for _, host := range hosts {
		ip := net.ParseIP(host)
		require.True(t, host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	}
	cfg.ConnectTimeout = 5 * time.Second
	admin := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })
	fixture := func(t *testing.T) *callReadOwnerFixture {
		return newCallReadOwnerFixtureWithOpener(t, func(owner string, queryLogger gormlogger.Interface) *gorm.DB {
			schema := "call_read_" + strings.ToLower(owner) + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			_, err := admin.Exec("CREATE SCHEMA " + schema)
			require.NoError(t, err)
			t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
			ownerConfig := cfg.Copy()
			ownerConfig.RuntimeParams["search_path"] = schema
			conn := stdlib.OpenDB(*ownerConfig)
			db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: queryLogger})
			require.NoError(t, err)
			return db
		})
	}
	t.Run("roles_and_current_membership", func(t *testing.T) {
		f := fixture(t)
		require.NoError(t, f.stores["RTM"].Model(&domain.Call{}).Where("id = ?", f.call.ID).Updates(map[string]any{"agent_id": nil, "conversation_id": nil}).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		role := domain.CustomRole{AccountID: f.call.AccountID, Name: "report", Permissions: `["报表:查看"]`}
		require.NoError(t, f.stores["TEN"].Create(&role).Error)
		require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Update("custom_role_id", role.ID).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		require.NoError(t, f.stores["TEN"].Model(&role).Update("permissions", `["contact_manage"]`).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Updates(map[string]any{"role": role.Name, "custom_role_id": nil}).Error)
		require.NoError(t, f.stores["TEN"].Model(&role).Update("permissions", `["report_manage"]`).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Update("custom_role_id", 9999).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Update("role", domain.RoleAdministrator).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		require.NoError(t, f.stores["TEN"].Where("account_id = ?", f.call.AccountID).Delete(&domain.AccountUser{}).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusForbidden)
	})
	t.Run("inbox_or_team_and_custom_scope", func(t *testing.T) {
		f := fixture(t)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		role := domain.CustomRole{AccountID: f.call.AccountID, Name: "participants", Permissions: `["conversation_participating_manage"]`}
		require.NoError(t, f.stores["TEN"].Create(&role).Error)
		require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Update("custom_role_id", role.ID).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["CON"].Create(&domain.ConversationParticipant{ConversationID: *f.call.ConversationID, UserID: f.user.ID}).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		require.NoError(t, f.stores["CHN"].Where("inbox_id = ?", f.call.InboxID).Delete(&domain.InboxMember{}).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		team := domain.Team{ID: 701, AccountID: f.call.AccountID, Name: "Voice team"}
		require.NoError(t, f.stores["TEN"].Create(&team).Error)
		require.NoError(t, f.stores["TEN"].Create(&domain.TeamMember{TeamID: team.ID, UserID: f.user.ID}).Error)
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("team_id", team.ID).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801/candidates", nil).Code)
		require.NoError(t, f.stores["TEN"].Where("team_id = ?", team.ID).Delete(&domain.TeamMember{}).Error)
		assertCallReadFailure(t, f.request("calls/801/candidates", nil), http.StatusNotFound)
	})
	t.Run("owner_and_conversation_reference", func(t *testing.T) {
		f := fixture(t)
		require.NoError(t, f.stores["RTM"].Model(&domain.Call{}).Where("id = ?", f.call.ID).Update("agent_id", 999).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["RTM"].Model(&domain.Call{}).Where("id = ?", f.call.ID).Updates(map[string]any{"agent_id": f.user.ID, "conversation_id": 401}).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound) // display-only match
		require.NoError(t, f.stores["RTM"].Model(&domain.Call{}).Where("id = ?", f.call.ID).Update("conversation_id", *f.call.ConversationID).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("contact_id", 999).Error)
		assertCallReadFailure(t, f.request("whatsapp_calls/provider-private-marker", nil), http.StatusNotFound)
	})
	t.Run("ambiguous_historical_reference", func(t *testing.T) {
		f := fixture(t)
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("display_id", *f.call.ConversationID).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
		conflict := domain.Conversation{ID: 302, DisplayID: *f.call.ConversationID, AccountID: f.call.AccountID, InboxID: f.call.InboxID, ContactID: f.call.ContactID}
		require.NoError(t, f.stores["CON"].Create(&conflict).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["CON"].Model(&conflict).Update("contact_id", 999).Error)
		require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
	})
	t.Run("tenant_isolation", func(t *testing.T) {
		f := fixture(t)
		require.NoError(t, f.stores["CUS"].Model(&domain.Contact{}).Where("id = ?", f.call.ContactID).Update("account_id", 102).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["CUS"].Model(&domain.Contact{}).Where("id = ?", f.call.ContactID).Update("account_id", f.call.AccountID).Error)
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("account_id", 102).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("account_id", f.call.AccountID).Error)
		role := domain.CustomRole{AccountID: 102, Name: "foreign report", Permissions: `["report_view"]`}
		require.NoError(t, f.stores["TEN"].Create(&role).Error)
		require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Update("custom_role_id", role.ID).Error)
		assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
	})
}
