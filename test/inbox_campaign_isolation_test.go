package test

import (
	"context"
	"encoding/json"
	"fmt"
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

func testInboxCampaignBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	chnDB, opsDB := openStore("CHN"), openStore("OPS")
	inboxes := []domain.Inbox{
		{AccountID: 1, Name: "Main", WebsiteToken: "campaign-main"},
		{AccountID: 1, Name: "Other", WebsiteToken: "campaign-other"},
		{AccountID: 2, Name: "Foreign", WebsiteToken: "campaign-foreign"},
		{AccountID: 1, Name: "Empty", WebsiteToken: "campaign-empty"},
	}
	for i := range inboxes {
		if err := chnDB.Create(&inboxes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Listing campaigns must not require TEN/IAM member hydration.
	if err := chnDB.Create(&domain.InboxMember{InboxID: inboxes[0].ID, UserID: 99}).Error; err != nil {
		t.Fatal(err)
	}
	campaigns := []domain.Campaign{
		{AccountID: 1, InboxID: inboxes[0].ID, Title: "First", Message: "one", CampaignType: "one_off", Status: "draft"},
		{AccountID: 1, InboxID: inboxes[0].ID, Title: "Second", Message: "two", CampaignType: "ongoing", Status: "active"},
		{AccountID: 1, InboxID: inboxes[1].ID, Title: "Other inbox", Message: "other"},
		{AccountID: 2, InboxID: inboxes[0].ID, Title: "Other account same reference", Message: "private"},
		{AccountID: 2, InboxID: inboxes[2].ID, Title: "Foreign inbox", Message: "private"},
	}
	for i := range campaigns {
		if err := opsDB.Create(&campaigns[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	owner := repository.NewInboxRepositoryWithAccountPort(chnDB, nil)
	operations := repository.NewCampaignRepository(opsDB)
	h := handler.NewInboxHandler(owner, nil)
	h.SetCampaignRepo(operations)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(1)); c.Next() })
	r.GET("/inboxes/:id/campaigns", h.ListInboxCampaigns)
	call := func(id string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/inboxes/"+id+"/campaigns", nil))
		return w
	}
	mainID := fmt.Sprint(inboxes[0].ID)
	w := call(mainID)
	var payload struct {
		Success bool              `json:"success"`
		Data    []domain.Campaign `json:"data"`
		Payload []domain.Campaign `json:"payload"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != 200 || !payload.Success || len(payload.Data) != 2 || len(payload.Payload) != 2 {
		t.Fatalf("campaign composition failed: %d %s err=%v", w.Code, w.Body.String(), err)
	}
	for i, id := range []uint{campaigns[1].ID, campaigns[0].ID} {
		if payload.Data[i].ID != id || payload.Payload[i].ID != id {
			t.Fatalf("scope/order/response changed: %+v", payload)
		}
	}
	for _, tc := range []struct {
		id     string
		status int
	}{
		{fmt.Sprint(inboxes[2].ID), 404}, {"99999", 404}, {"invalid", 400},
	} {
		if w := call(tc.id); w.Code != tc.status {
			t.Fatalf("id=%s status=%d body=%s", tc.id, w.Code, w.Body.String())
		}
	}
	w = call(fmt.Sprint(inboxes[3].ID))
	payload.Data, payload.Payload = nil, nil
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || w.Code != 200 || len(payload.Data) != 0 || len(payload.Payload) != 0 {
		t.Fatalf("empty inbox query: %d %s", w.Code, w.Body.String())
	}
	h.SetCampaignRepo(nil)
	if w := call(mainID); w.Code != 500 {
		t.Fatalf("missing OPS dependency hidden: %d", w.Code)
	}
	h.SetCampaignRepo(operations)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := operations.ListByInbox(ctx, 1, inboxes[0].ID); err == nil {
		t.Fatal("OPS ignored canceled request")
	}
	if chnDB.Migrator().HasTable(&domain.Campaign{}) || opsDB.Migrator().HasTable(&domain.Inbox{}) {
		t.Fatal("foreign authority table created")
	}
	conn, err := opsDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if w := call(mainID); w.Code != 500 {
		t.Fatalf("OPS failure masked: %d %s", w.Code, w.Body.String())
	}
	if found, err := owner.FindByWebsiteToken(inboxes[0].WebsiteToken); err != nil || found == nil {
		t.Fatalf("OPS outage broke CHN: %v %v", found, err)
	}
	conn, err = chnDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if w := call(mainID); w.Code != 500 {
		t.Fatalf("CHN failure reported as missing: %d", w.Code)
	}
}
