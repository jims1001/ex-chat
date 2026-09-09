package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
)

type logRecord map[string]any

func parseJSONLogs(raw string) []logRecord {
	var records []logRecord
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec logRecord
		if err := json.Unmarshal([]byte(line), &rec); err == nil {
			records = append(records, rec)
		}
	}
	return records
}

func findLogByComponent(records []logRecord, component string) []logRecord {
	var matched []logRecord
	for _, rec := range records {
		if c, ok := rec["component"].(string); ok && c == component {
			matched = append(matched, rec)
		}
	}
	return matched
}

func TestSystemLoggingObservability(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logBuf := &bytes.Buffer{}
	logger.SetOutput(logBuf, slog.LevelDebug, "json")

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test-secret-system-logging-2026",
		JWTExpirationHours: 24,
		LogLevel:           "debug",
		LogFormat:          "json",
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// ----------------------------------------------------
	// Scenario 1: Auth Lifecycle Logging
	// ----------------------------------------------------
	t.Run("Scenario 1: Auth Lifecycle Structured Logs", func(t *testing.T) {
		logBuf.Reset()

		signUpBody := `{"name":"Observability Admin","email":"obs-admin@example.com","password":"password123","account_name":"Obs Corp"}`
		req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader([]byte(signUpBody)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("sign_up expected 201, got %d body=%s", w.Code, w.Body.String())
		}

		// Invalid login to trigger warn log
		invalidLoginBody := `{"email":"obs-admin@example.com","password":"wrong-password"}`
		reqBad := httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader([]byte(invalidLoginBody)))
		reqBad.Header.Set("Content-Type", "application/json")
		wBad := httptest.NewRecorder()
		r.ServeHTTP(wBad, reqBad)

		if wBad.Code != http.StatusUnauthorized {
			t.Fatalf("sign_in expected 401, got %d", wBad.Code)
		}

		records := parseJSONLogs(logBuf.String())
		authLogs := findLogByComponent(records, "auth")
		if len(authLogs) == 0 {
			t.Fatalf("expected structured logs with component=auth, got none in: %s", logBuf.String())
		}

		foundRegister := false
		foundFailedLogin := false
		for _, log := range authLogs {
			msg, _ := log["msg"].(string)
			if strings.Contains(msg, "new user registered") {
				foundRegister = true
				if log["email"] != "obs-admin@example.com" {
					t.Errorf("expected log email obs-admin@example.com, got %v", log["email"])
				}
			}
			if strings.Contains(msg, "sign-in failed") {
				foundFailedLogin = true
			}
		}

		if !foundRegister {
			t.Errorf("expected register structured log event")
		}
		if !foundFailedLogin {
			t.Errorf("expected failed sign-in structured log event")
		}
	})

	// ----------------------------------------------------
	// Scenario 2: HTTP Middleware Request Logger
	// ----------------------------------------------------
	t.Run("Scenario 2: HTTP Middleware Request Logs", func(t *testing.T) {
		logBuf.Reset()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/widget/config?website_token=invalid-token", nil)
		req.Header.Set(foundation.HeaderRequestID, "req-trace-001")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		records := parseJSONLogs(logBuf.String())
		httpLogs := findLogByComponent(records, "http")
		if len(httpLogs) == 0 {
			t.Fatalf("expected HTTP middleware structured logs, got none in: %s", logBuf.String())
		}

		foundTrace := false
		for _, log := range httpLogs {
			if log["request_id"] == "req-trace-001" {
				foundTrace = true
				if log["method"] != "GET" {
					t.Errorf("expected method GET, got %v", log["method"])
				}
				if _, ok := log["latency_ms"].(float64); !ok {
					t.Errorf("expected latency_ms numeric field in HTTP log")
				}
			}
		}

		if !foundTrace {
			t.Errorf("expected HTTP middleware log to capture request_id req-trace-001")
		}
	})

	// ----------------------------------------------------
	// Scenario 3: Conversation & Message Creation Logs
	// ----------------------------------------------------
	t.Run("Scenario 3: Conversation & Message Creation Logs", func(t *testing.T) {
		logBuf.Reset()

		var account domain.Account
		_ = db.First(&account).Error
		var user domain.User
		_ = db.First(&user).Error

		inbox := domain.Inbox{
			AccountID:   account.ID,
			Name:        "Obs Inbox",
			ChannelType: "Channel::WebWidget",
		}
		_ = db.Create(&inbox).Error

		contact := domain.Contact{
			AccountID: account.ID,
			Name:      "Obs Customer",
			Email:     "customer-obs@example.com",
		}
		_ = db.Create(&contact).Error

		// Create conversation API
		token, _ := auth.GenerateToken(&user, cfg.JWTSecret, 24)
		convPayload := map[string]any{
			"inbox_id":   inbox.ID,
			"contact_id": contact.ID,
			"priority":   "high",
		}
		convBody, _ := json.Marshal(convPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", account.ID), bytes.NewReader(convBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for conversation, got %d body=%s", w.Code, w.Body.String())
		}

		var createdConv struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &createdConv)

		// Create message API
		msgPayload := map[string]any{
			"content":      "Hello observability message!",
			"message_type": "outgoing",
			"private":      false,
		}
		msgBody, _ := json.Marshal(msgPayload)
		reqMsg := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", account.ID, createdConv.Data.ID), bytes.NewReader(msgBody))
		reqMsg.Header.Set("Authorization", "Bearer "+token)
		reqMsg.Header.Set("Content-Type", "application/json")
		wMsg := httptest.NewRecorder()
		r.ServeHTTP(wMsg, reqMsg)

		if wMsg.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for message, got %d body=%s", wMsg.Code, wMsg.Body.String())
		}

		records := parseJSONLogs(logBuf.String())
		convLogs := findLogByComponent(records, "conversation")
		msgLogs := findLogByComponent(records, "message")

		if len(convLogs) == 0 {
			t.Errorf("expected conversation component logs")
		}
		if len(msgLogs) == 0 {
			t.Errorf("expected message component logs")
		}

		foundConvCreated := false
		for _, l := range convLogs {
			if strings.Contains(fmt.Sprint(l["msg"]), "conversation created") {
				foundConvCreated = true
				if l["account_id"] != float64(account.ID) {
					t.Errorf("expected account_id %d, got %v", account.ID, l["account_id"])
				}
			}
		}
		if !foundConvCreated {
			t.Errorf("expected conversation created structured log")
		}

		foundMsgCreated := false
		for _, l := range msgLogs {
			if strings.Contains(fmt.Sprint(l["msg"]), "message created") {
				foundMsgCreated = true
			}
		}
		if !foundMsgCreated {
			t.Errorf("expected message created structured log")
		}
	})

	// ----------------------------------------------------
	// Scenario 4: Automation Rule & Cloning Logs
	// ----------------------------------------------------
	t.Run("Scenario 4: Automation Rule & Cloning Logs", func(t *testing.T) {
		logBuf.Reset()

		var account domain.Account
		_ = db.First(&account).Error
		var user domain.User
		_ = db.First(&user).Error
		token, _ := auth.GenerateToken(&user, cfg.JWTSecret, 24)

		rulePayload := map[string]any{
			"name":        "Observability Alert Rule",
			"event_name":  "conversation_created",
			"description": "Trigger alert on new tickets",
			"conditions":  `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`,
			"actions":     `[{"action_name":"add_label","action_params":["ALERTED"]}]`,
			"active":      true,
		}
		body, _ := json.Marshal(rulePayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules", account.ID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 for automation rule creation, got %d", w.Code)
		}

		var ruleResp struct {
			Data domain.AutomationRule `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &ruleResp)

		// Clone the rule
		reqClone := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/automation_rules/%d/clone", account.ID, ruleResp.Data.ID), bytes.NewReader([]byte("{}")))
		reqClone.Header.Set("Authorization", "Bearer "+token)
		reqClone.Header.Set("Content-Type", "application/json")
		wClone := httptest.NewRecorder()
		r.ServeHTTP(wClone, reqClone)

		if wClone.Code != http.StatusCreated {
			t.Fatalf("expected 201 for rule clone, got %d", wClone.Code)
		}

		records := parseJSONLogs(logBuf.String())
		autoLogs := findLogByComponent(records, "automation")
		if len(autoLogs) == 0 {
			t.Fatalf("expected automation component logs, got none in: %s", logBuf.String())
		}

		foundClone := false
		for _, l := range autoLogs {
			if strings.Contains(fmt.Sprint(l["msg"]), "automation rule cloned") {
				foundClone = true
				if l["source_rule_id"] != float64(ruleResp.Data.ID) {
					t.Errorf("expected source_rule_id %d, got %v", ruleResp.Data.ID, l["source_rule_id"])
				}
			}
		}
		if !foundClone {
			t.Errorf("expected automation rule cloned log event")
		}
	})

	// ----------------------------------------------------
	// Scenario 5: Historical Data Migration Observability
	// ----------------------------------------------------
	t.Run("Scenario 5: Historical Data Migration Observability", func(t *testing.T) {
		logBuf.Reset()

		var account domain.Account
		_ = db.First(&account).Error

		migrationService := service.NewMigrationService(db)
		rawData := `[{"name":"Legacy Client","email":"legacy@example.com"}]`
		stats, err := migrationService.Migrate(context.Background(), account.ID, "contacts", rawData)
		if err != nil {
			t.Fatalf("migration failed: %v", err)
		}
		if stats.TotalProcessed() != 1 {
			t.Fatalf("expected 1 processed record, got %d", stats.TotalProcessed())
		}

		records := parseJSONLogs(logBuf.String())
		migrationLogs := findLogByComponent(records, "migration")
		if len(migrationLogs) == 0 {
			t.Fatalf("expected migration component logs, got none in: %s", logBuf.String())
		}

		foundStart := false
		foundEnd := false
		for _, l := range migrationLogs {
			msg := fmt.Sprint(l["msg"])
			if strings.Contains(msg, "starting historical data migration") {
				foundStart = true
			}
			if strings.Contains(msg, "historical data migration completed") {
				foundEnd = true
				if l["processed"] != float64(1) {
					t.Errorf("expected log processed 1, got %v", l["processed"])
				}
			}
		}

		if !foundStart || !foundEnd {
			t.Errorf("expected both migration start and end log records")
		}
	})

	// ----------------------------------------------------
	// Scenario 6: GORM Database Structured Logging Bridge
	// ----------------------------------------------------
	t.Run("Scenario 6: GORM Database Structured Logging Bridge", func(t *testing.T) {
		logBuf.Reset()

		gormLog := logger.NewGORMLogger(time.Millisecond)
		gormLog.Trace(context.Background(), time.Now().Add(-5*time.Millisecond), func() (string, int64) {
			return "SELECT * FROM users WHERE id = 1", 1
		}, nil)

		records := parseJSONLogs(logBuf.String())
		gormLogs := findLogByComponent(records, "gorm")
		if len(gormLogs) == 0 {
			t.Fatalf("expected gorm component logs, got none in: %s", logBuf.String())
		}

		if gormLogs[0]["sql"] != "SELECT * FROM users WHERE id = 1" {
			t.Errorf("expected sql in log, got %v", gormLogs[0]["sql"])
		}
	})
}
