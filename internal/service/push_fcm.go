package service

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

const (
	fcmOAuthEndpoint  = "https://oauth2.googleapis.com/token"
	fcmMessagingScope = "https://www.googleapis.com/auth/firebase.messaging"
	fcmRequestTimeout = 4 * time.Second
	fcmResponseLimit  = 64 << 10
)

var (
	firebaseProjectPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`)
	firebaseEmailPattern      = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+\.gserviceaccount\.com$`)
	firebaseNameSuffixPattern = regexp.MustCompile(`^[A-Za-z0-9%:._~-]+$`)
	errFirebaseConfig         = errors.New("Firebase configuration is unavailable")
	errFirebaseAuth           = errors.New("Firebase authorization is unavailable")
)

// PushOutcomePersistenceError means the observed send outcome could not be
// durably recorded. It is never permission to retry. Outcome is credential-free;
// accepted is provider acceptance only, never proof of device delivery.
type PushOutcomePersistenceError struct{ Outcome string }

func (e *PushOutcomePersistenceError) Error() string {
	return "native push outcome persistence failed; automatic resend is unsafe"
}

type fcmResult struct {
	Status, Category, ProviderMessageID, RetryAfter string
	HTTPStatus                                      int
	Unregistered                                    bool
}

type nativeFCM struct {
	project, email, keyID string
	key                   *rsa.PrivateKey
	mu                    sync.Mutex
	token                 string
	refreshAt             time.Time
	refreshing            chan struct{}
	refreshErr            error
	now                   func() time.Time
}

func (s *PushService) selectNativeFCM(ctx context.Context) (*nativeFCM, bool, error) {
	if s.firebaseConfig == nil {
		return nil, false, nil
	}
	project, credentials, err := s.firebaseConfig.FirebasePushConfiguration(ctx)
	if err != nil {
		return nil, true, errFirebaseConfig
	}
	if project == "" && credentials == "" {
		return nil, false, nil
	}
	if project == "" || credentials == "" {
		return nil, true, errFirebaseConfig
	}
	fingerprint := sha256.Sum256([]byte(project + "\x00" + credentials))
	s.nativeMu.Lock()
	defer s.nativeMu.Unlock()
	if s.native != nil && s.nativeFingerprint == fingerprint {
		return s.native, true, nil
	}
	native, err := newNativeFCM(project, credentials)
	if err != nil {
		return nil, true, errFirebaseConfig
	}
	s.native, s.nativeFingerprint = native, fingerprint
	return native, true, nil
}

func newNativeFCM(project, credentials string) (*nativeFCM, error) {
	if !firebaseProjectPattern.MatchString(project) || len(credentials) > 32768 {
		return nil, errFirebaseConfig
	}
	var c struct {
		Type         string `json:"type"`
		ClientEmail  string `json:"client_email"`
		PrivateKey   string `json:"private_key"`
		PrivateKeyID string `json:"private_key_id"`
		TokenURI     string `json:"token_uri"`
	}
	if json.Unmarshal([]byte(credentials), &c) != nil || c.Type != "service_account" ||
		!firebaseEmailPattern.MatchString(c.ClientEmail) || len(c.ClientEmail) > 254 ||
		(c.TokenURI != "" && c.TokenURI != fcmOAuthEndpoint) || len(c.PrivateKeyID) > 128 ||
		strings.ContainsAny(c.PrivateKeyID, "\r\n\x00") {
		return nil, errFirebaseConfig
	}
	block, rest := pem.Decode([]byte(c.PrivateKey))
	if block == nil || len(bytes.TrimSpace(rest)) != 0 || block.Type != "PRIVATE KEY" {
		return nil, errFirebaseConfig
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errFirebaseConfig
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 || key.N.BitLen() > 8192 || key.Validate() != nil {
		return nil, errFirebaseConfig
	}
	return &nativeFCM{project: project, email: c.ClientEmail, keyID: c.PrivateKeyID, key: key, now: time.Now}, nil
}

// Only the HTTP transport is inherited for test injection. Cookie jars,
// redirects and unbounded client timeouts cannot change credential destinations.
func fcmClient(injected *http.Client) *http.Client {
	client := &http.Client{Timeout: fcmRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if injected != nil {
		client.Transport = injected.Transport
		if injected.Timeout > 0 && injected.Timeout < client.Timeout {
			client.Timeout = injected.Timeout
		}
	}
	return client
}

func boundedFCMResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, fcmResponseLimit+1))
	if err != nil || len(body) > fcmResponseLimit {
		return nil, errors.New("provider response unavailable")
	}
	return body, nil
}

