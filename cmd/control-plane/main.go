package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"

	"github.com/YASSERRMD/GatewayForge/internal/control"
	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
)

func main() {
	logger := redact.NewJSONLogger(os.Stdout, nil)

	appCfg, err := config.LoadControlPlane(os.LookupEnv)
	if err != nil {
		logger.Error("invalid control-plane configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srvCfg := control.DefaultServerConfig()
	srvCfg.Addr = appCfg.Addr
	srvCfg.ShutdownTimeout = appCfg.ShutdownTimeout
	srvCfg.AllowedCORSOrigins = appCfg.AllowedCORSOrigins
	srvCfg.Logger = logger

	var redisClient *redis.Client
	if appCfg.RedisAddr != "" {
		rc, rErr := cache.Connect(ctx, appCfg.RedisAddr, appCfg.RedisPassword)
		if rErr != nil {
			logger.Warn("redis unavailable at control-plane startup; config hints will rely on gateway polling", "error", rErr)
		} else {
			redisClient = rc
			defer func() { _ = redisClient.Close() }()
		}
	}

	if appCfg.DatabaseURL != "" {
		pool, err := db.Connect(ctx, appCfg.DatabaseURL)
		if err != nil {
			logger.Error("failed to connect control-plane to PostgreSQL", "error", err)
			os.Exit(1)
		}
		defer pool.Close()

		st := store.New(pool)
		hasher := auth.NewArgon2idHasher(auth.DefaultArgon2idParams())
		bootstrapSvc := auth.NewBootstrapService(pool, appCfg.Env, hasher)
		sessionMgr := auth.NewSessionManager(pool, hasher, 8*time.Hour, appCfg.Env == config.EnvProduction)
		auditRec := api.NewAuditRecorder(st)
		targetPol := netsec.DefaultTargetPolicy(appCfg.AllowMockUpstreams)
		eventPub := events.NewPublisher(redisClient, events.ChannelConfigChanged)

		authH := &api.AuthHandler{Bootstrap: bootstrapSvc, Sessions: sessionMgr}
		orgH := &api.OrgHandler{Store: st, Sessions: sessionMgr, Audit: auditRec}
		memH := &api.MembershipHandler{Store: st, Sessions: sessionMgr, Hasher: hasher, Audit: auditRec}
		auditH := &api.AuditHandler{Store: st, Sessions: sessionMgr}
		svcH := &api.ServiceHandler{Store: st, Sessions: sessionMgr, Audit: auditRec}
		upH := &api.UpstreamHandler{Store: st, Sessions: sessionMgr, Audit: auditRec, TargetPolicy: targetPol}
		rtH := &api.RouteHandler{Store: st, Sessions: sessionMgr, Audit: auditRec}
		consH := &api.ConsumerHandler{
			Store:    st,
			Sessions: sessionMgr,
			Audit:    auditRec,
			OnKeyChanged: func(req *http.Request, orgID string) {
				_ = eventPub.PublishHint(req.Context(), events.ConfigChangeHint{
					OrgID:       orgID,
					PublishedAt: time.Now().UTC(),
				})
			},
		}
		polH := &api.PolicyHandler{Store: st, Sessions: sessionMgr, Audit: auditRec}
		pubH := &api.PublicationHandler{
			Store:        st,
			Sessions:     sessionMgr,
			Audit:        auditRec,
			TargetPolicy: targetPol,
			OnActivated: func(evCtx context.Context, ev api.ActivationEvent) {
				if pubErr := eventPub.PublishHint(evCtx, events.ConfigChangeHint{
					OrgID:       ev.OrgID,
					Version:     ev.Version,
					Checksum:    ev.Checksum,
					PublishedAt: ev.PublishedAt,
				}); pubErr != nil {
					logger.Warn("failed to publish Redis config-change hint", "error", pubErr, "version", ev.Version)
				}
			},
		}
		syncH := &api.SyncHandler{
			Store:      st,
			SyncTokens: api.ParseSyncTokens(appCfg.GatewaySyncToken),
		}
		var hbStore *telemetry.HeartbeatStore
		var aggStore *telemetry.AggregateStore
		var corrStore *telemetry.CorrelationStore
		if redisClient != nil {
			hbStore = telemetry.NewHeartbeatStore(redisClient, "")
			aggStore = telemetry.NewAggregateStore(redisClient, "", telemetry.DefaultRetention)
			corrStore = telemetry.NewCorrelationStore(redisClient, "", telemetry.DefaultCorrelationTTL)
		}
		telH := &api.TelemetryHandler{
			Store:              st,
			Sessions:           sessionMgr,
			Heartbeats:         hbStore,
			Aggregates:         aggStore,
			Correlations:       corrStore,
			TraceViewerBaseURL: os.Getenv("TRACE_VIEWER_BASE_URL"),
		}
		consoleH := &api.ConsoleHandler{
			Sessions:       sessionMgr,
			Audit:          auditRec,
			GatewayBaseURL: os.Getenv("GATEWAY_PROXY_URL"),
		}
		openAPIH := &api.OpenAPIHandler{
			Store:    st,
			Sessions: sessionMgr,
			Audit:    auditRec,
		}

		srvCfg.MountV1 = func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
			memH.MountMembershipRoutes(v1)
			auditH.MountAuditRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			consH.MountConsumerRoutes(v1)
			polH.MountPolicyRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
			telH.MountTelemetryRoutes(v1)
			consoleH.MountConsoleRoutes(v1)
			openAPIH.MountOpenAPIRoutes(v1)
		}

		srv := control.NewServer(srvCfg)
		srv.Health().Register("postgres", func(probeCtx context.Context) error {
			return pool.Ping(probeCtx)
		})
		if redisClient != nil {
			srv.Health().Register("redis", func(probeCtx context.Context) error {
				return redisClient.Ping(probeCtx).Err()
			})
		}

		logger.Info("starting control-plane",
			"build", buildinfo.ForService("control-plane").String(),
			"config", appCfg.Redacted(),
		)
		if err := srv.Run(ctx); err != nil {
			logger.Error("control-plane exited with error", "error", err)
			os.Exit(1)
		}
		logger.Info("control-plane stopped cleanly")
		return
	}

	srv := control.NewServer(srvCfg)
	logger.Info("starting control-plane (stateless mode without DATABASE_URL)",
		"build", buildinfo.ForService("control-plane").String(),
		"config", appCfg.Redacted(),
	)
	if err := srv.Run(ctx); err != nil {
		logger.Error("control-plane exited with error", "error", err)
		os.Exit(1)
	}
	logger.Info("control-plane stopped cleanly")
}
