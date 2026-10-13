//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package uploadpath

import (
	"errors"
	"os"
)

// Refuse to serve private files where a no-follow descriptor walk is unavailable.
func Open(rootDir, key string) (*os.File, error) {
	return nil, errors.New("secure upload file access is unsupported on this platform")
}
