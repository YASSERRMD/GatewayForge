package security

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/gateway"
	syncer "github.com/YASSERRMD/GatewayForge/internal/gateway/sync"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestGracefulShutdownDrainsLargeStreamAndRestoresLKGOnRestart(t *testing.T) {
	payload := bytes.Repeat([]byte("GATEWAYFORGE-DRAIN-STREAM-DATA!!"), 16384) // 512 KiB
	wantSum := sha256.Sum256(payload)
	wantHex := hex.EncodeToString(wantSum[:])

	streamStarted := make(chan struct{}, 1)
	releaseRemainder := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ping" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		// Send first chunk and notify test so shutdown triggers mid-stream
		_, _ = w.Write(payload[:65536])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case streamStarted <- struct{}{}:
		default:
		}
		<-releaseRemainder
		_, _ = w.Write(payload[65536:])
	}))
	defer upstream.Close()

	snap := snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-restart",
		Version:       1,
		PublishedAt:   time.Now().UTC().Truncate(time.Second),
		Pools: []snapshot.UpstreamPool{
			{ID: "p1", Name: "stream-pool", ConnectTimeoutMS: 2000, ReadTimeoutMS: 10000, Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}}},
		},
		Routes: []snapshot.Route{
			{ID: "r1", OrgID: "org-restart", UpstreamPoolID: "p1", Name: "stream", Host: "restart.local", PathPrefix: "/", Methods: []string{"GET"}, Priority: 100, Enabled: true},
		},
	}
	sum, err := snap.ComputeChecksum()
	if err != nil {
		t.Fatalf("compute checksum: %v", err)
	}
	snap.Checksum = sum

	lkgPath := filepath.Join(t.TempDir(), "gateway-lkg.json")
	if err := syncer.SaveLKG(lkgPath, snap); err != nil {
		t.Fatalf("save lkg: %v", err)
	}

	// 1. Start Gateway Instance #1
	srv1 := gateway.NewServer(gateway.ServerConfig{
		Addr:               "127.0.0.1:0",
		AllowMockUpstreams: true,
		AllowLoopback:      true,
		ShutdownTimeout:    3 * time.Second,
	})
	if _, err := srv1.TableHolder().Apply(snap, true); err != nil {
		t.Fatalf("apply snap: %v", err)
	}
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 1: %v", err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	serveErr1 := make(chan error, 1)
	go func() {
		serveErr1 <- srv1.Serve(ctx1, ln1)
	}()

	// Start in-flight 512 KiB download
	type downloadResult struct {
		status int
		sumHex string
		err    error
	}
	dlCh := make(chan downloadResult, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodGet, "http://"+ln1.Addr().String()+"/large", nil)
		req.Host = "restart.local"
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			dlCh <- downloadResult{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		h := sha256.New()
		_, err = io.Copy(h, resp.Body)
		dlCh <- downloadResult{status: resp.StatusCode, sumHex: hex.EncodeToString(h.Sum(nil)), err: err}
	}()

	<-streamStarted
	// Trigger graceful shutdown while 512 KiB stream is mid-flight
	cancel1()
	time.Sleep(40 * time.Millisecond)
	close(releaseRemainder)

	res := <-dlCh
	if res.err != nil || res.status != http.StatusOK || res.sumHex != wantHex {
		t.Fatalf("in-flight large stream failed to drain cleanly: err=%v status=%d gotSum=%s wantSum=%s", res.err, res.status, res.sumHex, wantHex)
	}
	if err := <-serveErr1; err != nil {
		t.Fatalf("gateway 1 shutdown error: %v", err)
	}

	// 2. Restart fresh Gateway Instance #2 with unreachable control plane -> boots from verified LKG file!
	srv2 := gateway.NewServer(gateway.ServerConfig{
		Addr:               "127.0.0.1:0",
		AllowMockUpstreams: true,
		AllowLoopback:      true,
		ShutdownTimeout:    2 * time.Second,
	})
	poller2 := syncer.NewPoller(srv2.TableHolder(), syncer.PollerConfig{
		ControlPlaneURL: "http://127.0.0.1:1", // unreachable control plane
		SyncToken:       "sync-token",
		OrgID:           "org-restart",
		LKGPath:         lkgPath,
	})
	if err := poller2.BootstrapFromLKG(); err != nil {
		t.Fatalf("restarted gateway failed to bootstrap from LKG: %v", err)
	}

	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen 2: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = srv2.Serve(ctx2, ln2) }()

	req2, _ := http.NewRequest(http.MethodGet, "http://"+ln2.Addr().String()+"/ping", nil)
	req2.Host = "restart.local"
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("request to restarted gateway failed: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK || string(body2) != "pong" {
		t.Fatalf("restarted gateway expected 200 pong from LKG, got %d %q", resp2.StatusCode, string(body2))
	}
}
