package channel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	outboundAttempt = "aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa"
	outboundAccount = "AC11111111111111111111111111111111"
	outboundSID     = "SM22222222222222222222222222222222"
)

type outboundRoundTripper func(*http.Request) (*http.Response, error)

func (f outboundRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func outboundRequest(provider string) TextRequest {
	r := TextRequest{AttemptID: outboundAttempt, Provider: provider, ProviderAccountID: outboundAccount, SenderID: "+14155550123", RecipientID: "+14155550999", Body: "hello & welcome 🌍", CallbackURL: "https://chat.example.test/api/twilio/status/" + outboundAttempt, Credential: strings.Repeat("s", 32)}
	if provider == "whatsapp" {
		r.ProviderAccountID, r.SenderID, r.RecipientID, r.APIVersion = "12345", "67890", "14155550999", "v25.0"
	}
	return r
}

func outboundResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func twilioOutboundJSON(status string) string {
	return `{"account_sid":"` + outboundAccount + `","sid":"` + outboundSID + `","from":"+14155550123","to":"+14155550999","direction":"outbound-api","num_media":"0","status":"` + status + `"}`
}

const whatsappOutboundJSON = `{"messaging_product":"whatsapp","contacts":[{"input":"14155550999","wa_id":"14155550999"}],"messages":[{"id":"wamid.example123="}]}`

func TestTextTransportSendWhatsAppProtocol(t *testing.T) {
	r := outboundRequest("whatsapp")
	calls := 0
	transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.GetBody != nil || req.Header.Get("Idempotency-Key") != "" || req.Header.Get("X-Idempotency-Key") != "" {
			t.Error("POST must not advertise replayability")
		}
		if req.Method != http.MethodPost || req.URL.String() != "https://graph.facebook.com/v25.0/67890/messages" || req.Header.Get("Authorization") != "Bearer "+r.Credential || req.Header.Get("Content-Type") != "application/json" {
			t.Error("incorrect WhatsApp endpoint, method or authentication")
		}
		if deadline, ok := req.Context().Deadline(); !ok || time.Until(deadline) > 15*time.Second {
			t.Error("request lacks bounded deadline")
		}
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body) != 6 || body["messaging_product"] != "whatsapp" || body["recipient_type"] != "individual" || body["type"] != "text" || body["to"] != r.RecipientID || body["biz_opaque_callback_data"] != r.AttemptID {
			t.Error("incorrect text-only payload identity")
		}
		text, ok := body["text"].(map[string]any)
		if !ok || len(text) != 2 || text["body"] != r.Body || text["preview_url"] != false {
			t.Error("incorrect text payload")
		}
		return outboundResponse(200, whatsappOutboundJSON), nil
	})})
	result := transport.SendText(context.Background(), r)
	if result != (Result{ProviderID: "wamid.example123=", Status: "provider_accepted", Disposition: DispositionAccepted}) || calls != 1 {
		t.Fatal(result, calls)
	}
}

func TestTextTransportSendTwilioProtocol(t *testing.T) {
	r := outboundRequest("twilio")
	calls := 0
	transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.GetBody != nil || req.Header.Get("Idempotency-Key") != "" || req.Header.Get("X-Idempotency-Key") != "" {
			t.Error("POST must not advertise replayability")
		}
		if req.Method != http.MethodPost || req.URL.String() != "https://api.twilio.com/2010-04-01/Accounts/"+r.ProviderAccountID+"/Messages.json" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Error("incorrect Twilio endpoint or method")
		}
		user, pass, ok := req.BasicAuth()
		if !ok || user != r.ProviderAccountID || pass != r.Credential {
			t.Error("incorrect Twilio authentication")
		}
		raw, _ := io.ReadAll(req.Body)
		form, err := url.ParseQuery(string(raw))
		if err != nil || len(form) != 4 || form.Get("To") != r.RecipientID || form.Get("From") != r.SenderID || form.Get("Body") != r.Body || form.Get("StatusCallback") != r.CallbackURL {
			t.Error("incorrect SMS-only payload")
		}
		return outboundResponse(201, twilioOutboundJSON("queued")), nil
	})})
	result := transport.SendText(context.Background(), r)
	if result != (Result{ProviderID: outboundSID, Status: "provider_accepted", Disposition: DispositionAccepted}) || calls != 1 {
		t.Fatal(result, calls)
	}
}

