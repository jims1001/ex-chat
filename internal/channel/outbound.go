package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	DispositionAccepted = "accepted"
	DispositionRejected = "rejected"
	DispositionUnknown  = "unknown"
)

// TextRequest contains a single persisted send attempt. Credential and Body are
// intentionally never included in a Result, diagnostic, or provider error.
// RecipientID is a WhatsApp digit-only wa_id or a Twilio E.164 SMS number.
type TextRequest struct {
	AttemptID         string
	Provider          string
	ProviderAccountID string
	SenderID          string
	RecipientID       string
	Body              string
	CallbackURL       string
	APIVersion        string
	Credential        string
}

// Result distinguishes definite acceptance, definite non-acceptance, and an
// ambiguous outcome. Unknown must never be interpreted as safe to resend.
type Result struct {
	ProviderID  string
	Status      string
	Disposition string
}

type TextTransport interface {
	SendText(context.Context, TextRequest) Result
	LookupText(context.Context, TextRequest, string) Result
}

type HTTPTextTransport struct{ client *http.Client }

// NewHTTPTextTransport copies the client, bounds the entire HTTP exchange and
// forbids redirects so credentials cannot leave the fixed provider origin.
// Inject a RoundTripper for tests; provider URLs are never configurable.
func NewHTTPTextTransport(client *http.Client) *HTTPTextTransport {
	if client == nil {
		client = &http.Client{}
	}
	safe := *client
	safe.Timeout = 15 * time.Second
	safe.Jar = nil
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPTextTransport{client: &safe}
}

func unknownTextResult() Result {
	return Result{Status: "unknown", Disposition: DispositionUnknown}
}

func rejectedTextResult() Result {
	return Result{Status: "failed", Disposition: DispositionRejected}
}

func validAttemptID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func validTextIdentity(r TextRequest) bool {
	if len(r.Credential) < 16 || len(r.Credential) > 16384 || strings.ContainsAny(r.Credential, "\r\n") {
		return false
	}
	switch r.Provider {
	case "whatsapp":
		return numericID.MatchString(r.ProviderAccountID) && numericID.MatchString(r.SenderID) && numericID.MatchString(r.RecipientID) && apiVersion.MatchString(r.APIVersion)
	case "twilio":
		return accountSID.MatchString(r.ProviderAccountID) && phoneNumber.MatchString(r.SenderID) && phoneNumber.MatchString(r.RecipientID) && len(r.Credential) <= 256
	default:
		return false
	}
}

func validTextSend(r TextRequest) bool {
	if !validTextIdentity(r) || !validAttemptID(r.AttemptID) || len(r.Body) == 0 || len(r.Body) > 16000 || !utf8.ValidString(r.Body) {
		return false
	}
	if r.Provider == "whatsapp" {
		return utf8.RuneCountInString(r.Body) <= 4096
	}
	callback, err := url.Parse(r.CallbackURL)
	return utf8.RuneCountInString(r.Body) <= 1600 && len(r.CallbackURL) <= 4096 && err == nil && callback.Scheme == "https" && callback.Hostname() != "" && callback.User == nil && callback.RawQuery == "" && !callback.ForceQuery && callback.Fragment == "" && !strings.Contains(callback.Hostname(), "_") && strings.TrimSpace(r.CallbackURL) == r.CallbackURL
}

func (p *HTTPTextTransport) SendText(ctx context.Context, r TextRequest) Result {
	if !validTextSend(r) {
		return rejectedTextResult()
	}
	var req *http.Request
	var err error
	switch r.Provider {
	case "whatsapp":
		body, _ := json.Marshal(struct {
			Product       string `json:"messaging_product"`
			RecipientType string `json:"recipient_type"`
			To            string `json:"to"`
			Type          string `json:"type"`
			Text          struct {
				Body       string `json:"body"`
				PreviewURL bool   `json:"preview_url"`
			} `json:"text"`
			AttemptID string `json:"biz_opaque_callback_data"`
		}{Product: "whatsapp", RecipientType: "individual", To: r.RecipientID, Type: "text", Text: struct {
			Body       string `json:"body"`
			PreviewURL bool   `json:"preview_url"`
		}{Body: r.Body, PreviewURL: false}, AttemptID: r.AttemptID})
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, "https://graph.facebook.com/"+r.APIVersion+"/"+r.SenderID+"/messages", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+r.Credential)
		}
	case "twilio":
		body := url.Values{"To": {r.RecipientID}, "From": {r.SenderID}, "Body": {r.Body}, "StatusCallback": {r.CallbackURL}}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, "https://api.twilio.com/2010-04-01/Accounts/"+r.ProviderAccountID+"/Messages.json", strings.NewReader(body.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.SetBasicAuth(r.ProviderAccountID, r.Credential)
		}
	}
	if err != nil || req == nil {
		return rejectedTextResult()
	}
	// Do not advertise replayability. In particular, an Idempotency-Key header
	// is not supported by these sends and can trigger net/http POST retries.
	req.GetBody = nil
	req.Header.Del("Idempotency-Key")
	req.Header.Del("X-Idempotency-Key")
	return p.executeText(req, r, "", false)
}

