package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type cannedValidationOpener func(*testing.T) *gorm.DB

func cannedValidationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "canned.sqlite")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	prepareCannedValidationDB(t, db)
	return db
}

func prepareCannedValidationDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&domain.CannedResponse{}))
}

func cannedValidationRequest(t *testing.T, db *gorm.DB, method string, accountID, id uint, shortcode, content string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewOpsHandler(nil, repository.NewCannedResponseRepository(db), nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, accountID); c.Next() })
	router.POST("/canned", h.CreateCannedResponse)
	router.PUT("/canned/:id", h.UpdateCannedResponse)
	body, err := json.Marshal(map[string]string{"short_code": shortcode, "content": content})
	require.NoError(t, err)
	path := "/canned"
	if method == http.MethodPut {
		path += "/" + strconv.FormatUint(uint64(id), 10)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	return out
}

func cannedValidationRows(t *testing.T, db *gorm.DB) []domain.CannedResponse {
	t.Helper()
	var rows []domain.CannedResponse
	require.NoError(t, db.Order("id").Find(&rows).Error)
	return rows
}

func testCannedResponseUpdateNeverResurrectsDeletedRecord(t *testing.T, open cannedValidationOpener) {
	db := open(t)
	repo := repository.NewCannedResponseRepository(db)
	row := domain.CannedResponse{AccountID: 91, ShortCode: "original", Content: "original"}
	require.NoError(t, repo.Create(&row))
	stale, err := repo.FindByID(91, row.ID)
	require.NoError(t, err)
	require.NotNil(t, stale)
	require.NoError(t, repo.Delete(91, row.ID))
	stale.Content = "stale update"
	err = repo.Update(stale)
	require.Empty(t, cannedValidationRows(t, db), "updating a previously loaded row must not recreate a deleted record")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCannedResponseUpdateNeverResurrectsDeletedRecord(t *testing.T) {
	testCannedResponseUpdateNeverResurrectsDeletedRecord(t, cannedValidationDB)
}

func testCannedResponseRESTRejectsBlankShortcodeAfterTrim(t *testing.T, open cannedValidationOpener) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range []struct{ name, code string }{
			{"empty", ""},
			{"ascii_space", " "},
			{"control_whitespace", "\t\n"},
			{"unicode_whitespace", "\u2003\u00a0"},
		} {
			t.Run(method+"_"+tc.name, func(t *testing.T) {
				db := open(t)
				row := domain.CannedResponse{AccountID: 91, ShortCode: "original", Content: "original"}
				require.NoError(t, db.Create(&row).Error)
				before := cannedValidationRows(t, db)
				writes := 0
				countWrite := func(*gorm.DB) { writes++ }
				require.NoError(t, db.Callback().Create().Before("gorm:create").Register("canned_validation_writes", countWrite))
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("canned_validation_writes", countWrite))
				require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("canned_validation_writes", countWrite))

				out := cannedValidationRequest(t, db, method, 91, row.ID, tc.code, "changed")
				require.Equal(t, http.StatusBadRequest, out.Code, out.Body.String())
				require.Zero(t, writes, "blank shortcode must be rejected before a write is attempted")
				require.Equal(t, before, cannedValidationRows(t, db), "rejected create/update must preserve every stored field")
			})
		}
	}
}

func TestCannedResponseRESTRejectsBlankShortcodeAfterTrim(t *testing.T) {
	testCannedResponseRESTRejectsBlankShortcodeAfterTrim(t, cannedValidationDB)
}

func testCannedResponseRESTDeleteBetweenLookupAndUpdate(t *testing.T, open cannedValidationOpener) {
	db := open(t)
	repo := repository.NewCannedResponseRepository(db)
	row := domain.CannedResponse{AccountID: 91, ShortCode: "original", Content: "original"}
	require.NoError(t, repo.Create(&row))
	deleted := false
	// Query callbacks run after the row has been loaded and its cursor closed.
	// Delete here to reproduce the handler's stale lookup without timing sleeps.
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("canned_delete_after_lookup", func(tx *gorm.DB) {
		loaded, ok := tx.Statement.Dest.(*domain.CannedResponse)
		if !deleted && ok && tx.Error == nil && loaded.ID == row.ID {
			deleted = true
			require.NoError(t, repo.Delete(row.AccountID, row.ID))
		}
	}))
	out := cannedValidationRequest(t, db, http.MethodPut, 91, row.ID, "new-code", "stale update")
	require.True(t, deleted, "the row must be deleted after the handler successfully loaded it")
	require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
	require.Empty(t, cannedValidationRows(t, db), "REST update must not resurrect the deleted row")
}

func TestCannedResponseRESTDeleteBetweenLookupAndUpdate(t *testing.T) {
	testCannedResponseRESTDeleteBetweenLookupAndUpdate(t, cannedValidationDB)
}

