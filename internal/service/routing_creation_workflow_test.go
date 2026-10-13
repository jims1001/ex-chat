package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func initialRoutingFixture(t *testing.T) (*gorm.DB, *repository.ConversationRepository, *service.RoutingService, domain.Conversation, domain.User) {
	t.Helper()
	db, err := database.InitDB(&config.Config{DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "routing.sqlite")})
	require.NoError(t, err)
	accounts := repository.NewAccountRepository(db)
	users := repository.NewUserRepository(db)
	inboxes := repository.NewInboxRepository(db)
	owner := repository.NewConversationRepository(db)
	owner.SetAutomationCapture(true)
	account := domain.Account{Name: "Initial routing"}
	require.NoError(t, accounts.Create(&account))
	agent := domain.User{Name: "Available", Email: "initial-routing@example.test", Role: domain.RoleAgent, Availability: domain.AvailabilityOnline}
	require.NoError(t, users.Create(&agent))
	require.NoError(t, accounts.AddMember(account.ID, agent.ID, domain.RoleAgent))
	inbox := domain.Inbox{AccountID: account.ID, Name: "Support", WebsiteToken: "initial-route"}
	require.NoError(t, inboxes.Create(&inbox))
	require.NoError(t, inboxes.AddMember(inbox.ID, agent.ID))
	conversation := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium}
	return db, owner, service.NewRoutingService(db, owner, nil), conversation, agent
}

func TestRoutingInitialCreationCapturesPostAssignmentVersion(t *testing.T) {
	db, owner, routing, conversation, agent := initialRoutingFixture(t)
	// CON must not write IAM associations while committing the routing plan.
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_routing_user_write", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "users" {
			_ = tx.AddError(errors.New("CON attempted IAM write"))
		}
	}))
	require.NoError(t, routing.CreateWithAutoAssign(&conversation))
	require.NotNil(t, conversation.AssigneeID)
	require.Equal(t, agent.ID, *conversation.AssigneeID)
	require.Equal(t, 1, conversation.ObjectVersion)
	stored, err := owner.FindSummaryByID(conversation.AccountID, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, conversation.AssigneeID, stored.AssigneeID)
	facts, err := owner.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, "conversation_created", facts[0].Payload["event_name"])
	require.EqualValues(t, stored.ObjectVersion, facts[0].Payload["object_version"])

	// Restart/replay reads the same immutable fact, even after a manual edit.
	require.NoError(t, owner.Assign(conversation.AccountID, conversation.ID, nil))
	restarted := repository.NewConversationRepository(db)
	restarted.SetAutomationCapture(true)
	factsAfter, err := restarted.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, factsAfter, 2)
	require.Equal(t, facts[0], factsAfter[0])
	stored, err = owner.FindSummaryByID(conversation.AccountID, conversation.ID)
	require.NoError(t, err)
	require.Nil(t, stored.AssigneeID)
	require.Equal(t, 2, stored.ObjectVersion)
}

func TestRoutingInitialCreationRollsBackWithFact(t *testing.T) {
	db, owner, routing, conversation, _ := initialRoutingFixture(t)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("reject_initial_routing_fact", func(tx *gorm.DB) {
		if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == "automation_trigger" {
			_ = tx.AddError(errors.New("scripted fact failure"))
		}
	}))
	require.Error(t, routing.CreateWithAutoAssign(&conversation))
	require.Zero(t, conversation.ID)
	require.Nil(t, conversation.AssigneeID)
	var count int64
	require.NoError(t, db.Model(&domain.Conversation{}).Count(&count).Error)
	require.Zero(t, count)
	facts, err := owner.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Empty(t, facts)
}

func TestRoutingInitialCreationPreservesExplicitAssignmentAndLegacyMode(t *testing.T) {
	_, owner, routing, conversation, agent := initialRoutingFixture(t)
	conversation.AssigneeID = &agent.ID
	require.NoError(t, routing.CreateWithAutoAssign(&conversation))
	require.Equal(t, 1, conversation.ObjectVersion)
	require.Equal(t, agent.ID, *conversation.AssigneeID)

	owner.SetAutomationCapture(false)
	legacy := domain.Conversation{AccountID: conversation.AccountID, InboxID: conversation.InboxID, ContactID: 2, Status: domain.ConversationStatusOpen}
	require.ErrorIs(t, routing.CreateWithAutoAssign(&legacy), gorm.ErrInvalidData)
	require.NoError(t, owner.Create(&legacy))
	assigned, err := routing.AutoAssign(&legacy)
	require.NoError(t, err)
	require.Equal(t, agent.ID, assigned.ID)
	stored, err := owner.FindSummaryByID(legacy.AccountID, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, 2, stored.ObjectVersion)
}

func TestRoutingInitialCreationLeavesPlannedMessagesToApplicationCoordinator(t *testing.T) {
	db, owner, routing, conversation, _ := initialRoutingFixture(t)
	initial := domain.Message{AccountID: conversation.AccountID, SenderType: domain.SenderTypeContact, SenderID: conversation.ContactID, MessageType: domain.MessageTypeIncoming, Content: "initial customer request"}
	require.NoError(t, routing.CreateWithInitialMessage(&conversation, &initial))
	require.NotZero(t, conversation.ID)
	require.Equal(t, 1, conversation.ObjectVersion)
	require.Zero(t, initial.ID, "RTE only persists the plan; it must not drive MSG")
	var count int64
	require.NoError(t, db.Model(&domain.Message{}).Count(&count).Error)
	require.Zero(t, count)
	facts, err := owner.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Empty(t, facts, "the created fact stays gated until application composition runs the plan")

	messages := repository.NewMessageRepositoryWithConversationPort(db, owner)
	messages.SetAutomationCapture(true)
	coordinator := repository.NewCreationWorkflowCoordinator(owner, messages)
	completed, err := coordinator.CompleteCreationWorkflow(context.Background(), conversation.AccountID, conversation.ID)
	require.NoError(t, err)
	require.Len(t, completed, 1)
	require.NotZero(t, completed[0].ID)
	facts, err = owner.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.EqualValues(t, 1, facts[0].AggregateVersion)
	require.EqualValues(t, 2, facts[0].Payload["object_version"])
	messageFacts, err := messages.PendingAutomationFacts(context.Background(), 10)
	require.NoError(t, err)
	require.Empty(t, messageFacts)
}
