import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { MetricCards } from "./metric-cards";

describe("MetricCards", () => {
  it("renders authentic telemetry summary values", () => {
    render(
      <MetricCards
        loading={false}
        error={null}
        summary={{
          org_id: "org-1",
          range: "1h",
          window_seconds: 3600,
          total_requests: 1240,
          rps: 0.34,
          errors_4xx: 12,
          errors_5xx: 3,
          auth_denied: 8,
          rate_limited: 4,
          error_rate: 0.0121,
          avg_latency_ms: 14.5,
          p50_latency_ms: 10,
          p95_latency_ms: 50,
          p99_latency_ms: 250,
          active_gateways: 2,
          stale_gateways: 0,
          data_available: true,
        }}
      />
    );
    expect(screen.getByTestId("metric-rps")).toHaveTextContent("0.34 req/s");
    expect(screen.getByTestId("metric-error-rate")).toHaveTextContent("1.21%");
    expect(screen.getByTestId("metric-p95")).toHaveTextContent("50.0 ms");
    expect(screen.getByTestId("metric-gateways")).toHaveTextContent("2");
  });

  it("renders retryable error banner when metrics fail to load", () => {
    const onRetry = vi.fn();
    render(<MetricCards loading={false} error="Metrics API offline" summary={null} onRetry={onRetry} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Metrics API offline");
    fireEvent.click(screen.getByRole("button", { name: /retry/i }));
    expect(onRetry).toHaveBeenCalledTimes(1);
  });
});
