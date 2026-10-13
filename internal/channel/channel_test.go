package channel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testConfig() Config {
	return Config{PublicBaseURL: "https://chat.example.test", EncryptionKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))), WhatsAppAppID: "1234", WhatsAppAppSecret: strings.Repeat("s", 32), WhatsAppVerifyToken: strings.Repeat("v", 32), WhatsAppAPIVersion: "v25.0"}
}

func TestCredentialEncryptionScopeAndRedaction(t *testing.T) {
	cfg := testConfig()
	key, _ := cfg.Key()
	secret := "provider-secret-value"
	a, err := Seal(key, "scope-a", secret)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal(key, "scope-a", secret)
	if err != nil || a == b || strings.Contains(a, secret) {
		t.Fatal("missing randomized encryption")
	}
	got, err := Open(key, "scope-a", a)
	if err != nil || got != secret {
		t.Fatal(got, err)
	}
	if _, err := Open(key, "scope-b", a); err == nil {
		t.Fatal("ciphertext crossed tenant scope")
	}
	if _, err := Open(key, "scope-a", a[:len(a)-2]+"xx"); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}

func TestChannelConfigFailsClosed(t *testing.T) {
	for _, cfg := range []Config{{}, {PublicBaseURL: "https://chat.example.test"}, {PublicBaseURL: "http://chat.example.test", EncryptionKey: testConfig().EncryptionKey}, {PublicBaseURL: "https://user:secret@chat.example.test", EncryptionKey: testConfig().EncryptionKey}, {PublicBaseURL: "https://chat.example.test/?token=x", EncryptionKey: testConfig().EncryptionKey}} {
		if cfg.Validate("twilio") == nil {
			t.Fatal("invalid config accepted")
		}
	}
	cfg := testConfig()
	if err := cfg.Validate("whatsapp"); err != nil {
		t.Fatal(err)
	}
	cfg.WhatsAppAPIVersion = ""
	if cfg.Validate("whatsapp") == nil {
		t.Fatal("missing pinned version accepted")
	}
}

func TestWhatsAppSignatureRawBytes(t *testing.T) {
	raw := []byte(`{"message":"hello"}`)
	secret := strings.Repeat("s", 32)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	header := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifyWhatsAppSignature(secret, header, raw) {
		t.Fatal("valid signature rejected")
	}
	for _, header := range []string{"", header[1:], "sha1=" + header[7:], header + "0"} {
		if VerifyWhatsAppSignature(secret, header, raw) {
			t.Fatal("invalid signature accepted")
		}
	}
	if VerifyWhatsAppSignature(secret, header, append(raw, ' ')) {
		t.Fatal("altered raw body accepted")
	}
}

type localTransport struct{ target string }

func (l localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	u := *req.URL
	cloned.URL = &u
	cloned.URL.Scheme = "http"
	cloned.URL.Host = strings.TrimPrefix(l.target, "http://")
	return http.DefaultTransport.RoundTrip(cloned)
}

func TestProviderHTTPProtocolsAndSanitizedFailures(t *testing.T) {
	cfg := testConfig()
	const sid = "AC11111111111111111111111111111111"
	token := strings.Repeat("t", 32)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "oauth/access_token"):
			if r.Method != "GET" || r.URL.Query().Get("client_id") != cfg.WhatsAppAppID || r.URL.Query().Get("client_secret") != cfg.WhatsAppAppSecret || r.URL.Query().Get("code") != "one-use-code" {
				t.Error("bad exchange protocol")
			}
			io.WriteString(w, `{"access_token":"mock-access-token","token_type":"bearer"}`)
		case strings.HasSuffix(r.URL.Path, "/789/phone_numbers"):
			if r.Header.Get("Authorization") != "Bearer mock-access-token" || r.URL.Query().Get("fields") != "id" {
				t.Error("missing ownership read token")
			}
			io.WriteString(w, `{"data":[{"id":"456"}]}`)
		case strings.HasSuffix(r.URL.Path, "IncomingPhoneNumbers.json"):
			user, pass, _ := r.BasicAuth()
			if user != sid || pass != token || r.URL.Query().Get("PhoneNumber") != "+14155550123" {
				t.Error("bad Twilio protocol")
			}
			io.WriteString(w, `{"incoming_phone_numbers":[{"account_sid":"`+sid+`","phone_number":"+14155550123","capabilities":{"sms":true}}]}`)
		default:
			t.Error("unexpected provider request")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	provider := NewHTTPProvider(&http.Client{Transport: localTransport{server.URL}})
	got, err := provider.ExchangeWhatsApp(context.Background(), cfg, "one-use-code", "789", "456")
	if err != nil || got != "mock-access-token" {
		t.Fatal(got, err)
	}
	if err := provider.VerifyTwilio(context.Background(), sid, token, "+14155550123"); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	if _, err := provider.ExchangeWhatsApp(context.Background(), cfg, "one-use-code", "789", "999"); !errors.Is(err, ErrUnauthorized) {
		t.Fatal(err)
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"LEAK-secret-token"}`)
	}))
	defer failing.Close()
	provider = NewHTTPProvider(&http.Client{Transport: localTransport{failing.URL}})
	if err := provider.VerifyTwilio(context.Background(), sid, token, "+14155550123"); err != ErrProvider || strings.Contains(err.Error(), "LEAK") {
		t.Fatal(err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, server.URL, 302) }))
	defer redirect.Close()
	provider = NewHTTPProvider(&http.Client{Transport: localTransport{redirect.URL}})
	prior := calls
	if err := provider.VerifyTwilio(context.Background(), sid, token, "+14155550123"); err != ErrProvider {
		t.Fatal(err)
	}
	if calls != prior {
		t.Fatal("followed credential-bearing redirect")
	}
}

func TestTwilioRejectsDuplicateValuesAndUnsupportedMedia(t *testing.T) {
	raw := `AccountSid=AC11111111111111111111111111111111&MessageSid=SM22222222222222222222222222222222&From=%2B14155550999&To=%2B14155550123&Body=hello&NumMedia=0&FutureField=kept`
	event, params, err := ParseTwilio([]byte(raw))
	if err != nil || event.Text != "hello" || params["FutureField"] != "kept" {
		t.Fatal(event, err)
	}
	if _, _, err := ParseTwilio([]byte(raw + "&Body=evil")); err != ErrInvalid {
		t.Fatal(err)
	}
	if _, _, err := ParseTwilio([]byte(strings.Replace(raw, "NumMedia=0", "NumMedia=1", 1))); err != ErrUnsupported {
		t.Fatal(err)
	}
}
