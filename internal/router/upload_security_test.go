package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/uploadpath"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type uploadFixture struct {
	t                                 *testing.T
	db                                *gorm.DB
	cfg                               *config.Config
	r                                 *gin.Engine
	account                           domain.Account
	inbox                             domain.Inbox
	owner, other                      domain.Contact
	user                              domain.User
	session, otherSession, agentToken string
}

func newUploadFixture(t *testing.T) *uploadFixture {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	t.Chdir(t.TempDir())
	f := &uploadFixture{t: t, cfg: &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "private-upload-security-test-secret"}}
	var err error
	f.db, err = database.InitDB(f.cfg)
	require.NoError(t, err)
	sqlDB, err := f.db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	f.account = domain.Account{Name: "Upload account"}
	require.NoError(t, f.db.Create(&f.account).Error)
	f.inbox = domain.Inbox{AccountID: f.account.ID, Name: "Upload inbox", WebsiteToken: "upload-widget", ChannelType: domain.ChannelWebWidget}
	require.NoError(t, f.db.Create(&f.inbox).Error)
	f.owner = domain.Contact{AccountID: f.account.ID, Name: "Owner"}
	require.NoError(t, f.db.Create(&f.owner).Error)
	f.other = domain.Contact{AccountID: f.account.ID, Name: "Attacker"}
	require.NoError(t, f.db.Create(&f.other).Error)
	require.NoError(t, f.db.Create(&domain.ContactInbox{InboxID: f.inbox.ID, ContactID: f.owner.ID, SourceID: "owner-source"}).Error)
	require.NoError(t, f.db.Create(&domain.ContactInbox{InboxID: f.inbox.ID, ContactID: f.other.ID, SourceID: "other-source"}).Error)
	f.session, err = auth.GenerateVisitorToken(f.inbox.ID, f.owner.ID, "owner-source", f.cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	f.otherSession, err = auth.GenerateVisitorToken(f.inbox.ID, f.other.ID, "other-source", f.cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	f.user = domain.User{Name: "Agent", Email: "agent@example.test", PasswordHash: "unused", Role: domain.RoleAdministrator}
	require.NoError(t, f.db.Create(&f.user).Error)
	require.NoError(t, f.db.Create(&domain.AccountUser{AccountID: f.account.ID, UserID: f.user.ID, Role: domain.RoleAdministrator}).Error)
	f.agentToken, err = auth.GenerateToken(&f.user, f.cfg.JWTSecret, 1)
	require.NoError(t, err)
	f.r = SetupRouter(f.cfg, f.db, ws.NewHub())
	return f
}

func (f *uploadFixture) request(method, path, body, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}
func (f *uploadFixture) download(path, session string) *httptest.ResponseRecorder {
	f.t.Helper()
	if session != "" {
		path += "?visitor_session=" + url.QueryEscape(session)
	}
	return f.request(http.MethodGet, path, "", "")
}
func (f *uploadFixture) file(key, body string) string {
	f.t.Helper()
	dest := filepath.Join("uploads", filepath.FromSlash(key))
	require.NoError(f.t, os.MkdirAll(filepath.Dir(dest), 0700))
	require.NoError(f.t, os.WriteFile(dest, []byte(body), 0600))
	return uploadpath.URL(key)
}
func (f *uploadFixture) message(contactID uint, private bool) (domain.Conversation, domain.Message) {
	f.t.Helper()
	conv := domain.Conversation{AccountID: f.account.ID, InboxID: f.inbox.ID, ContactID: contactID, Status: domain.ConversationStatusOpen}
	require.NoError(f.t, f.db.Create(&conv).Error)
	msg := domain.Message{AccountID: f.account.ID, ConversationID: conv.ID, SenderType: domain.SenderTypeUser, SenderID: f.user.ID, Content: "Attachment", Private: private}
	require.NoError(f.t, f.db.Create(&msg).Error)
	return conv, msg
}
func (f *uploadFixture) attach(conv domain.Conversation, msg domain.Message, path string) *httptest.ResponseRecorder {
	f.t.Helper()
	body, err := json.Marshal(map[string]any{"message_id": msg.ID, "file_type": "text/plain", "data_url": path, "file_size": 4})
	require.NoError(f.t, err)
	return f.request(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", f.account.ID, conv.ID), string(body), f.agentToken)
}

func TestPrivateUploadDirectLifecycleAndOwnership(t *testing.T) {
	f := newUploadFixture(t)
	negotiate := f.request(http.MethodPost, "/api/v1/widget/direct_uploads?website_token=upload-widget&visitor_session="+f.session, `{"blob":{"filename":"proof%20 space.txt","content_type":"text/plain","byte_size":4}}`, "")
	require.Equal(t, http.StatusOK, negotiate.Code, negotiate.Body.String())
	var offer struct {
		Key    string `json:"key"`
		Direct struct {
			URL string `json:"url"`
		} `json:"direct_upload"`
	}
	require.NoError(t, json.Unmarshal(negotiate.Body.Bytes(), &offer))
	uploaded := f.request(http.MethodPut, offer.Direct.URL, "data", "")
	require.Equal(t, http.StatusOK, uploaded.Code, uploaded.Body.String())
	var result struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(uploaded.Body.Bytes(), &result))
	require.Contains(t, result.URL, fmt.Sprintf("/widget/inbox_%d/contact_%d/", f.inbox.ID, f.owner.ID))
	require.Equal(t, http.StatusUnauthorized, f.download(result.URL, "").Code)
	reqWithHeader := httptest.NewRequest(http.MethodGet, result.URL, nil)
	reqWithHeader.Header.Set("X-Visitor-Session", f.session)
	withHeader := httptest.NewRecorder()
	f.r.ServeHTTP(withHeader, reqWithHeader)
	require.Equal(t, http.StatusOK, withHeader.Code, withHeader.Body.String())
	owner := f.download(result.URL, f.session)
	require.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
	require.Equal(t, "data", owner.Body.String())
	require.Equal(t, http.StatusForbidden, f.download(result.URL, f.otherSession).Code, "known filename must not grant another contact access")
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, result.URL+"?test_direct_access=true", "", "").Code)
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, result.URL+"?visitor_session=invalid&visitor_token="+f.session, "", "").Code)
	uploadURL, err := url.Parse(offer.Direct.URL)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, f.download(result.URL, uploadURL.Query().Get("visitor_token")).Code, "upload capability is not a download session")
	ownerConv, ownerMsg := f.message(f.owner.ID, false)
	bound := f.attach(ownerConv, ownerMsg, result.URL)
	require.Equal(t, http.StatusCreated, bound.Code, bound.Body.String())
	otherConv, otherMsg := f.message(f.other.ID, false)
	denied := f.attach(otherConv, otherMsg, result.URL)
	require.Equal(t, http.StatusForbidden, denied.Code, denied.Body.String())
	var count int64
	require.NoError(t, f.db.Model(&domain.Attachment{}).Where("message_id = ?", otherMsg.ID).Count(&count).Error)
	require.Zero(t, count)
	// Reconstruct all handlers/repositories: ownership survives process state.
	f.r = SetupRouter(f.cfg, f.db, ws.NewHub())
	require.Equal(t, http.StatusOK, f.download(result.URL, f.session).Code)
	require.Equal(t, http.StatusForbidden, f.download(result.URL, f.otherSession).Code)
	require.Equal(t, http.StatusOK, f.request(http.MethodGet, result.URL, "", f.agentToken).Code)
	require.Equal(t, http.StatusConflict, f.request(http.MethodPut, offer.Direct.URL, "more", "").Code)
	// A removed/changed mapping immediately invalidates the visitor's access.
	require.NoError(t, f.db.Model(&domain.ContactInbox{}).Where("contact_id = ? AND inbox_id = ?", f.owner.ID, f.inbox.ID).Update("source_id", "new-source").Error)
	require.Equal(t, http.StatusForbidden, f.download(result.URL, f.session).Code)
}

