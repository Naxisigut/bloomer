package service

import (
	"errors"
	"os"
	"syscall"

	"bloomer/agent/internal/localfs"
)

type Lock struct{ file *os.File }

func AcquireLock(path string) (*Lock, error) {
	f, err := localfs.OpenPrivate(path, syscall.O_RDWR|syscall.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another agent holds the data-directory lock")
	}
	return &Lock{f}, nil
}

// Keep the inode after release: unlinking a flock file creates a second lock domain.
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(err, closeErr)
}
