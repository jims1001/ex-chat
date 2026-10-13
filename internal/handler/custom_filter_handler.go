package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RawMessage preserves exact JSON numbers before the legacy string DTO is built.
type CreateCustomFilterReq struct {
	Name       string          `json:"name"`
	FilterType string          `json:"filter_type"`
	Query      json.RawMessage `json:"query"`
}

type UpdateCustomFilterReq = CreateCustomFilterReq

// Membership is established by the existing Auth/Tenant middleware. This check
// fails closed if a route is miswired or its authenticated context contradicts
// the URL; input/header/query ownership fields never establish identity.
func customFilterScope(c *gin.Context) (uint, uint, bool) {
	userID := c.GetUint(middleware.ContextUserID)
	rawUser, _ := c.Get(middleware.ContextUser)
	user, ok := rawUser.(*domain.User)
	if userID == 0 || !ok || user == nil || user.ID != userID {
		response.Unauthorized(c, "Authenticated user required")
		return 0, 0, false
	}
	account64, err := strconv.ParseUint(c.Param("account_id"), 10, strconv.IntSize)
	if err != nil || account64 == 0 || strconv.FormatUint(account64, 10) != c.Param("account_id") {
		response.BadRequest(c, "Invalid account ID")
		return 0, 0, false
	}
	accountID := uint(account64)
	rawAccount, _ := c.Get(middleware.ContextAccount)
	account, accountOK := rawAccount.(*domain.Account)
	rawMember, _ := c.Get(middleware.ContextAccountMembership)
	member, memberOK := rawMember.(*domain.AccountUser)
	if c.GetUint(middleware.ContextAccountID) != accountID || !accountOK || account == nil || account.ID != accountID || !memberOK || member == nil || member.AccountID != accountID || member.UserID != userID {
		response.Forbidden(c, "Account membership required")
		return 0, 0, false
	}
	return accountID, userID, true
}

func customFilterID(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, strconv.IntSize)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != c.Param("id") {
		response.NotFound(c, "Custom filter not found")
		return 0, false
	}
	return uint(id), true
}

func decodeCustomFilter(c *gin.Context, request *CreateCustomFilterReq) bool {
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(request); err != nil {
		response.BadRequest(c, "Invalid custom filter payload")
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		response.BadRequest(c, "Invalid custom filter payload")
		return false
	}
	return true
}

func parseCustomFilterQuery(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		var query string
		if json.Unmarshal(raw, &query) != nil {
			return "", false
		}
		query = strings.TrimSpace(query)
		return query, query != ""
	}
	if raw[0] != '{' {
		return "", false
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return "", false
	}
	return compact.String(), true
}

func validCustomFilterType(kind string) bool {
	return kind == "conversation" || kind == "contact" || kind == "report"
}

func customFilterFailure(c *gin.Context, err error, operation string, accountID, userID, id uint) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.NotFound(c, "Custom filter not found")
	case errors.Is(err, repository.ErrCustomFilterVersioned):
		response.Error(c, http.StatusConflict, "Custom filter requires the versioned SRH command")
	default:
		// No SQL/provider error or private name/query is safe to echo or log.
		logger.WithComponent("custom_filter").Error("custom filter storage failed", "operation", operation, "account_id", accountID, "user_id", userID, "filter_id", id)
		response.InternalError(c, "Custom filter storage unavailable")
	}
}

func (h *AdvancedHandler) CreateCustomFilter(c *gin.Context) {
	accountID, userID, ok := customFilterScope(c)
	if !ok {
		return
	}
	var request CreateCustomFilterReq
	if !decodeCustomFilter(c, &request) {
		return
	}
	name, kind := strings.TrimSpace(request.Name), strings.TrimSpace(request.FilterType)
	query, valid := parseCustomFilterQuery(request.Query)
	if name == "" || utf8.RuneCountInString(name) > 255 || !validCustomFilterType(kind) || !valid {
		response.BadRequest(c, "Valid name, filter_type and query are required")
		return
	}
	filter := domain.CustomFilter{Name: name, FilterType: kind, Query: query}
	if err := h.customFilterRepo.CreateLegacy(c.Request.Context(), accountID, userID, &filter); err != nil {
		customFilterFailure(c, err, "create", accountID, userID, 0)
		return
	}
	response.Created(c, filter)
}

func (h *AdvancedHandler) ListCustomFilters(c *gin.Context) {
	accountID, userID, ok := customFilterScope(c)
	if !ok {
		return
	}
	filters, err := h.customFilterRepo.List(c.Request.Context(), accountID, userID, strings.TrimSpace(c.Query("filter_type")))
	if err != nil {
		customFilterFailure(c, err, "list", accountID, userID, 0)
		return
	}
	response.Success(c, filters)
}

func (h *AdvancedHandler) GetCustomFilter(c *gin.Context) {
	accountID, userID, ok := customFilterScope(c)
	if !ok {
		return
	}
	id, ok := customFilterID(c)
	if !ok {
		return
	}
	filter, err := h.customFilterRepo.Find(c.Request.Context(), accountID, userID, id)
	if err != nil {
		customFilterFailure(c, err, "get", accountID, userID, id)
		return
	}
	response.Success(c, filter)
}

func (h *AdvancedHandler) UpdateCustomFilter(c *gin.Context) {
	accountID, userID, ok := customFilterScope(c)
	if !ok {
		return
	}
	id, ok := customFilterID(c)
	if !ok {
		return
	}
	var request UpdateCustomFilterReq
	if !decodeCustomFilter(c, &request) {
		return
	}
	patch := repository.CustomFilterPatch{}
	if name := strings.TrimSpace(request.Name); name != "" {
		if utf8.RuneCountInString(name) > 255 {
			response.BadRequest(c, "Invalid custom filter name")
			return
		}
		patch.Name = &name
	}
	if kind := strings.TrimSpace(request.FilterType); kind != "" {
		if !validCustomFilterType(kind) {
			response.BadRequest(c, "Invalid custom filter type")
			return
		}
		patch.FilterType = &kind
	}
	if len(request.Query) != 0 {
		query, valid := parseCustomFilterQuery(request.Query)
		if !valid {
			response.BadRequest(c, "Invalid custom filter query")
			return
		}
		patch.Query = &query
	}
	filter, err := h.customFilterRepo.UpdateLegacy(c.Request.Context(), accountID, userID, id, patch)
	if err != nil {
		customFilterFailure(c, err, "update", accountID, userID, id)
		return
	}
	response.Success(c, filter)
}

func (h *AdvancedHandler) DeleteCustomFilter(c *gin.Context) {
	accountID, userID, ok := customFilterScope(c)
	if !ok {
		return
	}
	id, ok := customFilterID(c)
	if !ok {
		return
	}
	if _, err := h.customFilterRepo.DeleteLegacy(c.Request.Context(), accountID, userID, id); err != nil {
		customFilterFailure(c, err, "delete", accountID, userID, id)
		return
	}
	// Preserve the client contract without revealing another owner's existence.
	response.Success(c, gin.H{"deleted": true})
}