func TestTextTransportOutcomeClassificationAndNoRetries(t *testing.T) {
	for _, provider := range []string{"whatsapp", "twilio"} {
		for _, code := range []int{300, 301, 302, 307, 308, 400, 401, 403, 404, 408, 409, 413, 422, 429, 500, 502, 503, 504} {
			t.Run(provider+"/"+http.StatusText(code), func(t *testing.T) {
				calls := 0
				transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(req *http.Request) (*http.Response, error) {
					calls++
					res := outboundResponse(code, `{"code":20429,"error":"secret-token private-message"}`)
					res.Header.Set("Location", "https://attacker.example/steal")
					return res, nil
				})})
				got := transport.SendText(context.Background(), outboundRequest(provider))
				want := unknownTextResult()
				if got != want || calls != 1 {
					t.Fatal(got, calls)
				}
			})
		}
	}
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled, errors.New("https://secret-token@provider.example private-message")} {
		calls := 0
		transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(*http.Request) (*http.Response, error) {
			calls++
			return nil, cause
		})})
		if got := transport.SendText(context.Background(), outboundRequest("twilio")); got != unknownTextResult() || calls != 1 {
			t.Fatal(got, calls)
		}
	}
}

func TestTextTransportRejectsUnsafeRequestsBeforeIO(t *testing.T) {
	mutations := []func(*TextRequest){
		func(r *TextRequest) { r.Provider = "custom" },
		func(r *TextRequest) { r.ProviderAccountID = "../other" },
		func(r *TextRequest) { r.SenderID = "../other" },
		func(r *TextRequest) { r.RecipientID = "whatsapp:+14155550999" },
		func(r *TextRequest) { r.AttemptID = "not-a-uuid" },
		func(r *TextRequest) { r.AttemptID = "00000000-0000-0000-0000-000000000000" },
		func(r *TextRequest) { r.Body = "" },
		func(r *TextRequest) { r.Body = strings.Repeat("x", 16001) },
		func(r *TextRequest) { r.Body = string([]byte{0xff}) },
		func(r *TextRequest) { r.Credential = "" },
		func(r *TextRequest) { r.Credential += "\r\nX-Secret: leak" },
	}
	for _, provider := range []string{"whatsapp", "twilio"} {
		for i, mutate := range mutations {
			r := outboundRequest(provider)
			mutate(&r)
			transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid request reached provider")
				return nil, nil
			})})
			if got := transport.SendText(context.Background(), r); got != rejectedTextResult() {
				t.Fatal(provider, i, got)
			}
		}
	}
	for _, callback := range []string{"", "http://chat.example/status", "https://user:secret@chat.example/status", "https://chat.example/status?attempt=x", "https://chat.example/status#fragment", "https://bad_host/status", "https:///nohost"} {
		r := outboundRequest("twilio")
		r.CallbackURL = callback
		if validTextSend(r) {
			t.Fatal("invalid callback accepted")
		}
	}
	for _, version := range []string{"", "v25.0/../../evil", "https://evil.test", "latest", "v25.1"} {
		r := outboundRequest("whatsapp")
		r.APIVersion = version
		if validTextSend(r) {
			t.Fatal("unpinned or invalid API version accepted")
		}
	}
	for _, provider := range []string{"whatsapp", "twilio"} {
		r := outboundRequest(provider)
		limit := 1600
		if provider == "whatsapp" {
			limit = 4096
		}
		r.Body = strings.Repeat("x", limit)
		if !validTextSend(r) {
			t.Fatal("valid maximum rejected")
		}
		r.Body += "x"
		if validTextSend(r) {
			t.Fatal("provider body limit not enforced")
		}
	}
}

