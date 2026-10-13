package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func enterpriseEnabledValidationCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	require.NoError(t, db.AutoMigrate(&domain.AccountFeature{}))
	repo := repository.NewEnterpriseRepository(db)
	require.NoError(t, repo.SetAccountFeature(91, "existing", true))
	require.NoError(t, repo.SetAccountFeature(92, "existing", false))
	h := NewEnterpriseHandler(repo)
	request := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set(middleware.ContextAccountID, uint(91))
		c.Request = httptest.NewRequest(http.MethodPost, "/features", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h.SetAccountFeature(c)
		return w
	}
	var before []domain.AccountFeature
	require.NoError(t, db.Order("id ASC").Find(&before).Error)
	writes := 0
	rejectWrite := func(tx *gorm.DB) { writes++; tx.AddError(errors.New("validation must not reach a storage write")) }
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_feature_validation_write", rejectWrite))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("reject_feature_validation_write", rejectWrite))
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("reject_feature_validation_write", rejectWrite))
	for name, suffix := range map[string]string{
		"missing": "", "null": `,"enabled":null`, "string": `,"enabled":"false"`,
		"number": `,"enabled":0`, "object": `,"enabled":{}`, "array": `,"enabled":[]`,
	} {
		for _, key := range []string{"existing", "absent"} {
			t.Run(name+"/"+key, func(t *testing.T) {
				w := request(fmt.Sprintf(`{"feature_name":%q%s}`, key, suffix))
				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				require.Zero(t, writes, "invalid boolean must be rejected before storage is called")
				var after []domain.AccountFeature
				require.NoError(t, db.Order("id ASC").Find(&after).Error)
				require.Equal(t, before, after, "validation failure must preserve values, IDs and timestamps and create no absent row")
			})
		}
	}
	require.NoError(t, db.Callback().Create().Remove("reject_feature_validation_write"))
	require.NoError(t, db.Callback().Update().Remove("reject_feature_validation_write"))
	require.NoError(t, db.Callback().Delete().Remove("reject_feature_validation_write"))
	for _, key := range []string{"existing", "absent"} {
		for _, enabled := range []bool{false, true, false} {
			w := request(fmt.Sprintf(`{"feature_name":%q,"enabled":%t}`, key, enabled))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var envelope struct {
				Success bool            `json:"success"`
				Data    map[string]bool `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
			require.True(t, envelope.Success)
			require.Equal(t, enabled, envelope.Data[key])
		}
	}
	other, err := repo.GetAccountFeatures(92)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"existing": false}, other)
}

func TestEnterpriseFeatureEnabledValidationSQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "enabled-validation.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	enterpriseEnabledValidationCases(t, db)
}

func TestPostgresEnterpriseFeatureEnabledValidation(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL feature validation regression")
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
	schema := "ent_validation_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) })
	cfg.RuntimeParams["search_path"] = schema
	conn := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	enterpriseEnabledValidationCases(t, db)
}
