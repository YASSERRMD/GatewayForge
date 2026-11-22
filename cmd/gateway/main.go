package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
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
			logger.Warn("redis unavailable at gateway startup; falling back to periodic snapshot polling and fail-mode rate limiting", "error", rErr)
			srv.AttachRateLimiter(ratelimit.NewEnforcer(
				ratelimit.NewRedisLimiter(nil, 250*time.Millisecond),
				nil,
				ratelimit.NewRedisQuotaCounter(nil, 250*time.Millisecond),
				nil,
			))
		} else {
			defer func() { _ = rc.Close() }()
			hints = events.NewHintCoalescer()
			go events.Subscribe(ctx, rc, events.ChannelConfigChanged, hints, logger)
			srv.AttachRateLimiter(ratelimit.NewEnforcer(
				ratelimit.NewRedisLimiter(rc, 250*time.Millisecond),
				nil,
				ratelimit.NewRedisQuotaCounter(rc, 250*time.Millisecond),
				nil,
			))

			// Publish periodic gateway instance heartbeat for control-plane convergence monitoring.
			hbStore := telemetry.NewHeartbeatStore(rc, "")
			instanceID, _ := os.Hostname()
			if instanceID == "" {
				instanceID = "gateway-local"
			}
			buildVer := buildinfo.ForService("gateway").Version
			go func() {
				ticker := time.NewTicker(telemetry.DefaultHBInterval)
				defer ticker.Stop()
				emit := func() {
					cur := srv.TableHolder().Current()
					_ = hbStore.Publish(ctx, telemetry.GatewayHeartbeat{
						InstanceID:    instanceID,
						OrgID:         cur.OrgID,
						ActiveVersion: cur.Version,
						Checksum:      cur.Checksum,
						Ready:         cur.Version > 0,
						BuildVersion:  buildVer,
					})
				}
				emit()
				for {
					select {
					case <-ctx.Done():
						return
					case <-ticker.C:
						emit()
					}
				}
			}()
		}
	} else {
		srv.AttachRateLimiter(ratelimit.NewEnforcer(
			nil,
			ratelimit.NewLocalLimiter(nil, 10000),
			nil,
			ratelimit.NewLocalQuotaCounter(nil, 10000),
		))
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
