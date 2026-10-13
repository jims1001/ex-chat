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
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type fcmRoundTrip func(*http.Request) (*http.Response, error)

func (f fcmRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fcmResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

var syntheticFCM struct {
	sync.Once
	credentials string
	key         *rsa.PrivateKey
	err         error
}

func syntheticFCMCredentials(t *testing.T) string {
	t.Helper()
	syntheticFCM.Do(func() {
		syntheticFCM.key, syntheticFCM.err = rsa.GenerateKey(rand.Reader, 2048)
		if syntheticFCM.err != nil {
			return
		}
		raw, err := x509.MarshalPKCS8PrivateKey(syntheticFCM.key)
		if err != nil {
			syntheticFCM.err = err
			return
		}
		data, err := json.Marshal(map[string]string{"type": "service_account", "project_id": "sender-project", "client_email": "fixture@sender-project.iam.gserviceaccount.com", "private_key_id": "synthetic-key-id", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: raw})), "token_uri": fcmOAuthEndpoint})
		syntheticFCM.err = err
		syntheticFCM.credentials = string(data)
	})
	require.NoError(t, syntheticFCM.err)
	return syntheticFCM.credentials
}

func TestNativeFCMJWTCacheAndProtocol(t *testing.T) {
	native, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
	require.NoError(t, err)
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	native.now = func() time.Time { return now }
	var tokens, sends atomic.Int32
	payload := PushPayload{Title: "authorized title", Body: "authorized body", AccountID: 91, ResourceID: 7, ResourceType: "Conversation", DeepLink: "app://accounts/91/conversations/7", NotificationType: "never-add-extra"}
	client := &http.Client{Transport: fcmRoundTrip(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "https", req.URL.Scheme)
		raw, err := io.ReadAll(req.Body)
		require.NoError(t, err)
		if req.URL.String() == fcmOAuthEndpoint {
			tokens.Add(1)
			require.Empty(t, req.Header.Get("Authorization"))
			require.Equal(t, "application/x-www-form-urlencoded", req.Header.Get("Content-Type"))
			form, err := url.ParseQuery(string(raw))
			require.NoError(t, err)
			require.Equal(t, "urn:ietf:params:oauth:grant-type:jwt-bearer", form.Get("grant_type"))
			require.Len(t, form, 2)
			parts := strings.Split(form.Get("assertion"), ".")
			require.Len(t, parts, 3)
			header, err := base64.RawURLEncoding.DecodeString(parts[0])
			require.NoError(t, err)
			require.JSONEq(t, `{"alg":"RS256","typ":"JWT","kid":"synthetic-key-id"}`, string(header))
			claims, err := base64.RawURLEncoding.DecodeString(parts[1])
			require.NoError(t, err)
			var c map[string]any
			require.NoError(t, json.Unmarshal(claims, &c))
			require.Len(t, c, 5)
			require.Equal(t, fcmOAuthEndpoint, c["aud"])
			require.Equal(t, fcmMessagingScope, c["scope"])
			require.Equal(t, "fixture@sender-project.iam.gserviceaccount.com", c["iss"])
			require.Equal(t, float64(now.Unix()), c["iat"])
			require.Equal(t, float64(now.Add(time.Hour).Unix()), c["exp"])
			sig, err := base64.RawURLEncoding.DecodeString(parts[2])
			require.NoError(t, err)
			digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
			require.NoError(t, rsa.VerifyPKCS1v15(&syntheticFCM.key.PublicKey, crypto.SHA256, digest[:], sig))
			return fcmResponse(200, `{"access_token":"synthetic-oauth-token","token_type":"Bearer","expires_in":120}`), nil
		}
		sends.Add(1)
		require.Equal(t, "https://fcm.googleapis.com/v1/projects/target-project/messages:send", req.URL.String())
		require.Equal(t, "Bearer synthetic-oauth-token", req.Header.Get("Authorization"))
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		require.JSONEq(t, `{"message":{"token":"synthetic-device","notification":{"title":"authorized title","body":"authorized body"},"android":{"priority":"high"},"apns":{"payload":{"aps":{"sound":"default"}}},"data":{"account_id":"91","resource_id":"7","resource_type":"Conversation","deep_link":"app://accounts/91/conversations/7"}}}`, string(raw))
		return fcmResponse(200, `{"name":"projects/target-project/messages/mock:message-1"}`), nil
	})}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := native.send(context.Background(), client, "synthetic-device", payload)
			require.Equal(t, "accepted", result.Status)
			require.Equal(t, "projects/target-project/messages/mock:message-1", result.ProviderMessageID)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, tokens.Load())
	require.EqualValues(t, 24, sends.Load())
	now = now.Add(109 * time.Second)
	require.Equal(t, "accepted", native.send(context.Background(), client, "synthetic-device", payload).Status)
	require.EqualValues(t, 2, tokens.Load())
	require.EqualValues(t, 25, sends.Load())
}

