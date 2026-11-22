"use client";

import * as React from "react";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import type { TimeSeriesPoint } from "@/lib/api/types";
import { cn } from "@/lib/utils";

export const TIME_RANGES = ["15m", "1h", "6h", "24h"] as const;
export type TimeRange = (typeof TIME_RANGES)[number];

export interface NormalizedChartPoint {
  label: string;
  requests: number;
  errors: number;
  p95Ms: number;
  reqHeightPct: number;
  errHeightPct: number;
  latHeightPct: number;
}

export function formatBucketLabel(isoTimestamp: string, range: TimeRange): string {
  const d = new Date(isoTimestamp);
  if (Number.isNaN(d.getTime())) return isoTimestamp;
  const hh = String(d.getUTCHours()).padStart(2, "0");
  const mm = String(d.getUTCMinutes()).padStart(2, "0");
  if (range === "24h") {
    return `${hh}:00`;
  }
  return `${hh}:${mm}`;
}

export function buildTrafficChartModel(
  points: TimeSeriesPoint[],
  range: TimeRange
): {
  items: NormalizedChartPoint[];
  maxRequests: number;
  maxLatencyMs: number;
  hasTraffic: boolean;
} {
  let maxRequests = 0;
  let maxLatencyMs = 0;
  let totalReqs = 0;

  for (const p of points) {
    totalReqs += p.requests;
    if (p.requests > maxRequests) maxRequests = p.requests;
    if (p.p95_latency_ms > maxLatencyMs) maxLatencyMs = p.p95_latency_ms;
  }

  const reqScale = maxRequests > 0 ? maxRequests : 1;
  const latScale = maxLatencyMs > 0 ? maxLatencyMs : 1;

  const items: NormalizedChartPoint[] = points.map((p) => {
    const errors = p.errors_4xx + p.errors_5xx;
    return {
      label: formatBucketLabel(p.timestamp, range),
      requests: p.requests,
      errors,
      p95Ms: p.p95_latency_ms,
      reqHeightPct: Math.round((p.requests / reqScale) * 100),
      errHeightPct: Math.round((errors / reqScale) * 100),
      latHeightPct: Math.round((p.p95_latency_ms / latScale) * 100),
    };
  });

  return {
    items,
    maxRequests,
    maxLatencyMs,
    hasTraffic: totalReqs > 0,
  };
}

export interface TrafficChartsProps {
  range: TimeRange;
  onRangeChange: (next: TimeRange) => void;
  bucketMinutes: number;
  points: TimeSeriesPoint[];
  loading: boolean;
  error: string | null;
  onRetry?: () => void;
}

