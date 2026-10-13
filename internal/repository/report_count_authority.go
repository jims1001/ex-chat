package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ReportCountUserRead clones this owner's existing handle, including its store
// and configuration. It does not rebind IAM reads to the router's shared store.
func (r *UserRepository) ReportCountUserRead(ctx context.Context) *UserRepository {
	clone := *r
	clone.db = reportCountReadSession(ctx, r.db)
	return &clone
}
func (r *AccountRepository) ReportCountAccountRead(ctx context.Context) *AccountRepository {
	clone := *r
	clone.db = reportCountReadSession(ctx, r.db)
	if r.users != nil {
		clone.users = r.users.ReportCountUserRead(ctx)
	}
	return &clone
}

// ReportCountIAMRead preserves the concrete legacy authority implementation and
// its IAM store. Other owner handles in an enterprise adapter are left intact;
// AuthMiddleware only reads IAM revocation through this port.
func ReportCountIAMRead(ctx context.Context, owner ChannelIAMRepository) (ChannelIAMRepository, error) {
	switch r := owner.(type) {
	case *channelAuthEnterpriseRepository:
		clone := *r
		iam := *r.channelAuthIAMRepository
		iam.db = reportCountReadSession(ctx, iam.db)
		clone.channelAuthIAMRepository = &iam
		return &clone, nil
	case *channelAuthIAMRepository:
		clone := *r
		clone.db = reportCountReadSession(ctx, r.db)
		return &clone, nil
	default:
		return nil, errors.New("report authentication is unavailable")
	}
}
func reportCountReadSession(ctx context.Context, db *gorm.DB) *gorm.DB {
	return db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}
