package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestReportCountAuthorityClonesPreserveOwnerStores(t *testing.T) {
	stores := make([]*gorm.DB, 7)
	for i := range stores {
		db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conn, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		stores[i] = db
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	user := NewUserRepository(stores[0])
	account := NewAccountRepositoryWithUserPort(stores[1], user)
	users := user.ReportCountUserRead(ctx)
	accounts := account.ReportCountAccountRead(ctx)
	require.NotSame(t, user, users)
	require.NotSame(t, account, accounts)
	require.NotSame(t, user, accounts.users)
	for _, pair := range [][2]*gorm.DB{{users.db, stores[0]}, {accounts.db, stores[1]}, {accounts.users.db, stores[0]}} {
		require.Same(t, pair[1].Statement.ConnPool, pair[0].Statement.ConnPool)
		require.Equal(t, ctx, pair[0].Statement.Context)
	}
	original := NewChannelAuthEnterpriseRepositoryWithOwnerStores(stores[0], stores[1], stores[2], stores[3], stores[4], stores[5], stores[6]).(*channelAuthEnterpriseRepository)
	scoped, err := ReportCountIAMRead(ctx, original)
	require.NoError(t, err)
	clone := scoped.(*channelAuthEnterpriseRepository)
	require.NotSame(t, original, clone)
	require.NotSame(t, original.channelAuthIAMRepository, clone.channelAuthIAMRepository)
	require.Same(t, stores[0].Statement.ConnPool, clone.channelAuthIAMRepository.db.Statement.ConnPool)
	require.Equal(t, ctx, clone.channelAuthIAMRepository.db.Statement.Context)
	require.Same(t, original.channelAuthRTMRepository, clone.channelAuthRTMRepository)
	require.Same(t, original.channelAuthMIGRepository, clone.channelAuthMIGRepository)
	require.Same(t, original.channelAuthRPTRepository, clone.channelAuthRPTRepository)
	require.Same(t, original.channelAuthENTRepository, clone.channelAuthENTRepository)
	require.Same(t, original.channelAuthBILRepository, clone.channelAuthBILRepository)
	require.Same(t, original.channelAuthTENRepository, clone.channelAuthTENRepository)
	require.NotEqual(t, ctx, user.db.Statement.Context)
	require.NotEqual(t, ctx, account.db.Statement.Context)
	require.NotEqual(t, ctx, original.channelAuthIAMRepository.db.Statement.Context)
	cancel()
	_, err = clone.IsTokenRevoked("private credential")
	require.ErrorIs(t, err, context.Canceled)
}
