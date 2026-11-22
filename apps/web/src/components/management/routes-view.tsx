"use client";

import * as React from "react";
import { Eye, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse, RouteItem, ServiceItem, UpstreamPoolItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const PAGE_SIZE = 15;

export interface RoutesLoadedContext {
  routes: RouteItem[];
  services: ServiceItem[];
  pools: UpstreamPoolItem[];
}

export interface RoutesViewProps {
  reloadKey?: number;
  onCreate?: () => void;
  onEdit?: (route: RouteItem) => void;
  onDelete?: (route: RouteItem) => void;
  onDataLoaded?: (ctx: RoutesLoadedContext) => void;
}

export function RoutesView({ reloadKey = 0, onCreate, onEdit, onDelete, onDataLoaded }: RoutesViewProps) {
  const { activeOrgId, activeOrg, clearSession } = useAuth();
  const canWrite = activeOrg?.role !== "viewer";

  const [search, setSearch] = React.useState("");
  const [offset, setOffset] = React.useState(0);
  const [routes, setRoutes] = React.useState<RouteItem[]>([]);
  const [total, setTotal] = React.useState(0);
  const [services, setServices] = React.useState<ServiceItem[]>([]);
  const [pools, setPools] = React.useState<UpstreamPoolItem[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [localTick, setLocalTick] = React.useState(0);
  const [selected, setSelected] = React.useState<RouteItem | null>(null);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    const qs = new URLSearchParams({
      limit: String(PAGE_SIZE),
      offset: String(offset),
    });
    if (search.trim()) {
      qs.set("search", search.trim());
    }

    Promise.all([
      apiRequest<PaginatedResponse<RouteItem>>(`/api/v1/orgs/${activeOrgId}/routes?${qs.toString()}`, {
        signal: ctrl.signal,
        onUnauthorized,
      }),
      apiRequest<PaginatedResponse<ServiceItem>>(`/api/v1/orgs/${activeOrgId}/services?limit=100&offset=0`, {
        signal: ctrl.signal,
        onUnauthorized,
      }).catch(() => ({ items: [] as ServiceItem[], total: 0, limit: 100, offset: 0 })),
      apiRequest<PaginatedResponse<UpstreamPoolItem>>(`/api/v1/orgs/${activeOrgId}/upstreams?limit=100&offset=0`, {
        signal: ctrl.signal,
        onUnauthorized,
      }).catch(() => ({ items: [] as UpstreamPoolItem[], total: 0, limit: 100, offset: 0 })),
    ])
      .then(([routeRes, svcRes, poolRes]) => {
        const nextRoutes = routeRes.items ?? [];
        const nextServices = svcRes.items ?? [];
        const nextPools = poolRes.items ?? [];
        setRoutes(nextRoutes);
        setTotal(routeRes.total ?? 0);
        setServices(nextServices);
        setPools(nextPools);
        onDataLoaded?.({ routes: nextRoutes, services: nextServices, pools: nextPools });
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load routes.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, search, offset, reloadKey, localTick, onUnauthorized, onDataLoaded]);

  const serviceMap = React.useMemo(() => {
    const m = new Map<string, ServiceItem>();
    for (const s of services) m.set(s.id, s);
    return m;
  }, [services]);

  const poolMap = React.useMemo(() => {
    const m = new Map<string, UpstreamPoolItem>();
    for (const p of pools) m.set(p.id, p);
    return m;
  }, [pools]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Routes</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Deterministic precedence table (priority DESC, longest path prefix, tie-break ID) for{" "}
            <span className="font-medium text-[var(--foreground)]">{activeOrg?.org_name}</span>.
          </p>
        </div>
        {canWrite && onCreate && (
          <Button type="button" onClick={onCreate}>
            <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
            New Route
          </Button>
        )}
      </div>

      <div className="relative max-w-md">
        <Search
          className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-[var(--muted-foreground)]"
          aria-hidden="true"
        />
        <Input
          aria-label="Search routes"
          placeholder="Filter by route name, host, or path…"
          value={search}
          onChange={(e) => {
            setOffset(0);
            setSearch(e.target.value);
          }}
          className="pl-9"
        />
      </div>

      {error ? (
        <ErrorBanner title="Could not load routes" message={error} onRetry={() => setLocalTick((n) => n + 1)} />
      ) : loading ? (
        <div className="space-y-2.5">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      ) : routes.length === 0 ? (
        <EmptyState
          title={search.trim() ? "No matching routes" : "No routes configured"}
          description={
            search.trim()
              ? "Try a different filter query."
              : "Attach a host, path prefix, and HTTP methods to a service and upstream pool."
          }
          action={
            canWrite && onCreate && !search.trim() ? (
              <Button type="button" onClick={onCreate}>
                <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
                Create Route
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Priority</TableHead>
                <TableHead>Route &amp; Match</TableHead>
                <TableHead>Methods</TableHead>
                <TableHead>Service / Upstream</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {routes.map((rt) => {
                const svc = serviceMap.get(rt.service_id);
                const pool = poolMap.get(rt.upstream_pool_id);
                return (
                  <TableRow key={rt.id}>
                    <TableCell className="font-mono text-xs tabular-nums">{rt.priority}</TableCell>
                    <TableCell>
                      <p className="font-medium text-[var(--foreground)]">{rt.name}</p>
                      <p className="font-mono text-xs text-[var(--muted-foreground)]">
                        {rt.host}
                        {rt.path_prefix}
                        {rt.strip_prefix ? " (strip)" : ""}
                      </p>
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap gap-1">
                        {rt.methods.map((m) => (
                          <Badge key={m} variant="default">
                            {m}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell>
                      <p className="text-sm text-[var(--foreground)]">{svc?.name ?? rt.service_id}</p>
                      <p className="text-xs text-[var(--muted-foreground)]">
                        Pool: {pool?.name ?? rt.upstream_pool_id}
                      </p>
                    </TableCell>
                    <TableCell>
                      <Badge variant={rt.enabled ? "success" : "default"}>
                        {rt.enabled ? "Enabled" : "Disabled"}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-right">
                      <div className="inline-flex items-center gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={`Inspect ${rt.name}`}
                          onClick={() => setSelected(rt)}
                        >
                          <Eye className="h-3.5 w-3.5" aria-hidden="true" />
                        </Button>
                        {canWrite && onEdit && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            aria-label={`Edit ${rt.name}`}
                            onClick={() => onEdit(rt)}
                          >
                            <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                          </Button>
                        )}
                        {canWrite && onDelete && (
                          <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            aria-label={`Delete ${rt.name}`}
                            onClick={() => onDelete(rt)}
                          >
                            <Trash2 className="h-3.5 w-3.5 text-[var(--destructive)]" aria-hidden="true" />
                          </Button>
                        )}
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>

          <div className="flex items-center justify-between text-xs text-[var(--muted-foreground)]">
            <span className="tabular-nums">
              Showing {offset + 1}–{Math.min(offset + routes.length, total)} of {total}
            </span>
            <div className="flex items-center gap-1.5">
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={offset === 0}
                onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
              >
                Prev
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                disabled={offset + PAGE_SIZE >= total}
                onClick={() => setOffset((o) => o + PAGE_SIZE)}
              >
                Next
              </Button>
            </div>
          </div>
        </>
      )}

      <Dialog
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected ? `Route Details: ${selected.name}` : "Route Details"}
        description="Effective matching semantics and service/upstream attachment."
      >
        {selected && (
          <dl className="grid grid-cols-2 gap-x-4 gap-y-3 text-sm">
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Host</dt>
              <dd className="font-mono">{selected.host}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Path Prefix</dt>
              <dd className="font-mono">{selected.path_prefix}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Priority</dt>
              <dd className="font-mono">{selected.priority}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Strip Prefix</dt>
              <dd>{selected.strip_prefix ? "Yes (strip matched prefix)" : "No (preserve full path)"}</dd>
            </div>
            <div className="col-span-2">
              <dt className="text-xs text-[var(--muted-foreground)]">Allowed HTTP Methods</dt>
              <dd className="mt-1 flex flex-wrap gap-1">
                {selected.methods.map((m) => (
                  <Badge key={m} variant="default">
                    {m}
                  </Badge>
                ))}
              </dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Effective Service</dt>
              <dd>{serviceMap.get(selected.service_id)?.name ?? selected.service_id}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Upstream Pool</dt>
              <dd>{poolMap.get(selected.upstream_pool_id)?.name ?? selected.upstream_pool_id}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Revision</dt>
              <dd className="font-mono">rev-{selected.revision}</dd>
            </div>
            <div>
              <dt className="text-xs text-[var(--muted-foreground)]">Status</dt>
              <dd>{selected.enabled ? "Enabled" : "Disabled"}</dd>
            </div>
          </dl>
        )}
      </Dialog>
    </div>
  );
}
