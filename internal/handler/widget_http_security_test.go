package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type widgetHTTPSecurityFixture struct {
	db                                                           *gorm.DB
	r                                                            *gin.Engine
	owner, other                                                 domain.Contact
	inbox, foreignInbox                                          domain.Inbox
	conversation                                                 domain.Conversation
	message                                                      domain.Message
	session, otherSession, foreignSession, invalidMappingSession string
	mail                                                         *service.MockEmailSender
}

func newWidgetHTTPSecurityFixture(t *testing.T) *widgetHTTPSecurityFixture {
	t.Helper()
	const secret = "http-widget-boundary-security-test"
	db, err := database.InitDB(&config.Config{DBDriver: "sqlite", DBPath: ":memory:"})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	f := &widgetHTTPSecurityFixture{db: db, r: gin.New(), mail: service.NewMockEmailSender()}
	account := domain.Account{Name: "HTTP boundary"}
	require.NoError(t, db.Create(&account).Error)
	foreign := domain.Account{Name: "Other tenant"}
	require.NoError(t, db.Create(&foreign).Error)
	f.inbox = domain.Inbox{AccountID: account.ID, Name: "Widget", WebsiteToken: "http-widget"}
	f.foreignInbox = domain.Inbox{AccountID: foreign.ID, Name: "Foreign", WebsiteToken: "foreign-http-widget"}
	require.NoError(t, db.Create(&f.inbox).Error)
	require.NoError(t, db.Create(&f.foreignInbox).Error)
	f.owner = domain.Contact{AccountID: account.ID, Name: "Owner", Email: "owner@example.test", Identifier: "owner-identifier"}
	f.other = domain.Contact{AccountID: account.ID, Name: "Other", Email: "other@example.test", Identifier: "other-identifier"}
	require.NoError(t, db.Create(&f.owner).Error)
	require.NoError(t, db.Create(&f.other).Error)
	foreignContact := domain.Contact{AccountID: foreign.ID, Name: "Foreign"}
	require.NoError(t, db.Create(&foreignContact).Error)
	for _, ci := range []domain.ContactInbox{
		{InboxID: f.inbox.ID, ContactID: f.owner.ID, SourceID: "source-owner"},
		{InboxID: f.inbox.ID, ContactID: f.other.ID, SourceID: "source-other"},
		{InboxID: f.foreignInbox.ID, ContactID: foreignContact.ID, SourceID: "source-owner"},
	} {
		require.NoError(t, db.Create(&ci).Error)
	}
	f.session, err = auth.GenerateVisitorToken(f.inbox.ID, f.owner.ID, "source-owner", secret, time.Hour)
	require.NoError(t, err)
	f.otherSession, err = auth.GenerateVisitorToken(f.inbox.ID, f.other.ID, "source-other", secret, time.Hour)
	require.NoError(t, err)
	f.foreignSession, err = auth.GenerateVisitorToken(f.foreignInbox.ID, foreignContact.ID, "source-owner", secret, time.Hour)
	require.NoError(t, err)
	f.invalidMappingSession, err = auth.GenerateVisitorToken(f.inbox.ID, f.owner.ID, "unknown-source", secret, time.Hour)
	require.NoError(t, err)
	f.conversation = domain.Conversation{AccountID: account.ID, InboxID: f.inbox.ID, ContactID: f.owner.ID, Status: domain.ConversationStatusOpen}
	require.NoError(t, db.Create(&f.conversation).Error)
	f.message = domain.Message{AccountID: account.ID, ConversationID: f.conversation.ID, SenderType: domain.SenderTypeContact, SenderID: f.owner.ID, MessageType: domain.MessageTypeIncoming, Content: "owner-public-message", ContentType: domain.ContentTypeText}
	require.NoError(t, db.Create(&f.message).Error)
	require.NoError(t, db.Create(&domain.Message{AccountID: account.ID, ConversationID: f.conversation.ID, SenderType: domain.SenderTypeUser, MessageType: domain.MessageTypeOutgoing, Content: "private-note-must-not-leak", Private: true}).Error)
	contacts, inboxes := repository.NewContactRepository(db), repository.NewInboxRepository(db)
	conversations, messages := repository.NewConversationRepository(db), repository.NewMessageRepository(db)
	wh := NewWidgetHandler(repository.NewWidgetRepository(db), conversations, contacts, messages, inboxes, nil)
	wh.SetJWTSecret(secret)
	ih := NewInboxHandler(inboxes, nil)
	ih.SetContactRepo(contacts)
	ih.SetJWTSecret(secret)
	ch := NewContactHandler(contacts, inboxes)
	ch.SetJWTSecret(secret)
	cv := NewConversationHandler(conversations, messages, inboxes, contacts, nil, nil)
	cv.SetJWTSecret(secret)
	emailService := service.NewEmailService(db)
	emailService.SetSender(f.mail)
	cv.SetEmailService(emailService)
	f.r.GET("/config", ih.WidgetConfig)
	f.r.POST("/config", ih.WidgetConfigCreate)
	f.r.GET("/contact", wh.GetContact)
	f.r.POST("/contact", ch.WidgetIdentify)
	f.r.PATCH("/contact", wh.UpdateContact)
	f.r.POST("/set_user", wh.SetUser)
	f.r.GET("/conversations", wh.ListConversations)
	f.r.POST("/conversations", cv.WidgetCreateConversation)
	f.r.GET("/conversations/:id", wh.GetConversation)
	f.r.PATCH("/conversations/:id/attributes", wh.MergeConversationCustomAttributes)
	f.r.GET("/messages", cv.WidgetListMessages)
	f.r.POST("/messages", cv.WidgetCreateMessage)
	f.r.PATCH("/messages/:id", wh.UpdateMessage)
	f.r.POST("/last_seen", cv.WidgetUpdateLastSeen)
	f.r.POST("/typing", cv.WidgetToggleTyping)
	f.r.POST("/transcript", cv.WidgetSendTranscript)
	f.r.POST("/events", wh.RecordEvent)
	f.r.GET("/events", wh.ListEvents)
	f.r.GET("/labels", wh.GetLabels)
	f.r.POST("/labels", wh.AddLabels)
	f.r.DELETE("/labels", wh.RemoveLabels)
	f.r.PATCH("/attributes", wh.MergeContactCustomAttributes)
	f.r.DELETE("/attributes", wh.DestroyContactCustomAttributes)
	return f
}

