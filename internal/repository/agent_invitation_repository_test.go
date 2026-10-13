package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// The same scenarios run against independent connection pools. PostgreSQL tests
// below reuse the opt-in, disposable-schema auth harness used by the CI gate.
func migrateInvitationTestModels(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.AutoMigrate(&domain.User{}, &domain.Account{}, &domain.CustomRole{}, &domain.AccountUser{}, &domain.AgentInvitation{}, &domain.InvitationIdentity{}, &domain.MFAProfile{}, &domain.UserSession{}))
}

func invitationTestDBs(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invitations.sqlite")
	first := openMFATestDB(t, path)
	migrateInvitationTestModels(t, first)
	return first, openMFATestDB(t, path)
}

type invitationFixture struct {
	db           *gorm.DB
	repo         *AccountRepository
	account      domain.Account
	issuer       domain.User
	issuerMember domain.AccountUser
	invitee      domain.User
}

func newInvitationFixture(t *testing.T, db *gorm.DB, suffix string) *invitationFixture {
	t.Helper()
	f := &invitationFixture{db: db, repo: &AccountRepository{db: db}}
	f.account = domain.Account{Name: "Disposable invitation " + suffix, Status: "active"}
	require.NoError(t, db.Create(&f.account).Error)
	f.issuer = domain.User{Name: "Issuer", Email: "issuer-" + suffix + "@example.test", PasswordHash: "unchanged-issuer"}
	f.invitee = domain.User{Name: "Existing identity", Email: "invitee-" + suffix + "@example.test", PasswordHash: "unchanged-invitee", Role: domain.RoleAgent, DisplayName: "Personal profile"}
	require.NoError(t, db.Create(&f.issuer).Error)
	require.NoError(t, db.Create(&f.invitee).Error)
	f.issuerMember = domain.AccountUser{AccountID: f.account.ID, UserID: f.issuer.ID, Role: domain.RoleAdministrator}
	require.NoError(t, db.Create(&f.issuerMember).Error)
	return f
}

func invitationTestProof(label string) (string, string) {
	rawSum := sha256.Sum256([]byte("disposable invitation proof: " + label))
	raw := hex.EncodeToString(rawSum[:])
	digest, _ := AgentInvitationTokenDigest(raw)
	return raw, digest
}

func (f *invitationFixture) issue(t *testing.T, label, role string, custom *uint) (*domain.AgentInvitation, string) {
	t.Helper()
	raw, digest := invitationTestProof(label)
	invite, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, role, custom, digest, &f.invitee.ID)
	require.NoError(t, err)
	require.NoError(t, f.repo.SetAgentInvitationDelivery(invite.ID, digest, "submitted"))
	return invite, raw
}

func requireInvitationMemberCount(t *testing.T, db *gorm.DB, accountID, userID uint, want int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", accountID, userID).Count(&count).Error)
	require.Equal(t, want, count)
}

func requireInvitationUnconsumed(t *testing.T, db *gorm.DB, id uint) {
	t.Helper()
	var stored domain.AgentInvitation
	require.NoError(t, db.First(&stored, id).Error)
	require.Nil(t, stored.ConsumedAt)
	require.Nil(t, stored.AcceptedUserID)
	require.Nil(t, stored.AcceptedMembershipID)
}

// Inject failures at the persistence boundary, after earlier writes in the same
// transaction, so rollback is verified on both production PostgreSQL and SQLite.
func installInvitationFailureTrigger(t *testing.T, db *gorm.DB, name, table, event, message string) func() {
	t.Helper()
	if db.Dialector.Name() == "postgres" {
		require.NoError(t, db.Exec(fmt.Sprintf("CREATE FUNCTION %s_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION '%s'; END $$", name, message)).Error)
		require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s FOR EACH ROW EXECUTE FUNCTION %s_fn()", name, event, table, name)).Error)
	} else {
		require.NoError(t, db.Exec(fmt.Sprintf("CREATE TRIGGER %s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT, '%s'); END", name, event, table, message)).Error)
	}
	var once sync.Once
	drop := func() {
		once.Do(func() {
			if db.Dialector.Name() == "postgres" {
				require.NoError(t, db.Exec(fmt.Sprintf("DROP TRIGGER %s ON %s", name, table)).Error)
				require.NoError(t, db.Exec(fmt.Sprintf("DROP FUNCTION %s_fn()", name)).Error)
			} else {
				require.NoError(t, db.Exec("DROP TRIGGER "+name).Error)
			}
		})
	}
	t.Cleanup(drop)
	return drop
}

