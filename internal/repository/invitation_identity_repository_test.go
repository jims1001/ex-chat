package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

func invitationPasswordHash(t *testing.T, password string) string {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	require.NoError(t, err)
	return string(hash)
}

func invitationActivationInput(label, email string) *domain.AgentInvitation {
	_, digest := invitationTestProof(label)
	return &domain.AgentInvitation{TokenDigest: digest, Email: email, AccountID: 99, Role: domain.RoleAdministrator, ExpiresAt: time.Now().Add(time.Hour), DeliveryState: "submitted"}
}

func requireInvitationTableCount(t *testing.T, db *gorm.DB, model any, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(model).Count(&count).Error)
	require.Equal(t, want, count)
}

func TestInvitationActivationCreatesOnlyBoundIdentityAndNeverOverwritesOnRetry(t *testing.T) {
	first, second := invitationTestDBs(t)
	r1, r2 := &UserRepository{db: first}, &UserRepository{db: second}
	invite := invitationActivationInput("create once", "new-invite@example.test")
	originalHash := invitationPasswordHash(t, "Original secret 123!")
	user, err := r1.ActivateInvitedIdentity(invite, "  Invited User  ", originalHash)
	require.NoError(t, err)
	require.NotZero(t, user.ID)
	require.Equal(t, "Invited User", user.Name)
	require.Equal(t, invite.Email, user.Email)
	require.Equal(t, domain.RoleAgent, user.Role, "a tenant administrator invitation never grants a global role")
	require.Equal(t, "User", user.Type)
	require.NotNil(t, user.ConfirmedAt)
	require.Zero(t, user.AuthVersion)
	require.Empty(t, user.PubsubToken)
	require.Empty(t, user.ConfirmationToken)
	retry, err := r2.ActivateInvitedIdentity(invite, "Attacker supplied replacement", invitationPasswordHash(t, "Replacement secret 123!"))
	require.NoError(t, err)
	require.Equal(t, user.ID, retry.ID)
	require.Equal(t, user.Name, retry.Name)
	require.Equal(t, originalHash, retry.PasswordHash)
	var binding domain.InvitationIdentity
	require.NoError(t, first.First(&binding).Error)
	require.Equal(t, invitationOperationKey(invite.TokenDigest), binding.OperationKey)
	require.Equal(t, user.ID, binding.UserID)
	require.Equal(t, invite.Email, binding.Email)
	require.NotEqual(t, invite.TokenDigest, binding.OperationKey)
	encoded, err := json.Marshal(binding)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded))
	requireInvitationTableCount(t, first, &domain.User{}, 1)
	requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 1)
	for _, model := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.UserSession{}, &domain.MFAProfile{}} {
		requireInvitationTableCount(t, first, model, 0)
	}
	// An activation result is an identity checkpoint, never a login/session token.
	encoded, err = json.Marshal(user)
	require.NoError(t, err)
	for _, key := range []string{`"token":`, `"access_token":`, `"refresh_token":`, `"password_hash":`} {
		require.NotContains(t, string(encoded), key)
	}
	altered := *invite
	altered.Email = "different@example.test"
	_, err = r2.ActivateInvitedIdentity(&altered, "Other User", originalHash)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	otherOperation := invitationActivationInput("different mailbox operation", invite.Email)
	_, err = r2.ActivateInvitedIdentity(otherOperation, "Other User", originalHash)
	require.ErrorIs(t, err, ErrInvitationSignInRequired)
	requireInvitationTableCount(t, first, &domain.User{}, 1)
	requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 1)
}

