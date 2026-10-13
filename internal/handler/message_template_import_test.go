package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func templateImportRequest(db *gorm.DB, method, path, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := NewInboxHandler(repository.NewInboxRepositoryWithOwnerPorts(db, nil, nil, nil), nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(91)) })
	r.GET("/inboxes/:id/message_templates", h.ListMessageTemplates)
	r.POST("/inboxes/:id/message_templates", h.CreateMessageTemplate)
	r.DELETE("/inboxes/:id/message_templates/:template_id", h.DeleteMessageTemplate)
	r.POST("/inboxes/:id/sync_templates", h.SyncMessageTemplates)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	return out
}

func templateImportSeed(t *testing.T, db *gorm.DB, config string) {
	t.Helper()
	rows := []domain.Inbox{
		{ID: 11, AccountID: 91, Name: "Local templates", WebsiteToken: "template-local", ProviderConfig: config},
		{ID: 12, AccountID: 92, Name: "Foreign templates", WebsiteToken: "template-foreign"},
	}
	require.NoError(t, db.Create(&rows).Error)
}

func templateImportRows(t *testing.T, db *gorm.DB) []domain.InboxMessageTemplate {
	t.Helper()
	var rows []domain.InboxMessageTemplate
	require.NoError(t, db.Order("id").Find(&rows).Error)
	return rows
}

