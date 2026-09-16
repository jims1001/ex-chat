package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
)

func setupQATestDB(t *testing.T) (*gorm.DB, *gin.Engine) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	err = db.AutoMigrate(
		&domain.Account{},
		&domain.User{},
		&domain.AccountUser{},
		&domain.Conversation{},
		&domain.Ticket{},
		&domain.QAScorecard{},
		&domain.QACriterion{},
		&domain.QASamplingRule{},
		&domain.QATask{},
		&domain.QAEvaluationScore{},
		&domain.QAAppeal{},
		&domain.QAAppealActivity{},
		&domain.LocalChangeJournal{},
		&domain.AuditLog{},
	)
	assert.NoError(t, err)

	// Seed account, users (admin, inspector, agent, reviewer)
	acc := domain.Account{ID: 1, Name: "QA Test Account"}
	db.Create(&acc)

	admin := domain.User{ID: 1, Email: "admin@example.com", Name: "系统管理员", Role: domain.RoleAdministrator}
	inspector := domain.User{ID: 2, Email: "inspector@example.com", Name: "质检主管刘琳", Role: domain.RoleAgent}
	agent := domain.User{ID: 3, Email: "agent@example.com", Name: "客服陈宁", Role: domain.RoleAgent}
	reviewer := domain.User{ID: 4, Email: "reviewer@example.com", Name: "独立复核专家赵晨", Role: domain.RoleAdministrator}
	db.Create(&admin)
	db.Create(&inspector)
	db.Create(&agent)
	db.Create(&reviewer)

	db.Create(&domain.AccountUser{AccountID: 1, UserID: 1, Role: domain.RoleAdministrator})
	db.Create(&domain.AccountUser{AccountID: 1, UserID: 2, Role: domain.RoleAgent})
	db.Create(&domain.AccountUser{AccountID: 1, UserID: 3, Role: domain.RoleAgent})
	db.Create(&domain.AccountUser{AccountID: 1, UserID: 4, Role: domain.RoleAdministrator})

	// Seed a test conversation
	conv := domain.Conversation{ID: 10842, AccountID: 1, Status: "resolved"}
	db.Create(&conv)

	accountRepo := repository.NewAccountRepository(db)
	userRepo := repository.NewUserRepository(db)
	qaRepo := repository.NewQARepository(db)
	ticketRepo := repository.NewTicketRepository(db)

	authMiddleware := func(c *gin.Context) {
		c.Set("account_id", uint(1))
		// Default to admin or user header
		userID := uint(1)
		if uidStr := c.GetHeader("X-User-ID"); uidStr != "" {
			var parsed uint
			fmt.Sscanf(uidStr, "%d", &parsed)
			if parsed > 0 {
				userID = parsed
			}
		}
		c.Set("user_id", userID)
		c.Next()
	}

	r := gin.New()
	r.Use(gin.Recovery())

	tenant := r.Group("/api/v1/accounts/:account_id")
	tenant.Use(authMiddleware)

	qaHandler := handler.NewQAHandler(qaRepo)
	ticketHandler := handler.NewTicketHandler(ticketRepo)

	// Scorecards
	tenant.GET("/qa/scorecards", qaHandler.ListScorecards)
	tenant.POST("/qa/scorecards", qaHandler.CreateScorecard)
	tenant.GET("/qa/scorecards/:id", qaHandler.GetScorecard)

	// Sampling rules
	tenant.GET("/qa/sampling_rules", qaHandler.ListSamplingRules)
	tenant.POST("/qa/sampling_rules", qaHandler.CreateSamplingRule)
	tenant.POST("/qa/sampling_rules/:id/run", qaHandler.RunSamplingRule)

	// Tasks
	tenant.GET("/qa/tasks", qaHandler.ListTasks)
	tenant.POST("/qa/tasks", qaHandler.CreateTask)
	tenant.GET("/qa/tasks/stats", qaHandler.TaskStats)
	tenant.GET("/qa/tasks/:id", qaHandler.GetTask)
	tenant.POST("/qa/tasks/:id/evaluate", qaHandler.EvaluateTask)
	tenant.POST("/qa/tasks/:id/rectify", qaHandler.RectifyTask)
	tenant.POST("/qa/tasks/:id/confirm_rectification", qaHandler.ConfirmRectification)
	tenant.POST("/qa/tasks/:id/appeal", qaHandler.CreateAppeal)

	// Appeals
	tenant.GET("/qa/appeals", qaHandler.ListAppeals)
	tenant.POST("/qa/appeals", qaHandler.CreateAppealDirect)
	tenant.GET("/qa/appeals/stats", qaHandler.AppealStats)
	tenant.GET("/qa/appeals/:id", qaHandler.GetAppeal)
	tenant.POST("/qa/appeals/:id/evidence", qaHandler.SubmitAppealEvidence)
	tenant.POST("/qa/appeals/:id/review", qaHandler.ReviewAppeal)

	// Reports
	reportsGroup := tenant.Group("/reports")
	reportsGroup.GET("/qa_summary", qaHandler.GetQASummaryReport)

	_ = accountRepo
	_ = userRepo
	_ = ticketHandler
	_ = router.SetupRouter

	return db, r
}

