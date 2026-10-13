package channel

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Provider performs only token exchange and ownership reads. It never subscribes
// an app, registers numbers, sends messages or changes a provider resource.
type Provider interface {
	ExchangeWhatsApp(context.Context, Config, string, string, string) (string, error)
	VerifyTwilio(context.Context, string, string, string) error
}

type HTTPProvider struct{ client *http.Client }

func NewHTTPProvider(client *http.Client) *HTTPProvider {
	if client == nil {
		client = &http.Client{}
	}
	safe := *client
	safe.Timeout = 10 * time.Second
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &HTTPProvider{client: &safe}
}

func (p *HTTPProvider) read(req *http.Request, target any) error {
	res, err := p.client.Do(req)
	if err != nil {
		return ErrProvider
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ErrProvider
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return ErrProvider
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return ErrProvider
	}
	return nil
}

func (p *HTTPProvider) ExchangeWhatsApp(ctx context.Context, cfg Config, code, wabaID, phoneID string) (string, error) {
	if err := cfg.Validate("whatsapp"); err != nil {
		return "", err
	}
	if code == "" || len(code) > 4096 || !numericID.MatchString(wabaID) || !numericID.MatchString(phoneID) {
		return "", ErrInvalid
	}
	// Meta's documented code exchange uses GET. Never log this URL, HTTP error,
	// request or response: the query contains an app secret and one-use code.
	query := url.Values{"client_id": {cfg.WhatsAppAppID}, "client_secret": {cfg.WhatsAppAppSecret}, "code": {code}}
	if cfg.WhatsAppRedirectURI != "" {
		query.Set("redirect_uri", cfg.WhatsAppRedirectURI)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.facebook.com/"+cfg.WhatsAppAPIVersion+"/oauth/access_token?"+query.Encode(), nil)
	if err != nil {
		return "", ErrProvider
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := p.read(req, &token); err != nil || token.AccessToken == "" || len(token.AccessToken) > 16384 {
		return "", ErrProvider
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, "https://graph.facebook.com/"+cfg.WhatsAppAPIVersion+"/"+wabaID+"/phone_numbers?fields=id&limit=100", nil)
	if err != nil {
		return "", ErrProvider
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	var numbers struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := p.read(req, &numbers); err != nil {
		return "", err
	}
	matches := 0
	for _, n := range numbers.Data {
		if n.ID == phoneID {
			matches++
		}
	}
	// Never follow provider-supplied paging URLs with an access token. Accounts
	// whose desired number is beyond this bounded page are unsupported for now.
	if matches != 1 {
		return "", ErrUnauthorized
	}
	return token.AccessToken, nil
}

func (p *HTTPProvider) VerifyTwilio(ctx context.Context, sid, token, number string) error {
	if !accountSID.MatchString(sid) || len(token) < 16 || len(token) > 256 || !phoneNumber.MatchString(number) {
		return ErrInvalid
	}
	query := url.Values{"PhoneNumber": {number}, "PageSize": {"100"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.twilio.com/2010-04-01/Accounts/"+sid+"/IncomingPhoneNumbers.json?"+query.Encode(), nil)
	if err != nil {
		return ErrProvider
	}
	req.SetBasicAuth(sid, token)
	var result struct {
		Numbers []struct {
			AccountSID   string `json:"account_sid"`
			Number       string `json:"phone_number"`
			Capabilities struct {
				SMS bool `json:"sms"`
			} `json:"capabilities"`
		} `json:"incoming_phone_numbers"`
	}
	if err := p.read(req, &result); err != nil {
		return err
	}
	matches := 0
	for _, n := range result.Numbers {
		if n.AccountSID == sid && n.Number == number && n.Capabilities.SMS {
			matches++
		}
	}
	if matches != 1 {
		return ErrUnauthorized
	}
	return nil
}
