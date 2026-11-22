"use client";

import * as React from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { FieldError, Input, Label, Select } from "@/components/ui/input";
import { ErrorBanner } from "@/components/ui/states";
import { RoutesView, type RoutesLoadedContext } from "@/components/management/routes-view";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { RouteItem, ServiceItem, UpstreamPoolItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const ALL_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

function normalizePath(p: string): string {
  const trimmed = p.trim().replace(/\/+$/, "");
  return trimmed === "" ? "/" : trimmed;
}

export function findRouteConflicts(
  existing: RouteItem[],
  candidate: { id?: string; host: string; path_prefix: string; priority: number; methods: string[]; enabled: boolean }
): RouteItem[] {
  if (!candidate.enabled) return [];
  const normHost = candidate.host.trim().toLowerCase();
  const normPath = normalizePath(candidate.path_prefix);
  const candMethods = new Set(candidate.methods.map((m) => m.toUpperCase()));
  if (!normHost || candMethods.size === 0) return [];

  return existing.filter((rt) => {
    if (!rt.enabled) return false;
    if (candidate.id && rt.id === candidate.id) return false;
    if (rt.host.trim().toLowerCase() !== normHost) return false;
    if (normalizePath(rt.path_prefix) !== normPath) return false;
    if (rt.priority !== candidate.priority) return false;
    return rt.methods.some((m) => candMethods.has(m.toUpperCase()));
  });
}

export function RoutesManager() {
  const { activeOrgId, csrfToken, clearSession } = useAuth();
  const [reloadKey, setReloadKey] = React.useState(0);
  const [routes, setRoutes] = React.useState<RouteItem[]>([]);
  const [services, setServices] = React.useState<ServiceItem[]>([]);
  const [pools, setPools] = React.useState<UpstreamPoolItem[]>([]);

  const [editorOpen, setEditorOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<RouteItem | null>(null);
  const [name, setName] = React.useState("");
  const [serviceId, setServiceId] = React.useState("");
  const [poolId, setPoolId] = React.useState("");
  const [host, setHost] = React.useState("api.gatewayforge.local");
  const [pathPrefix, setPathPrefix] = React.useState("/v1/");
  const [methods, setMethods] = React.useState<string[]>(["GET"]);
  const [priority, setPriority] = React.useState("100");
  const [stripPrefix, setStripPrefix] = React.useState(false);
  const [enabled, setEnabled] = React.useState(true);

  const [fieldErrs, setFieldErrs] = React.useState<Record<string, string>>({});
  const [formErr, setFormErr] = React.useState<string | null>(null);
  const [conflictErr, setConflictErr] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  const [deleting, setDeleting] = React.useState<RouteItem | null>(null);
  const [deleteErr, setDeleteErr] = React.useState<string | null>(null);

  const handleDataLoaded = React.useCallback((ctx: RoutesLoadedContext) => {
    setRoutes(ctx.routes);
    setServices(ctx.services);
    setPools(ctx.pools);
  }, []);

  const openCreate = () => {
    setEditing(null);
    setName("");
    setServiceId(services[0]?.id ?? "");
    setPoolId(pools[0]?.id ?? "");
    setHost("api.gatewayforge.local");
    setPathPrefix("/v1/orders");
    setMethods(["GET"]);
    setPriority("100");
    setStripPrefix(false);
    setEnabled(true);
    setFieldErrs({});
    setFormErr(null);
    setConflictErr(null);
    setEditorOpen(true);
  };

  const openEdit = (rt: RouteItem) => {
    setEditing(rt);
    setName(rt.name);
    setServiceId(rt.service_id);
    setPoolId(rt.upstream_pool_id);
    setHost(rt.host);
    setPathPrefix(rt.path_prefix);
    setMethods(rt.methods);
    setPriority(String(rt.priority));
    setStripPrefix(rt.strip_prefix);
    setEnabled(rt.enabled);
    setFieldErrs({});
    setFormErr(null);
    setConflictErr(null);
    setEditorOpen(true);
  };

  const toggleMethod = (m: string) => {
    setMethods((prev) => (prev.includes(m) ? prev.filter((x) => x !== m) : [...prev, m]));
  };

  const parsedPriority = Number.parseInt(priority, 10);
  const liveConflicts = React.useMemo(
    () =>
      editorOpen
        ? findRouteConflicts(routes, {
            id: editing?.id,
            host,
            path_prefix: pathPrefix,
            priority: Number.isFinite(parsedPriority) ? parsedPriority : 100,
            methods,
            enabled,
          })
        : [],
    [editorOpen, routes, editing, host, pathPrefix, parsedPriority, methods, enabled]
  );

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId) return;
    setFormErr(null);
    setConflictErr(null);

    const errs: Record<string, string> = {};
    if (!name.trim() || name.trim().length > 120) {
      errs.name = "Route name must be 1–120 characters.";
    }
    if (!serviceId) {
      errs.service_id = "Select a service.";
    }
    if (!poolId) {
      errs.upstream_pool_id = "Select an upstream pool.";
    }
    if (!host.trim()) {
      errs.host = "Host is required (e.g. api.gatewayforge.local).";
    }
    if (!pathPrefix.startsWith("/") || pathPrefix.includes("..")) {
      errs.path_prefix = "Path prefix must start with '/' and must not contain '..'.";
    }
    if (methods.length === 0) {
      errs.methods = "Select at least one HTTP method.";
    }
    if (!Number.isFinite(parsedPriority) || parsedPriority < 0 || parsedPriority > 10000) {
      errs.priority = "Priority must be between 0 and 10000.";
    }

    setFieldErrs(errs);
    if (Object.keys(errs).length > 0) return;

    setSaving(true);
    try {
      if (editing) {
        await apiRequest<RouteItem>(`/api/v1/orgs/${activeOrgId}/routes/${editing.id}`, {
          method: "PATCH",
          csrfToken,
          ifMatch: `W/"rev-${editing.revision}"`,
          body: {
            service_id: serviceId,
            upstream_pool_id: poolId,
            name: name.trim(),
            host: host.trim().toLowerCase(),
            path_prefix: pathPrefix.trim(),
            methods,
            strip_prefix: stripPrefix,
            priority: parsedPriority,
            enabled,
            revision: editing.revision,
          },
          onUnauthorized: () => clearSession(true),
        });
      } else {
        await apiRequest<RouteItem>(`/api/v1/orgs/${activeOrgId}/routes`, {
          method: "POST",
          csrfToken,
          body: {
            service_id: serviceId,
            upstream_pool_id: poolId,
            name: name.trim(),
            host: host.trim().toLowerCase(),
            path_prefix: pathPrefix.trim(),
            methods,
            strip_prefix: stripPrefix,
            priority: parsedPriority,
            enabled,
          },
          onUnauthorized: () => clearSession(true),
        });
      }
      setEditorOpen(false);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        if (err.isConflict) {
          setConflictErr(err.friendlyMessage());
        } else {
          setFormErr(err.friendlyMessage());
        }
      } else {
        setFormErr("Unexpected error while saving route.");
      }
    } finally {
      setSaving(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!activeOrgId || !deleting) return;
    setDeleteErr(null);
    setSaving(true);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/routes/${deleting.id}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized: () => clearSession(true),
      });
      setDeleting(null);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setDeleteErr(err instanceof ApiError ? err.friendlyMessage() : "Could not delete route.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <RoutesView
        reloadKey={reloadKey}
        onCreate={openCreate}
        onEdit={openEdit}
        onDelete={(rt) => {
          setDeleteErr(null);
          setDeleting(rt);
        }}
        onDataLoaded={handleDataLoaded}
      />

      <Dialog
        open={editorOpen}
        onClose={() => setEditorOpen(false)}
        title={editing ? `Edit Route: ${editing.name}` : "Create Route"}
        description="Define host, path prefix, HTTP methods, priority, and target pool attachment."
      >
        <form onSubmit={handleSave} noValidate className="space-y-4">
          {conflictErr && (
            <ErrorBanner
              title="Route Conflict (409)"
              message={conflictErr}
              onRetry={() => {
                setEditorOpen(false);
                setReloadKey((k) => k + 1);
              }}
            />
          )}
          {formErr && <ErrorBanner title="Validation / Save Error" message={formErr} />}

          {liveConflicts.length > 0 && (
            <div
              role="status"
              className="flex items-start gap-2.5 rounded-lg border border-amber-200 bg-amber-50 px-3.5 py-2.5 text-xs text-amber-900"
            >
              <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
              <div>
                <p className="font-semibold">Potential Route Conflict Detected</p>
                <p>
                  Overlaps with enabled route{" "}
                  <span className="font-mono font-medium">{liveConflicts[0]?.name}</span> on the same host, path
                  prefix, priority, and HTTP method. Adjust priority, path, or methods before publishing.
                </p>
              </div>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="route-name">Route Name</Label>
            <Input
              id="route-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Orders Read"
            />
            <FieldError message={fieldErrs.name} />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="route-service">Service</Label>
              <Select id="route-service" value={serviceId} onChange={(e) => setServiceId(e.target.value)}>
                <option value="">Select a service…</option>
                {services.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name} ({s.slug})
                  </option>
                ))}
              </Select>
              <FieldError message={fieldErrs.service_id} />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="route-pool">Upstream Pool</Label>
              <Select id="route-pool" value={poolId} onChange={(e) => setPoolId(e.target.value)}>
                <option value="">Select an upstream pool…</option>
                {pools.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
              <FieldError message={fieldErrs.upstream_pool_id} />
            </div>
          </div>

          <div className="grid grid-cols-3 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="route-host">Host</Label>
              <Input
                id="route-host"
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="api.gatewayforge.local"
              />
              <FieldError message={fieldErrs.host} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="route-path">Path Prefix</Label>
              <Input
                id="route-path"
                value={pathPrefix}
                onChange={(e) => setPathPrefix(e.target.value)}
                placeholder="/v1/orders"
              />
              <FieldError message={fieldErrs.path_prefix} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="route-priority">Priority</Label>
              <Input
                id="route-priority"
                type="number"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
              />
              <FieldError message={fieldErrs.priority} />
            </div>
          </div>

          <fieldset className="space-y-1.5">
            <legend className="text-xs font-medium text-[var(--foreground)]">HTTP Methods</legend>
            <div className="flex flex-wrap gap-2">
              {ALL_METHODS.map((m) => (
                <label
                  key={m}
                  className="inline-flex cursor-pointer items-center gap-1.5 rounded border border-[var(--border)] px-2.5 py-1 text-xs font-mono"
                >
                  <input
                    type="checkbox"
                    checked={methods.includes(m)}
                    onChange={() => toggleMethod(m)}
                  />
                  {m}
                </label>
              ))}
            </div>
            <FieldError message={fieldErrs.methods} />
          </fieldset>

          <div className="flex flex-wrap gap-4">
            <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
              <input
                type="checkbox"
                checked={stripPrefix}
                onChange={(e) => setStripPrefix(e.target.checked)}
                className="h-4 w-4 rounded border-[var(--border-strong)]"
              />
              Strip matched path prefix
            </label>
            <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
              <input
                type="checkbox"
                checked={enabled}
                onChange={(e) => setEnabled(e.target.checked)}
                className="h-4 w-4 rounded border-[var(--border-strong)]"
              />
              Route enabled
            </label>
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setEditorOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Saving…" : editing ? "Save Route" : "Create Route"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title="Delete Route"
        description={deleting ? `Remove route "${deleting.name}" from draft configuration?` : undefined}
      >
        <div className="space-y-4">
          {deleteErr && <ErrorBanner title="Cannot delete route" message={deleteErr} />}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={saving} onClick={handleConfirmDelete}>
              {saving ? "Deleting…" : "Confirm Delete"}
            </Button>
          </div>
        </div>
      </Dialog>
    </>
  );
}
