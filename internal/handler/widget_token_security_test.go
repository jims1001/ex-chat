package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
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
)

func TestWidgetSessionAndUploadCapabilities(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.ReleaseMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	t.Chdir(t.TempDir())
	const secret = "widget-capability-security-test-key"
	db, err := database.InitDB(&config.Config{DBDriver: "sqlite", DBPath: ":memory:"})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	account := domain.Account{Name: "Capability test"}
	require.NoError(t, db.Create(&account).Error)
	inbox := domain.Inbox{AccountID: account.ID, Name: "Widget", WebsiteToken: "capability-widget"}
	require.NoError(t, db.Create(&inbox).Error)
	contacts := repository.NewContactRepository(db)
	inboxes := repository.NewInboxRepository(db)
	h := NewWidgetHandler(repository.NewWidgetRepository(db), nil, contacts, nil, inboxes, nil)
	h.SetJWTSecret(secret)
	bootstrap := NewInboxHandler(inboxes, nil)
	bootstrap.contactRepo = contacts
	bootstrap.SetJWTSecret(secret)
	r := gin.New()
	r.POST("/config", bootstrap.WidgetConfigCreate)
	r.POST("/api/v1/widget/direct_uploads", h.DirectUpload)
	r.PUT("/api/v1/widget/direct_uploads/*key", h.DirectUploadWithKey)
	r.GET("/contact", h.GetContact)
	r.PATCH("/contact", h.UpdateContact)
	r.POST("/events", h.RecordEvent)
	r.POST("/labels", h.AddLabels)
	r.DELETE("/labels", h.RemoveLabels)
	r.PATCH("/attributes", h.MergeContactCustomAttributes)
	r.PATCH("/conversations/:id/attributes", h.MergeConversationCustomAttributes)
	r.PATCH("/messages/:id", h.UpdateMessage)
	r.DELETE("/attributes", h.DestroyContactCustomAttributes)
	r.POST("/set_user", h.SetUser)
	request := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Bootstrap creates and signs a new server-chosen anonymous identity;
	// caller source and identifier hints cannot claim existing contacts.
	w := request(http.MethodPost, "/config?website_token=capability-widget", `{"source_id":"unrelated","contact":{"source_id":"source-owner","identifier":"different-identifier","name":"Visitor"}}`, nil)
	require.Equal(t, http.StatusCreated, w.Code)
	var configResponse struct {
		VisitorToken string `json:"visitor_token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &configResponse))
	session := configResponse.VisitorToken
	claims, err := auth.ParseVisitorToken(session, secret)
	require.NoError(t, err)
	require.NotEqual(t, "source-owner", claims.SourceID)
	require.Len(t, claims.SourceID, 64)
	contact, err := contacts.FindContactBySourceID(inbox.ID, claims.SourceID)
	require.NoError(t, err)
	require.NotNil(t, contact)
	require.Equal(t, contact.ID, claims.ContactID)

	base := "/api/v1/widget/direct_uploads?website_token=capability-widget"
	blob := `{"blob":{"filename":"proof%20 space.txt","content_type":"text/plain","byte_size":4}}`
	negotiate := func(token string) (string, string) {
		t.Helper()
		w := request(http.MethodPost, base+"&visitor_session="+url.QueryEscape(token), blob, nil)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		var result struct {
			Key          string `json:"key"`
			DirectUpload struct {
				URL string `json:"url"`
			} `json:"direct_upload"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result.Key, result.DirectUpload.URL
	}
	key, uploadURL := negotiate(session)
	require.Contains(t, uploadURL, "proof%2520%20space.txt", "literal percent sequences and spaces must be escaped exactly once")
	u, err := url.Parse(uploadURL)
	require.NoError(t, err)
	capability := u.Query().Get("visitor_token")
	capClaims, err := auth.ParseVisitorUploadToken(capability, secret)
	require.NoError(t, err)
	require.Equal(t, key, capClaims.UploadKey)
	require.Equal(t, contact.ID, capClaims.ContactID)

	// Every explicit session is validated before legacy body/query fallback,
	// including mutators that historically preferred raw source identifiers.
	for _, credential := range []string{"invalid", capability} {
		for _, endpoint := range []struct{ method, path, body string }{
			{http.MethodGet, "/contact", ""},
			{http.MethodPatch, "/contact", `{"source_id":"source-owner","name":"forbidden"}`},
			{http.MethodPost, "/events", `{"source_id":"source-owner","name":"page_view"}`},
			{http.MethodPost, "/labels", `{"source_id":"source-owner","labels":["forbidden"]}`},
			{http.MethodDelete, "/labels", `{"source_id":"source-owner","contact_token":"source-owner","labels":["private"]}`},
			{http.MethodPatch, "/attributes", `{"source_id":"source-owner","contact_token":"source-owner","custom_attributes":{"private":"forbidden"}}`},
			{http.MethodPatch, "/conversations/1/attributes", `{"source_id":"source-owner","contact_token":"source-owner","custom_attributes":{"private":"forbidden"}}`},
			{http.MethodPatch, "/messages/1", `{"source_id":"source-owner","contact_token":"source-owner","content":"forbidden","submitted_values":{"private":"forbidden"}}`},
			{http.MethodDelete, "/attributes", `{"custom_attribute_keys":["private"]}`},
			{http.MethodPost, "/set_user", `{"identifier":"source-owner","name":"forbidden"}`},
		} {
			path := endpoint.path + "?website_token=capability-widget&source_id=source-owner&visitor_token=" + url.QueryEscape(credential)
			require.Equal(t, http.StatusForbidden, request(endpoint.method, path, endpoint.body, nil).Code)
		}
	}

	other := domain.Contact{AccountID: account.ID, Name: "Other", Identifier: "other-identifier", Email: "other@example.test"}
	require.NoError(t, db.Create(&other).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{InboxID: inbox.ID, ContactID: other.ID, SourceID: "source-other"}).Error)
	w = request(http.MethodPatch, "/contact?website_token=capability-widget&visitor_token="+session+"&source_id=source-other", `{"source_id":"source-other","contact_token":"source-other","name":"Session owner"}`, nil)
	require.Equal(t, http.StatusOK, w.Code)
	var afterOwner, afterOther domain.Contact
	require.NoError(t, db.First(&afterOwner, contact.ID).Error)
	require.NoError(t, db.First(&afterOther, other.ID).Error)
	require.Equal(t, "Session owner", afterOwner.Name)
	require.Equal(t, "Other", afterOther.Name, "JSON/query source IDs cannot override a signed session")
	for _, body := range []string{
		`{"identifier":"other-identifier","name":"forbidden"}`,
		`{"identifier":"unique-identifier","email":"other@example.test","name":"forbidden"}`,
	} {
		w := request(http.MethodPost, "/set_user?website_token=capability-widget&visitor_token="+session, body, nil)
		require.Equal(t, http.StatusForbidden, w.Code, "set_user must not select a foreign contact by identifier/email")
	}
	w = request(http.MethodPost, "/set_user?website_token=capability-widget&visitor_token="+session, `{"identifier":"new-owner-identifier","email":"owner@example.test","name":"Identified owner","avatar_url":"https://example.test/avatar.png"}`, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	bound, err := contacts.FindContactBySourceID(inbox.ID, claims.SourceID)
	require.NoError(t, err)
	require.Equal(t, claims.ContactID, bound.ID)
	require.Equal(t, "new-owner-identifier", bound.Identifier)
	require.Equal(t, "owner@example.test", bound.Email)
	require.NoError(t, db.First(&afterOther, other.ID).Error)
	require.Equal(t, "Other", afterOther.Name)

	otherAccount := domain.Account{Name: "Foreign tenant"}
	require.NoError(t, db.Create(&otherAccount).Error)
	otherInbox := domain.Inbox{AccountID: otherAccount.ID, Name: "Foreign widget", WebsiteToken: "foreign-widget"}
	require.NoError(t, db.Create(&otherInbox).Error)
	foreignContact := domain.Contact{AccountID: otherAccount.ID, Name: "Foreign visitor"}
	require.NoError(t, db.Create(&foreignContact).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{InboxID: otherInbox.ID, ContactID: foreignContact.ID, SourceID: claims.SourceID}).Error)
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		path := "/contact?website_token=foreign-widget&source_id=source-owner&visitor_token=" + session
		require.Equal(t, http.StatusForbidden, request(method, path, `{"source_id":"source-owner","name":"forbidden"}`, nil).Code, "same-source collision in another inbox must not broaden a signed session")
	}

	for _, mode := range []string{gin.ReleaseMode, gin.TestMode} {
		gin.SetMode(mode)
		for _, query := range []string{"", "&source_id=source-owner", "&visitor_token=invalid&source_id=source-owner", "&visitor_token=" + capability + "&source_id=source-owner", "&visitor_session=invalid&visitor_token=" + session} {
			w := request(http.MethodPost, base+query, blob, nil)
			require.Equal(t, http.StatusForbidden, w.Code, "unsigned, invalid, or upload-only credentials must not negotiate a new upload")
		}
	}
	gin.SetMode(gin.ReleaseMode)
	w = request(http.MethodPost, base, blob, map[string]string{"X-Visitor-Session": session})
	require.Equal(t, http.StatusOK, w.Code, "session header must be supported")

	// A correctly signed session still must match the stored contact/source mapping.
	mismatched, err := auth.GenerateVisitorToken(inbox.ID, contact.ID+100, claims.SourceID, secret, time.Hour)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, request(http.MethodGet, "/contact?website_token=capability-widget&visitor_token="+mismatched, "", nil).Code)
	require.Equal(t, http.StatusForbidden, request(http.MethodPost, base+"&visitor_token="+mismatched, blob, nil).Code)

	for _, test := range []struct {
		name  string
		token string
		key   string
	}{
		{"missing capability", "", key}, {"invalid capability", "invalid", key},
		{"session is not capability", session, key}, {"wrong key", capability, "blobs/other.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := (&url.URL{Path: "/api/v1/widget/direct_uploads/" + test.key, RawQuery: url.Values{"website_token": {"capability-widget"}, "visitor_token": {test.token}}.Encode()}).String()
			require.Equal(t, http.StatusForbidden, request(http.MethodPut, path, "data", nil).Code)
		})
	}
	for _, identity := range []struct {
		inbox, contact uint
		source         string
	}{
		{inbox.ID + 100, contact.ID, claims.SourceID},
		{inbox.ID, contact.ID + 100, claims.SourceID},
		{inbox.ID, contact.ID, "unknown-source"},
	} {
		bad, err := auth.GenerateVisitorUploadToken(identity.inbox, identity.contact, identity.source, key, secret, 4, time.Minute)
		require.NoError(t, err)
		path := (&url.URL{Path: "/api/v1/widget/direct_uploads/" + key, RawQuery: url.Values{"website_token": {"capability-widget"}, "visitor_token": {bad}}.Encode()}).String()
		require.Equal(t, http.StatusForbidden, request(http.MethodPut, path, "data", nil).Code)
	}

	// Valid capability alone uploads once. Untrusted identity hints cannot change it.
	w = request(http.MethodPut, uploadURL+"&source_id=unknown&visitor_session=invalid", "data", nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	file := filepath.Join("uploads", "widget", fmt.Sprintf("inbox_%d", inbox.ID), fmt.Sprintf("contact_%d", contact.ID), filepath.Base(key))
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "data", string(data))
	require.Equal(t, http.StatusConflict, request(http.MethodPut, uploadURL, "new", nil).Code)

	oversizeKey, oversizeURL := negotiate(session)
	require.Equal(t, http.StatusBadRequest, request(http.MethodPut, oversizeURL, "12345", nil).Code)
	_, err = os.Stat(filepath.Join("uploads", "widget", fmt.Sprintf("inbox_%d", inbox.ID), fmt.Sprintf("contact_%d", contact.ID), filepath.Base(oversizeKey)))
	require.True(t, os.IsNotExist(err), "oversized uploads must not leave a stored file")
}

func TestVisitorSourceExtractionFailsClosed(t *testing.T) {
	h := &WidgetHandler{jwtSecret: "visitor-extraction-test-key"}
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, h.extractSourceID(c)) })
	for _, query := range []string{"visitor_token=invalid&source_id=legacy", "visitor_session=invalid&visitor_token=other&source_id=legacy"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		require.Empty(t, w.Body.String(), "invalid explicit tokens must not fall back to an unsigned source")
	}
}
