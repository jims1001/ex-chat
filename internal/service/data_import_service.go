package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type ValidationErrorItem struct {
	Line       int    `json:"line"`
	Field      string `json:"field,omitempty"`
	Reason     string `json:"reason"`
	RawContent string `json:"raw_content,omitempty"`
}

type ValidationWarningItem struct {
	Line       int    `json:"line"`
	Field      string `json:"field,omitempty"`
	Reason     string `json:"reason"`
	RawContent string `json:"raw_content,omitempty"`
}

type ValidationResult struct {
	Valid       bool                    `json:"valid"`
	TotalRows   int                     `json:"total_rows"`
	ValidRows   int                     `json:"valid_rows"`
	InvalidRows int                     `json:"invalid_rows"`
	SkippedRows int                     `json:"skipped_rows"`
	Headers     []string                `json:"headers,omitempty"`
	PreviewRows []map[string]any        `json:"preview_rows,omitempty"`
	Errors      []ValidationErrorItem   `json:"errors"`
	Warnings    []ValidationWarningItem `json:"warnings"`
}

type ErrorRecord struct {
	Line   int    `json:"line"`
	Data   string `json:"data"`
	Reason string `json:"reason"`
}

type SkippedRecord struct {
	Line   int    `json:"line"`
	Data   string `json:"data"`
	Reason string `json:"reason"`
}

type DataImportService struct {
	db *gorm.DB
}

func NewDataImportService(db *gorm.DB) *DataImportService {
	return &DataImportService{db: db}
}

// Prevalidate performs deep static and relational verification of raw import data
func (s *DataImportService) Prevalidate(accountID uint, importType, provider, rawData string) (*ValidationResult, error) {
	result := &ValidationResult{
		Errors:   make([]ValidationErrorItem, 0),
		Warnings: make([]ValidationWarningItem, 0),
	}

	trimmed := strings.TrimSpace(rawData)
	if trimmed == "" {
		result.Errors = append(result.Errors, ValidationErrorItem{
			Line:   0,
			Reason: "Empty import payload",
		})
		return result, nil
	}

	if provider == "json" || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		return s.prevalidateJSON(accountID, importType, trimmed)
	}

	return s.prevalidateCSV(accountID, importType, trimmed)
}

func isValidEmail(email string) bool {
	if email == "" {
		return false
	}
	if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
		return false
	}
	_, err := mail.ParseAddress(email)
	return err == nil
}

