package channel

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// StatusEvent contains only provider identity and normalized delivery state.
// Authenticity and tenant/attempt correlation must be checked by the caller
// before mutating any message. Twilio's AttemptID comes from the signed route.
type StatusEvent struct {
	AttemptID         string
	Provider          string
	ProviderAccountID string
	SenderID          string
	RecipientID       string
	ProviderID        string
	Status            string
}

// ParseWhatsAppStatuses parses the entire envelope atomically. Unknown event
// types, inbound messages, or malformed siblings reject the whole batch.
// Signature verification must use the original raw bytes before parsing.
func ParseWhatsAppStatuses(raw []byte) ([]StatusEvent, error) {
	if len(raw) == 0 || len(raw) > MaxWebhookBytes {
		return nil, ErrInvalid
	}
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
					Statuses []struct {
						ID          string          `json:"id"`
						RecipientID string          `json:"recipient_id"`
						Status      string          `json:"status"`
						Timestamp   string          `json:"timestamp"`
						Opaque      json.RawMessage `json:"biz_opaque_callback_data"`
					} `json:"statuses"`
					Messages []json.RawMessage `json:"messages"`
					Errors   []json.RawMessage `json:"errors"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Object != "whatsapp_business_account" || len(payload.Entry) == 0 {
		return nil, ErrInvalid
	}
	var events []StatusEvent
	for _, entry := range payload.Entry {
		if !numericID.MatchString(entry.ID) || len(entry.Changes) == 0 {
			return nil, ErrInvalid
		}
		for _, change := range entry.Changes {
			v := change.Value
			if change.Field != "messages" || v.Product != "whatsapp" || len(v.Messages) > 0 || len(v.Errors) > 0 || len(v.Statuses) == 0 {
				return nil, ErrUnsupported
			}
			if !numericID.MatchString(v.Metadata.PhoneID) {
				return nil, ErrInvalid
			}
			for _, s := range v.Statuses {
				var attemptID string
				if len(s.Opaque) > 0 && (json.Unmarshal(s.Opaque, &attemptID) != nil || !validAttemptID(attemptID)) {
					return nil, ErrInvalid
				}
				timestamp, err := strconv.ParseInt(s.Timestamp, 10, 64)
				if err != nil || timestamp <= 0 || !numericID.MatchString(s.Timestamp) || !numericID.MatchString(s.RecipientID) || !validWhatsAppMessageID(s.ID) {
					return nil, ErrInvalid
				}
				status, ok := normalizeTextStatus("whatsapp", s.Status)
				if !ok {
					return nil, ErrUnsupported
				}
				events = append(events, StatusEvent{AttemptID: attemptID, Provider: "whatsapp", ProviderAccountID: entry.ID, SenderID: v.Metadata.PhoneID, RecipientID: s.RecipientID, ProviderID: s.ID, Status: status})
				if len(events) > 100 {
					return nil, ErrInvalid
				}
			}
		}
	}
	return events, nil
}

// ParseTwilioStatus preserves all single-value form fields for Twilio's
// official request validator. Never rebuild a signature input from only the
// recognized fields. Duplicate parameters are deliberately unsupported.
func ParseTwilioStatus(raw []byte) (StatusEvent, map[string]string, error) {
	if len(raw) == 0 || len(raw) > MaxWebhookBytes {
		return StatusEvent{}, nil, ErrInvalid
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil || len(values) == 0 || len(values) > 128 {
		return StatusEvent{}, nil, ErrInvalid
	}
	params := make(map[string]string, len(values))
	for k, v := range values {
		if k == "" || len(v) != 1 {
			return StatusEvent{}, nil, ErrInvalid
		}
		params[k] = v[0]
	}
	if strings.HasPrefix(params["From"], "whatsapp:") || strings.HasPrefix(params["To"], "whatsapp:") || (params["NumMedia"] != "" && params["NumMedia"] != "0") {
		return StatusEvent{}, nil, ErrUnsupported
	}
	if !accountSID.MatchString(params["AccountSid"]) || !validSMSID(params["MessageSid"]) || !phoneNumber.MatchString(params["From"]) || !phoneNumber.MatchString(params["To"]) || (params["SmsSid"] != "" && params["SmsSid"] != params["MessageSid"]) || (params["SmsStatus"] != "" && params["SmsStatus"] != params["MessageStatus"]) {
		return StatusEvent{}, nil, ErrInvalid
	}
	if params["MessageStatus"] == "" {
		return StatusEvent{}, nil, ErrInvalid
	}
	status, ok := normalizeTextStatus("twilio", params["MessageStatus"])
	if !ok {
		return StatusEvent{}, nil, ErrUnsupported
	}
	return StatusEvent{Provider: "twilio", ProviderAccountID: params["AccountSid"], SenderID: params["From"], RecipientID: params["To"], ProviderID: params["MessageSid"], Status: status}, params, nil
}
