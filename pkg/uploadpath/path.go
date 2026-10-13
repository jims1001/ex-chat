// Package uploadpath defines the canonical names used by private local uploads.
package uploadpath

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
)

var ErrInvalidPath = errors.New("invalid upload path")

// CanonicalKey rejects aliases rather than cleaning them into another owner's
// path. A key is relative to uploads and uses slash separators on every OS.
func CanonicalKey(key string) (string, error) {
	if key == "" || key == "." || path.IsAbs(key) || path.Clean(key) != key || strings.ContainsAny(key, "\\?#") || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return "", ErrInvalidPath
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidPath
		}
	}
	return key, nil
}

func NamespaceID(part, prefix string) (uint, error) {
	if !strings.HasPrefix(part, prefix) {
		return 0, ErrInvalidPath
	}
	digits := strings.TrimPrefix(part, prefix)
	n, err := strconv.ParseUint(digits, 10, strconv.IntSize)
	if err != nil || n == 0 || strconv.FormatUint(n, 10) != digits {
		return 0, ErrInvalidPath
	}
	return uint(n), nil
}

func WidgetKey(inboxID, contactID uint, filename string) (string, error) {
	if inboxID == 0 || contactID == 0 || strings.Contains(filename, "/") {
		return "", ErrInvalidPath
	}
	if _, err := CanonicalKey(filename); err != nil {
		return "", err
	}
	return fmt.Sprintf("widget/inbox_%d/contact_%d/%s", inboxID, contactID, filename), nil
}

// LocalURLKey recognizes only an origin-relative local upload URL. Absolute
// URLs cannot establish ownership even when their suffix looks like an upload.
func LocalURLKey(value string) (string, bool, error) {
	u, err := url.Parse(value)
	if err != nil {
		return "", false, err
	}
	if u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/uploads/") {
		return "", false, nil
	}
	if u.Fragment != "" {
		return "", true, ErrInvalidPath
	}
	key, err := CanonicalKey(strings.TrimPrefix(u.Path, "/uploads/"))
	return key, true, err
}

// URL returns the escaped origin-relative representation used in metadata.
func URL(key string) string {
	return (&url.URL{Path: "/uploads/" + key}).String()
}
