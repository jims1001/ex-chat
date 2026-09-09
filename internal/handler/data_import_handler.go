package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type CreateDataImportReq struct {
	SourceProvider string `json:"source_provider"` // csv, json
	FileFormat     string `json:"file_format"`
	ImportType     string `json:"import_type"` // contacts, conversations, messages, attachments
	RawData        string `json:"raw_data"`
	TotalRecords   int    `json:"total_records"`
	AutoStart      *bool  `json:"auto_start"` // default true for backward compatibility
	Action         string `json:"action"`     // "stage" or "execute"
}

// CreateDataImport creates a new data import task with staging or immediate execution
func (h *AuthEnterpriseHandler) CreateDataImport(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req CreateDataImportReq
	_ = c.ShouldBindJSON(&req)

	rawData := req.RawData
	if rawData == "" {
		if file, err := c.FormFile("file"); err == nil {
			if f, err := file.Open(); err == nil {
				defer f.Close()
				content, _ := io.ReadAll(f)
				rawData = string(content)
			}
		}
	}

	provider := req.SourceProvider
	if provider == "" && req.FileFormat != "" {
		provider = req.FileFormat
	}
	if provider == "" {
		provider = "csv"
	}
	importType := req.ImportType
	if importType == "" {
		importType = "contacts"
	}

	trimmed := strings.TrimSpace(rawData)
	// Check for invalid JSON syntax early
	if trimmed != "" && (provider == "json" || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{")) {
		var js any
		if err := json.Unmarshal([]byte(trimmed), &js); err != nil {
			imp := &domain.DataImport{
				AccountID:        uint(accountID),
				SourceProvider:   provider,
				ImportType:       importType,
				RawData:          rawData,
				Status:           "failed",
				TotalRecords:     0,
				ProcessedRecords: 0,
				ErrorsJSON:       fmt.Sprintf(`[{"error": "Invalid JSON: %s"}]`, err.Error()),
			}
			_ = h.repo.CreateDataImport(imp)
			response.BadRequest(c, "Invalid JSON data: "+err.Error())
			return
		}
	}

	autoStart := true
	if req.AutoStart != nil {
		autoStart = *req.AutoStart
	}
	if req.Action == "stage" {
		autoStart = false
	}

	// Direct count completion when no raw data is provided but total_records is set
	if autoStart && trimmed == "" && req.TotalRecords > 0 {
		imp := &domain.DataImport{
			AccountID:        uint(accountID),
			SourceProvider:   provider,
			ImportType:       importType,
			Status:           "completed",
			TotalRecords:     req.TotalRecords,
			ProcessedRecords: req.TotalRecords,
		}
		_ = h.repo.CreateDataImport(imp)
		response.Created(c, imp)
		return
	}

	if h.dataImportService == nil {
		h.dataImportService = service.NewDataImportService(h.repo.GetDB())
	}

	imp := &domain.DataImport{
		AccountID:      uint(accountID),
		SourceProvider: provider,
		ImportType:     importType,
		RawData:        rawData,
		Status:         "staged",
		TotalRecords:   req.TotalRecords,
	}

	// Prevalidate raw data if provided
	if trimmed != "" {
		valRes, _ := h.dataImportService.Prevalidate(uint(accountID), importType, provider, rawData)
		if valRes != nil {
			b, _ := json.Marshal(valRes)
			imp.ValidationJSON = string(b)
			imp.TotalRecords = valRes.TotalRows
			if valRes.Valid {
				imp.Status = "validated"
			}
		}
	}

	_ = h.repo.CreateDataImport(imp)

	logger.WithComponent("data_import").Info("data import task registered",
		"import_id", imp.ID,
		"account_id", accountID,
		"status", imp.Status,
		"auto_start", autoStart,
	)

	// If staged only, return immediately
	if !autoStart {
		response.Created(c, imp)
		return
	}

	// Immediate execution (AutoStart = true)
	_ = h.dataImportService.ExecuteImport(c.Request.Context(), uint(accountID), imp)
	_ = h.repo.UpdateDataImport(imp)
	response.Created(c, imp)
}

// PrevalidateDataImport performs dry-run validation on import data
func (h *AuthEnterpriseHandler) PrevalidateDataImport(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	idParam := c.Param("id")
	rawData := ""
	provider := "csv"
	importType := "contacts"

	if idParam != "" {
		// Prevalidate existing task
		id, err := strconv.ParseUint(idParam, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid import ID")
			return
		}
		imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
		if err != nil || imp == nil {
			response.NotFound(c, "Import task not found")
			return
		}
		rawData = imp.RawData
		provider = imp.SourceProvider
		importType = imp.ImportType

		if h.dataImportService == nil {
			h.dataImportService = service.NewDataImportService(h.repo.GetDB())
		}
		valRes, err := h.dataImportService.Prevalidate(uint(accountID), importType, provider, rawData)
		if err != nil {
			response.InternalError(c, err.Error())
			return
		}

		b, _ := json.Marshal(valRes)
		imp.ValidationJSON = string(b)
		imp.TotalRecords = valRes.TotalRows
		if valRes.Valid {
			imp.Status = "validated"
		}
		_ = h.repo.UpdateDataImport(imp)

		response.Success(c, valRes)
		return
	}

	// Prevalidate directly from request payload or form file
	var req CreateDataImportReq
	_ = c.ShouldBindJSON(&req)
	rawData = req.RawData
	if req.SourceProvider != "" {
		provider = req.SourceProvider
	}
	if req.ImportType != "" {
		importType = req.ImportType
	}

	if rawData == "" {
		if file, err := c.FormFile("file"); err == nil {
			if f, err := file.Open(); err == nil {
				defer f.Close()
				content, _ := io.ReadAll(f)
				rawData = string(content)
			}
		}
	}

	if h.dataImportService == nil {
		h.dataImportService = service.NewDataImportService(h.repo.GetDB())
	}

	valRes, err := h.dataImportService.Prevalidate(uint(accountID), importType, provider, rawData)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, valRes)
}

