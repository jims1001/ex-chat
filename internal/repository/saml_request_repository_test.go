package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/auth/samltest"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func samlTestRepositories(t *testing.T) (*SAMLRequestRepository, *SAMLRequestRepository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "saml.sqlite")
	first := openMFATestDB(t, path)
	require.NoError(t, first.AutoMigrate(&domain.SAMLAuthnRequest{}, &domain.SAMLReplay{}))
	return NewSAMLRequestRepository(first), NewSAMLRequestRepository(openMFATestDB(t, path)), path
}

func samlTestConfig() SAMLConfigurationBinding {
	return SAMLConfigurationBinding{AccountID: 7, ConfigVersion: 3, ConfigDigest: strings.Repeat("a", 64)}
}

func samlPrepare(t *testing.T, repo *SAMLRequestRepository, requestID string, config SAMLConfigurationBinding) *SAMLPreparedRequest {
	t.Helper()
	request, err := repo.Prepare(context.Background(), requestID, config, SAMLRequestMaxTTL)
	require.NoError(t, err)
	return request
}

func samlConcurrent(t *testing.T, first, second *SAMLRequestRepository, attempts int, action func(*SAMLRequestRepository, int) error) []error {
	t.Helper()
	start := make(chan struct{})
	results := make([]error, attempts)
	var workers sync.WaitGroup
	for i := range results {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			repo := first
			if index%2 == 1 {
				repo = second
			}
			<-start
			results[index] = action(repo, index)
		}(i)
	}
	close(start)
	workers.Wait()
	return results
}

func requireSAMLRowCounts(t *testing.T, repo *SAMLRequestRepository, pending, consumed, replay int64) {
	t.Helper()
	var count int64
	require.NoError(t, repo.db.Model(&domain.SAMLAuthnRequest{}).Where("consumed_at IS NULL").Count(&count).Error)
	require.Equal(t, pending, count)
	require.NoError(t, repo.db.Model(&domain.SAMLAuthnRequest{}).Where("consumed_at IS NOT NULL").Count(&count).Error)
	require.Equal(t, consumed, count)
	require.NoError(t, repo.db.Model(&domain.SAMLReplay{}).Count(&count).Error)
	require.Equal(t, replay, count)
}

func TestSAMLRequestPrepareLookupPersistsOnlyDigests(t *testing.T) {
	first, second, path := samlTestRepositories(t)
	config := samlTestConfig()
	request := samlPrepare(t, first, "_request1", config)
	require.Len(t, request.RelayState, 43)
	require.Len(t, request.BrowserSecret, 43)
	require.NotEqual(t, request.RelayState, request.BrowserSecret)
	for _, secret := range []string{request.RelayState, request.BrowserSecret} {
		decoded, err := base64.RawURLEncoding.DecodeString(secret)
		require.NoError(t, err)
		require.Len(t, decoded, 32)
	}
	var row domain.SAMLAuthnRequest
	require.NoError(t, first.db.First(&row).Error)
	relayDigest, err := samlSecretDigest(request.RelayState)
	require.NoError(t, err)
	browserDigest, err := samlSecretDigest(request.BrowserSecret)
	require.NoError(t, err)
	require.Equal(t, relayDigest, row.RelayDigest)
	require.Equal(t, browserDigest, row.BrowserSecretDigest)
	require.NotContains(t, fmt.Sprintf("%+v", row), request.RelayState)
	require.NotContains(t, fmt.Sprintf("%+v", row), request.BrowserSecret)
	encoded, err := json.Marshal(row)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded))
	require.Equal(t, SAMLRequestMaxTTL, row.ExpiresAt.Sub(row.CreatedAt))

	for _, repo := range []*SAMLRequestRepository{first, second, NewSAMLRequestRepository(openMFATestDB(t, path))} {
		got, err := repo.Lookup(context.Background(), request.RelayState, request.BrowserSecret)
		require.NoError(t, err)
		require.Equal(t, request.SAMLRequestBinding, *got)
	}
	// Returned values cannot alter persisted correlation state.
	request.ConfigVersion++
	got, err := second.Lookup(context.Background(), request.RelayState, request.BrowserSecret)
	require.NoError(t, err)
	require.Equal(t, config, got.SAMLConfigurationBinding)
}

