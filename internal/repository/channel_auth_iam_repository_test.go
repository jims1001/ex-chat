package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const testMFASecret = "JBSWY3DPEHPK3PXP"

func openMFATestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	// Each call uses a separate database/sql pool, as separate server instances
	// would. Busy timeout permits waiting for the other instance's write lock.
	db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func mfaTestRepositories(t *testing.T) (*channelAuthIAMRepository, *channelAuthIAMRepository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mfa.sqlite")
	first := openMFATestDB(t, path)
	require.NoError(t, first.AutoMigrate(&domain.MFAProfile{}))
	second := openMFATestDB(t, path)
	return &channelAuthIAMRepository{db: first}, &channelAuthIAMRepository{db: second}, path
}

func createMFATestProfile(t *testing.T, r *channelAuthIAMRepository, userID uint, codes ...string) *domain.MFAProfile {
	t.Helper()
	encoded, err := json.Marshal(codes)
	require.NoError(t, err)
	profile := &domain.MFAProfile{UserID: userID, Secret: testMFASecret, Enabled: true, BackupCodes: string(encoded)}
	require.NoError(t, r.SaveMFAProfile(profile))
	return profile
}

func runConcurrentMFAVerifications(t *testing.T, first, second *channelAuthIAMRepository, attempts int, verify func(*channelAuthIAMRepository, int) error) []error {
	t.Helper()
	start := make(chan struct{})
	results := make([]error, attempts)
	var workers sync.WaitGroup
	for i := range results {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			r := first
			if index%2 == 1 {
				r = second
			}
			<-start
			results[index] = verify(r, index)
		}(i)
	}
	close(start)
	workers.Wait()
	return results
}

func requireSingleMFASuccess(t *testing.T, results []error) {
	t.Helper()
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		} else {
			require.True(t, errors.Is(err, ErrMFAReplay) || errors.Is(err, ErrMFAInvalidCode) || errors.Is(err, ErrMFARateLimited), "unexpected infrastructure failure: %v", err)
		}
	}
	require.Equal(t, 1, successes)
}

func TestMFABackupCodeConsumedOnceAcrossDatabaseHandles(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1, "12345678", "12345678", "87654321")
	results := runConcurrentMFAVerifications(t, first, second, 12, func(r *channelAuthIAMRepository, _ int) error {
		return r.VerifyMFALogin(1, "12345678", "", time.Time{})
	})
	requireSingleMFASuccess(t, results)
	profile, err := second.GetMFAProfile(1)
	require.NoError(t, err)
	require.JSONEq(t, `["87654321"]`, profile.BackupCodes)
	require.EqualValues(t, 12, profile.VerificationAttempts, "failed and rate-limited attempts must commit")
	require.EqualValues(t, 12, profile.AuthVersion)
}

func TestMFATOTPConsumedOnceAcrossDatabaseHandles(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1)
	code, err := auth.GenerateTOTPCode(testMFASecret, time.Now())
	require.NoError(t, err)
	results := runConcurrentMFAVerifications(t, first, second, 12, func(r *channelAuthIAMRepository, index int) error {
		return r.VerifyMFALogin(1, code, fmt.Sprintf("challenge-%d", index), time.Now().Add(4*time.Minute))
	})
	requireSingleMFASuccess(t, results)
	profile, err := second.GetMFAProfile(1)
	require.NoError(t, err)
	require.Positive(t, profile.LastTOTPStep)
	require.EqualValues(t, 12, profile.VerificationAttempts)
}

func TestMFAChallengeConsumedOnceWithDifferentFactors(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	codes := []string{"12345670", "12345671", "12345672", "12345673", "12345674", "12345675", "12345676", "12345677"}
	createMFATestProfile(t, first, 1, codes...)
	expires := time.Now().Add(4 * time.Minute)
	results := runConcurrentMFAVerifications(t, first, second, len(codes), func(r *channelAuthIAMRepository, index int) error {
		return r.VerifyMFALogin(1, codes[index], "one-challenge", expires)
	})
	requireSingleMFASuccess(t, results)
	profile, err := second.GetMFAProfile(1)
	require.NoError(t, err)
	var remaining []string
	require.NoError(t, json.Unmarshal([]byte(profile.BackupCodes), &remaining))
	require.Len(t, remaining, len(codes)-1, "replayed challenges must not consume other valid factors")
	var used map[string]time.Time
	require.NoError(t, json.Unmarshal([]byte(profile.UsedChallenges), &used))
	require.Len(t, used, 1)
	require.WithinDuration(t, expires, used["one-challenge"], time.Millisecond)
}

