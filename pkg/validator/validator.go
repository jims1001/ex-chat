package validator

import (
	"html"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

var (
	// emailRegex matches standard RFC 5322 compliant emails
	emailRegex = regexp.MustCompile(`^[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$`)

	// phoneRegex matches international E.164 phone numbers or common local numbers
	phoneRegex = regexp.MustCompile(`^\+?[1-9]\d{1,14}$`)

	// uuidRegex matches standard UUID v4 or other valid hex UUID formats
	uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

// IsValidEmail checks if email matches valid address format
func IsValidEmail(email string) bool {
	email = strings.TrimSpace(email)
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	return emailRegex.MatchString(email)
}

// NormalizeEmail trims spaces and converts email to lowercase
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IsValidURL checks if string is a valid absolute HTTP or HTTPS URL
func IsValidURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// IsValidPhone checks if string is a valid E.164 phone number
func IsValidPhone(phone string) bool {
	phone = strings.ReplaceAll(phone, " ", "")
	phone = strings.ReplaceAll(phone, "-", "")
	phone = strings.ReplaceAll(phone, "(", "")
	phone = strings.ReplaceAll(phone, ")", "")
	return phoneRegex.MatchString(phone)
}

// IsValidUUID checks if string matches standard UUID format
func IsValidUUID(u string) bool {
	return uuidRegex.MatchString(strings.TrimSpace(u))
}

// IsStrongPassword validates if password contains at least 8 chars, 1 uppercase, 1 lowercase, and 1 digit
func IsStrongPassword(password string) (bool, string) {
	if len(password) < 8 {
		return false, "Password must be at least 8 characters long"
	}
	var hasUpper, hasLower, hasDigit bool
	for _, c := range password {
		switch {
		case unicode.IsUpper(c):
			hasUpper = true
		case unicode.IsLower(c):
			hasLower = true
		case unicode.IsDigit(c):
			hasDigit = true
		}
	}
	if !hasUpper {
		return false, "Password must contain at least one uppercase letter"
	}
	if !hasLower {
		return false, "Password must contain at least one lowercase letter"
	}
	if !hasDigit {
		return false, "Password must contain at least one number"
	}
	return true, ""
}

// SanitizeText escapes HTML tags and prevents script injection
func SanitizeText(input string) string {
	return html.EscapeString(strings.TrimSpace(input))
}
