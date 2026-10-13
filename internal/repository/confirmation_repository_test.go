package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The contract is shared with the opt-in native PostgreSQL tests. Both handles
// use independent connection pools, just like separate application instances.
type confirmationRepositoryFactory func(*testing.T) (*channelAuthIAMRepository, *channelAuthIAMRepository, *domain.User, string)

func confirmationCurrentUser(t *testing.T, repo *channelAuthIAMRepository, id uint) *domain.User {
	t.Helper()
	var user domain.User
	require.NoError(t, repo.db.First(&user, id).Error)
	return &user
}

func confirmationActiveToken(t *testing.T, repo *channelAuthIAMRepository, userID uint) (*domain.User, string) {
	t.Helper()
	snapshot, raw, err := repo.GenerateConfirmationToken(userID)
	require.NoError(t, err)
	require.NoError(t, repo.ActivateConfirmationToken(userID, raw))
	return snapshot, raw
}

func requireConfirmationInstant(t *testing.T, want, got *time.Time) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	// Native PostgreSQL may materialize a different Location for the same instant.
	require.True(t, want.Equal(*got), "expected instant %s, got %s", want, got)
}

func requireConfirmationState(t *testing.T, want, got *domain.User) {
	t.Helper()
	require.Equal(t, want.ConfirmationToken, got.ConfirmationToken)
	require.Equal(t, want.ConfirmationEmail, got.ConfirmationEmail)
	require.Equal(t, want.ConfirmationAuthVersion, got.ConfirmationAuthVersion)
	requireConfirmationInstant(t, want.ConfirmedAt, got.ConfirmedAt)
	requireConfirmationInstant(t, want.ConfirmationSentAt, got.ConfirmationSentAt)
	requireConfirmationInstant(t, want.ConfirmationIssuedAt, got.ConfirmationIssuedAt)
	requireConfirmationInstant(t, want.ConfirmationExpiresAt, got.ConfirmationExpiresAt)
}

func requireConfirmationConsumed(t *testing.T, user *domain.User) {
	t.Helper()
	require.NotNil(t, user.ConfirmedAt)
	require.Empty(t, user.ConfirmationToken)
	require.Empty(t, user.ConfirmationEmail)
	require.Zero(t, user.ConfirmationAuthVersion)
	require.Nil(t, user.ConfirmationSentAt)
	require.Nil(t, user.ConfirmationIssuedAt)
	require.Nil(t, user.ConfirmationExpiresAt)
}

func requireConfirmationRejected(t *testing.T, repo *channelAuthIAMRepository, raw string) {
	t.Helper()
	user, err := repo.ConfirmUserByToken(raw)
	require.ErrorIs(t, err, ErrConfirmationInvalid)
	require.Nil(t, user, "an invalid proof must not return a session-capable user")
}