func TestSAMLRequestPrepareRejectsInvalidInputAndDuplicateIDs(t *testing.T) {
	first, _, _ := samlTestRepositories(t)
	for _, requestID := range []string{"", "-invalid", "has space", "id:namespace", "\x00bad", strings.Repeat("r", 257)} {
		request, err := first.Prepare(context.Background(), requestID, samlTestConfig(), time.Minute)
		require.Nil(t, request)
		require.ErrorIs(t, err, ErrSAMLRequestInvalid)
	}
	for _, ttl := range []time.Duration{0, -time.Second, SAMLRequestMaxTTL + time.Nanosecond} {
		request, err := first.Prepare(context.Background(), "_request", samlTestConfig(), ttl)
		require.Nil(t, request)
		require.ErrorIs(t, err, ErrSAMLRequestInvalid)
	}
	for _, invalid := range []func(*SAMLConfigurationBinding){
		func(c *SAMLConfigurationBinding) { c.AccountID = 0 },
		func(c *SAMLConfigurationBinding) { c.ConfigVersion = 0 },
		func(c *SAMLConfigurationBinding) { c.ConfigDigest = "" },
		func(c *SAMLConfigurationBinding) { c.ConfigDigest = strings.Repeat("A", 64) },
		func(c *SAMLConfigurationBinding) { c.ConfigDigest = strings.Repeat("g", 64) },
	} {
		config := samlTestConfig()
		invalid(&config)
		request, err := first.Prepare(context.Background(), "_request", config, time.Minute)
		require.Nil(t, request)
		require.ErrorIs(t, err, ErrSAMLRequestInvalid)
	}
	samlPrepare(t, first, "_request", samlTestConfig())
	duplicate, err := first.Prepare(context.Background(), "_request", samlTestConfig(), time.Minute)
	require.Nil(t, duplicate)
	require.Error(t, err)
	requireSAMLRowCounts(t, first, 1, 0, 0)
}

func TestSAMLRequestSecretsAreIndependentAcrossRequestsAndBrowsers(t *testing.T) {
	first, _, _ := samlTestRepositories(t)
	one := samlPrepare(t, first, "_one", samlTestConfig())
	two := samlPrepare(t, first, "_two", samlTestConfig())
	require.NotEqual(t, one.RelayState, two.RelayState)
	require.NotEqual(t, one.BrowserSecret, two.BrowserSecret)
	for _, pair := range [][2]string{
		{one.RelayState, ""}, {"", one.BrowserSecret},
		{one.RelayState, two.BrowserSecret}, {two.RelayState, one.BrowserSecret},
		{one.BrowserSecret, one.RelayState}, {one.RelayState + "=", one.BrowserSecret},
		{strings.Repeat("!", 43), one.BrowserSecret}, {one.RelayState, strings.Repeat("A", 42) + "B"},
	} {
		got, err := first.Lookup(context.Background(), pair[0], pair[1])
		require.Nil(t, got)
		require.ErrorIs(t, err, ErrSAMLRequestInvalid)
	}
	requireSAMLRowCounts(t, first, 2, 0, 0)
}

func TestSAMLRequestLookupRejectsConsumedAndExactExpiryBoundary(t *testing.T) {
	first, _, _ := samlTestRepositories(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	first.now = func() time.Time { return now }
	request := samlPrepare(t, first, "_expires", samlTestConfig())
	now = request.ExpiresAt
	got, err := first.Lookup(context.Background(), request.RelayState, request.BrowserSecret)
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrSAMLRequestInvalid)
	now = now.Add(-time.Nanosecond)
	_, err = first.Lookup(context.Background(), request.RelayState, request.BrowserSecret)
	require.NoError(t, err)
	require.NoError(t, first.db.Model(&domain.SAMLAuthnRequest{}).Where("request_id = ?", request.RequestID).Update("consumed_at", now).Error)
	got, err = first.Lookup(context.Background(), request.RelayState, request.BrowserSecret)
	require.Nil(t, got)
	require.ErrorIs(t, err, ErrSAMLRequestInvalid)
}

type samlFailingReader struct{}

func (samlFailingReader) Read([]byte) (int, error) { return 0, errors.New("injected entropy failure") }