func runMessageTemplateImportCorrections(t *testing.T, open csatConfigOpener) {
	t.Run("missing_status_is_unknown", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, `{"templates":[{"name":"imported"}]}`)
		out := templateImportRequest(db, http.MethodPost, "/inboxes/11/message_templates", `{"name":"created"}`)
		require.Equal(t, http.StatusCreated, out.Code, out.Body.String())
		rows := templateImportRows(t, db)
		require.Len(t, rows, 1)
		require.Empty(t, rows[0].Status)
		require.Empty(t, rows[0].SyncStatus)
		require.Nil(t, rows[0].LastSyncAt)
		out = templateImportRequest(db, http.MethodPost, "/inboxes/11/sync_templates", "")
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		rows = templateImportRows(t, db)
		require.Len(t, rows, 2)
		require.Empty(t, rows[1].Status)
		require.Equal(t, "imported", rows[1].SyncStatus)
	})
	t.Run("direct_model_and_repository_defaults", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, "")
		direct := domain.InboxMessageTemplate{AccountID: 91, InboxID: 11, Name: "model"}
		require.NoError(t, db.Create(&direct).Error)
		repo := repository.NewInboxRepositoryWithOwnerPorts(db, nil, nil, nil)
		local := domain.InboxMessageTemplate{AccountID: 91, InboxID: 11, Name: "repository"}
		require.NoError(t, repo.CreateMessageTemplate(&local))
		for _, row := range templateImportRows(t, db) {
			require.Empty(t, row.Status)
			require.Empty(t, row.SyncStatus)
			require.Nil(t, row.LastSyncAt)
		}
		local.Status = "APPROVED"
		require.NoError(t, repo.CreateMessageTemplate(&local))
		local.Status = ""
		require.NoError(t, repo.CreateMessageTemplate(&local))
		require.Empty(t, local.Status, "direct local upsert must retain its explicit clear semantics")
	})
	t.Run("import_status_presence_preserves_only_omitted", func(t *testing.T) {
		for _, tc := range []struct{ name, field, want string }{
			{"omitted", "", "preserve"}, {"empty", `,"status":""`, ""},
			{"blank", `,"status":"  "`, ""}, {"null", `,"status":null`, ""},
			{"explicit", `,"status":"PAUSED"`, "PAUSED"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				db := open(t)
				templateImportSeed(t, db, `{"templates":[{"name":"approved"`+tc.field+`},{"name":"rejected"`+tc.field+`},{"name":"new"`+tc.field+`}]}`)
				old := []domain.InboxMessageTemplate{
					{AccountID: 91, InboxID: 11, Name: "approved", Status: "APPROVED", SyncStatus: "synced", ProviderTemplateID: "legacy-provider-reference"},
					{AccountID: 91, InboxID: 11, Name: "rejected", Status: "REJECTED", SyncStatus: "sync_failed"},
					{AccountID: 91, InboxID: 11, Name: "absent_from_config", Status: "PENDING", SyncStatus: "synced"},
				}
				require.NoError(t, db.Create(&old).Error)
				before := templateImportRows(t, db)
				repo := repository.NewInboxRepositoryWithOwnerPorts(db, nil, nil, nil)
				for i := 0; i < 2; i++ {
					_, err := repo.SyncMessageTemplates(91, 11)
					require.NoError(t, err)
					rows := templateImportRows(t, db)
					require.Len(t, rows, 4)
					for index := 0; index < 2; index++ {
						want := tc.want
						if want == "preserve" {
							want = old[index].Status
						}
						require.Equal(t, want, rows[index].Status)
						require.Equal(t, old[index].ID, rows[index].ID)
						require.True(t, before[index].CreatedAt.Equal(rows[index].CreatedAt))
						require.Equal(t, "imported", rows[index].SyncStatus)
						require.NotNil(t, rows[index].LastSyncAt)
					}
					require.Equal(t, "legacy-provider-reference", rows[0].ProviderTemplateID)
					require.Equal(t, before[2], rows[2], "absence is not deletion or metadata refresh")
					want := tc.want
					if want == "preserve" {
						want = ""
					}
					require.Equal(t, want, rows[3].Status)
					require.Greater(t, rows[3].ID, old[2].ID)
				}
			})
		}
	})

	t.Run("get_never_imports_or_invents_attempt", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, `{"templates":[{"name":"must_not_import"}]}`)
		out := templateImportRequest(db, http.MethodGet, "/inboxes/11/message_templates", "")
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		require.Empty(t, templateImportRows(t, db))
		var result struct {
			Templates []any          `json:"templates"`
			Meta      map[string]any `json:"meta"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &result))
		require.Empty(t, result.Templates)
		require.Nil(t, result.Meta["last_sync_attempt_at"])
	})
	t.Run("write_targets_are_scoped", func(t *testing.T) {
		for _, id := range []string{"12", "999"} {
			t.Run(id, func(t *testing.T) {
				db := open(t)
				templateImportSeed(t, db, "")
				for _, request := range []struct{ method, path, body string }{
					{http.MethodPost, "/inboxes/" + id + "/message_templates", `{"name":"invalid"}`},
					{http.MethodDelete, "/inboxes/" + id + "/message_templates/123", ""},
					{http.MethodPost, "/inboxes/" + id + "/sync_templates", ""},
				} {
					out := templateImportRequest(db, request.method, request.path, request.body)
					require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
				}
				require.Empty(t, templateImportRows(t, db))
			})
		}
	})
	t.Run("invalid_config_fails_without_effects", func(t *testing.T) {
		for _, config := range []struct{ name, value string }{
			{"malformed", `{`}, {"null", `null`}, {"array", `[]`}, {"status_type", `{"templates":[{"name":"first"},{"name":"bad","status":5}]}`}, {"error_without_templates", `{"sync_error":"unavailable"}`},
			{"error_with_templates", `{"error":"unavailable","templates":[{"name":"new"}]}`},
			{"invalid_second_row", `{"templates":[{"name":"first"},{"name":" "}]}`},
		} {
			t.Run(config.name, func(t *testing.T) {
				db := open(t)
				templateImportSeed(t, db, config.value)
				old := domain.InboxMessageTemplate{ID: 41, AccountID: 91, InboxID: 11, Name: "legacy", Status: "APPROVED", SyncStatus: "synced"}
				require.NoError(t, db.Create(&old).Error)
				before := templateImportRows(t, db)
				out := templateImportRequest(db, http.MethodPost, "/inboxes/11/sync_templates", "")
				require.GreaterOrEqual(t, out.Code, 400, out.Body.String())
				require.Equal(t, before, templateImportRows(t, db))
			})
		}
	})
	t.Run("storage_failure_rolls_back", func(t *testing.T) {
		for _, fail := range []string{"create", "update", "query", "lookup"} {
			t.Run(fail, func(t *testing.T) {
				db := open(t)
				templateImportSeed(t, db, `{"templates":[{"name":"first"},{"name":"second"}]}`)
				old := domain.InboxMessageTemplate{ID: 41, AccountID: 91, InboxID: 11, Name: "second", Status: "APPROVED", SyncStatus: "synced"}
				if fail == "update" {
					require.NoError(t, db.Create(&old).Error)
				}
				before := templateImportRows(t, db)
				calls := 0
				inject := func(tx *gorm.DB) {
					if tx.Statement.Table != "inbox_message_templates" {
						return
					}
					calls++
					if (fail == "create" && calls == 2) || (fail == "update" && calls == 1) || (fail == "query" && calls == 3) || (fail == "lookup" && calls == 1) {
						tx.AddError(errors.New("injected template failure"))
					}
				}
				name := "test:template_import_failure"
				if fail == "create" {
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, inject))
				}
				if fail == "update" {
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, inject))
				}
				if fail == "query" || fail == "lookup" {
					require.NoError(t, db.Callback().Query().Before("gorm:query").Register(name, inject))
				}
				out := templateImportRequest(db, http.MethodPost, "/inboxes/11/sync_templates", "")
				if fail == "create" {
					require.NoError(t, db.Callback().Create().Remove(name))
				}
				if fail == "update" {
					require.NoError(t, db.Callback().Update().Remove(name))
				}
				if fail == "query" || fail == "lookup" {
					require.NoError(t, db.Callback().Query().Remove(name))
				}
				require.Equal(t, http.StatusInternalServerError, out.Code, out.Body.String())
				require.Equal(t, before, templateImportRows(t, db))
			})
		}
	})
	t.Run("outer_transaction_rolls_back_local_writes", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, `{"templates":[{"name":"imported"}]}`)
		err := db.Transaction(func(tx *gorm.DB) error {
			repo := repository.NewInboxRepositoryWithOwnerPorts(tx, nil, nil, nil)
			require.NoError(t, repo.CreateMessageTemplate(&domain.InboxMessageTemplate{AccountID: 91, InboxID: 11, Name: "local"}))
			_, err := repo.SyncMessageTemplates(91, 11)
			require.NoError(t, err)
			return errors.New("outer receipt/event transaction failed")
		})
		require.Error(t, err)
		require.Empty(t, templateImportRows(t, db))
	})
	t.Run("inbox_storage_failures_are_visible", func(t *testing.T) {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
			t.Run(method, func(t *testing.T) {
				db := open(t)
				templateImportSeed(t, db, "")
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:inbox_failure", func(tx *gorm.DB) {
					if tx.Statement.Table == "inboxes" {
						tx.AddError(errors.New("injected inbox failure"))
					}
				}))
				path := "/inboxes/11/message_templates"
				if method == http.MethodDelete {
					path += "/123"
				}
				out := templateImportRequest(db, method, path, `{"name":"new"}`)
				require.Equal(t, http.StatusInternalServerError, out.Code, out.Body.String())
				require.NoError(t, db.Callback().Query().Remove("test:inbox_failure"))
				require.Empty(t, templateImportRows(t, db))
			})
		}
	})

	t.Run("empty_config_does_not_refresh_metadata", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, "")
		oldTime := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
		old := domain.InboxMessageTemplate{ID: 41, AccountID: 91, InboxID: 11, Name: "legacy", Status: "APPROVED", SyncStatus: "sync_failed", LastSyncAt: &oldTime}
		require.NoError(t, db.Create(&old).Error)
		before := templateImportRows(t, db)
		out := templateImportRequest(db, http.MethodPost, "/inboxes/11/sync_templates", "")
		require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		require.Equal(t, before, templateImportRows(t, db))
		require.Contains(t, out.Body.String(), `"status":"imported"`)
		require.Contains(t, out.Body.String(), `"synced_at":null`)
		require.NotContains(t, out.Body.String(), "last_sync_attempt_at")
	})
	t.Run("legacy_schema_defaults_and_ids", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, "")
		require.NoError(t, db.Migrator().DropTable(&domain.InboxMessageTemplate{}))
		require.NoError(t, db.Table("inbox_message_templates").AutoMigrate(&legacyImportTemplate{}))
		old := legacyImportTemplate{ID: 41, AccountID: 91, InboxID: 11, Name: "approved", Status: "APPROVED", SyncStatus: "synced"}
		require.NoError(t, db.Table("inbox_message_templates").Create(&old).Error)
		require.NoError(t, database.AutoMigrateModule(db, "CHN"))
		require.NoError(t, database.AutoMigrateModule(db, "CHN"))
		require.NoError(t, db.Exec("INSERT INTO inbox_message_templates (id, account_id, inbox_id, name) VALUES (42, 91, 11, 'unknown')").Error)
		rows := templateImportRows(t, db)
		require.Len(t, rows, 2)
		require.Equal(t, uint(41), rows[0].ID)
		require.Equal(t, "APPROVED", rows[0].Status)
		require.Equal(t, "synced", rows[0].SyncStatus)
		require.Equal(t, uint(42), rows[1].ID)
		require.Empty(t, rows[1].Status)
		require.Empty(t, rows[1].SyncStatus)
	})
	t.Run("repository_rejects_invalid_owner", func(t *testing.T) {
		db := open(t)
		templateImportSeed(t, db, "")
		repo := repository.NewInboxRepositoryWithOwnerPorts(db, nil, nil, nil)
		for _, inboxID := range []uint{12, 999} {
			tmpl := domain.InboxMessageTemplate{AccountID: 91, InboxID: inboxID, Name: fmt.Sprint(inboxID)}
			require.Error(t, repo.CreateMessageTemplate(&tmpl))
			require.Error(t, repo.DeleteMessageTemplate(91, inboxID, 1))
		}
		require.Empty(t, templateImportRows(t, db))
	})
}

type legacyImportTemplate struct {
	ID                 uint   `gorm:"primaryKey"`
	AccountID          uint   `gorm:"index;not null"`
	InboxID            uint   `gorm:"index;not null"`
	Name               string `gorm:"size:255;not null;index"`
	Status             string `gorm:"size:50;not null;default:'APPROVED'"`
	Category           string `gorm:"size:50;not null;default:'UTILITY'"`
	Language           string `gorm:"size:20;not null;default:'en_US'"`
	Components         string `gorm:"type:text"`
	ProviderTemplateID string `gorm:"size:255"`
	RejectedReason     string `gorm:"type:text"`
	LastSyncAt         *time.Time
	SyncStatus         string `gorm:"size:50;default:'synced'"`
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func TestMessageTemplateImportCorrections(t *testing.T) {
	runMessageTemplateImportCorrections(t, csatConfigDB)
}
