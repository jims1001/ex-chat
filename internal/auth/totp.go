package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"time"
)

const (
	totpTimeStep = 30
	totpDigits   = 6
)

// GenerateTOTPSecret generates a secure random 16-byte base32 secret
func GenerateTOTPSecret() (string, error) {
	bytes := make([]byte, 10)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes), nil
}

// GenerateTOTPCode calculates the 6-digit TOTP code for a secret at a specific time
func GenerateTOTPCode(secret string, t time.Time) (string, error) {
	cleanSecret := strings.ToUpper(strings.TrimSpace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(cleanSecret)
	if err != nil {
		return "", fmt.Errorf("invalid base32 secret: %w", err)
	}

	counter := uint64(t.Unix() / totpTimeStep)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(buf)
	h := mac.Sum(nil)

	offset := h[len(h)-1] & 0x0f
	code := binary.BigEndian.Uint32(h[offset:offset+4]) & 0x7fffffff
	code = code % uint32(math.Pow10(totpDigits))

	return fmt.Sprintf("%06d", code), nil
}

// VerifyTOTPCode verifies a 6-digit code against secret with +/- 1 time-step clock drift allowance
func VerifyTOTPCode(secret, code string) bool {
	cleanCode := strings.TrimSpace(code)
	if len(cleanCode) != totpDigits {
		return false
	}

	now := time.Now()
	// Check intervals: t-1, t, t+1
	for _, offset := range []time.Duration{-totpTimeStep * time.Second, 0, totpTimeStep * time.Second} {
		expected, err := GenerateTOTPCode(secret, now.Add(offset))
		if err == nil && expected == cleanCode {
			return true
		}
	}
	return false
}

// GenerateBackupCodes generates n secure 8-digit numeric backup codes
func GenerateBackupCodes(count int) ([]string, error) {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		num := binary.BigEndian.Uint32(b) % 100000000
		codes[i] = fmt.Sprintf("%08d", num)
	}
	return codes, nil
}

