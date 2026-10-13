package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testMFAPassword = "current-MFA-password"

func mfaManagementRepositories(t *testing.T) (*channelAuthIAMRepository, *channelAuthIAMRepository, string) {
	t.Helper()
	first, second, path := mfaTestRepositories(t)
	require.NoError(t, first.db.AutoMigrate(&domain.User{}))
	hash, err := auth.HashPassword(testMFAPassword)
	require.NoError(t, err)
	require.NoError(t, first.db.Create(&domain.User{ID: 1, Email: "mfa@example.test", PasswordHash: hash}).Error)
	return first, second, path
}

func requireMFAUnchanged(t *testing.T, before, after *domain.MFAProfile) {
	t.Helper()
	require.Equal(t, before.Enabled, after.Enabled)
	require.Equal(t, before.Secret, after.Secret)
	require.Equal(t, before.BackupCodes, after.BackupCodes)
	require.Equal(t, before.LastTOTPStep, after.LastTOTPStep)
	require.Equal(t, before.UsedChallenges, after.UsedChallenges)
}

func TestMFAManagementSetupEnableRegenerateDisable(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	pending, codes, err := first.ManageMFA(1, testMFAPassword, "setup", "")
	require.NoError(t, err)
	require.NotEmpty(t, pending.Secret)
	require.False(t, pending.Enabled)
	require.Empty(t, pending.BackupCodes)
	require.Empty(t, codes)
	code, err := auth.GenerateTOTPCode(pending.Secret, time.Now())
	require.NoError(t, err)
	active, initialCodes, err := second.ManageMFA(1, testMFAPassword, "enable", code)
	require.NoError(t, err)
	require.True(t, active.Enabled)
	require.Empty(t, active.Secret)
	require.Empty(t, active.BackupCodes)
	require.Len(t, initialCodes, 8)
	stored, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	require.Equal(t, pending.Secret, stored.Secret)
	require.Positive(t, stored.LastTOTPStep)
	encoded, err := json.Marshal(initialCodes)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), stored.BackupCodes)

	active, replacementCodes, err := first.ManageMFA(1, testMFAPassword, "backup_codes", initialCodes[0])
	require.NoError(t, err)
	require.Len(t, replacementCodes, 8)
	require.Empty(t, active.Secret)
	require.Empty(t, active.BackupCodes)
	require.NotEqual(t, initialCodes, replacementCodes)
	stored, err = second.GetMFAProfile(1)
	require.NoError(t, err)
	encoded, err = json.Marshal(replacementCodes)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), stored.BackupCodes)

	disabled, codes, err := second.ManageMFA(1, testMFAPassword, "disable", replacementCodes[0])
	require.NoError(t, err)
	require.False(t, disabled.Enabled)
	require.Empty(t, disabled.Secret)
	require.Empty(t, disabled.BackupCodes)
	require.Empty(t, codes)
	stored, err = first.GetMFAProfile(1)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Empty(t, stored.Secret)
	require.Empty(t, stored.BackupCodes)
	require.EqualValues(t, 4, stored.VerificationAttempts)
	require.EqualValues(t, 4, stored.AuthVersion)
	require.ErrorIs(t, first.VerifyMFALogin(1, replacementCodes[1], "", time.Time{}), ErrMFANotEnabled)
}

func TestMFAManagementActiveOperationsRequireReauthentication(t *testing.T) {
	cases := []struct {
		name, action, password, code string
		want                         error
	}{
		{"disable without credentials", "disable", "", "", ErrMFAInvalidPassword},
		{"disable wrong password", "disable", "wrong", "12345678", ErrMFAInvalidPassword},
		{"disable without factor", "disable", testMFAPassword, "", ErrMFAInvalidCode},
		{"regenerate wrong password", "backup_codes", "wrong", "12345678", ErrMFAInvalidPassword},
		{"regenerate without factor", "backup_codes", testMFAPassword, "", ErrMFAInvalidCode},
		{"setup cannot overwrite active", "setup", testMFAPassword, "12345678", ErrMFAAlreadyEnabled},
		{"enable cannot expose active codes", "enable", testMFAPassword, "12345678", ErrMFAAlreadyEnabled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, second, _ := mfaManagementRepositories(t)
			before := createMFATestProfile(t, first, 1, "12345678", "87654321")
			profile, codes, err := second.ManageMFA(1, tc.password, tc.action, tc.code)
			require.ErrorIs(t, err, tc.want)
			require.Nil(t, profile)
			require.Nil(t, codes)
			after, err := first.GetMFAProfile(1)
			require.NoError(t, err)
			requireMFAUnchanged(t, before, after)
			require.EqualValues(t, 1, after.VerificationAttempts)
		})
	}
}