func (s *DataImportService) prevalidateCSV(accountID uint, importType, rawCSV string) (*ValidationResult, error) {
	reader := csv.NewReader(strings.NewReader(rawCSV))
	records, err := reader.ReadAll()
	if err != nil {
		return &ValidationResult{
			Valid: false,
			Errors: []ValidationErrorItem{
				{Line: 1, Reason: fmt.Sprintf("Malformed CSV syntax: %s", err.Error()), RawContent: rawCSV},
			},
		}, nil
	}

	result := &ValidationResult{
		Errors:      make([]ValidationErrorItem, 0),
		Warnings:    make([]ValidationWarningItem, 0),
		PreviewRows: make([]map[string]any, 0),
	}

	if len(records) == 0 {
		result.Errors = append(result.Errors, ValidationErrorItem{Line: 0, Reason: "CSV file contains no rows"})
		return result, nil
	}

	header := records[0]
	result.Headers = header

	colMap := make(map[string]int)
	for idx, col := range header {
		colMap[strings.ToLower(strings.TrimSpace(col))] = idx
	}

	getVal := func(row []string, keys ...string) string {
		for _, k := range keys {
			if idx, ok := colMap[k]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
		}
		return ""
	}

	seenEmailsInBatch := make(map[string]int)
	totalDataRows := len(records) - 1
	result.TotalRows = totalDataRows

	for i := 1; i < len(records); i++ {
		row := records[i]
		rowStr := strings.Join(row, ",")

		// Check completely empty row
		isEmpty := true
		for _, val := range row {
			if strings.TrimSpace(val) != "" {
				isEmpty = false
				break
			}
		}
		if isEmpty {
			result.SkippedRows++
			result.Warnings = append(result.Warnings, ValidationWarningItem{
				Line:       i + 1,
				Reason:     "Empty row",
				RawContent: rowStr,
			})
			continue
		}

		email := getVal(row, "email", "email_address", "contact_email")
		name := getVal(row, "name", "full_name", "contact_name")
		phone := getVal(row, "phone", "phone_number", "mobile")
		identifier := getVal(row, "identifier", "id", "customer_id")

		rowHasError := false

		// For contacts import, email or identifier is required
		if importType == "" || importType == "contacts" {
			if email == "" && identifier == "" {
				result.InvalidRows++
				rowHasError = true
				result.Errors = append(result.Errors, ValidationErrorItem{
					Line:       i + 1,
					Field:      "email",
					Reason:     "Missing required field: email or identifier",
					RawContent: rowStr,
				})
			} else if email != "" && !isValidEmail(email) {
				result.InvalidRows++
				rowHasError = true
				result.Errors = append(result.Errors, ValidationErrorItem{
					Line:       i + 1,
					Field:      "email",
					Reason:     fmt.Sprintf("Invalid email format: '%s'", email),
					RawContent: rowStr,
				})
			} else if email != "" {
				// Check in-batch duplicate
				if prevLine, exists := seenEmailsInBatch[email]; exists {
					result.Warnings = append(result.Warnings, ValidationWarningItem{
						Line:       i + 1,
						Field:      "email",
						Reason:     fmt.Sprintf("Duplicate email in batch (first seen at line %d)", prevLine),
						RawContent: rowStr,
					})
				} else {
					seenEmailsInBatch[email] = i + 1
				}

				// Check DB existence
				var count int64
				s.db.Model(&domain.Contact{}).Where("account_id = ? AND email = ?", accountID, email).Count(&count)
				if count > 0 {
					result.Warnings = append(result.Warnings, ValidationWarningItem{
						Line:       i + 1,
						Field:      "email",
						Reason:     fmt.Sprintf("Contact already exists in database with email '%s'", email),
						RawContent: rowStr,
					})
				}
			}
		}

		if !rowHasError {
			result.ValidRows++
		}

		// Preview first 10 rows
		if len(result.PreviewRows) < 10 {
			previewItem := make(map[string]any)
			for colIdx, colName := range header {
				if colIdx < len(row) {
					previewItem[colName] = row[colIdx]
				}
			}
			previewItem["_line"] = i + 1
			previewItem["_email"] = email
			previewItem["_name"] = name
			previewItem["_phone"] = phone
			result.PreviewRows = append(result.PreviewRows, previewItem)
		}
	}

	result.Valid = result.InvalidRows == 0 && result.ValidRows > 0
	return result, nil
}

func (s *DataImportService) prevalidateJSON(accountID uint, importType, rawJSON string) (*ValidationResult, error) {
	result := &ValidationResult{
		Errors:      make([]ValidationErrorItem, 0),
		Warnings:    make([]ValidationWarningItem, 0),
		PreviewRows: make([]map[string]any, 0),
	}

	var rawItems []map[string]any

	if strings.HasPrefix(rawJSON, "[") {
		if err := json.Unmarshal([]byte(rawJSON), &rawItems); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationErrorItem{
				Line:   1,
				Reason: fmt.Sprintf("Invalid JSON array: %s", err.Error()),
			})
			return result, nil
		}
	} else if strings.HasPrefix(rawJSON, "{") {
		var bundle map[string]any
		if err := json.Unmarshal([]byte(rawJSON), &bundle); err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationErrorItem{
				Line:   1,
				Reason: fmt.Sprintf("Invalid JSON object: %s", err.Error()),
			})
			return result, nil
		}
		if contactsRaw, ok := bundle["contacts"].([]any); ok {
			for _, item := range contactsRaw {
				if m, ok := item.(map[string]any); ok {
					rawItems = append(rawItems, m)
				}
			}
		} else {
			rawItems = append(rawItems, bundle)
		}
	}

	result.TotalRows = len(rawItems)
	seenEmailsInBatch := make(map[string]int)

	for i, item := range rawItems {
		itemBytes, _ := json.Marshal(item)
		rawStr := string(itemBytes)

		email, _ := item["email"].(string)
		if email == "" {
			email, _ = item["email_address"].(string)
		}
		identifier, _ := item["identifier"].(string)
		if identifier == "" {
			identifier, _ = item["id"].(string)
		}

		rowHasError := false
		if importType == "" || importType == "contacts" {
			if email == "" && identifier == "" {
				result.InvalidRows++
				rowHasError = true
				result.Errors = append(result.Errors, ValidationErrorItem{
					Line:       i + 1,
					Field:      "email",
					Reason:     "Missing required field: email or identifier",
					RawContent: rawStr,
				})
			} else if email != "" && !isValidEmail(email) {
				result.InvalidRows++
				rowHasError = true
				result.Errors = append(result.Errors, ValidationErrorItem{
					Line:       i + 1,
					Field:      "email",
					Reason:     fmt.Sprintf("Invalid email format: '%s'", email),
					RawContent: rawStr,
				})
			} else if email != "" {
				if prevLine, exists := seenEmailsInBatch[email]; exists {
					result.Warnings = append(result.Warnings, ValidationWarningItem{
						Line:       i + 1,
						Field:      "email",
						Reason:     fmt.Sprintf("Duplicate email in batch (first seen at item %d)", prevLine),
						RawContent: rawStr,
					})
				} else {
					seenEmailsInBatch[email] = i + 1
				}

				var count int64
				s.db.Model(&domain.Contact{}).Where("account_id = ? AND email = ?", accountID, email).Count(&count)
				if count > 0 {
					result.Warnings = append(result.Warnings, ValidationWarningItem{
						Line:       i + 1,
						Field:      "email",
						Reason:     fmt.Sprintf("Contact already exists in database with email '%s'", email),
						RawContent: rawStr,
					})
				}
			}
		}

		if !rowHasError {
			result.ValidRows++
		}

		if len(result.PreviewRows) < 10 {
			previewMap := make(map[string]any)
			for k, v := range item {
				previewMap[k] = v
			}
			previewMap["_index"] = i + 1
			result.PreviewRows = append(result.PreviewRows, previewMap)
		}
	}

	result.Valid = result.InvalidRows == 0 && result.ValidRows > 0
	return result, nil
}

