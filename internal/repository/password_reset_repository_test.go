package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func resetTestRepositories(t *testing.T) (*channelAuthIAMRepository, *channelAuthIAMRepository, *domain.User, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "reset.sqlite")
	first := openMFATestDB(t, path)
	require.NoError(t, first.AutoMigrate(&domain.User{}, &domain.PasswordResetToken{}, &domain.MFAProfile{}, &domain.UserSession{}))
	hash, err := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	require.NoError(t, err)
	user := &domain.User{Name: "Reset Test", Email: "reset@example.test", PasswordHash: string(hash)}
	require.NoError(t, first.Create(user).Error)
	replacement, err := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.MinCost)
	require.NoError(t, err)
	return &channelAuthIAMRepository{db: first}, &channelAuthIAMRepository{db: openMFATestDB(t, path)}, user, string(replacement)
}

func pendingResetToken(t *testing.T, repo *channelAuthIAMRepository, user *domain.User, n int) (*domain.PasswordResetToken, string) {
	t.Helper()
	raw := fmt.Sprintf("%064x", n)
	digest, err := PasswordResetTokenDigest(raw)
	require.NoError(t, err)
	token := &domain.PasswordResetToken{UserID: user.ID, Email: user.Email, Token: digest, ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, repo.CreatePasswordResetToken(token))
	return token, raw
}

func activeResetToken(t *testing.T, repo *channelAuthIAMRepository, user *domain.User, n int) (*domain.PasswordResetToken, string) {
	t.Helper()
	token, raw := pendingResetToken(t, repo, user, n)
	require.NoError(t, repo.ActivatePasswordResetToken(token.ID))
	return token, raw
}

func TestPasswordResetAtomicOneTimeAcrossDatabaseHandles(t *testing.T) {
	first, second, user, replacement := resetTestRepositories(t)
	_, raw := activeResetToken(t, first, user, 1)
	sibling, siblingRaw := activeResetToken(t, first, user, 2)
	pending, _ := pendingResetToken(t, first, user, 3)
	profile := createMFATestProfile(t, first, user.ID, "12345678")
	profileBefore, err := json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, first.CreateSession(&domain.UserSession{UserID: user.ID}))
	results := runConcurrentMFAVerifications(t, first, second, 16, func(repo *channelAuthIAMRepository, _ int) error {
		return repo.ConsumePasswordResetToken(raw, replacement)
	})
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrPasswordResetInvalid)
		}
	}
	require.Equal(t, 1, successes)
	var got domain.User
	require.NoError(t, first.db.First(&got, user.ID).Error)
	require.Equal(t, replacement, got.PasswordHash)
	require.EqualValues(t, 1, got.AuthVersion)
	require.Equal(t, user.Name, got.Name)
	require.ErrorIs(t, first.ConsumePasswordResetToken(siblingRaw, replacement), ErrPasswordResetInvalid)
	require.ErrorIs(t, first.ActivatePasswordResetToken(pending.ID), ErrPasswordResetInvalid)
	require.NoError(t, first.db.First(sibling, sibling.ID).Error)
	require.True(t, sibling.Used)
	profileAfter, err := first.GetMFAProfile(user.ID)
	require.NoError(t, err)
	encoded, err := json.Marshal(profileAfter)
	require.NoError(t, err)
	require.JSONEq(t, string(profileBefore), string(encoded))
	sessions, err := first.ListSessions(user.ID)
	require.NoError(t, err)
	require.Empty(t, sessions)
}

func TestPasswordResetSiblingTokensRaceOnlyOneWins(t *testing.T) {
	first, second, user, replacement := resetTestRepositories(t)
	_, one := activeResetToken(t, first, user, 1)
	_, two := activeResetToken(t, first, user, 2)
	results := runConcurrentMFAVerifications(t, first, second, 12, func(repo *channelAuthIAMRepository, i int) error {
		raw := one
		if i%2 == 1 {
			raw = two
		}
		return repo.ConsumePasswordResetToken(raw, replacement)
	})
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrPasswordResetInvalid)
		}
	}
	require.Equal(t, 1, successes)
}

