package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func cannedGuardRequest(t *testing.T, db *gorm.DB, method string, account, id uint, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	h := NewOpsHandler(nil, repository.NewCannedResponseRepository(db), nil)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, account) })
	router.POST("/canned", h.CreateCannedResponse)
	router.PUT("/canned/:id", h.UpdateCannedResponse)
	router.DELETE("/canned/:id", h.DeleteCannedResponse)
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	path := "/canned"
	if method != http.MethodPost {
		path += "/" + strconv.FormatUint(uint64(id), 10)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	router.ServeHTTP(out, req)
	return out
}
func cannedRuntimeGuard(t *testing.T, open cannedValidationOpener) {
	for _, op := range []string{"create_duplicate", "update_versioned", "delete_versioned", "update_legacy_duplicate"} {
		t.Run(op, func(t *testing.T) {
			db := open(t)
			runtime := domain.CannedResponse{AccountID: 91, ShortCode: "hello", Content: "runtime", ObjectVersion: 3}
			legacy := domain.CannedResponse{AccountID: 91, ShortCode: "legacy", Content: "legacy"}
			require.NoError(t, db.Create(&runtime).Error)
			require.NoError(t, db.Create(&legacy).Error)
			before := cannedValidationRows(t, db)
			method, id := http.MethodPost, uint(0)
			switch op {
			case "update_versioned":
				method, id = http.MethodPut, runtime.ID
			case "delete_versioned":
				method, id = http.MethodDelete, runtime.ID
			case "update_legacy_duplicate":
				method, id = http.MethodPut, legacy.ID
			}
			out := cannedGuardRequest(t, db, method, 91, id, map[string]any{"short_code": "\u2003HELLO ", "content": "changed", "object_version": 0, "expected_version": 3})
			require.Equal(t, 409, out.Code, out.Body.String())
			require.Equal(t, before, cannedValidationRows(t, db))
		})
	}
	t.Run("legacy_inputs_and_duplicates_remain_compatible", func(t *testing.T) {
		db := open(t)
		for i := 0; i < 2; i++ {
			out := cannedGuardRequest(t, db, http.MethodPost, 91, 0, map[string]any{"short_code": " MiXeD ", "content": strings.Repeat("x", 70000), "object_version": 99})
			require.Equal(t, 201, out.Code, out.Body.String())
		}
		rows := cannedValidationRows(t, db)
		require.Len(t, rows, 2)
		for _, row := range rows {
			require.Zero(t, row.ObjectVersion)
			require.Equal(t, "MiXeD", row.ShortCode)
			require.Len(t, row.Content, 70000)
		}
	})
	t.Run("cross_account_runtime_id_is_not_disclosed", func(t *testing.T) {
		db := open(t)
		row := domain.CannedResponse{AccountID: 92, ShortCode: "foreign", Content: "secret", ObjectVersion: 1}
		require.NoError(t, db.Create(&row).Error)
		out := cannedGuardRequest(t, db, http.MethodPut, 91, row.ID, map[string]any{"short_code": "hello", "content": "changed"})
		require.Equal(t, 404, out.Code)
		out = cannedGuardRequest(t, db, http.MethodDelete, 91, row.ID, nil)
		require.Equal(t, 200, out.Code)
		require.NotContains(t, out.Body.String(), "secret")
		require.Len(t, cannedValidationRows(t, db), 1)
	})
}
func TestCannedResponseRuntimeRESTGuard(t *testing.T) { cannedRuntimeGuard(t, cannedValidationDB) }
