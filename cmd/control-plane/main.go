package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/YASSERRMD/GatewayForge/internal/control"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := control.DefaultServerConfig()
	srv := control.NewServer(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("starting control-plane", "build", buildinfo.ForService("control-plane").String(), "addr", cfg.Addr)
	if err := srv.Run(ctx); err != nil {
		logger.Error("control-plane exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("control-plane stopped cleanly")
}
