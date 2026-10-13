package modruntime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestBILCustomerSnapshotPreservesIntentAndProvenance(t *testing.T) {
	source := newModuleTestDB(t, "BIL")
	now := time.Now().UTC().Truncate(time.Microsecond)
	pendingID, readyID, customer := uuid.NewString(), uuid.NewString(), "cus_snapshot"
	records := []domain.BillingCustomer{
		{AccountID: 91, Currency: "eur", State: "selected"},
		{AccountID: 92, Currency: "eur", State: "creating", IntentID: &pendingID, IntentCreatedAt: &now, ProviderScope: strings.Repeat("a", 64)},
		{AccountID: 93, Currency: "usd", State: "ready", IntentID: &readyID, IntentCreatedAt: &now, ProviderScope: strings.Repeat("b", 64), CustomerID: &customer},
	}
	for i := range records {
		require.NoError(t, source.Create(&records[i]).Error)
	}
	path := filepath.Join(t.TempDir(), "billing.snapshot.json")
	require.NoError(t, CreateSnapshot(source, "BIL", "customer-generation", path))
	target := newModuleTestDB(t, "BIL")
	require.NoError(t, RestoreSnapshot(context.Background(), target, "BIL", "customer-generation", path, nil))
	var restored []domain.BillingCustomer
	require.NoError(t, target.Order("account_id").Find(&restored).Error)
	require.Len(t, restored, 3)
	normalize := func(rows []domain.BillingCustomer) {
		for i := range rows {
			rows[i].CreatedAt = rows[i].CreatedAt.UTC()
			rows[i].UpdatedAt = rows[i].UpdatedAt.UTC()
			if rows[i].IntentCreatedAt != nil {
				v := rows[i].IntentCreatedAt.UTC()
				rows[i].IntentCreatedAt = &v
			}
		}
	}
	normalize(records)
	normalize(restored)
	require.Equal(t, records, restored)
	for _, foreign := range []any{&domain.Account{}, &domain.AccountLimit{}, &domain.User{}} {
		require.False(t, target.Migrator().HasTable(foreign))
	}
	require.False(t, target.Migrator().HasColumn(&domain.BillingCustomer{}, "portal_url"))
}