func TestInvitationProofIsDigestOnlyAndPrivateBindingsAreNotSerialized(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "digest")
	raw, digest := invitationTestProof("digest")
	expected := sha256.Sum256([]byte(raw))
	require.Equal(t, hex.EncodeToString(expected[:]), digest)
	for _, invalid := range []string{"", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("z", 64), " " + raw, raw + " "} {
		_, err := AgentInvitationTokenDigest(invalid)
		require.ErrorIs(t, err, ErrInvitationInvalid)
		_, err = f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, domain.RoleAgent, nil, invalid, nil)
		require.ErrorIs(t, err, ErrInvitationInvalid)
	}
	invite, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, domain.RoleAgent, nil, digest, &f.invitee.ID)
	require.NoError(t, err)
	var stored domain.AgentInvitation
	require.NoError(t, first.First(&stored, invite.ID).Error)
	require.Equal(t, digest, stored.TokenDigest)
	require.NotEqual(t, raw, stored.TokenDigest)
	require.InDelta(t, float64(AgentInvitationLifetime), float64(stored.ExpiresAt.Sub(stored.CreatedAt)), float64(time.Second))
	columns, err := first.Migrator().ColumnTypes(&domain.AgentInvitation{})
	require.NoError(t, err)
	for _, column := range columns {
		require.NotContains(t, []string{"token", "raw_token", "password", "password_hash"}, column.Name())
	}
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	for _, hidden := range []string{raw, digest, "token_digest", "inviter_id", "inviter_membership_id", "intended_user_id", "accepted_user_id", "accepted_membership_id", "role_fingerprint"} {
		require.NotContains(t, string(encoded), hidden)
	}
}

func TestInvitationGrantRequiresCurrentSameTenantAuthority(t *testing.T) {
	cases := []struct {
		name, managerRole, grantRole         string
		custom, foreignCustom, managerCustom bool
		want                                 error
	}{
		{"administrator grants agent", domain.RoleAdministrator, domain.RoleAgent, false, false, false, nil},
		{"administrator grants administrator", domain.RoleAdministrator, domain.RoleAdministrator, false, false, false, nil},
		{"administrator grants own custom role", domain.RoleAdministrator, domain.RoleAgent, true, false, false, nil},
		{"cross tenant custom role", domain.RoleAdministrator, domain.RoleAgent, true, true, false, ErrInvitationInvalid},
		{"administrator with custom role", domain.RoleAdministrator, domain.RoleAdministrator, true, false, false, ErrInvitationInvalid},
		{"global privilege escalation", domain.RoleAdministrator, domain.RoleSuperAdmin, false, false, false, ErrInvitationInvalid},
		{"plain agent", domain.RoleAgent, domain.RoleAgent, false, false, false, ErrInvitationForbidden},
		{"manager grants plain agent", domain.RoleAgent, domain.RoleAgent, false, false, true, nil},
		{"manager grants administrator", domain.RoleAgent, domain.RoleAdministrator, false, false, true, ErrInvitationForbidden},
		{"manager grants custom role", domain.RoleAgent, domain.RoleAgent, true, false, true, ErrInvitationForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, _ := invitationTestDBs(t)
			f := newInvitationFixture(t, first, "grant")
			accountID := f.account.ID
			if tc.foreignCustom {
				other := domain.Account{Name: "Foreign tenant"}
				require.NoError(t, first.Create(&other).Error)
				accountID = other.ID
			}
			custom := domain.CustomRole{AccountID: accountID, Name: "Scoped role", Permissions: `["conversation_manage"]`}
			require.NoError(t, first.Create(&custom).Error)
			updates := map[string]any{"role": tc.managerRole}
			if tc.managerCustom {
				managerRole := domain.CustomRole{AccountID: f.account.ID, Name: "Invitation manager", Permissions: `["agent_manage"]`}
				require.NoError(t, first.Create(&managerRole).Error)
				updates["custom_role_id"] = managerRole.ID
			}
			require.NoError(t, first.Model(&domain.AccountUser{}).Where("id = ?", f.issuerMember.ID).Updates(updates).Error)
			var roleID *uint
			if tc.custom {
				roleID = &custom.ID
			}
			raw, digest := invitationTestProof(tc.name)
			invite, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, tc.grantRole, roleID, digest, &f.invitee.ID)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				require.Nil(t, invite)
				return
			}
			require.NoError(t, err)
			require.NoError(t, f.repo.SetAgentInvitationDelivery(invite.ID, digest, "submitted"))
			member, err := f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
			require.NoError(t, err)
			require.Equal(t, f.account.ID, member.AccountID)
			require.Equal(t, tc.grantRole, member.Role)
			require.Equal(t, roleID, member.CustomRoleID)
			var unchanged domain.User
			require.NoError(t, first.First(&unchanged, f.invitee.ID).Error)
			require.Equal(t, f.invitee.Name, unchanged.Name)
			require.Equal(t, f.invitee.DisplayName, unchanged.DisplayName)
			require.Equal(t, f.invitee.PasswordHash, unchanged.PasswordHash)
			require.Equal(t, f.invitee.Role, unchanged.Role, "tenant role grants never change the global identity")
		})
	}
}

