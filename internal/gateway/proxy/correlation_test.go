package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
)

func TestCorrelationAndTraceContextPropagation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"request_id":  r.Header.Get("X-Request-Id"),
			"traceparent": r.Header.Get("Traceparent"),
			"tracestate":  r.Header.Get("Tracestate"),
		})
	}))
	defer upstream.Close()

	holder := table.NewHolder()
	_, err := holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-corr",
		Version:       1,
		Pools: []snapshot.UpstreamPool{
			{ID: "p1", Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}}},
		},
		Routes: []snapshot.Route{
			{ID: "r1", UpstreamPoolID: "p1", Host: "corr.local", PathPrefix: "/echo", Methods: []string{"GET"}, Priority: 100, Enabled: true},
		},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DefaultDestinationPolicy(true)
	pol.AllowLoopback = true
	gw := httptest.NewServer(NewEngine(holder, nil, pol))
	defer gw.Close()

	t.Run("preserves valid request-id and W3C traceparent end-to-end", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, gw.URL+"/echo", nil)
		req.Host = "corr.local"
		req.Header.Set("X-Request-Id", "client-req-9988")
		validTP := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		req.Header.Set("Traceparent", validTP)
		req.Header.Set("Tracestate", "vendor=123")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		if got := resp.Header.Get("X-Request-Id"); got != "client-req-9988" {
			t.Fatalf("expected response X-Request-Id=client-req-9988, got %q", got)
		}
		var echoed map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&echoed)
		if echoed["request_id"] != "client-req-9988" || echoed["traceparent"] != validTP || echoed["tracestate"] != "vendor=123" {
			t.Fatalf("unexpected upstream headers: %+v", echoed)
		}
	})

	t.Run("generates safe IDs when client sends invalid/zero traceparent or missing request-id", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, gw.URL+"/echo", nil)
		req.Host = "corr.local"
		req.Header.Set("X-Request-Id", "bad id with spaces!")
		req.Header.Set("Traceparent", "00-00000000000000000000000000000000-0000000000000000-01")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()

		respReqID := resp.Header.Get("X-Request-Id")
		if !strings.HasPrefix(respReqID, "gw_") {
			t.Fatalf("expected generated gw_ request id, got %q", respReqID)
		}
		var echoed map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&echoed)
		if echoed["request_id"] != respReqID {
			t.Fatalf("upstream request_id %q != response %q", echoed["request_id"], respReqID)
		}
		if !traceparentRe.MatchString(echoed["traceparent"]) || strings.Contains(echoed["traceparent"], "00000000000000000000000000000000") {
			t.Fatalf("expected valid generated traceparent, got %q", echoed["traceparent"])
		}
	})
}