func (n *nativeFCM) accessToken(ctx context.Context, client *http.Client) (string, error) {
	n.mu.Lock()
	if n.token != "" && n.now().Before(n.refreshAt) {
		token := n.token
		n.mu.Unlock()
		return token, nil
	}
	if waiting := n.refreshing; waiting != nil {
		n.mu.Unlock()
		select {
		case <-ctx.Done():
			return "", errFirebaseAuth
		case <-waiting:
		}
		n.mu.Lock()
		defer n.mu.Unlock()
		if n.refreshErr != nil || n.token == "" || !n.now().Before(n.refreshAt) {
			return "", errFirebaseAuth
		}
		return n.token, nil
	}
	waiting := make(chan struct{})
	n.refreshing = waiting
	n.mu.Unlock()
	token, refreshAt, err := n.exchangeToken(ctx, client)
	if err == nil && !n.now().Before(refreshAt) {
		token, refreshAt, err = "", time.Time{}, errFirebaseAuth
	}
	n.mu.Lock()
	n.token, n.refreshAt, n.refreshErr = token, refreshAt, err
	n.refreshing = nil
	close(waiting)
	n.mu.Unlock()
	return token, err
}

func (n *nativeFCM) exchangeToken(ctx context.Context, client *http.Client) (string, time.Time, error) {
	now := n.now()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": n.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": n.email, "scope": fcmMessagingScope, "aud": fcmOAuthEndpoint, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix()})
	assertion := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(assertion))
	signature, err := rsa.SignPKCS1v15(rand.Reader, n.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", time.Time{}, errFirebaseAuth
	}
	assertion += "." + base64.RawURLEncoding.EncodeToString(signature)
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fcmOAuthEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, errFirebaseAuth
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", time.Time{}, errFirebaseAuth
	}
	body, err := boundedFCMResponse(resp)
	if err != nil || resp.StatusCode != http.StatusOK {
		return "", time.Time{}, errFirebaseAuth
	}
	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if json.Unmarshal(body, &result) != nil || !strings.EqualFold(result.TokenType, "Bearer") || result.ExpiresIn < 1 || result.ExpiresIn > 3600 || len(result.AccessToken) == 0 || len(result.AccessToken) > 8192 {
		return "", time.Time{}, errFirebaseAuth
	}
	for _, ch := range result.AccessToken {
		if ch <= 32 || ch >= 127 {
			return "", time.Time{}, errFirebaseAuth
		}
	}
	lifetime := time.Duration(result.ExpiresIn) * time.Second
	margin := lifetime / 10
	if margin > time.Minute {
		margin = time.Minute
	}
	// Start expiry at request construction, conservatively accounting for exchange latency.
	return result.AccessToken, now.Add(lifetime - margin), nil
}

