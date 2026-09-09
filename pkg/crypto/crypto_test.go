package crypto_test

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/pkg/crypto"
)

func TestCrypto(t *testing.T) {
	t.Run("Password Hashing", func(t *testing.T) {
		pwd := "P@ssw0rd!2026"
		hash, err := crypto.HashPassword(pwd)
		if err != nil {
			t.Fatalf("hash password failed: %v", err)
		}

		if !crypto.CheckPassword(pwd, hash) {
			t.Errorf("expected password to match hash")
		}

		if crypto.CheckPassword("wrong_pwd", hash) {
			t.Errorf("expected wrong password to fail")
		}
	})

	t.Run("Random Tokens", func(t *testing.T) {
		hexStr, err := crypto.RandomHex(16)
		if err != nil || len(hexStr) != 32 {
			t.Errorf("expected 32 hex chars, got %s (err: %v)", hexStr, err)
		}

		b64Str, err := crypto.RandomBase64(16)
		if err != nil || len(b64Str) == 0 {
			t.Errorf("expected base64 string, got %s", b64Str)
		}

		webToken := crypto.GenerateWebsiteToken()
		if len(webToken) != 32 {
			t.Errorf("expected 32-char website token, got %s", webToken)
		}
	})

	t.Run("HMAC-SHA256 Signing and Verification", func(t *testing.T) {
		data := []byte(`{"event":"conversation_created","id":1001}`)
		secret := []byte("super-secret-webhook-key")

		sig := crypto.SignHMACSHA256(data, secret)
		if len(sig) != 64 {
			t.Errorf("expected 64 hex characters for sha256 HMAC, got %d", len(sig))
		}

		if !crypto.VerifyHMACSHA256(data, secret, sig) {
			t.Errorf("expected signature verification to succeed")
		}

		tamperedData := []byte(`{"event":"conversation_created","id":1002}`)
		if crypto.VerifyHMACSHA256(tamperedData, secret, sig) {
			t.Errorf("expected tampered data verification to fail")
		}
	})

	t.Run("AES-256-GCM Encryption and Decryption", func(t *testing.T) {
		key := []byte("01234567890123456789012345678901") // 32 bytes
		original := []byte("Sensitive Shopify Access Token: shpat_123456789abcdef")

		cipherB64, err := crypto.EncryptAESGCM(original, key)
		if err != nil {
			t.Fatalf("AES encryption failed: %v", err)
		}

		decrypted, err := crypto.DecryptAESGCM(cipherB64, key)
		if err != nil {
			t.Fatalf("AES decryption failed: %v", err)
		}

		if string(decrypted) != string(original) {
			t.Errorf("expected decrypted text %s, got %s", string(original), string(decrypted))
		}

		// Decryption with wrong key should fail
		wrongKey := []byte("11234567890123456789012345678901")
		_, err = crypto.DecryptAESGCM(cipherB64, wrongKey)
		if err == nil {
			t.Errorf("expected decryption to fail with wrong key")
		}
	})
}
