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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const publicVisitorTestSecret = "public-visitor-boundary-test-secret"

type publicVisitorFixture struct {
	db                                                   *gorm.DB
	r                                                    *gin.Engine
	h                                                    *PublicHandler
	inbox, otherInbox, foreignInbox                      domain.Inbox
	owner, other, foreign                                domain.Contact
	conversation, otherConversation, foreignConversation domain.Conversation
	otherInboxConversation                               domain.Conversation
	session, otherSession, otherInboxSession             string
	foreignSession, wrongMappingSession, missingMapping  string
	uploadToken                                          string
}

func newPublicVisitorFixture(t *testing.T) *publicVisitorFixture {
	t.Helper()
	db, err := database.InitDB(&config.Config{DBDriver: "sqlite", DBPath: ":memory:"})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	f := &publicVisitorFixture{db: db, r: gin.New()}
	account := domain.Account{Name: "Public visitor account"}
	foreignAccount := domain.Account{Name: "Foreign visitor account"}
	require.NoError(t, db.Create(&account).Error)
	require.NoError(t, db.Create(&foreignAccount).Error)
	f.inbox = domain.Inbox{AccountID: account.ID, Name: "Public inbox", WebsiteToken: "public-security-inbox"}
	f.otherInbox = domain.Inbox{AccountID: account.ID, Name: "Other inbox", WebsiteToken: "public-security-other-inbox"}
	f.foreignInbox = domain.Inbox{AccountID: foreignAccount.ID, Name: "Foreign inbox", WebsiteToken: "public-security-foreign-inbox"}
	for _, inbox := range []*domain.Inbox{&f.inbox, &f.otherInbox, &f.foreignInbox} {
		require.NoError(t, db.Create(inbox).Error)
	}
	f.owner = domain.Contact{AccountID: account.ID, Name: "Public owner", Email: "public-owner@example.test", Identifier: "public-owner-identifier", PubsubToken: "public-owner-legacy-secret"}
	f.other = domain.Contact{AccountID: account.ID, Name: "Other owner", Email: "public-other@example.test", Identifier: "public-other-identifier"}
	f.foreign = domain.Contact{AccountID: foreignAccount.ID, Name: "Foreign owner", Email: "public-foreign@example.test"}
	for _, contact := range []*domain.Contact{&f.owner, &f.other, &f.foreign} {
		require.NoError(t, db.Create(contact).Error)
	}
	for _, mapping := range []domain.ContactInbox{
		{InboxID: f.inbox.ID, ContactID: f.owner.ID, SourceID: "public-owner-source"},
		{InboxID: f.inbox.ID, ContactID: f.other.ID, SourceID: "public-other-source"},
		{InboxID: f.otherInbox.ID, ContactID: f.owner.ID, SourceID: "public-owner-source"},
		{InboxID: f.foreignInbox.ID, ContactID: f.foreign.ID, SourceID: "public-owner-source"},
	} {
		require.NoError(t, db.Create(&mapping).Error)
	}
	for _, target := range []struct {
		token  *string
		inbox  uint
		owner  uint
		source string
	}{
		{&f.session, f.inbox.ID, f.owner.ID, "public-owner-source"},
		{&f.otherSession, f.inbox.ID, f.other.ID, "public-other-source"},
		{&f.otherInboxSession, f.otherInbox.ID, f.owner.ID, "public-owner-source"},
		{&f.foreignSession, f.foreignInbox.ID, f.foreign.ID, "public-owner-source"},
		{&f.wrongMappingSession, f.inbox.ID, f.owner.ID, "public-other-source"},
		{&f.missingMapping, f.inbox.ID, f.owner.ID, "unmapped-public-source"},
	} {
		*target.token, err = auth.GenerateVisitorToken(target.inbox, target.owner, target.source, publicVisitorTestSecret, time.Hour)
		require.NoError(t, err)
	}
	f.uploadToken, err = auth.GenerateVisitorUploadToken(f.inbox.ID, f.owner.ID, "public-owner-source", "blobs/public.txt", publicVisitorTestSecret, 1024, time.Minute)
	require.NoError(t, err)
	f.conversation = domain.Conversation{AccountID: account.ID, InboxID: f.inbox.ID, ContactID: f.owner.ID, Status: domain.ConversationStatusOpen}
	f.otherConversation = domain.Conversation{AccountID: account.ID, InboxID: f.inbox.ID, ContactID: f.other.ID, Status: domain.ConversationStatusOpen}
	f.otherInboxConversation = domain.Conversation{AccountID: account.ID, InboxID: f.otherInbox.ID, ContactID: f.owner.ID, Status: domain.ConversationStatusOpen}
	f.foreignConversation = domain.Conversation{AccountID: foreignAccount.ID, InboxID: f.foreignInbox.ID, ContactID: f.foreign.ID, Status: domain.ConversationStatusOpen}
	for _, conv := range []*domain.Conversation{&f.conversation, &f.otherConversation, &f.otherInboxConversation, &f.foreignConversation} {
		require.NoError(t, db.Create(conv).Error)
	}
	for _, message := range []domain.Message{
		{AccountID: account.ID, ConversationID: f.conversation.ID, Content: "public-owner-message", SenderType: domain.SenderTypeContact, SenderID: f.owner.ID, MessageType: domain.MessageTypeIncoming},
		{AccountID: account.ID, ConversationID: f.conversation.ID, Content: "private-public-note-never-expose", SenderType: domain.SenderTypeUser, MessageType: domain.MessageTypeOutgoing, Private: true},
	} {
		require.NoError(t, db.Create(&message).Error)
	}
	f.h = NewPublicHandler(repository.NewInboxRepository(db), repository.NewContactRepository(db), repository.NewConversationRepository(db), repository.NewMessageRepository(db))
	f.h.SetJWTSecret(publicVisitorTestSecret)
	base := f.r.Group("/public/api/v1/inboxes/:identifier")
	base.GET("", f.h.GetPublicInbox)
	base.POST("/contacts", f.h.CreateContact)
	base.GET("/contacts/:contact_id", f.h.GetContact)
	base.PATCH("/contacts/:contact_id", f.h.UpdateContact)
	base.PUT("/contacts/:contact_id", f.h.UpdateContact)
	base.GET("/contacts/:contact_id/conversations", f.h.ListConversations)
	base.POST("/contacts/:contact_id/conversations", f.h.CreateConversation)
	base.GET("/contacts/:contact_id/conversations/:conversation_id", f.h.GetConversation)
	base.GET("/contacts/:contact_id/conversations/:conversation_id/messages", f.h.ListMessages)
	base.POST("/contacts/:contact_id/conversations/:conversation_id/messages", f.h.CreateMessage)
	base.POST("/contacts/:contact_id/conversations/:conversation_id/update_last_seen", f.h.UpdateLastSeen)
	base.POST("/contacts/:contact_id/conversations/:conversation_id/toggle_status", f.h.ToggleConversationStatus)
	base.POST("/contacts/:contact_id/conversations/:conversation_id/toggle_typing", f.h.ToggleTyping)
	base.PATCH("/contacts/:contact_id/conversations/:conversation_id/custom_attributes", f.h.UpdateConversationCustomAttributes)
	return f
}

