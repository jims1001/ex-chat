package test

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestCSATLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_csat_lifecycle_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	doReq := func(method, path string, payload any, token string) *httptest.ResponseRecorder {
		var body []byte
		if payload != nil {
			body, _ = json.Marshal(payload)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		r.ServeHTTP(w, req)
		return w
	}

	getData := func(body []byte) any {
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		if d, ok := m["data"]; ok {
			return d
		}
		return m
	}

	getMap := func(body []byte) map[string]any {
		d := getData(body)
		if m, ok := d.(map[string]any); ok {
			return m
		}
		return nil
	}

	// 1. Sign up Account 1 (Acme Enterprise)
	w1 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
		"account_name": "Acme Enterprise CSAT",
		"name":         "Support Admin",
		"email":        "admin@acmecsat.com",
		"password":     "Password123!",
	}, "")
	if w1.Code != http.StatusCreated {
		t.Fatalf("failed to sign up account 1: %s", w1.Body.String())
	}
	var authResp1 struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &authResp1)
	token1 := authResp1.Data.Token

	// Seed Inbox
	inbox := domain.Inbox{
		AccountID:   1,
		Name:        "CSAT Test Channel",
		ChannelType: "Channel::WebWidget",
	}
	if err := db.Create(&inbox).Error; err != nil {
		t.Fatalf("failed to create inbox: %v", err)
	}

	// Seed Contacts
	contact1 := domain.Contact{
		AccountID:   1,
		Name:        "张三 (VIP客户)",
		Email:       "zhangsan@example.com",
		PhoneNumber: "+8613800000001",
	}
	contact2 := domain.Contact{
		AccountID:   1,
		Name:        "李四",
		Email:       "lisi@example.com",
		PhoneNumber: "+8613800000002",
	}
	if err := db.Create(&contact1).Error; err != nil || db.Create(&contact2).Error != nil {
		t.Fatalf("failed to create contacts: %v", err)
	}

	agentID := uint(1)

	// Seed Conversations
	conv1 := domain.Conversation{
		AccountID:  1,
		InboxID:    inbox.ID,
		ContactID:  contact1.ID,
		Status:     "open",
		AssigneeID: &agentID,
	}
	conv2 := domain.Conversation{
		AccountID:  1,
		InboxID:    inbox.ID,
		ContactID:  contact2.ID,
		Status:     "resolved",
		AssigneeID: &agentID,
	}
	conv3 := domain.Conversation{
		AccountID:  1,
		InboxID:    inbox.ID,
		ContactID:  contact1.ID,
		Status:     "resolved",
		AssigneeID: &agentID,
	}
	if err := db.Create(&conv1).Error; err != nil {
		t.Fatalf("failed to create conv1: %v", err)
	}
	if err := db.Create(&conv2).Error; err != nil {
		t.Fatalf("failed to create conv2: %v", err)
	}
	if err := db.Create(&conv3).Error; err != nil {
		t.Fatalf("failed to create conv3: %v", err)
	}

	t.Run("Scenario 1: Survey trigger and customer submission", func(t *testing.T) {
		// 1.1 Trigger survey via POST /conversations/:id/csat_survey
		w := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/1/conversations/%d/csat_survey", conv1.ID), nil, token1)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("expected 200/201 on trigger survey, got %d: %s", w.Code, w.Body.String())
		}
		data := getMap(w.Body.Bytes())
		if data == nil {
			t.Fatalf("expected survey response map, got nil")
		}
		if status, _ := data["review_status"].(string); status != "pending" {
			t.Errorf("expected initial review_status pending, got %v", status)
		}

		// 1.2 Customer submits rating and feedback for Conv 1
		submitW := doReq(http.MethodPost, "/api/v1/accounts/1/csat_surveys", map[string]any{
			"conversation_id": conv1.ID,
			"rating":          5,
			"feedback_text":   "客服服务态度极佳，问题解决迅速！",
		}, token1)
		if submitW.Code != http.StatusCreated {
			t.Fatalf("expected 201 on submit survey, got %d: %s", submitW.Code, submitW.Body.String())
		}
		submitData := getMap(submitW.Body.Bytes())
		if r, _ := submitData["rating"].(float64); int(r) != 5 {
			t.Errorf("expected rating 5, got %v", r)
		}
		if fb, _ := submitData["feedback_text"].(string); fb != "客服服务态度极佳，问题解决迅速！" {
			t.Errorf("expected feedback text match, got %v", fb)
		}

		// 1.3 Submit for Conv 2 (rating 2)
		s2 := doReq(http.MethodPost, "/api/v1/accounts/1/csat_surveys", map[string]any{
			"conversation_id": conv2.ID,
			"rating":          2,
			"feedback_text":   "排队时间较长，希望改进效率。",
		}, token1)
		if s2.Code != http.StatusCreated {
			t.Fatalf("expected 201 on submit survey 2, got %d: %s", s2.Code, s2.Body.String())
		}

		// 1.4 Submit for Conv 3 (rating 4)
		s3 := doReq(http.MethodPost, "/api/v1/accounts/1/csat_surveys", map[string]any{
			"conversation_id": conv3.ID,
			"rating":          4,
			"feedback_text":   "整体解答满意，但文档稍显简单。",
		}, token1)
		if s3.Code != http.StatusCreated {
			t.Fatalf("expected 201 on submit survey 3, got %d: %s", s3.Code, s3.Body.String())
		}
	})

	t.Run("Scenario 2: Detail query, listing and multi-condition filtering", func(t *testing.T) {
		// 2.1 Get single survey detail
		w := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys/1", nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on get survey 1, got %d: %s", w.Code, w.Body.String())
		}
		s1 := getMap(w.Body.Bytes())
		if r, _ := s1["rating"].(float64); int(r) != 5 {
			t.Errorf("expected rating 5, got %v", r)
		}

		// Alias endpoint /csat_survey_responses/:id
		aliasW := doReq(http.MethodGet, "/api/v1/accounts/1/csat_survey_responses/1", nil, token1)
		if aliasW.Code != http.StatusOK {
			t.Fatalf("expected 200 on alias endpoint, got %d: %s", aliasW.Code, aliasW.Body.String())
		}

		// 2.2 Filter by rating = 5
		filterW := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys?rating=5", nil, token1)
		if filterW.Code != http.StatusOK {
			t.Fatalf("expected 200 on filter rating, got %d: %s", filterW.Code, filterW.Body.String())
		}
		var listResp struct {
			Data struct {
				Items []domain.CSATSurvey `json:"items"`
				Total int64               `json:"total"`
			} `json:"data"`
		}
		if err := json.Unmarshal(filterW.Body.Bytes(), &listResp); err != nil {
			t.Fatalf("failed to unmarshal list response: %v", err)
		}
		if len(listResp.Data.Items) != 1 || listResp.Data.Items[0].Rating != 5 {
			t.Errorf("expected 1 record with rating 5, got %d", len(listResp.Data.Items))
		}

		// 2.3 Search query q="排队"
		searchW := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys?q=排队", nil, token1)
		if searchW.Code != http.StatusOK {
			t.Fatalf("expected 200 on search query, got %d: %s", searchW.Code, searchW.Body.String())
		}
		var searchResp struct {
			Data struct {
				Items []domain.CSATSurvey `json:"items"`
			} `json:"data"`
		}
		_ = json.Unmarshal(searchW.Body.Bytes(), &searchResp)
		if len(searchResp.Data.Items) != 1 || searchResp.Data.Items[0].Rating != 2 {
			t.Errorf("expected 1 record matching '排队', got %d", len(searchResp.Data.Items))
		}

		// 2.4 Pagination
		pageW := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys?page=1&page_size=2", nil, token1)
		if pageW.Code != http.StatusOK {
			t.Fatalf("expected 200 on paginated request, got %d: %s", pageW.Code, pageW.Body.String())
		}
		var pageResp struct {
			Data struct {
				Items      []domain.CSATSurvey `json:"items"`
				Total      int64               `json:"total"`
				Page       int                 `json:"page"`
				PageSize   int                 `json:"page_size"`
				TotalPages int                 `json:"total_pages"`
			} `json:"data"`
		}
		_ = json.Unmarshal(pageW.Body.Bytes(), &pageResp)
		if len(pageResp.Data.Items) != 2 {
			t.Errorf("expected 2 records on page 1, got %d", len(pageResp.Data.Items))
		}
		if pageResp.Data.Total != 3 {
			t.Errorf("expected total count 3, got %d", pageResp.Data.Total)
		}
	})

	t.Run("Scenario 3: Single and bulk audit/review moderation", func(t *testing.T) {
		// 3.1 Single review (PUT /csat_surveys/:id/review)
		reviewW := doReq(http.MethodPut, "/api/v1/accounts/1/csat_surveys/1/review", map[string]any{
			"review_status": "approved",
			"review_notes":  "五星好评，坐席解答专业标准",
		}, token1)
		if reviewW.Code != http.StatusOK {
			t.Fatalf("expected 200 on review, got %d: %s", reviewW.Code, reviewW.Body.String())
		}
		reviewed := getMap(reviewW.Body.Bytes())
		if s, _ := reviewed["review_status"].(string); s != "approved" {
			t.Errorf("expected review_status approved, got %v", s)
		}
		if n, _ := reviewed["review_notes"].(string); n != "五星好评，坐席解答专业标准" {
			t.Errorf("expected review notes match, got %v", n)
		}
		if rAt, _ := reviewed["reviewed_at"].(string); rAt == "" {
			t.Errorf("expected non-empty reviewed_at")
		}

		// 3.2 Bulk review (POST /csat_surveys/bulk_review)
		bulkW := doReq(http.MethodPost, "/api/v1/accounts/1/csat_surveys/bulk_review", map[string]any{
			"ids":           []uint{2, 3},
			"review_status": "rejected",
			"review_notes":  "批量质检复核完成",
		}, token1)
		if bulkW.Code != http.StatusOK {
			t.Fatalf("expected 200 on bulk review, got %d: %s", bulkW.Code, bulkW.Body.String())
		}
		bulkData := getMap(bulkW.Body.Bytes())
		if count, _ := bulkData["updated_count"].(float64); int(count) != 2 {
			t.Errorf("expected updated_count 2, got %v", count)
		}

		// 3.3 Verify list filtering by review_status
		listAppW := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys?review_status=approved", nil, token1)
		if listAppW.Code != http.StatusOK {
			t.Fatalf("expected 200 on list approved, got %d", listAppW.Code)
		}
		var appResp struct {
			Data struct {
				Items []domain.CSATSurvey `json:"items"`
			} `json:"data"`
		}
		_ = json.Unmarshal(listAppW.Body.Bytes(), &appResp)
		if len(appResp.Data.Items) != 1 || appResp.Data.Items[0].ID != 1 {
			t.Errorf("expected 1 approved survey with ID 1, got %d", len(appResp.Data.Items))
		}
	})

	t.Run("Scenario 4: CSV download and UTF-8 BOM verification", func(t *testing.T) {
		// 4.1 Download CSV via /csat_surveys/download
		csvW := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys/download", nil, token1)
		if csvW.Code != http.StatusOK {
			t.Fatalf("expected 200 on csv download, got %d: %s", csvW.Code, csvW.Body.String())
		}

		// Check Headers
		contentType := csvW.Header().Get("Content-Type")
		if !strings.Contains(contentType, "text/csv") {
			t.Errorf("expected text/csv content-type, got %v", contentType)
		}
		contentDisp := csvW.Header().Get("Content-Disposition")
		if !strings.Contains(contentDisp, "attachment; filename=") {
			t.Errorf("expected attachment content disposition, got %v", contentDisp)
		}

		bodyBytes := csvW.Body.Bytes()
		// Check UTF-8 BOM (\xEF\xBB\xBF)
		if len(bodyBytes) < 3 || bodyBytes[0] != 0xEF || bodyBytes[1] != 0xBB || bodyBytes[2] != 0xBF {
			t.Fatalf("expected UTF-8 BOM at start of CSV file")
		}

		// Parse CSV rows after BOM
		reader := csv.NewReader(bytes.NewReader(bodyBytes[3:]))
		records, err := reader.ReadAll()
		if err != nil {
			t.Fatalf("failed to parse csv: %v", err)
		}

		if len(records) < 4 { // 1 header + 3 data rows
			t.Fatalf("expected at least 4 csv rows (1 header + 3 records), got %d", len(records))
		}

		// Validate headers
		headers := records[0]
		expectedHeaderPrefix := []string{"ID", "Conversation ID", "Contact Name", "Contact Email", "Assigned Agent", "Rating", "Feedback Text", "Review Status"}
		for i, h := range expectedHeaderPrefix {
			if headers[i] != h {
				t.Errorf("expected header %d to be %q, got %q", i, h, headers[i])
			}
		}

		// Validate Chinese content
		csvContent := string(bodyBytes)
		if !strings.Contains(csvContent, "客服服务态度极佳，问题解决迅速！") {
			t.Errorf("expected CSV to contain Chinese feedback text without corruption")
		}
		if !strings.Contains(csvContent, "五星好评，坐席解答专业标准") {
			t.Errorf("expected CSV to contain Chinese review notes without corruption")
		}

		// Also check reports endpoint /reports/csat/download
		reportCsvW := doReq(http.MethodGet, "/api/v2/accounts/1/reports/csat/download", nil, token1)
		if reportCsvW.Code != http.StatusOK {
			t.Fatalf("expected 200 on reports csat download, got %d: %s", reportCsvW.Code, reportCsvW.Body.String())
		}
	})

	t.Run("Scenario 5: Metrics calculation and agent rankings", func(t *testing.T) {
		// 5.1 Metrics via /csat_surveys/metrics
		w := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys/metrics", nil, token1)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on csat metrics, got %d: %s", w.Code, w.Body.String())
		}

		var metricsResp struct {
			Data struct {
				TotalResponses        int64             `json:"total_responses"`
				AverageRating         float64           `json:"average_rating"`
				SatisfactionRate      float64           `json:"satisfaction_rate"`
				RatingBreakdown       map[string]int64  `json:"rating_breakdown"`
				ReviewStatusBreakdown map[string]int64  `json:"review_status_breakdown"`
				AgentMetrics          []any             `json:"agent_metrics"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &metricsResp); err != nil {
			t.Fatalf("failed to unmarshal metrics response: %v", err)
		}

		if metricsResp.Data.TotalResponses != 3 {
			t.Errorf("expected total_responses 3, got %d", metricsResp.Data.TotalResponses)
		}

		// Ratings: 5, 2, 4 -> avg = 3.67
		if metricsResp.Data.AverageRating < 3.6 || metricsResp.Data.AverageRating > 3.7 {
			t.Errorf("expected average_rating around 3.67, got %v", metricsResp.Data.AverageRating)
		}

		// Ratings >= 4: 2 out of 3 = 66.67%
		if metricsResp.Data.SatisfactionRate < 66.0 || metricsResp.Data.SatisfactionRate > 67.0 {
			t.Errorf("expected satisfaction_rate around 66.67, got %v", metricsResp.Data.SatisfactionRate)
		}

		// Review status breakdown: approved=1, rejected=2
		if metricsResp.Data.ReviewStatusBreakdown["approved"] != 1 {
			t.Errorf("expected 1 approved, got %d", metricsResp.Data.ReviewStatusBreakdown["approved"])
		}
		if metricsResp.Data.ReviewStatusBreakdown["rejected"] != 2 {
			t.Errorf("expected 2 rejected, got %d", metricsResp.Data.ReviewStatusBreakdown["rejected"])
		}

		// 5.2 Check reports responses route
		reportsW := doReq(http.MethodGet, "/api/v2/accounts/1/reports/csat/responses", nil, token1)
		if reportsW.Code != http.StatusOK {
			t.Fatalf("expected 200 on /reports/csat/responses, got %d: %s", reportsW.Code, reportsW.Body.String())
		}
	})

	t.Run("Scenario 6: Multi-tenant boundary protection and deletion", func(t *testing.T) {
		// 6.1 Sign up Account 2 (Beta Isolation Test)
		w2 := doReq(http.MethodPost, "/auth/sign_up", map[string]any{
			"account_name": "Beta Isolation Corp",
			"name":         "Beta Admin",
			"email":        "beta@isolation.com",
			"password":     "Password123!",
		}, "")
		if w2.Code != http.StatusCreated {
			t.Fatalf("failed to sign up account 2: %s", w2.Body.String())
		}
		var authResp2 struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
		token2 := authResp2.Data.Token

		// 6.2 Account 2 attempts to query Account 1's CSAT survey 1 -> 404
		crossGet := doReq(http.MethodGet, "/api/v1/accounts/2/csat_surveys/1", nil, token2)
		if crossGet.Code != http.StatusNotFound {
			t.Errorf("expected 404 on cross-tenant get, got %d", crossGet.Code)
		}

		// 6.3 Account 2 attempts to review Account 1's CSAT survey 1 -> 404
		crossReview := doReq(http.MethodPost, "/api/v1/accounts/2/csat_surveys/1/review", map[string]any{
			"review_status": "flagged",
		}, token2)
		if crossReview.Code != http.StatusNotFound {
			t.Errorf("expected 404 on cross-tenant review, got %d", crossReview.Code)
		}

		// 6.4 Account 2 attempts to delete Account 1's CSAT survey 1 -> 404
		crossDel := doReq(http.MethodDelete, "/api/v1/accounts/2/csat_surveys/1", nil, token2)
		if crossDel.Code != http.StatusNotFound {
			t.Errorf("expected 404 on cross-tenant delete, got %d", crossDel.Code)
		}

		// 6.5 Account 1 deletes survey 3
		delW := doReq(http.MethodDelete, "/api/v1/accounts/1/csat_surveys/3", nil, token1)
		if delW.Code != http.StatusOK {
			t.Fatalf("expected 200 on delete survey 3, got %d: %s", delW.Code, delW.Body.String())
		}

		// Verify survey 3 is deleted
		getAfterDel := doReq(http.MethodGet, "/api/v1/accounts/1/csat_surveys/3", nil, token1)
		if getAfterDel.Code != http.StatusNotFound {
			t.Errorf("expected 404 after deletion, got %d", getAfterDel.Code)
		}
	})
}