func TestInvitationDeliveryRevokeExpiryAndReissue(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "lifecycle")
	raw, digest := invitationTestProof("first")
	invite, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, domain.RoleAgent, nil, digest, &f.invitee.ID)
	require.NoError(t, err)
	_, err = f.repo.InspectAgentInvitation(raw)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	_, err = f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	require.NoError(t, f.repo.SetAgentInvitationDelivery(invite.ID, digest, "delivery_unknown"))
	_, err = f.repo.InspectAgentInvitation(raw)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	require.ErrorIs(t, f.repo.SetAgentInvitationDelivery(invite.ID, digest, "submitted"), ErrInvitationInvalid)
	raw2, digest2 := invitationTestProof("reissued")
	second, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, domain.RoleAgent, nil, digest2, &f.invitee.ID)
	require.NoError(t, err)
	require.Equal(t, invite.ID, second.ID)
	require.ErrorIs(t, f.repo.SetAgentInvitationDelivery(invite.ID, digest, "submitted"), ErrInvitationInvalid, "a delayed old mail result must not mark the new invitation delivered")
	_, err = f.repo.InspectAgentInvitation(raw)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	require.NoError(t, f.repo.SetAgentInvitationDelivery(second.ID, digest2, "captured_mock"))
	_, err = f.repo.InspectAgentInvitation(raw2)
	require.NoError(t, err)
	require.NoError(t, f.repo.RevokeAgentInvitation(f.account.ID, f.issuer.ID, second.ID))
	_, err = f.repo.AcceptAgentInvitation(raw2, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	third, raw3 := f.issue(t, "after revoke", domain.RoleAgent, nil)
	require.Equal(t, invite.ID, third.ID)
	require.Nil(t, third.RevokedAt)
	require.NoError(t, first.Model(&domain.AgentInvitation{}).Where("id = ?", third.ID).Update("expires_at", time.Now().Add(-time.Second)).Error)
	_, err = f.repo.AcceptAgentInvitation(raw3, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	requireInvitationUnconsumed(t, first, third.ID)
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 0)

	// Revocation can beat an in-flight mail provider result while delivery is
	// still pending. Its later success must not make the proof usable.
	raw4, digest4 := invitationTestProof("revoked before mail result")
	pending, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, domain.RoleAgent, nil, digest4, &f.invitee.ID)
	require.NoError(t, err)
	require.NoError(t, f.repo.RevokeAgentInvitation(f.account.ID, f.issuer.ID, pending.ID))
	require.ErrorIs(t, f.repo.SetAgentInvitationDelivery(pending.ID, digest4, "submitted"), ErrInvitationInvalid)
	_, err = f.repo.InspectAgentInvitation(raw4)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	_, err = f.repo.AcceptAgentInvitation(raw4, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	requireInvitationUnconsumed(t, first, pending.ID)
}