func (f *publicVisitorFixture) request(method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Visitor-Session", token)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func (f *publicVisitorFixture) contactPath() string {
	return fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d", f.inbox.WebsiteToken, f.owner.ID)
}

type publicVisitorEndpoint struct {
	method, suffix, body string
	status               int
}

func publicVisitorEndpoints(conversationID uint) []publicVisitorEndpoint {
	conv := fmt.Sprintf("/conversations/%d", conversationID)
	return []publicVisitorEndpoint{
		{http.MethodGet, "", "", http.StatusOK},
		{http.MethodPatch, "", `{"name":"Updated public owner"}`, http.StatusOK},
		{http.MethodPut, "", `{"name":"Updated public owner"}`, http.StatusOK},
		{http.MethodGet, "/conversations", "", http.StatusOK},
		{http.MethodPost, "/conversations", `{}`, http.StatusCreated},
		{http.MethodGet, conv, "", http.StatusOK},
		{http.MethodGet, conv + "/messages", "", http.StatusOK},
		{http.MethodPost, conv + "/messages", `{"content":"Authorized public message","echo_id":"public-owner-echo"}`, http.StatusCreated},
		{http.MethodPost, conv + "/update_last_seen", `{}`, http.StatusOK},
		{http.MethodPost, conv + "/toggle_status", `{"status":"resolved"}`, http.StatusOK},
		{http.MethodPost, conv + "/toggle_typing", `{"typing_status":"on"}`, http.StatusOK},
		{http.MethodPatch, conv + "/custom_attributes", `{"custom_attributes":{"public_test":true}}`, http.StatusOK},
	}
}

