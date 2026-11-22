import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { buildTrafficChartModel, TrafficCharts } from "./traffic-charts";

describe("TrafficCharts", () => {
  it("transforms time-series buckets accurately and detects empty ranges", () => {
    const empty = buildTrafficChartModel(
      [
        {
          timestamp: "2026-11-22T10:00:00Z",
          requests: 0,
          errors_4xx: 0,
          errors_5xx: 0,
          auth_denied: 0,
          rate_limited: 0,
          avg_latency_ms: 0,
          p95_latency_ms: 0,
        },
      ],
      "1h"
    );
    expect(empty.hasTraffic).toBe(false);

    const populated = buildTrafficChartModel(
      [
        {
          timestamp: "2026-11-22T10:00:00Z",
          requests: 50,
          errors_4xx: 5,
          errors_5xx: 0,
          auth_denied: 0,
          rate_limited: 0,
          avg_latency_ms: 12,
          p95_latency_ms: 50,
        },
        {
          timestamp: "2026-11-22T10:05:00Z",
          requests: 100,
          errors_4xx: 0,
          errors_5xx: 10,
          auth_denied: 0,
          rate_limited: 0,
          avg_latency_ms: 25,
          p95_latency_ms: 100,
        },
      ],
      "1h"
    );
    expect(populated.hasTraffic).toBe(true);
    expect(populated.maxRequests).toBe(100);
    expect(populated.items[0]?.reqHeightPct).toBe(50);
    expect(populated.items[1]?.reqHeightPct).toBe(100);
    expect(populated.items[1]?.label).toBe("10:05");
  });

  it("renders empty-range state and switches ranges via accessible buttons", () => {
    const onRangeChange = vi.fn();
    render(
      <TrafficCharts
        range="1h"
        onRangeChange={onRangeChange}
        bucketMinutes={5}
        points={[]}
        loading={false}
        error={null}
      />
    );
    expect(screen.getByText(/no gateway traffic recorded/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "15m" }));
    expect(onRangeChange).toHaveBeenCalledWith("15m");
  });
});
