package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestSystemEnhancementsAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "system-enhancement-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Admin User & Account Setup
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "系统管理员",
		"email":        "sysadmin@example.com",
		"password":     "password123456",
		"account_name": "系统综合服务中心",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusOK && wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d body=%s", wSignUp.Code, wSignUp.Body.String())
	}

	var signUpResp map[string]interface{}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &signUpResp)
	dataMap := signUpResp["data"].(map[string]interface{})
	adminToken := dataMap["token"].(string)

	var account domain.Account
	db.First(&account)
	accountID := account.ID

	// -------------------------------------------------------------
	// 1. 自定义角色权限前后端一致性测试 (Issue 1)
	// -------------------------------------------------------------
	t.Run("CustomRole_ChinesePermissions_And_AccessControl", func(t *testing.T) {
		// 1.1 创建拥有前端格式权限的角色（会话管理 + 工单只读）
		roleReqBody, _ := json.Marshal(map[string]interface{}{
			"name":        "初级客服主管",
			"description": "具有会话管理与工单查看权限",
			"permissions": []string{"会话:管理", "工单:查看"},
		})
		wRole := httptest.NewRecorder()
		reqRole, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/custom_roles", accountID), bytes.NewBuffer(roleReqBody))
		reqRole.Header.Set("Authorization", "Bearer "+adminToken)
		reqRole.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wRole, reqRole)
		if wRole.Code != http.StatusOK && wRole.Code != http.StatusCreated {
			t.Fatalf("create custom role failed: code=%d body=%s", wRole.Code, wRole.Body.String())
		}

		var roleResp map[string]interface{}
		_ = json.Unmarshal(wRole.Body.Bytes(), &roleResp)
		var customRoleID uint
		if data, ok := roleResp["data"].(map[string]interface{}); ok {
			customRoleID = uint(data["id"].(float64))
		}

		// 1.2 创建普通测试坐席成员并分配该自定义角色
		addMemberBody, _ := json.Marshal(map[string]interface{}{
			"name":           "张敏客服",
			"email":          "agent.zhang@example.com",
			"role":           "初级客服主管",
			"custom_role_id": customRoleID,
		})
		wMember := httptest.NewRecorder()
		reqMember, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addMemberBody))
		reqMember.Header.Set("Authorization", "Bearer "+adminToken)
		reqMember.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wMember, reqMember)
		if wMember.Code != http.StatusOK && wMember.Code != http.StatusCreated {
			t.Fatalf("add member with custom role failed: code=%d body=%s", wMember.Code, wMember.Body.String())
		}

		var agentUser domain.User
		db.Where("email = ?", "agent.zhang@example.com").First(&agentUser)

		// 生成该坐席成员的 JWT Token
		agentToken, err := auth.GenerateToken(&agentUser, cfg.JWTSecret, 24)
		if err != nil {
			t.Fatalf("failed to generate agent token: %v", err)
		}

		// 1.3 验证该坐席访问会话接口（拥有 "会话:管理" -> 映射成功，返回 200 而非 403）
		wConv := httptest.NewRecorder()
		reqConv, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), nil)
		reqConv.Header.Set("Authorization", "Bearer "+agentToken)
		engine.ServeHTTP(wConv, reqConv)
		if wConv.Code == http.StatusForbidden {
			t.Fatalf("agent with '会话:管理' was denied access with 403!")
		}
		if wConv.Code != http.StatusOK {
			t.Fatalf("unexpected code for conversation list: %d, body: %s", wConv.Code, wConv.Body.String())
		}

		// 1.4 验证工单查看（拥有 "工单:查看" -> 允许 GET /tickets）
		wTicketGet := httptest.NewRecorder()
		reqTicketGet, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets", accountID), nil)
		reqTicketGet.Header.Set("Authorization", "Bearer "+agentToken)
		engine.ServeHTTP(wTicketGet, reqTicketGet)
		if wTicketGet.Code == http.StatusForbidden {
			t.Fatalf("agent with '工单:查看' was denied ticket list with 403!")
		}
		if wTicketGet.Code != http.StatusOK {
			t.Fatalf("unexpected code for ticket list: %d, body: %s", wTicketGet.Code, wTicketGet.Body.String())
		}

		// 1.5 验证工单写操作（只有 "工单:查看"，无 "工单:管理" -> POST /tickets 必须返回 403 Forbidden!）
		ticketCreateBody, _ := json.Marshal(map[string]interface{}{
			"title":    "未授权的工单创建",
			"priority": "urgent",
		})
		wTicketPost := httptest.NewRecorder()
		reqTicketPost, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets", accountID), bytes.NewBuffer(ticketCreateBody))
		reqTicketPost.Header.Set("Authorization", "Bearer "+agentToken)
		reqTicketPost.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wTicketPost, reqTicketPost)
		if wTicketPost.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for ticket create without ticket_manage, got: %d, body: %s", wTicketPost.Code, wTicketPost.Body.String())
		}

		// 1.6 验证质检复核（无 "qa_appeal_review" -> POST /qa/appeals/1/review 必须返回 403 Forbidden!）
		wReview := httptest.NewRecorder()
		reqReview, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/1/review", accountID), bytes.NewBuffer([]byte(`{"decision":"approved"}`)))
		reqReview.Header.Set("Authorization", "Bearer "+agentToken)
		reqReview.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wReview, reqReview)
		if wReview.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for qa appeal review without permission, got: %d", wReview.Code)
		}
	})

	// -------------------------------------------------------------
	// 2. 真实消息翻译测试 (Issue 3)
	// -------------------------------------------------------------
	t.Run("MessageTranslation_RealTranslation_And_Cache", func(t *testing.T) {
		// 创建测试会话与消息
		inbox := domain.Inbox{
			AccountID:   accountID,
			Name:        "多语言测试收件箱",
			ChannelType: "web_widget",
		}
		db.Create(&inbox)

		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&conv)

		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			Content:        "你好，请问我的退款什么时候到账？",
			MessageType:    domain.MessageTypeIncoming,
			Status:         domain.MessageStatusSent,
		}
		db.Create(&msg)

		// 调用翻译接口将中文翻译为英文 (target_language: "en")
		transReqBody, _ := json.Marshal(map[string]string{
			"target_language": "en",
		})
		wTrans := httptest.NewRecorder()
		reqTrans, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/translate", accountID, conv.ID, msg.ID), bytes.NewBuffer(transReqBody))
		reqTrans.Header.Set("Authorization", "Bearer "+adminToken)
		reqTrans.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wTrans, reqTrans)

		if wTrans.Code != http.StatusOK {
			t.Fatalf("translate message failed: code=%d body=%s", wTrans.Code, wTrans.Body.String())
		}

		var transResp map[string]interface{}
		_ = json.Unmarshal(wTrans.Body.Bytes(), &transResp)
		transContent := transResp["content"].(string)

		// 断言返回的不是未翻译的原始内容
		if transContent == msg.Content {
			t.Fatalf("translation returned raw untranslated content! got: %s", transContent)
		}
		if !strings.Contains(transContent, "Hello") && !strings.Contains(transContent, "Refund") && !strings.Contains(transContent, "[EN Translation]") {
			t.Fatalf("expected translated english keywords, got: %s", transContent)
		}

		// 检查数据库中持久化了 Translations
		var updatedMsg domain.Message
		db.First(&updatedMsg, msg.ID)
		if updatedMsg.Translations == "" || !strings.Contains(updatedMsg.Translations, "en") {
			t.Fatalf("translations not persisted in db: %s", updatedMsg.Translations)
		}
	})

	// -------------------------------------------------------------
	// 3. 帮助中心公开站点与 Sitemap 测试 (Issue 4)
	// -------------------------------------------------------------
	t.Run("HelpCenter_PublicWebPortal_And_Sitemap", func(t *testing.T) {
		// 创建测试公开门户、分类与文章
		portal := domain.Portal{
			AccountID:  accountID,
			Name:       "官方用户支持中心",
			Slug:       "user-support",
			Color:      "#0ea5e9",
			HeaderText: "快速查询服务指引与常见问题解答",
		}
		db.Create(&portal)

		cat := domain.Category{
			PortalID:    portal.ID,
			Name:        "账户与安全",
			Slug:        "account-security",
			Description: "登录、密码修改与安全二次验证指引",
			Icon:        "🔒",
		}
		db.Create(&cat)

		art := domain.Article{
			PortalID:   portal.ID,
			CategoryID: cat.ID,
			Title:      "如何开启双重身份验证 (2FA)",
			Slug:       "enable-2fa",
			Content:    "## 安全设置步骤\n\n开启 2FA 能够保障您的账户资产安全：\n- 进入个人设置\n- 点击 **安全与认证**\n- 扫描二维码并输入动态验证码",
			Status:     "published",
		}
		db.Create(&art)

		// 3.1 访问门户首页 /hc/:slug
		wHome := httptest.NewRecorder()
		reqHome, _ := http.NewRequest("GET", "/hc/user-support", nil)
		engine.ServeHTTP(wHome, reqHome)
		if wHome.Code != http.StatusOK {
			t.Fatalf("portal home failed: code=%d body=%s", wHome.Code, wHome.Body.String())
		}
		homeHTML := wHome.Body.String()
		if !strings.Contains(homeHTML, "官方用户支持中心") || !strings.Contains(homeHTML, "账户与安全") {
			t.Fatalf("portal home missing portal/category content: %s", homeHTML)
		}
		if !strings.Contains(homeHTML, `<meta name="description"`) {
			t.Fatalf("portal home missing SEO meta tags")
		}

		// 3.2 访问分类页面 /hc/:slug/categories/:cat_slug
		wCat := httptest.NewRecorder()
		reqCat, _ := http.NewRequest("GET", "/hc/user-support/categories/account-security", nil)
		engine.ServeHTTP(wCat, reqCat)
		if wCat.Code != http.StatusOK {
			t.Fatalf("category page failed: code=%d body=%s", wCat.Code, wCat.Body.String())
		}
		catHTML := wCat.Body.String()
		if !strings.Contains(catHTML, "如何开启双重身份验证 (2FA)") {
			t.Fatalf("category page missing article item: %s", catHTML)
		}

		// 3.3 访问文章页面 /hc/:slug/articles/:art_slug (验证 Markdown 渲染与阅读计数递增)
		initialViews := art.Views
		wArt := httptest.NewRecorder()
		reqArt, _ := http.NewRequest("GET", "/hc/user-support/articles/enable-2fa", nil)
		engine.ServeHTTP(wArt, reqArt)
		if wArt.Code != http.StatusOK {
			t.Fatalf("article page failed: code=%d body=%s", wArt.Code, wArt.Body.String())
		}
		artHTML := wArt.Body.String()
		if !strings.Contains(artHTML, "<h2>安全设置步骤</h2>") || !strings.Contains(artHTML, "<strong>安全与认证</strong>") {
			t.Fatalf("markdown conversion failed in article page: %s", artHTML)
		}

		// 验证阅读数递增
		var updatedArt domain.Article
		db.First(&updatedArt, art.ID)
		if updatedArt.Views <= initialViews {
			t.Fatalf("article views did not increment: before=%d after=%d", initialViews, updatedArt.Views)
		}

		// 3.4 搜索文章 /hc/:slug/search?q=2FA
		wSearch := httptest.NewRecorder()
		reqSearch, _ := http.NewRequest("GET", "/hc/user-support/search?q=2FA", nil)
		engine.ServeHTTP(wSearch, reqSearch)
		if wSearch.Code != http.StatusOK {
			t.Fatalf("search failed: code=%d body=%s", wSearch.Code, wSearch.Body.String())
		}
		searchHTML := wSearch.Body.String()
		if !strings.Contains(searchHTML, "如何开启双重身份验证 (2FA)") {
			t.Fatalf("search result missing matching article: %s", searchHTML)
		}

		// 3.5 XML Sitemap /hc/:slug/sitemap.xml
		wSitemap := httptest.NewRecorder()
		reqSitemap, _ := http.NewRequest("GET", "/hc/user-support/sitemap.xml", nil)
		engine.ServeHTTP(wSitemap, reqSitemap)
		if wSitemap.Code != http.StatusOK {
			t.Fatalf("sitemap failed: code=%d body=%s", wSitemap.Code, wSitemap.Body.String())
		}
		sitemapXML := wSitemap.Body.String()
		if !strings.Contains(sitemapXML, "<urlset") || !strings.Contains(sitemapXML, "/hc/user-support/articles/enable-2fa") {
			t.Fatalf("sitemap xml missing article URL: %s", sitemapXML)
		}
	})

	// -------------------------------------------------------------
	// 4. Inbox 模板同步真实更新与失败持久化 (Issue 5)
	// -------------------------------------------------------------
	t.Run("InboxTemplate_SyncStatus_And_Persistence", func(t *testing.T) {
		// 创建包含真实模板定义及拒绝原因的渠道收件箱
		providerConfig := map[string]interface{}{
			"sync_error": "",
			"templates": []map[string]interface{}{
				{
					"name":            "payment_receipt",
					"status":          "APPROVED",
					"category":        "UTILITY",
					"language":        "zh_CN",
					"rejected_reason": "",
				},
				{
					"name":            "promotional_deal",
					"status":          "REJECTED",
					"category":        "MARKETING",
					"language":        "zh_CN",
					"rejected_reason": "文案包含违规推广词汇，不符合服务条款",
				},
			},
		}
		providerConfigJSON, _ := json.Marshal(providerConfig)

		inbox := domain.Inbox{
			AccountID:      accountID,
			Name:           "WhatsApp 企业客服",
			ChannelType:    "whatsapp",
			WebsiteToken:   "whatsapp-channel-token-999",
			ProviderConfig: string(providerConfigJSON),
		}
		db.Create(&inbox)

		// 调用同步接口
		wSync := httptest.NewRecorder()
		reqSync, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/sync_templates", accountID, inbox.ID), nil)
		reqSync.Header.Set("Authorization", "Bearer "+adminToken)
		engine.ServeHTTP(wSync, reqSync)
		if wSync.Code != http.StatusOK {
			t.Fatalf("sync message templates failed: code=%d body=%s", wSync.Code, wSync.Body.String())
		}

		// 检查数据库中保存的模板状态与失败原因
		var templates []domain.InboxMessageTemplate
		db.Where("inbox_id = ?", inbox.ID).Find(&templates)
		if len(templates) != 2 {
			t.Fatalf("expected 2 templates in db, got: %d", len(templates))
		}

		var rejectedTpl *domain.InboxMessageTemplate
		for i := range templates {
			if templates[i].Status == "REJECTED" {
				rejectedTpl = &templates[i]
			}
			if templates[i].LastSyncAt == nil {
				t.Fatalf("template %s has nil LastSyncAt", templates[i].Name)
			}
			if templates[i].SyncStatus != "synced" {
				t.Fatalf("template %s unexpected sync status: %s", templates[i].Name, templates[i].SyncStatus)
			}
		}

		if rejectedTpl == nil {
			t.Fatalf("rejected template not found")
		}
		if !strings.Contains(rejectedTpl.RejectedReason, "违规推广词汇") {
			t.Fatalf("rejected reason not recorded: %s", rejectedTpl.RejectedReason)
		}
	})

	// -------------------------------------------------------------------------
	// 5. 验证安全边界增强（CSAT防枚举/越权防篡改、QA申诉/证据主体限制、工单写操作RBAC拦截）
	// -------------------------------------------------------------------------
	t.Run("Security_Hardening_CSAT_QA_And_Ticket_Boundaries", func(t *testing.T) {
		// 5.1 CSAT 安全机制：纯数字 ID 无凭据访问与修改均返回 403 Forbidden
		var inbox domain.Inbox
		db.Where("account_id = ?", accountID).First(&inbox)

		convSec := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "resolved",
			DisplayID: 888,
		}
		_ = db.Create(&convSec)

		surveySec := domain.CSATSurvey{
			AccountID:      accountID,
			ConversationID: convSec.ID,
			Rating:         4,
			FeedbackText:   "机密评价内容",
		}
		_ = db.Create(&surveySec)

		// 匿名数字 ID 访问 -> 403 Forbidden
		wGetUnauth := httptest.NewRecorder()
		reqGetUnauth, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d", convSec.ID), nil)
		engine.ServeHTTP(wGetUnauth, reqGetUnauth)
		if wGetUnauth.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for unauthenticated numeric CSAT access, got %d", wGetUnauth.Code)
		}

		// 匿名数字 ID 修改 -> 403 Forbidden
		patchPayload, _ := json.Marshal(map[string]any{"rating": 1, "feedback_message": "恶意篡改"})
		wPatchUnauth := httptest.NewRecorder()
		reqPatchUnauth, _ := http.NewRequest("PATCH", fmt.Sprintf("/public/api/v1/csat_survey/%d", convSec.ID), bytes.NewBuffer(patchPayload))
		reqPatchUnauth.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPatchUnauth, reqPatchUnauth)
		if wPatchUnauth.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for unauthenticated numeric CSAT update, got %d", wPatchUnauth.Code)
		}

		// 5.2 QA 质检主体限制：非被评分人且非主管，严禁冒充整改、发起申诉或提交证据
		// 创建次要坐席 3 (非任务所属人)
		addAgent3Body, _ := json.Marshal(map[string]string{
			"name":     "局外坐席-小王",
			"email":    "agent.wang@example.com",
			"password": "Password123!",
			"role":     "agent",
		})
		wAgent3 := httptest.NewRecorder()
		reqAgent3, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addAgent3Body))
		reqAgent3.Header.Set("Content-Type", "application/json")
		reqAgent3.Header.Set("Authorization", "Bearer "+adminToken)
		engine.ServeHTTP(wAgent3, reqAgent3)

		loginAgent3Body, _ := json.Marshal(map[string]string{
			"email":    "agent.wang@example.com",
			"password": "Password123!",
		})
		wL3 := httptest.NewRecorder()
		reqL3, _ := http.NewRequest("POST", "/auth/sign_in", bytes.NewBuffer(loginAgent3Body))
		reqL3.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wL3, reqL3)
		var authL3 struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wL3.Body.Bytes(), &authL3)
		agent3Token := authL3.Data.Token

		var agent3User domain.User
		db.Where("email = ?", "agent.wang@example.com").First(&agent3User)

		var agentUser domain.User
		db.Where("email = ?", "agent.zhang@example.com").First(&agentUser)
		agentToken, _ := auth.GenerateToken(&agentUser, cfg.JWTSecret, 24)

		qaTask := domain.QATask{
			AccountID:     accountID,
			TaskNumber:    "QA-SEC-888",
			TargetType:    "conversation",
			TargetID:      convSec.ID,
			AgentID:       &agent3User.ID, // 属于 agent3User (小王)
			TotalScore:    50,
			HasFatalError: true,
			Status:        "rectifying",
		}
		_ = db.Create(&qaTask)

		// 坐席张敏试图提交整改计划 -> 403 Forbidden
		rectifyBody, _ := json.Marshal(map[string]string{"plan": "非被评人试图整改"})
		wR := httptest.NewRecorder()
		reqR, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/rectify", accountID, qaTask.ID), bytes.NewBuffer(rectifyBody))
		reqR.Header.Set("Authorization", "Bearer "+agentToken)
		reqR.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wR, reqR)
		if wR.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for non-assigned agent rectifying task, got %d", wR.Code)
		}

		// 坐席张敏试图冒充发起申诉 -> 403 Forbidden
		appealBody, _ := json.Marshal(map[string]string{"reason": "非被评人冒充申诉"})
		wA := httptest.NewRecorder()
		reqA, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/appeal", accountID, qaTask.ID), bytes.NewBuffer(appealBody))
		reqA.Header.Set("Authorization", "Bearer "+agentToken)
		reqA.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wA, reqA)
		if wA.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for non-assigned agent appealing task, got %d", wA.Code)
		}

		// 被评分坐席本人 (agent3Token / 小王) 发起申诉 -> 201 Created
		wAOK := httptest.NewRecorder()
		reqAOK, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/appeal", accountID, qaTask.ID), bytes.NewBuffer(appealBody))
		reqAOK.Header.Set("Authorization", "Bearer "+agent3Token)
		reqAOK.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAOK, reqAOK)
		if wAOK.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for assigned agent appealing own task, got %d, body=%s", wAOK.Code, wAOK.Body.String())
		}

		var appealCreated struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wAOK.Body.Bytes(), &appealCreated)
		appealID := appealCreated.Data.ID

		// 坐席张敏试图向小王的申诉追加证据 -> 403 Forbidden
		evBody, _ := json.Marshal(map[string]any{"evidence_notes": "非申诉人试图追加证据"})
		wEv := httptest.NewRecorder()
		reqEv, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d/evidence", accountID, appealID), bytes.NewBuffer(evBody))
		reqEv.Header.Set("Authorization", "Bearer "+agentToken)
		reqEv.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wEv, reqEv)
		if wEv.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for unauthorized agent submitting appeal evidence, got %d", wEv.Code)
		}

		// 小王（原申诉人）提交证据 -> 200 OK
		wEvOK := httptest.NewRecorder()
		reqEvOK, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d/evidence", accountID, appealID), bytes.NewBuffer(evBody))
		reqEvOK.Header.Set("Authorization", "Bearer "+agent3Token)
		reqEvOK.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wEvOK, reqEvOK)
		if wEvOK.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for appellant submitting evidence, got %d, body=%s", wEvOK.Code, wEvOK.Body.String())
		}

		// 5.3 工单写操作严格收口：普通 agent 无 ticket_manage 权限，写操作严格拦截
		ticketCreateBody, _ := json.Marshal(map[string]any{
			"title":    "普通坐席尝试创建工单",
			"priority": "low",
		})
		wTkPost := httptest.NewRecorder()
		reqTkPost, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets", accountID), bytes.NewBuffer(ticketCreateBody))
		reqTkPost.Header.Set("Authorization", "Bearer "+agent3Token)
		reqTkPost.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wTkPost, reqTkPost)
		if wTkPost.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for standard agent creating ticket without ticket_manage, got %d", wTkPost.Code)
		}
	})
}

func TestTicketAndQADeepFixesAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "deep-fixes-test-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Setup Admin & Account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "运营总监",
		"email":        "director.qa@example.com",
		"password":     "password123456",
		"account_name": "质量与运维保障中心",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusOK && wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d", wSignUp.Code)
	}

	var signUpResp map[string]interface{}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &signUpResp)
	adminToken := signUpResp["data"].(map[string]interface{})["token"].(string)

	var account domain.Account
	db.First(&account)
	accountID := account.ID

	// Create test agent
	agentUser := &domain.User{
		Name:  "质检坐席李雷",
		Email: "lilei.agent@example.com",
		Role:  domain.RoleAgent,
	}
	db.Create(agentUser)
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: agentUser.ID, Role: domain.RoleAgent})
	agentToken, _ := auth.GenerateToken(agentUser, cfg.JWTSecret, 24)

	// Create test QA reviewer
	reviewerUser := &domain.User{
		Name:  "质检主管韩梅梅",
		Email: "hanmeimei.lead@example.com",
		Role:  domain.RoleAdministrator,
	}
	db.Create(reviewerUser)
	db.Create(&domain.AccountUser{AccountID: accountID, UserID: reviewerUser.ID, Role: domain.RoleAdministrator})
	reviewerToken, _ := auth.GenerateToken(reviewerUser, cfg.JWTSecret, 24)
	_ = reviewerToken

	ticketRepo := repository.NewTicketRepository(db)
	qaRepo := repository.NewQARepository(db)

	// Setup Scorecard for QA tasks
	scorecard := &domain.QAScorecard{
		AccountID: accountID,
		Name:      "服务合规抽检模板",
		Status:    "active",
	}
	db.Create(scorecard)

	// -------------------------------------------------------------
	// 1. 编号高并发防冲突与唯一索引验证 (Issue 5)
	// -------------------------------------------------------------
	t.Run("Issue5_Concurrent_Number_Generation_And_Unique_Index", func(t *testing.T) {
		const concurrency = 10

		// 1.1 并发创建工单
		var wg sync.WaitGroup
		ticketNumbers := make([]string, concurrency)
		ticketErrors := make([]error, concurrency)

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				tk := &domain.Ticket{
					AccountID: accountID,
					Title:     fmt.Sprintf("并发测试工单-%d", idx),
					Priority:  "medium",
				}
				ticketErrors[idx] = ticketRepo.Create(tk)
				if ticketErrors[idx] == nil {
					ticketNumbers[idx] = tk.TicketNumber
				}
			}(i)
		}
		wg.Wait()

		for i, err := range ticketErrors {
			if err != nil {
				t.Fatalf("concurrent ticket creation failed at idx %d: %v", i, err)
			}
		}

		// 验证无重复
		uniqueTickets := make(map[string]bool)
		for _, num := range ticketNumbers {
			if uniqueTickets[num] {
				t.Fatalf("duplicate ticket number detected: %s", num)
			}
			uniqueTickets[num] = true
			if !strings.HasPrefix(num, "TK-") {
				t.Fatalf("invalid ticket number format: %s", num)
			}
		}

		// 1.2 复合唯一索引约束验证：直接插入相同编号应被数据库拒绝
		dupTicket := &domain.Ticket{
			AccountID:    accountID,
			TicketNumber: ticketNumbers[0],
			Title:        "试图伪造相同编号工单",
		}
		if err := db.Create(dupTicket).Error; err == nil {
			t.Fatalf("expected unique constraint violation on duplicate ticket number, but got nil")
		}

		// 1.3 并发创建 QA 任务
		qaNumbers := make([]string, concurrency)
		qaErrors := make([]error, concurrency)
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				task := &domain.QATask{
					AccountID:   accountID,
					TargetType:  "conversation",
					TargetID:    uint(100 + idx),
					ScorecardID: scorecard.ID,
					AgentID:     &agentUser.ID,
				}
				qaErrors[idx] = qaRepo.CreateTask(task)
				if qaErrors[idx] == nil {
					qaNumbers[idx] = task.TaskNumber
				}
			}(i)
		}
		wg.Wait()

		for i, err := range qaErrors {
			if err != nil {
				t.Fatalf("concurrent QA task creation failed at idx %d: %v", i, err)
			}
		}

		uniqueQAs := make(map[string]bool)
		for _, num := range qaNumbers {
			if uniqueQAs[num] {
				t.Fatalf("duplicate QA task number detected: %s", num)
			}
			uniqueQAs[num] = true
			if !strings.HasPrefix(num, "QA-") {
				t.Fatalf("invalid QA task number format: %s", num)
			}
		}

		// 1.4 复合唯一索引约束验证：直接插入相同 QA 任务编号应被拒绝
		dupTask := &domain.QATask{
			AccountID:   accountID,
			TaskNumber:  qaNumbers[0],
			TargetType:  "conversation",
			TargetID:    999,
			ScorecardID: scorecard.ID,
		}
		if err := db.Create(dupTask).Error; err == nil {
			t.Fatalf("expected unique constraint violation on duplicate QA task number, but got nil")
		}
	})

	// -------------------------------------------------------------
	// 2. 工单删除级联清理附件记录 (Issue 6)
	// -------------------------------------------------------------
	t.Run("Issue6_Ticket_Deletion_Cascades_Attachments", func(t *testing.T) {
		tk := &domain.Ticket{
			AccountID: accountID,
			Title:     "带附件的待删除工单",
			Priority:  "low",
		}
		if err := ticketRepo.Create(tk); err != nil {
			t.Fatalf("failed to create ticket: %v", err)
		}

		att, err := ticketRepo.CreateAttachment(accountID, tk.ID, "report.pdf", "application/pdf", 1024, "data:application/pdf;base64,dGVzdA==", nil)
		if err != nil {
			t.Fatalf("failed to create attachment: %v", err)
		}

		// 校验附件存在
		var foundAtt domain.TicketAttachment
		if err := db.Where("account_id = ? AND id = ?", accountID, att.ID).First(&foundAtt).Error; err != nil {
			t.Fatalf("attachment not found before deletion: %v", err)
		}

		// 执行删除
		if err := ticketRepo.Delete(accountID, tk.ID); err != nil {
			t.Fatalf("failed to delete ticket: %v", err)
		}

		// 确认工单已删除
		var foundTk domain.Ticket
		if err := db.Where("account_id = ? AND id = ?", accountID, tk.ID).First(&foundTk).Error; err == nil {
			t.Fatalf("ticket still exists after delete")
		}

		// 确认附件已被级联删除
		if err := db.Where("account_id = ? AND id = ?", accountID, att.ID).First(&foundAtt).Error; err == nil {
			t.Fatalf("orphan attachment still exists in database after ticket deletion!")
		}
	})

	// -------------------------------------------------------------
	// 3. 工单创建全事务保证与初始审计活动 (Issue 7)
	// -------------------------------------------------------------
	t.Run("Issue7_Ticket_Creation_Atomic_Transaction", func(t *testing.T) {
		tk := &domain.Ticket{
			AccountID:     accountID,
			Title:         "原子事务验证工单",
			Priority:      "urgent",
			AssignedGroup: "VIP客服组",
			CreatorID:     &agentUser.ID,
		}
		if err := ticketRepo.Create(tk); err != nil {
			t.Fatalf("failed to create ticket: %v", err)
		}

		// 检查活动记录
		var act domain.TicketActivity
		if err := db.Where("account_id = ? AND ticket_id = ? AND action = 'created'", accountID, tk.ID).First(&act).Error; err != nil {
			t.Fatalf("initial creation activity not found: %v", err)
		}
		if act.Details == "" || !strings.Contains(act.Details, "VIP客服组") {
			t.Fatalf("unexpected activity details: %s", act.Details)
		}

		// 检查初始状态历史
		var history domain.TicketStatusHistory
		if err := db.Where("account_id = ? AND ticket_id = ? AND reason = '工单创建'", accountID, tk.ID).First(&history).Error; err != nil {
			t.Fatalf("initial status history not found: %v", err)
		}
		if history.ToStatus != "open" {
			t.Fatalf("expected status open, got %s", history.ToStatus)
		}
	})

	// -------------------------------------------------------------
	// 4. 工单普通更新生成变更 Diff 审计活动 (Issue 8)
	// -------------------------------------------------------------
	t.Run("Issue8_Ticket_Update_Generates_Diff_Activity", func(t *testing.T) {
		tk := &domain.Ticket{
			AccountID: accountID,
			Title:     "原始标题",
			Priority:  "low",
		}
		if err := ticketRepo.Create(tk); err != nil {
			t.Fatalf("failed to create ticket: %v", err)
		}

		// 更新标题与优先级
		tk.Title = "更新后的新标题"
		tk.Priority = "urgent"
		if err := ticketRepo.UpdateWithActor(tk, &reviewerUser.ID); err != nil {
			t.Fatalf("failed to update ticket with actor: %v", err)
		}

		// 验证变更 Diff 活动已生成
		var updateAct domain.TicketActivity
		if err := db.Where("account_id = ? AND ticket_id = ? AND action = 'updated'", accountID, tk.ID).First(&updateAct).Error; err != nil {
			t.Fatalf("update diff activity not found: %v", err)
		}

		if !strings.Contains(updateAct.Details, "原始标题") || !strings.Contains(updateAct.Details, "更新后的新标题") {
			t.Fatalf("diff activity missing title changes: %s", updateAct.Details)
		}
		if !strings.Contains(updateAct.Details, "low") || !strings.Contains(updateAct.Details, "urgent") {
			t.Fatalf("diff activity missing priority changes: %s", updateAct.Details)
		}
	})

	// -------------------------------------------------------------
	// 5. 工单附件安全验证与存储规范 (Issue 9)
	// -------------------------------------------------------------
	t.Run("Issue9_Ticket_Attachment_Security_And_Size_Validation", func(t *testing.T) {
		tk := &domain.Ticket{
			AccountID: accountID,
			Title:     "附件安全测试工单",
			Priority:  "medium",
		}
		_ = ticketRepo.Create(tk)

		// 5.1 拒绝高危脚本后缀
		dangerousBody, _ := json.Marshal(map[string]any{
			"file_name": "malicious_payload.exe",
			"file_type": "application/x-msdownload",
			"file_size": 1024,
			"data_url":  "data:application/octet-stream;base64,TVqQAAMAAAAEAAAA//8AALgAAAAAAAAAQAAaAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEA",
		})
		wDanger := httptest.NewRecorder()
		reqDanger, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/attachments", accountID, tk.ID), bytes.NewBuffer(dangerousBody))
		reqDanger.Header.Set("Authorization", "Bearer "+adminToken)
		reqDanger.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wDanger, reqDanger)
		if wDanger.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for .exe attachment, got %d: %s", wDanger.Code, wDanger.Body.String())
		}
		if !strings.Contains(wDanger.Body.String(), "security") {
			t.Fatalf("expected security warning in response: %s", wDanger.Body.String())
		}

		// 5.2 拒绝超出 20MB 的附件
		oversizedBody, _ := json.Marshal(map[string]any{
			"file_name": "giant_video.mp4",
			"file_type": "video/mp4",
			"file_size": 25 * 1024 * 1024, // 25MB
			"data_url":  "data:video/mp4;base64,AAAA",
		})
		wOver := httptest.NewRecorder()
		reqOver, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/attachments", accountID, tk.ID), bytes.NewBuffer(oversizedBody))
		reqOver.Header.Set("Authorization", "Bearer "+adminToken)
		reqOver.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wOver, reqOver)
		if wOver.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request for oversized attachment, got %d: %s", wOver.Code, wOver.Body.String())
		}

		// 5.3 正常合法附件上传成功
		validBody, _ := json.Marshal(map[string]any{
			"file_name": "safe_log.txt",
			"file_type": "text/plain",
			"file_size": 512,
			"data_url":  "data:text/plain;base64,SGVsbG8gU2FmZSBGaWxlIQ==",
		})
		wValid := httptest.NewRecorder()
		reqValid, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/attachments", accountID, tk.ID), bytes.NewBuffer(validBody))
		reqValid.Header.Set("Authorization", "Bearer "+adminToken)
		reqValid.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wValid, reqValid)
		if wValid.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for safe attachment, got %d: %s", wValid.Code, wValid.Body.String())
		}
	})

	// -------------------------------------------------------------
	// 6. 申诉证据冻结生命周期与状态机 (Issue 10)
	// -------------------------------------------------------------
	t.Run("Issue10_Appeal_Evidence_Freeze_Lifecycle", func(t *testing.T) {
		task := &domain.QATask{
			AccountID:   accountID,
			TargetType:  "conversation",
			TargetID:    888,
			ScorecardID: scorecard.ID,
			AgentID:     &agentUser.ID,
			TotalScore:  60,
			Result:      "需改进",
			Status:      "rectifying",
		}
		_ = qaRepo.CreateTask(task)

		// 6.1 坐席创建申诉：初始状态 evidence_frozen 必须为 false
		appeal, err := qaRepo.CreateAppealWithDetails(accountID, domain.QAAppeal{
			TaskID:      task.ID,
			AppellantID: agentUser.ID,
			Reason:      "评分存在事实差错",
			DemandType:  "score_adjustment",
		})
		if err != nil {
			t.Fatalf("failed to create appeal: %v", err)
		}
		if appeal.EvidenceFrozen {
			t.Fatalf("expected newly created appeal to have evidence_frozen: false, got true")
		}

		// 6.2 允许在 pending 阶段提交证据材料
		evBody, _ := json.Marshal(map[string]any{
			"evidence_notes": "这是初始补充凭据",
			"evidence_urls":  []string{"https://oss.example.com/proof1.png"},
		})
		wEv := httptest.NewRecorder()
		reqEv, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d/evidence", accountID, appeal.ID), bytes.NewBuffer(evBody))
		reqEv.Header.Set("Authorization", "Bearer "+agentToken)
		reqEv.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wEv, reqEv)
		if wEv.Code != http.StatusOK {
			t.Fatalf("expected 200 OK submitting evidence on unfrozen appeal, got %d: %s", wEv.Code, wEv.Body.String())
		}

		// 6.3 复核人审结申诉 (action: adjusted) -> 证据链自动冻结
		score88 := 88
		adjudicated, err := qaRepo.ReviewAppealWithDetails(accountID, appeal.ID, reviewerUser.ID, "adjusted", &score88, "核实无误改判", false)
		if err != nil {
			t.Fatalf("failed to adjudicate appeal: %v", err)
		}
		if !adjudicated.EvidenceFrozen {
			t.Fatalf("expected adjudicated appeal to have evidence_frozen: true, got false")
		}

		// 6.4 审结后试图再提交证据材料 -> 必须返回 422 Unprocessable Entity
		wEvAfter := httptest.NewRecorder()
		reqEvAfter, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d/evidence", accountID, appeal.ID), bytes.NewBuffer(evBody))
		reqEvAfter.Header.Set("Authorization", "Bearer "+agentToken)
		reqEvAfter.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wEvAfter, reqEvAfter)
		if wEvAfter.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 Unprocessable Entity submitting evidence on frozen appeal, got %d: %s", wEvAfter.Code, wEvAfter.Body.String())
		}
	})

	// -------------------------------------------------------------
	// 7. 直接创建申诉必须提供任务 ID (Issue 11)
	// -------------------------------------------------------------
	t.Run("Issue11_Direct_Appeal_Requires_Task_ID", func(t *testing.T) {
		// 未提供 task_id (task_id: 0) -> 必须返回 400 Bad Request
		badBody, _ := json.Marshal(map[string]any{
			"reason":      "未指定任务的非法申诉",
			"demand_type": "score_adjustment",
		})
		wBad := httptest.NewRecorder()
		reqBad, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals", accountID), bytes.NewBuffer(badBody))
		reqBad.Header.Set("Authorization", "Bearer "+adminToken)
		reqBad.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 Bad Request when task_id is omitted, got %d: %s", wBad.Code, wBad.Body.String())
		}
		if !strings.Contains(wBad.Body.String(), "task_id is required") {
			t.Fatalf("expected 'task_id is required' in response: %s", wBad.Body.String())
		}
	})

	// -------------------------------------------------------------
	// 8. 状态冲突与精准 HTTP 状态码规整 (Issue 12)
	// -------------------------------------------------------------
	t.Run("Issue12_Precise_HTTP_Status_Codes_And_Conflicts", func(t *testing.T) {
		task := &domain.QATask{
			AccountID:   accountID,
			TargetType:  "conversation",
			TargetID:    777,
			ScorecardID: scorecard.ID,
			AgentID:     &agentUser.ID,
			TotalScore:  50,
			Result:      "需改进",
			Status:      "rectifying",
		}
		_ = qaRepo.CreateTask(task)

		// 8.1 第一次发起申诉 -> 201 Created
		appealBody, _ := json.Marshal(map[string]any{
			"task_id":     task.ID,
			"reason":      "首次申诉",
			"demand_type": "score_adjustment",
		})
		wApp1 := httptest.NewRecorder()
		reqApp1, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals", accountID), bytes.NewBuffer(appealBody))
		reqApp1.Header.Set("Authorization", "Bearer "+agentToken)
		reqApp1.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wApp1, reqApp1)
		if wApp1.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created on first appeal, got %d: %s", wApp1.Code, wApp1.Body.String())
		}

		// 8.2 重复发起申诉（原申诉正在流转中） -> 409 Conflict
		wApp2 := httptest.NewRecorder()
		reqApp2, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals", accountID), bytes.NewBuffer(appealBody))
		reqApp2.Header.Set("Authorization", "Bearer "+agentToken)
		reqApp2.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wApp2, reqApp2)
		if wApp2.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict for duplicate appeal in progress, got %d: %s", wApp2.Code, wApp2.Body.String())
		}

		// 8.3 已关闭任务无法整改 -> 409 Conflict
		closedTask := &domain.QATask{
			AccountID:   accountID,
			TargetType:  "conversation",
			TargetID:    666,
			ScorecardID: scorecard.ID,
			AgentID:     &agentUser.ID,
			TotalScore:  90,
			Result:      "合格",
			Status:      "closed",
		}
		_ = qaRepo.CreateTask(closedTask)

		rectifyBody, _ := json.Marshal(map[string]any{
			"plan": "关闭任务提交整改",
		})
		wRec := httptest.NewRecorder()
		reqRec, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/rectify", accountID, closedTask.ID), bytes.NewBuffer(rectifyBody))
		reqRec.Header.Set("Authorization", "Bearer "+agentToken)
		reqRec.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wRec, reqRec)
		if wRec.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict when submitting rectification on closed task, got %d: %s", wRec.Code, wRec.Body.String())
		}
	})
}
