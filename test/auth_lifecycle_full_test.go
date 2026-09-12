package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAuthLifecycleFull(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_jwt_secret_lifecycle_32bytes!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	makeReq := func(token, method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		rq := httptest.NewRequest(method, path, reqBody)
		if token != "" {
			rq.Header.Set("Authorization", "Bearer "+token)
		}
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, rq)
		return rec
	}

	// ----------------------------------------------------
	// 1. User Sign Up
	// ----------------------------------------------------
	userEmail := "lifecycle_agent@example.com"
	userPassword := "Password123!"

	signUpRes := makeReq("", http.MethodPost, "/auth/sign_up", map[string]string{
		"email":    userEmail,
		"password": userPassword,
		"name":     "Lifecycle Agent",
	})
	if signUpRes.Code != http.StatusCreated && signUpRes.Code != http.StatusOK {
		t.Fatalf("sign up failed with status %d: %s", signUpRes.Code, signUpRes.Body.String())
	}

	var signUpData struct {
		Data struct {
			Token string      `json:"token"`
			User  domain.User `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(signUpRes.Body.Bytes(), &signUpData)
	token := signUpData.Data.Token
	userID := signUpData.Data.User.ID
	if token == "" || userID == 0 {
		t.Fatalf("empty token or user ID in signup response: %s", signUpRes.Body.String())
	}

	// ----------------------------------------------------
	// 2. Validate Token (GET /auth/validate_token)
	// ----------------------------------------------------
	t.Run("Validate Token Success", func(t *testing.T) {
		valRes := makeReq(token, http.MethodGet, "/auth/validate_token", nil)
		if valRes.Code != http.StatusOK {
			t.Fatalf("expected 200 for validate_token, got %d: %s", valRes.Code, valRes.Body.String())
		}
		var valData struct {
			Data struct {
				User domain.User `json:"user"`
			} `json:"data"`
		}
		_ = json.Unmarshal(valRes.Body.Bytes(), &valData)
		if valData.Data.User.Email != userEmail {
			t.Errorf("expected email %s, got %s", userEmail, valData.Data.User.Email)
		}
	})

	// ----------------------------------------------------
	// 3. Email Confirmation & Resend Confirmation
	// ----------------------------------------------------
	t.Run("Email Confirmation and Resend", func(t *testing.T) {
		// Verify user initially unconfirmed
		var userInDB domain.User
		if err := db.First(&userInDB, userID).Error; err != nil {
			t.Fatalf("failed to fetch user: %v", err)
		}
		if userInDB.ConfirmedAt != nil {
			t.Errorf("expected new user ConfirmedAt to be nil")
		}
		firstToken := userInDB.ConfirmationToken
		if firstToken == "" {
			t.Errorf("expected confirmation token to be generated upon signup")
		}

		// Request resend confirmation
		resendRes := makeReq(token, http.MethodPost, "/api/v1/profile/resend_confirmation", nil)
		if resendRes.Code != http.StatusOK {
			t.Fatalf("resend confirmation failed with %d: %s", resendRes.Code, resendRes.Body.String())
		}

		// Verify confirmation token refreshed or valid
		if err := db.First(&userInDB, userID).Error; err != nil {
			t.Fatalf("failed to reload user: %v", err)
		}
		currentToken := userInDB.ConfirmationToken
		if currentToken == "" {
			t.Fatalf("confirmation token is empty after resend")
		}

		// Perform confirmation via GET /auth/confirmation
		confirmRes := makeReq("", http.MethodGet, "/auth/confirmation?confirmation_token="+currentToken, nil)
		if confirmRes.Code != http.StatusOK {
			t.Fatalf("confirm email via GET failed with %d: %s", confirmRes.Code, confirmRes.Body.String())
		}

		// Verify user is now confirmed
		if err := db.First(&userInDB, userID).Error; err != nil {
			t.Fatalf("failed to reload user: %v", err)
		}
		if userInDB.ConfirmedAt == nil {
			t.Errorf("expected ConfirmedAt to be populated after confirmation")
		}
	})

	// ----------------------------------------------------
	// 4. Avatar Upload and Delete
	// ----------------------------------------------------
	t.Run("Profile Avatar Upload and Delete", func(t *testing.T) {
		// Set avatar URL
		avatarURL := "https://example.com/avatars/lifecycle_agent.png"
		uploadRes := makeReq(token, http.MethodPost, "/api/v1/profile/avatar", map[string]string{
			"avatar_url": avatarURL,
		})
		if uploadRes.Code != http.StatusOK {
			t.Fatalf("upload avatar failed with %d: %s", uploadRes.Code, uploadRes.Body.String())
		}

		// Check Profile reflects avatar
		profileRes := makeReq(token, http.MethodGet, "/api/v1/profile", nil)
		if profileRes.Code != http.StatusOK {
			t.Fatalf("get profile failed with %d: %s", profileRes.Code, profileRes.Body.String())
		}
		var profData struct {
			Data struct {
				User domain.User `json:"user"`
			} `json:"data"`
		}
		_ = json.Unmarshal(profileRes.Body.Bytes(), &profData)
		if profData.Data.User.AvatarURL != avatarURL {
			t.Errorf("expected avatar_url %s, got %s", avatarURL, profData.Data.User.AvatarURL)
		}

		// Delete avatar
		delRes := makeReq(token, http.MethodDelete, "/api/v1/profile/avatar", nil)
		if delRes.Code != http.StatusOK {
			t.Fatalf("delete avatar failed with %d: %s", delRes.Code, delRes.Body.String())
		}

		// Check profile avatar is cleared
		profileRes2 := makeReq(token, http.MethodGet, "/api/v1/profile", nil)
		var profData2 struct {
			Data struct {
				User domain.User `json:"user"`
			} `json:"data"`
		}
		_ = json.Unmarshal(profileRes2.Body.Bytes(), &profData2)
		if profData2.Data.User.AvatarURL != "" {
			t.Errorf("expected empty avatar_url after delete, got %s", profData2.Data.User.AvatarURL)
		}
	})

	// ----------------------------------------------------
	// 5. MFA Activation, Backup Codes & Two-Step Sign In
	// ----------------------------------------------------
	t.Run("MFA Setup, Activation, Backup Codes and Sign In Challenge", func(t *testing.T) {
		// 1. Get MFA profile setup info
		mfaInitRes := makeReq(token, http.MethodGet, "/api/v1/profile/mfa", nil)
		if mfaInitRes.Code != http.StatusOK {
			t.Fatalf("get mfa profile failed with %d: %s", mfaInitRes.Code, mfaInitRes.Body.String())
		}

		var mfaInitData struct {
			Data struct {
				Secret      string `json:"secret"`
				Enabled     bool   `json:"enabled"`
				BackupCodes string `json:"backup_codes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(mfaInitRes.Body.Bytes(), &mfaInitData)
		if mfaInitData.Data.Secret == "" {
			t.Fatalf("mfa secret is empty")
		}
		if mfaInitData.Data.Enabled {
			t.Fatalf("mfa should not be enabled initially")
		}

		// 2. Generate valid TOTP code and activate MFA
		totpCode, err := auth.GenerateTOTPCode(mfaInitData.Data.Secret, time.Now())
		if err != nil {
			t.Fatalf("failed to generate TOTP code: %v", err)
		}

		verifyRes := makeReq(token, http.MethodPost, "/api/v1/profile/mfa/verify", map[string]string{
			"otp_code": totpCode,
		})
		if verifyRes.Code != http.StatusOK {
			t.Fatalf("mfa activation verify failed with %d: %s", verifyRes.Code, verifyRes.Body.String())
		}
		var verifyData struct {
			Data struct {
				Enabled     bool     `json:"enabled"`
				BackupCodes []string `json:"backup_codes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(verifyRes.Body.Bytes(), &verifyData)
		if !verifyData.Data.Enabled {
			t.Errorf("expected mfa to be enabled after verification")
		}
		if len(verifyData.Data.BackupCodes) != 8 {
			t.Errorf("expected 8 initial backup codes, got %d", len(verifyData.Data.BackupCodes))
		}

		// 3. Test Generate New Backup Codes (POST /api/v1/profile/mfa/backup_codes)
		newCodesRes := makeReq(token, http.MethodPost, "/api/v1/profile/mfa/backup_codes", nil)
		if newCodesRes.Code != http.StatusOK {
			t.Fatalf("generate backup codes failed with %d: %s", newCodesRes.Code, newCodesRes.Body.String())
		}
		var newCodesData struct {
			Data struct {
				BackupCodes []string `json:"backup_codes"`
			} `json:"data"`
		}
		_ = json.Unmarshal(newCodesRes.Body.Bytes(), &newCodesData)
		if len(newCodesData.Data.BackupCodes) != 8 {
			t.Fatalf("expected 8 refreshed backup codes, got %d", len(newCodesData.Data.BackupCodes))
		}
		testBackupCode := newCodesData.Data.BackupCodes[0]

		// 4. Test Sign In with MFA enabled:
		// Attempt A: Sign in with password only -> should return 206 Partial Content + mfa_token
		step1Res := makeReq("", http.MethodPost, "/auth/sign_in", map[string]string{
			"email":    userEmail,
			"password": userPassword,
		})
		if step1Res.Code != http.StatusPartialContent {
			t.Fatalf("expected 206 Partial Content when MFA is required, got %d: %s", step1Res.Code, step1Res.Body.String())
		}
		var step1Data struct {
			Data struct {
				MFARequired bool   `json:"mfa_required"`
				MFAToken    string `json:"mfa_token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(step1Res.Body.Bytes(), &step1Data)
		if !step1Data.Data.MFARequired || step1Data.Data.MFAToken == "" {
			t.Fatalf("expected mfa_token in 206 response, got: %s", step1Res.Body.String())
		}

		// Attempt B: Complete login with mfa_token + backup code via POST /auth/mfa/verify
		step2Res := makeReq("", http.MethodPost, "/auth/mfa/verify", map[string]string{
			"mfa_token":   step1Data.Data.MFAToken,
			"backup_code": testBackupCode,
		})
		if step2Res.Code != http.StatusOK {
			t.Fatalf("mfa login verification with backup code failed with %d: %s", step2Res.Code, step2Res.Body.String())
		}
		var step2Data struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(step2Res.Body.Bytes(), &step2Data)
		if step2Data.Data.Token == "" {
			t.Fatalf("expected JWT token after completing MFA verification")
		}

		// Attempt C: Ensure the same backup code CANNOT be reused (single-use)
		step3Res := makeReq("", http.MethodPost, "/auth/sign_in", map[string]string{
			"email":       userEmail,
			"password":    userPassword,
			"backup_code": testBackupCode,
		})
		if step3Res.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when reusing consumed backup code, got %d: %s", step3Res.Code, step3Res.Body.String())
		}

		// Attempt D: Direct sign in with fresh TOTP code in one shot
		freshTOTP, _ := auth.GenerateTOTPCode(mfaInitData.Data.Secret, time.Now())
		oneShotRes := makeReq("", http.MethodPost, "/auth/sign_in", map[string]string{
			"email":    userEmail,
			"password": userPassword,
			"otp_code": freshTOTP,
		})
		if oneShotRes.Code != http.StatusOK {
			t.Fatalf("direct sign in with otp_code failed with %d: %s", oneShotRes.Code, oneShotRes.Body.String())
		}
	})

	// ----------------------------------------------------
	// 6. Sign Out and Token Revocation Enforcement
	// ----------------------------------------------------
	t.Run("Sign Out and Token Revocation Interception", func(t *testing.T) {
		// First verify token is valid before sign out
		beforeRes := makeReq(token, http.MethodGet, "/api/v1/profile", nil)
		if beforeRes.Code != http.StatusOK {
			t.Fatalf("expected 200 before sign out, got %d", beforeRes.Code)
		}

		// Call sign out via DELETE /auth/sign_out
		signOutRes := makeReq(token, http.MethodDelete, "/auth/sign_out", nil)
		if signOutRes.Code != http.StatusOK {
			t.Fatalf("sign out failed with %d: %s", signOutRes.Code, signOutRes.Body.String())
		}

		// Verify subsequent requests with the same token are rejected with 401 Unauthorized
		afterRes := makeReq(token, http.MethodGet, "/api/v1/profile", nil)
		if afterRes.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized after token revocation, got %d: %s", afterRes.Code, afterRes.Body.String())
		}

		// Also verify /auth/validate_token rejects the revoked token
		valRes := makeReq(token, http.MethodGet, "/auth/validate_token", nil)
		if valRes.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 Unauthorized for validate_token on revoked token, got %d: %s", valRes.Code, valRes.Body.String())
		}

		// Also test /api/v1/profile/sign_out on a newly generated token
		// Sign in to get a fresh token
		var mfaProfile domain.MFAProfile
		_ = db.Where("user_id = ?", userID).First(&mfaProfile)
		freshTOTP, _ := auth.GenerateTOTPCode(mfaProfile.Secret, time.Now())
		signInRes := makeReq("", http.MethodPost, "/auth/sign_in", map[string]string{
			"email":    userEmail,
			"password": userPassword,
			"otp_code": freshTOTP,
		})
		if signInRes.Code != http.StatusOK {
			t.Fatalf("sign in failed: %s", signInRes.Body.String())
		}
		var signData struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(signInRes.Body.Bytes(), &signData)
		freshToken := signData.Data.Token

		// Check fresh token works
		okRes := makeReq(freshToken, http.MethodGet, "/api/v1/profile", nil)
		if okRes.Code != http.StatusOK {
			t.Fatalf("fresh token should work, got %d", okRes.Code)
		}

		// Revoke via DELETE /api/v1/profile/sign_out
		delSignOutRes := makeReq(freshToken, http.MethodDelete, "/api/v1/profile/sign_out", nil)
		if delSignOutRes.Code != http.StatusOK {
			t.Fatalf("profile sign out failed with %d: %s", delSignOutRes.Code, delSignOutRes.Body.String())
		}

		// Check revoked
		blockedRes := makeReq(freshToken, http.MethodGet, "/api/v1/profile", nil)
		if blockedRes.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 after profile sign out, got %d", blockedRes.Code)
		}
	})
}