func (n *nativeFCM) send(ctx context.Context, injected *http.Client, token string, payload PushPayload) fcmResult {
	client := fcmClient(injected)
	accessToken, err := n.accessToken(ctx, client)
	if err != nil {
		return fcmResult{Status: "rejected", Category: "authorization_unavailable"}
	}
	// Preserve existing authorized business content. The two fixed delivery
	// options are documented by product/API 28 section 13; neither introduces
	// private context, actor data, topic broadcasts or recipient resolution.
	envelope := map[string]any{"message": map[string]any{
		"token":        token,
		"notification": map[string]string{"title": payload.Title, "body": payload.Body},
		"android":      map[string]string{"priority": "high"},
		"apns":         map[string]any{"payload": map[string]any{"aps": map[string]string{"sound": "default"}}},
		"data":         map[string]string{"account_id": strconv.FormatUint(uint64(payload.AccountID), 10), "resource_id": strconv.FormatUint(uint64(payload.ResourceID), 10), "resource_type": payload.ResourceType, "deep_link": payload.DeepLink},
	}}
	body, _ := json.Marshal(envelope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://fcm.googleapis.com/v1/projects/"+n.project+"/messages:send", bytes.NewReader(body))
	if err != nil {
		return fcmResult{Status: "rejected", Category: "request_invalid"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return fcmResult{Status: "unknown", Category: "response_unavailable"}
	}
	result := fcmResult{HTTPStatus: resp.StatusCode, RetryAfter: safeFCMRetryAfter(resp.Header.Get("Retry-After"))}
	raw, readErr := boundedFCMResponse(resp)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var accepted struct {
			Name string `json:"name"`
		}
		prefix := "projects/" + n.project + "/messages/"
		if readErr != nil || json.Unmarshal(raw, &accepted) != nil || !strings.HasPrefix(accepted.Name, prefix) || len(accepted.Name) > 512 || !firebaseNameSuffixPattern.MatchString(strings.TrimPrefix(accepted.Name, prefix)) {
			result.Status, result.Category = "unknown", "acceptance_unconfirmed"
		} else {
			result.Status, result.ProviderMessageID = "accepted", accepted.Name
		}
		return result
	}
	if resp.StatusCode >= 500 || resp.StatusCode < 300 {
		result.Status, result.Category = "unknown", "provider_unavailable"
		return result
	}
	result.Status, result.Category = "rejected", "provider_rejected"
	if resp.StatusCode == 429 {
		result.Category = "provider_throttled"
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		result.Category = "redirect_rejected"
	}
	if resp.StatusCode == http.StatusNotFound && readErr == nil {
		var rejected struct {
			Error struct {
				Code    int    `json:"code"`
				Status  string `json:"status"`
				Details []struct {
					Type      string `json:"@type"`
					ErrorCode string `json:"errorCode"`
				} `json:"details"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &rejected) == nil && rejected.Error.Code == 404 && rejected.Error.Status == "NOT_FOUND" {
			for _, detail := range rejected.Error.Details {
				if detail.Type == "type.googleapis.com/google.firebase.fcm.v1.FcmError" && detail.ErrorCode == "UNREGISTERED" {
					result.Unregistered = true
					result.Category = "token_unregistered"
				}
			}
		}
	}
	return result
}

func safeFCMRetryAfter(value string) string {
	if seconds, err := strconv.ParseUint(value, 10, 32); err == nil {
		return strconv.FormatUint(seconds, 10)
	}
	if when, err := http.ParseTime(value); err == nil {
		return when.UTC().Format(http.TimeFormat)
	}
	return ""
}

func (s *PushService) recordNativeFCM(ctx context.Context, sub domain.NotificationSubscription, payload PushPayload, result fcmResult) error {
	if s.db == nil {
		return &PushOutcomePersistenceError{Outcome: result.Status}
	}
	entry := domain.PushDeliveryLog{AccountID: sub.AccountID, UserID: sub.UserID, DeviceToken: sub.PushToken, Platform: sub.SubscriptionType, Title: payload.Title, Body: payload.Body, Status: result.Status, HTTPStatusCode: result.HTTPStatus, ErrorDetail: result.Category, ProviderMessageID: result.ProviderMessageID, RetryAfter: result.RetryAfter, CreatedAt: time.Now().UTC()}
	// Do not log SQL bindings containing the device token or payload on failure.
	if err := s.db.WithContext(ctx).Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).Create(&entry).Error; err != nil {
		// Existing producers dispatch asynchronously and may discard the return
		// value; keep the uncertainty observable without SQL or payload details.
		logger.WithComponent("push").Warn("native push outcome persistence failed; automatic resend is unsafe", "user_id", sub.UserID, "account_id", sub.AccountID, "observed_status", result.Status)
		return &PushOutcomePersistenceError{Outcome: result.Status}
	}
	if result.Unregistered {
		if err := s.deviceRepo.DeleteAttemptedSubscription(ctx, sub); err != nil {
			logger.WithComponent("push").Warn("native push invalid subscription cleanup failed", "user_id", sub.UserID, "account_id", sub.AccountID)
			return errors.New("native push invalid subscription cleanup failed; automatic resend is unsafe")
		}
	}
	logger.WithComponent("push").Info("native push provider outcome", "user_id", sub.UserID, "account_id", sub.AccountID, "status", result.Status, "http_code", result.HTTPStatus, "category", result.Category)
	return nil
}
