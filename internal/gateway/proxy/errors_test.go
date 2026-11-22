package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestStandardGatewayErrorContracts(t *testing.T) {
	holder := table.NewHolder()
	_, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-err",
		Version:       1,
		Pools: []snapshot.UpstreamPool{
			{ID: "pool-empty", Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: "http://example.com", Weight: 100, Enabled: false}}},
			{ID: "pool-meta", Targets: []snapshot.UpstreamTarget{{ID: "t2", URL: "http://169.254.169.254", Weight: 100, Enabled: true}}},
			{ID: "pool-ok", Targets: []snapshot.UpstreamTarget{{ID: "t3", URL: "http://mock-upstream:8089", Weight: 100, Enabled: true}}},
		},
		Routes: []snapshot.Route{
			{ID: "r-empty", UpstreamPoolID: "pool-empty", Host: "err.local", PathPrefix: "/empty", Methods: []string{"GET"}, Priority: 100, Enabled: true},
			{ID: "r-meta", UpstreamPoolID: "pool-meta", Host: "err.local", PathPrefix: "/meta", Methods: []string{"GET"}, Priority: 100, Enabled: true},
			{ID: "r-timeout", UpstreamPoolID: "pool-ok", Host: "err.local", PathPrefix: "/timeout", Methods: []string{"GET"}, Priority: 100, Enabled: true},
			{ID: "r-badgw", UpstreamPoolID: "pool-ok", Host: "err.local", PathPrefix: "/badgw", Methods: []string{"GET"}, Priority: 100, Enabled: true},
		},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/timeout" {
			return nil, context.DeadlineExceeded
		}
		return nil, errors.New("connection refused")
	})

	eng := NewEngine(holder, rt, DefaultDestinationPolicy(true))

	cases := []struct {
		name       string
		method     string
		host       string
		path       string
		wantStatus int
		wantCode   string
	}{
		{"unmatched route -> 404 route_not_found", http.MethodGet, "err.local", "/missing", http.StatusNotFound, CodeRouteNotFound},
		{"disallowed method -> 405 method_not_allowed", http.MethodDelete, "err.local", "/empty", http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		{"all targets disabled -> 503 upstream_unavailable", http.MethodGet, "err.local", "/empty", http.StatusServiceUnavailable, CodeUpstreamUnavailable},
		{"blocked metadata target -> 502 upstream_blocked", http.MethodGet, "err.local", "/meta", http.StatusBadGateway, CodeUpstreamBlocked},
		{"timeout -> 504 upstream_timeout", http.MethodGet, "err.local", "/timeout", http.StatusGatewayTimeout, CodeUpstreamTimeout},
		{"dial failure -> 502 bad_gateway", http.MethodGet, "err.local", "/badgw", http.StatusBadGateway, CodeBadGateway},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Host = tc.host
			req.Header.Set("X-Request-Id", "req-err-contract")
			rec := httptest.NewRecorder()
			eng.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("got status %d, want %d (body=%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var ge GatewayError
			if err := json.NewDecoder(rec.Body).Decode(&ge); err != nil {
				t.Fatalf("decode GatewayError: %v", err)
			}
			if ge.Code != tc.wantCode || ge.Status != tc.wantStatus || ge.RequestID != "req-err-contract" || ge.TraceID == "" {
				t.Fatalf("unexpected GatewayError contract: %+v", ge)
			}
		})
	}
}