func testConfirmationLifecycle(t *testing.T, factory confirmationRepositoryFactory) {
	first, second, user, _ := factory(t)
	require.NoError(t, first.db.Model(user).Update("auth_version", 7).Error)
	before := time.Now().UTC()
	snapshot, raw, err := first.GenerateConfirmationToken(user.ID)
	require.NoError(t, err)
	require.NotNil(t, snapshot)
	require.Len(t, raw, 64)
	digest, err := ConfirmationTokenDigest(raw)
	require.NoError(t, err)
	require.Len(t, digest, 71)
	require.NotEqual(t, raw, digest)
	require.Equal(t, digest, snapshot.ConfirmationToken)
	require.Equal(t, user.Email, snapshot.Email)
	require.Equal(t, snapshot.Email, snapshot.ConfirmationEmail)
	require.EqualValues(t, 7, snapshot.AuthVersion)
	require.Equal(t, snapshot.AuthVersion, snapshot.ConfirmationAuthVersion)
	require.Nil(t, snapshot.ConfirmationIssuedAt)
	require.NotNil(t, snapshot.ConfirmationSentAt)
	require.NotNil(t, snapshot.ConfirmationExpiresAt)
	require.False(t, snapshot.ConfirmationSentAt.Before(before))
	require.False(t, snapshot.ConfirmationSentAt.After(time.Now().UTC()))
	require.Equal(t, 24*time.Hour, snapshot.ConfirmationExpiresAt.Sub(*snapshot.ConfirmationSentAt))
	stored := confirmationCurrentUser(t, second, user.ID)
	require.Equal(t, digest, stored.ConfirmationToken)
	// PostgreSQL persists microsecond precision, so compare its stored duration,
	// not a nanosecond Go timestamp against a rounded database timestamp.
	require.Equal(t, 24*time.Hour, stored.ConfirmationExpiresAt.Sub(*stored.ConfirmationSentAt))
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	for _, secret := range []string{raw, digest, "confirmation_token", "confirmation_email", "confirmation_auth_version", "confirmation_sent_at", "confirmation_expires_at", "confirmation_issued_at"} {
		require.NotContains(t, string(encoded), secret)
	}
	requireConfirmationRejected(t, second, raw)
	require.NoError(t, second.ActivateConfirmationToken(user.ID, raw))
	require.ErrorIs(t, first.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
	require.Nil(t, snapshot.ConfirmationIssuedAt, "activating must not mutate the prepared mail snapshot")
	confirmed, err := first.ConfirmUserByToken(raw)
	require.NoError(t, err)
	requireConfirmationConsumed(t, confirmed)
	require.Equal(t, user.ID, confirmed.ID)
	require.EqualValues(t, 7, confirmed.AuthVersion)
	require.Equal(t, user.PasswordHash, confirmed.PasswordHash)
	require.Equal(t, user.Email, confirmed.Email)
	require.Equal(t, user.Name, confirmed.Name)
	requireConfirmationConsumed(t, confirmationCurrentUser(t, second, user.ID))
	requireConfirmationRejected(t, second, raw)
	require.ErrorIs(t, second.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
	noUser, noToken, err := second.GenerateConfirmationToken(user.ID)
	require.ErrorIs(t, err, ErrConfirmationInvalid)
	require.Nil(t, noUser)
	require.Empty(t, noToken)
}

func TestConfirmationLifecycleAndDigestOnlyStorage(t *testing.T) {
	testConfirmationLifecycle(t, resetTestRepositories)
}

func TestConfirmationTokenDigestRejectsMalformedAndSeparatesPurpose(t *testing.T) {
	for _, raw := range []string{"", "legacy-token", "sha256:" + strings.Repeat("a", 64), strings.Repeat("z", 64), strings.Repeat("A", 64), strings.Repeat("0", 63), strings.Repeat("0", 65), " " + strings.Repeat("0", 64)} {
		_, err := ConfirmationTokenDigest(raw)
		require.ErrorIs(t, err, ErrConfirmationInvalid)
	}
	raw := strings.Repeat("a", 64)
	confirmationDigest, err := ConfirmationTokenDigest(raw)
	require.NoError(t, err)
	resetDigest, err := PasswordResetTokenDigest(raw)
	require.NoError(t, err)
	require.NotEqual(t, resetDigest, confirmationDigest, "a reset proof must not be a confirmation proof")
}

func testConfirmationInvalidProofs(t *testing.T, factory confirmationRepositoryFactory) {
	for _, state := range []string{"pending", "unknown", "legacy-raw", "missing-expiry", "missing-prepared-at", "missing-issued-at", "expired", "expiry-equal-now", "ttl-extended", "prepared-after-issued", "issued-in-future", "email-changed", "email-case-changed", "auth-version-changed", "confirmed", "deleted"} {
		t.Run(state, func(t *testing.T) {
			first, second, user, _ := factory(t)
			_, raw, err := first.GenerateConfirmationToken(user.ID)
			require.NoError(t, err)
			if state != "pending" {
				require.NoError(t, first.ActivateConfirmationToken(user.ID, raw))
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			updates := map[string]any{}
			switch state {
			case "unknown":
				raw = strings.Repeat("0", 64)
			case "legacy-raw":
				updates["confirmation_token"] = raw
			case "missing-expiry":
				updates["confirmation_expires_at"] = nil
			case "missing-prepared-at":
				updates["confirmation_sent_at"] = nil
			case "missing-issued-at":
				updates["confirmation_issued_at"] = nil
			case "expired":
				updates["confirmation_expires_at"] = now.Add(-time.Second)
			case "expiry-equal-now":
				updates["confirmation_expires_at"] = now
			case "ttl-extended":
				updates["confirmation_sent_at"] = now.Add(-time.Hour)
				updates["confirmation_expires_at"] = now.Add(23*time.Hour + time.Minute)
			case "prepared-after-issued":
				updates["confirmation_sent_at"] = now
				updates["confirmation_issued_at"] = now.Add(-time.Second)
			case "issued-in-future":
				updates["confirmation_issued_at"] = now.Add(time.Hour)
			case "email-changed":
				updates["email"] = "other@example.test"
			case "email-case-changed":
				updates["email"] = strings.ToUpper(user.Email)
			case "auth-version-changed":
				updates["auth_version"] = 1
			case "confirmed":
				updates["confirmed_at"] = now
			case "deleted":
				require.NoError(t, first.db.Delete(user).Error)
			}
			if len(updates) > 0 {
				require.NoError(t, first.db.Model(user).Updates(updates).Error)
			}
			var before *domain.User
			if state != "deleted" {
				before = confirmationCurrentUser(t, first, user.ID)
			}
			requireConfirmationRejected(t, second, raw)
			if before != nil {
				requireConfirmationState(t, before, confirmationCurrentUser(t, first, user.ID))
			}
		})
	}
	for _, state := range []string{"unknown-user", "empty-email"} {
		t.Run("prepare-"+state, func(t *testing.T) {
			first, _, user, _ := factory(t)
			id := user.ID
			if state == "unknown-user" {
				id += 10000
			} else {
				require.NoError(t, first.db.Model(user).Update("email", "").Error)
			}
			snapshot, raw, err := first.GenerateConfirmationToken(id)
			require.ErrorIs(t, err, ErrConfirmationInvalid)
			require.Nil(t, snapshot)
			require.Empty(t, raw)
		})
	}
}

func TestConfirmationRejectsInvalidOrStaleProofs(t *testing.T) {
	testConfirmationInvalidProofs(t, resetTestRepositories)
}

func testConfirmationActivationFencing(t *testing.T, factory confirmationRepositoryFactory) {
	for _, state := range []string{"resend", "email-changed", "email-case-changed", "auth-version-changed", "reset", "confirmed", "deleted", "expired", "expiry-equal-now", "missing-expiry", "missing-prepared-at", "prepared-in-future", "ttl-extended", "wrong-user", "legacy-raw"} {
		t.Run(state, func(t *testing.T) {
			first, second, user, replacement := factory(t)
			snapshot, raw, err := first.GenerateConfirmationToken(user.ID)
			require.NoError(t, err)
			originalEmail := snapshot.Email
			originalExpiry := *snapshot.ConfirmationExpiresAt
			now := time.Now().UTC().Truncate(time.Microsecond)
			updates := map[string]any{}
			activationID := user.ID
			switch state {
			case "resend":
				_, _, err = second.GenerateConfirmationToken(user.ID)
				require.NoError(t, err)
			case "email-changed":
				updates["email"] = "other@example.test"
			case "email-case-changed":
				updates["email"] = strings.ToUpper(user.Email)
			case "auth-version-changed":
				updates["auth_version"] = 1
			case "reset":
				_, resetRaw := activeResetToken(t, second, user, 711)
				require.NoError(t, second.ConsumePasswordResetToken(resetRaw, replacement))
			case "confirmed":
				updates["confirmed_at"] = now
			case "deleted":
				require.NoError(t, first.db.Delete(user).Error)
			case "expired":
				updates["confirmation_expires_at"] = now.Add(-time.Second)
			case "expiry-equal-now":
				updates["confirmation_expires_at"] = now
			case "missing-expiry":
				updates["confirmation_expires_at"] = nil
			case "missing-prepared-at":
				updates["confirmation_sent_at"] = nil
			case "prepared-in-future":
				updates["confirmation_sent_at"] = now.Add(time.Minute)
			case "ttl-extended":
				updates["confirmation_sent_at"] = now.Add(-time.Hour)
				updates["confirmation_expires_at"] = now.Add(23*time.Hour + time.Minute)
			case "wrong-user":
				other := &domain.User{Name: "Other", Email: "not-the-recipient@example.test", PasswordHash: user.PasswordHash}
				require.NoError(t, first.db.Create(other).Error)
				activationID = other.ID
			case "legacy-raw":
				updates["confirmation_token"] = raw
			}
			if len(updates) > 0 {
				require.NoError(t, first.db.Model(user).Updates(updates).Error)
			}
			var before *domain.User
			if state != "deleted" {
				before = confirmationCurrentUser(t, first, user.ID)
			}
			require.ErrorIs(t, second.ActivateConfirmationToken(activationID, raw), ErrConfirmationInvalid)
			requireConfirmationRejected(t, second, raw)
			if before != nil {
				requireConfirmationState(t, before, confirmationCurrentUser(t, first, user.ID))
			}
			require.Equal(t, originalEmail, snapshot.Email)
			require.True(t, originalExpiry.Equal(*snapshot.ConfirmationExpiresAt), "mail snapshot expiry must remain immutable")
		})
	}
}

func TestConfirmationLateActivationIsFenced(t *testing.T) {
	testConfirmationActivationFencing(t, resetTestRepositories)
}

func testConfirmationAtomicOneTime(t *testing.T, factory confirmationRepositoryFactory) {
	first, second, user, _ := factory(t)
	_, raw, err := first.GenerateConfirmationToken(user.ID)
	require.NoError(t, err)
	activationResults := runConcurrentMFAVerifications(t, first, second, 12, func(repo *channelAuthIAMRepository, _ int) error {
		return repo.ActivateConfirmationToken(user.ID, raw)
	})
	requireOneConfirmationSuccess(t, activationResults)
	returned := make([]*domain.User, 20)
	results := runConcurrentMFAVerifications(t, first, second, len(returned), func(repo *channelAuthIAMRepository, i int) error {
		var err error
		returned[i], err = repo.ConfirmUserByToken(raw)
		return err
	})
	requireOneConfirmationSuccess(t, results)
	for i, err := range results {
		if err != nil {
			require.Nil(t, returned[i])
		} else {
			requireConfirmationConsumed(t, returned[i])
			require.Equal(t, user.ID, returned[i].ID)
			require.Equal(t, user.AuthVersion, returned[i].AuthVersion)
		}
	}
	requireConfirmationConsumed(t, confirmationCurrentUser(t, second, user.ID))
}

func requireOneConfirmationSuccess(t *testing.T, results []error) {
	t.Helper()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrConfirmationInvalid, "a racing loser must fail closed, not with an infrastructure error")
		}
	}
	require.Equal(t, 1, successes)
}

