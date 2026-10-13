package test

import (
	"context"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestSnoozeCaptureDoesNotActivateHistoricalImports(t *testing.T) {
	db := setupOwnershipBoundaryDB(t)
	conn, err := db.DB()
	require.NoError(t, err)
	defer conn.Close()
	account, inbox, contact, _ := seedOwnershipAccount(t, db, "silent-snooze-import")
	con := repository.NewConversationRepository(db)
	con.SetAutomationCapture(true)
	msg := repository.NewMessageRepositoryWithConversationPort(db, con)
	msg.SetAutomationCapture(true)
	historical := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	imported := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: "snoozed", CreatedAt: historical, UpdatedAt: historical, LastActivityAt: historical}
	require.NoError(t, con.CreateImported(&imported))
	message := domain.Message{AccountID: account.ID, ConversationID: imported.ID, MessageType: domain.MessageTypeIncoming, Content: "historical import", CreatedAt: historical, UpdatedAt: historical}
	require.NoError(t, msg.CreateImported(&message))
	resumed, err := con.ResumeExpiredSnoozed(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	require.Empty(t, resumed, "historical snoozed import has no live expiry deadline")
	conFacts, err := con.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, conFacts)
	msgFacts, err := msg.PendingAutomationFacts(context.Background(), 100)
	require.NoError(t, err)
	require.Empty(t, msgFacts)
	var count int64
	require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "automation_trigger").Count(&count).Error)
	require.Zero(t, count)
	var current domain.Conversation
	require.NoError(t, db.First(&current, imported.ID).Error)
	require.Equal(t, "snoozed", current.Status)
	require.Nil(t, current.SnoozedUntil)
	require.True(t, historical.Equal(current.LastActivityAt))
	require.Equal(t, imported.ObjectVersion, current.ObjectVersion)
	require.Zero(t, current.UnreadCount)
}