func TestTextTransportMalformedSuccessRemainsUnknown(t *testing.T) {
	for _, provider := range []string{"whatsapp", "twilio"} {
		base := whatsappOutboundJSON
		if provider == "twilio" {
			base = twilioOutboundJSON("queued")
		}
		bodies := []string{"", "{", "{}", "null", base + "{}", strings.Repeat("x", (1<<20)+1)}
		if provider == "whatsapp" {
			bodies = append(bodies,
				strings.Replace(base, `"whatsapp"`, `"sms"`, 1),
				strings.Replace(base, `"input":"14155550999"`, `"input":"14155550000"`, 1),
				strings.Replace(base, `"wa_id":"14155550999"`, `"wa_id":"14155550000"`, 1),
				strings.Replace(base, `wamid.example123=`, "", 1),
				strings.Replace(base, `wamid.example123=`, "unrelated-id", 1),
				strings.Replace(base, `"contacts":[`, `"contacts":[{"input":"14155550999","wa_id":"14155550999"},`, 1),
				strings.Replace(base, `"messages":[`, `"messages":[{"id":"wamid.second"},`, 1),
				strings.TrimSuffix(base, "}")+`,"error":{"message":"private"}}`,
			)
		} else {
			for _, pair := range [][2]string{{outboundAccount, "AC33333333333333333333333333333333"}, {outboundSID, "MM22222222222222222222222222222222"}, {"+14155550123", "+14155550000"}, {"+14155550999", "+14155550000"}, {"outbound-api", "inbound"}, {`"num_media":"0"`, `"num_media":"1"`}, {`"queued"`, `"read"`}, {`"queued"`, `"future-status"`}} {
				bodies = append(bodies, strings.Replace(base, pair[0], pair[1], 1))
			}
			bodies = append(bodies, strings.Replace(base, `"num_media":"0",`, "", 1))
		}
		for i, body := range bodies {
			transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(*http.Request) (*http.Response, error) { return outboundResponse(200, body), nil })})
			if got := transport.SendText(context.Background(), outboundRequest(provider)); got != unknownTextResult() {
				t.Fatal(provider, i, got)
			}
		}
	}
}

func TestTextTransportTwilioStatusNormalization(t *testing.T) {
	for raw, want := range map[string]string{"accepted": "provider_accepted", "queued": "provider_accepted", "sending": "provider_accepted", "sent": "sent", "delivered": "delivered", "failed": "failed", "undelivered": "failed"} {
		transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(*http.Request) (*http.Response, error) {
			return outboundResponse(201, twilioOutboundJSON(raw)), nil
		})})
		got := transport.SendText(context.Background(), outboundRequest("twilio"))
		if got.Status != want || got.ProviderID != outboundSID || got.Disposition != DispositionAccepted {
			t.Fatal(raw, got)
		}
	}
}

func TestTextTransportLookupOnlyVerifiedTwilioSMS(t *testing.T) {
	calls := 0
	body, code := twilioOutboundJSON("delivered"), 200
	transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodGet || req.URL.String() != "https://api.twilio.com/2010-04-01/Accounts/"+outboundAccount+"/Messages/"+outboundSID+".json" || req.Body != nil {
			t.Error("unexpected lookup protocol")
		}
		user, password, ok := req.BasicAuth()
		if !ok || user != outboundAccount || password != strings.Repeat("s", 32) {
			t.Error("missing lookup credentials")
		}
		return outboundResponse(code, body), nil
	})})
	r := outboundRequest("twilio")
	r.Body, r.CallbackURL, r.AttemptID = "", "", ""
	if got := transport.LookupText(context.Background(), r, outboundSID); got != (Result{ProviderID: outboundSID, Status: "delivered", Disposition: DispositionAccepted}) {
		t.Fatal(got)
	}
	for _, status := range []int{400, 401, 403, 404, 408, 429, 500} {
		code = status
		if got := transport.LookupText(context.Background(), r, outboundSID); got != unknownTextResult() {
			t.Fatal(status, got)
		}
	}
	code = 200
	body = strings.Replace(twilioOutboundJSON("sent"), outboundSID, "SM33333333333333333333333333333333", 1)
	if got := transport.LookupText(context.Background(), r, outboundSID); got != unknownTextResult() {
		t.Fatal(got)
	}
	before := calls
	for _, id := range []string{"", "../traverse", "MM22222222222222222222222222222222"} {
		if got := transport.LookupText(context.Background(), r, id); got != unknownTextResult() {
			t.Fatal(got)
		}
	}
	if got := transport.LookupText(context.Background(), outboundRequest("whatsapp"), "wamid.example123="); got != unknownTextResult() || calls != before {
		t.Fatal("unsupported lookup reached provider", got, calls)
	}
}