func TestConfirmationAtomicOneTimeAcrossDatabaseHandles(t *testing.T) {
	testConfirmationAtomicOneTime(t, resetTestRepositories)
}

func testConfirmationResends(t *testing.T, factory confirmationRepositoryFactory) {
	t.Run("only-the-latest-of-concurrent-generations-can-activate", func(t *testing.T) {
		first, second, user, _ := factory(t)
		_, oldRaw := confirmationActiveToken(t, first, user.ID)
		raws := make([]string, 12)
		snapshots := make([]*domain.User, len(raws))
		results := runConcurrentMFAVerifications(t, first, second, len(raws), func(repo *channelAuthIAMRepository, i int) error {
			var err error
			snapshots[i], raws[i], err = repo.GenerateConfirmationToken(user.ID)
			return err
		})
		seen := map[string]bool{}
		for i, err := range results {
			require.NoError(t, err)
			require.False(t, seen[raws[i]], "resends need independent unpredictable proofs")
			seen[raws[i]] = true
			require.Nil(t, snapshots[i].ConfirmationIssuedAt)
		}
		current := confirmationCurrentUser(t, first, user.ID)
		require.Nil(t, current.ConfirmationIssuedAt)
		requireConfirmationRejected(t, first, oldRaw)
		require.ErrorIs(t, first.ActivateConfirmationToken(user.ID, oldRaw), ErrConfirmationInvalid)
		latest := ""
		for i, raw := range raws {
			if snapshots[i].ConfirmationToken == current.ConfirmationToken {
				require.Empty(t, latest)
				latest = raw
			} else {
				require.ErrorIs(t, second.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
				requireConfirmationRejected(t, second, raw)
			}
		}
		require.NotEmpty(t, latest)
		require.NoError(t, second.ActivateConfirmationToken(user.ID, latest))
		confirmed, err := first.ConfirmUserByToken(latest)
		require.NoError(t, err)
		requireConfirmationConsumed(t, confirmed)
		for _, raw := range raws {
			requireConfirmationRejected(t, second, raw)
			require.ErrorIs(t, second.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
		}
	})
	t.Run("resend-and-confirm-serialize-in-one-order", func(t *testing.T) {
		first, second, user, _ := factory(t)
		_, oldRaw := confirmationActiveToken(t, first, user.ID)
		var newer string
		var confirmed *domain.User
		results := runConcurrentMFAVerifications(t, first, second, 2, func(repo *channelAuthIAMRepository, i int) error {
			var err error
			if i == 0 {
				_, newer, err = repo.GenerateConfirmationToken(user.ID)
			} else {
				confirmed, err = repo.ConfirmUserByToken(oldRaw)
			}
			return err
		})
		requireOneConfirmationSuccess(t, results)
		if results[0] == nil {
			require.Nil(t, confirmed)
			require.NoError(t, second.ActivateConfirmationToken(user.ID, newer))
			confirmed, err := second.ConfirmUserByToken(newer)
			require.NoError(t, err)
			requireConfirmationConsumed(t, confirmed)
		} else {
			require.Empty(t, newer)
			requireConfirmationConsumed(t, confirmed)
		}
		requireConfirmationRejected(t, first, oldRaw)
	})
}

func TestConfirmationResendGenerationFencing(t *testing.T) {
	testConfirmationResends(t, resetTestRepositories)
}

func testConfirmationResetAndStaleProfiles(t *testing.T, factory confirmationRepositoryFactory) {
	t.Run("reset-revokes-an-active-proof-and-new-resend-recovers", func(t *testing.T) {
		first, second, user, replacement := factory(t)
		_, raw := confirmationActiveToken(t, first, user.ID)
		_, resetRaw := activeResetToken(t, second, user, 712)
		require.NoError(t, second.ConsumePasswordResetToken(resetRaw, replacement))
		requireConfirmationRejected(t, first, raw)
		require.ErrorIs(t, first.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
		snapshot, fresh := confirmationActiveToken(t, first, user.ID)
		require.EqualValues(t, 1, snapshot.ConfirmationAuthVersion)
		require.Equal(t, replacement, snapshot.PasswordHash)
		confirmed, err := second.ConfirmUserByToken(fresh)
		require.NoError(t, err)
		require.EqualValues(t, 1, confirmed.AuthVersion)
		require.Equal(t, replacement, confirmed.PasswordHash)
	})
	t.Run("new-email-needs-its-own-resend", func(t *testing.T) {
		first, second, user, _ := factory(t)
		oldSnapshot, raw := confirmationActiveToken(t, first, user.ID)
		require.NoError(t, first.db.Model(user).Update("email", "current@example.test").Error)
		requireConfirmationRejected(t, second, raw)
		snapshot, fresh := confirmationActiveToken(t, second, user.ID)
		require.NotEqual(t, oldSnapshot.Email, snapshot.Email)
		require.Equal(t, "current@example.test", snapshot.ConfirmationEmail)
		confirmed, err := first.ConfirmUserByToken(fresh)
		require.NoError(t, err)
		require.Equal(t, "current@example.test", confirmed.Email)
	})
	t.Run("stale-profile-cannot-erase-new-or-resurrect-consumed-proof", func(t *testing.T) {
		first, second, user, _ := factory(t)
		staleBeforeGeneration := *user
		_, raw := confirmationActiveToken(t, first, user.ID)
		active := confirmationCurrentUser(t, second, user.ID)
		staleBeforeGeneration.Name = "Saved after preparation"
		require.NoError(t, NewUserRepository(second.db).Update(&staleBeforeGeneration))
		requireConfirmationState(t, active, confirmationCurrentUser(t, first, user.ID))
		_, newer, err := second.GenerateConfirmationToken(user.ID)
		require.NoError(t, err)
		pending := confirmationCurrentUser(t, first, user.ID)
		active.Name = "Saved after resend"
		require.NoError(t, NewUserRepository(first.db).Update(active))
		requireConfirmationState(t, pending, confirmationCurrentUser(t, second, user.ID))
		requireConfirmationRejected(t, first, raw)
		require.NoError(t, first.ActivateConfirmationToken(user.ID, newer))
		_, err = second.ConfirmUserByToken(newer)
		require.NoError(t, err)
		consumed := confirmationCurrentUser(t, first, user.ID)
		active.Name = "Saved after confirmation"
		require.NoError(t, NewUserRepository(second.db).Update(active))
		current := confirmationCurrentUser(t, first, user.ID)
		requireConfirmationState(t, consumed, current)
		require.Equal(t, active.Name, current.Name)
		requireConfirmationRejected(t, second, newer)
		requireConfirmationRejected(t, second, raw)
	})
	t.Run("profile-write-cannot-mint-confirmation-state", func(t *testing.T) {
		first, second, user, _ := factory(t)
		now := time.Now().UTC()
		user.ConfirmedAt = &now
		user.ConfirmationToken = strings.Repeat("a", 64)
		user.ConfirmationEmail = user.Email
		user.ConfirmationIssuedAt = &now
		user.ConfirmationSentAt = &now
		user.ConfirmationExpiresAt = &now
		require.NoError(t, NewUserRepository(first.db).Update(user))
		current := confirmationCurrentUser(t, second, user.ID)
		require.Nil(t, current.ConfirmedAt)
		require.Empty(t, current.ConfirmationToken)
		require.Nil(t, current.ConfirmationIssuedAt)
	})
}

func TestConfirmationPasswordResetAndStaleProfileFencing(t *testing.T) {
	testConfirmationResetAndStaleProfiles(t, resetTestRepositories)
}

// Wrapping the real database transaction permits deterministic storage/commit
// failure and post-commit concurrency tests without provider or mail mocks.
type confirmationCommitPool struct {
	gorm.ConnPool
	beforeCommit func() error
	afterCommit  func() error
}

func (pool *confirmationCommitPool) BeginTx(ctx context.Context, opts *sql.TxOptions) (gorm.ConnPool, error) {
	var conn gorm.ConnPool
	var err error
	switch underlying := pool.ConnPool.(type) {
	case gorm.TxBeginner:
		conn, err = underlying.BeginTx(ctx, opts)
	case gorm.ConnPoolBeginner:
		conn, err = underlying.BeginTx(ctx, opts)
	default:
		return nil, gorm.ErrInvalidTransaction
	}
	if err != nil {
		return nil, err
	}
	return &confirmationCommitTx{ConnPool: conn, beforeCommit: pool.beforeCommit, afterCommit: pool.afterCommit}, nil
}

type confirmationCommitTx struct {
	gorm.ConnPool
	beforeCommit func() error
	afterCommit  func() error
}

func (tx *confirmationCommitTx) Commit() error {
	underlying := tx.ConnPool.(gorm.TxCommitter)
	if tx.beforeCommit != nil {
		if err := tx.beforeCommit(); err != nil {
			_ = underlying.Rollback()
			return err
		}
	}
	if err := underlying.Commit(); err != nil {
		return err
	}
	if tx.afterCommit != nil {
		return tx.afterCommit()
	}
	return nil
}

func (tx *confirmationCommitTx) Rollback() error {
	return tx.ConnPool.(gorm.TxCommitter).Rollback()
}

func confirmationWithCommitHooks(repo *channelAuthIAMRepository, before, after func() error) *channelAuthIAMRepository {
	db := repo.db.Session(&gorm.Session{NewDB: true, Context: context.Background()})
	db.Statement.ConnPool = &confirmationCommitPool{ConnPool: db.Statement.ConnPool, beforeCommit: before, afterCommit: after}
	return &channelAuthIAMRepository{db: db}
}

func testConfirmationLockedSnapshot(t *testing.T, factory confirmationRepositoryFactory) {
	t.Run("confirm-does-not-upgrade-proof-to-post-commit-reset-epoch", func(t *testing.T) {
		first, second, user, replacement := factory(t)
		_, raw := confirmationActiveToken(t, first, user.ID)
		_, resetRaw := activeResetToken(t, second, user, 713)
		hookCalls := 0
		wrapped := confirmationWithCommitHooks(first, nil, func() error {
			hookCalls++
			return second.ConsumePasswordResetToken(resetRaw, replacement)
		})
		confirmed, err := wrapped.ConfirmUserByToken(raw)
		require.NoError(t, err)
		require.Equal(t, 1, hookCalls)
		requireConfirmationConsumed(t, confirmed)
		require.EqualValues(t, 0, confirmed.AuthVersion, "returned session epoch must be the epoch verified under the lock")
		require.Equal(t, user.PasswordHash, confirmed.PasswordHash)
		current := confirmationCurrentUser(t, second, user.ID)
		require.EqualValues(t, 1, current.AuthVersion)
		require.Equal(t, replacement, current.PasswordHash)
	})
	t.Run("prepare-returns-its-bound-mailbox-not-a-post-commit-reload", func(t *testing.T) {
		first, second, user, _ := factory(t)
		wrapped := confirmationWithCommitHooks(first, nil, func() error {
			return second.db.Model(&domain.User{}).Where("id = ?", user.ID).Update("email", "after-commit@example.test").Error
		})
		snapshot, raw, err := wrapped.GenerateConfirmationToken(user.ID)
		require.NoError(t, err)
		require.Equal(t, user.Email, snapshot.Email)
		require.Equal(t, user.Email, snapshot.ConfirmationEmail)
		require.Equal(t, "after-commit@example.test", confirmationCurrentUser(t, second, user.ID).Email)
		require.ErrorIs(t, first.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
	})
}

func TestConfirmationReturnsLockedSnapshot(t *testing.T) {
	testConfirmationLockedSnapshot(t, resetTestRepositories)
}

func testConfirmationCommitRollback(t *testing.T, factory confirmationRepositoryFactory) {
	for _, phase := range []string{"prepare", "activate", "consume"} {
		t.Run(phase, func(t *testing.T) {
			first, second, user, _ := factory(t)
			_, raw, err := first.GenerateConfirmationToken(user.ID)
			require.NoError(t, err)
			if phase != "activate" {
				require.NoError(t, first.ActivateConfirmationToken(user.ID, raw))
			}
			before := confirmationCurrentUser(t, second, user.ID)
			injected := errors.New("injected confirmation commit failure")
			wrapped := confirmationWithCommitHooks(first, func() error { return injected }, nil)
			if phase == "prepare" {
				snapshot, failedRaw, err := wrapped.GenerateConfirmationToken(user.ID)
				require.ErrorIs(t, err, injected)
				require.Nil(t, snapshot)
				require.Empty(t, failedRaw)
			} else if phase == "activate" {
				require.ErrorIs(t, wrapped.ActivateConfirmationToken(user.ID, raw), injected)
			} else {
				confirmed, err := wrapped.ConfirmUserByToken(raw)
				require.ErrorIs(t, err, injected)
				require.Nil(t, confirmed)
			}
			requireConfirmationState(t, before, confirmationCurrentUser(t, second, user.ID))
			if phase == "activate" {
				requireConfirmationRejected(t, second, raw)
				require.NoError(t, second.ActivateConfirmationToken(user.ID, raw))
			}
			confirmed, err := second.ConfirmUserByToken(raw)
			require.NoError(t, err, "rollback must leave the prior proof usable")
			requireConfirmationConsumed(t, confirmed)
		})
	}
}

func TestConfirmationCommitFailuresRollBack(t *testing.T) {
	testConfirmationCommitRollback(t, resetTestRepositories)
}

func testConfirmationStorageFailures(t *testing.T, factory confirmationRepositoryFactory) {
	for _, phase := range []string{"prepare", "activate", "consume-before", "consume-after"} {
		t.Run(phase, func(t *testing.T) {
			first, second, user, _ := factory(t)
			_, raw, err := first.GenerateConfirmationToken(user.ID)
			require.NoError(t, err)
			if phase != "activate" {
				require.NoError(t, first.ActivateConfirmationToken(user.ID, raw))
			}
			before := confirmationCurrentUser(t, second, user.ID)
			drop := installConfirmationFailure(t, first.db, phase)
			if phase == "prepare" {
				snapshot, failedRaw, err := first.GenerateConfirmationToken(user.ID)
				require.Error(t, err)
				require.False(t, errors.Is(err, ErrConfirmationInvalid))
				require.Nil(t, snapshot)
				require.Empty(t, failedRaw)
			} else if phase == "activate" {
				err := first.ActivateConfirmationToken(user.ID, raw)
				require.Error(t, err)
				require.False(t, errors.Is(err, ErrConfirmationInvalid))
				requireConfirmationRejected(t, second, raw)
			} else {
				confirmed, err := first.ConfirmUserByToken(raw)
				require.Error(t, err)
				require.False(t, errors.Is(err, ErrConfirmationInvalid))
				require.Nil(t, confirmed)
			}
			requireConfirmationState(t, before, confirmationCurrentUser(t, second, user.ID))
			drop()
			if phase == "activate" {
				// A new resend recovers a failed delivery-activation operation;
				// a late completion of the old one cannot activate the new one.
				_, fresh, err := second.GenerateConfirmationToken(user.ID)
				require.NoError(t, err)
				require.ErrorIs(t, first.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
				raw = fresh
				require.NoError(t, second.ActivateConfirmationToken(user.ID, raw))
			}
			confirmed, err := second.ConfirmUserByToken(raw)
			require.NoError(t, err)
			requireConfirmationConsumed(t, confirmed)
		})
	}
}

func installConfirmationFailure(t *testing.T, db *gorm.DB, phase string) func() {
	t.Helper()
	column := "confirmed_at"
	when := "BEFORE"
	if phase == "prepare" {
		column = "confirmation_token"
	} else if phase == "activate" {
		column = "confirmation_issued_at"
	} else if phase == "consume-after" {
		when = "AFTER"
	}
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec(`CREATE FUNCTION confirmation_injected_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected confirmation storage failure'; END $$`).Error)
		require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TRIGGER confirmation_fail %s UPDATE OF %s ON users FOR EACH ROW EXECUTE FUNCTION confirmation_injected_failure()`, when, column)).Error)
		return func() {
			require.NoError(t, db.Exec(`DROP TRIGGER confirmation_fail ON users`).Error)
			require.NoError(t, db.Exec(`DROP FUNCTION confirmation_injected_failure()`).Error)
		}
	}
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TRIGGER confirmation_fail %s UPDATE OF %s ON users BEGIN SELECT RAISE(ABORT, 'injected confirmation storage failure'); END`, when, column)).Error)
	return func() { require.NoError(t, db.Exec(`DROP TRIGGER confirmation_fail`).Error) }
}

func TestConfirmationStorageFailuresRollBack(t *testing.T) {
	testConfirmationStorageFailures(t, resetTestRepositories)
}

func testConfirmationExpiryWhileWaitingForLock(t *testing.T, factory confirmationRepositoryFactory) {
	for _, phase := range []string{"activate", "consume"} {
		t.Run(phase, func(t *testing.T) {
			testConfirmationExpiryWhileWaitingForLockPhase(t, factory, phase)
		})
	}
}

func testConfirmationExpiryWhileWaitingForLockPhase(t *testing.T, factory confirmationRepositoryFactory, phase string) {
	first, second, user, _ := factory(t)
	_, raw, err := first.GenerateConfirmationToken(user.ID)
	require.NoError(t, err)
	if phase == "consume" {
		require.NoError(t, first.ActivateConfirmationToken(user.ID, raw))
	}
	expires := time.Now().UTC().Add(150 * time.Millisecond).Truncate(time.Microsecond)
	require.NoError(t, first.db.Model(user).Update("confirmation_expires_at", expires).Error)
	before := confirmationCurrentUser(t, first, user.ID)
	lock := second.db.Begin()
	require.NoError(t, lock.Error)
	defer lock.Rollback()
	require.NoError(t, lock.Model(&domain.User{}).Where("id = ?", user.ID).UpdateColumn("id", gorm.Expr("id")).Error)
	started := make(chan struct{})
	var once sync.Once
	const callbackName = "test:confirmation_waiting_for_lock"
	require.NoError(t, first.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		once.Do(func() { close(started) })
	}))
	t.Cleanup(func() { _ = first.db.Callback().Update().Remove(callbackName) })
	type result struct {
		user *domain.User
		err  error
	}
	done := make(chan result, 1)
	go func() {
		if phase == "activate" {
			done <- result{err: first.ActivateConfirmationToken(user.ID, raw)}
			return
		}
		confirmed, err := first.ConfirmUserByToken(raw)
		done <- result{confirmed, err}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation did not attempt to acquire the user lock")
	}
	time.Sleep(time.Until(expires.Add(20 * time.Millisecond)))
	require.NoError(t, lock.Commit().Error)
	select {
	case got := <-done:
		require.ErrorIs(t, got.err, ErrConfirmationInvalid)
		require.Nil(t, got.user)
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation did not finish after releasing the user lock")
	}
	requireConfirmationState(t, before, confirmationCurrentUser(t, second, user.ID))
	_, fresh := confirmationActiveToken(t, second, user.ID)
	confirmed, err := first.ConfirmUserByToken(fresh)
	require.NoError(t, err)
	requireConfirmationConsumed(t, confirmed)
}