func TestMFAManagementEnableRequiresPasswordAndPendingTOTP(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	_, _, err := first.ManageMFA(1, testMFAPassword, "enable", "12345678")
	require.ErrorIs(t, err, ErrMFASetupRequired)
	pending, _, err := first.ManageMFA(1, testMFAPassword, "setup", "")
	require.NoError(t, err)
	// Even a legacy pending profile containing backup codes must prove TOTP.
	require.NoError(t, first.db.Model(&domain.MFAProfile{}).Where("user_id = ?", 1).Update("backup_codes", `["12345678"]`).Error)
	_, _, err = second.ManageMFA(1, testMFAPassword, "enable", "12345678")
	require.ErrorIs(t, err, ErrMFAInvalidCode)
	code, err := auth.GenerateTOTPCode(pending.Secret, time.Now())
	require.NoError(t, err)
	_, _, err = second.ManageMFA(1, "wrong", "enable", code)
	require.ErrorIs(t, err, ErrMFAInvalidPassword)
	stored, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	require.False(t, stored.Enabled)
	require.Zero(t, stored.LastTOTPStep)
	_, codes, err := second.ManageMFA(1, testMFAPassword, "enable", code)
	require.NoError(t, err)
	require.Len(t, codes, 8)
}

func TestMFAManagementSetupAndLoginShareReplayState(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	pending, _, err := first.ManageMFA(1, testMFAPassword, "setup", "")
	require.NoError(t, err)
	code, err := auth.GenerateTOTPCode(pending.Secret, time.Now())
	require.NoError(t, err)
	_, _, err = first.ManageMFA(1, testMFAPassword, "enable", code)
	require.NoError(t, err)
	require.ErrorIs(t, second.VerifyMFALogin(1, code, "", time.Time{}), ErrMFAReplay)
	_, _, err = second.ManageMFA(1, testMFAPassword, "backup_codes", code)
	require.ErrorIs(t, err, ErrMFAReplay)
	_, _, err = second.ManageMFA(1, testMFAPassword, "disable", code)
	require.ErrorIs(t, err, ErrMFAReplay)
	stored, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	require.True(t, stored.Enabled)
	require.EqualValues(t, 5, stored.VerificationAttempts)
}

func TestMFAManagementLoginFactorCannotAuthorizeSettingsAgain(t *testing.T) {
	for _, factor := range []string{"totp", "backup"} {
		t.Run(factor, func(t *testing.T) {
			first, second, _ := mfaManagementRepositories(t)
			createMFATestProfile(t, first, 1, "12345678", "87654321")
			code := "12345678"
			want := ErrMFAInvalidCode
			if factor == "totp" {
				var err error
				code, err = auth.GenerateTOTPCode(testMFASecret, time.Now())
				require.NoError(t, err)
				want = ErrMFAReplay
			}
			require.NoError(t, first.VerifyMFALogin(1, code, "login", time.Now().Add(time.Minute)))
			before, err := first.GetMFAProfile(1)
			require.NoError(t, err)
			for _, action := range []string{"backup_codes", "disable"} {
				profile, codes, err := second.ManageMFA(1, testMFAPassword, action, code)
				require.ErrorIs(t, err, want)
				require.Nil(t, profile)
				require.Nil(t, codes)
			}
			after, err := first.GetMFAProfile(1)
			require.NoError(t, err)
			requireMFAUnchanged(t, before, after)
		})
	}
}

func TestMFAManagementPasswordlessAccountHasNoBypass(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	require.NoError(t, first.db.Model(&domain.User{}).Where("id = ?", 1).Update("password_hash", "").Error)
	for _, action := range []string{"setup", "enable", "backup_codes", "disable"} {
		profile, codes, err := second.ManageMFA(1, "", action, "12345678")
		require.ErrorIs(t, err, ErrMFAReauthUnsupported)
		require.Nil(t, profile)
		require.Nil(t, codes)
	}
	stored, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.Empty(t, stored.Secret)
	require.False(t, stored.Enabled)
	require.EqualValues(t, 4, stored.VerificationAttempts)
}

