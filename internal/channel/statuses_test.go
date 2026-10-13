package channel

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func whatsappStatusPayload(status, opaque string) []byte {
	data := map[string]any{"id": "wamid.example123=", "recipient_id": "14155550999", "status": status, "timestamp": "1700000000"}
	if opaque != "" {
		data["biz_opaque_callback_data"] = opaque
	}
	raw, _ := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{"id": "12345", "changes": []any{map[string]any{"field": "messages", "value": map[string]any{"messaging_product": "whatsapp", "metadata": map[string]any{"phone_number_id": "67890"}, "statuses": []any{data}}}}}}})
	return raw
}

func twilioStatusPayload(status string) []byte {
	return []byte(url.Values{"AccountSid": {outboundAccount}, "From": {"+14155550123"}, "To": {"+14155550999"}, "MessageSid": {outboundSID}, "MessageStatus": {status}, "SmsSid": {outboundSID}, "SmsStatus": {status}, "FutureField": {"must stay signed"}}.Encode())
}

func TestParseWhatsAppStatusesAllSupportedAndOptionalOpaque(t *testing.T) {
	for _, status := range []string{"sent", "delivered", "read", "failed"} {
		for _, opaque := range []string{"", outboundAttempt} {
			events, err := ParseWhatsAppStatuses(whatsappStatusPayload(status, opaque))
			want := StatusEvent{AttemptID: opaque, Provider: "whatsapp", ProviderAccountID: "12345", SenderID: "67890", RecipientID: "14155550999", ProviderID: "wamid.example123=", Status: status}
			if err != nil || len(events) != 1 || events[0] != want {
				t.Fatal(events, err)
			}
		}
	}
}

func TestParseWhatsAppStatusesRejectsMalformedAndMixedEnvelopes(t *testing.T) {
	base := string(whatsappStatusPayload("sent", outboundAttempt))
	tests := []string{"", "null", "{}", base + "{}", strings.Repeat("x", MaxWebhookBytes+1)}
	for _, pair := range [][2]string{
		{`"whatsapp_business_account"`, `"other"`},
		{`"id":"12345"`, `"id":"invalid"`},
		{`"field":"messages"`, `"field":"calls"`},
		{`"messaging_product":"whatsapp"`, `"messaging_product":"sms"`},
		{`"phone_number_id":"67890"`, `"phone_number_id":"../evil"`},
		{`"recipient_id":"14155550999"`, `"recipient_id":""`},
		{`"id":"wamid.example123="`, `"id":"not-wamid"`},
		{`"id":"wamid.example123="`, `"id":"wamid.bad\nvalue"`},
		{`"timestamp":"1700000000"`, `"timestamp":"0"`},
		{`"timestamp":"1700000000"`, `"timestamp":"bad"`},
		{outboundAttempt, "not-a-uuid"},
		{`"biz_opaque_callback_data":"` + outboundAttempt + `"`, `"biz_opaque_callback_data":null`},
		{`"biz_opaque_callback_data":"` + outboundAttempt + `"`, `"biz_opaque_callback_data":""`},
		{outboundAttempt, "00000000-0000-0000-0000-000000000000"},
		{`"status":"sent"`, `"status":"future"`},
		{`"status":"sent"`, `"status":"queued"`},
		{`"statuses":[`, `"messages":[{"id":"inbound"}],"statuses":[`},
		{`"statuses":[`, `"errors":[{"code":1}],"statuses":[`},
		{`"statuses":[`, `"statuses":[{"id":"wamid.other","recipient_id":"14155550999","timestamp":"0","status":"sent"},`},
	} {
		tests = append(tests, strings.Replace(base, pair[0], pair[1], 1))
	}
	for i, raw := range tests {
		events, err := ParseWhatsAppStatuses([]byte(raw))
		if err == nil || events != nil {
			t.Fatal("partial or malformed envelope accepted", i, events, err)
		}
	}
}

func TestParseWhatsAppStatusesBoundsWholeBatch(t *testing.T) {
	status := `{"id":"wamid.example123=","recipient_id":"14155550999","timestamp":"1700000000","status":"sent"}`
	for _, n := range []int{100, 101} {
		raw := `{"object":"whatsapp_business_account","entry":[{"id":"12345","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"67890"},"statuses":[` + strings.TrimSuffix(strings.Repeat(status+",", n), ",") + `]}}]}]}`
		events, err := ParseWhatsAppStatuses([]byte(raw))
		if n == 100 && (err != nil || len(events) != 100) {
			t.Fatal(len(events), err)
		}
		if n == 101 && (err == nil || events != nil) {
			t.Fatal("oversized batch accepted")
		}
	}
}

