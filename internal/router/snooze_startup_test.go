package router_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSnoozeStartupUsesComposedOwnerAndExplicitLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"legacy", "durable"} {
		for _, lifecycle := range []string{"none", "cancelled", "live"} {
			t.Run(mode+"/"+lifecycle, func(t *testing.T) {
				cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "startup.db"), JWTSecret: "snooze-startup-test", AutomationExecutionMode: mode}
				db, err := database.InitDB(cfg)
				require.NoError(t, err)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				sqlDB.SetMaxOpenConns(1)
				defer sqlDB.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				account := domain.Account{Name: "Snooze tenant"}
				require.NoError(t, db.Create(&account).Error)
				past := time.Now().UTC().Add(-time.Minute)
				first := domain.Conversation{AccountID: account.ID, Status: "snoozed", SnoozedUntil: &past, Priority: "medium", ObjectVersion: 1}
				require.NoError(t, db.Create(&first).Error)
				rule := domain.AutomationRule{AccountID: account.ID, Name: "Opened on expiry", EventName: "conversation_opened", Conditions: "[]", Actions: `[{"action_name":"change_priority","action_params":["urgent"]}]`, Active: true}
				require.NoError(t, service.NewAutomationService(db, nil, nil).CreateRule(&rule))
				options := router.Options{}
				if lifecycle != "none" {
					options.BackgroundContext = ctx
				}
				if lifecycle == "cancelled" {
					cancel()
				}
				_ = router.SetupRouterWithOptions(cfg, db, ws.NewHub(), options)
				if lifecycle == "live" {
					require.Eventually(t, func() bool {
						var current domain.Conversation
						if db.First(&current, first.ID).Error != nil {
							return false
						}
						if mode == "durable" {
							return current.Status == "open" && current.SnoozedUntil == nil && current.Priority == "urgent" && current.ObjectVersion == 3
						}
						return current.Status == "open" && current.SnoozedUntil == nil && current.Priority == "medium" && current.ObjectVersion == 2
					}, 5*time.Second, 10*time.Millisecond)
					cancel()
					second := domain.Conversation{AccountID: account.ID, Status: "snoozed", SnoozedUntil: &past, ObjectVersion: 1}
					require.NoError(t, db.Create(&second).Error)
					time.Sleep(30 * time.Millisecond)
					var current domain.Conversation
					require.NoError(t, db.First(&current, second.ID).Error)
					require.Equal(t, "snoozed", current.Status)
					require.Equal(t, 1, current.ObjectVersion)
				} else {
					time.Sleep(30 * time.Millisecond)
					var current domain.Conversation
					require.NoError(t, db.First(&current, first.ID).Error)
					require.Equal(t, "snoozed", current.Status)
					require.Equal(t, 1, current.ObjectVersion)
				}
				var captures, executions int64
				require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_trigger").Count(&captures).Error)
				require.NoError(t, db.Model(&domain.AutomationRuleExecution{}).Count(&executions).Error)
				if mode == "durable" && lifecycle == "live" {
					require.EqualValues(t, 1, captures)
					require.EqualValues(t, 1, executions)
				} else {
					require.Zero(t, captures)
					require.Zero(t, executions, "legacy expiry has never invoked automation callbacks")
				}
			})
		}
	}
}
