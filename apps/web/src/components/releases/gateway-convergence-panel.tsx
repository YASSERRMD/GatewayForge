"use client";

import * as React from "react";
import { AlertTriangle, CheckCircle2, RefreshCw, Server } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { GatewayInstanceStatus } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

interface GatewaysResponse {
  items: GatewayInstanceStatus[];
  expected_version?: number;
  expected_checksum?: string;
  data_available?: boolean;
}

export function GatewayConvergencePanel({ reloadKey = 0 }: { reloadKey?: number }) {
  const { activeOrgId, clearSession } = useAuth();
  const [data, setData] = React.useState<GatewaysResponse | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [tick, setTick] = React.useState(0);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    apiRequest<GatewaysResponse>(`/api/v1/orgs/${activeOrgId}/gateways`, {
      signal: ctrl.signal,
      onUnauthorized,
    })
      .then((res) => setData(res))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load gateway convergence status.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, reloadKey, tick, onUnauthorized]);

  const items = data?.items ?? [];
  const convergedCount = items.filter((g) => g.converged && g.status === "healthy").length;
  const staleOrLagging = items.filter((g) => !g.converged || g.status !== "healthy");

  return (
    <Card>
      <CardHeader className="flex flex-wrap items-start justify-between gap-2">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="flex items-center gap-1.5">
              <Server className="h-4 w-4" aria-hidden="true" />
              Gateway Fleet Convergence
            </CardTitle>
            {data && items.length > 0 && (
              <Badge variant={staleOrLagging.length === 0 ? "success" : "warning"}>
                {convergedCount}/{items.length} Converged
              </Badge>
            )}
          </div>
          <CardDescription>
            Observed per-instance active snapshot version, checksum, and heartbeat freshness.
            {data?.expected_version ? ` Expected active release: v${data.expected_version}.` : ""}
          </CardDescription>
        </div>
        <Button type="button" variant="outline" size="sm" onClick={() => setTick((t) => t + 1)}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
          Refresh Fleet
        </Button>
      </CardHeader>

      <CardContent className="space-y-3">
        {error ? (
          <ErrorBanner title="Convergence Check Unavailable" message={error} onRetry={() => setTick((t) => t + 1)} />
        ) : loading ? (
          <Skeleton className="h-20 w-full" />
        ) : items.length === 0 ? (
          <div
            role="status"
            className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2.5 text-xs text-amber-900"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
            <span>
              No gateway heartbeats observed yet for this organization. Instances may still be starting, polling, or
              disconnected from Redis heartbeats — verify data-plane health cautiously before assuming convergence.
            </span>
          </div>
        ) : (
          <>
            {staleOrLagging.length > 0 ? (
              <div
                role="status"
                className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-900"
              >
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
                <span>
                  {staleOrLagging.length} gateway instance{staleOrLagging.length === 1 ? " is" : "s are"} lagging,
                  stale, or offline. Periodic reconciliation will restore convergence automatically when reachable.
                </span>
              </div>
            ) : (
              <div className="flex items-center gap-2 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-900">
                <CheckCircle2 className="h-4 w-4 text-emerald-700" aria-hidden="true" />
                All {items.length} observed gateway instances are healthy and converged on the active snapshot checksum.
              </div>
            )}

            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Instance ID</TableHead>
                  <TableHead>Heartbeat</TableHead>
                  <TableHead>Observed Version</TableHead>
                  <TableHead>Checksum</TableHead>
                  <TableHead>Convergence</TableHead>
                  <TableHead className="text-right">Heartbeat Age</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((gw) => (
                  <TableRow key={gw.instance_id}>
                    <TableCell className="font-mono text-xs font-medium">{gw.instance_id}</TableCell>
                    <TableCell>
                      <Badge
                        variant={
                          gw.status === "healthy" ? "success" : gw.status === "stale" ? "warning" : "destructive"
                        }
                      >
                        {gw.status}
                      </Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">v{gw.active_version}</TableCell>
                    <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">
                      {gw.checksum ? `${gw.checksum.slice(0, 14)}…` : "—"}
                    </TableCell>
                    <TableCell>
                      <Badge variant={gw.converged ? "success" : "warning"}>
                        {gw.converged ? "Converged" : "Lagging"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right font-mono text-xs tabular-nums text-[var(--muted-foreground)]">
                      {gw.age_sec}s ago
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </>
        )}
      </CardContent>
    </Card>
  );
}
