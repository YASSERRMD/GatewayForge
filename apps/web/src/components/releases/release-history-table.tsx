"use client";

import * as React from "react";
import { ChevronLeft, ChevronRight, RotateCcw, Zap } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const PAGE_SIZE = 10;

export interface PublicationRecord {
  id: string;
  org_id: string;
  version: number;
  draft_revision: number;
  checksum: string;
  notes: string;
  published_by?: string | null;
  published_at: string;
  is_active: boolean;
  activation_revision?: number;
}

export interface ReleaseHistoryTableProps {
  reloadKey?: number;
  onActivate?: (pub: PublicationRecord, activeVersion: number, activeActivationRev: number) => void;
  onRollback?: (pub: PublicationRecord, activeVersion: number, activeActivationRev: number) => void;
}

export function ReleaseHistoryTable({ reloadKey = 0, onActivate, onRollback }: ReleaseHistoryTableProps) {
  const { activeOrgId, activeOrg, clearSession } = useAuth();
  const canOperateReleases = activeOrg?.role === "owner" || activeOrg?.role === "admin";

  const [items, setItems] = React.useState<PublicationRecord[]>([]);
  const [total, setTotal] = React.useState(0);
  const [offset, setOffset] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [localTick, setLocalTick] = React.useState(0);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    apiRequest<PaginatedResponse<PublicationRecord>>(
      `/api/v1/orgs/${activeOrgId}/publications?limit=${PAGE_SIZE}&offset=${offset}`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => {
        setItems(res.items ?? []);
        setTotal(res.total ?? 0);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load release history.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, offset, reloadKey, localTick, onUnauthorized]);

  const activePub = items.find((p) => p.is_active);
  const activeVersion = activePub?.version ?? 0;
  const activeActRev = activePub?.activation_revision ?? 0;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Immutable Release History</CardTitle>
        <CardDescription>
          Every published snapshot is immutable and checksum-verified. Activations and rollbacks create audited release events.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {error ? (
          <ErrorBanner
            title="Could not load release history"
            message={error}
            onRetry={() => setLocalTick((t) => t + 1)}
          />
        ) : loading ? (
          <div className="space-y-2">
            <Skeleton className="h-12 w-full" />
            <Skeleton className="h-12 w-full" />
          </div>
        ) : items.length === 0 ? (
          <EmptyState
            title="No releases published yet"
            description="Validate your draft configuration above and publish release v1 to distribute traffic config to gateways."
          />
        ) : (
          <>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Version</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Checksum (SHA-256)</TableHead>
                  <TableHead>Release Notes</TableHead>
                  <TableHead>Published At</TableHead>
                  {canOperateReleases && <TableHead className="text-right">Actions</TableHead>}
                </TableRow>
              </TableHeader>
              <TableBody>
                {items.map((pub) => {
                  const isOlderThanActive = activeVersion > 0 && pub.version < activeVersion;
                  return (
                    <TableRow key={pub.id}>
                      <TableCell className="font-mono font-semibold">v{pub.version}</TableCell>
                      <TableCell>
                        <Badge variant={pub.is_active ? "success" : "default"}>
                          {pub.is_active ? "Active" : "Immutable"}
                        </Badge>
                      </TableCell>
                      <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">
                        {pub.checksum.slice(0, 16)}…
                      </TableCell>
                      <TableCell className="max-w-xs truncate text-sm text-[var(--foreground)]">
                        {pub.notes || "—"}
                      </TableCell>
                      <TableCell className="text-xs text-[var(--muted-foreground)]">
                        {new Date(pub.published_at).toLocaleString()}
                      </TableCell>
                      {canOperateReleases && (
                        <TableCell className="text-right">
                          {pub.is_active ? (
                            <span className="text-xs text-[var(--muted-foreground)]">Live</span>
                          ) : isOlderThanActive && onRollback ? (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              aria-label={`Rollback to v${pub.version}`}
                              onClick={() => onRollback(pub, activeVersion, activeActRev)}
                            >
                              <RotateCcw className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
                              Rollback to v{pub.version}
                            </Button>
                          ) : onActivate ? (
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              aria-label={`Activate v${pub.version}`}
                              onClick={() => onActivate(pub, activeVersion, activeActRev)}
                            >
                              <Zap className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
                              Activate v{pub.version}
                            </Button>
                          ) : null}
                        </TableCell>
                      )}
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>

            <div className="flex items-center justify-between text-xs text-[var(--muted-foreground)]">
              <span className="tabular-nums">
                Showing {offset + 1}–{Math.min(offset + items.length, total)} of {total}
              </span>
              <div className="flex items-center gap-1.5">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={offset === 0}
                  onClick={() => setOffset((o) => Math.max(0, o - PAGE_SIZE))}
                >
                  <ChevronLeft className="h-3.5 w-3.5" aria-hidden="true" />
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
                  <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}
