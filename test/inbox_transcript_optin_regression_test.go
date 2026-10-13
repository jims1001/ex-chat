package test

import (
	"encoding/json"
	"net"
	"net/http"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/router"
)

// Keep this correctness regression baseline-compatible: copying this file alone
// to phase40 must compile and fail because opted-in Inbox mail is sent directly.
func TestInboxTranscriptQueueHTTPOptInRegression(t *testing.T) {
	for _, producer := range []string{"account", "widget"} {
		t.Run(producer, func(t *testing.T) {
			t.Setenv("INBOX_TRANSCRIPT_QUEUE_ENABLED", "true")
			f := newWidgetTranscriptFixture(t)
			inboxSMTP := newAccountTranscriptSMTP(t)
			host, port, err := net.SplitHostPort(inboxSMTP.listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			provider, err := json.Marshal(map[string]any{"smtp_address": host, "smtp_port": port, "smtp_enabled": true})
			if err != nil {
				t.Fatal(err)
			}
			if err = f.db.Model(&f.inbox).Update("provider_config", string(provider)).Error; err != nil {
				t.Fatal(err)
			}
			f.rebuild(router.Options{})
			if producer == "account" {
				a := f.accountTranscriptFixture
				requireAccountTranscriptQueued(t, a, a.request(http.MethodPost, a.path(), a.token, "inbox-optin-regression", map[string]string{}))
			} else {
				requireWidgetTranscriptQueued(t, f, f.request(http.MethodPost, f.path(), f.session, "inbox-optin-regression", f.input("")))
			}
			if inboxSMTP.connections.Load() != 0 || f.smtp.connections.Load() != 0 {
				t.Fatal("Inbox admission contacted SMTP instead of preserving a durable pending request")
			}
			requireAccountTranscriptCounts(t, f.accountTranscriptFixture, 1, 1, 0)
		})
	}
}