func TestPublicVisitorAllResourceRoutesRequireSession(t *testing.T) {
	f := newPublicVisitorFixture(t)
	originalMode := gin.Mode()
	t.Cleanup(func() { gin.SetMode(originalMode) })
	for _, mode := range []string{gin.TestMode, gin.DebugMode, gin.ReleaseMode} {
		gin.SetMode(mode)
		for _, endpoint := range publicVisitorEndpoints(f.conversation.ID) {
			for _, credential := range []struct{ name, token string }{
				{"missing", ""}, {"invalid", "invalid"}, {"raw source", "public-owner-source"},
				{"legacy pubsub", f.owner.PubsubToken}, {"cross-contact", f.otherSession},
				{"cross-inbox", f.otherInboxSession}, {"cross-tenant", f.foreignSession},
				{"wrong mapping", f.wrongMappingSession}, {"missing mapping", f.missingMapping},
				{"upload purpose", f.uploadToken},
			} {
				w := f.request(endpoint.method, f.contactPath()+endpoint.suffix+"?source_id=public-owner-source&contact_token=public-owner-source", endpoint.body, credential.token)
				require.Equal(t, http.StatusForbidden, w.Code, "%s %s %s %s: %s", mode, endpoint.method, endpoint.suffix, credential.name, w.Body.String())
				require.NotContains(t, w.Body.String(), f.owner.Email)
				require.NotContains(t, w.Body.String(), "public-owner-message")
			}
		}
	}
	var owner domain.Contact
	require.NoError(t, f.db.First(&owner, f.owner.ID).Error)
	require.Equal(t, f.owner.Name, owner.Name)
	var conversation domain.Conversation
	require.NoError(t, f.db.First(&conversation, f.conversation.ID).Error)
	require.Equal(t, domain.ConversationStatusOpen, conversation.Status)
	require.Equal(t, f.conversation.ContactLastSeenAt, conversation.ContactLastSeenAt)
	require.Equal(t, f.conversation.CustomAttributes, conversation.CustomAttributes)
	var messages, conversations int64
	require.NoError(t, f.db.Model(&domain.Message{}).Count(&messages).Error)
	require.NoError(t, f.db.Model(&domain.Conversation{}).Count(&conversations).Error)
	require.EqualValues(t, 2, messages)
	require.EqualValues(t, 4, conversations)
}

