package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/YASSERRMD/GatewayForge/internal/gateway/ratelimit"
	"github.com/YASSERRMD/GatewayForge/internal/gateway/table"
	"github.com/YASSERRMD/GatewayForge/internal/shared/snapshot"
	"github.com/YASSERRMD/GatewayForge/internal/shared/telemetry"
)

func TestGatewayPipelineTracingAndUpstreamContinuity(t *testing.T) {
	memExp := tracetest.NewInMemoryExporter()
	prov, err := telemetry.InitTracing(context.Background(), telemetry.TracingConfig{
		ServiceName: "gatewayforge-gateway",
		Environment: "test",
		SampleRatio: 1.0,
		Exporter:    memExp,
		SyncExport:  true,
	})
	if err != nil {
		t.Fatalf("InitTracing: %v", err)
	}
	defer func() { _ = prov.Shutdown(context.Background()) }()

	var upstreamTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamTraceparent = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	holder := table.NewHolder()
	_, err = holder.Apply(snapshot.Snapshot{
		SchemaVersion: snapshot.SchemaVersion,
		OrgID:         "org-trace",
		Version:       1,
		Pools: []snapshot.UpstreamPool{{
			ID: "p1", ConnectTimeoutMS: 2000, ReadTimeoutMS: 5000,
			Targets: []snapshot.UpstreamTarget{{ID: "t1", URL: upstream.URL, Weight: 100, Enabled: true}},
		}},
		Routes: []snapshot.Route{{
			ID: "r-trace", ServiceID: "svc-trace", Host: "trace.local", PathPrefix: "/ping",
			Methods: []string{"GET"}, UpstreamPoolID: "p1", Enabled: true,
		}},
	}, false)
	if err != nil {
		t.Fatalf("apply snapshot: %v", err)
	}

	pol := DestinationPolicy{AllowLoopback: true}
	eng := NewEngine(holder, NewSafeTransport(DefaultTransportConfig(), pol), pol)
	eng.Tracer = prov.Tracer()
	eng.RateLimit = ratelimit.NewEnforcer(ratelimit.NewLocalLimiter(nil, 100), nil, nil, nil)

	const incomingTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Host = "trace.local"
	req.Header.Set("traceparent", "00-"+incomingTraceID+"-00f067aa0ba902b7-01")
	rec := httptest.NewRecorder()
	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rec.Code)
	}
	if !strings.Contains(upstreamTraceparent, incomingTraceID) {
		t.Fatalf("upstream traceparent %q did not preserve trace ID %s", upstreamTraceparent, incomingTraceID)
	}

	spans := memExp.GetSpans()
	seen := make(map[string]bool)
	for _, sp := range spans {
		seen[sp.Name] = true
		if sp.SpanContext.TraceID().String() != incomingTraceID {
			t.Fatalf("span %s had traceID=%s want %s", sp.Name, sp.SpanContext.TraceID().String(), incomingTraceID)
		}
	}
	for _, want := range []string{"gateway.inbound", "gateway.policy.auth", "gateway.policy.ratelimit", "gateway.upstream.proxy"} {
		if !seen[want] {
			t.Fatalf("missing expected pipeline span %q (seen=%v)", want, seen)
		}
	}
}