// ExecuteImport processes raw data for an import task, tracking errors and skipped items
func (s *DataImportService) ExecuteImport(ctx context.Context, accountID uint, imp *domain.DataImport) error {
	imp.Status = "processing"
	_ = s.db.Save(imp)

	rawData := strings.TrimSpace(imp.RawData)
	if rawData == "" {
		imp.Status = "failed"
		imp.ErrorsJSON = `[{"line": 0, "reason": "No data to import"}]`
		_ = s.db.Save(imp)
		return errors.New("no data to import")
	}

	logger.WithComponent("data_import").Info("executing data import task",
		"import_id", imp.ID,
		"account_id", accountID,
		"import_type", imp.ImportType,
		"provider", imp.SourceProvider,
	)

	var errorRecords []ErrorRecord
	var skippedRecords []SkippedRecord
	processedCount := 0

	if imp.ImportType != "" && !strings.HasPrefix(imp.ImportType, "contact") {
		migService := NewMigrationService(s.db)
		stats, err := migService.Migrate(ctx, accountID, imp.ImportType, rawData)
		if stats != nil {
			processedCount = stats.TotalProcessed()
			for _, errMsg := range stats.Errors {
				errorRecords = append(errorRecords, ErrorRecord{Line: 1, Data: rawData, Reason: errMsg})
			}
		}
		if err != nil && len(errorRecords) == 0 {
			errorRecords = append(errorRecords, ErrorRecord{Line: 1, Data: rawData, Reason: err.Error()})
		}
	} else if imp.SourceProvider == "json" || strings.HasPrefix(rawData, "[") || strings.HasPrefix(rawData, "{") {
		processedCount, errorRecords, skippedRecords = s.executeJSONImport(accountID, imp.ImportType, rawData)
	} else {
		processedCount, errorRecords, skippedRecords = s.executeCSVImport(accountID, imp.ImportType, rawData)
	}

	imp.ProcessedRecords = processedCount
	imp.FailedRecords = len(errorRecords)
	imp.SkippedRecords = len(skippedRecords)
	imp.TotalRecords = processedCount + imp.FailedRecords + imp.SkippedRecords

	if len(errorRecords) > 0 {
		b, _ := json.Marshal(errorRecords)
		imp.ErrorsJSON = string(b)
	} else {
		imp.ErrorsJSON = "[]"
	}

	if len(skippedRecords) > 0 {
		b, _ := json.Marshal(skippedRecords)
		imp.SkippedJSON = string(b)
	} else {
		imp.SkippedJSON = "[]"
	}

	if processedCount > 0 {
		imp.Status = "completed"
	} else {
		imp.Status = "failed"
	}

	if err := s.db.Save(imp).Error; err != nil {
		logger.WithComponent("data_import").Error("failed to update data import record",
			"import_id", imp.ID,
			"account_id", accountID,
			"error", err.Error(),
		)
		return err
	}

	logger.WithComponent("data_import").Info("data import task completed",
		"import_id", imp.ID,
		"account_id", accountID,
		"status", imp.Status,
		"processed", imp.ProcessedRecords,
		"failed", imp.FailedRecords,
		"skipped", imp.SkippedRecords,
	)

	return nil
}

