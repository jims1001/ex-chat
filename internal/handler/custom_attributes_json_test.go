package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/stretchr/testify/require"
)

const exactCustomAttributes = `{"integer":9007199254740993,"negative":-9007199254740993,"decimal":0.100000000000000005,"exponent":9.007199254740993e15,"nested":{"array":[9007199254740995,0.100000000000000005]},"numeric_string":"9007199254740993","flag":true,"nil":null}`

func assertExactCustomAttributes(t *testing.T, raw string) {
	t.Helper()
	var attrs map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &attrs))
	for key, expected := range map[string]string{
		"integer": "9007199254740993", "negative": "-9007199254740993",
		"decimal": "0.100000000000000005", "exponent": "9.007199254740993e15",
		"nested":         `{"array":[9007199254740995,0.100000000000000005]}`,
		"numeric_string": `"9007199254740993"`, "flag": "true",
	} {
		require.Equal(t, expected, string(attrs[key]), "attribute %s", key)
	}
}

func ordinaryCustomAttributeRoutes(f *widgetHTTPSecurityFixture) {
	contacts := repository.NewContactRepository(f.db)
	inboxes := repository.NewInboxRepository(f.db)
	h := NewContactHandler(contacts, inboxes)
	conversations := NewConversationHandler(repository.NewConversationRepository(f.db), repository.NewMessageRepository(f.db), inboxes, contacts, nil, nil)
	g := f.r.Group("/ordinary", func(c *gin.Context) { c.Set(middleware.ContextAccountID, f.inbox.AccountID) })
	g.POST("/contacts", h.CreateContact)
	g.PATCH("/contacts/:id", h.UpdateContact)
	g.POST("/contacts/:id/destroy_custom_attributes", h.DestroyCustomAttributes)
	g.PATCH("/conversations/:id", conversations.UpdateConversation)
	g.PATCH("/conversations/:id/custom_attributes", conversations.UpdateCustomAttributes)
}

