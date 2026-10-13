package repository

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

const storedExactAttributes = `{"integer":9007199254740993,"decimal":0.100000000000000005,"exponent":9.007199254740993e15,"nested":{"n":-9007199254740993,"list":[9007199254740995]},"numeric_string":"9007199254740993","flag":true,"remove":"old"}`

func assertStoredExactAttributes(t *testing.T, raw string) {
	t.Helper()
	var attrs map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(raw), &attrs))
	for key, want := range map[string]string{
		"integer": "9007199254740993", "decimal": "0.100000000000000005",
		"exponent": "9.007199254740993e15", "nested": `{"list":[9007199254740995],"n":-9007199254740993}`,
		"numeric_string": `"9007199254740993"`, "flag": "true",
	} {
		require.Equal(t, want, string(attrs[key]), "attribute %s", key)
	}
}

func TestConversationCustomAttributeMergeRetainsStoredExactNumbers(t *testing.T) {
	for _, capture := range []bool{false, true} {
		name := "capture disabled"
		if capture {
			name = "capture enabled"
		}
		t.Run(name, func(t *testing.T) {
			repo := conversationAutomationTestRepository(t, capture)
			repo.SetAutomationCapture(capture)
			conversation := automationTestConversation(t, repo)
			require.NoError(t, repo.db.Model(&conversation).Update("custom_attributes", storedExactAttributes).Error)
			merged, err := repo.UpdateCustomAttributes(conversation.AccountID, conversation.ID, map[string]any{"unrelated": "new"})
			require.NoError(t, err)
			require.Equal(t, json.Number("9007199254740993"), merged["integer"])
			require.Equal(t, "9007199254740993", merged["numeric_string"])
			var stored domain.Conversation
			require.NoError(t, repo.db.First(&stored, conversation.ID).Error)
			assertStoredExactAttributes(t, stored.CustomAttributes)
			require.Contains(t, stored.CustomAttributes, `"unrelated":"new"`)
			_, err = repo.UpdateCustomAttributes(conversation.AccountID, conversation.ID, map[string]any{"incoming": json.Number("9007199254740997")})
			require.NoError(t, err)
			require.NoError(t, repo.db.First(&stored, conversation.ID).Error)
			assertStoredExactAttributes(t, stored.CustomAttributes)
			require.Contains(t, stored.CustomAttributes, `"incoming":9007199254740997`)
		})
	}
}

func exactAttributeContactRepository(t *testing.T) *ContactRepository {
	t.Helper()
	db := openMFATestDB(t, filepath.Join(t.TempDir(), "contact-attributes.sqlite"))
	db.Config.IgnoreRelationshipsWhenMigrating = true
	require.NoError(t, db.AutoMigrate(&domain.Contact{}, &domain.Company{}, &domain.Label{}, &domain.ContactLabel{}, &domain.ContactInbox{}, &domain.ContactNote{}, &domain.ChannelContactIdentity{}, &domain.LocalChangeJournal{}))
	return &ContactRepository{db: db}
}

func TestContactCustomAttributeMutationsRetainStoredExactNumbers(t *testing.T) {
	repo := exactAttributeContactRepository(t)
	contact := domain.Contact{AccountID: 91, Name: "Exact numbers", CustomAttributes: storedExactAttributes}
	require.NoError(t, repo.Create(&contact))
	updated, err := repo.UpdatePublic(contact.AccountID, contact.ID, nil, map[string]any{"unrelated": "new", "remove": nil, "incoming": json.Number("9007199254740997")})
	require.NoError(t, err)
	assertStoredExactAttributes(t, updated.CustomAttributes)
	require.NotContains(t, updated.CustomAttributes, `"remove"`)
	require.Contains(t, updated.CustomAttributes, `"incoming":9007199254740997`)
	updated, err = repo.DestroyCustomAttributes(contact.AccountID, contact.ID, []string{"unrelated"})
	require.NoError(t, err)
	assertStoredExactAttributes(t, updated.CustomAttributes)
	require.NotContains(t, updated.CustomAttributes, `"unrelated"`)
	require.Contains(t, updated.CustomAttributes, `"incoming":9007199254740997`)
	// The widget response adapter must not immediately round the map it returns.
	require.Equal(t, json.Number("9007199254740993"), decodeAttributes(updated.CustomAttributes)["integer"])
}

func TestContactMergeRetainsBothContactsExactNumbers(t *testing.T) {
	repo := exactAttributeContactRepository(t)
	base := domain.Contact{AccountID: 91, Name: "Base", CustomAttributes: storedExactAttributes}
	mergee := domain.Contact{AccountID: 91, Name: "Mergee", CustomAttributes: `{"integer":9007199254740995,"mergee_integer":9007199254740997,"mergee_decimal":1.0000000000000001,"mergee_array":[9007199254740999]}`}
	require.NoError(t, repo.Create(&base))
	require.NoError(t, repo.Create(&mergee))
	require.NoError(t, repo.MergeContacts(base.AccountID, base.ID, mergee.ID))
	updated, err := repo.FindByID(base.AccountID, base.ID)
	require.NoError(t, err)
	assertStoredExactAttributes(t, updated.CustomAttributes)
	for _, expected := range []string{`"mergee_integer":9007199254740997`, `"mergee_decimal":1.0000000000000001`, `"mergee_array":[9007199254740999]`} {
		require.Contains(t, updated.CustomAttributes, expected)
	}
}

func TestStoredCustomAttributeDecoderRejectsTrailingData(t *testing.T) {
	for _, raw := range []string{`{"n":9007199254740993} {"extra":true}`, `{"n":9007199254740993} trailing`, `{"n":9007199254740993`} {
		attrs := map[string]any{"existing": "retained"}
		require.Error(t, unmarshalCustomAttributeObject(raw, &attrs))
		require.Equal(t, map[string]any{"existing": "retained"}, attrs)
	}
}
