package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/uploadpath"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type recordingFixture struct {
	h      *ChannelDriverHandler
	db     *gorm.DB
	router *gin.Engine
	call   domain.Call
	root   string
}

func newRecordingFixture(t *testing.T) *recordingFixture {
	t.Helper()
	db, err := database.InitDB(&config.Config{DBDriver: "sqlite", DBPath: ":memory:"})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { sql.Close() })
	account := domain.Account{Name: "Recording test"}
	require.NoError(t, db.Create(&account).Error)
	inbox := domain.Inbox{AccountID: account.ID, Name: "Calls"}
	require.NoError(t, db.Create(&inbox).Error)
	contact := domain.Contact{AccountID: account.ID, Name: "Caller"}
	require.NoError(t, db.Create(&contact).Error)
	conv := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID}
	require.NoError(t, db.Create(&conv).Error)
	call := domain.Call{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, ConversationID: &conv.ID, ProviderCallID: "provider-call", RecordingURL: "https://provider.example/previous.wav"}
	require.NoError(t, db.Create(&call).Error)
	h := NewChannelDriverHandler(repository.NewChannelAuthEnterpriseRepository(db), repository.NewInboxRepository(db), repository.NewContactRepository(db), repository.NewConversationRepository(db), nil)
	root := filepath.Join(t.TempDir(), "uploads")
	h.SetRecordingStorage(service.NewLocalStorageService(root, ""))
	r := gin.New()
	r.POST("/accounts/:account_id/calls/:id/recordings", h.UploadCallRecording)
	return &recordingFixture{h: h, db: db, router: r, call: call, root: root}
}
func (f *recordingFixture) upload(t *testing.T, field, name string, data []byte, values map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if field != "" {
		p, err := w.CreateFormFile(field, name)
		require.NoError(t, err)
		_, err = p.Write(data)
		require.NoError(t, err)
	}
	for key, val := range values {
		require.NoError(t, w.WriteField(key, val))
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/accounts/%d/calls/%d/recordings", f.call.AccountID, f.call.ID), &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	out := httptest.NewRecorder()
	f.router.ServeHTTP(out, req)
	return out
}
func (f *recordingFixture) json(data string, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/accounts/%d/calls/%s/recordings", f.call.AccountID, id), strings.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	f.router.ServeHTTP(out, req)
	return out
}
func (f *recordingFixture) stored(t *testing.T) domain.Call {
	t.Helper()
	var call domain.Call
	require.NoError(t, f.db.First(&call, f.call.ID).Error)
	return call
}
func recordingWAV() []byte {
	return append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 64)...)
}
func recordingFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	require.NoError(t, err)
	return files
}
func TestCallRecordingPersistsBytesAndPreservesPreviousFiles(t *testing.T) {
	f := newRecordingFixture(t)
	wav := recordingWAV()
	out := f.upload(t, "recording", "../../evil.html", wav, map[string]string{"duration": "42", "recording_sid": "provider-recording"})
	require.Equal(t, 200, out.Code, out.Body.String())
	first := f.stored(t)
	require.Equal(t, 42, first.Duration)
	require.Equal(t, "provider-recording", first.RecordingSID)
	key, local, err := uploadpath.LocalURLKey(first.RecordingURL)
	require.NoError(t, err)
	require.True(t, local)
	require.True(t, strings.HasPrefix(key, fmt.Sprintf("account_%d/recordings/call_%d_", f.call.AccountID, f.call.ID)))
	require.True(t, strings.HasSuffix(key, ".wav"))
	actual, err := os.ReadFile(filepath.Join(f.root, key))
	require.NoError(t, err)
	require.Equal(t, wav, actual)
	out = f.upload(t, "file", "same.wav", wav, nil)
	require.Equal(t, 200, out.Code, out.Body.String())
	require.NotEqual(t, first.RecordingURL, f.stored(t).RecordingURL)
	require.Len(t, recordingFiles(t, f.root), 2)
	actual, err = os.ReadFile(filepath.Join(f.root, key))
	require.NoError(t, err)
	require.Equal(t, wav, actual)
}
func TestCallRecordingRejectsInvalidUploadsWithoutMutation(t *testing.T) {
	for _, tc := range []struct {
		name, field string
		data        []byte
		values      map[string]string
	}{
		{name: "empty", field: "recording"}, {name: "text disguised as audio", field: "recording", data: []byte("<html>not audio</html>")},
		{name: "unknown field", field: "attachment", data: recordingWAV()},
		{name: "URL plus file", field: "recording", data: recordingWAV(), values: map[string]string{"recording_url": "https://provider.example/file"}},
		{name: "bad duration", field: "recording", data: recordingWAV(), values: map[string]string{"duration": "bad"}},
		{name: "oversize", field: "recording", data: make([]byte, maxCallRecordingBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecordingFixture(t)
			out := f.upload(t, tc.field, "audio.wav", tc.data, tc.values)
			require.Equal(t, 400, out.Code, out.Body.String())
			require.Equal(t, f.call.RecordingURL, f.stored(t).RecordingURL)
			require.Empty(t, recordingFiles(t, f.root))
		})
	}
}
func TestCallRecordingMetadataDoesNotFabricateUploads(t *testing.T) {
	f := newRecordingFixture(t)
	for _, payload := range []string{`{}`, `{invalid`, `{"recording_url":"/uploads/account_2/private.wav"}`, `{"recording_url":"javascript:alert(1)"}`, `{"recording_url":"https://user:password@example.test/audio"}`, `{"recording_url":"https://provider.example/recording","duration":-1}`} {
		out := f.json(payload, "provider-call")
		require.Equal(t, 400, out.Code, out.Body.String())
		require.Equal(t, f.call.RecordingURL, f.stored(t).RecordingURL)
	}
	out := f.json(`{"recording_url":"https://provider.example/recording.wav","duration":5}`, "provider-call")
	require.Equal(t, 200, out.Code, out.Body.String())
	require.Contains(t, out.Body.String(), `"status":"updated"`)
	require.Equal(t, 5, f.stored(t).Duration)
	require.Empty(t, recordingFiles(t, f.root))
}
func TestCallRecordingRejectsCrossTenantAndMismatchedConversation(t *testing.T) {
	for _, column := range []string{"account_id", "inbox_id", "contact_id", "conversation_id"} {
		t.Run(column, func(t *testing.T) {
			f := newRecordingFixture(t)
			require.NoError(t, f.db.Model(&domain.Call{}).Where("id = ?", f.call.ID).Update(column, 999999).Error)
			out := f.upload(t, "recording", "a.wav", recordingWAV(), nil)
			require.Equal(t, 404, out.Code, out.Body.String())
			require.Empty(t, recordingFiles(t, f.root))
		})
	}
	t.Run("conversation mismatch", func(t *testing.T) {
		f := newRecordingFixture(t)
		require.NoError(t, f.db.Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("inbox_id", 99999).Error)
		out := f.upload(t, "recording", "a.wav", recordingWAV(), nil)
		require.Equal(t, 404, out.Code, out.Body.String())
		require.Empty(t, recordingFiles(t, f.root))
	})
}

