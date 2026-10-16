// Package service owns the process lock, database and bounded shutdown lifecycle.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"bloomer/agent/internal/config"
	"bloomer/agent/internal/httpapi"
	"bloomer/agent/internal/localfs"
	"bloomer/agent/internal/logging"
	"bloomer/agent/internal/store"
)

func Run(ctx context.Context, c config.Config, version string) (result error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := localfs.PrivateDir(c.DataDir); err != nil {
		return fmt.Errorf("prepare data directory: %w", err)
	}
	lock, err := AcquireLock(c.LockPath())
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	logger, logs, err := logging.New(c.LogDir)
	if err != nil {
		return fmt.Errorf("open logs: %w", err)
	}
	defer func() { result = errors.Join(result, logs.Close()) }()
	token, err := config.EnsureAccessToken(c.AccessTokenFile)
	if err != nil {
		return err
	}
	startupCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	db, err := store.Open(startupCtx, c.DatabasePath())
	cancel()
	if err != nil {
		logger.Error("database startup failed")
		return fmt.Errorf("database startup: %w", err)
	}
	defer func() { result = errors.Join(result, db.Close()) }()
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	if err = db.RecordStartup(ctx, time.Now()); err != nil {
		return err
	}
	server := &http.Server{Handler: httpapi.New(db, token, version, c.AccountID), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	logger.Info("agent started", "version", version, "phase", "P1", "listen", c.Listen, "collection_state", "disabled")
	select {
	case <-ctx.Done():
	case err = <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	// Future collectors must stop and finish their transactions before this point.
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err = server.Shutdown(shutdownCtx); err != nil {
		result = errors.Join(result, err)
		_ = server.Close()
	}
	if err = db.RecordShutdown(shutdownCtx, time.Now()); err != nil {
		logger.Error("save shutdown timestamp failed")
		result = errors.Join(result, err)
	}
	logger.Info("agent stopped")
	return result
}