func TestInvitationActivationNeverAdoptsPreexistingOrAmbiguousMailbox(t *testing.T) {
	for _, storedEmail := range []string{"existing@example.test", "Existing@Example.Test"} {
		t.Run(storedEmail, func(t *testing.T) {
			first, _ := invitationTestDBs(t)
			repo := &UserRepository{db: first}
			existing := domain.User{Name: "Protected administrator", Email: storedEmail, PasswordHash: "original-global-password", Role: domain.RoleAdministrator, Type: "SuperAdmin", DisplayName: "Original display name", AuthVersion: 7}
			require.NoError(t, first.Create(&existing).Error)
			profile := domain.MFAProfile{UserID: existing.ID, Enabled: true, Secret: testMFASecret, BackupCodes: `["12345678"]`}
			require.NoError(t, first.Create(&profile).Error)
			// Compare database snapshots so driver timezone representations do not obscure actual writes.
			require.NoError(t, first.First(&existing, existing.ID).Error)
			require.NoError(t, first.First(&profile, profile.ID).Error)
			invite := invitationActivationInput("preexisting", strings.ToLower(storedEmail))
			_, err := repo.ActivateInvitedIdentity(invite, "Replacement", invitationPasswordHash(t, "Replacement 123!"))
			require.ErrorIs(t, err, ErrInvitationSignInRequired)
			var after domain.User
			require.NoError(t, first.First(&after, existing.ID).Error)
			require.Equal(t, existing, after)
			var afterProfile domain.MFAProfile
			require.NoError(t, first.First(&afterProfile, profile.ID).Error)
			require.Equal(t, profile, afterProfile)
			requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 0)
			requireInvitationTableCount(t, first, &domain.Account{}, 0)
			requireInvitationTableCount(t, first, &domain.AccountUser{}, 0)
			requireInvitationTableCount(t, first, &domain.UserSession{}, 0)
			found, err := repo.FindInvitationIdentityByEmail(invite.Email)
			require.NoError(t, err)
			require.Equal(t, existing.ID, found.ID)
			missing, err := repo.FindInvitationIdentityByEmail("missing@example.test")
			require.NoError(t, err)
			require.Nil(t, missing)
		})
	}
	first, _ := invitationTestDBs(t)
	repo := &UserRepository{db: first}
	for _, email := range []string{"ambiguous@example.test", "Ambiguous@Example.Test"} {
		require.NoError(t, first.Create(&domain.User{Name: "Legacy collision", Email: email, PasswordHash: "old"}).Error)
	}
	user, err := repo.FindInvitationIdentityByEmail("ambiguous@example.test")
	require.ErrorIs(t, err, ErrInvitationInvalid)
	require.Nil(t, user)
}

func TestInvitationActivationCheckpointStopsAfterIdentitySecurityChanges(t *testing.T) {
	changes := []struct {
		name   string
		change func(*testing.T, *gorm.DB, *domain.User)
	}{
		{"credential version changed", func(t *testing.T, db *gorm.DB, u *domain.User) {
			require.NoError(t, db.Model(u).Update("auth_version", 1).Error)
		}},
		{"MFA enabled", func(t *testing.T, db *gorm.DB, u *domain.User) {
			require.NoError(t, db.Create(&domain.MFAProfile{UserID: u.ID, Enabled: true, Secret: testMFASecret, BackupCodes: `["12345678"]`}).Error)
		}},
		{"superadmin role", func(t *testing.T, db *gorm.DB, u *domain.User) {
			require.NoError(t, db.Model(u).Update("role", domain.RoleSuperAdmin).Error)
		}},
		{"superadmin type", func(t *testing.T, db *gorm.DB, u *domain.User) {
			require.NoError(t, db.Model(u).Update("type", "SuperAdmin").Error)
		}},
		{"confirmation removed", func(t *testing.T, db *gorm.DB, u *domain.User) {
			require.NoError(t, db.Model(u).Update("confirmed_at", nil).Error)
		}},
		{"mailbox changed", func(t *testing.T, db *gorm.DB, u *domain.User) {
			require.NoError(t, db.Model(u).Update("email", "changed@example.test").Error)
		}},
		{"identity removed", func(t *testing.T, db *gorm.DB, u *domain.User) { require.NoError(t, db.Delete(u).Error) }},
	}
	for _, tc := range changes {
		t.Run(tc.name, func(t *testing.T) {
			first, second := invitationTestDBs(t)
			repo := &UserRepository{db: first}
			invite := invitationActivationInput(tc.name, "security-change@example.test")
			hash := invitationPasswordHash(t, "Original 123!")
			user, err := repo.ActivateInvitedIdentity(invite, "Original", hash)
			require.NoError(t, err)
			tc.change(t, first, user)
			_, err = (&UserRepository{db: second}).ActivateInvitedIdentity(invite, "Replacement", hash)
			require.ErrorIs(t, err, ErrInvitationSignInRequired)
			requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 1)
			requireInvitationTableCount(t, first, &domain.UserSession{}, 0)
		})
	}
}

