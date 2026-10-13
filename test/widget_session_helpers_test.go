package test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
)

// bootstrapWidgetVisitor obtains a real server-created visitor identity. Public
// source identifiers and website tokens are deliberately not used as credentials.
func bootstrapWidgetVisitor(t *testing.T, handler http.Handler, websiteToken string) (sourceID, session string, contactID uint) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/config?website_token="+url.QueryEscape(websiteToken), strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("bootstrap visitor session: code=%d body=%s", w.Code, w.Body.String())
	}
	var result struct {
		SourceID string `json:"source_id"`
		Token    string `json:"visitor_token"`
		Contact  struct {
			ID uint `json:"id"`
		} `json:"contact"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode visitor bootstrap: %v", err)
	}
	if result.SourceID == "" || result.Token == "" || result.Contact.ID == 0 {
		t.Fatalf("bootstrap did not return a complete signed visitor identity: %s", w.Body.String())
	}
	return result.SourceID, result.Token, result.Contact.ID
}

// signWidgetVisitorSession is for tests that explicitly persist their own
// Contact and ContactInbox fixtures before exercising the protected endpoints.
func signWidgetVisitorSession(t *testing.T, inboxID, contactID uint, sourceID, secret string) string {
	t.Helper()
	token, err := auth.GenerateVisitorToken(inboxID, contactID, sourceID, secret, time.Hour)
	if err != nil {
		t.Fatalf("sign visitor fixture session: %v", err)
	}
	return token
}
