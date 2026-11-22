"use client";

import * as React from "react";
import { Activity, AlertTriangle, Gauge, Server } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorBanner, Skeleton } from "@/components/ui/states";
import type { MetricsSummary } from "@/lib/api/types";

export interface MetricCardsProps {
  summary: MetricsSummary | null;
  loading: boolean;
  error: string | null;
  onRetry?: () => void;
}

export function MetricCards({ summary, loading, error, onRetry }: MetricCardsProps) {
  if (error) {
    return <ErrorBanner title="Metrics unavailable" message={error} onRetry={onRetry} />;
  }

  if (loading || !summary) {
    return (
      <section aria-label="Key telemetry metrics" className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {Array.from({ length: 4 }).map((_, idx) => (
          <Card key={idx}>
            <CardHeader className="pb-2">
              <Skeleton className="h-4 w-28" />
            </CardHeader>
            <CardContent className="space-y-2">
              <Skeleton className="h-8 w-24" />
              <Skeleton className="h-3 w-36" />
            </CardContent>
          </Card>
        ))}
      </section>
    );
  }

  const errorPct = (summary.error_rate * 100).toFixed(2);
  const errorVariant =
    summary.error_rate >= 0.05 ? "destructive" : summary.error_rate >= 0.01 ? "warning" : "success";
  const gwVariant =
    summary.active_gateways > 0 ? "success" : summary.stale_gateways > 0 ? "warning" : "default";

  return (
    <section aria-label="Key telemetry metrics" className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
            Throughput
          </CardTitle>
          <Activity className="h-4 w-4 text-[var(--accent)]" aria-hidden="true" />
        </CardHeader>
        <CardContent>
          <p className="text-2xl font-semibold tabular-nums" data-testid="metric-rps">
            {summary.data_available ? `${summary.rps.toFixed(2)} req/s` : "—"}
          </p>
          <p className="mt-1 text-xs text-[var(--muted-foreground)] tabular-nums">
            {summary.data_available
              ? `${summary.total_requests.toLocaleString()} total requests (${summary.range})`
              : "Telemetry store not connected"}
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
            Error Rate
          </CardTitle>
          <AlertTriangle className="h-4 w-4 text-[var(--muted-foreground)]" aria-hidden="true" />
        </CardHeader>
        <CardContent>
          <div className="flex items-baseline gap-2">
            <p className="text-2xl font-semibold tabular-nums" data-testid="metric-error-rate">
              {summary.data_available ? `${errorPct}%` : "—"}
            </p>
            {summary.data_available && <Badge variant={errorVariant}>{summary.errors_5xx} 5xx</Badge>}
          </div>
          <p className="mt-1 text-xs text-[var(--muted-foreground)] tabular-nums">
            {summary.errors_4xx} 4xx · {summary.auth_denied} auth denied · {summary.rate_limited} limited
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
            p95 Latency
          </CardTitle>
          <Gauge className="h-4 w-4 text-[var(--muted-foreground)]" aria-hidden="true" />
        </CardHeader>
        <CardContent>
          <p className="text-2xl font-semibold tabular-nums" data-testid="metric-p95">
            {summary.data_available ? `${summary.p95_latency_ms.toFixed(1)} ms` : "—"}
          </p>
          <p className="mt-1 text-xs text-[var(--muted-foreground)] tabular-nums">
            avg {summary.avg_latency_ms.toFixed(1)} ms · p50 {summary.p50_latency_ms.toFixed(0)} ms · p99{" "}
            {summary.p99_latency_ms.toFixed(0)} ms
          </p>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
          <CardTitle className="text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
            Active Gateways
          </CardTitle>
          <Server className="h-4 w-4 text-[var(--muted-foreground)]" aria-hidden="true" />
        </CardHeader>
        <CardContent>
          <div className="flex items-baseline gap-2">
            <p className="text-2xl font-semibold tabular-nums" data-testid="metric-gateways">
              {summary.active_gateways}
            </p>
            <Badge variant={gwVariant}>
              {summary.active_gateways > 0 ? "Online" : "No heartbeats"}
            </Badge>
          </div>
          <p className="mt-1 text-xs text-[var(--muted-foreground)] tabular-nums">
            {summary.stale_gateways} stale instance{summary.stale_gateways === 1 ? "" : "s"}
          </p>
        </CardContent>
      </Card>
    </section>
  );
}