export function TrafficCharts({
  range,
  onRangeChange,
  bucketMinutes,
  points,
  loading,
  error,
  onRetry,
}: TrafficChartsProps) {
  const [activeIndex, setActiveIndex] = React.useState<number | null>(null);
  const model = React.useMemo(() => buildTrafficChartModel(points, range), [points, range]);
  const activePoint = activeIndex !== null ? model.items[activeIndex] ?? null : null;

  return (
    <Card>
      <CardHeader className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div>
          <CardTitle>Traffic &amp; Latency Trends</CardTitle>
          <CardDescription>
            {bucketMinutes}-minute UTC buckets across all synced gateway replicas
          </CardDescription>
        </div>

        <div
          role="group"
          aria-label="Select time range"
          className="inline-flex rounded-md border border-[var(--border)] bg-[var(--surface-subtle)] p-0.5"
        >
          {TIME_RANGES.map((r) => (
            <button
              key={r}
              type="button"
              aria-pressed={range === r}
              onClick={() => {
                setActiveIndex(null);
                onRangeChange(r);
              }}
              className={cn(
                "rounded px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                range === r
                  ? "bg-[var(--card)] text-[var(--foreground)] shadow-xs"
                  : "text-[var(--muted-foreground)] hover:text-[var(--foreground)]"
              )}
            >
              {r}
            </button>
          ))}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {error ? (
          <ErrorBanner title="Could not load traffic trends" message={error} onRetry={onRetry} />
        ) : loading ? (
          <Skeleton className="h-52 w-full" />
        ) : !model.hasTraffic ? (
          <EmptyState
            title={`No gateway traffic recorded in the last ${range}`}
            description="Send requests through a published gateway route or expand the time range to inspect traffic and latency trends."
          />
        ) : (
          <div className="space-y-4">
            <div
              className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-[var(--border)] bg-[var(--surface-subtle)] px-3 py-2 text-xs"
              aria-live="polite"
            >
              <div className="flex items-center gap-4">
                <span className="inline-flex items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded-xs bg-[var(--chart-1)]" />
                  Requests (peak {model.maxRequests})
                </span>
                <span className="inline-flex items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded-xs bg-[var(--chart-4)]" />
                  Errors (4xx/5xx)
                </span>
                <span className="inline-flex items-center gap-1.5">
                  <span className="h-2.5 w-2.5 rounded-xs bg-[var(--chart-2)]" />
                  p95 Latency (peak {model.maxLatencyMs.toFixed(0)} ms)
                </span>
              </div>
              <span className="font-mono text-[var(--foreground)] tabular-nums" data-testid="chart-inspection">
                {activePoint
                  ? `${activePoint.label} UTC — ${activePoint.requests} req · ${activePoint.errors} err · p95 ${activePoint.p95Ms.toFixed(1)} ms`
                  : "Hover or focus a bucket to inspect exact values"}
              </span>
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
              {/* Requests & Errors Bar Chart */}
              <div>
                <p className="mb-2 text-xs font-medium text-[var(--muted-foreground)]">
                  Request Volume &amp; Errors (count / bucket)
                </p>
                <div
                  role="img"
                  aria-label={`Request volume chart over ${range} peaking at ${model.maxRequests} requests`}
                  className="flex h-40 items-end gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] p-3"
                >
                  {model.items.map((pt, idx) => (
                    <button
                      key={`req-${idx}`}
                      type="button"
                      onMouseEnter={() => setActiveIndex(idx)}
                      onFocus={() => setActiveIndex(idx)}
                      aria-label={`${pt.label} UTC: ${pt.requests} requests, ${pt.errors} errors`}
                      className="group relative flex h-full flex-1 flex-col justify-end rounded-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                    >
                      <div
                        className="w-full rounded-t-xs bg-[var(--chart-1)] transition-opacity group-hover:opacity-85"
                        style={{ height: `${Math.max(pt.reqHeightPct, pt.requests > 0 ? 6 : 0)}%` }}
                      />
                      {pt.errors > 0 && (
                        <div
                          className="w-full bg-[var(--chart-4)]"
                          style={{ height: `${Math.max(pt.errHeightPct, 4)}%` }}
                        />
                      )}
                    </button>
                  ))}
                </div>
                <div className="mt-1 flex justify-between text-[10px] text-[var(--muted-foreground)] font-mono">
                  <span>{model.items[0]?.label ?? ""} UTC</span>
                  <span>{model.items[model.items.length - 1]?.label ?? ""} UTC</span>
                </div>
              </div>

              {/* p95 Latency Bar Chart */}
              <div>
                <p className="mb-2 text-xs font-medium text-[var(--muted-foreground)]">
                  p95 Latency (ms)
                </p>
                <div
                  role="img"
                  aria-label={`p95 latency chart over ${range} peaking at ${model.maxLatencyMs.toFixed(0)} milliseconds`}
                  className="flex h-40 items-end gap-1 rounded-md border border-[var(--border)] bg-[var(--card)] p-3"
                >
                  {model.items.map((pt, idx) => (
                    <button
                      key={`lat-${idx}`}
                      type="button"
                      onMouseEnter={() => setActiveIndex(idx)}
                      onFocus={() => setActiveIndex(idx)}
                      aria-label={`${pt.label} UTC: p95 latency ${pt.p95Ms.toFixed(1)} ms`}
                      className="group relative flex h-full flex-1 flex-col justify-end rounded-xs focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                    >
                      <div
                        className="w-full rounded-t-xs bg-[var(--chart-2)] transition-opacity group-hover:opacity-85"
                        style={{ height: `${Math.max(pt.latHeightPct, pt.p95Ms > 0 ? 6 : 0)}%` }}
                      />
                    </button>
                  ))}
                </div>
                <div className="mt-1 flex justify-between text-[10px] text-[var(--muted-foreground)] font-mono">
                  <span>{model.items[0]?.label ?? ""} UTC</span>
                  <span>{model.items[model.items.length - 1]?.label ?? ""} UTC</span>
                </div>
              </div>
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