// LookupText only reads an existing Twilio SMS resource. Meta has no supported
// message-status lookup here; absence of a receipt is not proof of failure.
func (p *HTTPTextTransport) LookupText(ctx context.Context, r TextRequest, providerID string) Result {
	if r.Provider != "twilio" || !validTextIdentity(r) || !validSMSID(providerID) {
		return unknownTextResult()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.twilio.com/2010-04-01/Accounts/"+r.ProviderAccountID+"/Messages/"+providerID+".json", nil)
	if err != nil {
		return unknownTextResult()
	}
	req.SetBasicAuth(r.ProviderAccountID, r.Credential)
	return p.executeText(req, r, providerID, true)
}

func (p *HTTPTextTransport) executeText(req *http.Request, r TextRequest, expectedID string, lookup bool) Result {
	req.Header.Set("Accept", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return unknownTextResult()
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return unknownTextResult()
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// A rejected lookup says nothing about the original send. For sends,
		// only a documented non-acceptance error is safe to classify rejected.
		if !lookup && definiteTextRejection(r.Provider, res.StatusCode, raw) {
			return rejectedTextResult()
		}
		return unknownTextResult()
	}
	if r.Provider == "whatsapp" {
		return parseWhatsAppSendResult(raw, r)
	}
	return parseTwilioTextResult(raw, r, expectedID)
}

// The narrow Twilio allowlist is documented at /docs/api/errors/{code}.
// Malformed envelopes, unknown codes, mismatched statuses, transient failures,
// and all Meta errors remain ambiguous. Meta rejection retryability is not
// enabled without verified documentation for its error semantics.
func definiteTextRejection(provider string, httpStatus int, raw []byte) bool {
	if provider != "twilio" {
		return false
	}
	var failure struct {
		Code    int    `json:"code"`
		Status  int    `json:"status"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &failure) != nil || failure.Status != httpStatus || failure.Message == "" {
		return false
	}
	switch httpStatus {
	case http.StatusBadRequest:
		switch failure.Code {
		case 20003, 21211, 21606, 21608:
			return true
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		return failure.Code == 20003
	case http.StatusTooManyRequests:
		return failure.Code == 20429
	}
	return false
}

func parseWhatsAppSendResult(raw []byte, r TextRequest) Result {
	var result struct {
		Product  string `json:"messaging_product"`
		Contacts []struct {
			Input string `json:"input"`
			WaID  string `json:"wa_id"`
		} `json:"contacts"`
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Product != "whatsapp" || len(result.Contacts) != 1 || len(result.Messages) != 1 || (len(result.Error) > 0 && string(result.Error) != "null") {
		return unknownTextResult()
	}
	if result.Contacts[0].Input != r.RecipientID || result.Contacts[0].WaID != r.RecipientID || !validWhatsAppMessageID(result.Messages[0].ID) {
		return unknownTextResult()
	}
	return Result{ProviderID: result.Messages[0].ID, Status: "provider_accepted", Disposition: DispositionAccepted}
}

func parseTwilioTextResult(raw []byte, r TextRequest, expectedID string) Result {
	var result struct {
		AccountSID string `json:"account_sid"`
		SID        string `json:"sid"`
		From       string `json:"from"`
		To         string `json:"to"`
		Direction  string `json:"direction"`
		NumMedia   string `json:"num_media"`
		Status     string `json:"status"`
	}
	if json.Unmarshal(raw, &result) != nil || result.AccountSID != r.ProviderAccountID || !validSMSID(result.SID) || (expectedID != "" && result.SID != expectedID) || result.From != r.SenderID || result.To != r.RecipientID || result.Direction != "outbound-api" || result.NumMedia != "0" {
		return unknownTextResult()
	}
	status, ok := normalizeTextStatus("twilio", result.Status)
	if !ok {
		return unknownTextResult()
	}
	return Result{ProviderID: result.SID, Status: status, Disposition: DispositionAccepted}
}

func validSMSID(id string) bool {
	return strings.HasPrefix(id, "SM") && messageSID.MatchString(id)
}

func validWhatsAppMessageID(id string) bool {
	if !strings.HasPrefix(id, "wamid.") || len(id) <= len("wamid.") || len(id) > 255 {
		return false
	}
	for _, c := range id {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

func normalizeTextStatus(provider, status string) (string, bool) {
	switch status {
	case "accepted", "queued", "sending":
		return "provider_accepted", provider == "twilio"
	case "sent", "delivered":
		return status, provider == "twilio" || provider == "whatsapp"
	case "read":
		return status, provider == "whatsapp"
	case "failed":
		return "failed", provider == "twilio" || provider == "whatsapp"
	case "undelivered":
		return "failed", provider == "twilio"
	default:
		return "", false
	}
}