func TestParseWhatsAppStatusesPreservesEnvelopeIdentity(t *testing.T) {
	var first, second map[string]any
	json.Unmarshal(whatsappStatusPayload("sent", outboundAttempt), &first)
	json.Unmarshal(whatsappStatusPayload("delivered", ""), &second)
	entries := first["entry"].([]any)
	secondEntry := second["entry"].([]any)[0].(map[string]any)
	secondEntry["id"] = "99999"
	secondValue := secondEntry["changes"].([]any)[0].(map[string]any)["value"].(map[string]any)
	secondValue["metadata"].(map[string]any)["phone_number_id"] = "88888"
	secondValue["future_field"] = map[string]any{"anything": true}
	first["entry"] = append(entries, secondEntry)
	raw, _ := json.Marshal(first)
	events, err := ParseWhatsAppStatuses(raw)
	if err != nil || len(events) != 2 || events[0].ProviderAccountID != "12345" || events[1].ProviderAccountID != "99999" || events[1].SenderID != "88888" || events[1].Status != "delivered" {
		t.Fatal(events, err)
	}
}

func TestParseTwilioStatusNormalizesAndRetainsValidatorParameters(t *testing.T) {
	for raw, want := range map[string]string{"accepted": "provider_accepted", "queued": "provider_accepted", "sending": "provider_accepted", "sent": "sent", "delivered": "delivered", "failed": "failed", "undelivered": "failed"} {
		event, params, err := ParseTwilioStatus(twilioStatusPayload(raw))
		if err != nil || event != (StatusEvent{Provider: "twilio", ProviderAccountID: outboundAccount, SenderID: "+14155550123", RecipientID: "+14155550999", ProviderID: outboundSID, Status: want}) || params["FutureField"] != "must stay signed" || params["MessageStatus"] != raw {
			t.Fatal(event, params, err)
		}
	}
}

func TestParseTwilioStatusRejectsAmbiguousAndUnsupportedEvents(t *testing.T) {
	base := string(twilioStatusPayload("delivered"))
	tests := []string{"", "%broken", "=value", strings.Repeat("x", MaxWebhookBytes+1), base + "&FutureField=duplicate", base + "&MessageSid=" + outboundSID, base + "&NumMedia=1"}
	for _, pair := range [][2]string{
		{outboundAccount, "not-an-account"},
		{outboundSID, "MM22222222222222222222222222222222"},
		{"From=%2B14155550123", "From=whatsapp%3A%2B14155550123"},
		{"To=%2B14155550999", "To=not-a-number"},
		{"MessageStatus=delivered", "MessageStatus="},
		{"SmsStatus=delivered", "SmsStatus=failed"},
		{"SmsSid=" + outboundSID, "SmsSid=SM33333333333333333333333333333333"},
	} {
		tests = append(tests, strings.Replace(base, pair[0], pair[1], 1))
	}
	for _, status := range []string{"read", "received", "receiving", "scheduled", "canceled", "future", "Delivered"} {
		tests = append(tests, string(twilioStatusPayload(status)))
	}
	for i, raw := range tests {
		event, params, err := ParseTwilioStatus([]byte(raw))
		if err == nil || event != (StatusEvent{}) || params != nil {
			t.Fatal("ambiguous event accepted", i, event, err)
		}
	}
	for _, field := range []string{"AccountSid", "From", "To", "MessageSid", "MessageStatus"} {
		values, _ := url.ParseQuery(base)
		values.Del(field)
		if _, _, err := ParseTwilioStatus([]byte(values.Encode())); err == nil {
			t.Fatal("missing field accepted", field)
		}
	}
}

func FuzzParseDeliveryStatuses(f *testing.F) {
	f.Add(whatsappStatusPayload("sent", outboundAttempt))
	f.Add(twilioStatusPayload("delivered"))
	f.Add([]byte("null"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if events, err := ParseWhatsAppStatuses(raw); err == nil {
			if len(events) == 0 || len(events) > 100 {
				t.Fatal("unbounded status set")
			}
			for _, e := range events {
				if e.Provider != "whatsapp" || !numericID.MatchString(e.ProviderAccountID) || !numericID.MatchString(e.SenderID) || !numericID.MatchString(e.RecipientID) || !validWhatsAppMessageID(e.ProviderID) || (e.AttemptID != "" && !validAttemptID(e.AttemptID)) {
					t.Fatal("invalid event admitted")
				}
			}
		}
		if e, params, err := ParseTwilioStatus(raw); err == nil {
			if e.Provider != "twilio" || e.AttemptID != "" || !accountSID.MatchString(e.ProviderAccountID) || !validSMSID(e.ProviderID) || !phoneNumber.MatchString(e.SenderID) || !phoneNumber.MatchString(e.RecipientID) || params["MessageSid"] != e.ProviderID {
				t.Fatal("invalid SMS event admitted")
			}
		}
	})
}