func (f *widgetHTTPSecurityFixture) request(method, path, body, token string) *httptest.ResponseRecorder {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	req := httptest.NewRequest(method, path+sep+"website_token=http-widget", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Visitor-Session", token)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func TestWidgetHTTPRequiresSessionInEveryMode(t *testing.T) {
	oldMode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(oldMode) })
	f := newWidgetHTTPSecurityFixture(t)
	for _, mode := range []string{gin.TestMode, gin.ReleaseMode, gin.DebugMode} {
		gin.SetMode(mode)
		for _, endpoint := range []struct{ method, path, body string }{
			{"GET", "/contact", ""}, {"POST", "/contact", `{"source_id":"source-owner","name":"unauthorized"}`},
			{"PATCH", "/contact", `{"source_id":"source-owner","name":"unauthorized"}`},
			{"POST", "/set_user", `{"identifier":"owner-identifier"}`},
			{"GET", "/conversations", ""}, {"POST", "/conversations", `{"source_id":"source-owner","message":"unauthorized"}`},
			{"GET", "/conversations/1", ""}, {"PATCH", "/conversations/1/attributes", `{"custom_attributes":{"x":true}}`},
			{"GET", "/messages?conversation_id=1", ""}, {"POST", "/messages", `{"source_id":"source-owner","conversation_id":1,"content":"unauthorized"}`},
			{"PATCH", "/messages/1", `{"source_id":"source-owner","content":"unauthorized"}`},
			{"POST", "/last_seen", `{"conversation_id":1}`}, {"POST", "/typing", `{"conversation_id":1,"typing_status":"on"}`},
			{"POST", "/transcript", `{"conversation_id":1,"email":"attacker@example.test"}`},
			{"POST", "/events", `{"source_id":"source-owner","name":"forged"}`}, {"GET", "/events", ""},
			{"GET", "/labels", ""}, {"POST", "/labels", `{"source_id":"source-owner","labels":["forged"]}`},
			{"DELETE", "/labels", `{"source_id":"source-owner","labels":["forged"]}`},
			{"PATCH", "/attributes", `{"source_id":"source-owner","custom_attributes":{"x":true}}`}, {"DELETE", "/attributes", `{"custom_attribute_keys":["x"]}`},
		} {
			for _, token := range []string{"", "invalid", f.invalidMappingSession, f.foreignSession} {
				path := endpoint.path
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				path += sep + "source_id=source-owner"
				w := f.request(endpoint.method, path, endpoint.body, token)
				require.Equal(t, http.StatusForbidden, w.Code, "%s %s in %s: %s", endpoint.method, endpoint.path, mode, w.Body.String())
			}
			for _, explicit := range []string{"", "invalid"} {
				sep := "?"
				if strings.Contains(endpoint.path, "?") {
					sep = "&"
				}
				w := f.request(endpoint.method, endpoint.path+sep+"visitor_session="+explicit, endpoint.body, f.session)
				require.Equal(t, http.StatusForbidden, w.Code, "explicit invalid/empty credential must not downgrade to valid header: %s %s", endpoint.method, endpoint.path)
			}

		}
	}
	require.Empty(t, f.mail.GetSentEmails())
}