func testCannedResponseRESTUpdatePreservesLegacyValues(t *testing.T, open cannedValidationOpener) {
	db := open(t)
	createdAt := time.Date(2021, 3, 4, 5, 6, 7, 123456000, time.UTC)
	rows := []domain.CannedResponse{
		{AccountID: 91, ShortCode: "before", Content: "before", CreatedAt: createdAt, UpdatedAt: createdAt},
		{AccountID: 91, ShortCode: "MiXeD", Content: "legacy duplicate one", CreatedAt: createdAt, UpdatedAt: createdAt},
		{AccountID: 91, ShortCode: "MiXeD", Content: "legacy duplicate two", CreatedAt: createdAt, UpdatedAt: createdAt},
		{AccountID: 92, ShortCode: "MiXeD", Content: "foreign tenant", CreatedAt: createdAt, UpdatedAt: createdAt},
	}
	require.NoError(t, db.Create(&rows).Error)
	before := cannedValidationRows(t, db)
	content := " \tCase-sensitive Content\n<b>Keep markup and whitespace</b> \n"
	out := cannedValidationRequest(t, db, http.MethodPut, 91, rows[0].ID, " \tMiXeD\u2003 ", content)
	require.Equal(t, http.StatusOK, out.Code, out.Body.String())
	var payload struct {
		Data domain.CannedResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &payload))
	require.Equal(t, rows[0].ID, payload.Data.ID)
	require.Equal(t, uint(91), payload.Data.AccountID)
	require.Equal(t, "MiXeD", payload.Data.ShortCode)
	require.Equal(t, content, payload.Data.Content)
	require.True(t, createdAt.Equal(payload.Data.CreatedAt), "response must preserve created_at")
	require.True(t, payload.Data.UpdatedAt.After(createdAt))
	after := cannedValidationRows(t, db)
	require.Len(t, after, len(before), "update must not add or deduplicate rows")
	require.Equal(t, before[0].ID, after[0].ID)
	require.Equal(t, before[0].AccountID, after[0].AccountID)
	require.Equal(t, "MiXeD", after[0].ShortCode)
	require.Equal(t, content, after[0].Content)
	require.True(t, before[0].CreatedAt.Equal(after[0].CreatedAt), "update must preserve stored created_at")
	require.True(t, after[0].UpdatedAt.After(before[0].UpdatedAt))
	// PostgreSQL stores timestamp values at microsecond precision.
	require.WithinDuration(t, payload.Data.UpdatedAt, after[0].UpdatedAt, time.Microsecond)
	require.Equal(t, before[1:], after[1:], "legacy duplicates and foreign-account rows must remain unchanged")
}

func TestCannedResponseRESTUpdatePreservesLegacyValues(t *testing.T) {
	testCannedResponseRESTUpdatePreservesLegacyValues(t, cannedValidationDB)
}

func testCannedResponseRESTUpdateRejectsWrongTenantOrMissing(t *testing.T, open cannedValidationOpener) {
	for _, target := range []string{"wrong_tenant", "missing"} {
		t.Run(target, func(t *testing.T) {
			db := open(t)
			rows := []domain.CannedResponse{{AccountID: 91, ShortCode: "own", Content: "own"}, {AccountID: 92, ShortCode: "foreign", Content: "foreign"}}
			require.NoError(t, db.Create(&rows).Error)
			before := cannedValidationRows(t, db)
			id := rows[1].ID
			if target == "missing" {
				id++
			}
			out := cannedValidationRequest(t, db, http.MethodPut, 91, id, "changed", "changed")
			require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
			require.Equal(t, before, cannedValidationRows(t, db))
		})
	}
}

func TestCannedResponseRESTUpdateRejectsWrongTenantOrMissing(t *testing.T) {
	testCannedResponseRESTUpdateRejectsWrongTenantOrMissing(t, cannedValidationDB)
}

func testCannedResponseRepositoryUpdateRejectsWrongTenantOrMissing(t *testing.T, open cannedValidationOpener) {
	for _, target := range []string{"wrong_tenant", "missing"} {
		t.Run(target, func(t *testing.T) {
			db := open(t)
			row := domain.CannedResponse{AccountID: 92, ShortCode: "foreign", Content: "foreign"}
			require.NoError(t, db.Create(&row).Error)
			before := cannedValidationRows(t, db)
			attempt := row
			attempt.AccountID = 91
			attempt.ShortCode = "changed"
			attempt.Content = "changed"
			if target == "missing" {
				attempt.ID++
			}
			err := repository.NewCannedResponseRepository(db).Update(&attempt)
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Equal(t, before, cannedValidationRows(t, db), "scoped update must neither overwrite a foreign row nor insert a missing row")
		})
	}
}

func TestCannedResponseRepositoryUpdateRejectsWrongTenantOrMissing(t *testing.T) {
	testCannedResponseRepositoryUpdateRejectsWrongTenantOrMissing(t, cannedValidationDB)
}

// The PostgreSQL wrapper runs these exact same regression cases against its
// disposable schemas, rather than maintaining a separate approximation.
func runCannedResponseWriteCorrections(t *testing.T, open cannedValidationOpener) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, cannedValidationOpener)
	}{
		{"repository_deleted_update", testCannedResponseUpdateNeverResurrectsDeletedRecord},
		{"blank_shortcode", testCannedResponseRESTRejectsBlankShortcodeAfterTrim},
		{"rest_delete_between_lookup_update", testCannedResponseRESTDeleteBetweenLookupAndUpdate},
		{"rest_update_preserves_legacy_values", testCannedResponseRESTUpdatePreservesLegacyValues},
		{"rest_wrong_tenant_or_missing", testCannedResponseRESTUpdateRejectsWrongTenantOrMissing},
		{"repository_wrong_tenant_or_missing", testCannedResponseRepositoryUpdateRejectsWrongTenantOrMissing},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, open) })
	}
}