func TestInvitationAcceptanceRechecksIdentityIssuerGrantAndAccount(t *testing.T) {
	cases := []struct {
		name                  string
		change                func(*testing.T, *invitationFixture, *domain.AgentInvitation)
		wrongUser, wrongEmail bool
	}{
		{name: "wrong identity", wrongUser: true}, {name: "wrong mailbox", wrongEmail: true},
		{name: "issuer removed", change: func(t *testing.T, f *invitationFixture, _ *domain.AgentInvitation) {
			require.NoError(t, f.db.Delete(&domain.AccountUser{}, f.issuerMember.ID).Error)
		}},
		{name: "issuer removed and readded", change: func(t *testing.T, f *invitationFixture, _ *domain.AgentInvitation) {
			require.NoError(t, f.db.Delete(&domain.AccountUser{}, f.issuerMember.ID).Error)
			replacement := domain.AccountUser{AccountID: f.account.ID, UserID: f.issuer.ID, Role: domain.RoleAdministrator}
			require.NoError(t, f.db.Create(&replacement).Error)
			require.NotEqual(t, f.issuerMember.ID, replacement.ID)
		}},
		{name: "issuer demoted", change: func(t *testing.T, f *invitationFixture, _ *domain.AgentInvitation) {
			require.NoError(t, f.db.Model(&domain.AccountUser{}).Where("id = ?", f.issuerMember.ID).Update("role", domain.RoleAgent).Error)
		}},
		{name: "custom grant permissions changed", change: func(t *testing.T, f *invitationFixture, invite *domain.AgentInvitation) {
			require.NoError(t, f.db.Model(&domain.CustomRole{}).Where("id = ?", *invite.CustomRoleID).Update("permissions", `["agent_manage"]`).Error)
		}},
		{name: "custom grant name changed", change: func(t *testing.T, f *invitationFixture, invite *domain.AgentInvitation) {
			require.NoError(t, f.db.Model(&domain.CustomRole{}).Where("id = ?", *invite.CustomRoleID).Update("name", "Changed role").Error)
		}},
		{name: "custom grant deleted", change: func(t *testing.T, f *invitationFixture, invite *domain.AgentInvitation) {
			require.NoError(t, f.db.Delete(&domain.CustomRole{}, *invite.CustomRoleID).Error)
		}},
		{name: "account inactive", change: func(t *testing.T, f *invitationFixture, _ *domain.AgentInvitation) {
			require.NoError(t, f.db.Model(&domain.Account{}).Where("id = ?", f.account.ID).Update("status", "suspended").Error)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, _ := invitationTestDBs(t)
			f := newInvitationFixture(t, first, "recheck")
			custom := domain.CustomRole{AccountID: f.account.ID, Name: "Original role", Permissions: `["conversation_manage"]`}
			require.NoError(t, first.Create(&custom).Error)
			invite, raw := f.issue(t, tc.name, domain.RoleAgent, &custom.ID)
			if tc.change != nil {
				tc.change(t, f, invite)
			}
			userID, email := f.invitee.ID, f.invitee.Email
			if tc.wrongUser {
				userID = f.issuer.ID
			}
			if tc.wrongEmail {
				email = "other@example.test"
			}
			member, err := f.repo.AcceptAgentInvitation(raw, userID, email)
			require.Error(t, err)
			require.Nil(t, member)
			requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 0)
			requireInvitationUnconsumed(t, first, invite.ID)
		})
	}
}

func TestInvitationConsumedProofCannotRecreateRemovedMembership(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "remove")
	invite, raw := f.issue(t, "remove", domain.RoleAgent, nil)
	member, err := f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.NoError(t, err)
	again, err := f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.NoError(t, err)
	require.Equal(t, member.ID, again.ID)
	require.NoError(t, first.Delete(&domain.AccountUser{}, member.ID).Error)
	_, err = f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 0)
	replacement := domain.AccountUser{AccountID: f.account.ID, UserID: f.invitee.ID, Role: domain.RoleAdministrator}
	require.NoError(t, first.Create(&replacement).Error)
	require.NotEqual(t, member.ID, replacement.ID)
	_, err = f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	var stored domain.AgentInvitation
	require.NoError(t, first.First(&stored, invite.ID).Error)
	require.Equal(t, member.ID, *stored.AcceptedMembershipID)
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 1)
}

