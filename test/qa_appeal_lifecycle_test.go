package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestQAAppealLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "qa-appeal-test-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Sign up Inspector Admin User (ID=1) & Account (ID=1)
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "原质检员-陈宁",
		"email":        "inspector.chen@example.com",
		"password":     "password123456",
		"account_name": "客服质量管理中心",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d body: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var authResp struct {
		Data struct {
			Token string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
			User struct {
				ID uint `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	inspectorToken := authResp.Data.Token
	inspectorID := authResp.Data.User.ID
	accountID := authResp.Data.Accounts[0].ID

	// Create Agent (ID=2)
	addAgentBody, _ := json.Marshal(map[string]string{
		"name":     "坐席-刘琳",
		"email":    "agent.liu@example.com",
		"password": "Password123!",
		"role":     "agent",
	})
	wAddAgent := httptest.NewRecorder()
	reqAddAgent, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addAgentBody))
	reqAddAgent.Header.Set("Content-Type", "application/json")
	reqAddAgent.Header.Set("Authorization", "Bearer "+inspectorToken)
	engine.ServeHTTP(wAddAgent, reqAddAgent)
	var addAgentResp struct {
		Data domain.User `json:"data"`
	}
	_ = json.Unmarshal(wAddAgent.Body.Bytes(), &addAgentResp)
	agentUserID := addAgentResp.Data.ID

	// Create Independent Reviewer Lead (ID=3)
	addLeadBody, _ := json.Marshal(map[string]string{
		"name":     "复核主管-赵晨",
		"email":    "reviewer.lead@example.com",
		"password": "Password123!",
		"role":     "administrator",
	})
	wAddLead := httptest.NewRecorder()
	reqAddLead, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addLeadBody))
	reqAddLead.Header.Set("Content-Type", "application/json")
	reqAddLead.Header.Set("Authorization", "Bearer "+inspectorToken)
	engine.ServeHTTP(wAddLead, reqAddLead)

	// Login as Independent Reviewer Lead to get leadToken
	loginBody, _ := json.Marshal(map[string]string{
		"email":    "reviewer.lead@example.com",
		"password": "Password123!",
	})
	wLogin := httptest.NewRecorder()
	reqLogin, _ := http.NewRequest("POST", "/auth/sign_in", bytes.NewBuffer(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wLogin, reqLogin)
	var leadAuthResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wLogin.Body.Bytes(), &leadAuthResp)
	leadToken := leadAuthResp.Data.Token

	// Setup a QA Task with Fatal Error initially
	task := domain.QATask{
		AccountID:     accountID,
		TaskNumber:    "QA-20260912-0001",
		TargetType:    "conversation",
		TargetID:      10842,
		TargetRef:     "会话 #10842",
		InspectorID:   &inspectorID,
		AgentID:       &agentUserID,
		TotalScore:    0,
		HasFatalError: true,
		Result:        "致命错误",
		Status:        "rectifying",
	}
	db.Create(&task)

	t.Run("Scenario 1: Direct Appeal Submission with Evidence and Disputed Criteria", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{
			"task_id":                task.ID,
			"reason":                 "客户情绪激动催促，坐席已按合规SOP核验并在内备注案，未违反合规红线",
			"demand_type":            "revoke_fatal",
			"disputed_criterion_ids": []uint{1, 5},
			"evidence_notes":         "参见会话记录第14条与第18条附件截图，客服严格执行了身份二次比对",
			"evidence_urls":          []string{"https://oss.example.com/proof-01.png", "https://oss.example.com/log-02.pdf"},
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals", accountID), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+inspectorToken)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
		}

		var res struct {
			Data struct {
				ID            uint   `json:"id"`
				AppealNumber  string `json:"appeal_number"`
				Status        string `json:"status"`
				DemandType    string `json:"demand_type"`
				OriginalFatal bool   `json:"original_fatal"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &res)

		if res.Data.Status != "pending" {
			t.Fatalf("expected status 'pending', got '%s'", res.Data.Status)
		}
		if res.Data.DemandType != "revoke_fatal" {
			t.Fatalf("expected demand_type 'revoke_fatal', got '%s'", res.Data.DemandType)
		}
		if !res.Data.OriginalFatal {
			t.Fatalf("expected original_fatal to be true")
		}

		// Verify task status updated to 'appealing'
		var updatedTask domain.QATask
		db.First(&updatedTask, task.ID)
		if updatedTask.Status != "appealing" {
			t.Fatalf("expected task status to transition to appealing, got %s", updatedTask.Status)
		}
	})

	t.Run("Scenario 2: Request Supplemental Evidence and Agent Submits Evidence", func(t *testing.T) {
		// 1. Lead requests evidence
		reviewBody, _ := json.Marshal(map[string]any{
			"action":   "need_evidence",
			"comments": "请补充脱敏后的后台操作日志截图以确认操作时间点",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/1/review", accountID), bytes.NewBuffer(reviewBody))
		req.Header.Set("Authorization", "Bearer "+leadToken)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		// Verify status is need_evidence
		var appeal domain.QAAppeal
		db.First(&appeal, 1)
		if appeal.Status != "need_evidence" {
			t.Fatalf("expected appeal status need_evidence, got %s", appeal.Status)
		}

		// 2. Agent submits supplementary evidence
		evBody, _ := json.Marshal(map[string]any{
			"evidence_notes": "已补充提供15:30:12后台操作审计流水快照",
			"evidence_urls":  []string{"https://oss.example.com/audit-proof-03.png"},
		})
		wEv := httptest.NewRecorder()
		reqEv, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/1/evidence", accountID), bytes.NewBuffer(evBody))
		reqEv.Header.Set("Authorization", "Bearer "+inspectorToken)
		reqEv.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wEv, reqEv)

		if wEv.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for evidence submission, got %d: %s", wEv.Code, wEv.Body.String())
		}

		// Verify status transitioned to under_review
		db.First(&appeal, 1)
		if appeal.Status != "under_review" {
			t.Fatalf("expected appeal status transitioned to under_review, got %s", appeal.Status)
		}
	})

	t.Run("Scenario 3: Avoidance Principle Enforcement (Inspector Cannot Review Own Task)", func(t *testing.T) {
		// Attempting review using inspectorToken (inspectorID == task.InspectorID)
		reviewBody, _ := json.Marshal(map[string]any{
			"action":         "adjusted",
			"adjusted_score": 90,
			"comments":       "质检员自我复核",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/1/review", accountID), bytes.NewBuffer(reviewBody))
		req.Header.Set("Authorization", "Bearer "+inspectorToken)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request due to avoidance principle violation, got %d", w.Code)
		}
	})

	t.Run("Scenario 4: Independent Reviewer Adjudication with Score Adjustment & Fatal Error Revocation", func(t *testing.T) {
		reviewBody, _ := json.Marshal(map[string]any{
			"action":         "adjusted",
			"adjusted_score": 88,
			"revoke_fatal":   true,
			"comments":       "查验会话完整录音与后台日志，坐席流程符合合规标准，准予改判并撤销致命红线判定",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/1/review", accountID), bytes.NewBuffer(reviewBody))
		req.Header.Set("Authorization", "Bearer "+leadToken)
		req.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for review adjudication, got %d: %s", w.Code, w.Body.String())
		}

		// Verify Appeal
		var appeal domain.QAAppeal
		db.Preload("Activities").First(&appeal, 1)
		if appeal.Status != "adjusted" {
			t.Fatalf("expected status adjusted, got %s", appeal.Status)
		}
		if appeal.AdjustedScore == nil || *appeal.AdjustedScore != 88 {
			t.Fatalf("expected adjusted_score 88")
		}
		if !appeal.RevokeFatal {
			t.Fatalf("expected revoke_fatal true")
		}
		if len(appeal.Activities) < 3 {
			t.Fatalf("expected at least 3 activity audit trails, got %d", len(appeal.Activities))
		}

		// Verify Task was synced!
		var updatedTask domain.QATask
		db.First(&updatedTask, task.ID)
		if updatedTask.TotalScore != 88 {
			t.Fatalf("expected task total score synced to 88, got %d", updatedTask.TotalScore)
		}
		if updatedTask.HasFatalError {
			t.Fatalf("expected task fatal error to be revoked")
		}
		if updatedTask.Result != "合格" {
			t.Fatalf("expected task result recalculated to '合格', got %s", updatedTask.Result)
		}
		if updatedTask.Status != "completed" {
			t.Fatalf("expected task status 'completed', got %s", updatedTask.Status)
		}
	})

	t.Run("Scenario 5: Audit Integrity Verification List and Decoupling", func(t *testing.T) {
		// Test GET /audit_integrity/verifications
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/audit_integrity/verifications", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+leadToken)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for audit_integrity/verifications, got %d: %s", w.Code, w.Body.String())
		}

		var verifResp struct {
			Data []domain.IntegrityVerification `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &verifResp)
		if len(verifResp.Data) == 0 {
			t.Fatalf("expected non-empty integrity verification list")
		}
		if !verifResp.Data[0].Verified {
			t.Fatalf("expected verified=true")
		}
	})
}
