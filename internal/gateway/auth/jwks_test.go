package gwauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/netsec"
)

func TestJWKSCacheRotationOutageAndSSRF(t *testing.T) {
	key1, _ := rsa.GenerateKey(rand.Reader, 2048)
	key2, _ := rsa.GenerateKey(rand.Reader, 2048)

	var phase atomic.Int32 // 0=key1, 1=key2 rotated, 2=outage 503
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch phase.Load() {
		case 0:
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{MarshalRSAPublicJWK("k1", &key1.PublicKey)}})
		case 1:
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{MarshalRSAPublicJWK("k2", &key2.PublicKey)}})
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	pol := netsec.DefaultTargetPolicy(true)
	pol.AllowLoopback = true
	cache := NewJWKSCache(nil, pol, 1*time.Minute)
	cache.now = func() time.Time { return now }

	// 1. SSRF metadata JWKS URL MUST be blocked!
	if _, err := cache.GetKey(context.Background(), "http://169.254.169.254/jwks.json", "k1"); err == nil {
		t.Fatal("expected SSRF block on metadata JWKS URL")
	}

	// 2. Fetch key k1 cleanly.
	k1, err := cache.GetKey(context.Background(), srv.URL, "k1")
	if err != nil || k1 == nil {
		t.Fatalf("expected k1 fetch success, got err=%v", err)
	}

	// 3. Key rotation: switch JWKS server to k2, advance past refresh cooldown -> k2 resolves!
	phase.Store(1)
	now = now.Add(15 * time.Second)
	k2, err := cache.GetKey(context.Background(), srv.URL, "k2")
	if err != nil || k2 == nil {
		t.Fatalf("expected rotated k2 fetch success, got err=%v", err)
	}

	// 4. Outage after TTL expiry fails closed safely.
	phase.Store(2)
	now = now.Add(5 * time.Minute)
	if _, err := cache.GetKey(context.Background(), srv.URL, "k2"); err == nil {
		t.Fatal("expected fail-closed error when JWKS is down and cache expired")
	}
}
