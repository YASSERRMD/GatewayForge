package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInitTracingResourceAttributesAndGracefulShutdown(t *testing.T) {
	memExp := tracetest.NewInMemoryExporter()
	prov, err := InitTracing(context.Background(), TracingConfig{
		ServiceName:    "gatewayforge-gateway",
		ServiceVersion: "0.9.0",
		Environment:    "test",
		SampleRatio:    1.0,
		Exporter:       memExp,
		SyncExport:     true,
	})
	if err != nil {
		t.Fatalf("InitTracing: %v", err)
	}

	_, span := prov.Tracer().Start(context.Background(), "gateway.request")
	span.SetAttributes(attribute.String("route.id", "rt-orders"))
	span.End()

	spans := memExp.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 exported span, got %d", len(spans))
	}
	if spans[0].Name != "gateway.request" {
		t.Fatalf("span name=%q want gateway.request", spans[0].Name)
	}

	resMap := make(map[string]string)
	for _, kv := range spans[0].Resource.Attributes() {
		resMap[string(kv.Key)] = kv.Value.AsString()
	}
	if resMap["service.name"] != "gatewayforge-gateway" || resMap["deployment.environment.name"] != "test" {
		t.Fatalf("unexpected resource attributes: %+v", resMap)
	}

	if err := prov.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