func TestPublicVisitorOwnerAccessAndResourceBoundaries(t *testing.T) {
	f := newPublicVisitorFixture(t)
	for _, endpoint := range publicVisitorEndpoints(f.conversation.ID) {
		w := f.request(endpoint.method, f.contactPath()+endpoint.suffix, endpoint.body, f.session)
		require.Equal(t, endpoint.status, w.Code, "%s %s: %s", endpoint.method, endpoint.suffix, w.Body.String())
		require.NotContains(t, w.Body.String(), "private-public-note-never-expose")
	}
	// Contact path guesses remain forbidden even with a valid different owner.
	for _, routeContact := range []string{fmt.Sprint(f.other.ID), "public-other-source", f.owner.Identifier, "0", "999999"} {
		path := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%s", f.inbox.WebsiteToken, routeContact)
		w := f.request(http.MethodGet, path, "", f.session)
		require.Equal(t, http.StatusForbidden, w.Code, routeContact)
	}
	// Exact session-owned source URLs continue to work, but a source is not a credential.
	sourcePath := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/public-owner-source", f.inbox.WebsiteToken)
	require.Equal(t, http.StatusOK, f.request(http.MethodGet, sourcePath, "", f.session).Code)
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, sourcePath, "", "").Code)
	for _, conv := range []domain.Conversation{f.otherConversation, f.otherInboxConversation, f.foreignConversation} {
		for _, endpoint := range publicVisitorEndpoints(conv.ID)[5:] {
			w := f.request(endpoint.method, f.contactPath()+endpoint.suffix, endpoint.body, f.session)
			require.Equal(t, http.StatusNotFound, w.Code, "%s %s: %s", endpoint.method, endpoint.suffix, w.Body.String())
		}
	}
	// Invalid explicit query credentials cannot downgrade to a valid header session.
	for _, query := range []string{"visitor_session=invalid", "visitor_session=", "visitor_token=invalid", "visitor_token="} {
		require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, f.contactPath()+"?"+query, "", f.session).Code)
	}
	// A same-source mapping in another inbox does not extend the session's authority.
	otherInboxPath := strings.Replace(f.contactPath(), f.inbox.WebsiteToken, f.otherInbox.WebsiteToken, 1)
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, otherInboxPath, "", f.session).Code)
	// Mapping corruption across tenant ownership must also fail closed.
	require.NoError(t, f.db.Model(&domain.ContactInbox{}).Where("inbox_id = ? AND contact_id = ?", f.inbox.ID, f.owner.ID).Update("contact_id", f.foreign.ID).Error)
	crossTenantMapping, err := auth.GenerateVisitorToken(f.inbox.ID, f.foreign.ID, "public-owner-source", publicVisitorTestSecret, time.Hour)
	require.NoError(t, err)
	foreignPath := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d", f.inbox.WebsiteToken, f.foreign.ID)
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, foreignPath, "", crossTenantMapping).Code)
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, f.contactPath(), "", f.session).Code)
}

