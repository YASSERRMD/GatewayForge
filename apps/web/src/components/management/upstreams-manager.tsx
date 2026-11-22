"use client";

import * as React from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { FieldError, Input, Label, Select } from "@/components/ui/input";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse, UpstreamPoolItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

interface PoolHealthDiag {
  pool_id: string;
  pool_name: string;
  destination_class: string;
  status: "healthy" | "unavailable" | string;
  enabled_targets: number;
  disabled_targets: number;
  total_targets: number;
}

interface HealthDiagResponse {
  items: PoolHealthDiag[];
  total: number;
  visibility: string;
}

const WRITE_ROLES = new Set(["owner", "admin", "developer"]);

export function UpstreamsManager() {
  const { activeOrgId, activeOrg, csrfToken, clearSession } = useAuth();
  const canWrite = Boolean(activeOrg && WRITE_ROLES.has(activeOrg.role));

  const [pools, setPools] = React.useState<UpstreamPoolItem[]>([]);
  const [healthByPool, setHealthByPool] = React.useState<Record<string, PoolHealthDiag>>({});
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);

  // Create Pool dialog state
  const [createOpen, setCreateOpen] = React.useState(false);
  const [poolName, setPoolName] = React.useState("");
  const [destClass, setDestClass] = React.useState<"mock_local" | "internal_approved" | "public">("mock_local");
  const [connectTimeout, setConnectTimeout] = React.useState("3000");
  const [readTimeout, setReadTimeout] = React.useState("15000");
  const [initialUrl, setInitialUrl] = React.useState("http://mock-upstream:8089");
  const [initialWeight, setInitialWeight] = React.useState("100");
  const [createErr, setCreateErr] = React.useState<string | null>(null);
  const [createFieldErrs, setCreateFieldErrs] = React.useState<Record<string, string>>({});
  const [saving, setSaving] = React.useState(false);

  // Add Target dialog state
  const [targetPool, setTargetPool] = React.useState<UpstreamPoolItem | null>(null);
  const [targetUrl, setTargetUrl] = React.useState("");
  const [targetWeight, setTargetWeight] = React.useState("100");
  const [targetErr, setTargetErr] = React.useState<string | null>(null);

  // Delete Pool dialog state
  const [deletingPool, setDeletingPool] = React.useState<UpstreamPoolItem | null>(null);
  const [deleteErr, setDeleteErr] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    let mounted = true;
    setLoading(true);
    setError(null);

    Promise.all([
      apiRequest<PaginatedResponse<UpstreamPoolItem>>(
        `/api/v1/orgs/${activeOrgId}/upstreams?limit=50&offset=0`,
        { signal: ctrl.signal, onUnauthorized: () => clearSession(true) },
      ),
      apiRequest<HealthDiagResponse>(`/api/v1/orgs/${activeOrgId}/upstreams/health`, {
        signal: ctrl.signal,
        onUnauthorized: () => clearSession(true),
      }).catch(() => ({ items: [], total: 0, visibility: "aggregated_viewer" })),
    ])
      .then(([poolRes, healthRes]) => {
        if (!mounted) return;
        setPools(poolRes.items ?? []);
        const map: Record<string, PoolHealthDiag> = {};
        for (const h of healthRes.items ?? []) {
          map[h.pool_id] = h;
        }
        setHealthByPool(map);
      })
      .catch((err: unknown) => {
        if (!mounted || (err instanceof DOMException && err.name === "AbortError")) return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load upstream pools.");
      })
      .finally(() => {
        if (mounted) setLoading(false);
      });

    return () => {
      mounted = false;
      ctrl.abort();
    };
  }, [activeOrgId, reloadKey, clearSession]);

  const openCreatePool = () => {
    setPoolName("");
    setDestClass("mock_local");
    setConnectTimeout("3000");
    setReadTimeout("15000");
    setInitialUrl("http://mock-upstream:8089");
    setInitialWeight("100");
    setCreateErr(null);
    setCreateFieldErrs({});
    setCreateOpen(true);
  };

  const handleCreatePool = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId) return;
    setCreateErr(null);
    const fErrs: Record<string, string> = {};

    const trimmedName = poolName.trim();
    const ct = Number.parseInt(connectTimeout, 10);
    const rt = Number.parseInt(readTimeout, 10);
    const wt = Number.parseInt(initialWeight, 10);
    const trimmedUrl = initialUrl.trim();

    if (!trimmedName || trimmedName.length > 120) {
      fErrs.name = "Pool name must be 1–120 characters.";
    }
    if (!Number.isFinite(ct) || ct < 100 || ct > 60000) {
      fErrs.connect_timeout_ms = "Connect timeout must be 100–60000 ms.";
    }
    if (!Number.isFinite(rt) || rt < 100 || rt > 120000) {
      fErrs.read_timeout_ms = "Read timeout must be 100–120000 ms.";
    }
    if (trimmedUrl) {
      if (!trimmedUrl.startsWith("http://") && !trimmedUrl.startsWith("https://")) {
        fErrs.url = "Target URL must start with http:// or https://.";
      }
      if (!Number.isFinite(wt) || wt < 1 || wt > 1000) {
        fErrs.weight = "Target weight must be between 1 and 1000.";
      }
    }

    if (Object.keys(fErrs).length > 0) {
      setCreateFieldErrs(fErrs);
      return;
    }

    setSaving(true);
    try {
      await apiRequest<UpstreamPoolItem>(`/api/v1/orgs/${activeOrgId}/upstreams`, {
        method: "POST",
        csrfToken,
        body: {
          name: trimmedName,
          destination_class: destClass,
          connect_timeout_ms: ct,
          read_timeout_ms: rt,
          targets: trimmedUrl ? [{ url: trimmedUrl, weight: wt, enabled: true }] : [],
        },
        onUnauthorized: () => clearSession(true),
      });
      setCreateOpen(false);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        const mapped: Record<string, string> = {};
        for (const fe of err.fieldErrors) {
          mapped[fe.field] = fe.message;
        }
        setCreateFieldErrs(mapped);
        setCreateErr(
          err.fieldErrors.length > 0
            ? `${err.friendlyMessage()} (${err.fieldErrors.map((f) => `${f.field}: ${f.message}`).join("; ")})`
            : err.friendlyMessage(),
        );
      } else {
        setCreateErr("Unexpected error while creating upstream pool.");
      }
    } finally {
      setSaving(false);
    }
  };

  const handleAddTarget = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId || !targetPool) return;
    setTargetErr(null);

    const trimmedUrl = targetUrl.trim();
    const wt = Number.parseInt(targetWeight, 10);
    if (!trimmedUrl.startsWith("http://") && !trimmedUrl.startsWith("https://")) {
      setTargetErr("Target URL must start with http:// or https://.");
      return;
    }
    if (!Number.isFinite(wt) || wt < 1 || wt > 1000) {
      setTargetErr("Weight must be between 1 and 1000.");
      return;
    }

    setSaving(true);
    try {
      await apiRequest(`/api/v1/orgs/${activeOrgId}/upstreams/${targetPool.id}/targets`, {
        method: "POST",
        csrfToken,
        body: { url: trimmedUrl, weight: wt, enabled: true },
        onUnauthorized: () => clearSession(true),
      });
      setTargetPool(null);
      setTargetUrl("");
      setTargetWeight("100");
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        const details = err.fieldErrors.map((f) => f.message).join("; ");
        setTargetErr(details ? `${err.friendlyMessage()}: ${details}` : err.friendlyMessage());
      } else {
        setTargetErr("Could not add target.");
      }
    } finally {
      setSaving(false);
    }
  };

  const handleRemoveTarget = async (poolId: string, targetId: string) => {
    if (!activeOrgId) return;
    setError(null);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/upstreams/${poolId}/targets/${targetId}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized: () => clearSession(true),
      });
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not remove target.");
    }
  };

  const handleDeletePool = async () => {
    if (!activeOrgId || !deletingPool) return;
    setDeleteErr(null);
    setSaving(true);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/upstreams/${deletingPool.id}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized: () => clearSession(true),
      });
      setDeletingPool(null);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setDeleteErr(err instanceof ApiError ? err.friendlyMessage() : "Could not delete upstream pool.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight text-[var(--foreground)]">Upstreams</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Configure SSRF-validated upstream pools, target weights, timeouts, and observe safe health diagnostics.
          </p>
        </div>
        {canWrite && (
          <Button onClick={openCreatePool}>Create Upstream Pool</Button>
        )}
      </div>

      {error && <ErrorBanner message={error} onRetry={() => setReloadKey((k) => k + 1)} />}

      {loading ? (
        <div className="space-y-3" aria-label="Loading upstream pools">
          <Skeleton className="h-28 w-full" />
          <Skeleton className="h-28 w-full" />
        </div>
      ) : pools.length === 0 ? (
        <EmptyState
          title="No upstream pools configured"
          description="Create an upstream pool with approved targets before attaching routes."
          action={canWrite ? <Button onClick={openCreatePool}>Create Upstream Pool</Button> : undefined}
        />
      ) : (
        <div className="space-y-4">
          {pools.map((pool) => {
            const diag = healthByPool[pool.id];
            const status = diag?.status ?? ((pool.targets?.filter((t) => t.enabled).length ?? 0) > 0 ? "healthy" : "unavailable");
            return (
              <Card key={pool.id}>
                <CardHeader className="flex flex-wrap items-start justify-between gap-4">
                  <div className="space-y-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <CardTitle>{pool.name}</CardTitle>
                      <Badge variant={status === "healthy" ? "success" : "destructive"}>{status}</Badge>
                      <Badge variant="default">{pool.destination_class}</Badge>
                    </div>
                    <CardDescription>
                      Connect {pool.connect_timeout_ms} ms · Read {pool.read_timeout_ms} ms ·{" "}
                      {diag ? `${diag.enabled_targets}/${diag.total_targets} targets enabled` : `${pool.targets?.length ?? 0} targets`}
                    </CardDescription>
                  </div>
                  {canWrite && (
                    <div className="flex items-center gap-2">
                      <Button
                        variant="outline"
                        size="sm"
                        onClick={() => {
                          setTargetErr(null);
                          setTargetUrl("");
                          setTargetWeight("100");
                          setTargetPool(pool);
                        }}
                      >
                        Add Target
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => {
                          setDeleteErr(null);
                          setDeletingPool(pool);
                        }}
                      >
                        Delete Pool
                      </Button>
                    </div>
                  )}
                </CardHeader>
                <CardContent>
                  {!pool.targets || pool.targets.length === 0 ? (
                    <p className="text-sm text-[var(--muted-foreground)]">
                      No targets registered in this pool. Add at least one approved target before publishing routes.
                    </p>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Target URL</TableHead>
                          <TableHead>Weight</TableHead>
                          <TableHead>Status</TableHead>
                          {canWrite && <TableHead className="text-right">Actions</TableHead>}
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {pool.targets.map((t) => (
                          <TableRow key={t.id}>
                            <TableCell className="font-mono text-xs">{t.url}</TableCell>
                            <TableCell>{t.weight}</TableCell>
                            <TableCell>
                              <Badge variant={t.enabled ? "success" : "default"}>
                                {t.enabled ? "Enabled" : "Disabled"}
                              </Badge>
                            </TableCell>
                            {canWrite && (
                              <TableCell className="text-right">
                                <Button
                                  variant="ghost"
                                  size="sm"
                                  onClick={() => handleRemoveTarget(pool.id, t.id)}
                                >
                                  Remove
                                </Button>
                              </TableCell>
                            )}
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      <Dialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Create Upstream Pool"
        description="Targets are validated against the server's SSRF and destination class policy."
      >
        <form onSubmit={handleCreatePool} noValidate className="space-y-4">
          {createErr && <ErrorBanner title="Upstream Policy Rejection" message={createErr} />}

          <div className="space-y-1.5">
            <Label htmlFor="pool-name">Pool Name</Label>
            <Input
              id="pool-name"
              value={poolName}
              onChange={(e) => setPoolName(e.target.value)}
              placeholder="orders-upstream"
            />
            <FieldError message={createFieldErrs.name} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="pool-dest-class">Destination Class</Label>
            <Select
              id="pool-dest-class"
              value={destClass}
              onChange={(e) => setDestClass(e.target.value as "mock_local" | "internal_approved" | "public")}
            >
              <option value="mock_local">mock_local (Compose / local mock targets)</option>
              <option value="internal_approved">internal_approved (Allowlisted internal hosts)</option>
              <option value="public">public (Public HTTPS/HTTP destinations)</option>
            </Select>
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="pool-connect-ms">Connect Timeout (ms)</Label>
              <Input
                id="pool-connect-ms"
                type="number"
                value={connectTimeout}
                onChange={(e) => setConnectTimeout(e.target.value)}
              />
              <FieldError message={createFieldErrs.connect_timeout_ms} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pool-read-ms">Read Timeout (ms)</Label>
              <Input
                id="pool-read-ms"
                type="number"
                value={readTimeout}
                onChange={(e) => setReadTimeout(e.target.value)}
              />
              <FieldError message={createFieldErrs.read_timeout_ms} />
            </div>
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="col-span-2 space-y-1.5">
              <Label htmlFor="pool-initial-url">Initial Target URL</Label>
              <Input
                id="pool-initial-url"
                value={initialUrl}
                onChange={(e) => setInitialUrl(e.target.value)}
                placeholder="http://mock-upstream:8089"
              />
              <FieldError message={createFieldErrs.url || createFieldErrs["targets.url"]} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="pool-initial-weight">Weight (1–1000)</Label>
              <Input
                id="pool-initial-weight"
                type="number"
                value={initialWeight}
                onChange={(e) => setInitialWeight(e.target.value)}
              />
              <FieldError message={createFieldErrs.weight} />
            </div>
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Creating…" : "Create Pool"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={targetPool !== null}
        onClose={() => setTargetPool(null)}
        title={targetPool ? `Add Target to ${targetPool.name}` : "Add Target"}
        description="Private metadata endpoints (e.g. 169.254.169.254) and disallowed hosts are rejected by server-side SSRF validation."
      >
        <form onSubmit={handleAddTarget} noValidate className="space-y-4">
          {targetErr && <ErrorBanner title="Target Rejected" message={targetErr} />}

          <div className="space-y-1.5">
            <Label htmlFor="add-target-url">Target URL</Label>
            <Input
              id="add-target-url"
              value={targetUrl}
              onChange={(e) => setTargetUrl(e.target.value)}
              placeholder="http://mock-upstream:8089"
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="add-target-weight">Weight (1–1000)</Label>
            <Input
              id="add-target-weight"
              type="number"
              value={targetWeight}
              onChange={(e) => setTargetWeight(e.target.value)}
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setTargetPool(null)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Adding…" : "Add Target"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={deletingPool !== null}
        onClose={() => setDeletingPool(null)}
        title="Delete Upstream Pool"
        description={deletingPool ? `Remove "${deletingPool.name}" and its targets?` : undefined}
      >
        <div className="space-y-4">
          {deleteErr && <ErrorBanner title="Cannot delete pool" message={deleteErr} />}
          <p className="text-sm text-[var(--muted-foreground)]">
            Routes referencing this pool must be updated or deleted before removal.
          </p>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setDeletingPool(null)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={saving} onClick={handleDeletePool}>
              {saving ? "Deleting…" : "Confirm Delete"}
            </Button>
          </div>
        </div>
      </Dialog>
    </div>
  );
}
