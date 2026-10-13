package repository

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestFirebasePushConfigurationPrivateBoundedRead(t *testing.T) {
	open := func(t *testing.T) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sys.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		require.NoError(t, db.Exec("CREATE TABLE system_configs (config_key, value)").Error)
		return db
	}
	t.Run("exact names and absent values", func(t *testing.T) {
		db := open(t)
		repo := NewSystemConfigRepository(db)
		for key, value := range map[string]string{"firebase_project_id": "wrong", "FIREBASE_PROJECT_ID_EXTRA": "wrong", "SMTP_PASSWORD": "private", "INSTALLATION_NAME": "public", "FIREBASE_PROJECT_ID": "target-project", "FIREBASE_CREDENTIALS": "private-synthetic"} {
			require.NoError(t, db.Exec("INSERT INTO system_configs (config_key, value) VALUES (?, ?)", key, value).Error)
		}
		project, credentials, err := repo.FirebasePushConfiguration(context.Background())
		require.NoError(t, err)
		require.Equal(t, "target-project", project)
		require.Equal(t, "private-synthetic", credentials)
		var count int64
		require.NoError(t, db.Model(&domain.SystemConfig{}).Count(&count).Error)
		require.EqualValues(t, 6, count)
	})
	t.Run("missing pair", func(t *testing.T) {
		db := open(t)
		project, credentials, err := NewSystemConfigRepository(db).FirebasePushConfiguration(context.Background())
		require.NoError(t, err)
		require.Empty(t, project)
		require.Empty(t, credentials)
	})
	for _, kind := range []string{"oversized", "duplicate", "null", "integer", "blob", "invalid utf8", "store unavailable"} {
		t.Run(kind, func(t *testing.T) {
			db := open(t)
			switch kind {
			case "oversized":
				require.NoError(t, db.Exec("INSERT INTO system_configs VALUES (?, ?)", "FIREBASE_CREDENTIALS", strings.Repeat("s", 32769)).Error)
			case "duplicate":
				require.NoError(t, db.Exec("INSERT INTO system_configs VALUES ('FIREBASE_PROJECT_ID','one'),('FIREBASE_PROJECT_ID','two')").Error)
			case "null":
				require.NoError(t, db.Exec("INSERT INTO system_configs VALUES ('FIREBASE_CREDENTIALS',NULL)").Error)
			case "integer":
				require.NoError(t, db.Exec("INSERT INTO system_configs VALUES ('FIREBASE_CREDENTIALS',123)").Error)
			case "blob":
				require.NoError(t, db.Exec("INSERT INTO system_configs VALUES ('FIREBASE_CREDENTIALS',x'0102')").Error)
			case "invalid utf8":
				require.NoError(t, db.Exec("INSERT INTO system_configs VALUES ('FIREBASE_CREDENTIALS',CAST(x'ff' AS TEXT))").Error)
			case "store unavailable":
				require.NoError(t, db.Exec("DROP TABLE system_configs").Error)
			}
			project, credentials, err := NewSystemConfigRepository(db).FirebasePushConfiguration(context.Background())
			require.EqualError(t, err, "Firebase configuration is unavailable")
			require.Empty(t, project)
			require.Empty(t, credentials)
		})
	}
	t.Run("at bound and cancelled context", func(t *testing.T) {
		db := open(t)
		require.NoError(t, db.Exec("INSERT INTO system_configs VALUES (?, ?)", "FIREBASE_CREDENTIALS", strings.Repeat("s", 32768)).Error)
		repo := NewSystemConfigRepository(db)
		_, credentials, err := repo.FirebasePushConfiguration(context.Background())
		require.NoError(t, err)
		require.Len(t, credentials, 32768)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err = repo.FirebasePushConfiguration(ctx)
		require.EqualError(t, err, "Firebase configuration is unavailable")
	})
}
