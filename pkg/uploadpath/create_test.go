//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package uploadpath

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreateExclusiveCommitRollbackAndExistingFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	f, finish, err := CreateExclusive(root, "account_1/recordings/one.wav")
	require.NoError(t, err)
	_, err = f.Write([]byte("old"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, finish(true))
	require.NoError(t, finish(false))
	_, _, err = CreateExclusive(root, "account_1/recordings/one.wav")
	require.Error(t, err)
	data, err := os.ReadFile(filepath.Join(root, "account_1/recordings/one.wav"))
	require.NoError(t, err)
	require.Equal(t, "old", string(data))
	f, finish, err = CreateExclusive(root, "account_1/recordings/two.wav")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, finish(false))
	_, err = os.Stat(filepath.Join(root, "account_1/recordings/two.wav"))
	require.True(t, os.IsNotExist(err))
}
func TestCreateExclusiveRejectsSymlinksAndTraversal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "account_1")))
	for _, key := range []string{"account_1/audio.wav", "../escape", "/absolute", "account_2/../escape", "account_2\\escape"} {
		_, _, err := CreateExclusive(root, key)
		require.Error(t, err, key)
	}
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoError(t, os.Mkdir(filepath.Join(root, "account_2"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "original"), []byte("original"), 0600))
	require.NoError(t, os.Symlink(filepath.Join(root, "original"), filepath.Join(root, "account_2", "audio.wav")))
	_, _, err = CreateExclusive(root, "account_2/audio.wav")
	require.Error(t, err)
	data, err := os.ReadFile(filepath.Join(root, "original"))
	require.NoError(t, err)
	require.Equal(t, "original", string(data))
}
func TestCreateExclusiveRollbackUsesHeldDirectory(t *testing.T) {
	root := t.TempDir()
	f, finish, err := CreateExclusive(root, "account_1/audio.wav")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	require.NoError(t, os.Rename(filepath.Join(root, "account_1"), filepath.Join(root, "moved")))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "audio.wav"), []byte("untouched"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "account_1")))
	require.NoError(t, finish(false))
	_, err = os.Stat(filepath.Join(root, "moved/audio.wav"))
	require.True(t, os.IsNotExist(err))
	data, err := os.ReadFile(filepath.Join(outside, "audio.wav"))
	require.NoError(t, err)
	require.Equal(t, "untouched", string(data))
}
