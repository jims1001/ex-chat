package test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func testAppliedSLAConversationBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	gin.SetMode(gin.TestMode)
	rptDB, conDB, cusDB, chnDB := openStore("RPT"), openStore("CON"), openStore("CUS"), openStore("CHN")
	inbox := domain.Inbox{AccountID: 1, Name: "SLA Inbox", WebsiteToken: "sla-inbox"}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	conversation := domain.Conversation{AccountID: 1, InboxID: inbox.ID, ContactID: 1, DisplayID: 1, SLAStatus: "missed"}
	foreignConversation := domain.Conversation{AccountID: 2, InboxID: inbox.ID, ContactID: 2, DisplayID: 1, SLAStatus: "missed"}
	if err := conDB.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&foreignConversation).Error; err != nil {
		t.Fatal(err)
	}
	label := domain.Label{AccountID: 1, Title: "vip"}
	if err := cusDB.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	if err := conDB.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: label.ID}).Error; err != nil {
		t.Fatal(err)
	}
	policy := domain.SLAPolicy{AccountID: 1, Name: "Gold", FirstResponseTimeThreshold: 60}
	if err := rptDB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	applied := domain.AppliedSLA{AccountID: 1, ConversationID: conversation.ID, SLAPolicyID: policy.ID, SLAStatus: "missed"}
	foreignApplied := domain.AppliedSLA{AccountID: 2, ConversationID: foreignConversation.ID, SLAPolicyID: policy.ID, SLAStatus: "missed"}
	if err := rptDB.Create(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if err := rptDB.Create(&foreignApplied).Error; err != nil {
		t.Fatal(err)
	}

	convRepo := repository.NewConversationRepositoryWithReadPorts(conDB, nil, nil, repository.NewInboxRepository(chnDB), nil, nil, nil, repository.NewLabelRepository(cusDB))
	h := handler.NewAppliedSLAHandler(rptDB, repository.NewAppliedSLARepository(rptDB), nil, convRepo)
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(1)); c.Next() })
	router.GET("/applied_slas", h.ListAppliedSLAs)
	router.GET("/applied_slas/metrics", h.GetMetrics)
	router.GET("/applied_slas/download", h.Download)
	router.DELETE("/conversations/:id/sla", h.RemoveSLA)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/applied_slas?label=vip&inbox_id=1", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"count":1`)) {
		t.Fatalf("RPT list was not composed through CON: code=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/applied_slas/metrics?label=vip", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"total_applied_slas":1`)) {
		t.Fatalf("RPT metrics were not filtered through CON: code=%d body=%s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/applied_slas/download?label=vip", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("SLA Inbox")) {
		t.Fatalf("RPT export was not hydrated through CON/CHN: code=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/conversations/"+itoa(conversation.ID)+"/sla", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("remove SLA code=%d body=%s", w.Code, w.Body.String())
	}
	var updated domain.Conversation
	if err := conDB.First(&updated, conversation.ID).Error; err != nil || updated.SLAPolicyID != nil || updated.SLAStatus != "active" {
		t.Fatalf("CON projection was not reset: conversation=%+v err=%v", updated, err)
	}
	var foreignCount int64
	if err := rptDB.Model(&domain.AppliedSLA{}).Where("account_id = ?", 2).Count(&foreignCount).Error; err != nil || foreignCount != 1 {
		t.Fatalf("cross-account RPT row changed: count=%d err=%v", foreignCount, err)
	}
	if rptDB.Migrator().HasTable(&domain.Conversation{}) || rptDB.Migrator().HasTable(&domain.ConversationLabel{}) || rptDB.Migrator().HasTable(&domain.Label{}) {
		t.Fatal("RPT created CON or CUS authority tables")
	}

	outageSQL, err := conDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := outageSQL.Close(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/applied_slas?inbox_id=1", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("CON outage was hidden: code=%d body=%s", w.Code, w.Body.String())
	}
}

func itoa(id uint) string {
	const digits = "0123456789"
	if id == 0 {
		return "0"
	}
	buf := make([]byte, 0, 20)
	for id > 0 {
		buf = append(buf, digits[id%10])
		id /= 10
	}
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	return string(buf)
}
