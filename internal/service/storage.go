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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
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
	if secret == "" {
		secret = "ex-chat-storage-secret-key-2026"
	}
	return &LocalStorageService{
		baseDir: baseDir,
		secret:  secret,
	}
}

// Upload writes reader contents to the specified key in the baseDir
func (s *LocalStorageService) Upload(ctx context.Context, key string, r io.Reader, size int64, contentType string) (string, error) {
	cleanKey := filepath.Clean(key)
	if strings.HasPrefix(cleanKey, "..") {
		return "", errors.New("invalid storage key path traversal")
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

	urlPath := "/" + filepath.ToSlash(destPath)
	return urlPath, nil
}

// Delete removes the specified key
func (s *LocalStorageService) Delete(ctx context.Context, key string) error {
	cleanKey := filepath.Clean(key)
	if strings.HasPrefix(cleanKey, "..") {
		return errors.New("invalid storage key path traversal")
	}
	filePath := filepath.Join(s.baseDir, cleanKey)
	return os.Remove(filePath)
}

// GetSignedURL generates an HMAC-SHA256 signed access URL with expiration
func (s *LocalStorageService) GetSignedURL(ctx context.Context, key string, expire time.Duration) (string, error) {
	if expire <= 0 {
		expire = 1 * time.Hour
	}
	expiresAt := time.Now().Add(expire).Unix()

	relKey := strings.TrimPrefix(filepath.Clean(key), "/")
	if strings.HasPrefix(relKey, s.baseDir+"/") {
		relKey = strings.TrimPrefix(relKey, s.baseDir+"/")
	}

	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(fmt.Sprintf("%s:%d", relKey, expiresAt)))
	signature := hex.EncodeToString(mac.Sum(nil))

	cleanPath := "/" + filepath.ToSlash(filepath.Join(s.baseDir, relKey))
	return fmt.Sprintf("%s?expires=%d&sig=%s", cleanPath, expiresAt, signature), nil
}

// VerifySignedURL validates signature and expiry of a signed access request
func (s *LocalStorageService) VerifySignedURL(ctx context.Context, key string, expiresAt int64, signature string) bool {
	if expiresAt < time.Now().Unix() {
		return false
	}

	relKey := strings.TrimPrefix(filepath.Clean(key), "/")
	if strings.HasPrefix(relKey, s.baseDir+"/") {
		relKey = strings.TrimPrefix(relKey, s.baseDir+"/")
	}

	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(fmt.Sprintf("%s:%d", relKey, expiresAt)))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if hmac.Equal([]byte(signature), []byte(expectedSig)) {
		return true
	}

	// Also check against raw key for backward compatibility
	macRaw := hmac.New(sha256.New, []byte(s.secret))
	macRaw.Write([]byte(fmt.Sprintf("%s:%d", key, expiresAt)))
	return hmac.Equal([]byte(signature), []byte(hex.EncodeToString(macRaw.Sum(nil))))
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
