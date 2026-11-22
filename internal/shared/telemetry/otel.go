package telemetry

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// TracingConfig configures OpenTelemetry resource attributes, sampling, and export target.
type TracingConfig struct {
	ServiceName    string
	ServiceVersion string
	Environment    string
	OTLPEndpoint   string                // e.g. "http://jaeger:4318" or empty for local no-op export
	SampleRatio    float64               // 0.0..1.0 (default 1.0 in dev/test, 0.1 in prod)
	Exporter       sdktrace.SpanExporter // optional explicit exporter (e.g. tracetest.InMemoryExporter)
	SyncExport     bool                  // use SimpleSpanProcessor for deterministic tests
}

// Provider wraps a configured OpenTelemetry TracerProvider with graceful shutdown.
type Provider struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

// InitTracing builds a resource-tagged OpenTelemetry TracerProvider and installs W3C TraceContext propagation.
func InitTracing(ctx context.Context, cfg TracingConfig) (*Provider, error) {
	if strings.TrimSpace(cfg.ServiceName) == "" {
		cfg.ServiceName = "gatewayforge"
	}
	if strings.TrimSpace(cfg.Environment) == "" {
		cfg.Environment = "development"
	}
	if cfg.SampleRatio <= 0 || cfg.SampleRatio > 1.0 {
		if cfg.Environment == "production" {
			cfg.SampleRatio = 0.1
		} else {
			cfg.SampleRatio = 1.0
		}
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			attribute.String("deployment.environment.name", cfg.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	var exp sdktrace.SpanExporter
	switch {
	case cfg.Exporter != nil:
		exp = cfg.Exporter
	case strings.TrimSpace(cfg.OTLPEndpoint) != "":
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(strings.TrimSpace(cfg.OTLPEndpoint)),
		}
		if strings.HasPrefix(strings.TrimSpace(cfg.OTLPEndpoint), "http://") {
			opts = append(opts, otlptracehttp.WithInsecure())
		}
		exp, err = otlptracehttp.New(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("create otlp trace exporter: %w", err)
		}
	}

	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRatio))),
	}
	if exp != nil {
		if cfg.SyncExport {
			tpOpts = append(tpOpts, sdktrace.WithSyncer(exp))
		} else {
			tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
		}
	}

	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Provider{
		tp:     tp,
		tracer: tp.Tracer(cfg.ServiceName),
	}, nil
}

// Tracer returns the configured OpenTelemetry tracer for this service.
func (p *Provider) Tracer() trace.Tracer {
	if p == nil || p.tracer == nil {
		return otel.Tracer("gatewayforge")
	}
	return p.tracer
}

// Shutdown flushes queued spans and closes the underlying TracerProvider cleanly.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || p.tp == nil {
		return nil
	}
	return p.tp.Shutdown(ctx)
}
