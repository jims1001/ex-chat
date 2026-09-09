package repository

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

// AIToolFilter defines criteria for querying custom AI tools
type AIToolFilter struct {
	Status          string
	PermissionLevel string
	Category        string
	Environment     string
	Query           string
	Page            int
	PageSize        int
}

// AIToolMetrics provides aggregated monitoring data for AI tools
type AIToolMetrics struct {
	TotalTools      int64  `json:"total_tools"`
	ReadonlyTools   int64  `json:"readonly_tools"`
	WriteTools      int64  `json:"write_tools"`
	RestrictedTools int64  `json:"restricted_tools"`
	ActiveTools     int64  `json:"active_tools"`
	TodayCalls      int64  `json:"today_calls"`
	TotalCalls      int64  `json:"total_calls"`
	SuccessRate     string `json:"success_rate"`
}

// AICustomToolRepository manages database operations for custom AI tools and execution logs
type AICustomToolRepository struct {
	db *gorm.DB
}

// NewAICustomToolRepository creates a new instance of AICustomToolRepository
func NewAICustomToolRepository(db *gorm.DB) *AICustomToolRepository {
	return &AICustomToolRepository{db: db}
}

// CreateTool registers a new custom AI tool
func (r *AICustomToolRepository) CreateTool(tool *domain.AICustomTool) error {
	if tool.AccountID == 0 {
		return errors.New("account_id is required")
	}
	tool.Name = strings.TrimSpace(strings.ToLower(tool.Name))
	if tool.Name == "" {
		return errors.New("tool name is required")
	}

	// Check name uniqueness per account
	var count int64
	r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ? AND name = ?", tool.AccountID, tool.Name).
		Count(&count)
	if count > 0 {
		return fmt.Errorf("tool with name '%s' already exists in this account", tool.Name)
	}

	if tool.Status == "" {
		tool.Status = domain.AIToolStatusActive
	}
	if tool.PermissionLevel == "" {
		tool.PermissionLevel = domain.AIToolPermissionReadonly
	}
	if tool.Environment == "" {
		tool.Environment = "production"
	}
	if tool.ToolType == "" {
		tool.ToolType = domain.AIToolTypeInternalFunction
	}
	if tool.TimeoutSeconds <= 0 {
		tool.TimeoutSeconds = 10
	}

	now := time.Now().UTC()
	tool.CreatedAt = now
	tool.UpdatedAt = now

	return r.db.Create(tool).Error
}

// GetToolByID retrieves a tool by ID
func (r *AICustomToolRepository) GetToolByID(accountID, toolID uint) (*domain.AICustomTool, error) {
	var tool domain.AICustomTool
	err := r.db.Where("account_id = ? AND id = ?", accountID, toolID).First(&tool).Error
	if err != nil {
		return nil, err
	}
	return &tool, nil
}

// GetToolByName retrieves a tool by its unique name within an account
func (r *AICustomToolRepository) GetToolByName(accountID uint, name string) (*domain.AICustomTool, error) {
	var tool domain.AICustomTool
	err := r.db.Where("account_id = ? AND name = ?", accountID, strings.TrimSpace(strings.ToLower(name))).First(&tool).Error
	if err != nil {
		return nil, err
	}
	return &tool, nil
}

// UpdateTool modifies an existing custom AI tool
func (r *AICustomToolRepository) UpdateTool(tool *domain.AICustomTool) error {
	tool.UpdatedAt = time.Now().UTC()
	updates := map[string]any{
		"title":                 tool.Title,
		"description":           tool.Description,
		"category":              tool.Category,
		"tool_type":             tool.ToolType,
		"endpoint_url":          tool.EndpointURL,
		"http_method":           tool.HTTPMethod,
		"headers":               tool.Headers,
		"input_schema":          tool.InputSchema,
		"output_schema":         tool.OutputSchema,
		"permission_level":      tool.PermissionLevel,
		"requires_confirmation": tool.RequiresConfirmation,
		"timeout_seconds":       tool.TimeoutSeconds,
		"status":                tool.Status,
		"environment":           tool.Environment,
		"updated_at":            tool.UpdatedAt,
	}

	return r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ? AND id = ?", tool.AccountID, tool.ID).
		Updates(updates).Error
}

// DeleteTool physically deletes a tool and cascades deletions of its execution logs
func (r *AICustomToolRepository) DeleteTool(accountID, toolID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var tool domain.AICustomTool
		if err := tx.Where("account_id = ? AND id = ?", accountID, toolID).First(&tool).Error; err != nil {
			return err
		}

		// Delete execution logs
		if err := tx.Where("account_id = ? AND tool_id = ?", accountID, toolID).
			Delete(&domain.AIToolExecutionLog{}).Error; err != nil {
			return err
		}

		return tx.Delete(&tool).Error
	})
}