type failedRecordingRepo struct {
	repository.ChannelRTMRepository
}

func (r failedRecordingRepo) UpdateCallRecording(uint, uint, string, string, *int) error {
	return errors.New("forced database failure")
}
func TestCallRecordingRollsBackFileOnMetadataFailure(t *testing.T) {
	f := newRecordingFixture(t)
	f.h.repo = failedRecordingRepo{f.h.repo}
	out := f.upload(t, "recording", "a.wav", recordingWAV(), nil)
	require.Equal(t, 500, out.Code, out.Body.String())
	require.Empty(t, recordingFiles(t, f.root))
	require.Equal(t, f.call.RecordingURL, f.stored(t).RecordingURL)
}
func TestCallRecordingFailsOnSymlinkStorage(t *testing.T) {
	f := newRecordingFixture(t)
	target := t.TempDir()
	require.NoError(t, os.Symlink(target, f.root))
	out := f.upload(t, "recording", "a.wav", recordingWAV(), nil)
	require.Equal(t, 500, out.Code, out.Body.String())
	entries, err := os.ReadDir(target)
	require.NoError(t, err)
	require.Empty(t, entries)
	require.Equal(t, f.call.RecordingURL, f.stored(t).RecordingURL)
}
func TestCallRecordingRequestLimit(t *testing.T) {
	f := newRecordingFixture(t)
	payload, _ := json.Marshal(map[string]string{"recording_url": strings.Repeat("x", maxCallRecordingBytes+(2<<20))})
	out := f.json(string(payload), "provider-call")
	require.Equal(t, 400, out.Code, out.Body.String())
	require.Equal(t, f.call.RecordingURL, f.stored(t).RecordingURL)
}

func TestCallRecordingMetadataUpdateIsTenantScopedAndDoesNotResurrect(t *testing.T) {
	f := newRecordingFixture(t)
	require.NoError(t, f.db.Model(&domain.Call{}).Where("id = ?", f.call.ID).Update("status", "completed").Error)
	out := f.upload(t, "recording", "a.wav", recordingWAV(), map[string]string{"duration": "0"})
	require.Equal(t, 200, out.Code, out.Body.String())
	require.Equal(t, "completed", f.stored(t).Status)
	require.ErrorIs(t, f.h.repo.UpdateCallRecording(f.call.AccountID+1, f.call.ID, "https://bad.example/recording", "", nil), gorm.ErrRecordNotFound)
	require.NoError(t, f.db.Delete(&domain.Call{}, f.call.ID).Error)
	require.ErrorIs(t, f.h.repo.UpdateCallRecording(f.call.AccountID, f.call.ID, "https://bad.example/recording", "", nil), gorm.ErrRecordNotFound)
	var count int64
	require.NoError(t, f.db.Model(&domain.Call{}).Where("id = ?", f.call.ID).Count(&count).Error)
	require.Zero(t, count)
}