func TestInvitationActivationRejectsMalformedInput(t *testing.T) {
	first, _ := invitationTestDBs(t)
	repo := &UserRepository{db: first}
	hash := invitationPasswordHash(t, "Example 123!")
	valid := invitationActivationInput("validation", "new@example.test")
	_, err := repo.ActivateInvitedIdentity(nil, "Name", hash)
	require.ErrorIs(t, err, ErrInvitationSignInRequired)
	for _, name := range []string{"", " \t ", strings.Repeat("a", 256)} {
		_, err = repo.ActivateInvitedIdentity(valid, name, hash)
		require.ErrorIs(t, err, ErrInvitationSignInRequired)
	}
	_, err = repo.ActivateInvitedIdentity(valid, "Name", "not a bcrypt hash")
	require.ErrorIs(t, err, ErrInvitationInvalid)
	bound := *valid
	bound.IntendedUserID = new(uint)
	_, err = repo.ActivateInvitedIdentity(&bound, "Name", hash)
	require.ErrorIs(t, err, ErrInvitationSignInRequired)
	bad := *valid
	bad.TokenDigest = "bad"
	_, err = repo.ActivateInvitedIdentity(&bad, "Name", hash)
	require.ErrorIs(t, err, ErrInvitationSignInRequired)
	requireInvitationTableCount(t, first, &domain.User{}, 0)
	requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 0)
}

func testInvitationConcurrentActivation(t *testing.T, first, second *gorm.DB, sameOperation bool) {
	t.Helper()
	const attempts = 12
	hash := invitationPasswordHash(t, "Original concurrent 123!")
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	users := make([]*domain.User, attempts)
	errs := make([]error, attempts)
	invites := make([]*domain.AgentInvitation, attempts)
	for i := range errs {
		label := fmt.Sprintf("operation-%d", i)
		if sameOperation {
			label = "same operation"
		}
		invites[i] = invitationActivationInput(label, "racing@example.test")
		ready.Add(1)
		done.Add(1)
		go func(i int) {
			defer done.Done()
			db := first
			if i%2 == 1 {
				db = second
			}
			ready.Done()
			<-start
			users[i], errs[i] = (&UserRepository{db: db}).ActivateInvitedIdentity(invites[i], fmt.Sprintf("Original name %d", i), hash)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	wins := 0
	var winner *domain.User
	winnerIndex := -1
	for i, err := range errs {
		if sameOperation {
			require.NoError(t, err, "same-operation retry %d must reconcile its committed checkpoint", i)
		}
		if err != nil {
			require.Nil(t, users[i])
			continue
		}
		wins++
		require.NotNil(t, users[i])
		if winner == nil {
			winner, winnerIndex = users[i], i
		}
		require.Equal(t, winner.ID, users[i].ID)
		if sameOperation {
			require.Equal(t, winner.Name, users[i].Name)
			require.Equal(t, winner.PasswordHash, users[i].PasswordHash)
		}
	}
	if sameOperation {
		require.Equal(t, attempts, wins)
	} else {
		require.Equal(t, 1, wins, "unrelated activation operations must never adopt the winning identity")
	}
	require.NotNil(t, winner)
	requireInvitationTableCount(t, first, &domain.User{}, 1)
	requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 1)
	var binding domain.InvitationIdentity
	require.NoError(t, first.First(&binding).Error)
	require.Equal(t, invitationOperationKey(invites[winnerIndex].TokenDigest), binding.OperationKey)
	require.Equal(t, winner.ID, binding.UserID)
	if !sameOperation {
		for i := range invites {
			if i == winnerIndex {
				continue
			}
			user, err := (&UserRepository{db: second}).ActivateInvitedIdentity(invites[i], "Takeover retry", hash)
			require.ErrorIs(t, err, ErrInvitationSignInRequired)
			require.Nil(t, user)
		}
	}
	for _, model := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.UserSession{}} {
		requireInvitationTableCount(t, first, model, 0)
	}
}

func TestInvitationActivationConcurrentSameOperationReconciles(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationConcurrentActivation(t, first, second, true)
}
func TestInvitationActivationConcurrentDifferentOperationsCannotAdoptIdentity(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationConcurrentActivation(t, first, second, false)
}
func TestPostgresInvitationActivationConcurrentSameOperationReconciles(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationConcurrentActivation(t, first, second, true)
}
func TestPostgresInvitationActivationConcurrentDifferentOperationsCannotAdoptIdentity(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationConcurrentActivation(t, first, second, false)
}

