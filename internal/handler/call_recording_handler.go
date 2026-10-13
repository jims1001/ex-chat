package handler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const maxCallRecordingBytes = 25 << 20

// SetRecordingStorage configures the same local storage root used for uploads.
func (h *ChannelDriverHandler) SetRecordingStorage(storage *service.LocalStorageService) {
	h.recordingStorage = storage
}

// UploadCallRecording accepts actual audio/video bytes or explicit provider URL
// metadata. It never claims that a file exists merely because a name was supplied.
func (h *ChannelDriverHandler) UploadCallRecording(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 32)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	id := c.Param("id")
	var call *domain.Call
	if callID, parseErr := strconv.ParseUint(id, 10, 32); parseErr == nil && callID > 0 {
		call, err = h.repo.GetCall(uint(accountID), uint(callID))
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "Failed to load call")
		return
	}
	if call == nil {
		call, err = h.repo.GetCallByProviderCallID(uint(accountID), id)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "Failed to load call")
		return
	}
	if call == nil || call.AccountID != uint(accountID) {
		response.NotFound(c, "Call not found")
		return
	}
	if h.inboxRepo == nil || h.contactRepo == nil {
		response.InternalError(c, "Call ownership service unavailable")
		return
	}
	inbox, err := h.inboxRepo.FindSummaryByID(uint(accountID), call.InboxID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "Failed to verify call inbox")
		return
	}
	if err != nil || inbox == nil {
		response.NotFound(c, "Call inbox not found")
		return
	}
	contact, err := h.contactRepo.FindSummaryByID(uint(accountID), call.ContactID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		response.InternalError(c, "Failed to verify call contact")
		return
	}
	if err != nil || contact == nil {
		response.NotFound(c, "Call contact not found")
		return
	}
	if call.ConversationID != nil {
		if h.convRepo == nil {
			response.InternalError(c, "Call ownership service unavailable")
			return
		}
		conv, err := h.convRepo.FindSummaryByID(uint(accountID), *call.ConversationID)
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			response.InternalError(c, "Failed to verify call conversation")
			return
		}
		if err != nil || conv == nil || conv.InboxID != call.InboxID || conv.ContactID != call.ContactID {
			response.NotFound(c, "Call conversation not found")
			return
		}
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxCallRecordingBytes+(1<<20))
	var req struct {
		RecordingURL string `json:"recording_url" form:"recording_url"`
		RecordingSID string `json:"recording_sid" form:"recording_sid"`
		Duration     *int   `json:"duration" form:"duration"`
	}
	var data []byte
	var extension string
	multipart := strings.HasPrefix(c.ContentType(), "multipart/form-data")
	if multipart {
		if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
			if c.Request.MultipartForm != nil {
				_ = c.Request.MultipartForm.RemoveAll()
			}
			response.BadRequest(c, "Invalid or oversized recording upload")
			return
		}
		defer c.Request.MultipartForm.RemoveAll()
	}
	if err := c.ShouldBind(&req); err != nil {
		response.BadRequest(c, "Invalid recording metadata")
		return
	}
	if (req.Duration != nil && *req.Duration < 0) || len(req.RecordingSID) > 255 || len(req.RecordingURL) > 1024 {
		response.BadRequest(c, "Invalid recording metadata")
		return
	}
	if multipart {
		files := append(c.Request.MultipartForm.File["recording"], c.Request.MultipartForm.File["file"]...)
		count := 0
		for _, group := range c.Request.MultipartForm.File {
			count += len(group)
		}
		if len(files) != 1 || count != 1 || req.RecordingURL != "" {
			response.BadRequest(c, "Supply one recording file without a recording URL")
			return
		}
		file := files[0]
		if file.Size <= 0 || file.Size > maxCallRecordingBytes {
			response.BadRequest(c, "Recording must be between 1 byte and 25MB")
			return
		}
		f, err := file.Open()
		if err != nil {
			response.BadRequest(c, "Failed to read recording")
			return
		}
		data, err = io.ReadAll(io.LimitReader(f, maxCallRecordingBytes+1))
		closeErr := f.Close()
		if err != nil || closeErr != nil || len(data) == 0 || len(data) > maxCallRecordingBytes {
			response.BadRequest(c, "Invalid recording content")
			return
		}
		// Content type and generated extension come from bytes, never the client name.
		switch http.DetectContentType(data) {
		case "audio/mpeg":
			extension = ".mp3"
		case "audio/wave", "audio/x-wav":
			extension = ".wav"
		case "application/ogg":
			extension = ".ogg"
		case "video/mp4":
			extension = ".mp4"
		case "video/webm":
			extension = ".webm"
		default:
			response.BadRequest(c, "Recording must contain supported audio or video")
			return
		}
	} else {
		parsed, err := url.Parse(req.RecordingURL)
		if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			response.BadRequest(c, "Supply a recording file or an HTTPS provider recording URL")
			return
		}
	}

	updated := *call
	status := "updated"
	if multipart {
		if h.recordingStorage == nil {
			response.InternalError(c, "Recording storage unavailable")
			return
		}
		var random [16]byte
		if _, err := rand.Read(random[:]); err != nil {
			response.InternalError(c, "Failed to prepare recording storage")
			return
		}
		key := fmt.Sprintf("account_%d/recordings/call_%d_%s%s", accountID, call.ID, hex.EncodeToString(random[:]), extension)
		storedURL, finish, err := h.recordingStorage.StageRecording(c.Request.Context(), key, data)
		if err != nil {
			if errors.Is(err, service.ErrMaliciousFileDetected) {
				response.BadRequest(c, "Recording contains prohibited content")
				return
			}
			response.InternalError(c, "Failed to save recording file")
			return
		}
		committed := false
		defer func() {
			if err := finish(committed); err != nil {
				logger.WithComponent("call").Error("recording storage finalization failed", "call_id", call.ID, "error", err)
			}
		}()
		updated.RecordingURL = storedURL
		if req.RecordingSID != "" {
			updated.RecordingSID = req.RecordingSID
		}
		if req.Duration != nil {
			updated.Duration = *req.Duration
		}
		updated.UpdatedAt = time.Now()
		if err := h.repo.UpdateCallRecording(updated.AccountID, updated.ID, updated.RecordingURL, req.RecordingSID, req.Duration); err != nil {
			response.InternalError(c, "Failed to save recording metadata")
			return
		}
		committed = true
		status = "uploaded"
	} else {
		updated.RecordingURL = req.RecordingURL
		if req.RecordingSID != "" {
			updated.RecordingSID = req.RecordingSID
		}
		if req.Duration != nil {
			updated.Duration = *req.Duration
		}
		updated.UpdatedAt = time.Now()
		if err := h.repo.UpdateCallRecording(updated.AccountID, updated.ID, updated.RecordingURL, req.RecordingSID, req.Duration); err != nil {
			response.InternalError(c, "Failed to save recording metadata")
			return
		}
	}
	response.Success(c, gin.H{"status": status, "call_id": updated.ID, "recording_url": updated.RecordingURL, "recording_sid": updated.RecordingSID, "duration": updated.Duration})
}