func TestConfirmationExpiryRecheckedAfterUserLock(t *testing.T) {
	testConfirmationExpiryWhileWaitingForLock(t, resetTestRepositories)
}

func testConfirmationFinalCAS(t *testing.T, factory confirmationRepositoryFactory) {
	for _, phase := range []string{"activate", "consume"} {
		for _, changed := range []string{"expiry", "email", "auth-version", "generation", "confirmed"} {
			t.Run(phase+"-"+changed, func(t *testing.T) {
				first, second, user, _ := factory(t)
				_, raw, err := first.GenerateConfirmationToken(user.ID)
				require.NoError(t, err)
				if phase == "consume" {
					require.NoError(t, first.ActivateConfirmationToken(user.ID, raw))
				}
				before := confirmationCurrentUser(t, second, user.ID)
				const callbackName = "test:confirmation_final_cas"
				injected := false
				require.NoError(t, first.db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
					updates, ok := tx.Statement.Dest.(map[string]interface{})
					if !ok || injected {
						return
					}
					key := "confirmation_issued_at"
					if phase == "consume" {
						key = "confirmed_at"
					}
					if _, ok := updates[key]; !ok {
						return
					}
					// Change state within the locked transaction immediately before
					// its final write. Removing the final CAS predicate must fail
					// this test, even if the earlier in-memory validation remains.
					injected = true
					column := "confirmation_expires_at"
					var value any = time.Now().UTC().Add(-time.Second)
					switch changed {
					case "email":
						column, value = "email", "interleaved@example.test"
					case "auth-version":
						column, value = "auth_version", before.AuthVersion+1
					case "generation":
						column, value = "confirmation_token", "sha256:"+strings.Repeat("0", 64)
					case "confirmed":
						column, value = "confirmed_at", time.Now().UTC()
					}
					fault := tx.Session(&gorm.Session{NewDB: true}).Exec("UPDATE users SET "+column+" = ? WHERE id = ?", value, user.ID)
					if fault.Error != nil {
						tx.AddError(fault.Error)
					}
				}))
				t.Cleanup(func() { _ = first.db.Callback().Update().Remove(callbackName) })
				if phase == "activate" {
					require.ErrorIs(t, first.ActivateConfirmationToken(user.ID, raw), ErrConfirmationInvalid)
				} else {
					requireConfirmationRejected(t, first, raw)
				}
				require.True(t, injected, "test must reach the final state-changing write")
				current := confirmationCurrentUser(t, second, user.ID)
				requireConfirmationState(t, before, current)
				require.Equal(t, before.Email, current.Email)
				require.Equal(t, before.AuthVersion, current.AuthVersion)
				require.NoError(t, first.db.Callback().Update().Remove(callbackName))
				if phase == "activate" {
					require.NoError(t, second.ActivateConfirmationToken(user.ID, raw))
				}
				confirmed, err := second.ConfirmUserByToken(raw)
				require.NoError(t, err)
				requireConfirmationConsumed(t, confirmed)
			})
		}
	}
}

func TestConfirmationFinalCASRechecksBoundState(t *testing.T) {
	testConfirmationFinalCAS(t, resetTestRepositories)
}
