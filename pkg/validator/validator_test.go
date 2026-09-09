package validator_test

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/pkg/validator"
)

func TestValidator(t *testing.T) {
	t.Run("Email Validation", func(t *testing.T) {
		validEmails := []string{
			"support@example.com",
			"john.doe+chat@sub.domain.org",
			"admin@localhost.net",
		}
		for _, em := range validEmails {
			if !validator.IsValidEmail(em) {
				t.Errorf("expected %s to be valid email", em)
			}
		}

		invalidEmails := []string{
			"plainaddress",
			"@missingusername.com",
			"username@.com",
			"",
		}
		for _, em := range invalidEmails {
			if validator.IsValidEmail(em) {
				t.Errorf("expected %s to be invalid email", em)
			}
		}

		if validator.NormalizeEmail(" User@Example.COM ") != "user@example.com" {
			t.Errorf("failed to normalize email properly")
		}
	})

	t.Run("URL Validation", func(t *testing.T) {
		if !validator.IsValidURL("https://api.chatwoot.app/v1") {
			t.Errorf("expected https URL to be valid")
		}
		if !validator.IsValidURL("http://localhost:8080/webhook") {
			t.Errorf("expected http localhost URL to be valid")
		}
		if validator.IsValidURL("ftp://not-supported.com") {
			t.Errorf("expected ftp URL to be invalid")
		}
		if validator.IsValidURL("not a url") {
			t.Errorf("expected plain text to be invalid")
		}
	})

	t.Run("Phone Validation", func(t *testing.T) {
		if !validator.IsValidPhone("+14155552671") {
			t.Errorf("expected +14155552671 to be valid")
		}
		if !validator.IsValidPhone("+86 138 0000 0000") {
			t.Errorf("expected +86 formatted phone to be valid")
		}
		if validator.IsValidPhone("abc") {
			t.Errorf("expected abc to be invalid phone")
		}
	})

	t.Run("Strong Password Validation", func(t *testing.T) {
		ok, _ := validator.IsStrongPassword("StrongP@ssw0rd")
		if !ok {
			t.Errorf("expected StrongP@ssw0rd to be strong")
		}

		ok, reason := validator.IsStrongPassword("weak")
		if ok || reason == "" {
			t.Errorf("expected weak to fail length check")
		}

		ok, _ = validator.IsStrongPassword("alllowercase123")
		if ok {
			t.Errorf("expected missing uppercase to fail")
		}
	})

	t.Run("Text Sanitization", func(t *testing.T) {
		unsafe := "<script>alert('xss')</script> Hello & Welcome "
		safe := validator.SanitizeText(unsafe)
		if safe != "&lt;script&gt;alert(&#39;xss&#39;)&lt;/script&gt; Hello &amp; Welcome" {
			t.Errorf("unexpected sanitized output: %s", safe)
		}
	})
}
