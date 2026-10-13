package repository

import (
	"context"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// FindUploadIdentity returns an unambiguous durable channel mapping. When a
// source ID is supplied, do not narrow the query by contact first: a duplicate
// source mapping must fail closed rather than selecting the convenient match.
func (r *ContactRepository) FindUploadIdentity(ctx context.Context, inboxID, contactID uint, sourceID string) (*domain.ContactInbox, error) {
	if inboxID == 0 || contactID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	q := r.db.WithContext(ctx).Where("inbox_id = ?", inboxID)
	if sourceID != "" {
		q = q.Where("source_id = ?", sourceID)
	} else {
		q = q.Where("contact_id = ?", contactID)
	}
	var matches []domain.ContactInbox
	if err := q.Limit(2).Find(&matches).Error; err != nil {
		return nil, err
	}
	if len(matches) != 1 || matches[0].ContactID != contactID {
		return nil, gorm.ErrRecordNotFound
	}
	return &matches[0], nil
}
