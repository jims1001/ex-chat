package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/OracleBetX-Projects/ex-chat/pkg/security"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// AICustomToolHandler handles RESTful management and testing of custom AI tools
type AICustomToolHandler struct {
	db         *gorm.DB
	toolRepo   *repository.AICustomToolRepository
	ticketRepo *repository.TicketRepository
	msgRepo    *repository.MessageRepository
	orderRepo  *repository.OrderRepository
}

type AICustomToolDependencies struct {
	TicketRepo  *repository.TicketRepository
	MessageRepo *repository.MessageRepository
	OrderRepo   *repository.OrderRepository
}

// NewAICustomToolHandler creates a new instance of AICustomToolHandler
func NewAICustomToolHandler(db *gorm.DB, toolRepo *repository.AICustomToolRepository, options ...AICustomToolDependencies) *AICustomToolHandler {
	ticketRepo := repository.NewTicketRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	orderRepo := repository.NewOrderRepository(db)
	if len(options) > 0 {
		if options[0].TicketRepo != nil {
			ticketRepo = options[0].TicketRepo
		}
		if options[0].MessageRepo != nil {
			msgRepo = options[0].MessageRepo
		}
		if options[0].OrderRepo != nil {
			orderRepo = options[0].OrderRepo
		}
	}
	return &AICustomToolHandler{
		db:         db,
		toolRepo:   toolRepo,
		ticketRepo: ticketRepo,
		msgRepo:    msgRepo,
		orderRepo:  orderRepo,
	}
}

func (h *AICustomToolHandler) getAccountID(c *gin.Context) uint {
	if accVal, exists := c.Get("account_id"); exists {
		if id, ok := accVal.(uint); ok {
			return id
		}
	}
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	return uint(accID)
}

func (h *AICustomToolHandler) getUserID(c *gin.Context) uint {
	if userVal, exists := c.Get("user_id"); exists {
		if id, ok := userVal.(uint); ok {
			return id
		}
	}
	return 1
}

// ----------------- Tool Management Endpoints -----------------