func (s *DataImportService) executeCSVImport(accountID uint, importType, rawCSV string) (int, []ErrorRecord, []SkippedRecord) {
	reader := csv.NewReader(strings.NewReader(rawCSV))
	records, err := reader.ReadAll()
	if err != nil {
		return 0, []ErrorRecord{{Line: 1, Data: rawCSV, Reason: fmt.Sprintf("Malformed CSV: %s", err.Error())}}, nil
	}
	if len(records) <= 1 {
		return 0, nil, []SkippedRecord{{Line: 1, Reason: "Empty file or only headers present"}}
	}

	header := records[0]
	colMap := make(map[string]int)
	for idx, col := range header {
		colMap[strings.ToLower(strings.TrimSpace(col))] = idx
	}

	getVal := func(row []string, keys ...string) string {
		for _, k := range keys {
			if idx, ok := colMap[k]; ok && idx < len(row) {
				return strings.TrimSpace(row[idx])
			}
		}
		return ""
	}

	var errorsList []ErrorRecord
	var skippedList []SkippedRecord
	processed := 0
	seenEmails := make(map[string]bool)

	for i := 1; i < len(records); i++ {
		row := records[i]
		rowStr := strings.Join(row, ",")

		isEmpty := true
		for _, val := range row {
			if strings.TrimSpace(val) != "" {
				isEmpty = false
				break
			}
		}
		if isEmpty {
			skippedList = append(skippedList, SkippedRecord{
				Line:   i + 1,
				Data:   rowStr,
				Reason: "Empty row",
			})
			continue
		}

		email := getVal(row, "email", "email_address", "contact_email")
		name := getVal(row, "name", "full_name", "contact_name")
		phone := getVal(row, "phone", "phone_number", "mobile")
		identifier := getVal(row, "identifier", "id", "customer_id")

		if email == "" && identifier == "" {
			errorsList = append(errorsList, ErrorRecord{
				Line:   i + 1,
				Data:   rowStr,
				Reason: "Missing required email or identifier",
			})
			continue
		}

		if email != "" && !isValidEmail(email) {
			errorsList = append(errorsList, ErrorRecord{
				Line:   i + 1,
				Data:   rowStr,
				Reason: fmt.Sprintf("Invalid email format: '%s'", email),
			})
			continue
		}

		if email != "" {
			if seenEmails[email] {
				skippedList = append(skippedList, SkippedRecord{
					Line:   i + 1,
					Data:   rowStr,
					Reason: fmt.Sprintf("Duplicate email in import file: '%s'", email),
				})
				continue
			}
			seenEmails[email] = true

			// Check DB duplicate
			var existing domain.Contact
			if err := s.db.Where("account_id = ? AND email = ?", accountID, email).First(&existing).Error; err == nil && existing.ID > 0 {
				skippedList = append(skippedList, SkippedRecord{
					Line:   i + 1,
					Data:   rowStr,
					Reason: fmt.Sprintf("Contact already exists in database with email '%s'", email),
				})
				continue
			}
		}

		if name == "" {
			if email != "" {
				name = strings.Split(email, "@")[0]
			} else {
				name = "Imported Contact"
			}
		}

		contact := domain.Contact{
			AccountID:   accountID,
			Name:        name,
			Email:       email,
			PhoneNumber: phone,
			Identifier:  identifier,
			CreatedAt:   time.Now().UTC(),
		}

		if err := s.db.Create(&contact).Error; err != nil {
			errorsList = append(errorsList, ErrorRecord{
				Line:   i + 1,
				Data:   rowStr,
				Reason: fmt.Sprintf("Database insert failed: %s", err.Error()),
			})
		} else {
			processed++
		}
	}

	return processed, errorsList, skippedList
}