func TestNativeFCMResponseOutcomes(t *testing.T) {
	cases := []struct {
		name                   string
		code                   int
		body, status, category string
		invalid                bool
	}{
		{"accepted", 200, `{"name":"projects/target-project/messages/one"}`, "accepted", "", false},
		{"empty success", 200, `{}`, "unknown", "acceptance_unconfirmed", false},
		{"foreign success", 200, `{"name":"projects/foreign-project/messages/one"}`, "unknown", "acceptance_unconfirmed", false},
		{"truncated success", 200, `{"name":`, "unknown", "acceptance_unconfirmed", false},
		{"oversized success", 200, strings.Repeat("x", fcmResponseLimit+1), "unknown", "acceptance_unconfirmed", false},
		{"unregistered", 404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`, "rejected", "token_unregistered", true},
		{"generic404", 404, `{"error":{"code":404,"status":"NOT_FOUND"}}`, "rejected", "provider_rejected", false},
		{"spoofed unregister", 404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"other","errorCode":"UNREGISTERED"}]}}`, "rejected", "provider_rejected", false},
		{"ambiguous400", 400, `{"error":{"code":400,"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"}]}}`, "rejected", "provider_rejected", false},
		{"forbidden", 403, `{"error":{"message":"private-provider-message"}}`, "rejected", "provider_rejected", false},
		{"unauthorized", 401, `secret`, "rejected", "provider_rejected", false},
		{"quota", 429, `secret`, "rejected", "provider_throttled", false},
		{"server", 500, `secret`, "unknown", "provider_unavailable", false},
		{"unavailable", 503, `secret`, "unknown", "provider_unavailable", false},
		{"redirect", 307, `secret`, "rejected", "redirect_rejected", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			native, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
			require.NoError(t, err)
			calls := 0
			client := &http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() == fcmOAuthEndpoint {
					return fcmResponse(200, `{"access_token":"synthetic","token_type":"Bearer","expires_in":3600}`), nil
				}
				resp := fcmResponse(tc.code, tc.body)
				resp.Header.Set("Location", "https://never-follow.invalid/secret")
				resp.Header.Set("Retry-After", "120")
				return resp, nil
			})}
			result := native.send(context.Background(), client, "synthetic-device", PushPayload{})
			require.Equal(t, tc.status, result.Status)
			require.Equal(t, tc.category, result.Category)
			require.Equal(t, tc.invalid, result.Unregistered)
			require.Equal(t, "120", result.RetryAfter)
			require.Equal(t, 2, calls)
		})
	}
	for _, kind := range []string{"lost response", "timeout", "read failure"} {
		t.Run(kind, func(t *testing.T) {
			native, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
			require.NoError(t, err)
			calls := 0
			client := &http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() == fcmOAuthEndpoint {
					return fcmResponse(200, `{"access_token":"synthetic","token_type":"Bearer","expires_in":3600}`), nil
				}
				if kind == "read failure" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: fcmBrokenBody{}}, nil
				}
				return nil, errors.New("synthetic-device private-key Authorization secret")
			})}
			result := native.send(context.Background(), client, "synthetic-device", PushPayload{})
			require.Equal(t, "unknown", result.Status)
			require.NotContains(t, result.Category, "secret")
			require.False(t, result.Unregistered)
			require.Equal(t, 2, calls)
		})
	}
}

type fcmBrokenBody struct{}

func (fcmBrokenBody) Read([]byte) (int, error) { return 0, errors.New("private-body-error") }
func (fcmBrokenBody) Close() error             { return nil }

