"use client";

import * as React from "react";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { MetricCards } from "@/components/dashboard/metric-cards";
import { TrafficCharts, type TimeRange } from "@/components/dashboard/traffic-charts";
import { RecentActivityPanel } from "@/components/dashboard/recent-activity-panel";
import { ServiceSummaryPanel } from "@/components/dashboard/service-summary-panel";
import { ApiError, apiRequest } from "@/lib/api/client";
import type {
  AuditEventItem,
  MetricsSummary,
  PaginatedResponse,
  RouteItem,
  ServiceItem,
  TimeSeriesResponse,
} from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const ACTIVITY_PAGE_SIZE = 6;

export function OverviewDashboard() {
  const { activeOrgId, activeOrg, clearSession } = useAuth();
  const [range, setRange] = React.useState<TimeRange>("1h");
  const [activityOffset, setActivityOffset] = React.useState(0);
  const [reloadTick, setReloadTick] = React.useState(0);

  const [summary, setSummary] = React.useState<MetricsSummary | null>(null);
  const [summaryLoading, setSummaryLoading] = React.useState(true);
  const [summaryError, setSummaryError] = React.useState<string | null>(null);

  const [series, setSeries] = React.useState<TimeSeriesResponse | null>(null);
  const [seriesLoading, setSeriesLoading] = React.useState(true);
  const [seriesError, setSeriesError] = React.useState<string | null>(null);

  const [services, setServices] = React.useState<ServiceItem[]>([]);
  const [routes, setRoutes] = React.useState<RouteItem[]>([]);
  const [catalogLoading, setCatalogLoading] = React.useState(true);
  const [catalogError, setCatalogError] = React.useState<string | null>(null);

  const [events, setEvents] = React.useState<AuditEventItem[]>([]);
  const [eventsTotal, setEventsTotal] = React.useState(0);
  const [eventsLoading, setEventsLoading] = React.useState(true);
  const [eventsError, setEventsError] = React.useState<string | null>(null);

  const onUnauthorized = React.useCallback(() => {
    clearSession(true);
  }, [clearSession]);

  // Fetch metrics summary & time-series when org or range changes.
  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();

    setSummaryLoading(true);
    setSummaryError(null);
    apiRequest<MetricsSummary>(
      `/api/v1/orgs/${activeOrgId}/metrics/summary?range=${range}`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => setSummary(res))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setSummaryError(err instanceof ApiError ? err.friendlyMessage() : "Could not load metrics summary.");
      })
      .finally(() => setSummaryLoading(false));

    setSeriesLoading(true);
    setSeriesError(null);
    apiRequest<TimeSeriesResponse>(
      `/api/v1/orgs/${activeOrgId}/metrics/timeseries?range=${range}`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => setSeries(res))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setSeriesError(err instanceof ApiError ? err.friendlyMessage() : "Could not load time-series data.");
      })
      .finally(() => setSeriesLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, range, reloadTick, onUnauthorized]);

  // Fetch services & routes summary.
  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setCatalogLoading(true);
    setCatalogError(null);

    Promise.all([
      apiRequest<PaginatedResponse<ServiceItem>>(
        `/api/v1/orgs/${activeOrgId}/services?limit=25&offset=0`,
        { signal: ctrl.signal, onUnauthorized }
      ),
      apiRequest<PaginatedResponse<RouteItem>>(
        `/api/v1/orgs/${activeOrgId}/routes?limit=25&offset=0`,
        { signal: ctrl.signal, onUnauthorized }
      ),
    ])
      .then(([svcRes, rtRes]) => {
        setServices(svcRes.items ?? []);
        setRoutes(rtRes.items ?? []);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setCatalogError(err instanceof ApiError ? err.friendlyMessage() : "Could not load services and routes.");
      })
      .finally(() => setCatalogLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, reloadTick, onUnauthorized]);

  // Fetch recent audit activity.
  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setEventsLoading(true);
    setEventsError(null);

    apiRequest<PaginatedResponse<AuditEventItem>>(
      `/api/v1/orgs/${activeOrgId}/audit-events?limit=${ACTIVITY_PAGE_SIZE}&offset=${activityOffset}`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => {
        setEvents(res.items ?? []);
        setEventsTotal(res.total ?? 0);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setEventsError(err instanceof ApiError ? err.friendlyMessage() : "Could not load recent activity.");
      })
      .finally(() => setEventsLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, activityOffset, reloadTick, onUnauthorized]);

  const refreshAll = () => setReloadTick((n) => n + 1);

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">
            Overview
          </h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Live control-plane configuration and gateway traffic telemetry for{" "}
            <span className="font-medium text-[var(--foreground)]">
              {activeOrg?.org_name ?? "your organization"}
            </span>
            .
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={refreshAll}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
          Refresh
        </Button>
      </div>

      <MetricCards
        summary={summary}
        loading={summaryLoading}
        error={summaryError}
        onRetry={refreshAll}
      />

      <TrafficCharts
        range={range}
        onRangeChange={setRange}
        bucketMinutes={series?.bucket_minutes ?? 5}
        points={series?.points ?? []}
        loading={seriesLoading}
        error={seriesError}
        onRetry={refreshAll}
      />

      <div className="grid gap-6 lg:grid-cols-2">
        <ServiceSummaryPanel
          services={services}
          routes={routes}
          loading={catalogLoading}
          error={catalogError}
          onRetry={refreshAll}
        />

        <RecentActivityPanel
          events={events}
          total={eventsTotal}
          limit={ACTIVITY_PAGE_SIZE}
          offset={activityOffset}
          loading={eventsLoading}
          error={eventsError}
          onPageChange={setActivityOffset}
          onRetry={refreshAll}
        />
      </div>
    </div>
  );
}