func testInvitationAcceptanceRollback(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	f := newInvitationFixture(t, first, "rollback")
	invite, raw := f.issue(t, "rollback", domain.RoleAgent, nil)
	dropConsumption := installInvitationFailureTrigger(t, first, "reject_invitation_consumption", "agent_invitations", "UPDATE OF consumed_at", "injected consumption failure")
	_, err := (&AccountRepository{db: second}).AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorContains(t, err, "injected consumption failure")
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 0)
	requireInvitationUnconsumed(t, first, invite.ID)
	dropConsumption()
	dropMember := installInvitationFailureTrigger(t, first, "reject_invitation_member", "account_users", "INSERT", "injected member failure")
	_, err = f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorContains(t, err, "injected member failure")
	requireInvitationUnconsumed(t, first, invite.ID)
	dropMember()
	member, err := f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.NoError(t, err)
	require.NotZero(t, member.ID)
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 1)
}

func TestInvitationAcceptanceRollsBackMembershipAndConsumption(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationAcceptanceRollback(t, first, second)
}
func TestPostgresInvitationAcceptanceRollsBackMembershipAndConsumption(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationAcceptanceRollback(t, first, second)
}

func testInvitationConcurrentAcceptance(t *testing.T, first, second *gorm.DB) {
	t.Helper()
	f := newInvitationFixture(t, first, "concurrent")
	invite, raw := f.issue(t, "concurrent", domain.RoleAgent, nil)
	const attempts = 16
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	members := make([]*domain.AccountUser, attempts)
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
			ready.Done()
			<-start
			members[i], errs[i] = (&AccountRepository{db: db}).AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
		}(i)
	}
	ready.Wait()
	close(start)
	done.Wait()
	var membershipID uint
	for i, err := range errs {
		require.NoError(t, err, "attempt %d must accept or reconcile, never return a SQLite snapshot-upgrade failure", i)
		require.NotNil(t, members[i])
		if membershipID == 0 {
			membershipID = members[i].ID
		}
		require.Equal(t, membershipID, members[i].ID)
	}
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 1)
	var stored domain.AgentInvitation
	require.NoError(t, first.First(&stored, invite.ID).Error)
	require.NotNil(t, stored.ConsumedAt)
	require.Equal(t, f.invitee.ID, *stored.AcceptedUserID)
	require.Equal(t, membershipID, *stored.AcceptedMembershipID)
}

func TestInvitationConcurrentAcceptanceReconcilesAcrossDatabaseHandles(t *testing.T) {
	first, second := invitationTestDBs(t)
	testInvitationConcurrentAcceptance(t, first, second)
}
func TestPostgresInvitationConcurrentAcceptanceReconciles(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	migrateInvitationTestModels(t, first)
	testInvitationConcurrentAcceptance(t, first, second)
}

