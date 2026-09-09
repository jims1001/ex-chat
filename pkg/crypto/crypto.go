package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword generates a bcrypt hash of the password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("failed to hash password: %w", err)
	}
	return string(bytes), nil
}

// CheckPassword verifies a password against a bcrypt hash
func CheckPassword(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// RandomBytes generates n cryptographically secure random bytes
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(rand.Reader, b)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return b, nil
}

// RandomHex generates a hex-encoded random string of length 2*n
func RandomHex(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RandomBase64 generates a URL-safe base64-encoded random string
func RandomBase64(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// GenerateWebsiteToken generates a 32-character hexadecimal token
func GenerateWebsiteToken() string {
	token, err := RandomHex(16)
	if err != nil {
		return fmt.Sprintf("fallback_%d", randFallback())
	}
	return token
}

func randFallback() int64 {
	b := make([]byte, 8)
	_, _ = io.ReadFull(rand.Reader, b)
	return int64(hex.EncodeToString(b)[0])
}

// SignHMACSHA256 generates a hex-encoded HMAC-SHA256 signature
func SignHMACSHA256(data, key []byte) string {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyHMACSHA256 verifies an HMAC-SHA256 signature using constant-time comparison
func VerifyHMACSHA256(data, key []byte, signatureHex string) bool {
	expectedSig, err := hex.DecodeString(signatureHex)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	actualSig := mac.Sum(nil)
	return hmac.Equal(expectedSig, actualSig)
}

// EncryptAESGCM encrypts data using AES-256-GCM and returns standard Base64 string
func EncryptAESGCM(plaintext []byte, key []byte) (string, error) {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return "", errors.New("crypto: invalid key size for AES (must be 16, 24, or 32 bytes)")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptAESGCM decrypts Base64-encoded AES-256-GCM ciphertext
func DecryptAESGCM(ciphertextBase64 string, key []byte) ([]byte, error) {
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		return nil, errors.New("crypto: invalid key size for AES (must be 16, 24, or 32 bytes)")
	}

	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(ciphertext) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	nonce, actualCiphertext := ciphertext[:nonceSize], ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, actualCiphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt GCM: %w", err)
	}

	return plaintext, nil
}
