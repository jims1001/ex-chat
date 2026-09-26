package test

import (
	"bytes"
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

func testAgentCapacityPolicyOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	gin.SetMode(gin.TestMode)
	rteDB, tenDB, iamDB, chnDB := openStore("RTE"), openStore("TEN"), openStore("IAM"), openStore("CHN")
	member := domain.User{Name: "Capacity Agent", Email: "capacity@example.test"}
	foreign := domain.User{Name: "Foreign Capacity Agent", Email: "foreign-capacity@example.test"}
	if err := iamDB.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err := iamDB.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 1, UserID: member.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 2, UserID: foreign.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	ownedInbox := domain.Inbox{AccountID: 1, Name: "Owned Capacity Inbox", WebsiteToken: "capacity-owned"}
	foreignInbox := domain.Inbox{AccountID: 2, Name: "Foreign Capacity Inbox", WebsiteToken: "capacity-foreign"}
	if err := chnDB.Create(&ownedInbox).Error; err != nil {
		t.Fatal(err)
	}
	if err := chnDB.Create(&foreignInbox).Error; err != nil {
		t.Fatal(err)
	}

	repo := repository.NewAgentCapacityPolicyRepository(rteDB)
	policy := domain.AgentCapacityPolicy{
		AccountID: 1,
		Name:      "Separated capacity",
		AccountUsers: []domain.AccountUser{
			{AccountID: 1, UserID: member.ID},
		},
		InboxCapacityLimits: []domain.InboxCapacityLimit{
			{InboxID: ownedInbox.ID, ConversationLimit: 99},
		},
	}
	if err := repo.Create(&policy); err != nil {
		t.Fatalf("RTE create attempted a foreign association write: %v", err)
	}
	if err := repo.Update(&policy); err != nil {
		t.Fatalf("RTE update attempted a foreign association write: %v", err)
	}
	limit, err := repo.CreateInboxLimit(policy.ID, ownedInbox.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB)).AssignCapacityPolicy(1, member.ID, policy.ID); err != nil {
		t.Fatal(err)
	}

	userRepo := repository.NewUserRepository(iamDB)
	accountRepo := repository.NewAccountRepositoryWithUserPort(tenDB, userRepo)
	inboxRepo := repository.NewInboxRepositoryWithAccountPort(chnDB, accountRepo)
	h := handler.NewAgentCapacityPolicyHandler(repo)
	h.SetOwnerRepositories(accountRepo, inboxRepo)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextAccountID, uint(1))
		c.Next()
	})
	router.GET("/policies", h.List)
	router.GET("/policies/:id", h.Show)
	router.DELETE("/policies/:id", h.Delete)
	router.GET("/policies/:id/users", h.ListUsers)
	router.POST("/policies/:id/users", h.AddUser)
	router.DELETE("/policies/:id/users/:user_id", h.RemoveUser)
	router.POST("/policies/:id/limits", h.CreateInboxLimit)
	router.PUT("/policies/:id/limits/:limit_id", h.UpdateInboxLimit)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/policies/%d", policy.ID), nil))
	var detail struct {
		Data domain.AgentCapacityPolicy `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK || detail.Data.UsersCount != 1 || len(detail.Data.InboxCapacityLimits) != 1 || detail.Data.InboxCapacityLimits[0].Inbox == nil || detail.Data.InboxCapacityLimits[0].Inbox.ID != ownedInbox.ID {
		t.Fatalf("composed policy code=%d body=%s data=%+v err=%v", w.Code, w.Body.String(), detail.Data, err)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/policies/%d/users", policy.ID), nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("Capacity Agent")) {
		t.Fatalf("TEN/IAM members code=%d body=%s", w.Code, w.Body.String())
	}

	foreignUserBody := []byte(fmt.Sprintf(`{"user_id":%d}`, foreign.ID))
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/policies/%d/users", policy.ID), bytes.NewReader(foreignUserBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-account member accepted: code=%d body=%s", w.Code, w.Body.String())
	}

	foreignInboxBody := []byte(fmt.Sprintf(`{"inbox_id":%d,"conversation_limit":3}`, foreignInbox.ID))
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/policies/%d/limits", policy.ID), bytes.NewReader(foreignInboxBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-account inbox accepted: code=%d body=%s", w.Code, w.Body.String())
	}

	updateBody := []byte(`{"conversation_limit":4}`)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, fmt.Sprintf("/policies/%d/limits/%d", policy.ID, limit.ID), bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("Owned Capacity Inbox")) {
		t.Fatalf("limit update was not composed through CHN: code=%d body=%s", w.Code, w.Body.String())
	}

	if rteDB.Migrator().HasTable(&domain.AccountUser{}) || rteDB.Migrator().HasTable(&domain.User{}) || rteDB.Migrator().HasTable(&domain.Inbox{}) {
		t.Fatal("RTE created a TEN, IAM, or CHN authority table")
	}
	var limitCount int64
	if err := rteDB.Model(&domain.InboxCapacityLimit{}).Count(&limitCount).Error; err != nil || limitCount != 1 {
		t.Fatalf("cross-account limit changed RTE: count=%d err=%v", limitCount, err)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/policies/%d", policy.ID), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("delete policy code=%d body=%s", w.Code, w.Body.String())
	}
	membership, err := accountRepo.GetMembership(1, member.ID)
	if err != nil || membership == nil || membership.AgentCapacityPolicyID != nil {
		t.Fatalf("TEN reference was not cleared: member=%+v err=%v", membership, err)
	}
	if found, err := repo.FindSummaryByID(1, policy.ID); err != nil || found != nil {
		t.Fatalf("RTE policy was not deleted: policy=%+v err=%v", found, err)
	}

	outageCHN := openStore("CHN")
	outageSQL, err := outageCHN.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := outageSQL.Close(); err != nil {
		t.Fatal(err)
	}
	outagePolicy := domain.AgentCapacityPolicy{AccountID: 1, Name: "Outage"}
	if err := repo.Create(&outagePolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateInboxLimit(outagePolicy.ID, ownedInbox.ID, 1); err != nil {
		t.Fatal(err)
	}
	h.SetOwnerRepositories(accountRepo, repository.NewInboxRepositoryWithAccountPort(outageCHN, accountRepo))
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/policies/%d", outagePolicy.ID), nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("CHN outage was hidden: code=%d body=%s", w.Code, w.Body.String())
	}
}
