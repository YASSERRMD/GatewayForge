package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	appCfg, err := config.LoadGateway(os.LookupEnv)
	if err != nil {
		logger.Error("invalid gateway configuration", "error", err)
		os.Exit(1)
	}

	srvCfg := gateway.DefaultServerConfig()
	srvCfg.Addr = appCfg.Addr
	srvCfg.ShutdownTimeout = appCfg.ShutdownTimeout
	srv := gateway.NewServer(srvCfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	logger.Info("starting gateway",
		"build", buildinfo.ForService("gateway").String(),
		"config", appCfg.Redacted(),
	)
	if err := srv.Run(ctx); err != nil {
		logger.Error("gateway exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("gateway stopped cleanly")
}