// ListTools returns tools matching criteria with pagination
func (r *AICustomToolRepository) ListTools(accountID uint, filter AIToolFilter) ([]domain.AICustomTool, int64, error) {
	var tools []domain.AICustomTool
	var total int64

	q := r.db.Model(&domain.AICustomTool{}).Where("account_id = ?", accountID)

	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.PermissionLevel != "" {
		q = q.Where("permission_level = ?", filter.PermissionLevel)
	}
	if filter.Category != "" {
		q = q.Where("category = ?", filter.Category)
	}
	if filter.Environment != "" {
		q = q.Where("environment = ?", filter.Environment)
	}
	if filter.Query != "" {
		like := "%" + strings.TrimSpace(filter.Query) + "%"
		q = q.Where("name LIKE ? OR title LIKE ? OR description LIKE ?", like, like, like)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	err := q.Order("id DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&tools).Error

	return tools, total, err
}

// RecordExecutionLog saves an execution or test log and updates tool metrics
func (r *AICustomToolRepository) RecordExecutionLog(log *domain.AIToolExecutionLog) error {
	if log.AccountID == 0 || log.ToolID == 0 {
		return errors.New("account_id and tool_id are required")
	}

	now := time.Now().UTC()
	log.CreatedAt = now

	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(log).Error; err != nil {
			return err
		}

		// Update tool call statistics
		updates := map[string]any{
			"daily_calls": gorm.Expr("daily_calls + ?", 1),
			"total_calls": gorm.Expr("total_calls + ?", 1),
		}
		if log.Status == domain.AIToolExecutionSuccess {
			updates["success_calls"] = gorm.Expr("success_calls + ?", 1)
		}

		return tx.Model(&domain.AICustomTool{}).
			Where("account_id = ? AND id = ?", log.AccountID, log.ToolID).
			Updates(updates).Error
	})
}

// ListExecutionLogs retrieves paginated logs for a specific tool
func (r *AICustomToolRepository) ListExecutionLogs(accountID, toolID uint, page, pageSize int) ([]domain.AIToolExecutionLog, int64, error) {
	var logs []domain.AIToolExecutionLog
	var total int64

	q := r.db.Model(&domain.AIToolExecutionLog{}).
		Where("account_id = ? AND tool_id = ?", accountID, toolID)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	err := q.Order("id DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&logs).Error

	return logs, total, err
}

// GetToolMetrics calculates aggregated metrics for AI tools
func (r *AICustomToolRepository) GetToolMetrics(accountID uint) (*AIToolMetrics, error) {
	metrics := &AIToolMetrics{
		SuccessRate: "100%",
	}

	// 1. Tool count and breakdown
	var totalTools int64
	_ = r.db.Model(&domain.AICustomTool{}).Where("account_id = ?", accountID).Count(&totalTools)
	metrics.TotalTools = totalTools

	var readonlyTools int64
	_ = r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ? AND permission_level = ?", accountID, domain.AIToolPermissionReadonly).
		Count(&readonlyTools)
	metrics.ReadonlyTools = readonlyTools

	var writeTools int64
	_ = r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ? AND permission_level IN (?, ?)", accountID, domain.AIToolPermissionWriteConfirm, domain.AIToolPermissionWriteStrongConfirm).
		Count(&writeTools)
	metrics.WriteTools = writeTools

	var restrictedTools int64
	_ = r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ? AND (status = ? OR requires_confirmation = true)", accountID, domain.AIToolStatusRestricted).
		Count(&restrictedTools)
	metrics.RestrictedTools = restrictedTools

	var activeTools int64
	_ = r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ? AND status = ?", accountID, domain.AIToolStatusActive).
		Count(&activeTools)
	metrics.ActiveTools = activeTools

	// 2. Call counts & success rate
	type callStats struct {
		TotalCalls   int64
		SuccessCalls int64
		DailyCalls   int64
	}
	var stats callStats
	_ = r.db.Model(&domain.AICustomTool{}).
		Where("account_id = ?", accountID).
		Select("COALESCE(SUM(total_calls), 0) as total_calls, COALESCE(SUM(success_calls), 0) as success_calls, COALESCE(SUM(daily_calls), 0) as daily_calls").
		Scan(&stats)

	metrics.TotalCalls = stats.TotalCalls
	metrics.TodayCalls = stats.DailyCalls

	if stats.TotalCalls > 0 {
		rate := float64(stats.SuccessCalls) / float64(stats.TotalCalls) * 100.0
		metrics.SuccessRate = fmt.Sprintf("%.1f%%", rate)
	}

	logger.WithComponent("ai_tool").Info("ai tool metrics aggregated",
		"account_id", accountID,
		"total_tools", metrics.TotalTools,
		"today_calls", metrics.TodayCalls,
		"success_rate", metrics.SuccessRate,
	)

	return metrics, nil
}