func TestMFAManagementFirstSetupFailuresPersistAndThrottleAcrossInstances(t *testing.T) {
	first, second, path := mfaManagementRepositories(t)
	results := runConcurrentMFAVerifications(t, first, second, 12, func(r *channelAuthIAMRepository, _ int) error {
		profile, codes, err := r.ManageMFA(1, "wrong", "setup", "")
		if profile != nil || codes != nil {
			return fmt.Errorf("credentials disclosed on failure")
		}
		return err
	})
	invalid := 0
	for _, err := range results {
		require.True(t, errors.Is(err, ErrMFAInvalidPassword) || errors.Is(err, ErrMFARateLimited), "%v", err)
		if errors.Is(err, ErrMFAInvalidPassword) {
			invalid++
		}
	}
	require.Equal(t, mfaVerificationLimit, invalid)
	var count int64
	require.NoError(t, first.db.Model(&domain.MFAProfile{}).Where("user_id = ?", 1).Count(&count).Error)
	require.EqualValues(t, 1, count)
	reopened := &channelAuthIAMRepository{db: openMFATestDB(t, path)}
	profile, codes, err := reopened.ManageMFA(1, testMFAPassword, "setup", "")
	require.ErrorIs(t, err, ErrMFARateLimited)
	require.Nil(t, profile)
	require.Nil(t, codes)
	stored, err := reopened.GetMFAProfile(1)
	require.NoError(t, err)
	require.Empty(t, stored.Secret)
	require.EqualValues(t, 13, stored.VerificationAttempts)
	require.NoError(t, first.db.Model(&domain.MFAProfile{}).Where("user_id = ?", 1).Update("verification_window_started_at", time.Now().Add(-mfaVerificationWindow-time.Second)).Error)
	profile, _, err = reopened.ManageMFA(1, testMFAPassword, "setup", "")
	require.NoError(t, err)
	require.NotEmpty(t, profile.Secret)
	require.EqualValues(t, 1, profile.VerificationAttempts)
}

func TestMFAManagementAndLoginCannotResetEachOthersBudget(t *testing.T) {
	for _, exhaustWithSettings := range []bool{false, true} {
		t.Run(fmt.Sprintf("settings=%t", exhaustWithSettings), func(t *testing.T) {
			first, second, _ := mfaManagementRepositories(t)
			createMFATestProfile(t, first, 1, "12345678")
			for i := 0; i < mfaVerificationLimit; i++ {
				if exhaustWithSettings {
					_, _, err := first.ManageMFA(1, "wrong", "disable", "12345678")
					require.ErrorIs(t, err, ErrMFAInvalidPassword)
				} else {
					require.ErrorIs(t, first.VerifyMFALogin(1, "wrong", "", time.Time{}), ErrMFAInvalidCode)
				}
			}
			_, _, err := second.ManageMFA(1, testMFAPassword, "disable", "12345678")
			require.ErrorIs(t, err, ErrMFARateLimited)
			require.ErrorIs(t, second.VerifyMFALogin(1, "12345678", "", time.Time{}), ErrMFARateLimited)
			stored, err := first.GetMFAProfile(1)
			require.NoError(t, err)
			require.True(t, stored.Enabled)
			require.JSONEq(t, `["12345678"]`, stored.BackupCodes)
			require.EqualValues(t, 7, stored.VerificationAttempts)
		})
	}
}

func TestMFAManagementConcurrentEnableSucceedsOnce(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	pending, _, err := first.ManageMFA(1, testMFAPassword, "setup", "")
	require.NoError(t, err)
	code, err := auth.GenerateTOTPCode(pending.Secret, time.Now())
	require.NoError(t, err)
	var issued [][]string
	var mu sync.Mutex
	results := runConcurrentMFAVerifications(t, first, second, 2, func(r *channelAuthIAMRepository, _ int) error {
		profile, codes, err := r.ManageMFA(1, testMFAPassword, "enable", code)
		if err == nil {
			mu.Lock()
			issued = append(issued, codes)
			mu.Unlock()
		} else if profile != nil || codes != nil {
			return fmt.Errorf("credentials disclosed on failure")
		}
		return err
	})
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrMFAAlreadyEnabled)
		}
	}
	require.Equal(t, 1, successes)
	require.Len(t, issued, 1)
	stored, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	encoded, err := json.Marshal(issued[0])
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), stored.BackupCodes)
	require.ErrorIs(t, second.VerifyMFALogin(1, code, "", time.Time{}), ErrMFAReplay)
}

