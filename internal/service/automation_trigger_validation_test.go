package service

import (
	"path/filepath"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAutomationTriggerDurableServiceAcceptsDeclaredStatusEvents(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "rules.sqlite")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.AutomationRule{}, &domain.AutomationRuleVersion{}))
	s := &AutomationService{db: db}
	require.False(t, s.durable)
	s.SetDurableExecution(true)
	require.True(t, s.durable)
	for _, event := range []string{"conversation_created", "conversation_updated", "message_created", "conversation_opened", "conversation_resolved"} {
		rule := domain.AutomationRule{AccountID: 91, Name: event, EventName: event, Active: true, Conditions: "[]", Actions: "[]"}
		require.NoError(t, s.CreateRule(&rule))
		require.NoError(t, s.UpdateRule(&rule, map[string]any{"event_name": "conversation_resolved"}))
		require.NoError(t, s.UpdateRule(&rule, map[string]any{"event_name": "conversation_opened"}))
		require.ErrorIs(t, s.UpdateRule(&rule, map[string]any{"event_name": "conversation_closed"}), ErrAutomationEventUnsupported)
		var stored domain.AutomationRule
		require.NoError(t, db.First(&stored, rule.ID).Error)
		require.Equal(t, "conversation_opened", stored.EventName)
	}
	preexisting := domain.AutomationRule{AccountID: 91, Name: "old opened", EventName: "conversation_opened", Active: true}
	require.NoError(t, db.Create(&preexisting).Error)
	require.NoError(t, s.PrepareDurableRules(), "formerly accepted opened definitions now have explicit durable semantics")
	var frozen domain.AutomationRuleVersion
	require.NoError(t, db.Where("rule_id = ?", preexisting.ID).First(&frozen).Error)
	require.Equal(t, "conversation_opened", frozen.EventName)
	require.False(t, frozen.CreatedAt.Before(preexisting.CreatedAt))
	unsupported := domain.AutomationRule{AccountID: 91, Name: "unknown", EventName: "conversation_closed", Active: true}
	require.ErrorIs(t, s.CreateRule(&unsupported), ErrAutomationEventUnsupported)
}