func testInvitationActivationBindingRollback(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	invite := invitationActivationInput("atomic binding", "atomic@example.test")
	hash := invitationPasswordHash(t, "Atomic original 123!")
	drop := installInvitationFailureTrigger(t, first, "reject_invitation_binding", "invitation_identities", "INSERT", "injected binding failure")
	_, err := (&UserRepository{db: second}).ActivateInvitedIdentity(invite, "Original", hash)
	require.ErrorContains(t, err, "injected binding failure")
	requireInvitationTableCount(t, first, &domain.User{}, 0)
	requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 0)
	drop()
	_, err = (&UserRepository{db: second}).ActivateInvitedIdentity(invite, "Original", hash)
	require.NoError(t, err)
	requireInvitationTableCount(t, first, &domain.User{}, 1)
	requireInvitationTableCount(t, first, &domain.InvitationIdentity{}, 1)
}

func TestInvitationActivationBindingFailureRollsBackIdentity(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationActivationBindingRollback(t, first, second)
}
func TestPostgresInvitationActivationBindingFailureRollsBackIdentity(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationActivationBindingRollback(t, first, second)
}

func TestInvitationSagaPartialIAMSuccessRetriesSameIdentityAcrossStores(t *testing.T) {
	iam, _ := invitationTestDBs(t)
	ten, tenRetry := invitationTestDBs(t)
	f := newInvitationFixture(t, ten, "saga")
	raw, digest := invitationTestProof("saga partial success")
	invite, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, "new-saga@example.test", domain.RoleAgent, nil, digest, nil)
	require.NoError(t, err)
	require.NoError(t, f.repo.SetAgentInvitationDelivery(invite.ID, digest, "submitted"))
	// Ensure the IAM key cannot accidentally refer to an existing TEN fixture.
	require.NoError(t, iam.Create(&domain.User{ID: 900, Name: "Unrelated IAM user", Email: "unrelated@example.test", PasswordHash: "unchanged"}).Error)
	iamRepo := &UserRepository{db: iam}
	originalHash := invitationPasswordHash(t, "Saga original 123!")
	created, err := iamRepo.ActivateInvitedIdentity(invite, "Saga identity", originalHash)
	require.NoError(t, err)
	require.NoError(t, ten.Exec(`CREATE TRIGGER reject_saga_member BEFORE INSERT ON account_users BEGIN SELECT RAISE(ABORT, 'TEN temporarily unavailable'); END`).Error)
	_, err = f.repo.AcceptAgentInvitation(raw, created.ID, created.Email)
	require.ErrorContains(t, err, "TEN temporarily unavailable")
	requireInvitationUnconsumed(t, ten, invite.ID)
	requireInvitationMemberCount(t, ten, f.account.ID, created.ID, 0)
	retried, err := iamRepo.ActivateInvitedIdentity(invite, "Replacement profile", invitationPasswordHash(t, "Replacement secret 123!"))
	require.NoError(t, err)
	require.Equal(t, created.ID, retried.ID)
	require.Equal(t, originalHash, retried.PasswordHash)
	require.Equal(t, created.Name, retried.Name)
	require.NoError(t, ten.Exec("DROP TRIGGER reject_saga_member").Error)
	member, err := (&AccountRepository{db: tenRetry}).AcceptAgentInvitation(raw, retried.ID, retried.Email)
	require.NoError(t, err)
	// A lost successful TEN response can also be reconciled without new identity,
	// duplicate membership, role mutation, or an incidental login session.
	again, err := f.repo.AcceptAgentInvitation(raw, retried.ID, retried.Email)
	require.NoError(t, err)
	require.Equal(t, member.ID, again.ID)
	requireInvitationMemberCount(t, ten, f.account.ID, created.ID, 1)
	requireInvitationTableCount(t, iam, &domain.User{}, 2)
	requireInvitationTableCount(t, iam, &domain.InvitationIdentity{}, 1)
	for _, model := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.UserSession{}} {
		requireInvitationTableCount(t, iam, model, 0)
	}
	requireInvitationTableCount(t, ten, &domain.User{}, 2)
	requireInvitationTableCount(t, ten, &domain.InvitationIdentity{}, 0)
	requireInvitationTableCount(t, ten, &domain.UserSession{}, 0)
}

