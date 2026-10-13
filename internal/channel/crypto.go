package channel

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strconv"
)

func uintString(v uint) string { return strconv.FormatUint(uint64(v), 10) }

// StableKey encodes fields unambiguously before hashing; no provider text is
// exposed through local key values or journal identifiers.
func StableKey(fields ...string) string {
	encoded, _ := json.Marshal(fields)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func Seal(key []byte, scope, secret string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrNotConfigured
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", ErrNotConfigured
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(secret), []byte(scope))
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Open(key []byte, scope, encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", ErrNotConfigured
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", ErrNotConfigured
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(raw) < gcm.NonceSize() {
		return "", ErrNotConfigured
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], []byte(scope))
	if err != nil || len(plain) == 0 {
		return "", ErrNotConfigured
	}
	return string(plain), nil
}

func VerifyWhatsAppSignature(secret, header string, raw []byte) bool {
	if secret == "" || len(header) != 71 || header[:7] != "sha256=" {
		return false
	}
	actual, err := hex.DecodeString(header[7:])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return hmac.Equal(actual, mac.Sum(nil))
}

func EqualSecret(a, b string) bool { return a != "" && b != "" && hmac.Equal([]byte(a), []byte(b)) }