// StartDataImport starts execution for an existing staged or validated task
func (h *AuthEnterpriseHandler) StartDataImport(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	if imp.Status == "completed" {
		response.BadRequest(c, "Import task has already completed")
		return
	}
	if imp.Status == "discarded" || imp.Status == "cancelled" {
		response.BadRequest(c, "Import task has been discarded/cancelled")
		return
	}

	if h.dataImportService == nil {
		h.dataImportService = service.NewDataImportService(h.repo.GetDB())
	}

	if err := h.dataImportService.ExecuteImport(c.Request.Context(), uint(accountID), imp); err != nil {
		response.InternalError(c, "Failed to execute import: "+err.Error())
		return
	}

	_ = h.repo.UpdateDataImport(imp)
	response.Success(c, imp)
}

// CancelDataImport cancels or discards an import task
func (h *AuthEnterpriseHandler) CancelDataImport(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	if imp.Status == "completed" {
		response.BadRequest(c, "Cannot cancel or discard a completed import task")
		return
	}

	imp.Status = "discarded"
	_ = h.repo.UpdateDataImport(imp)

	logger.WithComponent("data_import").Info("data import task discarded",
		"import_id", imp.ID,
		"account_id", accountID,
	)

	response.Success(c, gin.H{"status": "discarded", "id": id})
}

// DiscardDataImport is an alias for CancelDataImport
func (h *AuthEnterpriseHandler) DiscardDataImport(c *gin.Context) {
	h.CancelDataImport(c)
}

// GetDataImport retrieves single import task details
func (h *AuthEnterpriseHandler) GetDataImport(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	response.Success(c, imp)
}

// ListDataImports lists all data imports for an account
func (h *AuthEnterpriseHandler) ListDataImports(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	imports, err := h.repo.ListDataImports(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, imports)
}

// GetImportErrors returns error records and skipped records in JSON format
func (h *AuthEnterpriseHandler) GetImportErrors(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	var errorsList []any
	if imp.ErrorsJSON != "" && imp.ErrorsJSON != "[]" {
		_ = json.Unmarshal([]byte(imp.ErrorsJSON), &errorsList)
	}

	var skippedList []any
	if imp.SkippedJSON != "" && imp.SkippedJSON != "[]" {
		_ = json.Unmarshal([]byte(imp.SkippedJSON), &skippedList)
	}

	response.Success(c, gin.H{
		"import_id":      imp.ID,
		"failed_count":   imp.FailedRecords,
		"skipped_count":  imp.SkippedRecords,
		"errors":         errorsList,
		"skipped":        skippedList,
	})
}

// DownloadImportErrors streams CSV download of failed rows
func (h *AuthEnterpriseHandler) DownloadImportErrors(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	if h.dataImportService == nil {
		h.dataImportService = service.NewDataImportService(h.repo.GetDB())
	}

	csvBytes, err := h.dataImportService.GenerateErrorsCSV(imp)
	if err != nil {
		response.InternalError(c, "Failed to generate errors CSV: "+err.Error())
		return
	}

	filename := fmt.Sprintf("import_%d_errors.csv", imp.ID)
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Data(http.StatusOK, "text/csv", csvBytes)
}

// GetImportSkipped returns skipped records details in JSON format
func (h *AuthEnterpriseHandler) GetImportSkipped(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	var skippedList []any
	if imp.SkippedJSON != "" && imp.SkippedJSON != "[]" {
		_ = json.Unmarshal([]byte(imp.SkippedJSON), &skippedList)
	}

	response.Success(c, gin.H{
		"import_id":     imp.ID,
		"skipped_count": imp.SkippedRecords,
		"skipped":       skippedList,
	})
}

// DownloadSkippedRecords streams CSV download of skipped records
func (h *AuthEnterpriseHandler) DownloadSkippedRecords(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid import ID")
		return
	}

	imp, err := h.repo.GetDataImport(uint(accountID), uint(id))
	if err != nil || imp == nil {
		response.NotFound(c, "Import task not found")
		return
	}

	if h.dataImportService == nil {
		h.dataImportService = service.NewDataImportService(h.repo.GetDB())
	}

	csvBytes, err := h.dataImportService.GenerateSkippedCSV(imp)
	if err != nil {
		response.InternalError(c, "Failed to generate skipped CSV: "+err.Error())
		return
	}

	filename := fmt.Sprintf("import_%d_skipped.csv", imp.ID)
	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Data(http.StatusOK, "text/csv", csvBytes)
}