func TestSAMLRequestInfrastructureFailsClosed(t *testing.T) {
	ctx := context.Background()
	for _, repo := range []*SAMLRequestRepository{nil, NewSAMLRequestRepository(nil), {}} {
		request, err := repo.Prepare(ctx, "_request", samlTestConfig(), time.Minute)
		require.Nil(t, request)
		require.ErrorIs(t, err, ErrSAMLStorageUnavailable)
		binding, err := repo.Lookup(ctx, "", "")
		require.Nil(t, binding)
		require.ErrorIs(t, err, ErrSAMLStorageUnavailable)
		require.ErrorIs(t, repo.Consume(ctx, "", "", samlTestConfig(), nil), ErrSAMLStorageUnavailable)
	}
	first, _, _ := samlTestRepositories(t)
	first.random = samlFailingReader{}
	request, err := first.Prepare(ctx, "_request", samlTestConfig(), time.Minute)
	require.Nil(t, request)
	require.ErrorContains(t, err, "entropy failure")
	requireSAMLRowCounts(t, first, 0, 0, 0)
	first = NewSAMLRequestRepository(first.db)
	request = samlPrepare(t, first, "_request", samlTestConfig())
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	got, err := first.Lookup(cancelled, request.RelayState, request.BrowserSecret)
	require.Nil(t, got)
	require.ErrorIs(t, err, context.Canceled)
	prepared, err := first.Prepare(cancelled, "_cancelled", samlTestConfig(), time.Minute)
	require.Nil(t, prepared)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, first.db.Migrator().DropTable(&domain.SAMLAuthnRequest{}))
	got, err = first.Lookup(ctx, request.RelayState, request.BrowserSecret)
	require.Nil(t, got)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrSAMLRequestInvalid)
	prepared, err = first.Prepare(ctx, "_missing_table", samlTestConfig(), time.Minute)
	require.Nil(t, prepared)
	require.Error(t, err)
}

func TestSAMLRequestConsumeRejectsNilAndZeroValueCapabilities(t *testing.T) {
	first, _, _ := samlTestRepositories(t)
	request := samlPrepare(t, first, "_request", samlTestConfig())
	for _, verified := range []*auth.SAMLVerifiedAssertion{nil, {}} {
		require.ErrorIs(t, first.Consume(context.Background(), request.RelayState, request.BrowserSecret, samlTestConfig(), verified), ErrSAMLRequestInvalid)
	}
	requireSAMLRowCounts(t, first, 1, 0, 0)
}

func samlVerified(t *testing.T, fixture *samltest.Fixture, requestID string) (*auth.SAMLVerifiedAssertion, SAMLConfigurationBinding) {
	t.Helper()
	provider, err := auth.NewSAMLServiceProvider(fixture.Config)
	require.NoError(t, err)
	verified, err := provider.VerifyXML(fixture.SignedResponse(requestID), requestID)
	require.NoError(t, err)
	return verified, SAMLConfigurationBinding{AccountID: fixture.Config.AccountID, ConfigVersion: fixture.Config.ConfigVersion, ConfigDigest: provider.ConfigDigest()}
}

func requireSAMLWinner(t *testing.T, results []error) {
	t.Helper()
	winners := 0
	for _, err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, ErrSAMLRequestInvalid)
		}
	}
	require.Equal(t, 1, winners)
}

func TestSAMLRequestAtomicConsumeAcrossInstancesAndRestart(t *testing.T) {
	first, second, path := samlTestRepositories(t)
	fixture := samltest.NewFixture(t)
	verified, config := samlVerified(t, fixture, "_one_use")
	request := samlPrepare(t, first, "_one_use", config)
	results := samlConcurrent(t, first, second, 16, func(repo *SAMLRequestRepository, _ int) error {
		return repo.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified)
	})
	requireSAMLWinner(t, results)
	requireSAMLRowCounts(t, first, 0, 1, 1)
	for _, repo := range []*SAMLRequestRepository{first, second} {
		sqlDB, err := repo.db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
	}
	restarted := NewSAMLRequestRepository(openMFATestDB(t, path))
	require.ErrorIs(t, restarted.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified), ErrSAMLRequestInvalid)
	_, err := restarted.Lookup(context.Background(), request.RelayState, request.BrowserSecret)
	require.ErrorIs(t, err, ErrSAMLRequestInvalid)
	var replay domain.SAMLReplay
	require.NoError(t, restarted.db.First(&replay).Error)
	require.Equal(t, samlReplayDigest("assertion", verified.Issuer(), verified.AssertionID()), replay.AssertionDigest)
	require.Equal(t, samlReplayDigest("response", verified.Issuer(), verified.ResponseID()), replay.ResponseDigest)
	require.False(t, replay.RetainUntil.Before(request.ExpiresAt))
	require.False(t, replay.RetainUntil.Before(verified.ExpiresAt()))
	require.NotContains(t, fmt.Sprintf("%+v", replay), verified.AssertionID())
	require.NotContains(t, fmt.Sprintf("%+v", replay), verified.ResponseID())
	require.NotContains(t, fmt.Sprintf("%+v", replay), verified.Issuer())
	encoded, err := json.Marshal(replay)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(encoded))
}