// ListTools handles GET /captain/tools
func (h *AICustomToolHandler) ListTools(c *gin.Context) {
	accountID := h.getAccountID(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	status := c.Query("status")
	perm := c.Query("permission_level")
	category := c.Query("category")
	env := c.Query("environment")
	query := c.Query("q")

	filter := repository.AIToolFilter{
		Status:          status,
		PermissionLevel: perm,
		Category:        category,
		Environment:     env,
		Query:           query,
		Page:            page,
		PageSize:        pageSize,
	}

	tools, total, err := h.toolRepo.ListTools(accountID, filter)
	if err != nil {
		logger.WithComponent("ai_tool").Error("failed to list ai tools",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list AI tools")
		return
	}

	logger.WithComponent("ai_tool").Info("listed ai tools",
		"account_id", accountID,
		"count", len(tools),
		"total", total,
	)

	response.Success(c, gin.H{
		"tools": tools,
		"meta": gin.H{
			"total":    total,
			"page":     page,
			"per_page": pageSize,
		},
	})
}

// CreateToolReq defines payload for registering a custom AI tool
type CreateToolReq struct {
	Name                 string `json:"name" binding:"required"`
	Title                string `json:"title" binding:"required"`
	Description          string `json:"description" binding:"required"`
	Category             string `json:"category"`
	ToolType             string `json:"tool_type"`
	EndpointURL          string `json:"endpoint_url"`
	HTTPMethod           string `json:"http_method"`
	Headers              string `json:"headers"`
	InputSchema          string `json:"input_schema" binding:"required"`
	OutputSchema         string `json:"output_schema"`
	PermissionLevel      string `json:"permission_level"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
	TimeoutSeconds       int    `json:"timeout_seconds"`
	Status               string `json:"status"`
	Environment          string `json:"environment"`
}

// CreateTool handles POST /captain/tools
func (h *AICustomToolHandler) CreateTool(c *gin.Context) {
	accountID := h.getAccountID(c)

	var req CreateToolReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Validate JSON Schema
	var schemaCheck json.RawMessage
	if err := json.Unmarshal([]byte(req.InputSchema), &schemaCheck); err != nil {
		response.BadRequest(c, "input_schema must be valid JSON Schema: "+err.Error())
		return
	}
	if req.OutputSchema != "" {
		if err := json.Unmarshal([]byte(req.OutputSchema), &schemaCheck); err != nil {
			response.BadRequest(c, "output_schema must be valid JSON: "+err.Error())
			return
		}
	}

	if req.EndpointURL != "" {
		if err := security.ValidateSafeURL(req.EndpointURL); err != nil {
			response.BadRequest(c, "Invalid endpoint_url: "+err.Error())
			return
		}
	}

	tool := domain.AICustomTool{
		AccountID:            accountID,
		Name:                 req.Name,
		Title:                req.Title,
		Description:          req.Description,
		Category:             req.Category,
		ToolType:             req.ToolType,
		EndpointURL:          req.EndpointURL,
		HTTPMethod:           req.HTTPMethod,
		Headers:              req.Headers,
		InputSchema:          req.InputSchema,
		OutputSchema:         req.OutputSchema,
		PermissionLevel:      req.PermissionLevel,
		RequiresConfirmation: req.RequiresConfirmation,
		TimeoutSeconds:       req.TimeoutSeconds,
		Status:               req.Status,
		Environment:          req.Environment,
	}

	if err := h.toolRepo.CreateTool(&tool); err != nil {
		logger.WithComponent("ai_tool").Error("failed to create ai tool",
			"account_id", accountID,
			"tool_name", req.Name,
			"error", err.Error(),
		)
		response.BadRequest(c, err.Error())
		return
	}

	logger.WithComponent("ai_tool").Info("created ai tool",
		"account_id", accountID,
		"tool_id", tool.ID,
		"tool_name", tool.Name,
		"title", tool.Title,
	)

	response.Success(c, tool)
}

// GetTool handles GET /captain/tools/:id
func (h *AICustomToolHandler) GetTool(c *gin.Context) {
	accountID := h.getAccountID(c)
	toolID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || toolID == 0 {
		response.BadRequest(c, "Invalid tool ID")
		return
	}

	tool, err := h.toolRepo.GetToolByID(accountID, uint(toolID))
	if err != nil || tool == nil {
		response.NotFound(c, "AI tool not found")
		return
	}

	response.Success(c, tool)
}

// UpdateToolReq defines update payload for custom AI tools
type UpdateToolReq struct {
	Title                *string `json:"title"`
	Description          *string `json:"description"`
	Category             *string `json:"category"`
	ToolType             *string `json:"tool_type"`
	EndpointURL          *string `json:"endpoint_url"`
	HTTPMethod           *string `json:"http_method"`
	Headers              *string `json:"headers"`
	InputSchema          *string `json:"input_schema"`
	OutputSchema         *string `json:"output_schema"`
	PermissionLevel      *string `json:"permission_level"`
	RequiresConfirmation *bool   `json:"requires_confirmation"`
	TimeoutSeconds       *int    `json:"timeout_seconds"`
	Status               *string `json:"status"`
	Environment          *string `json:"environment"`
}

// UpdateTool handles PUT/PATCH /captain/tools/:id
func (h *AICustomToolHandler) UpdateTool(c *gin.Context) {
	accountID := h.getAccountID(c)
	toolID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || toolID == 0 {
		response.BadRequest(c, "Invalid tool ID")
		return
	}

	tool, err := h.toolRepo.GetToolByID(accountID, uint(toolID))
	if err != nil || tool == nil {
		response.NotFound(c, "AI tool not found")
		return
	}

	var req UpdateToolReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.InputSchema != nil {
		var schemaCheck json.RawMessage
		if err := json.Unmarshal([]byte(*req.InputSchema), &schemaCheck); err != nil {
			response.BadRequest(c, "input_schema must be valid JSON Schema: "+err.Error())
			return
		}
		tool.InputSchema = *req.InputSchema
	}
	if req.OutputSchema != nil {
		tool.OutputSchema = *req.OutputSchema
	}
	if req.Title != nil {
		tool.Title = *req.Title
	}
	if req.Description != nil {
		tool.Description = *req.Description
	}
	if req.Category != nil {
		tool.Category = *req.Category
	}
	if req.ToolType != nil {
		tool.ToolType = *req.ToolType
	}
	if req.EndpointURL != nil {
		if *req.EndpointURL != "" {
			if err := security.ValidateSafeURL(*req.EndpointURL); err != nil {
				response.BadRequest(c, "Invalid endpoint_url: "+err.Error())
				return
			}
		}
		tool.EndpointURL = *req.EndpointURL
	}
	if req.HTTPMethod != nil {
		tool.HTTPMethod = *req.HTTPMethod
	}
	if req.Headers != nil {
		tool.Headers = *req.Headers
	}
	if req.PermissionLevel != nil {
		tool.PermissionLevel = *req.PermissionLevel
	}
	if req.RequiresConfirmation != nil {
		tool.RequiresConfirmation = *req.RequiresConfirmation
	}
	if req.TimeoutSeconds != nil {
		tool.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.Status != nil {
		tool.Status = *req.Status
	}
	if req.Environment != nil {
		tool.Environment = *req.Environment
	}

	if err := h.toolRepo.UpdateTool(tool); err != nil {
		logger.WithComponent("ai_tool").Error("failed to update ai tool",
			"account_id", accountID,
			"tool_id", toolID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update AI tool")
		return
	}

	logger.WithComponent("ai_tool").Info("updated ai tool",
		"account_id", accountID,
		"tool_id", toolID,
		"status", tool.Status,
	)

	response.Success(c, tool)
}

// DeleteTool handles DELETE /captain/tools/:id
func (h *AICustomToolHandler) DeleteTool(c *gin.Context) {
	accountID := h.getAccountID(c)
	toolID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || toolID == 0 {
		response.BadRequest(c, "Invalid tool ID")
		return
	}

	if err := h.toolRepo.DeleteTool(accountID, uint(toolID)); err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFound(c, "AI tool not found")
			return
		}
		logger.WithComponent("ai_tool").Error("failed to delete ai tool",
			"account_id", accountID,
			"tool_id", toolID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete AI tool")
		return
	}

	logger.WithComponent("ai_tool").Info("deleted ai tool",
		"account_id", accountID,
		"tool_id", toolID,
	)

	response.Success(c, gin.H{"deleted": true, "tool_id": toolID})
}

// GetMetrics handles GET /captain/tools/metrics
func (h *AICustomToolHandler) GetMetrics(c *gin.Context) {
	accountID := h.getAccountID(c)

	metrics, err := h.toolRepo.GetToolMetrics(accountID)
	if err != nil {
		logger.WithComponent("ai_tool").Error("failed to get ai tool metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get AI tool metrics")
		return
	}

	response.Success(c, metrics)
}

// ListExecutionLogs handles GET /captain/tools/:id/logs and GET /captain/tools/execution_logs
func (h *AICustomToolHandler) ListExecutionLogs(c *gin.Context) {
	accountID := h.getAccountID(c)
	var toolID uint
	if paramID := c.Param("id"); paramID != "" && paramID != "execution_logs" {
		if id, err := strconv.ParseUint(paramID, 10, 32); err == nil {
			toolID = uint(id)
		}
	} else if qToolID := c.Query("tool_id"); qToolID != "" {
		if id, err := strconv.ParseUint(qToolID, 10, 32); err == nil {
			toolID = uint(id)
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))

	logs, total, err := h.toolRepo.ListExecutionLogs(accountID, toolID, page, pageSize)
	if err != nil {
		response.InternalError(c, "Failed to list execution logs")
		return
	}

	response.Success(c, gin.H{
		"logs": logs,
		"meta": gin.H{
			"total":    total,
			"page":     page,
			"per_page": pageSize,
		},
	})
}

// ----------------- Testing & Execution Engine -----------------

// TestToolReq defines testing input for validating and simulating tool execution
type TestToolReq struct {
	ToolID         *uint          `json:"tool_id"`
	ToolName       string         `json:"tool_name"`
	Params         map[string]any `json:"params"`
	ConversationID *uint          `json:"conversation_id"`
	ConfirmExecute bool           `json:"confirm_execute"`
	ExecutionMode  string         `json:"execution_mode"` // "test" by default
}

// TestTool handles POST /captain/tools/:id/test and POST /captain/tools/test
func (h *AICustomToolHandler) TestTool(c *gin.Context) {
	accountID := h.getAccountID(c)
	userID := h.getUserID(c)

	var req TestToolReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, err.Error())
		return
	}

	// Resolve tool ID from URL parameter or body
	if paramID := c.Param("id"); paramID != "" {
		if id, err := strconv.ParseUint(paramID, 10, 32); err == nil {
			u := uint(id)
			req.ToolID = &u
		}
	}

	var tool *domain.AICustomTool
	var err error
	if req.ToolID != nil && *req.ToolID > 0 {
		tool, err = h.toolRepo.GetToolByID(accountID, *req.ToolID)
	} else if req.ToolName != "" {
		tool, err = h.toolRepo.GetToolByName(accountID, req.ToolName)
	} else {
		response.BadRequest(c, "tool_id or tool_name is required")
		return
	}

	if err != nil || tool == nil {
		response.NotFound(c, "AI tool not found in current account")
		return
	}

	if req.Params == nil {
		req.Params = make(map[string]any)
	}

	executionMode := req.ExecutionMode
	if executionMode == "" {
		executionMode = "test"
	}

	// 1. JSON Schema Pre-validation
	if schemaErr := validateInputSchema(tool.InputSchema, req.Params); schemaErr != nil {
		inputBytes, _ := json.Marshal(req.Params)
		_ = h.toolRepo.RecordExecutionLog(&domain.AIToolExecutionLog{
			AccountID:      accountID,
			ToolID:         tool.ID,
			ToolName:       tool.Name,
			UserID:         userID,
			ConversationID: req.ConversationID,
			ExecutionMode:  executionMode,
			InputParams:    string(inputBytes),
			OutputResult:   "{}",
			Status:         domain.AIToolExecutionFailed,
			ErrorMessage:   schemaErr.Error(),
			LatencyMs:      1,
		})

		logger.WithComponent("ai_tool").Warn("ai tool schema validation failed",
			"account_id", accountID,
			"tool_name", tool.Name,
			"error", schemaErr.Error(),
		)

		response.BadRequest(c, "Schema validation failed: "+schemaErr.Error())
		return
	}

	// 2. Permission and confirmation check
	if (tool.RequiresConfirmation ||
		tool.PermissionLevel == domain.AIToolPermissionWriteConfirm ||
		tool.PermissionLevel == domain.AIToolPermissionWriteStrongConfirm) && !req.ConfirmExecute {

		inputBytes, _ := json.Marshal(req.Params)
		_ = h.toolRepo.RecordExecutionLog(&domain.AIToolExecutionLog{
			AccountID:      accountID,
			ToolID:         tool.ID,
			ToolName:       tool.Name,
			UserID:         userID,
			ConversationID: req.ConversationID,
			ExecutionMode:  executionMode,
			InputParams:    string(inputBytes),
			OutputResult:   "{}",
			Status:         domain.AIToolExecutionRejected,
			ErrorMessage:   "Operation requires user/agent confirmation before write execution",
			LatencyMs:      1,
		})

		c.JSON(http.StatusOK, gin.H{
			"status":                "requires_confirmation",
			"message":               "此操作为写操作，需要人工确认授权后方可执行",
			"tool_id":               tool.ID,
			"tool_name":             tool.Name,
			"permission_level":      tool.PermissionLevel,
			"requires_confirmation": true,
		})
		return
	}

	// 3. Execution Simulation / Webhook Forwarding
	startTime := time.Now()
	var outputResult any
	var execStatus = domain.AIToolExecutionSuccess
	var execError string

	timeout := time.Duration(tool.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	cleanName := strings.TrimSpace(strings.ToLower(tool.Name))
	isBuiltin := cleanName == "query_order" || cleanName == "order_lookup" ||
		cleanName == "lookup_logistics" || cleanName == "query_logistics" ||
		cleanName == "create_ticket" || cleanName == "update_shipping_address" ||
		cleanName == "kb_search"

	if tool.ToolType == domain.AIToolTypeWebhook || tool.ToolType == domain.AIToolTypeHTTPAPI {
		if strings.TrimSpace(tool.EndpointURL) != "" {
			outputResult, err = executeHTTPTool(tool, req.Params, timeout)
			if err != nil {
				execStatus = domain.AIToolExecutionFailed
				execError = err.Error()
				outputResult = gin.H{"error": err.Error(), "success": false}
			}
		} else {
			execStatus = domain.AIToolExecutionFailed
			execError = "Tool endpoint URL is not configured; cannot execute HTTP/Webhook tool without endpoint"
			outputResult = gin.H{
				"success": false,
				"error":   execError,
				"code":    "endpoint_missing",
			}
		}
	} else if isBuiltin {
		outputResult, err = h.executeInternalTool(accountID, userID, req.ConversationID, tool.Name, req.Params)
		if err != nil {
			execStatus = domain.AIToolExecutionFailed
			execError = err.Error()
			outputResult = gin.H{"error": err.Error(), "success": false}
		}
	} else if strings.TrimSpace(tool.EndpointURL) != "" {
		outputResult, err = executeHTTPTool(tool, req.Params, timeout)
		if err != nil {
			execStatus = domain.AIToolExecutionFailed
			execError = err.Error()
			outputResult = gin.H{"error": err.Error(), "success": false}
		}
	} else {
		execStatus = domain.AIToolExecutionFailed
		execError = fmt.Sprintf("Tool '%s' has no endpoint URL configured and is not a recognized internal built-in function", tool.Name)
		outputResult = gin.H{
			"success": false,
			"error":   execError,
			"code":    "endpoint_missing_and_unknown_tool",
		}
	}

	latency := time.Since(startTime).Milliseconds()
	if latency == 0 {
		latency = 1
	}

	inputBytes, _ := json.Marshal(req.Params)
	outputBytes, _ := json.Marshal(outputResult)

	// 4. Record execution log
	consumedTokens := 25
	logEntry := domain.AIToolExecutionLog{
		AccountID:      accountID,
		ToolID:         tool.ID,
		ToolName:       tool.Name,
		UserID:         userID,
		ConversationID: req.ConversationID,
		ExecutionMode:  executionMode,
		InputParams:    string(inputBytes),
		OutputResult:   string(outputBytes),
		Status:         execStatus,
		ErrorMessage:   execError,
		LatencyMs:      latency,
		TokensConsumed: consumedTokens,
	}
	_ = h.toolRepo.RecordExecutionLog(&logEntry)

	// 5. Update Monthly Quota if exists
	var quota domain.AIUsageQuota
	if h.db != nil && h.db.Where("account_id = ?", accountID).First(&quota).Error == nil {
		_ = h.db.Model(&quota).Updates(map[string]any{
			"used_tokens":   gorm.Expr("used_tokens + ?", consumedTokens),
			"used_requests": gorm.Expr("used_requests + ?", 1),
		}).Error
	}

	logger.WithComponent("ai_tool").Info("ai tool executed",
		"account_id", accountID,
		"tool_name", tool.Name,
		"mode", executionMode,
		"status", execStatus,
		"latency_ms", latency,
	)

	response.Success(c, gin.H{
		"status":           execStatus,
		"tool_id":          tool.ID,
		"tool_name":        tool.Name,
		"result":           outputResult,
		"latency_ms":       latency,
		"log_id":           logEntry.ID,
		"schema_validated": true,
		"tokens_consumed":  consumedTokens,
	})
}

// Helper: validate parameters against JSON Schema
func validateInputSchema(schemaJSON string, params map[string]any) error {
	if strings.TrimSpace(schemaJSON) == "" {
		return nil
	}

	var schemaObj struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}

	if err := json.Unmarshal([]byte(schemaJSON), &schemaObj); err != nil {
		return nil // skip if not standard object schema
	}

	// Check required fields
	for _, reqKey := range schemaObj.Required {
		val, exists := params[reqKey]
		if !exists || val == nil {
			return fmt.Errorf("missing required parameter '%s'", reqKey)
		}
		if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
			return fmt.Errorf("required parameter '%s' cannot be empty", reqKey)
		}
	}

	return nil
}

// Helper: execute built-in tools with real business data persistence
func (h *AICustomToolHandler) executeInternalTool(accountID uint, userID uint, convID *uint, name string, params map[string]any) (any, error) {
	cleanName := strings.TrimSpace(strings.ToLower(name))
	now := time.Now().UTC()

	switch cleanName {
	case "query_order", "order_lookup":
		orderID, _ := params["order_id"].(string)
		if orderID == "" {
			if o, ok := params["order_no"].(string); ok && o != "" {
				orderID = o
			} else if idStr, ok := params["id"].(string); ok && idStr != "" {
				orderID = idStr
			}
		}
		if orderID == "" {
			return nil, fmt.Errorf("order_id parameter is required")
		}

		var order domain.Order
		err := h.db.Where("account_id = ? AND (order_id = ? OR tracking_number = ?)", accountID, orderID, orderID).First(&order).Error
		if err != nil {
			return nil, fmt.Errorf("order '%s' not found in database", orderID)
		}

		var items []gin.H
		if order.ItemsJSON != "" {
			_ = json.Unmarshal([]byte(order.ItemsJSON), &items)
		}
		if items == nil {
			items = []gin.H{}
		}

		return gin.H{
			"order_id":         order.OrderID,
			"order_status":     order.OrderStatus,
			"customer_name":    order.CustomerName,
			"customer_email":   order.CustomerEmail,
			"customer_phone":   order.CustomerPhone,
			"amount_yuan":      order.AmountYuan,
			"carrier":          order.Carrier,
			"tracking_number":  order.TrackingNumber,
			"shipping_address": order.ShippingAddress,
			"warehouse_synced": order.WarehouseSynced,
			"items":            items,
			"created_at":       order.CreatedAt.Format("2006-01-02 15:04:05"),
			"updated_at":       order.UpdatedAt.Format("2006-01-02 15:04:05"),
		}, nil

	case "update_shipping_address":
		orderID, _ := params["order_id"].(string)
		if orderID == "" {
			if o, ok := params["order_no"].(string); ok && o != "" {
				orderID = o
			}
		}
		newAddress, _ := params["new_address"].(string)
		if newAddress == "" {
			if a, ok := params["address"].(string); ok && a != "" {
				newAddress = a
			}
		}

		if orderID == "" || newAddress == "" {
			return nil, fmt.Errorf("order_id and new_address parameters are required")
		}

		var order domain.Order
		err := h.db.Where("account_id = ? AND order_id = ?", accountID, orderID).First(&order).Error
		if err != nil {
			return nil, fmt.Errorf("cannot update address: order '%s' not found in database", orderID)
		}

		// Update the existing database order
		order.ShippingAddress = newAddress
		order.WarehouseSynced = true
		order.SyncedAt = &now
		order.UpdatedAt = now
		if convID != nil && *convID > 0 {
			order.ConversationID = convID
		}
		if saveErr := h.orderRepo.Update(accountID, &order); saveErr != nil {
			return nil, fmt.Errorf("failed to update order in database: %w", saveErr)
		}

		// Also record an activity message on the conversation if conversation_id was provided
		if convID != nil && *convID > 0 {
			actMsg := domain.Message{
				AccountID:      accountID,
				ConversationID: *convID,
				MessageType:    domain.MessageTypeActivity,
				Content:        fmt.Sprintf("【系统变更】订单 %s 收货地址已由 Captain 工具修改为：%s，已同步中台仓库。", orderID, newAddress),
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			_ = h.msgRepo.Create(&actMsg)
		}

		return gin.H{
			"success":          true,
			"action":           "update_shipping_address",
			"order_id":         order.OrderID,
			"updated_address":  order.ShippingAddress,
			"sync_status":      "synced_to_warehouse",
			"warehouse_synced": true,
			"synced_at":        now.Format("2006-01-02 15:04:05"),
			"message":          "收货地址修改成功，已实时同步配送网点与物流中台",
		}, nil

	case "create_ticket":
		title, _ := params["title"].(string)
		if title == "" {
			title = "售后加急工单"
		}
		desc, _ := params["description"].(string)
		priority, _ := params["priority"].(string)
		if priority == "" {
			priority = "high"
		}
		assignedGroup, _ := params["assigned_group"].(string)
		if assignedGroup == "" {
			assignedGroup = "二线技术支持组"
		}

		dueAt := now.Add(12 * time.Hour)
		ticket := domain.Ticket{
			AccountID:      accountID,
			Title:          title,
			Description:    desc,
			Status:         "open",
			Priority:       priority,
			AssignedGroup:  assignedGroup,
			ConversationID: convID,
			DueAt:          &dueAt,
			SLAStatus:      "normal",
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := h.ticketRepo.Create(&ticket); err != nil {
			return nil, fmt.Errorf("failed to create ticket in database: %w", err)
		}

		if convID != nil && *convID > 0 {
			actMsg := domain.Message{
				AccountID:      accountID,
				ConversationID: *convID,
				MessageType:    domain.MessageTypeActivity,
				Content:        fmt.Sprintf("【工单创建】工单 #%s: %s (优先级: %s, 处理组: %s)", ticket.TicketNumber, ticket.Title, ticket.Priority, ticket.AssignedGroup),
				CreatedAt:      now,
				UpdatedAt:      now,
			}
			_ = h.msgRepo.Create(&actMsg)
		}

		return gin.H{
			"ticket_id":       ticket.TicketNumber,
			"id":              ticket.ID,
			"title":           ticket.Title,
			"description":     ticket.Description,
			"status":          ticket.Status,
			"priority":        ticket.Priority,
			"assigned_group":  ticket.AssignedGroup,
			"conversation_id": ticket.ConversationID,
			"created_at":      ticket.CreatedAt.Format("2006-01-02 15:04:05"),
		}, nil

	case "lookup_logistics", "query_logistics":
		trackingNo, _ := params["tracking_number"].(string)
		orderID, _ := params["order_id"].(string)
		if trackingNo == "" && orderID == "" {
			return nil, fmt.Errorf("tracking_number or order_id parameter is required for logistics lookup")
		}

		var order domain.Order
		err := h.db.Where("account_id = ? AND (tracking_number = ? OR order_id = ?)", accountID, trackingNo, orderID).First(&order).Error
		if err != nil {
			return nil, fmt.Errorf("logistics record not found for tracking_number='%s' or order_id='%s'", trackingNo, orderID)
		}

		carrier := order.Carrier
		status := order.OrderStatus
		dest := order.ShippingAddress
		if trackingNo == "" {
			trackingNo = order.TrackingNumber
		}

		var checkpoints []gin.H
		if order.CheckpointsJSON != "" {
			_ = json.Unmarshal([]byte(order.CheckpointsJSON), &checkpoints)
		}
		if checkpoints == nil {
			checkpoints = []gin.H{}
		}

		return gin.H{
			"tracking_number":  trackingNo,
			"carrier":          carrier,
			"status":           status,
			"shipping_address": dest,
			"warehouse_synced": order.WarehouseSynced,
			"checkpoints":      checkpoints,
		}, nil

	case "kb_search":
		kw, _ := params["keyword"].(string)
		var chunks []domain.CaptainDocChunk
		_ = h.db.Where("account_id = ? AND LOWER(content) LIKE ?", accountID, "%"+strings.ToLower(kw)+"%").Limit(3).Find(&chunks)
		return chunks, nil

	default:
		return nil, fmt.Errorf("unrecognized internal built-in function: %s", name)
	}
}

// Helper: HTTP Webhook execution with timeout
func executeHTTPTool(tool *domain.AICustomTool, params map[string]any, timeout time.Duration) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	bodyBytes, _ := json.Marshal(params)
	method := strings.ToUpper(tool.HTTPMethod)
	if method == "" {
		method = "POST"
	}

	req, err := http.NewRequestWithContext(ctx, method, tool.EndpointURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	if tool.Headers != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(tool.Headers), &headers); err == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}

	if err := security.ValidateSafeURL(tool.EndpointURL); err != nil {
		return nil, fmt.Errorf("blocked by SSRF protection: %w", err)
	}

	client := security.NewSafeHTTPClient(timeout)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var jsonResult any
	if err := json.Unmarshal(respBytes, &jsonResult); err == nil {
		return jsonResult, nil
	}

	return string(respBytes), nil
}