func TestTextTransportCopiesClientAndBoundsTimeout(t *testing.T) {
	original := &http.Client{Timeout: time.Hour, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	transport := NewHTTPTextTransport(original)
	if transport.client == original || original.Timeout != time.Hour || transport.client.Timeout != 15*time.Second {
		t.Fatal("client not safely copied")
	}
	if transport.client.CheckRedirect(nil, nil) != http.ErrUseLastResponse || original.CheckRedirect(nil, nil) != nil {
		t.Fatal("redirect prevention missing or modified caller")
	}
	if NewHTTPTextTransport(nil).client.Timeout != 15*time.Second {
		t.Fatal("nil client unsafe")
	}
}

// A partial success body is an ambiguous send even if its prefix looks valid.
type outboundBrokenBody struct{ closed bool }

func (b *outboundBrokenBody) Read([]byte) (int, error) {
	return 0, errors.New("private provider body read error")
}
func (b *outboundBrokenBody) Close() error { b.closed = true; return nil }

func TestTextTransportClosesBodiesAndKeepsReadFailuresUnknown(t *testing.T) {
	for _, provider := range []string{"twilio", "whatsapp"} {
		body := &outboundBrokenBody{}
		transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: body}, nil
		})})
		if got := transport.SendText(context.Background(), outboundRequest(provider)); got != unknownTextResult() || !body.closed {
			t.Fatal(got, body.closed)
		}
	}
}

func TestTextTransportOnlyDocumentedErrorsPermitRetry(t *testing.T) {
	for _, test := range []struct {
		provider string
		status   int
		body     string
		rejected bool
	}{
		{"twilio", 400, `{"code":21211,"status":400,"message":"invalid To"}`, true},
		{"twilio", 400, `{"code":21606,"status":400,"message":"invalid From"}`, true},
		{"twilio", 400, `{"code":21608,"status":400,"message":"unverified recipient"}`, true},
		{"twilio", 400, `{"code":20003,"status":400,"message":"permission denied"}`, true},
		{"twilio", 401, `{"code":20003,"status":401,"message":"authentication failed"}`, true},
		{"twilio", 403, `{"code":20003,"status":403,"message":"permission denied"}`, true},
		{"twilio", 429, `{"code":20429,"status":429,"message":"too many requests"}`, true},
		{"twilio", 400, `{"code":99999,"status":400,"message":"unknown error"}`, false},
		{"twilio", 400, `{"code":21211,"status":401,"message":"mismatched status"}`, false},
		{"twilio", 401, `{"code":21211,"status":401,"message":"wrong auth code"}`, false},
		{"twilio", 429, `{"code":20429,"status":429}`, false},
		{"twilio", 429, `{"code":99999,"status":429,"message":"unknown rate error"}`, false},
		{"twilio", 500, `{"code":20003,"status":500,"message":"not a definitive rejection"}`, false},
		{"twilio", 408, `{"code":21211,"status":408,"message":"uncertain"}`, false},
		{"twilio", 400, `{`, false},
		{"whatsapp", 400, `{"error":{"code":1,"is_transient":true}}`, false},
		{"whatsapp", 400, `{"error":{"code":100,"is_transient":false}}`, false},
		{"whatsapp", 401, `{"error":{"code":190,"is_transient":false}}`, false},
	} {
		transport := NewHTTPTextTransport(&http.Client{Transport: outboundRoundTripper(func(*http.Request) (*http.Response, error) { return outboundResponse(test.status, test.body), nil })})
		want := unknownTextResult()
		if test.rejected {
			want = rejectedTextResult()
		}
		if got := transport.SendText(context.Background(), outboundRequest(test.provider)); got != want {
			t.Fatal(test.provider, test.status, got)
		}
	}
}

func TestTextTransportResponseLossMakesOnePhysicalPOST(t *testing.T) {
	var postCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "warm connection")
			return
		}
		postCount.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close() // Simulate provider processing then losing its response.
	}))
	defer server.Close()
	localURL, _ := url.Parse(server.URL)
	network := http.DefaultTransport.(*http.Transport).Clone()
	network.Proxy = nil
	defer network.CloseIdleConnections()
	route := outboundRoundTripper(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "api.twilio.com" {
			t.Fatal("unexpected provider host")
		}
		clone := req.Clone(req.Context())
		clone.URL.Scheme, clone.URL.Host = localURL.Scheme, localURL.Host
		return network.RoundTrip(clone)
	})
	client := &http.Client{Transport: route}
	// A reused connection is necessary to exercise net/http's retry path.
	warm, _ := http.NewRequest(http.MethodGet, "https://api.twilio.com/warm", nil)
	res, err := client.Do(warm)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	got := NewHTTPTextTransport(client).SendText(context.Background(), outboundRequest("twilio"))
	if got != unknownTextResult() || postCount.Load() != 1 {
		t.Fatal("response loss replayed POST", got, postCount.Load())
	}
}
