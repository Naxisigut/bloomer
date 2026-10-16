package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"bloomer/agent/internal/localfs"
)

// EnsureAccessToken creates a distinct dashboard secret when absent. No secret is logged.
func EnsureAccessToken(path string) (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", errors.New("generate access token failed")
	}
	f, err := localfs.OpenPrivate(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err == nil {
		_, err = f.WriteString(hex.EncodeToString(secret) + "\n")
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			os.Remove(path)
			return "", errors.New("save access token failed")
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", errors.New("open access token failed")
	}
	return ReadToken(path)
}

func ReadToken(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("read credential file failed")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return "", errors.New("credential must be a regular file with private permissions (0600)")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return "", errors.New("credential exceeds size limit or cannot be read")
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("credential must contain one token of at least 32 characters")
	}
	return token, nil
}
