//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package uploadpath

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// Open walks with O_NOFOLLOW at every component, so even a symlink to another
// contact's file inside uploads cannot cross an authorization boundary. Holding
// directory descriptors also prevents rename/symlink substitution races.
func Open(rootDir, key string) (*os.File, error) {
	if _, err := CanonicalKey(key); err != nil {
		return nil, err
	}
	fd, err := unix.Open(rootDir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(key, "/")
	for i, part := range parts {
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		_ = unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Nlink != 1 {
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		return nil, ErrInvalidPath
	}
	f := os.NewFile(uintptr(fd), key)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err == nil {
			err = ErrInvalidPath
		}
		return nil, err
	}
	return f, nil
}
