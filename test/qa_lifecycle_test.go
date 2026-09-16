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

func TestQALifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "qa-lifecycle-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Sign up Inspector Admin User & Account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "质检主管",
		"email":        "qa.admin@example.com",
		"password":     "password123456",
		"account_name": "质量管理卓越中心",
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
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	inspectorID := authResp.Data.User.ID

	// Create a secondary agent user (agent being reviewed)
	addAgentBody, _ := json.Marshal(map[string]string{
		"name":     "一线客服小陈",
		"email":    "agent.chen@example.com",
		"password": "Password123!",
		"role":     "agent",
	})
	wAddAgent := httptest.NewRecorder()
	reqAddAgent, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addAgentBody))
	reqAddAgent.Header.Set("Content-Type", "application/json")
	reqAddAgent.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wAddAgent, reqAddAgent)
	var addAgentResp struct {
		Data domain.User `json:"data"`
	}
	_ = json.Unmarshal(wAddAgent.Body.Bytes(), &addAgentResp)
	agentUserID := addAgentResp.Data.ID

	// Create a tertiary user (independent supervisor for appeal review)
	addLeadBody, _ := json.Marshal(map[string]string{
		"name":     "服务复核总监",
		"email":    "lead.director@example.com",
		"password": "Password123!",
		"role":     "administrator",
	})
	wAddLead := httptest.NewRecorder()
	reqAddLead, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addLeadBody))
	reqAddLead.Header.Set("Content-Type", "application/json")
	reqAddLead.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wAddLead, reqAddLead)

	// 1. Scenario 1: Scorecard & Criteria Management
	var scorecardID uint
	var criteriaList []domain.QACriterion
	t.Run("Scenario 1: Scorecard and Criteria Configuration", func(t *testing.T) {
		createCardBody, _ := json.Marshal(map[string]interface{}{
			"name":          "技术支持全方位质检表",
			"description":   "用于高级技术支持团队的服务规范与故障解决评价",
			"total_score":   100,
			"passing_score": 85,
			"criteria": []map[string]interface{}{
				{
					"category":    "流程符合",
					"title":       "安全验证与日志排查执行",
					"description": "严格核对操作权限并收集底层错误代码",
					"max_score":   30,
					"is_fatal":    false,
					"order_index": 1,
				},
				{
					"category":    "解决质量",
					"title":       "排错分析准确度与方案交付",
					"description": "提供精准的调试建议或修复工单",
					"max_score":   40,
					"is_fatal":    false,
					"order_index": 2,
				},
				{
					"category":    "沟通体验",
					"title":       "技术表达清晰度与专业礼貌",
					"description": "无生硬反问，沟通清晰通顺",
					"max_score":   30,
					"is_fatal":    false,
					"order_index": 3,
				},
				{
					"category":    "合规红线",
					"title":       "严禁私自获取或篡改生产环境密码",
					"description": "一旦发现违规获取私钥或密码，一票否决",
					"max_score":   0,
					"is_fatal":    true,
					"order_index": 4,
				},
			},
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/scorecards", accountID), bytes.NewBuffer(createCardBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 on create scorecard, got %d body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.QAScorecard `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		scorecardID = resp.Data.ID
		if scorecardID == 0 {
			t.Fatalf("expected non-zero scorecard ID")
		}

		// Retrieve and check criteria
		wGet := httptest.NewRecorder()
		reqGet, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/scorecards/%d", accountID, scorecardID), nil)
		reqGet.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wGet, reqGet)

		if wGet.Code != http.StatusOK {
			t.Fatalf("expected 200 on get scorecard, got %d", wGet.Code)
		}
		var getResp struct {
			Data domain.QAScorecard `json:"data"`
		}
		_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)
		if len(getResp.Data.Criteria) != 4 {
			t.Fatalf("expected 4 criteria, got %d", len(getResp.Data.Criteria))
		}
		criteriaList = getResp.Data.Criteria
	})

	// 2. Scenario 2: Sampling Rule & Automatic Task Generation
	var sampledTaskID uint
	t.Run("Scenario 2: Sampling Rules and Generation", func(t *testing.T) {
		// Seed a completed ticket to be sampled
		seedTicket := domain.Ticket{
			AccountID:     accountID,
			TicketNumber:  "TK-SAMPLE-2026",
			Title:         "API 响应延迟故障排除",
			Status:        "resolved",
			Priority:      "high",
			AssignedGroup: "技术支持",
			AssigneeID:    &agentUserID,
		}
		db.Create(&seedTicket)

		ruleBody, _ := json.Marshal(map[string]interface{}{
			"name":                  "重点故障工单抽检策略",
			"target_type":           "ticket",
			"scorecard_id":          scorecardID,
			"sampling_rate":         20.0,
			"assigned_inspector_id": inspectorID,
		})

		wRule := httptest.NewRecorder()
		reqRule, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/sampling_rules", accountID), bytes.NewBuffer(ruleBody))
		reqRule.Header.Set("Content-Type", "application/json")
		reqRule.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wRule, reqRule)

		if wRule.Code != http.StatusCreated {
			t.Fatalf("expected 201 on create sampling rule, got %d body: %s", wRule.Code, wRule.Body.String())
		}

		var ruleResp struct {
			Data domain.QASamplingRule `json:"data"`
		}
		_ = json.Unmarshal(wRule.Body.Bytes(), &ruleResp)
		ruleID := ruleResp.Data.ID

		// Run sampling
		wRun := httptest.NewRecorder()
		reqRun, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/sampling_rules/%d/run", accountID, ruleID), nil)
		reqRun.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wRun, reqRun)

		if wRun.Code != http.StatusOK {
			t.Fatalf("expected 200 on run sampling, got %d body: %s", wRun.Code, wRun.Body.String())
		}

		var runResp struct {
			Data  []domain.QATask `json:"data"`
			Count int             `json:"count"`
		}
		_ = json.Unmarshal(wRun.Body.Bytes(), &runResp)
		if runResp.Count < 1 || len(runResp.Data) < 1 {
			t.Fatalf("expected at least 1 task generated from sampling, got %d", runResp.Count)
		}
		sampledTaskID = runResp.Data[0].ID
		if runResp.Data[0].TaskNumber == "" {
			t.Errorf("expected generated task to have QA-xxx task number")
		}
	})

	// 3. Scenario 3: Task Evaluation & Passing Grade
	t.Run("Scenario 3: Normal Task Evaluation and Scoring Calculation", func(t *testing.T) {
		// Grade task: 28/30 + 38/40 + 26/30 = 92 points (合格)
		evalBody, _ := json.Marshal(map[string]interface{}{
			"feedback": "问题排查思路清晰，方案交付准确，符合标准规范。",
			"evaluations": []map[string]interface{}{
				{
					"criterion_id":     criteriaList[0].ID,
					"criterion_title":  criteriaList[0].Title,
					"category":         criteriaList[0].Category,
					"max_score":        criteriaList[0].MaxScore,
					"score":            28,
					"deduction_reason": "扣2分：日志关键时间戳提取稍有延迟",
				},
				{
					"criterion_id":     criteriaList[1].ID,
					"criterion_title":  criteriaList[1].Title,
					"category":         criteriaList[1].Category,
					"max_score":        criteriaList[1].MaxScore,
					"score":            38,
					"deduction_reason": "扣2分：未附带配置模板",
				},
				{
					"criterion_id":     criteriaList[2].ID,
					"criterion_title":  criteriaList[2].Title,
					"category":         criteriaList[2].Category,
					"max_score":        criteriaList[2].MaxScore,
					"score":            26,
					"deduction_reason": "扣4分：用语可更具安抚感",
				},
			},
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/evaluate", accountID, sampledTaskID), bytes.NewBuffer(evalBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on evaluate, got %d body: %s", w.Code, w.Body.String())
		}

		var evalResp struct {
			Data domain.QATask `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &evalResp)
		if evalResp.Data.TotalScore != 92 {
			t.Errorf("expected total score 92, got %d", evalResp.Data.TotalScore)
		}
		if evalResp.Data.Result != "合格" {
			t.Errorf("expected result '合格', got %s", evalResp.Data.Result)
		}
		if evalResp.Data.Status != "completed" {
			t.Errorf("expected status 'completed', got %s", evalResp.Data.Status)
		}
	})

	// 4. Scenario 4: Fatal Error One-Vote Veto Evaluation
	var fatalTaskID uint
	t.Run("Scenario 4: Fatal Error Veto Evaluation", func(t *testing.T) {
		// Create a new task
		createTaskBody, _ := json.Marshal(map[string]interface{}{
			"target_type": "conversation",
			"target_id":   10899,
			"target_ref":  "会话 #10899",
			"scorecard_id": scorecardID,
			"agent_id":    agentUserID,
		})
		wCreate := httptest.NewRecorder()
		reqCreate, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks", accountID), bytes.NewBuffer(createTaskBody))
		reqCreate.Header.Set("Content-Type", "application/json")
		reqCreate.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wCreate, reqCreate)
		if wCreate.Code != http.StatusCreated {
			t.Fatalf("expected 201 on create task, got %d", wCreate.Code)
		}
		var cr struct {
			Data domain.QATask `json:"data"`
		}
		_ = json.Unmarshal(wCreate.Body.Bytes(), &cr)
		fatalTaskID = cr.Data.ID

		// Grade with fatal error triggered
		evalBody, _ := json.Marshal(map[string]interface{}{
			"feedback": "触碰合规红线：客服要求客户通过聊天发送明文数据库密码",
			"evaluations": []map[string]interface{}{
				{
					"criterion_id":     criteriaList[0].ID,
					"criterion_title":  criteriaList[0].Title,
					"max_score":        30,
					"score":            30,
				},
				{
					"criterion_id":       criteriaList[3].ID, // Fatal item
					"criterion_title":    criteriaList[3].Title,
					"max_score":          0,
					"score":              0,
					"is_fatal_triggered": true,
					"deduction_reason":   "一票否决：索要客户生产环境明文凭证",
				},
			},
		})

		wEval := httptest.NewRecorder()
		reqEval, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/evaluate", accountID, fatalTaskID), bytes.NewBuffer(evalBody))
		reqEval.Header.Set("Content-Type", "application/json")
		reqEval.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wEval, reqEval)

		if wEval.Code != http.StatusOK {
			t.Fatalf("expected 200 on evaluate fatal, got %d", wEval.Code)
		}

		var fatalResp struct {
			Data domain.QATask `json:"data"`
		}
		_ = json.Unmarshal(wEval.Body.Bytes(), &fatalResp)
		if !fatalResp.Data.HasFatalError {
			t.Errorf("expected has_fatal_error to be true")
		}
		if fatalResp.Data.TotalScore != 0 {
			t.Errorf("expected total score forced to 0 on fatal error, got %d", fatalResp.Data.TotalScore)
		}
		if fatalResp.Data.Result != "致命错误" {
			t.Errorf("expected result '致命错误', got %s", fatalResp.Data.Result)
		}
		if fatalResp.Data.Status != "rectifying" {
			t.Errorf("expected status 'rectifying' on fatal error, got %s", fatalResp.Data.Status)
		}
	})

	// 5. Scenario 5: Rectification Plan & Confirmation Lifecycle
	t.Run("Scenario 5: Rectification Plan and Closure", func(t *testing.T) {
		// Agent submits improvement plan
		rectifyBody, _ := json.Marshal(map[string]interface{}{
			"plan": "立即停止不当索证行为，复习《客户凭证安全守则》，在后续会话中通过统一工单安全通道接收授权。",
		})
		wRec := httptest.NewRecorder()
		reqRec, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/rectify", accountID, fatalTaskID), bytes.NewBuffer(rectifyBody))
		reqRec.Header.Set("Content-Type", "application/json")
		reqRec.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wRec, reqRec)

		if wRec.Code != http.StatusOK {
			t.Fatalf("expected 200 on rectify, got %d body: %s", wRec.Code, wRec.Body.String())
		}

		// Inspector confirms rectification and closes task
		wConfirm := httptest.NewRecorder()
		reqConfirm, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/confirm_rectification", accountID, fatalTaskID), nil)
		reqConfirm.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wConfirm, reqConfirm)

		if wConfirm.Code != http.StatusOK {
			t.Fatalf("expected 200 on confirm rectification, got %d body: %s", wConfirm.Code, wConfirm.Body.String())
		}

		var closedResp struct {
			Data domain.QATask `json:"data"`
		}
		_ = json.Unmarshal(wConfirm.Body.Bytes(), &closedResp)
		if closedResp.Data.Status != "closed" {
			t.Errorf("expected status 'closed', got %s", closedResp.Data.Status)
		}
		if closedResp.Data.RectifiedAt == nil {
			t.Errorf("expected rectified_at timestamp to be set")
		}
	})

	// 6. Scenario 6: Appeal & Avoidance Principle Checks
	t.Run("Scenario 6: Appeal Submission and Avoidance Principle Adjudication", func(t *testing.T) {
		// Agent appeals the first task (score: 92) requesting upgrade to 96
		appealBody, _ := json.Marshal(map[string]interface{}{
			"reason": "排错分析中已提供了完整的排查命令链，客户在第4轮已确认问题解决，申请调整扣分至优秀档。",
		})
		wAppeal := httptest.NewRecorder()
		reqAppeal, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/appeal", accountID, sampledTaskID), bytes.NewBuffer(appealBody))
		reqAppeal.Header.Set("Content-Type", "application/json")
		reqAppeal.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wAppeal, reqAppeal)

		if wAppeal.Code != http.StatusCreated {
			t.Fatalf("expected 201 on appeal, got %d body: %s", wAppeal.Code, wAppeal.Body.String())
		}

		var appealResp struct {
			Data domain.QAAppeal `json:"data"`
		}
		_ = json.Unmarshal(wAppeal.Body.Bytes(), &appealResp)
		appealID := appealResp.Data.ID

		// Attempt 1: Original inspector tries to review own QA task -> MUST BE REJECTED by Avoidance Principle
		reviewBody, _ := json.Marshal(map[string]interface{}{
			"action":   "upheld",
			"comments": "原质检员自行维持原判",
		})
		wAvoid := httptest.NewRecorder()
		reqAvoid, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d/review", accountID, appealID), bytes.NewBuffer(reviewBody))
		reqAvoid.Header.Set("Content-Type", "application/json")
		reqAvoid.Header.Set("Authorization", "Bearer "+token) // token belongs to inspectorID
		engine.ServeHTTP(wAvoid, reqAvoid)

		if wAvoid.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request due to avoidance principle violation, got %d body: %s", wAvoid.Code, wAvoid.Body.String())
		}

		// Attempt 2: Independent director reviews the appeal directly via database / new session
		// Login as leadUser to get independent token
		wLoginLead := httptest.NewRecorder()
		loginLeadBody, _ := json.Marshal(map[string]string{
			"email":    "lead.director@example.com",
			"password": "Password123!",
		})
		reqLoginLead, _ := http.NewRequest("POST", "/auth/sign_in", bytes.NewBuffer(loginLeadBody))
		reqLoginLead.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wLoginLead, reqLoginLead)
		if wLoginLead.Code != http.StatusOK {
			t.Fatalf("lead login failed: %d body: %s", wLoginLead.Code, wLoginLead.Body.String())
		}
		var leadAuth struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wLoginLead.Body.Bytes(), &leadAuth)
		leadToken := leadAuth.Data.Token

		// Independent review succeeds and adjusts score to 96
		adjScore := 96
		leadReviewBody, _ := json.Marshal(map[string]interface{}{
			"action":         "adjusted",
			"adjusted_score": adjScore,
			"comments":       "经复核主管复审，客服给出的调试命令确属最佳实践，予以调整至96分（优秀）。",
		})
		wRevLead := httptest.NewRecorder()
		reqRevLead, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d/review", accountID, appealID), bytes.NewBuffer(leadReviewBody))
		reqRevLead.Header.Set("Content-Type", "application/json")
		reqRevLead.Header.Set("Authorization", "Bearer "+leadToken)
		engine.ServeHTTP(wRevLead, reqRevLead)

		if wRevLead.Code != http.StatusOK {
			t.Fatalf("expected 200 on independent review, got %d body: %s", wRevLead.Code, wRevLead.Body.String())
		}

		var revResp struct {
			Data domain.QAAppeal `json:"data"`
		}
		_ = json.Unmarshal(wRevLead.Body.Bytes(), &revResp)
		if revResp.Data.Status != "adjusted" {
			t.Errorf("expected status 'adjusted', got %s", revResp.Data.Status)
		}
		if revResp.Data.AdjustedScore == nil || *revResp.Data.AdjustedScore != 96 {
			t.Errorf("expected adjusted score 96")
		}

		// Verify task status and score updated
		wTaskGet := httptest.NewRecorder()
		reqTaskGet, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d", accountID, sampledTaskID), nil)
		reqTaskGet.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wTaskGet, reqTaskGet)
		var tgResp struct {
			Data domain.QATask `json:"data"`
		}
		_ = json.Unmarshal(wTaskGet.Body.Bytes(), &tgResp)
		if tgResp.Data.TotalScore != 96 {
			t.Errorf("expected task score updated to 96, got %d", tgResp.Data.TotalScore)
		}
		if tgResp.Data.Result != "优秀" {
			t.Errorf("expected task result upgraded to '优秀', got %s", tgResp.Data.Result)
		}
	})

	// 7. Scenario 7: QA Dashboard Metrics
	t.Run("Scenario 7: QA Dashboard Statistics", func(t *testing.T) {
		wTaskStats := httptest.NewRecorder()
		reqTaskStats, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/stats", accountID), nil)
		reqTaskStats.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wTaskStats, reqTaskStats)

		if wTaskStats.Code != http.StatusOK {
			t.Fatalf("expected 200 on task stats, got %d", wTaskStats.Code)
		}

		var tsResp struct {
			Data map[string]interface{} `json:"data"`
		}
		_ = json.Unmarshal(wTaskStats.Body.Bytes(), &tsResp)
		if tsResp.Data["total"] == nil {
			t.Errorf("expected total in task stats")
		}

		wAppealStats := httptest.NewRecorder()
		reqAppealStats, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/stats", accountID), nil)
		reqAppealStats.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wAppealStats, reqAppealStats)

		if wAppealStats.Code != http.StatusOK {
			t.Fatalf("expected 200 on appeal stats, got %d", wAppealStats.Code)
		}

		var asResp struct {
			Data map[string]interface{} `json:"data"`
		}
		_ = json.Unmarshal(wAppealStats.Body.Bytes(), &asResp)
		if asResp.Data["monthly_total"] == nil {
			t.Errorf("expected monthly_total in appeal stats")
		}
	})

	// 8. Scenario 8: Multi-tenant Security Isolation
	t.Run("Scenario 8: Multi-tenant Security Isolation", func(t *testing.T) {
		otherAccount := domain.Account{Name: "外部租户质检隔离"}
		db.Create(&otherAccount)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d", otherAccount.ID, sampledTaskID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
			t.Errorf("expected 404 or 403 for unauthorized cross-tenant QA access, got %d", w.Code)
		}
	})
}