func TestContactCustomAttributesHTTPPreservesExactNumbers(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	ordinaryCustomAttributeRoutes(f)
	for _, form := range []string{"object", "legacy string"} {
		t.Run(form, func(t *testing.T) {
			payload := exactCustomAttributes
			if form == "legacy string" {
				encoded, err := json.Marshal(payload)
				require.NoError(t, err)
				payload = string(encoded)
			}
			w := f.request(http.MethodPost, "/ordinary/contacts", `{"name":"Exact contact","custom_attributes":`+payload+`}`, "")
			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			var response struct {
				Data domain.Contact `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			id := response.Data.ID
			require.NotZero(t, id)
			assertExactCustomAttributes(t, response.Data.CustomAttributes)
			var stored domain.Contact
			require.NoError(t, f.db.First(&stored, id).Error)
			assertExactCustomAttributes(t, stored.CustomAttributes)

			// A regular profile update and deleting an absent/unrelated key must
			// not round the saved custom numbers.
			path := fmt.Sprintf("/ordinary/contacts/%d", id)
			w = f.request(http.MethodPatch, path, `{"name":"Renamed"}`, "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			w = f.request(http.MethodPost, path+"/destroy_custom_attributes", `{"custom_attributes":["unrelated"]}`, "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NoError(t, f.db.First(&stored, id).Error)
			assertExactCustomAttributes(t, stored.CustomAttributes)

			// Exercise actual numeric ingress through update, independently of
			// create and of stored-number retention.
			require.NoError(t, f.db.Model(&stored).Update("custom_attributes", "{}").Error)
			w = f.request(http.MethodPatch, path, `{"custom_attributes":`+payload+`}`, "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NoError(t, f.db.First(&stored, id).Error)
			assertExactCustomAttributes(t, stored.CustomAttributes)
		})
	}
}

func TestConversationCustomAttributesHTTPPreservesExactNumbers(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	ordinaryCustomAttributeRoutes(f)
	base := fmt.Sprintf("/ordinary/conversations/%d", f.conversation.ID)
	for _, tc := range []struct{ name, path, body string }{
		{"nested merge", base + "/custom_attributes", `{"custom_attributes":` + exactCustomAttributes + `}`},
		{"flat merge", base + "/custom_attributes", exactCustomAttributes},
		{"ordinary update", base, `{"custom_attributes":` + exactCustomAttributes + `}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, f.db.Model(&domain.Conversation{}).Where("id = ?", f.conversation.ID).Update("custom_attributes", "{}").Error)
			w := f.request(http.MethodPatch, tc.path, tc.body, "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var stored domain.Conversation
			require.NoError(t, f.db.First(&stored, f.conversation.ID).Error)
			assertExactCustomAttributes(t, stored.CustomAttributes)

			w = f.request(http.MethodPatch, base+"/custom_attributes", `{"custom_attributes":{"unrelated":"changed"}}`, "")
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.NoError(t, f.db.First(&stored, f.conversation.ID).Error)
			assertExactCustomAttributes(t, stored.CustomAttributes)
			var response struct {
				Data struct {
					CustomAttributes json.RawMessage `json:"custom_attributes"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			assertExactCustomAttributes(t, string(response.Data.CustomAttributes))
		})
	}
}

func TestWidgetCustomAttributeIngressPreservesExactNumbers(t *testing.T) {
	f := newWidgetHTTPSecurityFixture(t)
	for _, tc := range []struct {
		name, method, path, extra string
		conversation              bool
	}{
		{"identify", http.MethodPost, "/contact", "", false},
		{"update contact", http.MethodPatch, "/contact", "", false},
		{"merge contact", http.MethodPatch, "/attributes", "", false},
		{"set user", http.MethodPost, "/set_user", `"identifier":"owner-identifier",`, false},
		{"merge conversation", http.MethodPatch, fmt.Sprintf("/conversations/%d/attributes", f.conversation.ID), "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, f.db.Model(&domain.Contact{}).Where("id = ?", f.owner.ID).Update("custom_attributes", "{}").Error)
			w := f.request(tc.method, tc.path, `{`+tc.extra+`"custom_attributes":`+exactCustomAttributes+`}`, f.session)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			if tc.conversation {
				var stored domain.Conversation
				require.NoError(t, f.db.First(&stored, f.conversation.ID).Error)
				assertExactCustomAttributes(t, stored.CustomAttributes)
			} else {
				var stored domain.Contact
				require.NoError(t, f.db.First(&stored, f.owner.ID).Error)
				assertExactCustomAttributes(t, stored.CustomAttributes)
			}
		})
	}
}

func TestPublicCustomAttributeIngressPreservesExactNumbers(t *testing.T) {
	f := newPublicVisitorFixture(t)
	for _, tc := range []struct {
		name, path   string
		conversation bool
	}{
		{"contact", f.contactPath(), false},
		{"conversation", fmt.Sprintf("%s/conversations/%d/custom_attributes", f.contactPath(), f.conversation.ID), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(http.MethodPatch, tc.path, `{"custom_attributes":`+exactCustomAttributes+`}`, f.session)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			if tc.conversation {
				var stored domain.Conversation
				require.NoError(t, f.db.First(&stored, f.conversation.ID).Error)
				assertExactCustomAttributes(t, stored.CustomAttributes)
			} else {
				var stored domain.Contact
				require.NoError(t, f.db.First(&stored, f.owner.ID).Error)
				// Public contact merge treats null as removal, as before.
				require.NotContains(t, stored.CustomAttributes, `"nil"`)
				require.Contains(t, stored.CustomAttributes, `"integer":9007199254740993`)
				require.Contains(t, stored.CustomAttributes, `"decimal":0.100000000000000005`)
			}
		})
	}
}

func TestCustomAttributesJSONBindingKeepsValidationAndScope(t *testing.T) {
	originalUseNumber := binding.EnableDecoderUseNumber
	for _, tc := range []struct {
		name, body string
		wantError  bool
	}{
		{"valid", `{"custom_attributes":{"n":9007199254740993}}`, false},
		{"missing required", `{}`, true},
		{"null required", `{"custom_attributes":null}`, true},
		{"wrong object type", `{"custom_attributes":"bad"}`, true},
		{"malformed", `{"custom_attributes":`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(tc.body))
			var payload WidgetMergeCustomAttributesRequest
			err := bindCustomAttributesJSON(c, &payload)
			if tc.wantError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, json.Number("9007199254740993"), payload.CustomAttributes["n"])
			}
		})
	}
	require.Equal(t, originalUseNumber, binding.EnableDecoderUseNumber)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"n":9007199254740993}`))
	var ordinary map[string]any
	require.NoError(t, c.ShouldBindJSON(&ordinary))
	if !originalUseNumber {
		require.IsType(t, float64(0), ordinary["n"], "unrelated Gin binding retains legacy numeric decoding")
	}
}