func TestWidgetTranscriptRequiresOwnerBeforeMail(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	body := fmt.Sprintf(`{"conversation_id":%d,"email":"recipient@example.test"}`, f.conversation.ID)
	for _, tc := range []struct {
		name, path, token string
		status            int
	}{
		{"missing source", "/transcript", "", 403},
		{"unknown source", "/transcript?source_id=does-not-exist", "", 403},
		{"known raw source", "/transcript?source_id=source-owner", "", 403},
		{"invalid token cannot downgrade", "/transcript?source_id=source-owner", "invalid", 403},
		{"valid token unknown mapping", "/transcript", f.invalidMappingSession, 403},
		{"cross contact", "/transcript?source_id=source-owner", f.otherSession, 404},
		{"cross inbox and tenant", "/transcript", f.foreignSession, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request("POST", tc.path, body, tc.token)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Empty(t, f.mail.GetSentEmails())
		})
	}
	w := f.request("POST", "/transcript?source_id=source-other", body, f.session)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	sent := f.mail.GetSentEmails()
	require.Len(t, sent, 1)
	require.Equal(t, "recipient@example.test", sent[0].To)
	require.Contains(t, sent[0].TextBody, "owner-public-message")
	require.NotContains(t, sent[0].TextBody, "private-note-must-not-leak")
	require.NotContains(t, sent[0].HTMLBody, "private-note-must-not-leak")

}

func TestWidgetBootstrapCannotClaimExistingContact(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	w := f.request("GET", "/config?source_id=source-owner", "", "")
	require.Equal(t, http.StatusOK, w.Code)
	var public struct {
		Contact     *domain.Contact `json:"contact"`
		PubsubToken string          `json:"pubsub_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &public))
	require.Nil(t, public.Contact)
	require.Empty(t, public.PubsubToken)
	guessed := `{"source_id":"source-owner","contact":{"source_id":"source-owner","identifier":"owner-identifier","email":"owner@example.test","name":"Impostor"}}`
	w = f.request("POST", "/config", guessed, "")
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var boot struct {
		Contact  domain.Contact `json:"contact"`
		SourceID string         `json:"source_id"`
		Token    string         `json:"visitor_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &boot))
	require.NotEqual(t, f.owner.ID, boot.Contact.ID)
	require.NotEqual(t, "source-owner", boot.SourceID)
	require.Len(t, boot.SourceID, 64)
	require.Empty(t, boot.Contact.Identifier)
	require.Empty(t, boot.Contact.Email)
	require.NotEmpty(t, boot.Token)
	w = f.request("GET", "/contact?source_id=source-owner", "", boot.Token)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "owner@example.test")
	for _, method := range []string{"GET", "POST"} {
		w = f.request(method, "/config?source_id=source-owner", guessed, "invalid")
		require.Equal(t, http.StatusForbidden, w.Code)
	}
	var beforeInvalid, afterInvalid int64
	require.NoError(t, f.db.Model(&domain.Contact{}).Count(&beforeInvalid).Error)
	w = f.request("POST", "/config?visitor_session=", guessed, f.session)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.NoError(t, f.db.Model(&domain.Contact{}).Count(&afterInvalid).Error)
	require.Equal(t, beforeInvalid, afterInvalid)
	w = f.request("POST", "/config?source_id=source-other", guessed, f.session)
	require.Equal(t, http.StatusCreated, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &boot))
	require.Equal(t, f.owner.ID, boot.Contact.ID)
	require.Equal(t, "source-owner", boot.SourceID)
	var owner domain.Contact
	require.NoError(t, f.db.First(&owner, f.owner.ID).Error)
	require.Equal(t, "Owner", owner.Name)
}

