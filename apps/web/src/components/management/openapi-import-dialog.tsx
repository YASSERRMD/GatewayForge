"use client";

import * as React from "react";
import { AlertTriangle, FileUp } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label, Select, Textarea } from "@/components/ui/input";
import { ErrorBanner } from "@/components/ui/states";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { UpstreamPoolItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

export interface PreviewRouteItem {
  key: string;
  name: string;
  host: string;
  path_prefix: string;
  methods: string[];
  strip_prefix: boolean;
  priority: number;
  source_path: string;
  conflict: boolean;
  existing_route_id?: string;
  conflict_reason?: string;
  default_action: "create" | "skip";
}

export interface OpenAPIPreview {
  openapi_version: string;
  service: {
    slug: string;
    name: string;
    description: string;
  };
  service_exists: boolean;
  existing_service_id?: string;
  default_host: string;
  base_path: string;
  routes: PreviewRouteItem[];
  warnings: Array<{ code: string; path?: string; message: string }>;
}

export type ConflictDecisionAction = "create" | "skip" | "rename" | "overwrite";

interface RouteDecisionState {
  action: ConflictDecisionAction;
  renamedName: string;
  renamedPath: string;
}

export function OpenAPIImportDialog({
  open,
  pools,
  onClose,
  onImported,
}: {
  open: boolean;
  pools: UpstreamPoolItem[];
  onClose: () => void;
  onImported: () => void;
}) {
  const { activeOrgId, csrfToken, clearSession } = useAuth();

  const [spec, setSpec] = React.useState("");
  const [overrideHost, setOverrideHost] = React.useState("");
  const [upstreamPoolId, setUpstreamPoolId] = React.useState("");
  const [preview, setPreview] = React.useState<OpenAPIPreview | null>(null);
  const [decisions, setDecisions] = React.useState<Record<string, RouteDecisionState>>({});
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (open) {
      setUpstreamPoolId((prev) => prev || pools[0]?.id || "");
    } else {
      setPreview(null);
      setDecisions({});
      setError(null);
    }
  }, [open, pools]);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  const handleFileUpload = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    if (file.size > 2 * 1024 * 1024) {
      setError("OpenAPI file exceeds the 2 MiB maximum upload limit.");
      return;
    }
    setError(null);
    const text = await file.text();
    setSpec(text);
  };

  const handlePreview = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId) return;
    setError(null);
    if (!spec.trim()) {
      setError("Paste or upload an OpenAPI 3.0/3.1 JSON or YAML specification first.");
      return;
    }
    setBusy(true);
    try {
      const res = await apiRequest<OpenAPIPreview>(`/api/v1/orgs/${activeOrgId}/openapi/preview`, {
        method: "POST",
        csrfToken,
        body: {
          spec,
          override_host: overrideHost.trim(),
        },
        onUnauthorized,
      });
      setPreview(res);
      const nextDecs: Record<string, RouteDecisionState> = {};
      for (const rt of res.routes) {
        nextDecs[rt.key] = {
          // Conflicts default strictly to "skip" — never silent overwrite!
          action: rt.conflict ? "skip" : "create",
          renamedName: `${rt.name}-imported`,
          renamedPath: `${rt.path_prefix.replace(/\/+$/, "")}-v2`,
        };
      }
      setDecisions(nextDecs);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not parse OpenAPI specification.");
    } finally {
      setBusy(false);
    }
  };

  const handleCommit = async () => {
    if (!activeOrgId || !preview) return;
    setError(null);
    if (!upstreamPoolId) {
      setError("Select an upstream pool for the imported routes.");
      return;
    }

    const routesPayload = preview.routes.map((rt) => {
      const dec = decisions[rt.key];
      const action = dec?.action ?? (rt.conflict ? "skip" : "create");
      return {
        key: rt.key,
        action,
        name: action === "rename" ? (dec?.renamedName.trim() || rt.name) : rt.name,
        host: rt.host,
        path_prefix: action === "rename" ? (dec?.renamedPath.trim() || rt.path_prefix) : rt.path_prefix,
        methods: rt.methods,
        strip_prefix: rt.strip_prefix,
        priority: rt.priority,
        existing_route_id: rt.existing_route_id ?? "",
      };
    });

    setBusy(true);
    try {
      await apiRequest(`/api/v1/orgs/${activeOrgId}/openapi/import`, {
        method: "POST",
        csrfToken,
        body: {
          service: preview.service,
          upstream_pool_id: upstreamPoolId,
          routes: routesPayload,
        },
        onUnauthorized,
      });
      onImported();
      onClose();
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not commit OpenAPI draft import.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Import OpenAPI 3.0 / 3.1 Specification"
      description="Upload or paste an OpenAPI spec to preview draft changes and explicitly resolve route collisions."
    >
      <div className="space-y-4">
        {error && <ErrorBanner title="OpenAPI Import Error" message={error} />}

        {!preview ? (
          <form onSubmit={handlePreview} className="space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="openapi-file">Upload OpenAPI File (.yaml, .yml, .json — max 2 MiB)</Label>
              <Input id="openapi-file" type="file" accept=".yaml,.yml,.json" onChange={handleFileUpload} />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="openapi-override-host">Route Host Override (optional)</Label>
              <Input
                id="openapi-override-host"
                value={overrideHost}
                onChange={(e) => setOverrideHost(e.target.value)}
                placeholder="api.gatewayforge.local"
              />
            </div>

            <div className="space-y-1.5">
              <Label htmlFor="openapi-spec">OpenAPI Spec (YAML or JSON)</Label>
              <Textarea
                id="openapi-spec"
                rows={8}
                value={spec}
                onChange={(e) => setSpec(e.target.value)}
                placeholder="openapi: '3.0.3'&#10;info:&#10;  title: Orders API&#10;  version: '1.0.0'&#10;paths: ..."
                className="font-mono text-xs"
              />
            </div>

            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={onClose}>
                Cancel
              </Button>
              <Button type="submit" disabled={busy}>
                <FileUp className="mr-1.5 h-4 w-4" aria-hidden="true" />
                {busy ? "Analyzing…" : "Preview Import"}
              </Button>
            </div>
          </form>
        ) : (
          <div className="space-y-4">
            <div className="rounded-md border border-[var(--border)] bg-[var(--muted)]/30 p-3 text-xs">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <span className="font-semibold text-[var(--foreground)]">Service: {preview.service.name}</span>{" "}
                  <span className="font-mono text-[var(--muted-foreground)]">({preview.service.slug})</span>
                </div>
                <Badge variant={preview.service_exists ? "warning" : "success"}>
                  {preview.service_exists ? "Reuses existing service" : "New draft service"}
                </Badge>
              </div>
              <p className="mt-1 text-[var(--muted-foreground)]">
                OpenAPI {preview.openapi_version} · Host: <code>{preview.default_host}</code>
              </p>
            </div>

            {preview.warnings.length > 0 && (
              <div className="space-y-1 rounded-md border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900">
                <p className="flex items-center gap-1.5 font-semibold">
                  <AlertTriangle className="h-3.5 w-3.5 text-amber-700" aria-hidden="true" />
                  Mapping Warnings ({preview.warnings.length})
                </p>
                <ul className="list-disc space-y-0.5 pl-4">
                  {preview.warnings.map((w, i) => (
                    <li key={`${w.code}-${i}`}>{w.message}</li>
                  ))}
                </ul>
              </div>
            )}

            <div className="space-y-1.5">
              <Label htmlFor="openapi-target-pool">Target Upstream Pool</Label>
              <Select
                id="openapi-target-pool"
                value={upstreamPoolId}
                onChange={(e) => setUpstreamPoolId(e.target.value)}
              >
                <option value="">Select an upstream pool…</option>
                {pools.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </Select>
            </div>

            <div className="max-h-72 space-y-2 overflow-y-auto">
              <p className="text-xs font-semibold uppercase tracking-wider text-[var(--muted-foreground)]">
                Proposed Routes &amp; Conflict Resolution ({preview.routes.length})
              </p>
              {preview.routes.map((rt) => {
                const dec = decisions[rt.key] ?? {
                  action: rt.conflict ? "skip" : "create",
                  renamedName: `${rt.name}-imported`,
                  renamedPath: `${rt.path_prefix}-v2`,
                };
                return (
                  <div key={rt.key} className="space-y-2 rounded-md border border-[var(--border)] p-3 text-xs">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <span className="font-mono font-medium text-[var(--foreground)]">
                          {rt.host}
                          {rt.path_prefix}
                        </span>
                        <span className="ml-2 text-[var(--muted-foreground)]">{rt.methods.join(", ")}</span>
                      </div>
                      <Badge variant={rt.conflict ? "warning" : "success"}>
                        {rt.conflict ? "Collision" : "New"}
                      </Badge>
                    </div>
                    {rt.conflict && rt.conflict_reason && (
                      <p className="text-amber-800">{rt.conflict_reason}</p>
                    )}

                    <div className="flex flex-wrap items-center gap-2">
                      <Label htmlFor={`dec-${rt.key}`}>Resolution</Label>
                      <Select
                        id={`dec-${rt.key}`}
                        aria-label={`Resolution for ${rt.path_prefix}`}
                        value={dec.action}
                        onChange={(e) =>
                          setDecisions((prev) => ({
                            ...prev,
                            [rt.key]: { ...dec, action: e.target.value as ConflictDecisionAction },
                          }))
                        }
                        className="w-auto"
                      >
                        {!rt.conflict && <option value="create">Create route</option>}
                        <option value="skip">Skip (keep existing)</option>
                        {rt.conflict && <option value="rename">Rename / new path</option>}
                        {rt.conflict && <option value="overwrite">Overwrite existing draft route</option>}
                      </Select>
                    </div>

                    {dec.action === "rename" && (
                      <div className="grid grid-cols-2 gap-2">
                        <Input
                          aria-label={`New route name for ${rt.path_prefix}`}
                          value={dec.renamedName}
                          onChange={(e) =>
                            setDecisions((prev) => ({
                              ...prev,
                              [rt.key]: { ...dec, renamedName: e.target.value },
                            }))
                          }
                          placeholder="new-route-name"
                        />
                        <Input
                          aria-label={`New path prefix for ${rt.path_prefix}`}
                          value={dec.renamedPath}
                          onChange={(e) =>
                            setDecisions((prev) => ({
                              ...prev,
                              [rt.key]: { ...dec, renamedPath: e.target.value },
                            }))
                          }
                          placeholder="/v2/orders"
                        />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>

            <div className="flex justify-between gap-2 pt-2">
              <Button type="button" variant="outline" onClick={() => setPreview(null)}>
                Back to Spec
              </Button>
              <div className="flex gap-2">
                <Button type="button" variant="outline" onClick={onClose}>
                  Cancel
                </Button>
                <Button type="button" disabled={busy} onClick={handleCommit}>
                  {busy ? "Importing…" : "Commit Draft Import"}
                </Button>
              </div>
            </div>
          </div>
        )}
      </div>
    </Dialog>
  );
}
