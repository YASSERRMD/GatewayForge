package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
)

func TestAggregateMetricsAPITenantIsolationAndTimeframes(t *testing.T) {
	pool := openIntegrationDB(t)
	resetAndMigrateDB(t, pool)

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6389"
	}
	redisPw := os.Getenv("REDIS_PASSWORD")
	if redisPw == "" {
		redisPw = "gatewayforge_local_redis_pw"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	aggPrefix := fmt.Sprintf("gwf:test:agg:%d:", time.Now().UnixNano())
	hbPrefix := fmt.Sprintf("gwf:test:hb-agg:%d:", time.Now().UnixNano())
	aggStore := telemetry.NewAggregateStore(rc, aggPrefix, time.Hour)
	hbStore := telemetry.NewHeartbeatStore(rc, hbPrefix)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)

	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	orgH := &api.OrgHandler{Store: st, Sessions: sessions, Audit: auditRec}
	telH := &api.TelemetryHandler{Store: st, Sessions: sessions, Heartbeats: hbStore, Aggregates: aggStore}

	srv := httptest.NewServer(api.NewRouter(api.RouterOptions{
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			orgH.MountOrgRoutes(v1)
			telH.MountTelemetryRoutes(v1)
		},
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 5 * time.Second}
	doReq := func(method, path string, body any, cookie *http.Cookie, csrf string) (*http.Response, []byte) {
		var rdr io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rdr = bytes.NewReader(raw)
		}
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, rdr)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		b, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		return r, b
	}

	_, b := doReq(http.MethodPost, "/api/v1/auth/bootstrap", map[string]string{
		"email": "obs@metrics.test", "password": "VeryStrongPassword!99", "display_name": "Obs", "org_name": "OrgA", "org_slug": "org-a",
	}, nil, "")
	var boot struct {
		OrgID string `json:"org_id"`
	}
	_ = json.Unmarshal(b, &boot)

	resp, b := doReq(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "obs@metrics.test", "password": "VeryStrongPassword!99"}, nil, "")
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			cookie = c
		}
	}
	var loginRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(b, &loginRes)

	// Create second organization OrgB.
	resp, b = doReq(http.MethodPost, "/api/v1/orgs", map[string]string{"display_name": "OrgB", "slug": "org-b"}, cookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create orgB=%d %s", resp.StatusCode, b)
	}
	var orgB struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &orgB)

	// Record traffic ONLY for OrgA (8x 200 OK, 1x 401 auth_denied, 1x 504 timeout) + 1 healthy gateway heartbeat.
	now := time.Now().UTC()
	for i := 0; i < 8; i++ {
		_ = aggStore.Record(ctx, telemetry.TrafficSample{Timestamp: now, OrgID: boot.OrgID, Status: 200, Outcome: "ok", DurationMS: 12})
	}
	_ = aggStore.Record(ctx, telemetry.TrafficSample{Timestamp: now, OrgID: boot.OrgID, Status: 401, Outcome: "auth_denied", DurationMS: 3})
	_ = aggStore.Record(ctx, telemetry.TrafficSample{Timestamp: now, OrgID: boot.OrgID, Status: 504, Outcome: "timeout", DurationMS: 450})
	_ = hbStore.Publish(ctx, telemetry.GatewayHeartbeat{InstanceID: "gw-obs-1", OrgID: boot.OrgID, ActiveVersion: 1, Ready: true, LastSeenAt: now})

	// 1. OrgA summary returns exact counts & active gateway = 1.
	resp, b = doReq(http.MethodGet, fmt.Sprintf("/api/v1/orgs/%s/metrics/summary?range=15m", boot.OrgID), nil, cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("orgA summary=%d %s", resp.StatusCode, b)
	}
	var sumA telemetry.MetricsSummary
	_ = json.Unmarshal(b, &sumA)
	if sumA.TotalRequests != 10 || sumA.Errors4xx != 1 || sumA.Errors5xx != 1 || sumA.AuthDenied != 1 || sumA.ActiveGateways != 1 || !sumA.DataAvailable {
		t.Fatalf("unexpected OrgA summary: %+v", sumA)
	}

	// 2. OrgB summary MUST have 0 requests and 0 gateways (strict tenant isolation!).
	resp, b = doReq(http.MethodGet, fmt.Sprintf("/api/v1/orgs/%s/metrics/summary?range=15m", orgB.ID), nil, cookie, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("orgB summary=%d %s", resp.StatusCode, b)
	}
	var sumB telemetry.MetricsSummary
	_ = json.Unmarshal(b, &sumB)
	if sumB.TotalRequests != 0 || sumB.ActiveGateways != 0 {
		t.Fatalf("OrgA metrics leaked into OrgB: %+v", sumB)
	}
}
