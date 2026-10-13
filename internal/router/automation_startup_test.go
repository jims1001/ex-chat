package router_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAutomationStartupRecoversAfterCompositionAndStopsOnCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "startup.db"), JWTSecret: "startup-workflow-test", AutomationExecutionMode: "durable"}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); _ = sqlDB.Close() })
	account := domain.Account{Name: "Startup tenant"}
	require.NoError(t, db.Create(&account).Error)
	inbox := domain.Inbox{AccountID: account.ID, Name: "Startup inbox"}
	require.NoError(t, db.Create(&inbox).Error)
	contact := domain.Contact{AccountID: account.ID, Name: "Startup contact"}
	require.NoError(t, db.Create(&contact).Error)
	rule := domain.AutomationRule{AccountID: account.ID, Name: "Created after workflow", EventName: "conversation_created", Conditions: "[]", Actions: `[{"action_name":"change_priority","action_params":["urgent"]}]`, Active: true}
	require.NoError(t, service.NewAutomationService(db, nil, nil).CreateRule(&rule))
	con := repository.NewConversationRepository(db)
	con.SetAutomationCapture(true)
	makePlan := func() domain.Conversation {
		c := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: "open", Priority: "medium"}
		require.NoError(t, con.CreateWithInitialMessages(&c, []domain.Message{{SenderType: domain.SenderTypeContact, SenderID: contact.ID, MessageType: domain.MessageTypeIncoming, ContentType: "text", Content: "recover initial message", Status: "sent"}}))
		return c
	}
	first := makePlan()
	_ = router.SetupRouterWithOptions(cfg, db, ws.NewHub(), router.Options{BackgroundContext: ctx})
	require.Eventually(t, func() bool {
		var current domain.Conversation
		if db.First(&current, first.ID).Error != nil {
			return false
		}
		return current.ObjectVersion == 3 && current.Priority == "urgent" && current.UnreadCount == 1
	}, 5*time.Second, 10*time.Millisecond)
	var count int64
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	cancel()
	// Every owner call also carries the cancelled worker context. A newly pending
	// workflow must stay pending after the already completed iteration exits.
	second := makePlan()
	time.Sleep(1200 * time.Millisecond)
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var current domain.Conversation
	require.NoError(t, db.First(&current, second.ID).Error)
	require.Equal(t, 1, current.ObjectVersion)
	require.Equal(t, "medium", current.Priority)
	var pending int64
	require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("source_module = ? AND action = ? AND object_id = ? AND status = ?", "CON", "creation_workflow", second.ID, "pending").Count(&pending).Error)
	require.EqualValues(t, 1, pending)
}