func TestPrivateUploadMultipartNamespaceAndEscaping(t *testing.T) {
	f := newUploadFixture(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "literal%20 space.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("hello"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/direct_uploads?website_token=upload-widget&visitor_session="+f.session, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Contains(t, result.URL, "%2520%20space.txt")
	owner := f.download(result.URL, f.session)
	require.Equal(t, http.StatusOK, owner.Code, owner.Body.String())
	require.Equal(t, "hello", owner.Body.String())
	require.Equal(t, http.StatusForbidden, f.download(result.URL, f.otherSession).Code)
}

func TestPrivateUploadLegacyMetadataAndPrivateMessages(t *testing.T) {
	f := newUploadFixture(t)
	conv, msg := f.message(f.owner.ID, false)
	key := fmt.Sprintf("widget/inbox_%d/old.txt", f.inbox.ID)
	path := f.file(key, "legacy")
	require.Equal(t, http.StatusForbidden, f.download(path, f.session).Code, "inbox alone is not proof")
	require.NoError(t, f.db.Create(&domain.Attachment{AccountID: f.account.ID, MessageID: msg.ID, DataURL: path, FileType: "text/plain"}).Error)
	require.Equal(t, http.StatusOK, f.download(path, f.session).Code)
	require.Equal(t, http.StatusForbidden, f.download(path, f.otherSession).Code)
	legacySpaceKey := fmt.Sprintf("widget/inbox_%d/old space.txt", f.inbox.ID)
	legacySpacePath := f.file(legacySpaceKey, "old space")
	require.NoError(t, f.db.Create(&domain.Attachment{AccountID: f.account.ID, MessageID: msg.ID, DataURL: "/uploads/" + legacySpaceKey}).Error)
	require.Equal(t, http.StatusOK, f.download(legacySpacePath, f.session).Code, "legacy raw spaces retain exact owner metadata")
	require.Equal(t, http.StatusForbidden, f.download(legacySpacePath, f.otherSession).Code)
	encodedSpaceKey := fmt.Sprintf("widget/inbox_%d/encoded space.txt", f.inbox.ID)
	encodedSpacePath := f.file(encodedSpaceKey, "space file")
	require.NoError(t, f.db.Create(&domain.Attachment{AccountID: f.account.ID, MessageID: msg.ID, DataURL: encodedSpacePath}).Error)
	literalPercentPath := f.file(strings.Replace(encodedSpaceKey, " ", "%20", 1), "different literal percent file")
	require.Equal(t, http.StatusOK, f.download(encodedSpacePath, f.session).Code)
	require.Equal(t, http.StatusForbidden, f.download(literalPercentPath, f.session).Code, "encoded metadata cannot authorize a different literal-percent file")
	accountPath := f.file(fmt.Sprintf("account_%d/conv_%d/agent.txt", f.account.ID, conv.ID), "agent reply")
	bound := f.attach(conv, msg, accountPath)
	require.Equal(t, http.StatusCreated, bound.Code, bound.Body.String())
	require.Equal(t, http.StatusOK, f.download(accountPath, f.session).Code)
	require.Equal(t, http.StatusForbidden, f.download(accountPath, f.otherSession).Code)
	// A same basename in a different namespace never inherits this metadata.
	otherPath := f.file(fmt.Sprintf("account_%d/other/agent.txt", f.account.ID), "different")
	require.Equal(t, http.StatusForbidden, f.download(otherPath, f.session).Code)
	require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", msg.ID).Update("private", true).Error)
	require.Equal(t, http.StatusForbidden, f.download(path, f.session).Code)
	require.Equal(t, http.StatusForbidden, f.download(accountPath, f.session).Code)
	require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", msg.ID).Updates(map[string]any{"private": false, "deleted": true}).Error)
	require.Equal(t, http.StatusForbidden, f.download(path, f.session).Code)
	require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", msg.ID).Update("deleted", false).Error)
	_, otherMsg := f.message(f.other.ID, false)
	require.NoError(t, f.db.Create(&domain.Attachment{AccountID: f.account.ID, MessageID: otherMsg.ID, DataURL: path}).Error)
	require.Equal(t, http.StatusForbidden, f.download(path, f.session).Code, "ambiguous historical ownership fails closed")
	require.Equal(t, http.StatusForbidden, f.download(path, f.otherSession).Code)
}

func TestPrivateUploadCredentialAndTenantBoundaries(t *testing.T) {
	f := newUploadFixture(t)
	key, err := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, "known.txt")
	require.NoError(t, err)
	path := f.file(key, "private")
	foreign := domain.Account{Name: "Foreign"}
	require.NoError(t, f.db.Create(&foreign).Error)
	foreignInbox := domain.Inbox{AccountID: foreign.ID, Name: "Foreign", WebsiteToken: "foreign-widget"}
	require.NoError(t, f.db.Create(&foreignInbox).Error)
	foreignContact := domain.Contact{AccountID: foreign.ID, Name: "Foreign visitor"}
	require.NoError(t, f.db.Create(&foreignContact).Error)
	require.NoError(t, f.db.Create(&domain.ContactInbox{InboxID: foreignInbox.ID, ContactID: foreignContact.ID, SourceID: "owner-source"}).Error)
	foreignSession, err := auth.GenerateVisitorToken(foreignInbox.ID, foreignContact.ID, "owner-source", f.cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, f.download(path, foreignSession).Code)
	sameAccountInbox := domain.Inbox{AccountID: f.account.ID, Name: "Other inbox", WebsiteToken: "other-widget"}
	require.NoError(t, f.db.Create(&sameAccountInbox).Error)
	require.NoError(t, f.db.Create(&domain.ContactInbox{InboxID: sameAccountInbox.ID, ContactID: f.owner.ID, SourceID: "owner-source"}).Error)
	otherInboxSession, err := auth.GenerateVisitorToken(sameAccountInbox.ID, f.owner.ID, "owner-source", f.cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, f.download(path, otherInboxSession).Code)
	// A valid JWT with invented contact/source binding is still rejected.
	mismatch, err := auth.GenerateVisitorToken(f.inbox.ID, f.other.ID, "owner-source", f.cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, f.download(path, mismatch).Code)
	claims, err := auth.ParseVisitorToken(f.session, f.cfg.JWTSecret)
	require.NoError(t, err)
	claims.IssuedAt = jwt.NewNumericDate(time.Now().Add(-2 * time.Hour))
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(f.cfg.JWTSecret))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, f.download(path, expired).Code)
	mfa, err := auth.GenerateMFAToken(&f.user, f.cfg.JWTSecret)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, path, "", mfa).Code)
	require.Equal(t, http.StatusUnauthorized, f.download(path, mfa).Code)
	expiredAgent, err := auth.GenerateToken(&f.user, f.cfg.JWTSecret, -1)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, path, "", expiredAgent).Code)
	foreignUser := domain.User{Name: "Foreign user", Email: "foreign@example.test", PasswordHash: "unused"}
	require.NoError(t, f.db.Create(&foreignUser).Error)
	require.NoError(t, f.db.Create(&domain.AccountUser{UserID: foreignUser.ID, AccountID: foreign.ID, Role: domain.RoleAdministrator}).Error)
	foreignAgent, err := auth.GenerateToken(&foreignUser, f.cfg.JWTSecret, 1)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, path, "", foreignAgent).Code)
	for _, token := range []string{f.agentToken, f.session} {
		require.NoError(t, f.db.Create(&domain.RevokedToken{Token: token, UserID: f.user.ID, ExpiresAt: time.Now().Add(time.Hour)}).Error)
	}
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, path, "", f.agentToken).Code)
	require.Equal(t, http.StatusUnauthorized, f.download(path, f.session).Code)
	// Fail closed when revocation storage cannot be read.
	require.NoError(t, f.db.Migrator().DropTable(&domain.RevokedToken{}))
	require.Equal(t, http.StatusServiceUnavailable, f.request(http.MethodGet, path, "", f.agentToken).Code)
}

