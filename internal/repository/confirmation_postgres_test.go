package repository

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// Reuse the established opt-in, loopback-only DB_DSN helper. Each test receives
// its own disposable schema and two independent database connection pools.
func confirmationPostgresRepositories(t *testing.T) (*channelAuthIAMRepository, *channelAuthIAMRepository, *domain.User, string) {
	t.Helper()
	first, second := isolatedAuthPostgres(t)
	oldHash, err := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.MinCost)
	require.NoError(t, err)
	replacement, err := bcrypt.GenerateFromPassword([]byte("new-password"), bcrypt.MinCost)
	require.NoError(t, err)
	user := &domain.User{Name: "PG confirmation", Email: "confirmation@example.test", PasswordHash: string(oldHash)}
	require.NoError(t, first.Create(user).Error)
	return &channelAuthIAMRepository{db: first}, &channelAuthIAMRepository{db: second}, user, string(replacement)
}

func TestPostgresConfirmationLifecycleAndDigestOnlyStorage(t *testing.T) {
	testConfirmationLifecycle(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationRejectsInvalidOrStaleProofs(t *testing.T) {
	testConfirmationInvalidProofs(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationLateActivationIsFenced(t *testing.T) {
	testConfirmationActivationFencing(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationAtomicOneTimeAcrossDatabaseHandles(t *testing.T) {
	testConfirmationAtomicOneTime(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationResendGenerationFencing(t *testing.T) {
	testConfirmationResends(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationPasswordResetAndStaleProfileFencing(t *testing.T) {
	testConfirmationResetAndStaleProfiles(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationReturnsLockedSnapshot(t *testing.T) {
	testConfirmationLockedSnapshot(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationCommitFailuresRollBack(t *testing.T) {
	testConfirmationCommitRollback(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationStorageFailuresRollBack(t *testing.T) {
	testConfirmationStorageFailures(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationExpiryRecheckedAfterUserLock(t *testing.T) {
	testConfirmationExpiryWhileWaitingForLock(t, confirmationPostgresRepositories)
}

func TestPostgresConfirmationFinalCASRechecksBoundState(t *testing.T) {
	testConfirmationFinalCAS(t, confirmationPostgresRepositories)
}
