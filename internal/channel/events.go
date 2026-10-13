package channel

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

const MaxWebhookBytes = 256 * 1024

type TextEvent struct {
	Provider          string
	ProviderAccountID string
	RecipientID       string
	ID                string
	From              string
	Name              string
	Text              string
}

func ParseWhatsApp(raw []byte) ([]TextEvent, error) {
	var payload struct {
		Object string `json:"object"`
		Entry  []struct {
			ID      string `json:"id"`
			Changes []struct {
				Field string `json:"field"`
				Value struct {
					Product  string `json:"messaging_product"`
					Metadata struct {
						PhoneID string `json:"phone_number_id"`
					} `json:"metadata"`
					Contacts []struct {
						WaID    string `json:"wa_id"`
						Profile struct {
							Name string `json:"name"`
						} `json:"profile"`
					} `json:"contacts"`
					Messages []struct {
						From      string `json:"from"`
						ID        string `json:"id"`
						Timestamp string `json:"timestamp"`
						Type      string `json:"type"`
						Text      struct {
							Body string `json:"body"`
						} `json:"text"`
					} `json:"messages"`
					Statuses []json.RawMessage `json:"statuses"`
					Errors   []json.RawMessage `json:"errors"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Object != "whatsapp_business_account" || len(payload.Entry) == 0 {
		return nil, ErrInvalid
	}
	var events []TextEvent
	for _, entry := range payload.Entry {
		if !numericID.MatchString(entry.ID) || len(entry.Changes) == 0 {
			return nil, ErrInvalid
		}
		for _, change := range entry.Changes {
			v := change.Value
			if change.Field != "messages" || v.Product != "whatsapp" || len(v.Statuses) > 0 || len(v.Errors) > 0 || len(v.Messages) == 0 {
				return nil, ErrUnsupported
			}
			if !numericID.MatchString(v.Metadata.PhoneID) {
				return nil, ErrInvalid
			}
			for _, m := range v.Messages {
				if m.Type != "text" {
					return nil, ErrUnsupported
				}
				timestamp, err := strconv.ParseInt(m.Timestamp, 10, 64)
				if err != nil || timestamp <= 0 || !numericID.MatchString(m.From) || m.ID == "" || len(m.ID) > 255 || len(m.Text.Body) == 0 || len(m.Text.Body) > 16000 {
					return nil, ErrInvalid
				}
				name := m.From
				for _, contact := range v.Contacts {
					if contact.WaID == m.From && contact.Profile.Name != "" {
						name = contact.Profile.Name
						break
					}
				}
				if len(name) > 255 {
					name = m.From
				}
				events = append(events, TextEvent{Provider: "whatsapp", ProviderAccountID: entry.ID, RecipientID: v.Metadata.PhoneID, ID: m.ID, From: m.From, Name: name, Text: m.Text.Body})
				if len(events) > 100 {
					return nil, ErrInvalid
				}
			}
		}
	}
	return events, nil
}

// Twilio's Go validator takes single-valued parameters; duplicates and URL
// query parameters are deliberately rejected rather than parsed ambiguously.
func ParseTwilio(raw []byte) (TextEvent, map[string]string, error) {
	values, err := url.ParseQuery(string(raw))
	if err != nil || len(values) == 0 || len(values) > 128 {
		return TextEvent{}, nil, ErrInvalid
	}
	params := make(map[string]string, len(values))
	for k, v := range values {
		if len(v) != 1 || k == "" {
			return TextEvent{}, nil, ErrInvalid
		}
		params[k] = v[0]
	}
	if params["NumMedia"] != "" && params["NumMedia"] != "0" {
		return TextEvent{}, nil, ErrUnsupported
	}
	if strings.HasPrefix(params["From"], "whatsapp:") || strings.HasPrefix(params["To"], "whatsapp:") {
		return TextEvent{}, nil, ErrUnsupported
	}
	if !accountSID.MatchString(params["AccountSid"]) || !messageSID.MatchString(params["MessageSid"]) || !phoneNumber.MatchString(params["From"]) || !phoneNumber.MatchString(params["To"]) || len(params["Body"]) == 0 || len(params["Body"]) > 16000 {
		return TextEvent{}, nil, ErrInvalid
	}
	if params["SmsSid"] != "" && params["SmsSid"] != params["MessageSid"] {
		return TextEvent{}, nil, ErrInvalid
	}
	return TextEvent{Provider: "twilio", ProviderAccountID: params["AccountSid"], RecipientID: params["To"], ID: params["MessageSid"], From: params["From"], Name: params["From"], Text: params["Body"]}, params, nil
}
