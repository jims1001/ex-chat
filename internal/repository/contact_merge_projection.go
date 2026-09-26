package repository

import (
	"context"

	"gorm.io/gorm"
)

type combinedContactMergeProjection struct {
	calls   *CallRepository
	widgets *WidgetRepository
}

func newCombinedContactMergeProjection(db *gorm.DB) contactMergeProjectionPort {
	return combinedContactMergeProjection{calls: NewCallRepository(db), widgets: NewWidgetRepository(db)}
}

func (p combinedContactMergeProjection) ReassignContact(accountID, fromContactID, toContactID uint) error {
	if err := p.calls.ReassignContact(accountID, fromContactID, toContactID); err != nil {
		return err
	}
	return p.widgets.ReassignContact(context.Background(), accountID, fromContactID, toContactID)
}