func TestSAMLRequestIndependentReplayIDsSurviveConfigurationChanges(t *testing.T) {
	for _, replayID := range []string{"assertion", "response"} {
		for _, scope := range []string{"same", "config-rotation", "account-change"} {
			t.Run(replayID+"/"+scope, func(t *testing.T) {
				first, _, path := samlTestRepositories(t)
				fixture := samltest.NewFixture(t)
				one, config := samlVerified(t, fixture, "_first")
				request := samlPrepare(t, first, "_first", config)
				require.NoError(t, first.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, one))
				if replayID == "assertion" {
					fixture.ResponseID += "_new"
				} else {
					fixture.AssertionID += "_new"
				}
				if scope == "config-rotation" {
					fixture.Config.ConfigVersion++
					fixture.Config.SSOURL += "/rotated"
				}
				if scope == "account-change" {
					fixture.Config.AccountID++
				}
				two, rotated := samlVerified(t, fixture, "_second")
				if scope != "same" {
					require.NotEqual(t, config.ConfigDigest, rotated.ConfigDigest)
				}
				restarted := NewSAMLRequestRepository(openMFATestDB(t, path))
				sibling := samlPrepare(t, restarted, "_second", rotated)
				require.ErrorIs(t, restarted.Consume(context.Background(), sibling.RelayState, sibling.BrowserSecret, rotated, two), ErrSAMLRequestInvalid)
				requireSAMLRowCounts(t, restarted, 1, 1, 1)
				// A replay conflict rolls back pending consumption; lookup remains possible.
				_, err := restarted.Lookup(context.Background(), sibling.RelayState, sibling.BrowserSecret)
				require.NoError(t, err)
			})
		}
	}
}

func testSAMLConcurrentReplayIDs(t *testing.T, first, second *SAMLRequestRepository) {
	t.Helper()
	for _, replayID := range []string{"assertion", "response"} {
		t.Run(replayID, func(t *testing.T) {
			fixture := samltest.NewFixture(t)
			const attempts = 12
			capabilities := make([]*auth.SAMLVerifiedAssertion, attempts)
			pending := make([]*SAMLPreparedRequest, attempts)
			var config SAMLConfigurationBinding
			for i := range pending {
				requestID := fmt.Sprintf("_%s_%d", replayID, i)
				if replayID == "assertion" {
					fixture.ResponseID = fmt.Sprintf("_%s_response_%d", replayID, i)
				} else {
					fixture.AssertionID = fmt.Sprintf("_%s_assertion_%d", replayID, i)
				}
				capabilities[i], config = samlVerified(t, fixture, requestID)
				pending[i] = samlPrepare(t, first, requestID, config)
			}
			results := samlConcurrent(t, first, second, attempts, func(repo *SAMLRequestRepository, i int) error {
				return repo.Consume(context.Background(), pending[i].RelayState, pending[i].BrowserSecret, config, capabilities[i])
			})
			requireSAMLWinner(t, results)
		})
	}
	requireSAMLRowCounts(t, first, 22, 2, 2)
}

func TestSAMLRequestAtomicIndependentReplayRaces(t *testing.T) {
	first, second, _ := samlTestRepositories(t)
	testSAMLConcurrentReplayIDs(t, first, second)
}

func TestSAMLRequestConsumeRechecksAllBindings(t *testing.T) {
	for _, mismatch := range []string{"browser", "relay", "request", "current-account", "current-version", "current-digest", "pending-account", "pending-version", "pending-digest", "expired-request", "expired-assertion"} {
		t.Run(mismatch, func(t *testing.T) {
			first, _, _ := samlTestRepositories(t)
			fixture := samltest.NewFixture(t)
			verified, config := samlVerified(t, fixture, "_bound")
			request := samlPrepare(t, first, "_bound", config)
			relay, browser := request.RelayState, request.BrowserSecret
			current := config
			switch mismatch {
			case "browser":
				browser = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
			case "relay":
				relay = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
			case "request":
				verified, _ = samlVerified(t, fixture, "_different_request")
			case "current-account":
				current.AccountID++
			case "current-version":
				current.ConfigVersion++
			case "current-digest":
				current.ConfigDigest = strings.Repeat("b", 64)
			case "pending-account":
				require.NoError(t, first.db.Model(&domain.SAMLAuthnRequest{}).Where("request_id = ?", request.RequestID).Update("account_id", config.AccountID+1).Error)
			case "pending-version":
				require.NoError(t, first.db.Model(&domain.SAMLAuthnRequest{}).Where("request_id = ?", request.RequestID).Update("config_version", config.ConfigVersion+1).Error)
			case "pending-digest":
				require.NoError(t, first.db.Model(&domain.SAMLAuthnRequest{}).Where("request_id = ?", request.RequestID).Update("config_digest", strings.Repeat("b", 64)).Error)
			case "expired-request":
				require.NoError(t, first.db.Model(&domain.SAMLAuthnRequest{}).Where("request_id = ?", request.RequestID).Update("expires_at", time.Now().Add(-time.Second)).Error)
			case "expired-assertion":
				first.now = func() time.Time { return verified.ExpiresAt() }
			}
			require.ErrorIs(t, first.Consume(context.Background(), relay, browser, current, verified), ErrSAMLRequestInvalid)
			requireSAMLRowCounts(t, first, 1, 0, 0)
		})
	}
}