func TestInvitationInvalidCreationDoesNotPersistOrEscalate(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "invalid")
	_, digest := invitationTestProof("invalid")
	for _, email := range []string{"", " Invitee@example.test", "Invitee@example.test", strings.Repeat("x", 256)} {
		_, err := f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, email, domain.RoleAgent, nil, digest, nil)
		require.ErrorIs(t, err, ErrInvitationInvalid)
	}
	_, err := f.repo.CreateAgentInvitation(f.account.ID, f.invitee.ID, f.invitee.Email, domain.RoleAgent, nil, digest, nil)
	require.ErrorIs(t, err, ErrInvitationForbidden)
	_, err = f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.issuer.Email, domain.RoleAgent, nil, digest, &f.issuer.ID)
	require.ErrorIs(t, err, ErrInvitationMemberExists)
	zero := uint(0)
	_, err = f.repo.CreateAgentInvitation(f.account.ID, f.issuer.ID, f.invitee.Email, domain.RoleAgent, &zero, digest, nil)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	var count int64
	require.NoError(t, first.Model(&domain.AgentInvitation{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestInvitationTenantScopeForListAndRevoke(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "scope")
	invite, raw := f.issue(t, "scope", domain.RoleAgent, nil)
	foreign := newInvitationFixture(t, first, "foreign")
	require.ErrorIs(t, foreign.repo.RevokeAgentInvitation(foreign.account.ID, foreign.issuer.ID, invite.ID), ErrInvitationInvalid)
	_, err := foreign.repo.ListAgentInvitations(f.account.ID, foreign.issuer.ID)
	require.ErrorIs(t, err, ErrInvitationForbidden)
	managerRole := domain.CustomRole{AccountID: f.account.ID, Name: "Delegated manager", Permissions: `["agent_manage"]`}
	require.NoError(t, first.Create(&managerRole).Error)
	manager := domain.AccountUser{AccountID: f.account.ID, UserID: foreign.issuer.ID, Role: domain.RoleAgent, CustomRoleID: &managerRole.ID}
	require.NoError(t, first.Create(&manager).Error)
	list, err := f.repo.ListAgentInvitations(f.account.ID, foreign.issuer.ID)
	require.NoError(t, err)
	require.Empty(t, list)
	require.ErrorIs(t, f.repo.RevokeAgentInvitation(f.account.ID, foreign.issuer.ID, invite.ID), ErrInvitationInvalid)
	_, err = f.repo.InspectAgentInvitation(raw)
	require.NoError(t, err)
	list, err = f.repo.ListAgentInvitations(f.account.ID, f.issuer.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	_, digest := invitationTestProof("manager own")
	own, err := f.repo.CreateAgentInvitation(f.account.ID, foreign.issuer.ID, fmt.Sprintf("unregistered-%d@example.test", manager.ID), domain.RoleAgent, nil, digest, nil)
	require.NoError(t, err)
	list, err = f.repo.ListAgentInvitations(f.account.ID, foreign.issuer.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, own.ID, list[0].ID)
	require.NoError(t, f.repo.RevokeAgentInvitation(f.account.ID, foreign.issuer.ID, own.ID))
}

func TestInvitationDelegatedIssuerLosesPermissionBeforeAcceptance(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "delegated-recheck")
	role := domain.CustomRole{AccountID: f.account.ID, Name: "Invitation manager", Permissions: `["agent_manage"]`}
	require.NoError(t, first.Create(&role).Error)
	require.NoError(t, first.Model(&domain.AccountUser{}).Where("id = ?", f.issuerMember.ID).Updates(map[string]any{"role": domain.RoleAgent, "custom_role_id": role.ID}).Error)
	invite, raw := f.issue(t, "delegated-recheck", domain.RoleAgent, nil)
	require.NoError(t, first.Model(&role).Update("permissions", `["conversation_manage"]`).Error)
	_, err := f.repo.InspectAgentInvitation(raw)
	require.ErrorIs(t, err, ErrInvitationForbidden)
	_, err = f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationForbidden)
	requireInvitationUnconsumed(t, first, invite.ID)
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 0)
}

func TestInvitationReissuedConsumedProofClearsOldIdentityBindings(t *testing.T) {
	first, _ := invitationTestDBs(t)
	f := newInvitationFixture(t, first, "consumed-reissue")
	original, raw := f.issue(t, "first-consumed", domain.RoleAgent, nil)
	member, err := f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.NoError(t, err)
	require.NoError(t, first.Delete(&domain.AccountUser{}, member.ID).Error)
	reissued, next := f.issue(t, "reissued-after-removal", domain.RoleAgent, nil)
	require.Equal(t, original.ID, reissued.ID)
	requireInvitationUnconsumed(t, first, reissued.ID)
	_, err = f.repo.AcceptAgentInvitation(raw, f.invitee.ID, f.invitee.Email)
	require.ErrorIs(t, err, ErrInvitationInvalid)
	nextMember, err := f.repo.AcceptAgentInvitation(next, f.invitee.ID, f.invitee.Email)
	require.NoError(t, err)
	require.NotEqual(t, member.ID, nextMember.ID, "only an explicitly reissued proof can create a new membership")
	requireInvitationMemberCount(t, first, f.account.ID, f.invitee.ID, 1)
}
