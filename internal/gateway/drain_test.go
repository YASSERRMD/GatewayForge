package gateway

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestGracefulConnectionDrainingUnderLoad(t *testing.T) {
	started := make(chan struct{}, 5)
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("drained-ok"))
	}))
	defer upstream.Close()

	srv := NewServer(ServerConfig{
		Addr:               "127.0.0.1:0",
		AllowMockUpstreams: true,
		AllowLoopback:      true,
		ShutdownTimeout:    2 * time.Second,
	})
	if _, err := srv.TableHolder().Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{{
			ID: "r1", Host: "drain.local", PathPrefix: "/work", Methods: []string{"GET"},
			UpstreamPoolID: "p1", Enabled: true,
		}},
	}, false); err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	baseURL := "http://" + ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ctx, ln)
	}()

	// Launch 5 concurrent in-flight requests.
	client := &http.Client{Timeout: 4 * time.Second}
	var completedOK atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, baseURL+"/work", nil)
			req.Host = "drain.local"
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("in-flight request failed during drain: %v", err)
				return
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK && string(body) == "drained-ok" {
				completedOK.Add(1)
			}
		}()
	}

	// Wait until all 5 requests have reached the upstream.
	for i := 0; i < 5; i++ {
		<-started
	}

	// Trigger graceful shutdown while all 5 requests are in flight.
	cancel()
	time.Sleep(40 * time.Millisecond)

	// New connections must be refused once shutdown closes the listener.
	newReq, _ := http.NewRequest(http.MethodGet, baseURL+"/work", nil)
	newReq.Host = "drain.local"
	newClient := &http.Client{Timeout: 300 * time.Millisecond}
	if resp, err := newClient.Do(newReq); err == nil {
		_ = resp.Body.Close()
		t.Fatal("expected new connection to be refused while draining")
	}

	// Release upstream so all 5 in-flight requests drain cleanly.
	close(release)
	wg.Wait()

	if err := <-serveDone; err != nil {
		t.Fatalf("expected clean graceful shutdown, got err=%v", err)
	}
	if completedOK.Load() != 5 {
		t.Fatalf("expected all 5 in-flight requests to complete cleanly, got %d", completedOK.Load())
	}
}

func TestShutdownBoundedGraceForceClosesStuckRequests(t *testing.T) {
	stuck := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-stuck:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(stuck)
		upstream.Close()
	}()

	srv := NewServer(ServerConfig{
		Addr:               "127.0.0.1:0",
		AllowMockUpstreams: true,
		AllowLoopback:      true,
		ShutdownTimeout:    150 * time.Millisecond,
	})
	_, _ = srv.TableHolder().Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 2000, ReadTimeoutMS: 10000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{{
			ID: "r1", Host: "stuck.local", PathPrefix: "/hang", Methods: []string{"GET"},
			UpstreamPoolID: "p1", Enabled: true,
		}},
	}, false)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	baseURL := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, ln) }()

	go func() {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/hang", nil)
		req.Host = "stuck.local"
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}()
	time.Sleep(40 * time.Millisecond)

	start := time.Now()
	cancel()
	<-serveDone
	elapsed := time.Since(start)
	if elapsed > 600*time.Millisecond {
		t.Fatalf("shutdown exceeded bounded grace period: %v", elapsed)
	}
}
