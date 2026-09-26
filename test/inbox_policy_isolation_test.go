package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func testInboxPolicyBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	chnDB, rteDB := openStore("CHN"), openStore("RTE")
	fallbackID := uint(99)
	policy := domain.AssignmentPolicy{AccountID: 1, Name: "Owner policy", FallbackAssigneeID: &fallbackID}
	foreign := domain.AssignmentPolicy{AccountID: 2, Name: "Private policy"}
	for _, p := range []*domain.AssignmentPolicy{&policy, &foreign} {
		if err := rteDB.Create(p).Error; err != nil {
			t.Fatal(err)
		}
	}
	owner := repository.NewInboxRepositoryWithAccountPort(chnDB, nil)
	inbox := domain.Inbox{AccountID: 1, Name: "Inbox", WebsiteToken: "chn-rte-split", AssignmentPolicyID: &policy.ID, AssignmentPolicy: &policy}
	if err := owner.Create(&inbox); err != nil {
		t.Fatal(err)
	}
	if err := owner.Update(&inbox); err != nil {
		t.Fatal(err)
	}
	found, err := owner.FindByID(1, inbox.ID)
	if err != nil || found == nil || found.AssignmentPolicy != nil {
		t.Fatalf("CHN policy read: %v %v", found, err)
	}
	byToken, err := owner.FindByWebsiteToken(inbox.WebsiteToken)
	if err != nil || byToken == nil || byToken.AssignmentPolicy != nil {
		t.Fatalf("token lookup reads RTE: %v %v", byToken, err)
	}
	if chnDB.Migrator().HasTable(&domain.AssignmentPolicy{}) {
		t.Fatal("CHN created RTE table")
	}
	h := handler.NewInboxHandler(owner, nil)
	h.SetAssignmentPolicyRepo(repository.NewAssignmentPolicyRepository(rteDB))
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(1)); c.Next() })
	r.GET("/inboxes", h.ListInboxes)
	r.GET("/inboxes/:id", h.GetInbox)
	r.POST("/inboxes", h.CreateInbox)
	r.PATCH("/inboxes/:id", h.UpdateInbox)
	r.POST("/inboxes/:id/assignment_policy", h.BindAssignmentPolicy)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	path := fmt.Sprintf("/inboxes/%d", inbox.ID)
	w := call(http.MethodGet, path, "")
	var payload struct {
		Data domain.Inbox `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != 200 || payload.Data.AssignmentPolicy == nil || payload.Data.AssignmentPolicy.Name != policy.Name {
		t.Fatalf("policy composition failed: %d %s %v", w.Code, w.Body.String(), err)
	}
	w = call(http.MethodGet, "/inboxes", "")
	var list struct {
		Data []domain.Inbox `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.Data) != 1 || list.Data[0].AssignmentPolicy == nil {
		t.Fatalf("list composition failed: %d %s", w.Code, w.Body.String())
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodPost, "/inboxes", fmt.Sprintf(`{"name":"Rejected","assignment_policy_id":%d}`, foreign.ID)},
		{http.MethodPatch, path, fmt.Sprintf(`{"name":"Rejected","assignment_policy_id":%d}`, foreign.ID)},
		{http.MethodPost, path + "/assignment_policy", fmt.Sprintf(`{"assignment_policy_id":%d,"reassign":true}`, foreign.ID)},
	} {
		if w := call(request.method, request.path, request.body); w.Code != 404 {
			t.Fatalf("foreign policy accepted: %d %s", w.Code, w.Body.String())
		}
	}
	found, err = owner.FindByID(1, inbox.ID)
	if err != nil || found.Name != "Inbox" || *found.AssignmentPolicyID != policy.ID {
		t.Fatalf("rejected update wrote CHN: %v %v", found, err)
	}
	conn, err := rteDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if w := call(http.MethodGet, path, ""); w.Code != 500 {
		t.Fatalf("RTE outage masked: %d %s", w.Code, w.Body.String())
	}
	if found, err := owner.FindByWebsiteToken(inbox.WebsiteToken); err != nil || found == nil {
		t.Fatalf("RTE outage broke CHN lookup: %v %v", found, err)
	}
}