func TestMFARateLimitPersistsAcrossHandlesAndReopen(t *testing.T) {
	first, second, path := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1, "12345678")
	for i := 0; i < mfaVerificationLimit; i++ {
		r := first
		if i%2 == 1 {
			r = second
		}
		require.ErrorIs(t, r.VerifyMFALogin(1, "bad-code", fmt.Sprintf("new-challenge-%d", i), time.Now().Add(4*time.Minute)), ErrMFAInvalidCode)
	}
	// A fresh repository and connection pool must inherit the exhausted budget.
	reopened := &channelAuthIAMRepository{db: openMFATestDB(t, path)}
	require.ErrorIs(t, reopened.VerifyMFALogin(1, "12345678", "brand-new-challenge", time.Now().Add(4*time.Minute)), ErrMFARateLimited)
	profile, err := reopened.GetMFAProfile(1)
	require.NoError(t, err)
	require.EqualValues(t, 6, profile.VerificationAttempts)
	require.JSONEq(t, `["12345678"]`, profile.BackupCodes)

	// Advance the persisted window rather than waiting five minutes in a test.
	require.NoError(t, first.db.Model(&domain.MFAProfile{}).Where("user_id = ?", 1).
		Update("verification_window_started_at", time.Now().Add(-mfaVerificationWindow-time.Second)).Error)
	require.NoError(t, second.VerifyMFALogin(1, "12345678", "after-window", time.Now().Add(4*time.Minute)))
	profile, err = reopened.GetMFAProfile(1)
	require.NoError(t, err)
	require.EqualValues(t, 1, profile.VerificationAttempts)
}

func TestMFASuccessesCountTowardSharedRateLimit(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1, "12345670", "12345671", "12345672", "12345673", "12345674", "12345675")
	for i := 0; i < mfaVerificationLimit; i++ {
		require.NoError(t, first.VerifyMFALogin(1, fmt.Sprintf("1234567%d", i), fmt.Sprintf("challenge-%d", i), time.Now().Add(4*time.Minute)))
	}
	require.ErrorIs(t, second.VerifyMFALogin(1, "12345675", "new-challenge", time.Now().Add(4*time.Minute)), ErrMFARateLimited)
	// Limits are per user, not a global application outage.
	createMFATestProfile(t, first, 2, "12345675")
	require.NoError(t, second.VerifyMFALogin(2, "12345675", "new-challenge", time.Now().Add(4*time.Minute)))
}

func TestMFAReplayStateSurvivesConfigurationSaveAndBackupRegeneration(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	stale := createMFATestProfile(t, first, 1, "12345678", "87654321")
	require.NoError(t, second.VerifyMFALogin(1, "12345678", "used-challenge", time.Now().Add(4*time.Minute)))
	stale.Secret = "KRSXG5DSNFXGOIDB"
	stale.Enabled = false
	require.NoError(t, first.SaveMFAProfile(stale))
	profile, err := second.GetMFAProfile(1)
	require.NoError(t, err)
	require.False(t, profile.Enabled)
	require.Equal(t, stale.Secret, profile.Secret)
	require.JSONEq(t, `["87654321"]`, profile.BackupCodes, "configuration save must not resurrect consumed codes")
	require.EqualValues(t, 1, profile.VerificationAttempts)
	require.Contains(t, profile.UsedChallenges, "used-challenge")

	profile.Enabled = true
	require.NoError(t, second.SaveMFAProfile(profile))
	newCodes, err := first.GenerateMFABackupCodes(1)
	require.NoError(t, err)
	require.Len(t, newCodes, 8)
	require.ErrorIs(t, second.VerifyMFALogin(1, newCodes[0], "used-challenge", time.Now().Add(4*time.Minute)), ErrMFAReplay)
	profile, err = second.GetMFAProfile(1)
	require.NoError(t, err)
	require.EqualValues(t, 2, profile.VerificationAttempts)
	encoded, err := json.Marshal(newCodes)
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), profile.BackupCodes)
}

func TestMFAConfigurationSaveCannotRaceBackupConsumption(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	stale := createMFATestProfile(t, first, 1, "12345678")
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; results <- first.SaveMFAProfile(stale) }()
	go func() { <-start; results <- second.VerifyMFALogin(1, "12345678", "", time.Time{}) }()
	close(start)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	profile, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	require.JSONEq(t, `[]`, profile.BackupCodes)
	require.EqualValues(t, 1, profile.VerificationAttempts)
}

func TestMFATOTPAcceptedStepsAreMonotonic(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1)
	now := time.Now()
	future, err := auth.GenerateTOTPCode(testMFASecret, now.Add(30*time.Second))
	require.NoError(t, err)
	current, err := auth.GenerateTOTPCode(testMFASecret, now)
	require.NoError(t, err)
	require.NoError(t, first.VerifyMFALogin(1, future, "", time.Time{}))
	require.ErrorIs(t, second.VerifyMFALogin(1, current, "", time.Time{}), ErrMFAReplay)
	require.ErrorIs(t, second.VerifyMFALogin(1, future, "", time.Time{}), ErrMFAReplay)
}