func TestPasswordResetStorageFailuresRollBack(t *testing.T) {
	for _, failure := range []string{"password", "token", "sibling", "session"} {
		t.Run(failure, func(t *testing.T) {
			first, _, user, replacement := resetTestRepositories(t)
			token, raw := activeResetToken(t, first, user, 1)
			sibling, siblingRaw := activeResetToken(t, first, user, 2)
			require.NoError(t, first.CreateSession(&domain.UserSession{UserID: user.ID}))
			statements := map[string]string{
				"password": "CREATE TRIGGER reset_fail BEFORE UPDATE OF password_hash ON users WHEN NEW.password_hash <> OLD.password_hash BEGIN SELECT RAISE(ABORT, 'injected password failure'); END",
				"token":    "CREATE TRIGGER reset_fail BEFORE UPDATE OF used ON password_reset_tokens BEGIN SELECT RAISE(ABORT, 'injected token failure'); END",
				"sibling":  fmt.Sprintf("CREATE TRIGGER reset_fail BEFORE UPDATE OF used ON password_reset_tokens WHEN OLD.id = %d BEGIN SELECT RAISE(ABORT, 'injected sibling failure'); END", sibling.ID),
				"session":  "CREATE TRIGGER reset_fail BEFORE DELETE ON user_sessions BEGIN SELECT RAISE(ABORT, 'injected session failure'); END",
			}
			require.NoError(t, first.db.Exec(statements[failure]).Error)
			err := first.ConsumePasswordResetToken(raw, replacement)
			require.Error(t, err)
			require.False(t, errors.Is(err, ErrPasswordResetInvalid))
			var got domain.User
			require.NoError(t, first.db.First(&got, user.ID).Error)
			require.Equal(t, user.PasswordHash, got.PasswordHash)
			require.Zero(t, got.AuthVersion)
			require.NoError(t, first.db.First(token, token.ID).Error)
			require.False(t, token.Used)
			require.NoError(t, first.db.First(sibling, sibling.ID).Error)
			require.False(t, sibling.Used)
			sessions, err := first.ListSessions(user.ID)
			require.NoError(t, err)
			require.Len(t, sessions, 1)
			require.NoError(t, first.db.Exec("DROP TRIGGER reset_fail").Error)
			require.NoError(t, first.ConsumePasswordResetToken(raw, replacement))
			require.ErrorIs(t, first.ConsumePasswordResetToken(siblingRaw, replacement), ErrPasswordResetInvalid)
		})
	}
}

func TestPasswordResetRejectsUnissuedExpiredLegacyAndSSOTokens(t *testing.T) {
	for _, state := range []string{"pending", "expired", "legacy", "passwordless", "invalid-hash", "email-changed", "deleted-user"} {
		t.Run(state, func(t *testing.T) {
			first, _, user, replacement := resetTestRepositories(t)
			token, raw := pendingResetToken(t, first, user, 1)
			if state != "pending" {
				require.NoError(t, first.ActivatePasswordResetToken(token.ID))
			}
			switch state {
			case "expired":
				require.NoError(t, first.db.Model(token).Update("expires_at", time.Now().Add(-time.Second)).Error)
			case "legacy":
				require.NoError(t, first.db.Model(token).Update("token", raw).Error)
			case "passwordless":
				require.NoError(t, first.db.Model(user).Update("password_hash", "").Error)
			case "invalid-hash":
				require.NoError(t, first.db.Model(user).Update("password_hash", "sso-only").Error)
			case "email-changed":
				require.NoError(t, first.db.Model(user).Update("email", "changed@example.test").Error)
			case "deleted-user":
				require.NoError(t, first.db.Delete(user).Error)
			}
			require.ErrorIs(t, first.ConsumePasswordResetToken(raw, replacement), ErrPasswordResetInvalid)
			var count int64
			require.NoError(t, first.db.Model(&domain.User{}).Where("password_hash = ?", replacement).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestPasswordResetNewTokensDigestOnly(t *testing.T) {
	first, _, user, _ := resetTestRepositories(t)
	token, raw := pendingResetToken(t, first, user, 1)
	require.NotEqual(t, raw, token.Token)
	require.True(t, strings.HasPrefix(token.Token, "sha256:"))
	encoded, err := json.Marshal(token)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), raw)
	require.NotContains(t, string(encoded), token.Token)
	bad := &domain.PasswordResetToken{UserID: user.ID, Email: user.Email, Token: raw, ExpiresAt: time.Now().Add(time.Hour)}
	require.ErrorIs(t, first.CreatePasswordResetToken(bad), ErrPasswordResetInvalid)
	for _, raw := range []string{"", "legacy-token", strings.Repeat("z", 64), strings.Repeat("A", 64), strings.Repeat("0", 63)} {
		_, err := PasswordResetTokenDigest(raw)
		require.ErrorIs(t, err, ErrPasswordResetInvalid)
	}
}
