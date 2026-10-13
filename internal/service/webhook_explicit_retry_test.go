package service

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type webhookRetryTransport struct {
	calls                               atomic.Int64
	body, event, signature, url, method string
}

func (s *webhookRetryTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	s.calls.Add(1)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	s.body = string(data)
	s.url = r.URL.String()
	s.method = r.Method
	s.event = r.Header.Get("X-Chatwoot-Event")
	s.signature = r.Header.Get("X-Chatwoot-Signature")
	return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader("synthetic accepted")), Header: make(http.Header)}, nil
}

type webhookRetrySnapshot struct {
	Webhooks   []domain.Webhook
	Deliveries []domain.WebhookDelivery
}

func webhookRetrySnapshotDB(t *testing.T, db *gorm.DB) webhookRetrySnapshot {
	t.Helper()
	var s webhookRetrySnapshot
	require.NoError(t, db.Order("id").Find(&s.Webhooks).Error)
	require.NoError(t, db.Order("id").Find(&s.Deliveries).Error)
	return s
}
func TestWebhookExplicitRetryOwner(t *testing.T) {
	runWebhookExplicitRetryOwner(t, openSummaryMessageSQLite)
}
func TestPostgresWebhookExplicitRetryOwner(t *testing.T) {
	runWebhookExplicitRetryOwner(t, reportWindowTestPostgres(t))
}
func runWebhookExplicitRetryOwner(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, name := range []string{"missing_with_latest", "cross_account_with_latest", "missing_with_webhook_only", "read_error_empty", "read_error_populated", "missing_table", "exact_target", "legacy_latest", "legacy_create", "legacy_empty"} {
		t.Run(name, func(t *testing.T) {
			db := open(t, "EXT")
			require.NoError(t, database.AutoMigrateModule(db, "EXT"))
			wh := domain.Webhook{ID: 11, AccountID: 91, URL: "https://synthetic.invalid/hook", Secret: "fixture-signing-secret", Subscriptions: `["*"]`}
			if name != "legacy_empty" {
				require.NoError(t, db.Create(&wh).Error)
			}
			target := domain.WebhookDelivery{ID: 101, AccountID: 91, WebhookID: wh.ID, Event: "conversation.created", URL: wh.URL, Payload: `{"selected":true}`, Attempts: 3, Status: "failed"}
			latest := target
			latest.ID = 102
			latest.Payload = `{"selected":false}`
			foreign := target
			foreign.ID = 201
			foreign.AccountID = 92
			if name != "missing_with_webhook_only" && name != "legacy_create" && name != "legacy_empty" {
				for _, del := range []domain.WebhookDelivery{target, latest, foreign} {
					require.NoError(t, db.Create(&del).Error)
				}
			}
			before := webhookRetrySnapshotDB(t, db)
			var reads, writes int
			fault := false
			populated := false
			require.NoError(t, db.Callback().Query().After("gorm:after_query").Register("explicit_retry_query", func(tx *gorm.DB) {
				if tx.Statement.Table != "webhook_deliveries" {
					return
				}
				reads++
				if fault {
					if d, ok := tx.Statement.Dest.(*domain.WebhookDelivery); ok && d.ID != 0 {
						populated = true
					}
					_ = tx.AddError(errors.New("private-webhook-read-failure"))
				}
			}))
			for _, cb := range []struct {
				name     string
				register func(string, func(*gorm.DB)) error
			}{
				{"explicit_retry_create", func(n string, f func(*gorm.DB)) error {
					return db.Callback().Create().Before("gorm:create").Register(n, f)
				}},
				{"explicit_retry_update", func(n string, f func(*gorm.DB)) error {
					return db.Callback().Update().Before("gorm:update").Register(n, f)
				}},
				{"explicit_retry_delete", func(n string, f func(*gorm.DB)) error {
					return db.Callback().Delete().Before("gorm:delete").Register(n, f)
				}},
			} {
				require.NoError(t, cb.register(cb.name, func(*gorm.DB) { writes++ }))
			}
			transport := &webhookRetryTransport{}
			s := NewWebhookService(db, repository.NewWebhookRepository(db))
			s.SetHTTPClient(&http.Client{Transport: transport})
			id := target.ID
			switch name {
			case "missing_with_latest", "missing_with_webhook_only", "read_error_empty":
				id = 999
			case "cross_account_with_latest":
				id = foreign.ID
			case "legacy_latest", "legacy_create", "legacy_empty":
				id = 0
			}
			fault = name == "read_error_empty" || name == "read_error_populated"
			if name == "missing_table" {
				require.NoError(t, db.Migrator().RenameTable("webhook_deliveries", "retry_unavailable_deliveries"))
			}
			got, err := s.RetryDelivery(91, id)
			fault = false
			capturedReads, capturedWrites := reads, writes
			if name == "missing_table" {
				require.NoError(t, db.Migrator().RenameTable("retry_unavailable_deliveries", "webhook_deliveries"))
			}
			switch name {
			case "exact_target", "legacy_latest", "legacy_create":
				require.NoError(t, err)
				require.NotNil(t, got)
				require.Equal(t, int64(1), transport.calls.Load())
				require.Equal(t, "delivered", got.Status)
				require.Equal(t, 202, got.ResponseCode)
				if name == "exact_target" {
					require.Equal(t, target.ID, got.ID)
					require.Equal(t, target.Attempts+1, got.Attempts)
					require.Equal(t, target.Payload, transport.body)
					require.Equal(t, target.URL, transport.url)
					require.Equal(t, http.MethodPost, transport.method)
					require.Equal(t, target.Event, transport.event)
					require.Equal(t, s.SignPayload([]byte(target.Payload), wh.Secret), transport.signature)
					after := webhookRetrySnapshotDB(t, db)
					require.Equal(t, before.Webhooks, after.Webhooks)
					require.Equal(t, before.Deliveries[1:], after.Deliveries[1:])
					require.Equal(t, *got, after.Deliveries[0])
					require.Equal(t, 1, capturedReads)
				}
				if name == "legacy_latest" {
					require.Equal(t, latest.ID, got.ID)
					require.Equal(t, latest.Payload, transport.body)
				}
				if name == "legacy_create" {
					require.Equal(t, "manual.retry", got.Event)
					require.NotZero(t, got.ID)
				}
			default:
				require.Error(t, err)
				require.Nil(t, got)
				require.Zero(t, transport.calls.Load())
				require.Zero(t, capturedWrites)
				if name != "legacy_empty" {
					require.Equal(t, 1, capturedReads, "explicit target cannot fall back to a second read")
				}
				if name == "read_error_populated" {
					require.True(t, populated, "fault must occur after a target row was populated")
				}
				if strings.HasPrefix(name, "missing_with") || name == "cross_account_with_latest" {
					require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				}
				require.Equal(t, before, webhookRetrySnapshotDB(t, db))
			}
		})
	}
}
