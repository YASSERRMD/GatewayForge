"use client";

import * as React from "react";
import { Link2, Plus, Trash2, Unlink } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { FieldError, Input, Label, Select } from "@/components/ui/input";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import {
  AuthPolicyForm,
  defaultApiKeyConfig,
  defaultJwtConfig,
  validateAuthPolicyConfig,
} from "@/components/management/auth-policy-form";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse, PolicyItem, RouteItem, ServiceItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

export type SupportedPolicyType = "api_key" | "jwt";

export function defaultConfigForPolicyType(t: SupportedPolicyType): Record<string, unknown> {
  if (t === "api_key") return defaultApiKeyConfig() as unknown as Record<string, unknown>;
  return defaultJwtConfig() as unknown as Record<string, unknown>;
}

export function PoliciesManager() {
  const { activeOrgId, activeOrg, csrfToken, clearSession } = useAuth();
  const canWrite = activeOrg?.role !== "viewer";

  const [policies, setPolicies] = React.useState<PolicyItem[]>([]);
  const [services, setServices] = React.useState<ServiceItem[]>([]);
  const [routes, setRoutes] = React.useState<RouteItem[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);

  // Create policy modal
  const [createOpen, setCreateOpen] = React.useState(false);
  const [policyName, setPolicyName] = React.useState("");
  const [policyType, setPolicyType] = React.useState<SupportedPolicyType>("api_key");
  const [config, setConfig] = React.useState<Record<string, unknown>>(defaultConfigForPolicyType("api_key"));
  const [fieldErrs, setFieldErrs] = React.useState<Record<string, string>>({});
  const [formErr, setFormErr] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  // Attach policy modal
  const [attaching, setAttaching] = React.useState<PolicyItem | null>(null);
  const [scopeType, setScopeType] = React.useState<"org" | "service" | "route">("org");
  const [scopeServiceId, setScopeServiceId] = React.useState("");
  const [scopeRouteId, setScopeRouteId] = React.useState("");
  const [scopePriority, setScopePriority] = React.useState("100");
  const [attachErr, setAttachErr] = React.useState<string | null>(null);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    Promise.all([
      apiRequest<PaginatedResponse<PolicyItem>>(`/api/v1/orgs/${activeOrgId}/policies?limit=100&offset=0`, {
        signal: ctrl.signal,
        onUnauthorized,
      }),
      apiRequest<PaginatedResponse<ServiceItem>>(`/api/v1/orgs/${activeOrgId}/services?limit=100&offset=0`, {
        signal: ctrl.signal,
        onUnauthorized,
      }).catch(() => ({ items: [] as ServiceItem[], total: 0, limit: 100, offset: 0 })),
      apiRequest<PaginatedResponse<RouteItem>>(`/api/v1/orgs/${activeOrgId}/routes?limit=100&offset=0`, {
        signal: ctrl.signal,
        onUnauthorized,
      }).catch(() => ({ items: [] as RouteItem[], total: 0, limit: 100, offset: 0 })),
    ])
      .then(([polRes, svcRes, rtRes]) => {
        setPolicies(polRes.items ?? []);
        setServices(svcRes.items ?? []);
        setRoutes(rtRes.items ?? []);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load policies.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, reloadKey, onUnauthorized]);

  const serviceName = (id?: string | null) => services.find((s) => s.id === id)?.name ?? id ?? "";
  const routeName = (id?: string | null) => routes.find((r) => r.id === id)?.name ?? id ?? "";

  const openCreate = () => {
    setPolicyName("");
    setPolicyType("api_key");
    setConfig(defaultConfigForPolicyType("api_key"));
    setFieldErrs({});
    setFormErr(null);
    setCreateOpen(true);
  };

  const handleTypeChange = (nextType: SupportedPolicyType) => {
    setPolicyType(nextType);
    setConfig(defaultConfigForPolicyType(nextType));
    setFieldErrs({});
    setFormErr(null);
  };

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId) return;
    setFormErr(null);

    const errs: Record<string, string> = {};
    if (!policyName.trim() || policyName.trim().length > 120) {
      errs.name = "Policy name must be 1–120 characters.";
    }
    Object.assign(errs, validateAuthPolicyConfig(policyType, config));
    setFieldErrs(errs);
    if (Object.keys(errs).length > 0) return;

    setSaving(true);
    try {
      await apiRequest<PolicyItem>(`/api/v1/orgs/${activeOrgId}/policies`, {
        method: "POST",
        csrfToken,
        body: {
          name: policyName.trim(),
          policy_type: policyType,
          config,
          enabled: true,
        },
        onUnauthorized,
      });
      setCreateOpen(false);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setFormErr(err instanceof ApiError ? err.friendlyMessage() : "Could not create policy.");
    } finally {
      setSaving(false);
    }
  };

  const handleAttach = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId || !attaching) return;
    setAttachErr(null);

    if (scopeType === "service" && !scopeServiceId) {
      setAttachErr("Select a service to attach this policy.");
      return;
    }
    if (scopeType === "route" && !scopeRouteId) {
      setAttachErr("Select a route to attach this policy.");
      return;
    }

    setSaving(true);
    try {
      await apiRequest(`/api/v1/orgs/${activeOrgId}/policies/${attaching.id}/attachments`, {
        method: "POST",
        csrfToken,
        body: {
          scope_type: scopeType,
          service_id: scopeType === "service" ? scopeServiceId : undefined,
          route_id: scopeType === "route" ? scopeRouteId : undefined,
          priority: Number.parseInt(scopePriority, 10) || 100,
        },
        onUnauthorized,
      });
      setAttaching(null);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setAttachErr(err instanceof ApiError ? err.friendlyMessage() : "Could not attach policy.");
    } finally {
      setSaving(false);
    }
  };

  const handleDetach = async (policyId: string, attachmentId: string) => {
    if (!activeOrgId) return;
    setError(null);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/policies/${policyId}/attachments/${attachmentId}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized,
      });
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not detach policy.");
    }
  };

  const handleDeletePolicy = async (policyId: string) => {
    if (!activeOrgId) return;
    setError(null);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/policies/${policyId}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized,
      });
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not delete policy.");
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Policies</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Typed gateway policies attached by precedence (route &gt; service &gt; org) in{" "}
            <span className="font-medium text-[var(--foreground)]">{activeOrg?.org_name}</span>.
          </p>
        </div>
        {canWrite && (
          <Button type="button" onClick={openCreate}>
            <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
            New Policy
          </Button>
        )}
      </div>

      {error && <ErrorBanner title="Policy operation failed" message={error} onRetry={() => setReloadKey((k) => k + 1)} />}

      {loading ? (
        <div className="space-y-2.5">
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      ) : policies.length === 0 ? (
        <EmptyState
          title="No policies configured"
          description="Create typed authentication, rate-limiting, or resilience policies and bind them to routes, services, or your organization."
          action={
            canWrite ? (
              <Button type="button" onClick={openCreate}>
                <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
                Create Policy
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          {policies.map((pol) => (
            <Card key={pol.id}>
              <CardHeader className="flex flex-wrap items-start justify-between gap-2">
                <div className="space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <CardTitle>{pol.name}</CardTitle>
                    <Badge variant="default">{pol.policy_type}</Badge>
                    <Badge variant={pol.enabled ? "success" : "warning"}>
                      {pol.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </div>
                  <CardDescription>
                    Schema v{pol.schema_version} · rev-{pol.revision}
                  </CardDescription>
                </div>
                {canWrite && (
                  <div className="flex items-center gap-1">
                    <Button
                      type="button"
                      variant="outline"
                      size="sm"
                      onClick={() => {
                        setAttachErr(null);
                        setScopeType("org");
                        setScopeServiceId(services[0]?.id ?? "");
                        setScopeRouteId(routes[0]?.id ?? "");
                        setScopePriority("100");
                        setAttaching(pol);
                      }}
                    >
                      <Link2 className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
                      Attach
                    </Button>
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={`Delete policy ${pol.name}`}
                      onClick={() => handleDeletePolicy(pol.id)}
                    >
                      <Trash2 className="h-3.5 w-3.5 text-[var(--destructive)]" aria-hidden="true" />
                    </Button>
                  </div>
                )}
              </CardHeader>
              <CardContent className="space-y-3">
                <pre className="overflow-x-auto rounded border border-[var(--border)] bg-[var(--muted)]/40 p-2.5 font-mono text-xs text-[var(--foreground)]">
                  {JSON.stringify(pol.config, null, 2)}
                </pre>
                <div className="space-y-1.5">
                  <p className="text-xs font-medium text-[var(--muted-foreground)]">Attachments</p>
                  {!pol.attachments || pol.attachments.length === 0 ? (
                    <p className="text-xs text-[var(--muted-foreground)]">Not attached to any scope yet.</p>
                  ) : (
                    <ul className="space-y-1">
                      {pol.attachments.map((att) => (
                        <li
                          key={att.id}
                          className="flex items-center justify-between rounded border border-[var(--border)] px-2.5 py-1.5 text-xs"
                        >
                          <span>
                            <span className="font-semibold uppercase">{att.scope_type}</span>
                            {att.scope_type === "service" && `: ${serviceName(att.service_id)}`}
                            {att.scope_type === "route" && `: ${routeName(att.route_id)}`}
                            {" · "}priority {att.priority}
                          </span>
                          {canWrite && (
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              aria-label={`Detach ${att.scope_type} attachment`}
                              onClick={() => handleDetach(pol.id, att.id)}
                            >
                              <Unlink className="h-3.5 w-3.5" aria-hidden="true" />
                            </Button>
                          )}
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      <Dialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Create Policy"
        description="Configure typed policy settings with safe defaults and inline validation."
      >
        <form onSubmit={handleCreate} noValidate className="space-y-4">
          {formErr && <ErrorBanner title="Validation / Save Error" message={formErr} />}

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="policy-name">Policy Name</Label>
              <Input
                id="policy-name"
                value={policyName}
                onChange={(e) => setPolicyName(e.target.value)}
                placeholder="require-partner-jwt"
              />
              <FieldError message={fieldErrs.name} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="policy-type">Policy Type</Label>
              <Select
                id="policy-type"
                value={policyType}
                onChange={(e) => handleTypeChange(e.target.value as SupportedPolicyType)}
              >
                <option value="api_key">API Key Auth (api_key)</option>
                <option value="jwt">JWT Verification (jwt)</option>
              </Select>
            </div>
          </div>

          <AuthPolicyForm
            policyType={policyType}
            config={config}
            errors={fieldErrs}
            onChange={setConfig}
          />

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Creating…" : "Create Policy"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={attaching !== null}
        onClose={() => setAttaching(null)}
        title={attaching ? `Attach Policy: ${attaching.name}` : "Attach Policy"}
        description="Scope precedence: route overrides service, service overrides organization."
      >
        <form onSubmit={handleAttach} className="space-y-4">
          {attachErr && <ErrorBanner title="Attachment Error" message={attachErr} />}

          <div className="space-y-1.5">
            <Label htmlFor="attach-scope">Attachment Scope</Label>
            <Select
              id="attach-scope"
              value={scopeType}
              onChange={(e) => setScopeType(e.target.value as "org" | "service" | "route")}
            >
              <option value="org">Organization (all routes)</option>
              <option value="service">Service</option>
              <option value="route">Route</option>
            </Select>
          </div>

          {scopeType === "service" && (
            <div className="space-y-1.5">
              <Label htmlFor="attach-service">Target Service</Label>
              <Select
                id="attach-service"
                value={scopeServiceId}
                onChange={(e) => setScopeServiceId(e.target.value)}
              >
                <option value="">Select a service…</option>
                {services.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </div>
          )}

          {scopeType === "route" && (
            <div className="space-y-1.5">
              <Label htmlFor="attach-route">Target Route</Label>
              <Select
                id="attach-route"
                value={scopeRouteId}
                onChange={(e) => setScopeRouteId(e.target.value)}
              >
                <option value="">Select a route…</option>
                {routes.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name} ({r.host}
                    {r.path_prefix})
                  </option>
                ))}
              </Select>
            </div>
          )}

          <div className="space-y-1.5">
            <Label htmlFor="attach-priority">Attachment Priority</Label>
            <Input
              id="attach-priority"
              type="number"
              value={scopePriority}
              onChange={(e) => setScopePriority(e.target.value)}
            />
          </div>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setAttaching(null)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Attaching…" : "Attach Policy"}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}