func TestMFARejectsInvalidChallengeMetadataAndDisabledProfiles(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	cases := []struct {
		name    string
		id      string
		expires time.Time
	}{
		{"expired", "challenge", time.Now().Add(-time.Second)},
		{"no expiry", "challenge", time.Time{}},
		{"too long lived", "challenge", time.Now().Add(6 * time.Minute)},
		{"expiry without ID", "", time.Now().Add(time.Minute)},
		{"whitespace ID", " ", time.Now().Add(time.Minute)},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID := uint(i + 1)
			createMFATestProfile(t, first, userID, "12345678")
			require.ErrorIs(t, second.VerifyMFALogin(userID, "12345678", tc.id, tc.expires), ErrMFAInvalidCode)
			profile, err := first.GetMFAProfile(userID)
			require.NoError(t, err)
			require.EqualValues(t, 1, profile.VerificationAttempts)
			require.JSONEq(t, `["12345678"]`, profile.BackupCodes)
		})
	}
	profile := createMFATestProfile(t, first, 100, "12345678")
	profile.Enabled = false
	require.NoError(t, first.SaveMFAProfile(profile))
	require.ErrorIs(t, second.VerifyMFALogin(100, "12345678", "", time.Time{}), ErrMFANotEnabled)
	require.ErrorIs(t, second.VerifyMFALogin(101, "12345678", "", time.Time{}), ErrMFANotEnabled)
}

func TestMFAExpiredChallengesArePrunedWithoutLosingLiveReplayState(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1, "12345678", "87654321")
	used, err := json.Marshal(map[string]time.Time{"expired": time.Now().Add(-time.Minute), "live": time.Now().Add(time.Minute)})
	require.NoError(t, err)
	require.NoError(t, first.db.Model(&domain.MFAProfile{}).Where("user_id = ?", 1).Update("used_challenges", string(used)).Error)
	require.NoError(t, second.VerifyMFALogin(1, "12345678", "new", time.Now().Add(time.Minute)))
	profile, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	var retained map[string]time.Time
	require.NoError(t, json.Unmarshal([]byte(profile.UsedChallenges), &retained))
	require.NotContains(t, retained, "expired")
	require.Contains(t, retained, "live")
	require.Contains(t, retained, "new")
	require.ErrorIs(t, second.VerifyMFALogin(1, "87654321", "live", time.Now().Add(time.Minute)), ErrMFAReplay)
}

func TestMFAStorageFailureRollsBackFactorConsumption(t *testing.T) {
	first, second, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1, "12345678")
	require.NoError(t, first.db.Exec(`CREATE TRIGGER reject_mfa_state BEFORE UPDATE OF verification_attempts ON mfa_profiles BEGIN SELECT RAISE(ABORT, 'test storage failure'); END`).Error)
	err := second.VerifyMFALogin(1, "12345678", "challenge", time.Now().Add(time.Minute))
	require.ErrorContains(t, err, "test storage failure")
	profile, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	require.Zero(t, profile.AuthVersion)
	require.Zero(t, profile.VerificationAttempts)
	require.Empty(t, profile.UsedChallenges)
	require.JSONEq(t, `["12345678"]`, profile.BackupCodes)
	require.NoError(t, first.db.Exec("DROP TRIGGER reject_mfa_state").Error)
	require.NoError(t, second.VerifyMFALogin(1, "12345678", "challenge", time.Now().Add(time.Minute)))
}

func TestMFAAuthenticationStateIsNotSerialized(t *testing.T) {
	first, _, _ := mfaTestRepositories(t)
	createMFATestProfile(t, first, 1, "12345678")
	require.NoError(t, first.VerifyMFALogin(1, "12345678", "private-challenge", time.Now().Add(time.Minute)))
	profile, err := first.GetMFAProfile(1)
	require.NoError(t, err)
	encoded, err := json.Marshal(profile)
	require.NoError(t, err)
	for _, value := range []string{"private-challenge", "VerificationAttempts", "VerificationWindowStartedAt", "LastTOTPStep", "AuthVersion", "UsedChallenges"} {
		require.NotContains(t, string(encoded), value)
	}
}

func TestMFAAutoMigrationInitializesExistingProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-mfa.sqlite")
	db := openMFATestDB(t, path)
	require.NoError(t, db.Exec(`CREATE TABLE mfa_profiles (id integer PRIMARY KEY AUTOINCREMENT, user_id integer NOT NULL UNIQUE, secret text NOT NULL, enabled numeric DEFAULT false, backup_codes text, created_at datetime, updated_at datetime)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO mfa_profiles (user_id, secret, enabled, backup_codes) VALUES (?, ?, ?, ?)`, 1, testMFASecret, true, `["12345678"]`).Error)
	require.NoError(t, db.AutoMigrate(&domain.MFAProfile{}))
	r := &channelAuthIAMRepository{db: db}
	profile, err := r.GetMFAProfile(1)
	require.NoError(t, err)
	require.Zero(t, profile.AuthVersion)
	require.Zero(t, profile.LastTOTPStep)
	require.Zero(t, profile.VerificationAttempts)
	require.Nil(t, profile.VerificationWindowStartedAt)
	require.NoError(t, r.VerifyMFALogin(1, "12345678", "", time.Time{}))
}