func TestSAMLRequestExpiryCrossingDuringTransactionRollsBack(t *testing.T) {
	for _, expiryCall := range []int{3, 4} {
		t.Run(fmt.Sprintf("clock-read-%d", expiryCall), func(t *testing.T) {
			first, _, _ := samlTestRepositories(t)
			fixture := samltest.NewFixture(t)
			verified, config := samlVerified(t, fixture, "_boundary")
			request := samlPrepare(t, first, "_boundary", config)
			now := time.Now().UTC()
			expired := request.ExpiresAt
			if verified.ExpiresAt().Before(expired) {
				expired = verified.ExpiresAt()
			}
			calls := 0
			first.now = func() time.Time {
				calls++
				if calls >= expiryCall {
					return expired
				}
				return now
			}
			require.ErrorIs(t, first.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified), ErrSAMLRequestInvalid)
			require.GreaterOrEqual(t, calls, expiryCall)
			requireSAMLRowCounts(t, first, 1, 0, 0)
		})
	}
}

func TestSAMLRequestConsumeStorageFailuresRollBackWithoutFallback(t *testing.T) {
	for _, failure := range []string{"request-update", "replay-insert", "missing-replay-table", "cancelled-context", "closed-database"} {
		t.Run(failure, func(t *testing.T) {
			first, second, path := samlTestRepositories(t)
			fixture := samltest.NewFixture(t)
			verified, config := samlVerified(t, fixture, "_failure")
			request := samlPrepare(t, first, "_failure", config)
			ctx := context.Background()
			switch failure {
			case "request-update":
				require.NoError(t, first.db.Exec("CREATE TRIGGER saml_fail BEFORE UPDATE OF consumed_at ON saml_authn_requests BEGIN SELECT RAISE(ABORT, 'injected request failure'); END").Error)
			case "replay-insert":
				require.NoError(t, first.db.Exec("CREATE TRIGGER saml_fail BEFORE INSERT ON saml_replays BEGIN SELECT RAISE(ABORT, 'injected replay failure'); END").Error)
			case "missing-replay-table":
				require.NoError(t, first.db.Migrator().DropTable(&domain.SAMLReplay{}))
			case "closed-database":
				sqlDB, err := first.db.DB()
				require.NoError(t, err)
				require.NoError(t, sqlDB.Close())
			case "cancelled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err := first.Consume(ctx, request.RelayState, request.BrowserSecret, config, verified)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrSAMLRequestInvalid)
			if failure == "closed-database" {
				requireSAMLRowCounts(t, second, 1, 0, 0)
				first = NewSAMLRequestRepository(openMFATestDB(t, path))
			}
			if failure == "request-update" || failure == "replay-insert" {
				require.NoError(t, first.db.Exec("DROP TRIGGER saml_fail").Error)
			}
			if failure == "missing-replay-table" {
				require.NoError(t, first.db.AutoMigrate(&domain.SAMLReplay{}))
			}
			requireSAMLRowCounts(t, first, 1, 0, 0)
			require.NoError(t, first.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, verified))
			requireSAMLRowCounts(t, first, 0, 1, 1)
		})
	}
}

func TestSAMLRequestReplayIDsAreScopedToTrustedIssuer(t *testing.T) {
	first, _, _ := samlTestRepositories(t)
	fixture := samltest.NewFixture(t)
	one, config := samlVerified(t, fixture, "_issuer_one")
	request := samlPrepare(t, first, "_issuer_one", config)
	require.NoError(t, first.Consume(context.Background(), request.RelayState, request.BrowserSecret, config, one))
	fixture.Config.IDPEntityID = "https://second-idp.saml.test/entity"
	two, config2 := samlVerified(t, fixture, "_issuer_two")
	request2 := samlPrepare(t, first, "_issuer_two", config2)
	require.Equal(t, one.AssertionID(), two.AssertionID())
	require.Equal(t, one.ResponseID(), two.ResponseID())
	require.NotEqual(t, one.Issuer(), two.Issuer())
	require.NoError(t, first.Consume(context.Background(), request2.RelayState, request2.BrowserSecret, config2, two))
	requireSAMLRowCounts(t, first, 0, 2, 2)
}
