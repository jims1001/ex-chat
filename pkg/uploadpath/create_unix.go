//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package uploadpath

import (
	"errors"
	"os"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// CreateExclusive creates a private file without following symlinks at any
// component or replacing an existing file. finish must be called: true keeps
// the file; false unlinks it through the held parent descriptor. It is idempotent.
func CreateExclusive(rootDir, key string) (*os.File, func(bool) error, error) {
	if _, err := CanonicalKey(key); err != nil {
		return nil, nil, err
	}
	// The configured root's parent is trusted configuration. The root itself must
	// be a real directory, not a symlink (even to another path inside uploads).
	if err := os.Mkdir(rootDir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, nil, err
	}
	fd, err := unix.Open(rootDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	parts := strings.Split(key, "/")
	for _, part := range parts[:len(parts)-1] {
		if err := unix.Mkdirat(fd, part, 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			unix.Close(fd)
			return nil, nil, err
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if err != nil {
			return nil, nil, err
		}
		fd = next
	}
	name := parts[len(parts)-1]
	fileFD, err := unix.Openat(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		unix.Close(fd)
		return nil, nil, err
	}
	var once sync.Once
	var finishErr error
	finish := func(keep bool) error {
		once.Do(func() {
			if !keep {
				finishErr = unix.Unlinkat(fd, name, 0)
			}
			closeErr := unix.Close(fd)
			if finishErr == nil {
				finishErr = closeErr
			}
		})
		return finishErr
	}
	return os.NewFile(uintptr(fileFD), key), finish, nil
}
