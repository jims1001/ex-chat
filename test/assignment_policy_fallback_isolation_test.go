package test

import (
	"bytes"
	"encoding/json"
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

func testAssignmentPolicyFallbackBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	gin.SetMode(gin.TestMode)
	rteDB, tenDB, iamDB := openStore("RTE"), openStore("TEN"), openStore("IAM")

	member := domain.User{Name: "Fallback Agent", Email: "fallback@example.test"}
	foreignUser := domain.User{Name: "Foreign Agent", Email: "foreign@example.test"}
	if err := iamDB.Create(&member).Error; err != nil {
		t.Fatal(err)
	}
	if err := iamDB.Create(&foreignUser).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 1, UserID: member.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 2, UserID: foreignUser.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	ownedTeam := domain.Team{AccountID: 1, Name: "Primary Team"}
	foreignTeam := domain.Team{AccountID: 2, Name: "Foreign Team"}
	if err := tenDB.Create(&ownedTeam).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&foreignTeam).Error; err != nil {
		t.Fatal(err)
	}

	repo := repository.NewAssignmentPolicyRepository(rteDB)
	policy := domain.AssignmentPolicy{
		AccountID:          1,
		Name:               "Owner-only fallbacks",
		StrategyType:       "round_robin",
		FallbackAssigneeID: &member.ID,
		FallbackTeamID:     &ownedTeam.ID,
		FallbackAssignee:   &member,
		FallbackTeam:       &ownedTeam,
	}
	if err := repo.Create(&policy); err != nil {
		t.Fatalf("RTE create attempted a foreign aggregate write: %v", err)
	}
	policy.Name = "Owner-only fallbacks updated"
	if err := repo.Update(&policy); err != nil {
		t.Fatalf("RTE update attempted a foreign aggregate write: %v", err)
	}
	if rteDB.Migrator().HasTable(&domain.User{}) || rteDB.Migrator().HasTable(&domain.Team{}) || rteDB.Migrator().HasTable(&domain.AccountUser{}) {
		t.Fatal("RTE created an IAM or TEN authority table")
	}

	userRepo := repository.NewUserRepository(iamDB)
	accountRepo := repository.NewAccountRepositoryWithUserPort(tenDB, userRepo)
	teamRepo := repository.NewTeamRepositoryWithUserPort(tenDB, userRepo)
	policyHandler := handler.NewAssignmentPolicyHandler(repo, nil)
	policyHandler.SetFallbackRepositories(accountRepo, teamRepo)

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(middleware.ContextAccountID, uint(1))
		c.Next()
	})
	router.GET("/policies", policyHandler.List)
	router.GET("/policies/:id", policyHandler.Get)
	router.POST("/policies", policyHandler.Create)
	router.PUT("/policies/:id", policyHandler.Update)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/policies/1", nil))
	var detail struct {
		Data domain.AssignmentPolicy `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK {
		t.Fatalf("policy detail code=%d body=%s err=%v", w.Code, w.Body.String(), err)
	}
	if detail.Data.FallbackAssignee == nil || detail.Data.FallbackAssignee.ID != member.ID || detail.Data.FallbackTeam == nil || detail.Data.FallbackTeam.ID != ownedTeam.ID {
		t.Fatalf("owner composition missing: %+v", detail.Data)
	}

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/policies", nil))
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("Fallback Agent")) || !bytes.Contains(w.Body.Bytes(), []byte("Primary Team")) {
		t.Fatalf("policy list did not compose owner data: code=%d body=%s", w.Code, w.Body.String())
	}

	createBody := []byte(`{"name":"invalid fallback","fallback_assignee_id":` + jsonUint(foreignUser.ID) + `}`)
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/policies", bytes.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-account assignee accepted: code=%d body=%s", w.Code, w.Body.String())
	}
	var count int64
	if err := rteDB.Model(&domain.AssignmentPolicy{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("invalid create changed RTE: count=%d err=%v", count, err)
	}

	updateBody := []byte(`{"fallback_team_id":` + jsonUint(foreignTeam.ID) + `}`)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/policies/1", bytes.NewReader(updateBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("cross-account team accepted: code=%d body=%s", w.Code, w.Body.String())
	}
	stored, err := repo.FindSummaryByID(1, policy.ID)
	if err != nil || stored == nil || stored.FallbackTeamID == nil || *stored.FallbackTeamID != ownedTeam.ID {
		t.Fatalf("invalid update changed stored fallback: policy=%+v err=%v", stored, err)
	}

	outageIAM := openStore("IAM")
	outageSQL, err := outageIAM.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := outageSQL.Close(); err != nil {
		t.Fatal(err)
	}
	outageAccountRepo := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(outageIAM))
	policyHandler.SetFallbackRepositories(outageAccountRepo, teamRepo)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/policies/1", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("IAM outage was hidden: code=%d body=%s", w.Code, w.Body.String())
	}
}

func jsonUint(value uint) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
