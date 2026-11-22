package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/mockupstream"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := os.Getenv("MOCK_UPSTREAM_ADDR")
	if addr == "" {
		addr = ":8089"
	}
	instanceID := os.Getenv("MOCK_UPSTREAM_INSTANCE")
	if instanceID == "" {
		instanceID = "mock-upstream-1"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mockupstream.NewHandler(instanceID),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Info("starting mock-upstream", "addr", addr, "instance", instanceID)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("mock-upstream exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("mock-upstream stopped cleanly")
}