func TestPublicVisitorBootstrapCannotClaimExistingIdentity(t *testing.T) {
	f := newPublicVisitorFixture(t)
	path := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts", f.inbox.WebsiteToken)
	body := fmt.Sprintf(`{"id":%d,"contact_id":%d,"name":"Claimed owner","identifier":%q,"email":%q,"source_id":"public-owner-source","phone_number":"123","custom_attributes":{"claimed":true}}`, f.owner.ID, f.owner.ID, f.owner.Identifier, f.owner.Email)
	var created []uint
	for i := 0; i < 2; i++ {
		w := f.request(http.MethodPost, path+"?source_id=public-owner-source", body, "")
		require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
		var payload struct {
			Data struct {
				domain.Contact
				SourceID     string `json:"source_id"`
				VisitorToken string `json:"visitor_token"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
		require.NotEqual(t, f.owner.ID, payload.Data.ID)
		require.NotContains(t, created, payload.Data.ID)
		created = append(created, payload.Data.ID)
		require.Empty(t, payload.Data.Email)
		require.Empty(t, payload.Data.Identifier)
		require.Empty(t, payload.Data.PhoneNumber)
		require.Empty(t, payload.Data.CustomAttributes)
		require.Len(t, payload.Data.SourceID, 64)
		require.NotEqual(t, "public-owner-source", payload.Data.SourceID)
		claims, err := auth.ParseVisitorToken(payload.Data.VisitorToken, publicVisitorTestSecret)
		require.NoError(t, err)
		require.Equal(t, payload.Data.ID, claims.ContactID)
		require.Equal(t, f.inbox.ID, claims.InboxID)
		require.Equal(t, payload.Data.SourceID, claims.SourceID)
		ownPath := fmt.Sprintf("%s/%d", path, payload.Data.ID)
		require.Equal(t, http.StatusOK, f.request(http.MethodGet, ownPath, "", payload.Data.VisitorToken).Code)
		require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, f.contactPath(), "", payload.Data.VisitorToken).Code)
	}
	// Resuming a session cannot be redirected by guessed identity fields.
	w := f.request(http.MethodPost, path, `{"identifier":"public-other-identifier","email":"public-other@example.test","name":"Injected"}`, f.session)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var resumed struct {
		Data domain.Contact `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resumed))
	require.Equal(t, f.owner.ID, resumed.Data.ID)
	require.Equal(t, f.owner.Name, resumed.Data.Name)
	for _, token := range []string{"invalid", f.uploadToken, f.otherInboxSession, f.foreignSession, f.wrongMappingSession, f.missingMapping} {
		require.Equal(t, http.StatusForbidden, f.request(http.MethodPost, path, `{}`, token).Code)
	}
	require.Equal(t, http.StatusForbidden, f.request(http.MethodPost, path+"?visitor_session=", `{}`, "").Code)
	var contacts, mappings int64
	require.NoError(t, f.db.Model(&domain.Contact{}).Count(&contacts).Error)
	require.NoError(t, f.db.Model(&domain.ContactInbox{}).Count(&mappings).Error)
	require.EqualValues(t, 5, contacts)
	require.EqualValues(t, 6, mappings)
	var existing domain.Contact
	require.NoError(t, f.db.First(&existing, f.owner.ID).Error)
	require.Equal(t, f.owner.Name, existing.Name)
	require.Equal(t, f.owner.Email, existing.Email)
	require.Equal(t, f.owner.Identifier, existing.Identifier)
}

func TestPublicVisitorProfileUpdatesCannotClaimOtherContacts(t *testing.T) {
	f := newPublicVisitorFixture(t)
	for _, body := range []string{
		`{"identifier":"public-other-identifier","name":"Injected"}`,
		`{"email":" PUBLIC-OTHER@EXAMPLE.TEST ","name":"Injected"}`,
	} {
		w := f.request(http.MethodPatch, f.contactPath(), body, f.session)
		require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
	}
	var owner domain.Contact
	require.NoError(t, f.db.First(&owner, f.owner.ID).Error)
	require.Equal(t, f.owner.Name, owner.Name)
	require.Equal(t, f.owner.Email, owner.Email)
	require.Equal(t, f.owner.Identifier, owner.Identifier)
	w := f.request(http.MethodPatch, f.contactPath(), `{"name":"Legitimate owner","identifier":"new-public-owner-identifier","email":" NEW-PUBLIC-OWNER@EXAMPLE.TEST ","custom_attributes":{"language":"en"}}`, f.session)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.NoError(t, f.db.First(&owner, f.owner.ID).Error)
	require.Equal(t, "Legitimate owner", owner.Name)
	require.Equal(t, "new-public-owner@example.test", owner.Email)
	require.Equal(t, "new-public-owner-identifier", owner.Identifier)
	// A public informational inbox lookup remains usable without a session.
	w = f.request(http.MethodGet, "/public/api/v1/inboxes/"+f.inbox.WebsiteToken, "", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NotContains(t, w.Body.String(), "pubsub_token")
}

func TestPublicVisitorUnconfiguredSecretFailsClosed(t *testing.T) {
	f := newPublicVisitorFixture(t)
	f.h.SetJWTSecret("")
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, f.contactPath(), "", f.session).Code)
	path := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts", f.inbox.WebsiteToken)
	require.Equal(t, http.StatusInternalServerError, f.request(http.MethodPost, path, `{}`, "").Code)
	var contacts int64
	require.NoError(t, f.db.Model(&domain.Contact{}).Count(&contacts).Error)
	require.EqualValues(t, 3, contacts)
}