func TestNativeFCMAuthFailuresAndExpiry(t *testing.T) {
	cases := []struct {
		name string
		code int
		body string
	}{
		{"rejected", 401, `private credential diagnostic`}, {"redirect", 302, `{}`}, {"malformed", 200, `{`}, {"missing token", 200, `{"token_type":"Bearer","expires_in":3600}`}, {"wrong type", 200, `{"access_token":"s","token_type":"Basic","expires_in":3600}`}, {"long expiry", 200, `{"access_token":"s","token_type":"Bearer","expires_in":3601}`}, {"zero expiry", 200, `{"access_token":"s","token_type":"Bearer","expires_in":0}`}, {"header injection", 200, `{"access_token":"s\r\nX:bad","token_type":"Bearer","expires_in":3600}`}, {"oversized", 200, strings.Repeat("x", fcmResponseLimit+1)}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
			require.NoError(t, err)
			calls := 0
			client := &http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, fcmOAuthEndpoint, r.URL.String())
				resp := fcmResponse(tc.code, tc.body)
				resp.Header.Set("Location", "https://never-follow.invalid")
				return resp, nil
			})}
			result := n.send(context.Background(), client, "synthetic-device", PushPayload{})
			require.Equal(t, "rejected", result.Status)
			require.Equal(t, "authorization_unavailable", result.Category)
			require.Equal(t, 1, calls)
		})
	}
	t.Run("expired during exchange", func(t *testing.T) {
		n, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
		require.NoError(t, err)
		now := time.Now()
		n.now = func() time.Time { return now }
		calls := 0
		client := &http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			now = now.Add(2 * time.Second)
			return fcmResponse(200, `{"access_token":"s","token_type":"Bearer","expires_in":1}`), nil
		})}
		result := n.send(context.Background(), client, "device", PushPayload{})
		require.Equal(t, "authorization_unavailable", result.Category)
		require.Equal(t, 1, calls)
	})
	t.Run("cancelled waiter", func(t *testing.T) {
		n, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
		require.NoError(t, err)
		n.refreshing = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = n.accessToken(ctx, fcmClient(nil))
		require.ErrorIs(t, err, errFirebaseAuth)
	})
	require.Equal(t, fcmRequestTimeout, fcmClient(&http.Client{}).Timeout)
	require.Equal(t, time.Millisecond, fcmClient(&http.Client{Timeout: time.Millisecond}).Timeout)
}

func TestNativeFCMCredentialValidation(t *testing.T) {
	good := syntheticFCMCredentials(t)
	for _, raw := range []string{"", `{}`, `[]`, `null`, `{`, strings.Replace(good, "service_account", "authorized_user", 1), strings.Replace(good, fcmOAuthEndpoint, "https://attacker.invalid/token", 1), strings.Replace(good, "PRIVATE KEY", "RSA PRIVATE KEY", 1), strings.Repeat("x", 32769)} {
		_, err := newNativeFCM("target-project", raw)
		require.Error(t, err)
		require.EqualError(t, err, "Firebase configuration is unavailable")
	}
	for _, project := range []string{"", "../evil", "a/b", "https://x", "target-project?alt=json"} {
		_, err := newNativeFCM(project, good)
		require.Error(t, err)
	}
	_, err := newNativeFCM("target-project", strings.Replace(good, "fixture@sender-project.iam.gserviceaccount.com", "123456-compute@developer.gserviceaccount.com", 1))
	require.NoError(t, err)
}

func TestNativeFCMOwnerStores(t *testing.T) {
	runNativeFCMOwnerStores(t, func(t *testing.T, owner string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		pool, err := db.DB()
		require.NoError(t, err)
		pool.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		return db
	})
}
func TestPostgresNativeFCMOwnerStores(t *testing.T) {
	runNativeFCMOwnerStores(t, reportWindowTestPostgres(t))
}

