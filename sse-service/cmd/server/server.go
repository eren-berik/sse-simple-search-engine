package main

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const shutdownTimeout = 10 * time.Second

// serve runs the background sync and the HTTP server until ctx is cancelled or the server fails,
// then shuts both down gracefully.
func (a *application) serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.ingest.Run(ctx, a.cfg.SyncInterval)
	}()

	serverErr := make(chan error, 1)
	go func() {
		a.log.Info("http server listening", "addr", a.cfg.HTTPAddr)
		serverErr <- a.http.Listen(a.cfg.HTTPAddr)
	}()

	var runErr error
	select {
	case err := <-serverErr:
		runErr = fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		a.log.Info("shutting down")
		if err := a.http.ShutdownWithTimeout(shutdownTimeout); err != nil {
			a.log.Error("http shutdown", "error", err)
		}
	}

	cancel()
	wg.Wait()
	return runErr
}
