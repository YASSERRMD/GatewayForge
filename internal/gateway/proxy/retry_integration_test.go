package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/policy"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestEngineRetriesIdempotentGETAndNeverRetriesPOST(t *testing.T) {
	var getAttempts, postAttempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			n := getAttempts.Add(1)
			if n < 3 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"recovered":true}`))
			return
		}
		postAttempts.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	retryCfg, _ := json.Marshal(policy.RetryPolicy{
		MaxAttempts:      3,
		BaseBackoffMS:    10,
		MaxBackoffMS:     50,
		RetryableMethods: []string{"GET"},
	})
	holder := table.NewHolder()
	_, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 1000, ReadTimeoutMS: 2000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{{
			ID: "r1", Host: "api.local", PathPrefix: "/orders", Methods: []string{"GET", "POST"},
			UpstreamPoolID: "p1", Enabled: true,
			Policies: []snapshot.AttachedPolicy{{ID: "pol-r", PolicyType: "retry", ScopeType: "route", Config: retryCfg}},
		}},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DestinationPolicy{AllowLoopback: true}
	eng := NewEngine(holder, NewSafeTransport(DefaultTransportConfig(), pol), pol)

	// GET fails twice with 503 and succeeds on 3rd attempt -> returns 200!
	getReq := httptest.NewRequest(http.MethodGet, "/orders", nil)
	getReq.Host = "api.local"
	getRec := httptest.NewRecorder()
	eng.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || getAttempts.Load() != 3 {
		t.Fatalf("GET expected 200 after 3 attempts, got code=%d attempts=%d", getRec.Code, getAttempts.Load())
	}

	// POST receives 503 -> MUST NEVER be retried (attempts == 1)!
	postReq := httptest.NewRequest(http.MethodPost, "/orders", bytes.NewBufferString(`{"item":"book"}`))
	postReq.Host = "api.local"
	postRec := httptest.NewRecorder()
	eng.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusServiceUnavailable || postAttempts.Load() != 1 {
		t.Fatalf("POST must NOT be retried: code=%d attempts=%d", postRec.Code, postAttempts.Load())
	}
}