func TestPrivateUploadSignaturesAndFilesystemBoundaries(t *testing.T) {
	f := newUploadFixture(t)
	key, err := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, "normal.txt")
	require.NoError(t, err)
	path := f.file(key, "secret")
	storage := service.NewLocalStorageService("uploads", f.cfg.JWTSecret)
	signed, err := storage.GetSignedURL(context.Background(), key, time.Minute)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, f.request(http.MethodGet, signed, "", "").Code)
	parsed, err := url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, path+"?expires=1&sig=wrong", "", f.agentToken).Code, "invalid signature must not downgrade to valid agent auth")
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, path+"?expires=bad", "", "").Code)
	otherKey, _ := uploadpath.WidgetKey(f.inbox.ID, f.other.ID, "other.txt")
	otherPath := f.file(otherKey, "other")
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, otherPath+"?"+parsed.RawQuery, "", "").Code)
	// An attacker cannot move a signed URL to the similarly named nested key.
	aliasPath := f.file("uploads/"+key, "aliased secret")
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, aliasPath+"?"+parsed.RawQuery, "", "").Code)
	for _, bad := range []string{"/uploads/../outside.txt", "/uploads/widget/../" + key, "/uploads/" + strings.Replace(key, "/", "//", 1), "/uploads/" + strings.Replace(key, "/", "%5c", 1)} {
		req := httptest.NewRequest(http.MethodGet, bad+"?visitor_session="+f.session, nil)
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusOK, w.Code, bad)
	}
	realFile := filepath.Join("uploads", filepath.FromSlash(key))
	linkKey, _ := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, "link.txt")
	require.NoError(t, os.Symlink(filepath.Base(realFile), filepath.Join("uploads", filepath.FromSlash(linkKey))))
	require.Equal(t, http.StatusNotFound, f.download(uploadpath.URL(linkKey), f.session).Code)
	// A signed URL also cannot override filesystem link safety.
	linkSigned, err := storage.GetSignedURL(context.Background(), linkKey, time.Minute)
	require.NoError(t, err)
	require.Equal(t, http.StatusNotFound, f.request(http.MethodGet, linkSigned, "", "").Code)
	hardKey, _ := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, "hard.txt")
	require.NoError(t, os.Link(realFile, filepath.Join("uploads", filepath.FromSlash(hardKey))))
	require.Equal(t, http.StatusNotFound, f.download(uploadpath.URL(hardKey), f.session).Code)
	require.Equal(t, http.StatusNotFound, f.download(path, f.session).Code, "both hardlink names must fail closed")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0600))
	outsideKey, _ := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, "outside.txt")
	require.NoError(t, os.Symlink(outside, filepath.Join("uploads", filepath.FromSlash(outsideKey))))
	require.Equal(t, http.StatusNotFound, f.download(uploadpath.URL(outsideKey), f.session).Code)
	for label, target := range map[string]string{"foreign-hard.txt": filepath.Join("uploads", filepath.FromSlash(otherKey)), "outside-hard.txt": outside} {
		hardKey, err := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, label)
		require.NoError(t, err)
		require.NoError(t, os.Link(target, filepath.Join("uploads", filepath.FromSlash(hardKey))))
		require.Equal(t, http.StatusNotFound, f.download(uploadpath.URL(hardKey), f.session).Code)
	}
	// Replace a valid namespace directory with a symlink to another contact.
	ownerDir := filepath.Join("uploads", "widget", fmt.Sprintf("inbox_%d", f.inbox.ID), fmt.Sprintf("contact_%d", f.owner.ID))
	require.NoError(t, os.Rename(ownerDir, ownerDir+"-saved"))
	require.NoError(t, os.Symlink(fmt.Sprintf("contact_%d", f.other.ID), ownerDir))
	ownerAlias, _ := uploadpath.WidgetKey(f.inbox.ID, f.owner.ID, "other.txt")
	require.Equal(t, http.StatusNotFound, f.download(uploadpath.URL(ownerAlias), f.session).Code)
}

func TestPrivateUploadAgentNamespaceAndDeletion(t *testing.T) {
	f := newUploadFixture(t)
	validPath := f.file(fmt.Sprintf("account_%d/normal.txt", f.account.ID), "owned")
	require.Equal(t, http.StatusOK, f.request(http.MethodGet, validPath, "", f.agentToken).Code)
	for _, key := range []string{
		fmt.Sprintf("account_%dextra/known.txt", f.account.ID),
		fmt.Sprintf("account_0%d/known.txt", f.account.ID),
		fmt.Sprintf("widget/inbox_%dextra/known.txt", f.inbox.ID),
		fmt.Sprintf("widget/inbox_%d/contact_%dextra/known.txt", f.inbox.ID, f.owner.ID),
		fmt.Sprintf("account_%d/known.txt", f.account.ID+100),
		"recordings/known.txt",
	} {
		path := f.file(key, "denied")
		require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, path, "", f.agentToken).Code, key)
	}
	require.NoError(t, f.db.Where("id = ?", f.user.ID).Delete(&domain.User{}).Error)
	require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, validPath, "", f.agentToken).Code)
}