func TestPublicVisitorIdentifierHMACIsRequired(t *testing.T) {
	f := newPublicVisitorFixture(t)
	require.NoError(t, f.db.Model(&domain.Inbox{}).Where("id = ?", f.inbox.ID).Updates(map[string]any{"hmac_mandatory": true, "hmac_token": "public-hmac-test-key"}).Error)
	for _, body := range []string{`{"identifier":"verified-owner"}`, `{"identifier":"verified-owner","identifier_hash":"invalid"}`} {
		w := f.request(http.MethodPatch, f.contactPath(), body, f.session)
		require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	}
	mac := hmac.New(sha256.New, []byte("public-hmac-test-key"))
	_, _ = mac.Write([]byte("verified-owner"))
	body := fmt.Sprintf(`{"identifier":"verified-owner","identifier_hash":%q}`, hex.EncodeToString(mac.Sum(nil)))
	require.Equal(t, http.StatusOK, f.request(http.MethodPatch, f.contactPath(), body, f.session).Code)
}

type publicCSATSecurityFixture struct {
	*publicVisitorFixture
	csat                                *CSATHandler
	iam                                 repository.ChannelIAMRepository
	survey                              domain.CSATSurvey
	agent                               domain.User
	outsider                            domain.User
	agentToken, outsiderToken, mfaToken string
}

func newPublicCSATSecurityFixture(t *testing.T) *publicCSATSecurityFixture {
	t.Helper()
	f := &publicCSATSecurityFixture{publicVisitorFixture: newPublicVisitorFixture(t)}
	f.iam = repository.NewChannelAuthEnterpriseRepository(f.db)
	f.csat = NewCSATHandler(repository.NewCSATExtensionRepository(f.db), repository.NewMessageRepository(f.db), repository.NewConversationRepository(f.db), repository.NewInboxRepository(f.db), repository.NewContactRepository(f.db), repository.NewAccountRepository(f.db))
	f.csat.SetJWTSecret(publicVisitorTestSecret)
	f.csat.SetAuthRepositories(repository.NewUserRepository(f.db), f.iam)
	f.survey = domain.CSATSurvey{AccountID: f.inbox.AccountID, ConversationID: f.conversation.ID, Rating: 4, FeedbackText: "Original owner feedback"}
	require.NoError(t, f.db.Create(&f.survey).Error)
	f.agent = domain.User{Name: "CSAT member", Email: "csat-agent@example.test", PasswordHash: "test", Role: domain.RoleAgent}
	f.outsider = domain.User{Name: "Foreign member", Email: "csat-outsider@example.test", PasswordHash: "test", Role: domain.RoleAgent}
	require.NoError(t, f.db.Create(&f.agent).Error)
	require.NoError(t, f.db.Create(&f.outsider).Error)
	for _, membership := range []domain.AccountUser{
		{AccountID: f.inbox.AccountID, UserID: f.agent.ID, Role: domain.RoleAgent},
		{AccountID: f.foreignInbox.AccountID, UserID: f.outsider.ID, Role: domain.RoleAgent},
	} {
		require.NoError(t, f.db.Create(&membership).Error)
	}
	var err error
	f.agentToken, err = auth.GenerateToken(&f.agent, publicVisitorTestSecret, 1)
	require.NoError(t, err)
	f.outsiderToken, err = auth.GenerateToken(&f.outsider, publicVisitorTestSecret, 1)
	require.NoError(t, err)
	f.mfaToken, err = auth.GenerateMFAToken(&f.agent, publicVisitorTestSecret)
	require.NoError(t, err)
	for _, base := range []string{"/public/api/v1/csat_survey", "/public/api/v1/csat_surveys", "/csat_survey", "/survey/responses"} {
		f.r.GET(base+"/:id", f.csat.GetPublicCSATSurvey)
		f.r.PATCH(base+"/:id", f.csat.UpdatePublicCSATSurvey)
		f.r.PUT(base+"/:id", f.csat.UpdatePublicCSATSurvey)
		if base != "/survey/responses" {
			f.r.POST(base+"/:id", f.csat.UpdatePublicCSATSurvey)
		}
	}
	return f
}

