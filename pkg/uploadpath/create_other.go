//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package uploadpath

import (
	"errors"
	"os"
)

func CreateExclusive(rootDir, key string) (*os.File, func(bool) error, error) {
	return nil, nil, errors.New("secure upload file creation is unsupported on this platform")
}
