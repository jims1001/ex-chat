package repository

import (
	"path/filepath"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAccountFeatureExplicitFalsePersists(t *testing.T) {
	for _, initial := range []string{"new_row", "existing_true"} {
		t.Run(initial, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "features.db")), &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.AutoMigrate(&domain.AccountFeature{}))
			repo := NewEnterpriseRepository(db)
			if initial == "existing_true" {
				require.NoError(t, repo.SetAccountFeature(91, "captain", true))
			}
			require.NoError(t, repo.SetAccountFeature(91, "captain", false))
			flags, err := repo.GetAccountFeatures(91)
			require.NoError(t, err)
			require.Equal(t, map[string]bool{"captain": false}, flags)
		})
	}
}