func (f *publicCSATSecurityFixture) requestCSAT(method, path, query, header, token string) *httptest.ResponseRecorder {
	if query != "" {
		path += "?" + query
	}
	req := httptest.NewRequest(method, path, strings.NewReader(`{"rating":5,"feedback_message":"Updated owner feedback"}`))
	req.Header.Set("Content-Type", "application/json")
	if header != "" {
		req.Header.Set(header, token)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func TestPublicCSATVisitorAliasesEnforceSignedLiveOwnership(t *testing.T) {
	f := newPublicCSATSecurityFixture(t)
	for _, base := range []string{"/public/api/v1/csat_survey", "/public/api/v1/csat_surveys", "/csat_survey", "/survey/responses"} {
		path := fmt.Sprintf("%s/%d", base, f.conversation.ID)
		for _, method := range []string{http.MethodGet, http.MethodPatch} {
			for _, credential := range []string{"", "invalid", "public-owner-source", f.owner.PubsubToken, f.inbox.WebsiteToken, f.otherSession, f.otherInboxSession, f.foreignSession, f.wrongMappingSession, f.missingMapping, f.uploadToken} {
				w := f.requestCSAT(method, path, "token="+credential, "", "")
				require.Equal(t, http.StatusForbidden, w.Code, "%s %s: %s", method, path, w.Body.String())
				require.NotContains(t, w.Body.String(), "Original owner feedback")
			}
		}
		for _, alias := range []string{"visitor_session", "visitor_token", "token", "contact_token", "uuid"} {
			w := f.requestCSAT(http.MethodGet, path, alias+"="+f.session, "", "")
			require.Equal(t, http.StatusOK, w.Code, "%s %s: %s", path, alias, w.Body.String())
			require.NotContains(t, w.Body.String(), f.owner.Email)
			require.NotContains(t, w.Body.String(), "public-owner-message")
		}
		for _, header := range []string{"X-Visitor-Session", "X-Visitor-Token", "X-Contact-Token", "X-Auth-Token"} {
			w := f.requestCSAT(http.MethodGet, path, "", header, f.session)
			require.Equal(t, http.StatusOK, w.Code, "%s %s: %s", path, header, w.Body.String())
		}
	}
	var unchanged domain.CSATSurvey
	require.NoError(t, f.db.First(&unchanged, f.survey.ID).Error)
	require.Equal(t, 4, unchanged.Rating)
	require.Equal(t, f.survey.FeedbackText, unchanged.FeedbackText)
	w := f.requestCSAT(http.MethodPatch, fmt.Sprintf("/csat_survey/%d", f.conversation.ID), "", "X-Visitor-Session", f.session)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	// Deleting a live mapping immediately invalidates an otherwise current session.
	require.NoError(t, f.db.Where("inbox_id = ? AND contact_id = ?", f.inbox.ID, f.owner.ID).Delete(&domain.ContactInbox{}).Error)
	require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, fmt.Sprintf("/csat_survey/%d", f.conversation.ID), "token="+f.session, "", "").Code)
}

func TestPublicCSATCapabilitiesAreScopedAndCannotBypassExplicitCredentials(t *testing.T) {
	f := newPublicCSATSecurityFixture(t)
	for _, base := range []string{"/public/api/v1/csat_survey", "/public/api/v1/csat_surveys", "/csat_survey", "/survey/responses"} {
		numeric := fmt.Sprintf("%s/%d", base, f.conversation.ID)
		capability := base + "/" + f.conversation.UUID
		for _, method := range []string{http.MethodGet, http.MethodPatch, http.MethodPut} {
			require.Equal(t, http.StatusOK, f.requestCSAT(method, capability, "", "", "").Code)
			require.Equal(t, http.StatusOK, f.requestCSAT(method, numeric, "token="+f.conversation.UUID, "", "").Code)
			require.Equal(t, http.StatusForbidden, f.requestCSAT(method, numeric, "token="+f.otherConversation.UUID, "", "").Code)
		}
		for _, query := range []string{"visitor_session=invalid", "visitor_session=", "visitor_token=invalid", "token=invalid", "contact_token=public-owner-source", "uuid=" + f.otherConversation.UUID, "visitor_session=" + f.session + "&visitor_token=invalid"} {
			require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, capability, query, "", "").Code, query)
		}
		require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, capability, "", "Authorization", "Bearer invalid").Code)
	}
	// A CSAT capability is never a general visitor or contact session.
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, f.contactPath(), "", f.conversation.UUID).Code)
	for _, bad := range []string{"", "00000000-0000-0000-0000-000000000000", "owner-known-public-identifier-123456", "6ba7b810-9dad-11d1-80b4-00c04fd430c8"} {
		require.False(t, publicCSATCapability(bad, &domain.Conversation{UUID: bad}), bad)
	}
}