func createInvitationMFAUser(t *testing.T, db *gorm.DB, email string, codes ...string) (*domain.User, *domain.MFAProfile) {
	t.Helper()
	now := time.Now().UTC()
	user := &domain.User{Name: "MFA invitee", Email: email, PasswordHash: "unchanged", ConfirmedAt: &now}
	require.NoError(t, db.Create(user).Error)
	encoded, err := json.Marshal(codes)
	require.NoError(t, err)
	profile := &domain.MFAProfile{UserID: user.ID, Enabled: true, Secret: testMFASecret, BackupCodes: string(encoded)}
	require.NoError(t, db.Create(profile).Error)
	return user, profile
}

func loadInvitationMFAProfile(t *testing.T, db *gorm.DB, userID uint) *domain.MFAProfile {
	t.Helper()
	var profile domain.MFAProfile
	require.NoError(t, db.Where("user_id = ?", userID).First(&profile).Error)
	return &profile
}

func invitationReceipts(t *testing.T, profile *domain.MFAProfile) map[string]time.Time {
	t.Helper()
	receipts := map[string]time.Time{}
	if profile.UsedChallenges != "" {
		require.NoError(t, json.Unmarshal([]byte(profile.UsedChallenges), &receipts))
	}
	return receipts
}

func TestInvitationAuthorizationChecksCurrentIdentityAndCurrentMFA(t *testing.T) {
	first, second := invitationTestDBs(t)
	repo := &UserRepository{db: second}
	user := &domain.User{Name: "Initially no MFA", Email: "current@example.test", PasswordHash: "unchanged", AuthVersion: 4}
	require.NoError(t, first.Create(user).Error)
	_, digest := invitationTestProof("current authorization")
	require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, user.AuthVersion, user.Email, digest, ""))
	requireInvitationTableCount(t, first, &domain.MFAProfile{}, 0)
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 3, user.Email, digest, ""), ErrInvitationInvalid)
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 4, "other@example.test", digest, ""), ErrInvitationInvalid)
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(0, 4, user.Email, digest, ""), ErrInvitationInvalid)
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 4, user.Email, "invalid digest", ""), ErrInvitationInvalid)
	profile := &domain.MFAProfile{UserID: user.ID, Enabled: false, Secret: testMFASecret, BackupCodes: `["12345678"]`}
	require.NoError(t, first.Create(profile).Error)
	require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, 4, user.Email, digest, ""))
	require.NoError(t, first.Model(profile).Update("enabled", true).Error)
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 4, user.Email, digest, ""), ErrMFAInvalidCode, "MFA enabled after the session was issued must still require fresh proof")
	require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, 4, user.Email, digest, "12345678"))
	require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, 4, user.Email, digest, ""), "same invitation can reconcile its own unexpired receipt")
	stored := loadInvitationMFAProfile(t, first, user.ID)
	require.JSONEq(t, `[]`, stored.BackupCodes)
	require.EqualValues(t, 2, stored.VerificationAttempts)
	requireInvitationTableCount(t, first, &domain.UserSession{}, 0)
}

