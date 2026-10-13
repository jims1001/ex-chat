package handler

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestAutomationHistoryPreservesExistingExposedHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "history.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrateModule(db, "AUT"); err != nil {
		t.Fatal(err)
	}
	handler := AdvancedHandler{automationService: service.NewAutomationService(db, nil, nil)}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		c.Set("account_id", uint(1))
		c.Header("Access-Control-Expose-Headers", "X-Request-Id")
		c.Header("Access-Control-Allow-Origin", "https://frontend.example")
		c.Next()
	})
	engine.GET("/history", handler.ListAutomationRuleExecutions)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/history", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	exposed := response.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(exposed, "X-Request-Id") || !strings.Contains(exposed, "X-Next-Cursor") {
		t.Fatalf("existing/new exposed headers missing: %s", exposed)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "https://frontend.example" {
		t.Fatal("history changed allowed origin")
	}
}
