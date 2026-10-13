package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStageRecordingCancellationAndContentChecks(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	s := NewLocalStorageService(root, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := s.StageRecording(ctx, "account_1/recordings/audio.wav", []byte("audio"))
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = s.StageRecording(context.Background(), "account_1/recordings/audio.wav", []byte("RIFF <?php malicious"))
	require.ErrorIs(t, err, ErrMaliciousFileDetected)
	_, err = os.Stat(root)
	require.True(t, os.IsNotExist(err))
}
func TestStageRecordingExclusiveAndRollback(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	s := NewLocalStorageService(root, "")
	key := "account_1/recordings/audio.wav"
	url, finish, err := s.StageRecording(context.Background(), key, []byte("persisted"))
	require.NoError(t, err)
	require.Equal(t, "/uploads/"+key, url)
	data, err := os.ReadFile(filepath.Join(root, key))
	require.NoError(t, err)
	require.Equal(t, "persisted", string(data))
	_, _, err = s.StageRecording(context.Background(), key, []byte("replacement"))
	require.Error(t, err)
	require.NoError(t, finish(false))
	_, err = os.Stat(filepath.Join(root, key))
	require.True(t, os.IsNotExist(err))
}
