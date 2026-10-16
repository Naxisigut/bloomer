// Package localfs manages private runtime files. Supported targets are macOS and Linux.
package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

func PrivateDir(path string) error {
	if !filepath.IsAbs(path) || path == string(filepath.Separator) {
		return errors.New("invalid private directory")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return errors.New("private directory must be a directory, not a symlink")
	}
	return os.Chmod(path, 0700)
}

// OpenPrivate rejects symlinks and non-regular files and restricts existing permissions.
func OpenPrivate(path string, flags int) (*os.File, error) {
	fd, err := syscall.Open(path, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("runtime file must be regular")
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
