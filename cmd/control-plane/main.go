package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/YASSERRMD/GatewayForge/internal/control"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	appCfg, err := config.LoadControlPlane(os.LookupEnv)
	if err != nil {
		logger.Error("invalid control-plane configuration", "error", err)
		os.Exit(1)
	}

	srvCfg := control.DefaultServerConfig()
	srvCfg.Addr = appCfg.Addr
	srvCfg.ShutdownTimeout = appCfg.ShutdownTimeout
	srv := control.NewServer(srvCfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("starting control-plane",
		"build", buildinfo.ForService("control-plane").String(),
		"config", appCfg.Redacted(),
	)
	if err := srv.Run(ctx); err != nil {
		logger.Error("control-plane exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("control-plane stopped cleanly")
}