func TestWidgetHTTPSessionCannotCrossContact(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	for _, ep := range []struct{ method, path, body string }{
		{"GET", fmt.Sprintf("/messages?conversation_id=%d&source_id=source-owner", f.conversation.ID), ""},
		{"GET", fmt.Sprintf("/conversations/%d", f.conversation.ID), ""},
		{"POST", "/messages", fmt.Sprintf(`{"source_id":"source-owner","conversation_id":%d,"content":"forged"}`, f.conversation.ID)},
		{"POST", "/last_seen", fmt.Sprintf(`{"source_id":"source-owner","conversation_id":%d}`, f.conversation.ID)},
		{"POST", "/typing", fmt.Sprintf(`{"source_id":"source-owner","conversation_id":%d,"typing_status":"on"}`, f.conversation.ID)},
		{"POST", "/events", fmt.Sprintf(`{"source_id":"source-owner","conversation_id":%d,"name":"forged"}`, f.conversation.ID)},
		{"PATCH", fmt.Sprintf("/messages/%d", f.message.ID), `{"source_id":"source-owner","content":"forged"}`},
	} {
		w := f.request(ep.method, ep.path, ep.body, f.otherSession)
		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	}
	for _, method := range []string{"POST", "PATCH"} {
		w := f.request(method, "/contact?source_id=source-owner", `{"source_id":"source-owner","name":"Updated own profile"}`, f.otherSession)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		w = f.request(method, "/contact", `{"source_id":"source-owner","identifier":"owner-identifier","email":"owner@example.test"}`, f.otherSession)
		require.Equal(t, http.StatusForbidden, w.Code)
	}
	var owner, other domain.Contact
	require.NoError(t, f.db.First(&owner, f.owner.ID).Error)
	require.NoError(t, f.db.First(&other, f.other.ID).Error)
	require.Equal(t, "Owner", owner.Name)
	require.Equal(t, "Updated own profile", other.Name)
	w := f.request("POST", "/conversations", `{"source_id":"source-owner","message":"own session"}`, f.otherSession)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created domain.Conversation
	require.NoError(t, f.db.Last(&created).Error)
	require.Equal(t, f.other.ID, created.ContactID)
}

func TestWidgetIdentifierProofCannotBeBypassedByAlternateContactRoutes(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	require.NoError(t, f.db.Model(&domain.Inbox{}).Where("id = ?", f.inbox.ID).Updates(map[string]any{"hmac_token": "server-only-hmac-secret", "hmac_mandatory": true}).Error)
	for _, endpoint := range []struct{ method, path string }{{"POST", "/contact"}, {"PATCH", "/contact"}, {"POST", "/set_user"}} {
		for _, proof := range []string{"", "invalid"} {
			body, err := json.Marshal(map[string]string{"identifier": "verified-external-id", "identifier_hash": proof})
			require.NoError(t, err)
			w := f.request(endpoint.method, endpoint.path, string(body), f.session)
			require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
			var contact domain.Contact
			require.NoError(t, f.db.First(&contact, f.owner.ID).Error)
			require.Equal(t, "owner-identifier", contact.Identifier)
		}
	}
	mac := hmac.New(sha256.New, []byte("server-only-hmac-secret"))
	_, _ = mac.Write([]byte("verified-external-id"))
	body, err := json.Marshal(map[string]string{"identifier": "verified-external-id", "identifier_hash": hex.EncodeToString(mac.Sum(nil))})
	require.NoError(t, err)
	for _, endpoint := range []struct{ method, path string }{{"POST", "/contact"}, {"PATCH", "/contact"}, {"POST", "/set_user"}} {
		w := f.request(endpoint.method, endpoint.path, string(body), f.session)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
}

func TestWidgetIdentityRejectsMixedCaseImportedEmailCollision(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	require.NoError(t, f.db.Model(&domain.Contact{}).Where("id = ?", f.other.ID).Update("email", "Imported.MixedCase@Example.test").Error)
	contacts := repository.NewContactRepository(f.db)
	matched, err := contacts.FindByEmail(f.inbox.AccountID, "  imported.mixedcase@example.test  ")
	require.NoError(t, err)
	require.NotNil(t, matched)
	require.Equal(t, f.other.ID, matched.ID)
	for _, endpoint := range []struct{ method, path string }{
		{"POST", "/contact"}, {"PATCH", "/contact"}, {"POST", "/set_user"},
	} {
		w := f.request(endpoint.method, endpoint.path, `{"identifier":"new-identifier","email":"  imported.mixedcase@example.test  ","name":"forged"}`, f.session)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	}
	var owner, other domain.Contact
	require.NoError(t, f.db.First(&owner, f.owner.ID).Error)
	require.NoError(t, f.db.First(&other, f.other.ID).Error)
	require.Equal(t, "Owner", owner.Name)
	require.Equal(t, "owner@example.test", owner.Email)
	require.Equal(t, "Imported.MixedCase@Example.test", other.Email)
}
