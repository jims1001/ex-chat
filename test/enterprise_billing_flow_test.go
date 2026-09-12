package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestEnterpriseBillingFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "enterprise_billing_test_secret_32b!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Create test user & account
	account := &domain.Account{
		Name: "Enterprise Billing Account",
	}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	adminUser := &domain.User{
		Email: "billing_admin@enterprise.com",
		Name:  "Billing Admin",
		Role:  domain.RoleAdministrator,
	}
	if err := db.Create(adminUser).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	accountUser := &domain.AccountUser{
		AccountID: account.ID,
		UserID:    adminUser.ID,
		Role:      domain.RoleAdministrator,
	}
	if err := db.Create(accountUser).Error; err != nil {
		t.Fatalf("failed to create account_user: %v", err)
	}

	token, err := auth.GenerateToken(adminUser, cfg.JWTSecret, cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	makeReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		rq := httptest.NewRequest(method, path, reqBody)
		rq.Header.Set("Authorization", "Bearer "+token)
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, rq)
		return rec
	}

	entBase := fmt.Sprintf("/enterprise/api/v1/accounts/%d", account.ID)
	v1Base := fmt.Sprintf("/api/v1/accounts/%d", account.ID)

	// 1. Checkout session generation
	t.Run("1_Checkout", func(t *testing.T) {
		// Test on enterprise route
		res1 := makeReq(http.MethodPost, entBase+"/checkout", nil)
		if res1.Code != http.StatusOK {
			t.Fatalf("expected 200 on enterprise checkout, got %d: %s", res1.Code, res1.Body.String())
		}
		var data1 struct {
			RedirectURL string `json:"redirect_url"`
		}
		_ = json.Unmarshal(res1.Body.Bytes(), &data1)
		if !strings.Contains(data1.RedirectURL, "https://billing.stripe.com/session/") {
			t.Errorf("expected stripe redirect_url, got %s", data1.RedirectURL)
		}

		// Test on standard v1 route
		res2 := makeReq(http.MethodPost, v1Base+"/checkout", nil)
		if res2.Code != http.StatusOK {
			t.Fatalf("expected 200 on v1 checkout, got %d: %s", res2.Code, res2.Body.String())
		}
	})

	// 2. Select Billing Currency
	t.Run("2_SelectBillingCurrency", func(t *testing.T) {
		res := makeReq(http.MethodPost, entBase+"/select_billing_currency", map[string]string{
			"currency": "EUR",
		})
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on select_billing_currency, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			BillingCurrency string `json:"billing_currency"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if data.BillingCurrency != "eur" {
			t.Errorf("expected currency 'eur', got %s", data.BillingCurrency)
		}

		// Also check v1 route
		resV1 := makeReq(http.MethodPost, v1Base+"/select_billing_currency", map[string]string{
			"currency": "USD",
		})
		if resV1.Code != http.StatusOK {
			t.Fatalf("expected 200 on v1 select_billing_currency, got %d: %s", resV1.Code, resV1.Body.String())
		}
	})

	// 3. Toggle Deletion (delete & undelete switch)
	t.Run("3_ToggleDeletion", func(t *testing.T) {
		// Test invalid action
		badRes := makeReq(http.MethodPost, entBase+"/toggle_deletion", map[string]string{
			"action_type": "destroy_all",
		})
		if badRes.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 for invalid action_type, got %d", badRes.Code)
		}

		// Test mark for deletion
		delRes := makeReq(http.MethodPost, entBase+"/toggle_deletion", map[string]string{
			"action_type": "delete",
		})
		if delRes.Code != http.StatusOK {
			t.Fatalf("expected 200 for mark deletion, got %d: %s", delRes.Code, delRes.Body.String())
		}
		var delData struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(delRes.Body.Bytes(), &delData)
		if delData.Message != "Account marked for deletion" {
			t.Errorf("expected 'Account marked for deletion', got %s", delData.Message)
		}

		// Test unmark for deletion on v1 route
		undelRes := makeReq(http.MethodPost, v1Base+"/toggle_deletion", map[string]string{
			"action_type": "undelete",
		})
		if undelRes.Code != http.StatusOK {
			t.Fatalf("expected 200 for undelete, got %d: %s", undelRes.Code, undelRes.Body.String())
		}
		var undelData struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(undelRes.Body.Bytes(), &undelData)
		if undelData.Message != "Account unmarked for deletion" {
			t.Errorf("expected 'Account unmarked for deletion', got %s", undelData.Message)
		}
	})

	// 4. Top-up Options
	t.Run("4_TopupOptions", func(t *testing.T) {
		res := makeReq(http.MethodGet, entBase+"/topup_options", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on topup_options, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			ID       uint   `json:"id"`
			Currency string `json:"currency"`
			Options  []struct {
				Credits int     `json:"credits"`
				Amount  float64 `json:"amount"`
			} `json:"options"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if len(data.Options) == 0 {
			t.Fatalf("expected topup options to be populated")
		}
		if data.Options[0].Credits <= 0 || data.Options[0].Amount <= 0 {
			t.Errorf("invalid option values: %+v", data.Options[0])
		}

		// Also check v1 route
		resV1 := makeReq(http.MethodGet, v1Base+"/topup_options", nil)
		if resV1.Code != http.StatusOK {
			t.Fatalf("expected 200 on v1 topup_options, got %d: %s", resV1.Code, resV1.Body.String())
		}
	})

	// 5. Top-up Checkout
	t.Run("5_TopupCheckout", func(t *testing.T) {
		// Test validation on zero credits
		badRes := makeReq(http.MethodPost, entBase+"/topup_checkout", map[string]any{
			"credits": 0,
		})
		if badRes.Code != http.StatusUnprocessableEntity {
			t.Errorf("expected 422 on zero credits, got %d", badRes.Code)
		}

		// Test valid topup checkout
		res := makeReq(http.MethodPost, entBase+"/topup_checkout", map[string]any{
			"credits": 1000,
		})
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on topup_checkout, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			Credits     int     `json:"credits"`
			Amount      float64 `json:"amount"`
			Currency    string  `json:"currency"`
			RedirectURL string  `json:"redirect_url"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if data.Credits != 1000 {
			t.Errorf("expected credits 1000, got %d", data.Credits)
		}
		if data.Amount <= 0 {
			t.Errorf("expected positive amount, got %f", data.Amount)
		}
		if !strings.Contains(data.RedirectURL, "https://checkout.stripe.com/pay/") {
			t.Errorf("expected stripe topup redirect URL, got %s", data.RedirectURL)
		}

		// Also check v1 route
		resV1 := makeReq(http.MethodPost, v1Base+"/topup_checkout", map[string]any{
			"credits": 5000,
		})
		if resV1.Code != http.StatusOK {
			t.Fatalf("expected 200 on v1 topup_checkout, got %d: %s", resV1.Code, resV1.Body.String())
		}
	})
}
