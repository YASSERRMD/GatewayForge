package integration

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/api"
	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
	"github.com/YASSERRMD/GatewayForge/internal/control/store"
	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/shared/cache"
	"github.com/YASSERRMD/GatewayForge/internal/shared/config"
	"github.com/YASSERRMD/GatewayForge/internal/shared/events"
	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
	"github.com/YASSERRMD/GatewayForge/internal/shared/redact"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func signTestRS256JWT(t *testing.T, priv *rsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal jwt header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestConsumerSecurityTwoReplicaE2E(t *testing.T) {
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
	mockURL := os.Getenv("MOCK_UPSTREAM_URL")
	if mockURL == "" {
		mockURL = "http://127.0.0.1:18089"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	rc, err := cache.Connect(ctx, redisAddr, redisPw)
	if err != nil {
		t.Fatalf("connect redis: %v", err)
	}
	defer func() { _ = rc.Close() }()

	// Local RS256 JWKS server for JWT E2E validation.
	rsaPriv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	jwksSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(rsaPriv.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(rsaPriv.E)).Bytes())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "kid": "e2e-kid-1", "alg": "RS256", "use": "sig", "n": n, "e": e,
			}},
		})
	}))
	defer jwksSrv.Close()

	var logBuf syncBuffer
	safeLogger := redact.NewJSONLogger(&logBuf, nil)

	channel := "gatewayforge:test:consumer-security-e2e"
	eventPub := events.NewPublisher(rc, channel)

	hasher := newTestArgonHasher()
	bootstrap := auth.NewBootstrapService(pool, config.EnvTest, hasher)
	sessions := auth.NewSessionManager(pool, hasher, time.Hour, false)
	st := store.New(pool)
	auditRec := api.NewAuditRecorder(st)
	targetPol := netsec.DefaultTargetPolicy(true)
	targetPol.AllowLoopback = true

	const syncToken = "e2e-security-sync-token-123456"
	authH := &api.AuthHandler{Bootstrap: bootstrap, Sessions: sessions}
	svcH := &api.ServiceHandler{Store: st, Sessions: sessions, Audit: auditRec}
	upH := &api.UpstreamHandler{Store: st, Sessions: sessions, Audit: auditRec, TargetPolicy: targetPol}
	rtH := &api.RouteHandler{Store: st, Sessions: sessions, Audit: auditRec}
	consH := &api.ConsumerHandler{
		Store:    st,
		Sessions: sessions,
		Audit:    auditRec,
		OnKeyChanged: func(req *http.Request, orgID string) {
			_ = eventPub.PublishHint(req.Context(), events.ConfigChangeHint{
				OrgID:       orgID,
				PublishedAt: time.Now().UTC(),
			})
		},
	}
	polH := &api.PolicyHandler{Store: st, Sessions: sessions, Audit: auditRec}
	pubH := &api.PublicationHandler{
		Store:        st,
		Sessions:     sessions,
		Audit:        auditRec,
		TargetPolicy: targetPol,
		OnActivated: func(evCtx context.Context, ev api.ActivationEvent) {
			_ = eventPub.PublishHint(evCtx, events.ConfigChangeHint{
				OrgID:       ev.OrgID,
				Version:     ev.Version,
				Checksum:    ev.Checksum,
				PublishedAt: ev.PublishedAt,
			})
		},
	}
	syncH := &api.SyncHandler{Store: st, SyncTokens: []string{syncToken}}

	cpRouter := api.NewRouter(api.RouterOptions{
		Logger: safeLogger,
		MountV1: func(v1 chi.Router) {
			authH.MountAuthRoutes(v1)
			svcH.MountServiceRoutes(v1)
			upH.MountUpstreamRoutes(v1)
			rtH.MountRouteRoutes(v1)
			consH.MountConsumerRoutes(v1)
			polH.MountPolicyRoutes(v1)
			pubH.MountPublicationRoutes(v1)
			syncH.MountGatewaySyncRoutes(v1)
		},
	})
	cpSrv := httptest.NewServer(cpRouter)
	defer cpSrv.Close()

	// Launch TWO independent gateway replicas.
	startReplica := func(name string) (*gateway.Server, string) {
		gw := gateway.NewServer(gateway.ServerConfig{
			Addr:               "127.0.0.1:0",
			AllowMockUpstreams: true,
			AllowLoopback:      true,
			ShutdownTimeout:    2 * time.Second,
		})
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen %s: %v", name, err)
		}
		hints := events.NewHintCoalescer()
		go events.Subscribe(ctx, rc, channel, hints, safeLogger)
		poller := syncer.NewPoller(gw.TableHolder(), syncer.PollerConfig{
			ControlPlaneURL: cpSrv.URL,
			SyncToken:       syncToken,
			Interval:        200 * time.Millisecond,
			Logger:          safeLogger,
			Hints:           hints,
		})
		gw.AttachSyncStatus(poller.Status)
		go poller.Run(ctx)
		go func() { _ = gw.Serve(ctx, ln) }()
		return gw, "http://" + ln.Addr().String()
	}

	gw1, gw1URL := startReplica("gw-1")
	gw2, gw2URL := startReplica("gw-2")
	replicas := []string{gw1URL, gw2URL}

	// Bootstrap admin + login.
	client := &http.Client{Timeout: 5 * time.Second}
	postCP := func(path string, body any, cookie *http.Cookie, csrf string) (*http.Response, []byte) {
		var rdr io.Reader
		if body != nil {
			raw, _ := json.Marshal(body)
			rdr = bytes.NewReader(raw)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, cpSrv.URL+path, rdr)
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set("X-CSRF-Token", csrf)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp, b
	}

	resp, b := postCP("/api/v1/auth/bootstrap", map[string]string{
		"email": "sec@e2e.test", "password": "VeryStrongPassword!99", "display_name": "Sec Admin", "org_name": "SecOrg", "org_slug": "sec-org",
	}, nil, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap=%d body=%s", resp.StatusCode, b)
	}
	var boot struct {
		OrgID string `json:"org_id"`
	}
	_ = json.Unmarshal(b, &boot)

	resp, b = postCP("/api/v1/auth/login", map[string]string{
		"email": "sec@e2e.test", "password": "VeryStrongPassword!99",
	}, nil, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login=%d body=%s", resp.StatusCode, b)
	}
	var sessCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			sessCookie = c
		}
	}
	var loginRes struct {
		CSRFToken string `json:"csrf_token"`
	}
	_ = json.Unmarshal(b, &loginRes)

	// Create service, upstream pool with target, and 3 routes: public, api-key protected, jwt protected.
	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/services", boot.OrgID), map[string]any{"slug": "orders", "name": "Orders Service"}, sessCookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("service=%d body=%s", resp.StatusCode, b)
	}
	var svc struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &svc)

	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/upstreams", boot.OrgID), map[string]any{
		"name":              "mock-pool",
		"destination_class": "mock_local",
		"targets":           []map[string]any{{"url": mockURL, "weight": 100}},
	}, sessCookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upstream=%d body=%s", resp.StatusCode, b)
	}
	var upPool struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &upPool)

	createRoute := func(name, prefix string) string {
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/routes", boot.OrgID), map[string]any{
			"service_id": svc.ID, "upstream_pool_id": upPool.ID, "name": name,
			"host": "sec.gateway.test", "path_prefix": prefix, "methods": []string{"GET"},
			"strip_prefix": true, "priority": 100, "enabled": true,
		}, sessCookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("create route %s: %d %s", name, r.StatusCode, rb)
		}
		var out struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rb, &out)
		return out.ID
	}

	_ = createRoute("public-route", "/public")
	keyedRouteID := createRoute("keyed-route", "/keyed")
	jwtRouteID := createRoute("jwt-route", "/jwt")

	createAndAttachPolicy := func(name, ptype string, cfg any, routeID string) {
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/policies", boot.OrgID), map[string]any{
			"name": name, "policy_type": ptype, "config": cfg,
		}, sessCookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("create policy %s: %d %s", name, r.StatusCode, rb)
		}
		var p struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(rb, &p)
		r, rb = postCP(fmt.Sprintf("/api/v1/orgs/%s/policies/%s/attachments", boot.OrgID, p.ID), map[string]any{
			"scope_type": "route", "route_id": routeID, "priority": 100,
		}, sessCookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("attach policy %s: %d %s", name, r.StatusCode, rb)
		}
	}

	createAndAttachPolicy("require-api-key", "api_key", map[string]any{
		"header_name": "X-Api-Key", "required_scopes": []string{"orders:read"},
	}, keyedRouteID)
	createAndAttachPolicy("keyed-cors", "cors", map[string]any{
		"allowed_origins":   []string{"https://console.example.com"},
		"allowed_methods":   []string{"GET"},
		"allowed_headers":   []string{"X-Api-Key"},
		"allow_credentials": true,
		"max_age_seconds":   300,
	}, keyedRouteID)
	createAndAttachPolicy("require-jwt", "jwt", map[string]any{
		"issuer":          "https://issuer.e2e.test",
		"audience":        "orders-api",
		"jwks_url":        jwksSrv.URL,
		"allowed_algs":    []string{"RS256"},
		"required_scopes": []string{"billing:read"},
	}, jwtRouteID)

	// Create consumer + issue 3 keys: valid scoped, wrong-scope, expired.
	_, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/consumers", boot.OrgID), map[string]any{
		"name": "partner-app", "owner_email": "partner@e2e.test",
	}, sessCookie, loginRes.CSRFToken)
	var cons struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(b, &cons)

	issueKey := func(label string, scopes []string, exp *time.Time) (string, string) {
		body := map[string]any{"label": label, "scopes": scopes}
		if exp != nil {
			body["expires_at"] = exp.UTC().Format(time.RFC3339)
		}
		r, rb := postCP(fmt.Sprintf("/api/v1/orgs/%s/consumers/%s/keys", boot.OrgID, cons.ID), body, sessCookie, loginRes.CSRFToken)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("issue key %s: %d %s", label, r.StatusCode, rb)
		}
		var out struct {
			ID     string `json:"id"`
			APIKey string `json:"api_key"`
		}
		_ = json.Unmarshal(rb, &out)
		return out.ID, out.APIKey
	}

	past := time.Now().Add(-10 * time.Minute)
	validKeyID, validKey := issueKey("primary", []string{"orders:read"}, nil)
	_, wrongScopeKey := issueKey("wrong-scope", []string{"users:read"}, nil)
	_, expiredKey := issueKey("expired", []string{"orders:read"}, &past)

	// Publish v1 and wait for BOTH replicas to converge.
	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/publications", boot.OrgID), map[string]string{"notes": "sec v1"}, sessCookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("publish v1: %d %s", resp.StatusCode, b)
	}

	waitBothReplicas := func(cond func(*gateway.Server) bool, desc string) {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			if cond(gw1) && cond(gw2) {
				return
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for both replicas: %s", desc)
	}
	waitBothReplicas(func(g *gateway.Server) bool { return g.TableHolder().Current().Version == 1 }, "v1 snapshot")

	callGW := func(baseURL, method, path string, headers map[string]string) (*http.Response, []byte) {
		req, _ := http.NewRequestWithContext(ctx, method, baseURL+path, nil)
		req.Host = "sec.gateway.test"
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatalf("gateway %s %s: %v", method, path, err)
		}
		rb, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		return r, rb
	}

	for idx, gwURL := range replicas {
		// 1. Anonymous on public route -> 200.
		r, _ := callGW(gwURL, http.MethodGet, "/public/echo", nil)
		if r.StatusCode != http.StatusOK {
			t.Fatalf("gw%d public=%d want 200", idx+1, r.StatusCode)
		}
		// 2. Anonymous on keyed route -> 401.
		r, _ = callGW(gwURL, http.MethodGet, "/keyed/echo", nil)
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("gw%d anon keyed=%d want 401", idx+1, r.StatusCode)
		}
		// 3. Valid API key -> 200 and X-Api-Key stripped before upstream!
		r, rb := callGW(gwURL, http.MethodGet, "/keyed/echo", map[string]string{"X-Api-Key": validKey})
		if r.StatusCode != http.StatusOK {
			t.Fatalf("gw%d valid key=%d want 200 body=%s", idx+1, r.StatusCode, rb)
		}
		if bytes.Contains(rb, []byte(validKey)) {
			t.Fatalf("gw%d leaked X-Api-Key to upstream echo!", idx+1)
		}
		// 4. Wrong scope key -> 403.
		r, _ = callGW(gwURL, http.MethodGet, "/keyed/echo", map[string]string{"X-Api-Key": wrongScopeKey})
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("gw%d wrong scope=%d want 403", idx+1, r.StatusCode)
		}
		// 5. Expired key -> 401.
		r, _ = callGW(gwURL, http.MethodGet, "/keyed/echo", map[string]string{"X-Api-Key": expiredKey})
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("gw%d expired key=%d want 401", idx+1, r.StatusCode)
		}
		// 6. CORS preflight without key -> 204 for approved origin, 403 for disallowed origin.
		r, _ = callGW(gwURL, http.MethodOptions, "/keyed/echo", map[string]string{
			"Origin":                         "https://console.example.com",
			"Access-Control-Request-Method":  "GET",
			"Access-Control-Request-Headers": "X-Api-Key",
		})
		if r.StatusCode != http.StatusNoContent || r.Header.Get("Access-Control-Allow-Origin") != "https://console.example.com" {
			t.Fatalf("gw%d valid preflight=%d ACAO=%q", idx+1, r.StatusCode, r.Header.Get("Access-Control-Allow-Origin"))
		}
		r, _ = callGW(gwURL, http.MethodOptions, "/keyed/echo", map[string]string{
			"Origin":                        "https://evil.example.com",
			"Access-Control-Request-Method": "GET",
		})
		if r.StatusCode != http.StatusForbidden {
			t.Fatalf("gw%d evil preflight=%d want 403", idx+1, r.StatusCode)
		}
	}

	// 7. Rotate valid key -> old key becomes 401, replacement becomes 200 on BOTH replicas!
	oldChecksum := gw1.TableHolder().Current().Checksum
	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/keys/%s/rotate", boot.OrgID, validKeyID), nil, sessCookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("rotate key: %d %s", resp.StatusCode, b)
	}
	var rotRes struct {
		Replacement struct {
			ID     string `json:"id"`
			APIKey string `json:"api_key"`
		} `json:"replacement"`
	}
	_ = json.Unmarshal(b, &rotRes)

	waitBothReplicas(func(g *gateway.Server) bool {
		return g.TableHolder().Current().Checksum != oldChecksum
	}, "rotated key checksum convergence")

	for idx, gwURL := range replicas {
		r, _ := callGW(gwURL, http.MethodGet, "/keyed/echo", map[string]string{"X-Api-Key": validKey})
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("gw%d rotated-out key=%d want 401", idx+1, r.StatusCode)
		}
		r, _ = callGW(gwURL, http.MethodGet, "/keyed/echo", map[string]string{"X-Api-Key": rotRes.Replacement.APIKey})
		if r.StatusCode != http.StatusOK {
			t.Fatalf("gw%d replacement key=%d want 200", idx+1, r.StatusCode)
		}
	}

	// 8. Revoke replacement key -> 401 on BOTH replicas!
	rotChecksum := gw1.TableHolder().Current().Checksum
	resp, b = postCP(fmt.Sprintf("/api/v1/orgs/%s/keys/%s/revoke", boot.OrgID, rotRes.Replacement.ID), nil, sessCookie, loginRes.CSRFToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke key: %d %s", resp.StatusCode, b)
	}
	waitBothReplicas(func(g *gateway.Server) bool {
		return g.TableHolder().Current().Checksum != rotChecksum
	}, "revoked key checksum convergence")

	for idx, gwURL := range replicas {
		r, _ := callGW(gwURL, http.MethodGet, "/keyed/echo", map[string]string{"X-Api-Key": rotRes.Replacement.APIKey})
		if r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("gw%d revoked key=%d want 401", idx+1, r.StatusCode)
		}
	}

	// 9. JWT scenarios: valid RS256, tampered signature, wrong audience, alg:none, expired.
	validJWT := signTestRS256JWT(t, rsaPriv,
		map[string]any{"alg": "RS256", "kid": "e2e-kid-1", "typ": "JWT"},
		map[string]any{
			"iss":   "https://issuer.e2e.test",
			"sub":   "jwt-user-42",
			"aud":   "orders-api",
			"scope": "billing:read orders:read",
			"exp":   time.Now().Add(10 * time.Minute).Unix(),
		},
	)
	wrongAudJWT := signTestRS256JWT(t, rsaPriv,
		map[string]any{"alg": "RS256", "kid": "e2e-kid-1", "typ": "JWT"},
		map[string]any{
			"iss":   "https://issuer.e2e.test",
			"sub":   "jwt-user-42",
			"aud":   "wrong-api",
			"scope": "billing:read",
			"exp":   time.Now().Add(10 * time.Minute).Unix(),
		},
	)
	tamperedJWT := validJWT[:len(validJWT)-4] + "AAAA"
	algNoneJWT := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://issuer.e2e.test","aud":"orders-api","scope":"billing:read"}`)) + "."

	for idx, gwURL := range replicas {
		r, rb := callGW(gwURL, http.MethodGet, "/jwt/echo", map[string]string{"Authorization": "Bearer " + validJWT})
		if r.StatusCode != http.StatusOK {
			t.Fatalf("gw%d valid JWT=%d want 200 body=%s", idx+1, r.StatusCode, rb)
		}
		if bytes.Contains(rb, []byte(validJWT)) {
			t.Fatalf("gw%d leaked JWT Authorization header to upstream!", idx+1)
		}
		for name, bad := range map[string]string{
			"tampered":  tamperedJWT,
			"wrong-aud": wrongAudJWT,
			"alg-none":  algNoneJWT,
		} {
			r, _ := callGW(gwURL, http.MethodGet, "/jwt/echo", map[string]string{"Authorization": "Bearer " + bad})
			if r.StatusCode != http.StatusUnauthorized {
				t.Fatalf("gw%d %s JWT=%d want 401", idx+1, name, r.StatusCode)
			}
		}
	}

	// 10. Automated leak scan on captured logs.
	leaks := redact.FindLeaks(logBuf.String(), validKey, wrongScopeKey, expiredKey, rotRes.Replacement.APIKey, validJWT)
	if len(leaks) > 0 {
		t.Fatalf("captured logs leaked credentials: %v", leaks)
	}
}