func runNativeFCMOwnerStores(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	type fixture struct {
		s                     *PushService
		ntf, sys              *gorm.DB
		sub                   domain.NotificationSubscription
		gateway, oauth, sends int
		send                  func() (*http.Response, error)
	}
	setup := func(t *testing.T) *fixture {
		f := &fixture{ntf: open(t, "NTF"), sys: open(t, "SYS")}
		require.NoError(t, database.AutoMigrateModule(f.ntf, "NTF"))
		require.NoError(t, database.AutoMigrateModule(f.sys, "SYS"))
		f.sub = domain.NotificationSubscription{AccountID: 91, UserID: 7, SubscriptionType: "fcm", PushToken: "synthetic-device"}
		require.NoError(t, f.ntf.Create(&f.sub).Error)
		f.s = NewPushService(f.ntf, repository.NewDeviceRepository(f.ntf))
		f.s.SetFirebaseConfigOwner(repository.NewSystemConfigRepository(f.sys))
		f.send = func() (*http.Response, error) {
			return fcmResponse(200, `{"name":"projects/target-project/messages/accepted-id"}`), nil
		}
		f.s.SetHTTPClient(&http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Host {
			case "oauth2.googleapis.com":
				f.oauth++
				return fcmResponse(200, `{"access_token":"synthetic","token_type":"Bearer","expires_in":3600}`), nil
			case "fcm.googleapis.com":
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var envelope map[string]any
				require.NoError(t, json.Unmarshal(raw, &envelope))
				message := envelope["message"].(map[string]any)
				require.Equal(t, map[string]any{"priority": "high"}, message["android"])
				require.Equal(t, map[string]any{"payload": map[string]any{"aps": map[string]any{"sound": "default"}}}, message["apns"])
				require.NotContains(t, message, "fcm_options", "production must not invent diagnostic analytics labels")
				f.sends++
				return f.send()
			default:
				raw, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				var envelope map[string]any
				require.NoError(t, json.Unmarshal(raw, &envelope))
				message := envelope["message"].(map[string]any)
				require.NotContains(t, message, "android")
				require.NotContains(t, message, "apns")
				require.NotContains(t, message, "fcm_options")
				f.gateway++
				return fcmResponse(200, `{}`), nil
			}
		})})
		require.False(t, f.ntf.Migrator().HasTable(&domain.SystemConfig{}))
		require.False(t, f.sys.Migrator().HasTable(&domain.NotificationSubscription{}))
		return f
	}
	configure := func(t *testing.T, f *fixture) {
		repo := repository.NewSystemConfigRepository(f.sys)
		require.NoError(t, repo.Set("FIREBASE_PROJECT_ID", "target-project"))
		require.NoError(t, repo.Set("FIREBASE_CREDENTIALS", syntheticFCMCredentials(t)))
	}
	payload := PushPayload{AccountID: 91, ResourceID: 7, ResourceType: "Conversation", Title: "authorized", Body: "allowed"}
	dispatch := func(t *testing.T, f *fixture) (int, error) { return f.s.Dispatch(context.Background(), 7, 91, payload) }
	t.Setenv("FCM_GATEWAY_URL", "https://compatibility.invalid/push")
	t.Run("direct selection and acceptance", func(t *testing.T) {
		f := setup(t)
		configure(t, f)
		count, err := dispatch(t, f)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.Equal(t, 1, f.sends)
		require.Zero(t, f.gateway)
		var log domain.PushDeliveryLog
		require.NoError(t, f.ntf.First(&log).Error)
		require.Equal(t, "accepted", log.Status)
		require.Equal(t, "projects/target-project/messages/accepted-id", log.ProviderMessageID)
		require.Equal(t, 91, int(log.AccountID))
		require.Equal(t, 7, int(log.UserID))
		require.Empty(t, log.ErrorDetail)
		count, err = dispatch(t, f)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Equal(t, 1, f.sends)
	})
	t.Run("authorization failure has no fallback", func(t *testing.T) {
		f := setup(t)
		configure(t, f)
		f.s.SetHTTPClient(&http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
			f.oauth++
			require.Equal(t, fcmOAuthEndpoint, r.URL.String())
			return fcmResponse(401, "private-oauth-rejection"), nil
		})})
		count, err := dispatch(t, f)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Equal(t, 1, f.oauth)
		require.Zero(t, f.sends)
		require.Zero(t, f.gateway)
		var log domain.PushDeliveryLog
		require.NoError(t, f.ntf.First(&log).Error)
		require.Equal(t, "authorization_unavailable", log.ErrorDetail)
	})
	t.Run("absent pair gateway compatibility", func(t *testing.T) {
		f := setup(t)
		count, err := dispatch(t, f)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.Equal(t, 1, f.gateway)
		require.Zero(t, f.oauth)
		var log domain.PushDeliveryLog
		require.NoError(t, f.ntf.First(&log).Error)
		require.Equal(t, "delivered", log.Status)
	})
	for _, kind := range []string{"partial project", "partial credentials", "malformed", "unavailable store"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			repo := repository.NewSystemConfigRepository(f.sys)
			switch kind {
			case "partial project":
				require.NoError(t, repo.Set("FIREBASE_PROJECT_ID", "target-project"))
			case "partial credentials":
				require.NoError(t, repo.Set("FIREBASE_CREDENTIALS", syntheticFCMCredentials(t)))
			case "malformed":
				configure(t, f)
				require.NoError(t, repo.Set("FIREBASE_CREDENTIALS", "private-malformed-credential"))
			case "unavailable store":
				require.NoError(t, f.sys.Migrator().DropTable(&domain.SystemConfig{}))
			}
			count, err := dispatch(t, f)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Zero(t, f.oauth)
			require.Zero(t, f.sends)
			require.Zero(t, f.gateway)
			var log domain.PushDeliveryLog
			require.NoError(t, f.ntf.First(&log).Error)
			require.Equal(t, "rejected", log.Status)
			require.Equal(t, "configuration_unavailable", log.ErrorDetail)
		})
	}
	for _, kind := range []string{"unregistered", "replaced token", "refreshed row", "recreated identity", "ambiguous400", "429", "503", "lost response"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			configure(t, f)
			other := domain.NotificationSubscription{AccountID: 92, UserID: 7, SubscriptionType: "fcm", PushToken: f.sub.PushToken}
			require.NoError(t, f.ntf.Create(&other).Error)
			otherUser := domain.NotificationSubscription{AccountID: 91, UserID: 8, SubscriptionType: "fcm", PushToken: f.sub.PushToken}
			require.NoError(t, f.ntf.Create(&otherUser).Error)
			f.send = func() (*http.Response, error) {
				switch kind {
				case "replaced token":
					require.NoError(t, f.ntf.Model(&domain.NotificationSubscription{}).Where("id = ?", f.sub.ID).Update("push_token", "replacement-device").Error)
				case "refreshed row":
					require.NoError(t, f.ntf.Model(&domain.NotificationSubscription{}).Where("id = ?", f.sub.ID).Update("updated_at", f.sub.UpdatedAt.Add(time.Second)).Error)
				case "recreated identity":
					require.NoError(t, f.ntf.Delete(&domain.NotificationSubscription{}, f.sub.ID).Error)
					replacement := domain.NotificationSubscription{AccountID: 91, UserID: 7, SubscriptionType: "fcm", PushToken: f.sub.PushToken}
					require.NoError(t, f.ntf.Create(&replacement).Error)
					require.NotEqual(t, f.sub.ID, replacement.ID)
				case "ambiguous400":
					return fcmResponse(400, `{"error":{"code":400,"status":"INVALID_ARGUMENT"}}`), nil
				case "429":
					resp := fcmResponse(429, `{}`)
					resp.Header.Set("Retry-After", "31")
					return resp, nil
				case "503":
					return fcmResponse(503, `private`), nil
				case "lost response":
					return nil, errors.New("private-lost-response")
				}
				return fcmResponse(404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`), nil
			}
			count, err := dispatch(t, f)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Equal(t, 1, f.sends)
			require.Zero(t, f.gateway)
			var remaining int64
			require.NoError(t, f.ntf.Model(&domain.NotificationSubscription{}).Count(&remaining).Error)
			if kind == "unregistered" {
				require.EqualValues(t, 2, remaining)
			} else {
				require.EqualValues(t, 3, remaining)
			}
			var log domain.PushDeliveryLog
			require.NoError(t, f.ntf.First(&log).Error)
			if kind == "503" || kind == "lost response" {
				require.Equal(t, "unknown", log.Status)
			} else {
				require.Equal(t, "rejected", log.Status)
			}
			if kind == "429" {
				require.Equal(t, "31", log.RetryAfter)
			}
			require.NotContains(t, log.ErrorDetail, "private")
		})
	}
	t.Run("persistence failure stops device iteration", func(t *testing.T) {
		f := setup(t)
		configure(t, f)
		next := domain.NotificationSubscription{AccountID: 91, UserID: 7, SubscriptionType: "fcm", PushToken: "second-device"}
		require.NoError(t, f.ntf.Create(&next).Error)
		f.send = func() (*http.Response, error) {
			require.NoError(t, f.ntf.Migrator().DropTable(&domain.PushDeliveryLog{}))
			return fcmResponse(200, `{"name":"projects/target-project/messages/accepted-id"}`), nil
		}
		count, err := dispatch(t, f)
		var persistence *PushOutcomePersistenceError
		require.ErrorAs(t, err, &persistence)
		require.Equal(t, "accepted", persistence.Outcome)
		require.Equal(t, 1, count)
		require.Equal(t, 1, f.sends)
		require.Zero(t, f.gateway)
		count, err = dispatch(t, f)
		require.NoError(t, err)
		require.Zero(t, count)
		require.Equal(t, 1, f.sends)
	})
	t.Run("unregistered persistence failure never deletes", func(t *testing.T) {
		f := setup(t)
		configure(t, f)
		f.send = func() (*http.Response, error) {
			require.NoError(t, f.ntf.Migrator().DropTable(&domain.PushDeliveryLog{}))
			return fcmResponse(404, `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`), nil
		}
		_, err := dispatch(t, f)
		require.Error(t, err)
		var count int64
		require.NoError(t, f.ntf.Model(&domain.NotificationSubscription{}).Count(&count).Error)
		require.EqualValues(t, 1, count)
		require.Equal(t, 1, f.sends)
	})
	for _, kind := range []string{"muted", "quiet hours", "flag disabled"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			configure(t, f)
			setting := domain.NotificationSetting{AccountID: 91, UserID: 7, SelectedPushFlags: "[]"}
			switch kind {
			case "muted":
				setting.Muted = true
			case "quiet hours":
				setting.QuietHoursEnabled = true
				setting.QuietHoursStart = time.Now().UTC().Add(-time.Minute).Format("15:04")
				setting.QuietHoursEnd = time.Now().UTC().Add(time.Minute).Format("15:04")
			}
			require.NoError(t, f.ntf.Create(&setting).Error)
			request := payload
			request.NotificationType = "conversation_creation"
			count, err := f.s.Dispatch(context.Background(), 7, 91, request)
			require.NoError(t, err)
			require.Zero(t, count)
			require.Zero(t, f.oauth)
			require.Zero(t, f.sends)
			require.Zero(t, f.gateway)
		})
	}
	t.Run("configured direct treats URL tokens as data", func(t *testing.T) {
		f := setup(t)
		configure(t, f)
		require.NoError(t, f.ntf.Model(&domain.NotificationSubscription{}).Where("id = ?", f.sub.ID).Update("push_token", "https://never-contact.invalid/device").Error)
		count, err := dispatch(t, f)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.Equal(t, 1, f.sends)
		require.Zero(t, f.gateway)
	})
	t.Run("non FCM stays on compatibility path", func(t *testing.T) {
		f := setup(t)
		configure(t, f)
		require.NoError(t, f.ntf.Model(&domain.NotificationSubscription{}).Where("id = ?", f.sub.ID).Update("subscription_type", "browser").Error)
		count, err := dispatch(t, f)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.Zero(t, f.oauth)
		require.Zero(t, f.sends)
		require.Equal(t, 1, f.gateway)
	})
}

func TestNativeFCMRealTimeoutAndRefreshWaiters(t *testing.T) {
	t.Run("send timeout", func(t *testing.T) {
		n, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
		require.NoError(t, err)
		calls := 0
		client := &http.Client{Timeout: 10 * time.Millisecond, Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.String() == fcmOAuthEndpoint {
				return fcmResponse(200, `{"access_token":"synthetic","token_type":"Bearer","expires_in":3600}`), nil
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		result := n.send(context.Background(), client, "device", PushPayload{})
		require.Equal(t, "unknown", result.Status)
		require.Equal(t, "response_unavailable", result.Category)
		require.Equal(t, 2, calls)
	})
	t.Run("cancelled waiters do not refresh", func(t *testing.T) {
		n, err := newNativeFCM("target-project", syntheticFCMCredentials(t))
		require.NoError(t, err)
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		client := fcmClient(&http.Client{Transport: fcmRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			close(entered)
			<-release
			return fcmResponse(401, `secret`), nil
		})})
		go func() {
			defer close(done)
			_, err := n.accessToken(context.Background(), client)
			require.Error(t, err)
		}()
		<-entered
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
				defer cancel()
				_, err := n.accessToken(ctx, client)
				require.Error(t, err)
			}()
		}
		wg.Wait()
		require.EqualValues(t, 1, calls.Load())
		close(release)
		<-done
		require.Empty(t, n.token)
	})
}

func TestNativeFCMSensitiveSQLSuppressed(t *testing.T) {
	var captured bytes.Buffer
	sensitiveLogger := logger.New(log.New(&captured, "", 0), logger.Config{LogLevel: logger.Info})
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "audit.db")), &gorm.Config{Logger: sensitiveLogger})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, db.AutoMigrate(&domain.PushDeliveryLog{}, &domain.NotificationSubscription{}))
	service := NewPushService(db, repository.NewDeviceRepository(db))
	sub := domain.NotificationSubscription{ID: 1, AccountID: 91, UserID: 7, SubscriptionType: "fcm", PushToken: "sentinel-device-token", UpdatedAt: time.Now().UTC()}
	payload := PushPayload{Title: "sentinel-private-title", Body: "sentinel-private-body"}
	require.NoError(t, db.Migrator().DropTable(&domain.NotificationSubscription{}))
	captured.Reset()
	err = service.recordNativeFCM(context.Background(), sub, payload, fcmResult{Status: "rejected", Category: "token_unregistered", HTTPStatus: 404, Unregistered: true})
	require.EqualError(t, err, "native push invalid subscription cleanup failed; automatic resend is unsafe")
	require.Empty(t, captured.String(), "native audit and failed exact-token cleanup must override verbose SQL logger")
	require.NoError(t, db.Migrator().DropTable(&domain.PushDeliveryLog{}))
	captured.Reset()
	err = service.recordNativeFCM(context.Background(), sub, payload, fcmResult{Status: "accepted", ProviderMessageID: "projects/target-project/messages/synthetic"})
	var persistence *PushOutcomePersistenceError
	require.ErrorAs(t, err, &persistence)
	require.Empty(t, captured.String(), "failed native audit must not log payload or token")
}

func TestNativeFCMRetryAfterNormalization(t *testing.T) {
	require.Equal(t, "31", safeFCMRetryAfter("00031"))
	require.Equal(t, "Thu, 08 Oct 2026 05:00:00 GMT", safeFCMRetryAfter("Thu, 08 Oct 2026 05:00:00 GMT"))
	for _, value := range []string{"private-provider-body", "-1", "12\r\nAuthorization: private", strings.Repeat("9", 100)} {
		require.Empty(t, safeFCMRetryAfter(value))
	}
}

type fcmConfigFunc func(context.Context) (string, string, error)

func (f fcmConfigFunc) FirebasePushConfiguration(ctx context.Context) (string, string, error) {
	return f(ctx)
}
func TestNativeFCMConfigurationRotation(t *testing.T) {
	credentials := syntheticFCMCredentials(t)
	service := NewPushService(nil, nil)
	var configErr error
	project := "target-project"
	service.SetFirebaseConfigOwner(fcmConfigFunc(func(context.Context) (string, string, error) { return project, credentials, configErr }))
	first, selected, err := service.selectNativeFCM(context.Background())
	require.NoError(t, err)
	require.True(t, selected)
	same, selected, err := service.selectNativeFCM(context.Background())
	require.NoError(t, err)
	require.True(t, selected)
	require.Same(t, first, same)
	credentials = strings.Replace(credentials, "synthetic-key-id", "rotated-synthetic-key", 1)
	rotated, selected, err := service.selectNativeFCM(context.Background())
	require.NoError(t, err)
	require.True(t, selected)
	require.NotSame(t, first, rotated)
	require.Empty(t, rotated.token)
	configErr = errors.New("private-store-details")
	blocked, selected, err := service.selectNativeFCM(context.Background())
	require.EqualError(t, err, "Firebase configuration is unavailable")
	require.True(t, selected)
	require.Nil(t, blocked)
	configErr = nil
	project = ""
	credentials = ""
	absent, selected, err := service.selectNativeFCM(context.Background())
	require.NoError(t, err)
	require.False(t, selected)
	require.Nil(t, absent)
}