func testInvitationConcurrentMFAReceipt(t *testing.T, first, second *gorm.DB, sameInvite, totp bool) {
	t.Helper()
	user, _ := createInvitationMFAUser(t, first, "concurrent-mfa@example.test", "12345678", "12345678", "87654321")
	code := "12345678"
	if totp {
		var err error
		code, err = auth.GenerateTOTPCode(testMFASecret, time.Now())
		require.NoError(t, err)
	}
	const attempts = 12
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	errs := make([]error, attempts)
	for i := range errs {
		ready.Add(1)
		done.Add(1)
		go func(i int) {
			defer done.Done()
			db := first
			if i%2 == 1 {
				db = second
			}
			label := fmt.Sprintf("MFA invite %d", i)
			if sameInvite {
				label = "MFA one invite"
			}
			_, digest := invitationTestProof(label)
			ready.Done()
			<-start
			errs[i] = (&UserRepository{db: db}).AuthorizeInvitationAcceptance(user.ID, user.AuthVersion, user.Email, digest, code)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	wins := 0
	for i, err := range errs {
		if err == nil {
			wins++
			continue
		}
		if sameInvite {
			require.NoError(t, err, "same invitation retry %d must reuse its receipt", i)
		}
		require.True(t, errors.Is(err, ErrMFAInvalidCode) || errors.Is(err, ErrMFAReplay) || errors.Is(err, ErrMFARateLimited), "unexpected infrastructure failure: %v", err)
	}
	profile := loadInvitationMFAProfile(t, first, user.ID)
	if sameInvite {
		require.Equal(t, attempts, wins)
		require.EqualValues(t, 1, profile.VerificationAttempts)
	} else {
		require.Equal(t, 1, wins, "a factor receipt is bound to exactly one invitation")
		require.EqualValues(t, attempts, profile.VerificationAttempts)
	}
	if totp {
		require.Positive(t, profile.LastTOTPStep)
		require.JSONEq(t, `["12345678","12345678","87654321"]`, profile.BackupCodes)
	} else {
		require.JSONEq(t, `["87654321"]`, profile.BackupCodes)
	}
	receipts := invitationReceipts(t, profile)
	require.Len(t, receipts, 1)
	for key, expires := range receipts {
		require.True(t, strings.HasPrefix(key, "invitation:"))
		require.WithinDuration(t, time.Now().Add(5*time.Minute), expires, 10*time.Second)
	}
	requireInvitationTableCount(t, first, &domain.UserSession{}, 0)
}

func TestInvitationMFASameInvitationConcurrentRetriesConsumeOneBackup(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationConcurrentMFAReceipt(t, first, second, true, false)
}
func TestInvitationMFADifferentInvitationsCannotReuseBackupOrTOTP(t *testing.T) {
	for _, totp := range []bool{false, true} {
		t.Run(fmt.Sprintf("TOTP=%t", totp), func(t *testing.T) {
			first, second := invitationTestDBs(t)
			testInvitationConcurrentMFAReceipt(t, first, second, false, totp)
		})
	}
}
func TestPostgresInvitationMFASameInvitationConcurrentRetriesConsumeOneBackup(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationConcurrentMFAReceipt(t, first, second, true, false)
}
func TestPostgresInvitationMFADifferentInvitationsCannotReuseBackupOrTOTP(t *testing.T) {
	for _, totp := range []bool{false, true} {
		t.Run(fmt.Sprintf("TOTP=%t", totp), func(t *testing.T) {
			first, second := isolatedAuthPostgres(t)
			migrateInvitationTestModels(t, first)
			testInvitationConcurrentMFAReceipt(t, first, second, false, totp)
		})
	}
}

func TestInvitationMFAReceiptBoundToCredentialVersionAndSecret(t *testing.T) {
	for _, change := range []string{"credential version", "MFA secret"} {
		t.Run(change, func(t *testing.T) {
			first, second := invitationTestDBs(t)
			user, profile := createInvitationMFAUser(t, first, "receipt-binding@example.test", "12345678", "87654321")
			repo := &UserRepository{db: second}
			_, digest := invitationTestProof("security-bound receipt")
			require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"))
			if change == "credential version" {
				require.NoError(t, first.Model(user).Update("auth_version", 1).Error)
				require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, ""), ErrInvitationInvalid)
			} else {
				require.NoError(t, first.Model(profile).Update("secret", "KRSXG5DSNFXGOIDB").Error)
			}
			currentVersion := int64(0)
			if change == "credential version" {
				currentVersion = 1
			}
			require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, currentVersion, user.Email, digest, ""), ErrMFAInvalidCode, "a pre-change receipt cannot satisfy current MFA")
			require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, currentVersion, user.Email, digest, "12345678"), ErrMFAInvalidCode)
			require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, currentVersion, user.Email, digest, "87654321"))
			stored := loadInvitationMFAProfile(t, first, user.ID)
			require.JSONEq(t, `[]`, stored.BackupCodes)
			require.Len(t, invitationReceipts(t, stored), 2)
		})
	}
}

