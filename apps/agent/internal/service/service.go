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

	"bloomer/agent/internal/alert"
	"bloomer/agent/internal/collector"
	"bloomer/agent/internal/config"
	"bloomer/agent/internal/control"
	"bloomer/agent/internal/health"
	"bloomer/agent/internal/httpapi"
	"bloomer/agent/internal/localfs"
	"bloomer/agent/internal/logging"
	"bloomer/agent/internal/retention"
	"bloomer/agent/internal/store"
	"bloomer/agent/internal/telemetry"
	"bloomer/agent/internal/upstream"
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
	state, err := db.LoadState(ctx, c.AccountID)
	if err != nil {
		return fmt.Errorf("restore collection state: %w", err)
	}
	if err = db.RecordStartup(ctx, time.Now()); err != nil {
		return err
	}
	runtime := health.New()
	if state.LastError != "" {
		runtime.Collection("degraded", state.LastError, state.RecoveryReason)
	}
	if state.CleanupError != "" {
		runtime.Cleanup("failed", state.CleanupError)
	}
	client, err := upstream.NewClient(c.UpstreamURL, c.UpstreamCredentialFile, nil)
	if err != nil {
		return err
	}
	identityClient, err := upstream.NewIdentityClient(c.UpstreamURL, nil)
	if err != nil {
		return err
	}
	clock := WallClock{}
	var collect roundWorker = &collector.Collector{Store: db, Source: client, AccountID: c.AccountID, Now: clock.Now, Runtime: runtime, Logger: logger}
	if !c.CollectionEnabled {
		collect = disabledCollector{}
		runtime.Collection("disabled", "", "")
	}
	cleaner := &retention.Cleaner{Store: db, AccountID: c.AccountID, Now: clock.Now, Runtime: runtime, Logger: logger}
	controls, err := control.New(ctx, c.DataDir, c.DarkbloomBinary, c.RemoteControlEnabled)
	if err != nil {
		logger.Error("remote control history unavailable")
	} else {
		defer controls.Close()
	}
	alerts := alert.New(db, c.Alert, logger)
	handler := httpapi.New(db, token, version, c.AccountID, runtime).WithControl(controls).WithAlerts(alerts)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	serverErrors := make(chan error, 1)
	go func() { serverErrors <- server.Serve(listener) }()
	jobsCtx, cancelJobs := context.WithCancel(ctx)
	defer cancelJobs()
	alertsDone := make(chan struct{})
	go func() { defer close(alertsDone); alerts.Run(jobsCtx) }()
	jobsDone := make(chan struct{})
	go func() {
		defer close(jobsDone)
		Scheduler{Collector: collect, Cleaner: cleaner, Clock: clock}.Run(jobsCtx)
	}()
	deviceDone := make(chan struct{})
	go func() {
		defer close(deviceDone)
		if c.DeviceStateEnabled {
			sampler := telemetry.NewSampler(c.ProviderStateFile, c.DataDir)
			sampler.LatestEarnings = deviceEarningsSource(db, identityClient)
			runDeviceCollection(jobsCtx, sampler, db, clock, logger, alerts.Evaluate)
		}
	}()
	logger.Info("agent started", "version", version, "phase", "P6", "listen", c.Listen, "collection_enabled", c.CollectionEnabled, "device_state_enabled", c.DeviceStateEnabled)
	select {
	case <-ctx.Done():
	case err = <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	// Cancel network/maintenance work and wait for transaction commit/rollback
	// before persisting shutdown or closing the database.
	cancelJobs()
	<-jobsDone
	<-deviceDone
	<-alertsDone
	shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err = server.Shutdown(shutdownCtx); err != nil {
		result = errors.Join(result, err)
		_ = server.Close()
	}
	checkpointCtx, checkpointCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer checkpointCancel()
	if err = db.RecordShutdown(checkpointCtx, time.Now()); err != nil {
		logger.Error("save shutdown timestamp failed")
		result = errors.Join(result, err)
	}
	logger.Info("agent stopped")
	return result
}

type disabledCollector struct{}

func (disabledCollector) RunOnce(context.Context) (collector.Result, error) {
	return collector.Result{Skipped: true}, nil
}
