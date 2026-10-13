package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const httpNumericConditions = `[{"attribute_key":"risk_score","custom_attribute_type":"conversation_attribute","custom_attribute_definition_id":17,"filter_operator":"is_greater_than","values":[9007199254740993.0000000000000001]}]`

func numericAdoptionHTTP(t *testing.T) (*gorm.DB, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "adoption.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.AutoMigrate(&domain.AutomationRule{}, &domain.AutomationRuleVersion{}))
	h := AdvancedHandler{automationService: service.NewAutomationService(db, nil, nil)}
	engine := gin.New()
	engine.Use(func(c *gin.Context) { c.Set("account_id", uint(91)); c.Next() })
	engine.POST("/rules", h.CreateAutomationRule)
	engine.PATCH("/rules/:id", h.UpdateAutomationRule)
	engine.POST("/rules/:id/clone", h.CloneAutomationRule)
	return db, engine
}

func numericAdoptionRequest(t *testing.T, engine *gin.Engine, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)
	require.Equal(t, want, w.Code, w.Body.String())
	return w
}

func TestAutomationNumericAdoptionHTTPPreservesProfileAndClone(t *testing.T) {
	db, engine := numericAdoptionHTTP(t)
	response := numericAdoptionRequest(t, engine, http.MethodPost, "/rules", `{"name":"Exact","event_name":"conversation_created","active":false,"condition_semantics":"numeric_custom.v1","conditions":`+httpNumericConditions+`,"actions":[]}`, http.StatusCreated)
	var created struct {
		Data domain.AutomationRule `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
	rule := created.Data
	require.NotZero(t, rule.ID)
	require.Equal(t, domain.AutomationNumericCustomV1, rule.ConditionSemantics)
	require.Contains(t, rule.Conditions, "9007199254740993.0000000000000001")
	path := fmt.Sprintf("/rules/%d", rule.ID)
	numericAdoptionRequest(t, engine, http.MethodPatch, path, `{"name":"Renamed"}`, http.StatusOK)
	clone := numericAdoptionRequest(t, engine, http.MethodPost, path+"/clone", `{}`, http.StatusCreated)
	var copied struct {
		Data domain.AutomationRule `json:"data"`
	}
	require.NoError(t, json.Unmarshal(clone.Body.Bytes(), &copied))
	require.NotEqual(t, rule.ID, copied.Data.ID)
	require.False(t, copied.Data.Active)
	require.Equal(t, domain.AutomationNumericCustomV1, copied.Data.ConditionSemantics)
	require.Equal(t, rule.Conditions, copied.Data.Conditions)
	var versions []domain.AutomationRuleVersion
	require.NoError(t, db.Find(&versions).Error)
	require.Len(t, versions, 3)
	for _, version := range versions {
		require.Equal(t, domain.AutomationNumericCustomV1, version.ConditionSemantics)
		require.Equal(t, rule.Conditions, version.Conditions)
	}
	for _, profile := range []string{`null`, `true`, `17`, `[]`, `{}`, `"future.v1"`} {
		numericAdoptionRequest(t, engine, http.MethodPatch, path, `{"condition_semantics":`+profile+`}`, http.StatusBadRequest)
		numericAdoptionRequest(t, engine, http.MethodPost, "/rules", `{"name":"bad","event_name":"conversation_created","conditions":`+httpNumericConditions+`,"condition_semantics":`+profile+`}`, http.StatusBadRequest)
	}
	numericAdoptionRequest(t, engine, http.MethodPatch, path, `{"conditions":[{"attribute_key":"risk_score","filter_operator":"is_greater_than","values":[1]}]}`, http.StatusBadRequest)
}

func TestAutomationNumericAdoptionHTTPStringAndScopedValidation(t *testing.T) {
	db, engine := numericAdoptionHTTP(t)
	raw := " \n" + httpNumericConditions + " \n"
	encoded, err := json.Marshal(raw)
	require.NoError(t, err)
	numericAdoptionRequest(t, engine, http.MethodPost, "/rules", `{"name":"exact raw","event_name":"conversation_created","active":false,"conditions":`+string(encoded)+`,"condition_semantics":"numeric_custom.v1"}`, http.StatusCreated)
	var frozen domain.AutomationRuleVersion
	require.NoError(t, db.First(&frozen).Error)
	require.Equal(t, raw, frozen.Conditions)
	// Actual custom keys are accepted only by an explicitly adopted numeric leaf.
	require.Error(t, validateAutomationRuleForProfile("conversation_created", httpNumericConditions, nil, false, ""))
	require.NoError(t, validateAutomationRuleForProfile("conversation_created", httpNumericConditions, nil, false, domain.AutomationNumericCustomV1))
	require.Error(t, validateAutomationRuleForProfile("conversation_created", strings.Replace(httpNumericConditions, "is_greater_than", "equal_to", 1), nil, false, domain.AutomationNumericCustomV1))
	require.Error(t, validateAutomationRuleForProfile("conversation_created", httpNumericConditions, `[{"action_name":"unsupported"}]`, false, domain.AutomationNumericCustomV1))
	require.Error(t, validateAutomationRuleForProfile("conversation_created", strings.Replace(httpNumericConditions, `"values":`, `"query_operator":"XOR","values":`, 1), nil, false, domain.AutomationNumericCustomV1))
	// A saved legacy rule never adopts the profile on an ordinary edit.
	legacy := domain.AutomationRule{AccountID: 91, Name: "legacy", EventName: "conversation_created", Conditions: httpNumericConditions, Actions: "[]"}
	require.NoError(t, db.Create(&legacy).Error)
	path := fmt.Sprintf("/rules/%d", legacy.ID)
	numericAdoptionRequest(t, engine, http.MethodPatch, path, `{"name":"legacy renamed"}`, http.StatusOK)
	require.NoError(t, db.First(&legacy, legacy.ID).Error)
	require.Empty(t, legacy.ConditionSemantics)
	numericAdoptionRequest(t, engine, http.MethodPatch, path, `{"condition_semantics":"numeric_custom.v1"}`, http.StatusOK)
	require.NoError(t, db.First(&legacy, legacy.ID).Error)
	require.Equal(t, domain.AutomationNumericCustomV1, legacy.ConditionSemantics)
}

func TestAutomationNumericAdoptionHTTPLegacyModeRejectsActivation(t *testing.T) {
	db, engine := numericAdoptionHTTP(t)
	numericAdoptionRequest(t, engine, http.MethodPost, "/rules", `{"name":"active by default","event_name":"conversation_created","condition_semantics":"numeric_custom.v1","conditions":`+httpNumericConditions+`}`, http.StatusBadRequest)
	created := numericAdoptionRequest(t, engine, http.MethodPost, "/rules", `{"name":"inactive preparation","active":false,"event_name":"conversation_created","condition_semantics":"numeric_custom.v1","conditions":`+httpNumericConditions+`}`, http.StatusCreated)
	var result struct {
		Data domain.AutomationRule `json:"data"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &result))
	path := fmt.Sprintf("/rules/%d", result.Data.ID)
	numericAdoptionRequest(t, engine, http.MethodPatch, path, `{"active":true}`, http.StatusBadRequest)
	numericAdoptionRequest(t, engine, http.MethodPost, path+"/clone", `{"active":true}`, http.StatusBadRequest)
	var count int64
	require.NoError(t, db.Model(&domain.AutomationRule{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
