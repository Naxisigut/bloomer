package service

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/localfs"
	"bloomer/agent/internal/store"
)

// Preflight validates configuration and credential readability without contacting
// the official endpoint or changing provider state. The installer checks ports.
func Preflight(c config.Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.CollectionEnabled {
		if _, err := config.ReadToken(c.UpstreamCredentialFile); err != nil {
			return errors.New("official credential is missing, unreadable or not private")
		}
	}
	for _, dir := range []string{c.DataDir, c.LogDir} {
		if st, err := os.Stat(dir); err == nil && !st.IsDir() {
			return errors.New("runtime path is not a directory")
		}
	}
	_, _, err := net.SplitHostPort(c.Listen)
	return err
}
func Maintain(ctx context.Context, c config.Config, backup, restore string) error {
	if (backup == "") == (restore == "") {
		return errors.New("choose exactly one maintenance operation")
	}
	if err := localfs.PrivateDir(c.DataDir); err != nil {
		return err
	}
	lock, err := AcquireLock(c.LockPath())
	if err != nil {
		return err
	}
	defer lock.Close()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if restore != "" {
		return store.Restore(ctx, restore, c.DatabasePath())
	}
	db, err := store.Open(ctx, c.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Backup(ctx, backup)
}