func TestMFAManagementConcurrentMutationsConsumeFactorOnce(t *testing.T) {
	for _, factor := range []string{"totp", "backup"} {
		for _, actions := range [][2]string{{"disable", "disable"}, {"backup_codes", "backup_codes"}, {"disable", "backup_codes"}, {"backup_codes", "login"}} {
			t.Run(factor+"/"+actions[0]+"/"+actions[1], func(t *testing.T) {
				first, second, _ := mfaManagementRepositories(t)
				createMFATestProfile(t, first, 1, "12345678", "87654321")
				code := "12345678"
				if factor == "totp" {
					var err error
					code, err = auth.GenerateTOTPCode(testMFASecret, time.Now())
					require.NoError(t, err)
				}
				results := runConcurrentMFAVerifications(t, first, second, 2, func(r *channelAuthIAMRepository, index int) error {
					if actions[index] == "login" {
						return r.VerifyMFALogin(1, code, "", time.Time{})
					}
					profile, codes, err := r.ManageMFA(1, testMFAPassword, actions[index], code)
					if err != nil && (profile != nil || codes != nil) {
						return fmt.Errorf("credentials disclosed on failure")
					}
					return err
				})
				successes := 0
				for _, err := range results {
					if err == nil {
						successes++
					} else {
						require.True(t, errors.Is(err, ErrMFANotEnabled) || errors.Is(err, ErrMFAReplay) || errors.Is(err, ErrMFAInvalidCode), "%v", err)
					}
				}
				require.Equal(t, 1, successes)
				stored, err := first.GetMFAProfile(1)
				require.NoError(t, err)
				require.EqualValues(t, 2, stored.VerificationAttempts)
			})
		}
	}
}

func TestMFAManagementStorageFailuresRollBackMutationAndFactor(t *testing.T) {
	for _, action := range []string{"enable", "backup_codes", "disable"} {
		t.Run(action, func(t *testing.T) {
			first, second, _ := mfaManagementRepositories(t)
			before := createMFATestProfile(t, first, 1, "12345678", "87654321")
			code := "12345678"
			if action == "enable" {
				before.Enabled = false
				require.NoError(t, first.SaveMFAProfile(before))
				var err error
				code, err = auth.GenerateTOTPCode(testMFASecret, time.Now())
				require.NoError(t, err)
			}
			require.NoError(t, first.db.Exec(`CREATE TRIGGER reject_mfa_management BEFORE UPDATE OF secret ON mfa_profiles BEGIN SELECT RAISE(ABORT, 'test management storage failure'); END`).Error)
			profile, codes, err := second.ManageMFA(1, testMFAPassword, action, code)
			require.ErrorContains(t, err, "test management storage failure")
			require.Nil(t, profile)
			require.Nil(t, codes)
			after, err := first.GetMFAProfile(1)
			require.NoError(t, err)
			requireMFAUnchanged(t, before, after)
			require.Zero(t, after.AuthVersion)
			require.Zero(t, after.VerificationAttempts)
			require.NoError(t, first.db.Exec("DROP TRIGGER reject_mfa_management").Error)
			_, _, err = second.ManageMFA(1, testMFAPassword, action, code)
			require.NoError(t, err)
		})
	}
}

func TestMFAManagementFirstSetupStorageFailureDoesNotLeakSecret(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	require.NoError(t, first.db.Exec(`CREATE TRIGGER reject_mfa_management BEFORE UPDATE OF secret ON mfa_profiles BEGIN SELECT RAISE(ABORT, 'test setup storage failure'); END`).Error)
	profile, codes, err := second.ManageMFA(1, testMFAPassword, "setup", "")
	require.ErrorContains(t, err, "test setup storage failure")
	require.Nil(t, profile)
	require.Nil(t, codes)
	profile, err = first.GetMFAProfile(1)
	require.NoError(t, err)
	require.Nil(t, profile)
}

func TestMFAManagementCorruptFactorsAndMissingUsersFailClosed(t *testing.T) {
	first, second, _ := mfaManagementRepositories(t)
	before := createMFATestProfile(t, first, 1, "12345678")
	require.NoError(t, first.db.Model(&domain.MFAProfile{}).Where("user_id = ?", 1).Update("backup_codes", "corrupt").Error)
	before.BackupCodes = "corrupt"
	profile, codes, err := second.ManageMFA(1, testMFAPassword, "disable", "12345678")
	require.Error(t, err)
	require.False(t, isMFAAuthenticationError(err))
	require.Nil(t, profile)
	require.Nil(t, codes)
	after, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	requireMFAUnchanged(t, before, after)
	require.Zero(t, after.VerificationAttempts)
	profile, codes, err = second.ManageMFA(2, testMFAPassword, "setup", "")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.Nil(t, profile)
	require.Nil(t, codes)
	profile, err = first.GetMFAProfile(2)
	require.NoError(t, err)
	require.Nil(t, profile)
}