func TestQASystemFull9Capabilities(t *testing.T) {
	_, r := setupQATestDB(t)

	var scorecardID uint
	var criterion1ID uint
	var criterion2ID uint
	var taskID uint
	var appealID uint

	t.Run("1. 质检模板与评分项管理 (Scorecard & Criteria)", func(t *testing.T) {
		body := map[string]interface{}{
			"name":          "电商服务标准质检表",
			"description":   "用于电商订单、物流咨询及退款服务的质检评分标准",
			"total_score":   100,
			"passing_score": 85,
			"criteria": []map[string]interface{}{
				{
					"category":    "流程规范",
					"title":       "问候语及标准规范用语",
					"description": "开场需致以标准品牌问候语，结语感谢并告知服务评价。",
					"max_score":   30,
					"is_fatal":    false,
					"order_index": 1,
				},
				{
					"category":    "业务解决",
					"title":       "问题定性与准确解决",
					"description": "严格核查订单状态，给出准确物流或退款时效方案。",
					"max_score":   50,
					"is_fatal":    false,
					"order_index": 2,
				},
				{
					"category":    "合规红线",
					"title":       "严禁诱导私下交易或泄露客户隐私",
					"description": "严重红线项，一票否决为0分。",
					"max_score":   20,
					"is_fatal":    true,
					"order_index": 3,
				},
			},
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "/api/v1/accounts/1/qa/scorecards", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		cardData := resp["data"].(map[string]interface{})
		scorecardID = uint(cardData["id"].(float64))
		assert.NotEmpty(t, scorecardID)

		criteria := cardData["criteria"].([]interface{})
		assert.Len(t, criteria, 3)
		c1 := criteria[0].(map[string]interface{})
		criterion1ID = uint(c1["id"].(float64))
		c2 := criteria[1].(map[string]interface{})
		criterion2ID = uint(c2["id"].(float64))
	})

	t.Run("2. 自动与人工抽检规则 (Sampling Rules)", func(t *testing.T) {
		body := map[string]interface{}{
			"name":                  "低满意度与超时会话自动抽检",
			"target_type":           "conversation",
			"scorecard_id":          scorecardID,
			"sampling_rate":         10.0,
			"conditions":            `{"status":"resolved"}`,
			"assigned_inspector_id": 2, // inspector 刘琳
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "/api/v1/accounts/1/qa/sampling_rules", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		ruleData := resp["data"].(map[string]interface{})
		ruleID := uint(ruleData["id"].(float64))

		// 执行抽检规则
		runReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/sampling_rules/%d/run", ruleID), nil)
		wRun := httptest.NewRecorder()
		r.ServeHTTP(wRun, runReq)
		assert.Equal(t, http.StatusOK, wRun.Code)
	})

	t.Run("3. 质检任务创建与分配 (Task Creation & Assignment)", func(t *testing.T) {
		dueAt := time.Now().Add(24 * time.Hour)
		body := map[string]interface{}{
			"target_type":    "conversation",
			"target_id":      10842,
			"target_ref":     "会话 #10842",
			"scorecard_id":   scorecardID,
			"inspector_id":   2, // 刘琳
			"agent_id":       3, // 陈宁
			"due_at":         dueAt,
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "/api/v1/accounts/1/qa/tasks", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		taskData := resp["data"].(map[string]interface{})
		taskID = uint(taskData["id"].(float64))
		assert.NotEmpty(t, taskID)
		assert.Contains(t, taskData["task_number"].(string), "QA-")
	})

	t.Run("4. 逐项评分与总分自动计算 (Itemized Evaluation & Scoring)", func(t *testing.T) {
		body := map[string]interface{}{
			"feedback": "服务流程大体合格，但在问题确认环节缺乏追问，扣除5分。",
			"evaluations": []map[string]interface{}{
				{
					"criterion_id":     criterion1ID,
					"score":            28, // 满分 30，扣 2 分
					"deduction_reason": "结语缺少标准满意度邀请。",
				},
				{
					"criterion_id":     criterion2ID,
					"score":            45, // 满分 50，扣 5 分
					"deduction_reason": "未及时跟进发票开具时效说明。",
				},
			},
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/tasks/%d/evaluate", taskID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "2") // Inspector 刘琳打分
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		taskData := resp["data"].(map[string]interface{})
		totalScore := int(taskData["total_score"].(float64))
		assert.Equal(t, 73, totalScore) // 28 + 45 = 73 (未达 85 分 passing_score)
		assert.Equal(t, "需改进", taskData["result"])
		assert.Equal(t, "rectifying", taskData["status"])
	})

	t.Run("5. 整改要求与闭环确认 (Rectification Flow)", func(t *testing.T) {
		// 坐席提交整改计划
		planBody := map[string]interface{}{
			"plan": "已重新研读《企业发票处理规范》，并在快捷回复中配置标准时效安抚话术，今后避免类似遗漏。",
		}
		data, _ := json.Marshal(planBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/tasks/%d/rectify", taskID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "3") // 客服陈宁提交
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// 质检主管确认整改并关闭
		confirmReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/tasks/%d/confirm_rectification", taskID), nil)
		confirmReq.Header.Set("X-User-ID", "2")
		wConfirm := httptest.NewRecorder()
		r.ServeHTTP(wConfirm, confirmReq)
		assert.Equal(t, http.StatusOK, wConfirm.Code)

		var resp map[string]interface{}
		json.Unmarshal(wConfirm.Body.Bytes(), &resp)
		taskData := resp["data"].(map[string]interface{})
		assert.Equal(t, "closed", taskData["status"])
	})

	t.Run("6. 坐席申诉发起 (Agent Appeal Submission)", func(t *testing.T) {
		body := map[string]interface{}{
			"demand_type":            "score_adjustment",
			"disputed_criterion_ids": []uint{criterion2ID},
			"reason":                 "客户在发票信息提供时未给全纳税人识别号，我已主动提示并追问两次，不应扣除5分完整解决分。",
			"evidence_notes":         "参见会话第4条与第7条消息记录。",
			"evidence_urls":          []string{"https://cdn.example.com/evidence/chat_msg_10842.png"},
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/tasks/%d/appeal", taskID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "3") // 坐席发起申诉
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusCreated, w.Code)

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		appealData := resp["data"].(map[string]interface{})
		appealID = uint(appealData["id"].(float64))
		assert.NotEmpty(t, appealID)
		assert.Contains(t, appealData["appeal_number"].(string), "AP-")
		assert.Equal(t, "pending", appealData["status"])
	})

	t.Run("7. 证据和附件补充 (Appeal Evidence Upload)", func(t *testing.T) {
		body := map[string]interface{}{
			"evidence_notes": "补充客户微信聊天确认截图与纳税号重发时间戳证据。",
			"evidence_urls":  []string{"https://cdn.example.com/evidence/wechat_ts.png"},
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/appeals/%d/evidence", appealID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-User-ID", "3")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("8. 申诉独立复核与回避原则 (Adjudication & Avoidance Principle)", func(t *testing.T) {
		// 原质检员 (UserID: 2 刘琳) 试图复核 -> 必须触发回避原则被拒绝
		reviewBody := map[string]interface{}{
			"action":         "adjusted",
			"adjusted_score": 88,
			"comments":       "我来复核我自己的评分",
		}
		data, _ := json.Marshal(reviewBody)
		reqRefused, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/appeals/%d/review", appealID), bytes.NewReader(data))
		reqRefused.Header.Set("Content-Type", "application/json")
		reqRefused.Header.Set("X-User-ID", "2") // 原质检员
		wRefused := httptest.NewRecorder()
		r.ServeHTTP(wRefused, reqRefused)
		assert.Equal(t, http.StatusBadRequest, wRefused.Code)
		assert.Contains(t, wRefused.Body.String(), "回避原则")

		// 独立复核人 (UserID: 4 赵晨) 进行改判 -> 成功生效并调整分数
		validReviewBody := map[string]interface{}{
			"action":         "adjusted",
			"adjusted_score": 88,
			"comments":       "经查证会话记录，客服陈宁确已尽到追问义务，改判增加15分，原总分调整为88分合格。",
		}
		validData, _ := json.Marshal(validReviewBody)
		reqValid, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/1/qa/appeals/%d/review", appealID), bytes.NewReader(validData))
		reqValid.Header.Set("Content-Type", "application/json")
		reqValid.Header.Set("X-User-ID", "4") // 独立复核人
		wValid := httptest.NewRecorder()
		r.ServeHTTP(wValid, reqValid)
		assert.Equal(t, http.StatusOK, wValid.Code)

		var resp map[string]interface{}
		json.Unmarshal(wValid.Body.Bytes(), &resp)
		appealData := resp["data"].(map[string]interface{})
		assert.Equal(t, "adjusted", appealData["status"])
		assert.Equal(t, float64(88), appealData["adjusted_score"].(float64))
	})

	t.Run("9. 质检统计报表汇总 (QA Summary Report)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/v1/accounts/1/reports/qa_summary", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		reportData := resp["data"].(map[string]interface{})

		assert.NotNil(t, reportData["task_metrics"])
		assert.NotNil(t, reportData["appeal_metrics"])
		assert.Equal(t, float64(1), reportData["rectified_count"].(float64))
		assert.Equal(t, float64(1), reportData["scorecards_count"].(float64))
		assert.Equal(t, float64(1), reportData["sampling_rules_count"].(float64))
	})
}
