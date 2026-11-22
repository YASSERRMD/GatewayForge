package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
)

func main() {
	logger := redact.NewJSONLogger(os.Stdout, nil)

	appCfg, err := config.LoadGateway(os.LookupEnv)
	if err != nil {
		logger.Error("invalid gateway configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srvCfg := gateway.DefaultServerConfig()
	srvCfg.Addr = appCfg.Addr
	srvCfg.ShutdownTimeout = appCfg.ShutdownTimeout
	srvCfg.AllowMockUpstreams = appCfg.AllowMockUpstreams
	srv := gateway.NewServer(srvCfg)

	var hints *events.HintCoalescer
	if appCfg.RedisAddr != "" {
		rc, rErr := cache.Connect(ctx, appCfg.RedisAddr, appCfg.RedisPassword)
		if rErr != nil {
			logger.Warn("redis unavailable at gateway startup; falling back to periodic snapshot polling", "error", rErr)
		} else {
			defer func() { _ = rc.Close() }()
			hints = events.NewHintCoalescer()
			go events.Subscribe(ctx, rc, events.ChannelConfigChanged, hints, logger)
		}
	}

	if appCfg.ControlPlaneURL != "" && appCfg.GatewaySyncToken != "" {
		poller := syncer.NewPoller(srv.TableHolder(), syncer.PollerConfig{
			ControlPlaneURL: appCfg.ControlPlaneURL,
			SyncToken:       appCfg.GatewaySyncToken,
			LKGPath:         os.Getenv("GATEWAY_LKG_PATH"),
			Interval:        appCfg.SyncInterval,
			Logger:          logger,
			Hints:           hints,
		})
		srv.AttachSyncStatus(poller.Status)
		go poller.Run(ctx)
	}

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