func TestInvitationMFAReceiptExpiresAndCannotResurrectConsumedFactor(t *testing.T) {
	first, second := invitationTestDBs(t)
	user, _ := createInvitationMFAUser(t, first, "expiry@example.test", "12345678", "87654321")
	repo := &UserRepository{db: second}
	_, digest := invitationTestProof("expires")
	require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"))
	profile := loadInvitationMFAProfile(t, first, user.ID)
	receipts := invitationReceipts(t, profile)
	for key := range receipts {
		receipts[key] = time.Now().Add(-time.Second)
	}
	receipts["unrelated-live-login-challenge"] = time.Now().Add(time.Minute)
	encoded, err := json.Marshal(receipts)
	require.NoError(t, err)
	require.NoError(t, first.Model(profile).Update("used_challenges", string(encoded)).Error)
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, ""), ErrMFAInvalidCode)
	profile = loadInvitationMFAProfile(t, first, user.ID)
	retained := invitationReceipts(t, profile)
	require.Len(t, retained, 1)
	require.Contains(t, retained, "unrelated-live-login-challenge")
	require.ErrorIs(t, repo.AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"), ErrMFAInvalidCode)
	require.NoError(t, repo.AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "87654321"))
}

func TestInvitationMFAAttemptsSharePersistentThrottleWithLogin(t *testing.T) {
	first, second := invitationTestDBs(t)
	user, _ := createInvitationMFAUser(t, first, "throttle@example.test", "12345678")
	for i := 0; i < mfaVerificationLimit; i++ {
		_, digest := invitationTestProof(fmt.Sprintf("invalid-%d", i))
		db := first
		if i%2 == 1 {
			db = second
		}
		require.ErrorIs(t, (&UserRepository{db: db}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "bad"), ErrMFAInvalidCode)
	}
	_, digest := invitationTestProof("new token does not reset throttle")
	require.ErrorIs(t, (&UserRepository{db: second}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"), ErrMFARateLimited)
	require.ErrorIs(t, (&channelAuthIAMRepository{db: first}).VerifyMFALogin(user.ID, "12345678", "login-challenge", time.Now().Add(time.Minute)), ErrMFARateLimited)
	profile := loadInvitationMFAProfile(t, first, user.ID)
	require.EqualValues(t, mfaVerificationLimit+2, profile.VerificationAttempts)
	require.JSONEq(t, `["12345678"]`, profile.BackupCodes)
	require.Empty(t, invitationReceipts(t, profile))
	require.NoError(t, first.Model(profile).Update("verification_window_started_at", time.Now().Add(-mfaVerificationWindow-time.Second)).Error)
	require.NoError(t, (&UserRepository{db: second}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"))
	profile = loadInvitationMFAProfile(t, first, user.ID)
	require.EqualValues(t, 1, profile.VerificationAttempts)
}

func testInvitationMFAStorageRollback(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	user, _ := createInvitationMFAUser(t, first, "rollback-mfa@example.test", "12345678")
	_, digest := invitationTestProof("rollback MFA")
	drop := installInvitationFailureTrigger(t, first, "reject_invitation_mfa", "mfa_profiles", "UPDATE OF verification_attempts", "injected MFA receipt failure")
	err := (&UserRepository{db: second}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678")
	require.ErrorContains(t, err, "injected MFA receipt failure")
	profile := loadInvitationMFAProfile(t, first, user.ID)
	require.Zero(t, profile.AuthVersion)
	require.Zero(t, profile.VerificationAttempts)
	require.Nil(t, profile.VerificationWindowStartedAt)
	require.Empty(t, profile.UsedChallenges)
	require.JSONEq(t, `["12345678"]`, profile.BackupCodes)
	drop()
	require.NoError(t, (&UserRepository{db: second}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"))
	profile = loadInvitationMFAProfile(t, first, user.ID)
	require.EqualValues(t, 1, profile.AuthVersion)
	require.EqualValues(t, 1, profile.VerificationAttempts)
	require.Len(t, invitationReceipts(t, profile), 1)
}

func TestInvitationMFAStorageFailureRollsBackReceiptFactorAndThrottle(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationMFAStorageRollback(t, first, second)
}
func TestPostgresInvitationMFAStorageFailureRollsBackReceiptFactorAndThrottle(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationMFAStorageRollback(t, first, second)
}

func TestInvitationMFAMalformedReplayStateFailsClosedAndRollsBack(t *testing.T) {
	first, second := invitationTestDBs(t)
	user, profile := createInvitationMFAUser(t, first, "malformed-mfa@example.test", "12345678")
	require.NoError(t, first.Model(profile).Update("used_challenges", "broken JSON").Error)
	_, digest := invitationTestProof("malformed receipt")
	require.Error(t, (&UserRepository{db: second}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"))
	stored := loadInvitationMFAProfile(t, first, user.ID)
	require.Zero(t, stored.AuthVersion)
	require.Zero(t, stored.VerificationAttempts)
	require.JSONEq(t, `["12345678"]`, stored.BackupCodes)
}

func TestInvitationMFAThrottleSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "invitation-reopen.sqlite")
	first := openMFATestDB(t, path)
	migrateInvitationTestModels(t, first)
	user, _ := createInvitationMFAUser(t, first, "reopen@example.test", "12345678")
	_, digest := invitationTestProof("reopened receipt")
	for i := 0; i < mfaVerificationLimit; i++ {
		require.ErrorIs(t, (&UserRepository{db: first}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "bad"), ErrMFAInvalidCode)
	}
	reopened := openMFATestDB(t, path)
	require.ErrorIs(t, (&UserRepository{db: reopened}).AuthorizeInvitationAcceptance(user.ID, 0, user.Email, digest, "12345678"), ErrMFARateLimited)
	require.JSONEq(t, `["12345678"]`, loadInvitationMFAProfile(t, reopened, user.ID).BackupCodes)
}

func testInvitationActivationRacesIndependentSignup(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	activationHash := invitationPasswordHash(t, "Invited creation 123!")
	signupHash := invitationPasswordHash(t, "Independent signup 123!")
	for i := 0; i < 6; i++ {
		email := fmt.Sprintf("signup-race-%d@example.test", i)
		invite := invitationActivationInput(email, email)
		signup := &domain.User{Name: "Independent signup", Email: email, PasswordHash: signupHash, DisplayName: "Private original profile", Role: domain.RoleAdministrator, AuthVersion: 3}
		var activated *domain.User
		var activationErr, signupErr error
		start := make(chan struct{})
		var ready, done sync.WaitGroup
		ready.Add(2)
		done.Add(2)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			activated, activationErr = (&UserRepository{db: first}).ActivateInvitedIdentity(invite, "Invited creation", activationHash)
		}()
		go func() { defer done.Done(); ready.Done(); <-start; signupErr = second.Create(signup).Error }()
		ready.Wait()
		close(start)
		done.Wait()
		require.True(t, (activationErr == nil) != (signupErr == nil), "exactly one independent creator may own this email; activation=%v signup=%v", activationErr, signupErr)
		var stored domain.User
		require.NoError(t, first.Where("email = ?", email).First(&stored).Error)
		if signupErr == nil {
			require.ErrorIs(t, activationErr, ErrInvitationSignInRequired)
			require.Nil(t, activated, "email equality may not adopt the independently registered identity")
			require.Equal(t, signup.ID, stored.ID)
			require.Equal(t, signup.Name, stored.Name)
			require.Equal(t, signup.DisplayName, stored.DisplayName)
			require.Equal(t, signupHash, stored.PasswordHash)
			require.Equal(t, signup.Role, stored.Role)
			require.Equal(t, signup.AuthVersion, stored.AuthVersion)
			var count int64
			require.NoError(t, first.Model(&domain.InvitationIdentity{}).Where("operation_key = ?", invitationOperationKey(invite.TokenDigest)).Count(&count).Error)
			require.Zero(t, count)
			_, err := (&UserRepository{db: first}).ActivateInvitedIdentity(invite, "Takeover retry", activationHash)
			require.ErrorIs(t, err, ErrInvitationSignInRequired)
		} else {
			require.NotNil(t, activated)
			require.Equal(t, activated.ID, stored.ID)
			require.Equal(t, "Invited creation", stored.Name)
			require.Equal(t, activationHash, stored.PasswordHash)
			require.Equal(t, domain.RoleAgent, stored.Role)
			retry, err := (&UserRepository{db: second}).ActivateInvitedIdentity(invite, "Different profile", signupHash)
			require.NoError(t, err)
			require.Equal(t, activated.ID, retry.ID)
			require.Equal(t, activationHash, retry.PasswordHash)
		}
	}
	requireInvitationTableCount(t, first, &domain.User{}, 6)
	for _, model := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.UserSession{}} {
		requireInvitationTableCount(t, first, model, 0)
	}
}

func TestInvitationActivationRacesIndependentSignupWithoutIdentityTakeover(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationActivationRacesIndependentSignup(t, first, second)
}

func TestPostgresInvitationActivationRacesIndependentSignupWithoutIdentityTakeover(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationActivationRacesIndependentSignup(t, first, second)
}