func (s *DataImportService) executeJSONImport(accountID uint, importType, rawJSON string) (int, []ErrorRecord, []SkippedRecord) {
	var rawItems []map[string]any
	if strings.HasPrefix(rawJSON, "[") {
		_ = json.Unmarshal([]byte(rawJSON), &rawItems)
	} else if strings.HasPrefix(rawJSON, "{") {
		var bundle map[string]any
		_ = json.Unmarshal([]byte(rawJSON), &bundle)
		if contactsRaw, ok := bundle["contacts"].([]any); ok {
			for _, item := range contactsRaw {
				if m, ok := item.(map[string]any); ok {
					rawItems = append(rawItems, m)
				}
			}
		} else {
			rawItems = append(rawItems, bundle)
		}
	}

	var errorsList []ErrorRecord
	var skippedList []SkippedRecord
	processed := 0
	seenEmails := make(map[string]bool)

	for i, item := range rawItems {
		itemBytes, _ := json.Marshal(item)
		rawStr := string(itemBytes)

		email, _ := item["email"].(string)
		if email == "" {
			email, _ = item["email_address"].(string)
		}
		name, _ := item["name"].(string)
		phone, _ := item["phone"].(string)
		if phone == "" {
			phone, _ = item["phone_number"].(string)
		}
		identifier, _ := item["identifier"].(string)
		if identifier == "" {
			identifier, _ = item["id"].(string)
		}

		if email == "" && identifier == "" {
			errorsList = append(errorsList, ErrorRecord{
				Line:   i + 1,
				Data:   rawStr,
				Reason: "Missing required email or identifier",
			})
			continue
		}

		if email != "" && !isValidEmail(email) {
			errorsList = append(errorsList, ErrorRecord{
				Line:   i + 1,
				Data:   rawStr,
				Reason: fmt.Sprintf("Invalid email format: '%s'", email),
			})
			continue
		}

		if email != "" {
			if seenEmails[email] {
				skippedList = append(skippedList, SkippedRecord{
					Line:   i + 1,
					Data:   rawStr,
					Reason: fmt.Sprintf("Duplicate email in import file: '%s'", email),
				})
				continue
			}
			seenEmails[email] = true

			var existing domain.Contact
			if err := s.db.Where("account_id = ? AND email = ?", accountID, email).First(&existing).Error; err == nil && existing.ID > 0 {
				skippedList = append(skippedList, SkippedRecord{
					Line:   i + 1,
					Data:   rawStr,
					Reason: fmt.Sprintf("Contact already exists in database with email '%s'", email),
				})
				continue
			}
		}

		if name == "" {
			if email != "" {
				name = strings.Split(email, "@")[0]
			} else {
				name = "Imported Contact"
			}
		}

		contact := domain.Contact{
			AccountID:   accountID,
			Name:        name,
			Email:       email,
			PhoneNumber: phone,
			Identifier:  identifier,
			CreatedAt:   time.Now().UTC(),
		}

		if err := s.db.Create(&contact).Error; err != nil {
			errorsList = append(errorsList, ErrorRecord{
				Line:   i + 1,
				Data:   rawStr,
				Reason: fmt.Sprintf("Database insert failed: %s", err.Error()),
			})
		} else {
			processed++
		}
	}

	return processed, errorsList, skippedList
}

// GenerateErrorsCSV generates downloadable CSV content for failed rows
func (s *DataImportService) GenerateErrorsCSV(imp *domain.DataImport) ([]byte, error) {
	var records []ErrorRecord
	if imp.ErrorsJSON != "" && imp.ErrorsJSON != "[]" {
		_ = json.Unmarshal([]byte(imp.ErrorsJSON), &records)
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Line", "Data", "Reason"})

	for _, rec := range records {
		_ = w.Write([]string{
			fmt.Sprintf("%d", rec.Line),
			rec.Data,
			rec.Reason,
		})
	}
	w.Flush()
	return buf.Bytes(), nil
}

// GenerateSkippedCSV generates downloadable CSV content for skipped rows
func (s *DataImportService) GenerateSkippedCSV(imp *domain.DataImport) ([]byte, error) {
	var records []SkippedRecord
	if imp.SkippedJSON != "" && imp.SkippedJSON != "[]" {
		_ = json.Unmarshal([]byte(imp.SkippedJSON), &records)
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"Line", "Data", "Reason"})

	for _, rec := range records {
		_ = w.Write([]string{
			fmt.Sprintf("%d", rec.Line),
			rec.Data,
			rec.Reason,
		})
	}
	w.Flush()
	return buf.Bytes(), nil
}
