package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control"
	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/buildinfo"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/db"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

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

	var closeDB func()
	if appCfg.DatabaseURL != "" {
		pool, err := db.Connect(ctx, appCfg.DatabaseURL)
		if err != nil {
			logger.Error("failed to connect control-plane to PostgreSQL", "error", err)
			os.Exit(1)
		}
		closeDB = pool.Close
		defer closeDB()

		st := store.New(pool)
		hasher := auth.NewArgon2idHasher(auth.DefaultArgon2idParams())
		bootstrapSvc := auth.NewBootstrapService(pool, appCfg.Env, hasher)
		sessionMgr := auth.NewSessionManager(pool, hasher, 8*time.Hour, appCfg.Env == config.EnvProduction)
		auditRec := api.NewAuditRecorder(st)

		authH := &api.AuthHandler{Bootstrap: bootstrapSvc, Sessions: sessionMgr}
		orgH := &api.OrgHandler{Store: st, Sessions: sessionMgr, Audit: auditRec}
		memH := &api.MembershipHandler{Store: st, Sessions: sessionMgr, Hasher: hasher, Audit: auditRec}
		auditH := &api.AuditHandler{Store: st, Sessions: sessionMgr}

		srvCfg.MountV1 = func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
			memH.MountMembershipRoutes(v1)
			auditH.MountAuditRoutes(v1)
		}

		srv := control.NewServer(srvCfg)
		srv.Health().Register("postgres", func(probeCtx context.Context) error {
			return pool.Ping(probeCtx)
		})

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
