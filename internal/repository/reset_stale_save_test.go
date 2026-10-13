package repository

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPasswordResetRejectsStaleProfileCredentialRollback(t *testing.T) {
	repo, _, stale, replacement := resetTestRepositories(t)
	_, raw := activeResetToken(t, repo, stale, 987)
	require.NoError(t, repo.ConsumePasswordResetToken(raw, replacement))
	stale.Name = "Stale profile"
	require.Error(t, NewUserRepository(repo.db).Update(stale))
	var current domain.User
	require.NoError(t, repo.db.First(&current, stale.ID).Error)
	require.Equal(t, replacement, current.PasswordHash)
	require.EqualValues(t, 1, current.AuthVersion)
	current.Name = "Fresh profile"
	require.NoError(t, NewUserRepository(repo.db).Update(&current))
	require.NoError(t, repo.db.First(&current, stale.ID).Error)
	require.Equal(t, replacement, current.PasswordHash)
	require.EqualValues(t, 1, current.AuthVersion)
	// Confirmation modifies only confirmation fields and returns current epoch.
	_, confirmationRaw, err := repo.GenerateConfirmationToken(current.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ActivateConfirmationToken(current.ID, confirmationRaw))
	confirmed, err := repo.ConfirmUserByToken(confirmationRaw)
	require.NoError(t, err)
	require.EqualValues(t, 1, confirmed.AuthVersion)
	require.Equal(t, replacement, confirmed.PasswordHash)
}

func TestCallLifecycleUpdateCannotEraseNewRecording(t *testing.T) {
	repo, _, user, _ := resetTestRepositories(t)
	rtm := &channelAuthRTMRepository{db: repo.db}
	require.NoError(t, repo.db.AutoMigrate(&domain.Call{}))
	call := domain.Call{AccountID: 1, InboxID: 1, ContactID: user.ID, Status: "in-progress", RecordingURL: "previous.wav"}
	require.NoError(t, rtm.CreateCall(&call))
	stale := call
	require.NoError(t, rtm.UpdateCallRecording(call.AccountID, call.ID, "/uploads/account_1/recordings/new.wav", "new-sid", nil))
	stale.Status = "completed"
	require.NoError(t, rtm.UpdateCall(&stale))
	current, err := rtm.GetCall(call.AccountID, call.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", current.Status)
	require.Equal(t, "/uploads/account_1/recordings/new.wav", current.RecordingURL)
	require.Equal(t, "new-sid", current.RecordingSID)
}
