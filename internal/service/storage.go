package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/uploadpath"
)

var (
	ErrMaliciousFileDetected = errors.New("malicious file signature detected")
	ErrSignedURLExpired      = errors.New("signed URL has expired")
	ErrInvalidSignature      = errors.New("invalid storage signature")
)

// StorageService defines the interface for file storage operations
type StorageService interface {
	Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error)
	Delete(ctx context.Context, key string) error
	GetSignedURL(ctx context.Context, key string, expire time.Duration) (string, error)
	VerifySignedURL(ctx context.Context, key string, expiresAt int64, signature string) bool
	ScanVirus(ctx context.Context, data []byte) error
	CleanExpiredFiles(ctx context.Context, baseDir string, maxAge time.Duration) (int, error)
}

// LocalStorageService provides local filesystem storage with enterprise security features
type LocalStorageService struct {
	baseDir string
	secret  string
}

// NewLocalStorageService creates a new LocalStorageService
func NewLocalStorageService(baseDir string, secret string) *LocalStorageService {
	if baseDir == "" {
		baseDir = "uploads"
	}
	return &LocalStorageService{
		baseDir: baseDir,
		secret:  secret,
	}
}

// Upload writes reader contents to the specified key in the baseDir
func (s *LocalStorageService) Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	cleanKey, err := uploadpath.CanonicalKey(key)
	if err != nil {
		return "", err
	}

	destPath := filepath.Join(s.baseDir, cleanKey)
	destDir := filepath.Dir(destPath)

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create storage directory: %w", err)
	}

	outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to open destination file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, r); err != nil {
		_ = os.Remove(destPath)
		return "", fmt.Errorf("failed to write storage content: %w", err)
	}

	urlPath := url.URL{Path: "/" + strings.TrimPrefix(filepath.ToSlash(destPath), "/")}
	return urlPath.String(), nil
}

// Delete removes the specified key
func (s *LocalStorageService) Delete(ctx context.Context, key string) error {
	cleanKey, err := uploadpath.CanonicalKey(key)
	if err != nil {
		return err
	}
	filePath := filepath.Join(s.baseDir, cleanKey)
	return os.Remove(filePath)
}

// GetSignedURL generates an HMAC-SHA256 signed access URL with expiration
func (s *LocalStorageService) GetSignedURL(ctx context.Context, key string, expire time.Duration) (string, error) {
	if s.secret == "" {
		return "", ErrInvalidSignature
	}
	if expire <= 0 {
		expire = time.Hour
	}
	// Accept the canonical stored URL or a relative key, but reject traversal
	// before signing. Verification takes only exact relative keys, never aliases.
	relKey := key
	base := strings.TrimSuffix(filepath.ToSlash(s.baseDir), "/")
	if strings.HasPrefix(key, "/"+strings.TrimPrefix(base, "/")+"/") {
		relKey = strings.TrimPrefix(key, "/"+strings.TrimPrefix(base, "/")+"/")
	} else if strings.HasPrefix(key, base+"/") {
		relKey = strings.TrimPrefix(key, base+"/")
	}
	relKey, err := uploadpath.CanonicalKey(relKey)
	if err != nil {
		return "", err
	}
	expiresAt := time.Now().Add(expire).Unix()
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(fmt.Sprintf("%s:%d", relKey, expiresAt)))
	signature := hex.EncodeToString(mac.Sum(nil))
	path := "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(s.baseDir, relKey)), "/")
	signed := url.URL{Path: path}
	query := url.Values{"expires": {fmt.Sprint(expiresAt)}, "sig": {signature}}
	signed.RawQuery = query.Encode()
	return signed.String(), nil
}

// VerifySignedURL accepts only the exact storage-relative key the router opens.
// Prefix stripping here would make a signature for x also authorize uploads/x.
func (s *LocalStorageService) VerifySignedURL(ctx context.Context, key string, expiresAt int64, signature string) bool {
	if s.secret == "" || expiresAt <= time.Now().Unix() {
		return false
	}
	if _, err := uploadpath.CanonicalKey(key); err != nil {
		return false
	}
	decoded, err := hex.DecodeString(signature)
	if err != nil || len(decoded) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(fmt.Sprintf("%s:%d", key, expiresAt)))
	return hmac.Equal(decoded, mac.Sum(nil))
}

// ScanVirus scans data for known malicious patterns and signatures
func (s *LocalStorageService) ScanVirus(ctx context.Context, data []byte) error {
	if len(data) == 0 {
		return nil
	}

	// 1. EICAR Standard Antivirus Test string check
	eicar := []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")
	if bytes.Contains(data, eicar) {
		return ErrMaliciousFileDetected
	}

	// 2. PHP/Webshell execution markers check
	webshellMarkers := [][]byte{
		[]byte("<?php"),
		[]byte("eval(base64_decode("),
		[]byte("system($_GET["),
		[]byte("passthru($_POST["),
		[]byte("shell_exec($_REQUEST["),
	}
	for _, m := range webshellMarkers {
		if bytes.Contains(bytes.ToLower(data), bytes.ToLower(m)) {
			return ErrMaliciousFileDetected
		}
	}

	return nil
}

// CleanExpiredFiles walks the directory and deletes files older than maxAge
func (s *LocalStorageService) CleanExpiredFiles(ctx context.Context, targetDir string, maxAge time.Duration) (int, error) {
	if targetDir == "" {
		targetDir = s.baseDir
	}
	if maxAge <= 0 {
		maxAge = 30 * 24 * time.Hour
	}

	cutoff := time.Now().Add(-maxAge)
	deletedCount := 0

	err := filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !info.IsDir() && info.ModTime().Before(cutoff) {
			if removeErr := os.Remove(path); removeErr == nil {
				deletedCount++
				logger.WithComponent("storage").Info("cleaned expired file",
					"path", path, "mod_time", info.ModTime(), "cutoff", cutoff)
			}
		}
		return nil
	})

	return deletedCount, err
}
