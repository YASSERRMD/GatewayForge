package proxy

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafeTransportConnectionReuseAndShutdown(t *testing.T) {
	var newConns atomic.Int32
	var closedConns atomic.Int32

	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	}))
	ts.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		switch state {
		case http.StateNew:
			newConns.Add(1)
		case http.StateClosed:
			closedConns.Add(1)
		}
	}
	ts.Start()
	defer ts.Close()

	pol := DefaultDestinationPolicy(true)
	pol.AllowLoopback = true
	tr := NewSafeTransport(DefaultTransportConfig(), pol)
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	for i := 0; i < 5; i++ {
		resp, err := client.Get(ts.URL)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	if got := newConns.Load(); got != 1 {
		t.Fatalf("expected 1 pooled TCP connection reused across 5 requests, got %d", got)
	}

	// Closing idle connections must drop the pooled connection cleanly.
	tr.CloseIdleConnections()
	deadline := time.Now().Add(500 * time.Millisecond)
	for closedConns.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if closedConns.Load() == 0 {
		t.Fatal("expected CloseIdleConnections to close pooled idle TCP connection")
	}
}

func TestSafeTransportBlocksLoopbackByDefaultAtDialTime(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Production policy forbids loopback even at dial time.
	tr := NewSafeTransport(DefaultTransportConfig(), DefaultDestinationPolicy(false))
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}

	_, err := client.Get(ts.URL)
	if !errors.Is(err, ErrBlockedDestination) {
		t.Fatalf("expected ErrBlockedDestination at dial time, got: %v", err)
	}
}