func TestPublicCSATRevocationAndLiveAgentChecks(t *testing.T) {
	f := newPublicCSATSecurityFixture(t)
	path := fmt.Sprintf("/csat_survey/%d", f.conversation.ID)
	capability := "/survey/responses/" + f.conversation.UUID
	require.Equal(t, http.StatusOK, f.requestCSAT(http.MethodGet, path, "", "Authorization", "Bearer "+f.agentToken).Code)
	for _, token := range []string{f.outsiderToken, f.mfaToken, f.session, "invalid"} {
		require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, path, "", "Authorization", "Bearer "+token).Code)
	}
	require.NoError(t, f.iam.RevokeToken(f.session, 0, time.Now().Add(time.Hour)))
	require.NoError(t, f.iam.RevokeToken(f.agentToken, f.agent.ID, time.Now().Add(time.Hour)))
	for _, base := range []string{"/public/api/v1/csat_survey", "/public/api/v1/csat_surveys", "/csat_survey", "/survey/responses"} {
		for _, id := range []string{fmt.Sprint(f.conversation.ID), f.conversation.UUID} {
			for _, alias := range []string{"visitor_session", "visitor_token", "token", "contact_token", "uuid"} {
				require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, base+"/"+id, alias+"="+f.session, "", "").Code)
			}
			for _, header := range []string{"X-Visitor-Session", "X-Visitor-Token", "X-Contact-Token", "X-Auth-Token"} {
				require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, base+"/"+id, "", header, f.session).Code)
			}
			require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, base+"/"+id, "", "Authorization", "Bearer "+f.agentToken).Code)
		}
	}
	freshSession, err := auth.GenerateVisitorToken(f.inbox.ID, f.owner.ID, "public-owner-source", publicVisitorTestSecret, time.Hour)
	require.NoError(t, err)
	for _, query := range []string{
		"visitor_session=" + freshSession + "&visitor_token=" + f.session,
		"visitor_session=" + freshSession + "&visitor_session=" + f.session,
		"visitor_session=" + freshSession + "&token=" + f.session,
	} {
		require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, capability, query, "", "").Code)
	}
	freshAgent, err := auth.GenerateToken(&f.agent, publicVisitorTestSecret, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.requestCSAT(http.MethodGet, path, "", "Authorization", "Bearer "+freshAgent).Code)
	require.NoError(t, f.db.Where("account_id = ? AND user_id = ?", f.inbox.AccountID, f.agent.ID).Delete(&domain.AccountUser{}).Error)
	require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, path, "", "Authorization", "Bearer "+freshAgent).Code)
	require.NoError(t, f.db.Create(&domain.AccountUser{AccountID: f.inbox.AccountID, UserID: f.agent.ID, Role: domain.RoleAgent}).Error)
	require.NoError(t, f.db.Delete(&domain.User{}, f.agent.ID).Error)
	require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, path, "", "Authorization", "Bearer "+freshAgent).Code)
	// Revocation-store outages fail closed; the independent survey capability remains scoped.
	require.NoError(t, f.db.Migrator().DropTable(&domain.RevokedToken{}))
	require.Equal(t, http.StatusForbidden, f.requestCSAT(http.MethodGet, path, "token="+freshSession, "", "").Code)
	require.Equal(t, http.StatusOK, f.requestCSAT(http.MethodGet, capability, "", "", "").Code)
}
