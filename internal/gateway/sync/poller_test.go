package syncer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func makeSignedSnap(version int, path string) snapshot.Snapshot {
	s := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-1",
		Version:       version,
		PublishedAt:   time.Now().UTC().Truncate(time.Second),
		Pools: []snapshot.UpstreamPool{
			{ID: "p1", Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: "http://mock-upstream:8089", Weight: 100, Enabled: true}}},
		},
		Routes: []snapshot.Route{
			{ID: "r1", UpstreamPoolID: "p1", Host: "api.local", PathPrefix: path, Methods: []string{"GET"}, Priority: 100, Enabled: true},
		},
	}
	s.Checksum, _ = s.ComputeChecksum()
	return s
}

func TestPollerReconcilesWithoutRedis(t *testing.T) {
	var currentSnap atomic.Pointer[snapshot.Snapshot]
	v1 := makeSignedSnap(1, "/v1")
	currentSnap.Store(&v1)

	cp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-sync-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		snap := currentSnap.Load()
		etag := `"` + snap.Checksum + `"`
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snap)
	}))
	defer cp.Close()

	holder := table.NewHolder()
	poller := NewPoller(holder, PollerConfig{
		ControlPlaneURL: cp.URL,
		SyncToken:       "test-sync-token",
		Interval:        40 * time.Millisecond,
		Logger:          slog.New(slog.NewJSONHandler(io.Discard, nil)),
		Hints:           nil, // Simulate Redis completely down / no pub-sub hints!
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go poller.Run(ctx)

	// Wait for initial v1 sync.
	deadline := time.Now().Add(2 * time.Second)
	for holder.Current().Version != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if holder.Current().Version != 1 || !holder.Current().Matcher.Match("api.local", "GET", "/v1").Matched {
		t.Fatalf("expected v1 active, got version=%d", holder.Current().Version)
	}

	// Publish v2 WITHOUT any Redis event: periodic reconciliation MUST pick it up automatically!
	v2 := makeSignedSnap(2, "/v2")
	currentSnap.Store(&v2)

	deadline = time.Now().Add(2 * time.Second)
	for holder.Current().Version != 2 && time.Now().Before(deadline) {
		time.Sleep(15 * time.Millisecond)
	}
	if holder.Current().Version != 2 || !holder.Current().Matcher.Match("api.local", "GET", "/v2").Matched {
		t.Fatalf("expected periodic reconciliation to apply v2 with Redis down, got version=%d", holder.Current().Version)
	}
}
