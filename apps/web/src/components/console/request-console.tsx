"use client";

import * as React from "react";
import { Play, Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldError, Input, Label, Select, Textarea } from "@/components/ui/input";
import { ErrorBanner } from "@/components/ui/states";
import { ResponsePanel } from "@/components/console/response-panel";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse, RouteItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

export const MAX_CONSOLE_BODY_BYTES = 64 * 1024;
const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"] as const;

export interface HeaderRow {
  id: string;
  key: string;
  value: string;
}

export interface ConsoleExecuteRequest {
  method: string;
  host: string;
  path: string;
  headers: Record<string, string>;
  body: string;
}

export interface ConsoleExecuteResponse {
  status: number;
  duration_ms: number;
  headers: Record<string, string[]>;
  body: string;
  body_truncated: boolean;
  body_bytes: number;
  is_binary: boolean;
  content_type: string;
  gateway_target: string;
}

export interface RequestConsoleProps {
  onExecuteOverride?: (req: ConsoleExecuteRequest) => Promise<ConsoleExecuteResponse>;
  renderResponse?: (resp: ConsoleExecuteResponse | null, running: boolean) => React.ReactNode;
  headerSlot?: React.ReactNode;
  sidebarSlot?: React.ReactNode;
  presetHost?: string;
  externalDraft?: Partial<ConsoleExecuteRequest> & { nonce?: number };
  onExecuted?: (req: ConsoleExecuteRequest, resp: ConsoleExecuteResponse) => void;
}

export function RequestConsole({
  onExecuteOverride,
  renderResponse,
  headerSlot,
  sidebarSlot,
  presetHost,
  externalDraft,
  onExecuted,
}: RequestConsoleProps) {
  const { activeOrgId, activeOrg, csrfToken, clearSession } = useAuth();
  const canUseConsole =
    activeOrg?.role === "owner" || activeOrg?.role === "admin" || activeOrg?.role === "developer";

  const [routes, setRoutes] = React.useState<RouteItem[]>([]);
  const [selectedRouteId, setSelectedRouteId] = React.useState("");
  const [method, setMethod] = React.useState("GET");
  const [host, setHost] = React.useState("api.gatewayforge.local");
  const [path, setPath] = React.useState("/");
  const [headers, setHeaders] = React.useState<HeaderRow[]>([
    { id: "h-1", key: "Accept", value: "application/json" },
  ]);
  const [body, setBody] = React.useState("");

  const [validationErr, setValidationErr] = React.useState<string | null>(null);
  const [execErr, setExecErr] = React.useState<string | null>(null);
  const [running, setRunning] = React.useState(false);
  const [response, setResponse] = React.useState<ConsoleExecuteResponse | null>(null);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (presetHost) {
      setHost(presetHost);
    }
  }, [presetHost]);

  React.useEffect(() => {
    if (!externalDraft) return;
    if (externalDraft.method) setMethod(externalDraft.method);
    if (externalDraft.host) setHost(externalDraft.host);
    if (externalDraft.path) setPath(externalDraft.path);
    if (externalDraft.body !== undefined) setBody(externalDraft.body);
    if (externalDraft.headers) {
      const nextRows = Object.entries(externalDraft.headers).map(([k, v], i) => ({
        id: `ext-${i}-${k}`,
        key: k,
        value: v,
      }));
      setHeaders(nextRows.length > 0 ? nextRows : [{ id: "h-empty", key: "", value: "" }]);
    }
  }, [externalDraft]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    apiRequest<PaginatedResponse<RouteItem>>(`/api/v1/orgs/${activeOrgId}/routes?limit=100&offset=0`, {
      signal: ctrl.signal,
      onUnauthorized,
    })
      .then((res) => setRoutes(res.items ?? []))
      .catch(() => setRoutes([]));
    return () => ctrl.abort();
  }, [activeOrgId, onUnauthorized]);

  const bodyBytes = React.useMemo(() => new TextEncoder().encode(body).length, [body]);

  const handleSelectRoute = (routeId: string) => {
    setSelectedRouteId(routeId);
    const rt = routes.find((r) => r.id === routeId);
    if (!rt) return;
    setHost(rt.host);
    setPath(rt.path_prefix);
    if (rt.methods.length > 0 && rt.methods[0]) {
      setMethod(rt.methods[0]);
    }
  };

  const addHeaderRow = () => {
    setHeaders((prev) => [...prev, { id: `h-${Date.now()}-${prev.length}`, key: "", value: "" }]);
  };

  const updateHeader = (id: string, field: "key" | "value", val: string) => {
    setHeaders((prev) => prev.map((r) => (r.id === id ? { ...r, [field]: val } : r)));
  };

  const removeHeader = (id: string) => {
    setHeaders((prev) => prev.filter((r) => r.id !== id));
  };

  const executeRequest = React.useCallback(async () => {
    if (!activeOrgId || !canUseConsole) return;
    setValidationErr(null);
    setExecErr(null);

    const trimmedHost = host.trim();
    const trimmedPath = path.trim();
    if (!trimmedHost || /\s|\/|:/.test(trimmedHost)) {
      setValidationErr("Host must be a valid route hostname (without scheme, path, or port).");
      return;
    }
    if (!trimmedPath.startsWith("/")) {
      setValidationErr("Path must start with '/'. Arbitrary external URLs are disallowed.");
      return;
    }
    if (bodyBytes > MAX_CONSOLE_BODY_BYTES) {
      setValidationErr(`Request body exceeds maximum cap of ${MAX_CONSOLE_BODY_BYTES} bytes (64 KiB).`);
      return;
    }

    const headerMap: Record<string, string> = {};
    for (const row of headers) {
      const k = row.key.trim();
      if (k) {
        headerMap[k] = row.value;
      }
    }

    const payload: ConsoleExecuteRequest = {
      method,
      host: trimmedHost,
      path: trimmedPath,
      headers: headerMap,
      body: method === "GET" || method === "HEAD" ? "" : body,
    };

    setRunning(true);
    try {
      const res = onExecuteOverride
        ? await onExecuteOverride(payload)
        : await apiRequest<ConsoleExecuteResponse>(`/api/v1/orgs/${activeOrgId}/console/execute`, {
            method: "POST",
            csrfToken,
            body: payload,
            onUnauthorized,
          });
      setResponse(res);
      onExecuted?.(payload, res);
    } catch (err: unknown) {
      setExecErr(err instanceof ApiError ? err.friendlyMessage() : "Console execution failed.");
    } finally {
      setRunning(false);
    }
  }, [activeOrgId, canUseConsole, host, path, bodyBytes, headers, method, body, onExecuteOverride, csrfToken, onUnauthorized, onExecuted]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
      e.preventDefault();
      void executeRequest();
    }
  };

  return (
    <div className="space-y-4" onKeyDown={handleKeyDown}>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Request Console</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Send constrained test traffic through the GatewayForge data plane (press <kbd className="rounded border px-1 py-0.5 text-xs">Ctrl+Enter</kbd> to send).
          </p>
        </div>
        {headerSlot}
      </div>

      {!canUseConsole && (
        <ErrorBanner
          title="Read-only role"
          message="Your viewer role cannot execute requests through the developer console. Contact an organization administrator for developer access."
        />
      )}

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-12">
        <Card className={sidebarSlot ? "xl:col-span-5" : "xl:col-span-6"}>
          <CardHeader>
            <CardTitle>Request Builder</CardTitle>
            <CardDescription>
              Requests are dispatched exclusively to the configured gateway data plane using your selected route host and path.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            {execErr && <ErrorBanner title="Request Failed" message={execErr} />}

            <div className="space-y-1.5">
              <Label htmlFor="console-route-select">Prefill from Published/Draft Route</Label>
              <Select
                id="console-route-select"
                value={selectedRouteId}
                onChange={(e) => handleSelectRoute(e.target.value)}
              >
                <option value="">Manual host &amp; path…</option>
                {routes.map((rt) => (
                  <option key={rt.id} value={rt.id}>
                    {rt.name} ({rt.methods.join(",")} {rt.host}
                    {rt.path_prefix})
                  </option>
                ))}
              </Select>
            </div>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-12">
              <div className="sm:col-span-3 space-y-1">
                <Label htmlFor="console-method">Method</Label>
                <Select
                  id="console-method"
                  value={method}
                  onChange={(e) => setMethod(e.target.value)}
                >
                  {METHODS.map((m) => (
                    <option key={m} value={m}>
                      {m}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="sm:col-span-4 space-y-1">
                <Label htmlFor="console-host">Route Host</Label>
                <Input
                  id="console-host"
                  value={host}
                  onChange={(e) => setHost(e.target.value)}
                  placeholder="api.gatewayforge.local"
                />
              </div>
              <div className="sm:col-span-5 space-y-1">
                <Label htmlFor="console-path">Path &amp; Query</Label>
                <Input
                  id="console-path"
                  value={path}
                  onChange={(e) => setPath(e.target.value)}
                  placeholder="/v1/orders?limit=5"
                />
              </div>
            </div>
            <FieldError message={validationErr} />

            <div className="space-y-2">
              <div className="flex items-center justify-between">
                <Label>Request Headers</Label>
                <Button type="button" variant="outline" size="sm" onClick={addHeaderRow}>
                  <Plus className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
                  Add Header
                </Button>
              </div>
              <div className="space-y-2">
                {headers.map((row, idx) => (
                  <div key={row.id} className="flex items-center gap-2">
                    <Input
                      aria-label={`Header name ${idx + 1}`}
                      placeholder="X-Api-Key"
                      value={row.key}
                      onChange={(e) => updateHeader(row.id, "key", e.target.value)}
                    />
                    <Input
                      aria-label={`Header value ${idx + 1}`}
                      placeholder="value"
                      value={row.value}
                      onChange={(e) => updateHeader(row.id, "value", e.target.value)}
                    />
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={`Remove header ${idx + 1}`}
                      onClick={() => removeHeader(row.id)}
                    >
                      <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                    </Button>
                  </div>
                ))}
              </div>
            </div>

            <div className="space-y-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="console-body">Request Body</Label>
                <span className="font-mono text-xs text-[var(--muted-foreground)] tabular-nums">
                  {bodyBytes.toLocaleString()} / {MAX_CONSOLE_BODY_BYTES.toLocaleString()} B
                </span>
              </div>
              <Textarea
                id="console-body"
                rows={6}
                disabled={method === "GET" || method === "HEAD"}
                value={body}
                onChange={(e) => setBody(e.target.value)}
                placeholder='{"order_id": "ord_123"}'
                className="font-mono text-xs"
              />
            </div>

            <div className="flex justify-end">
              <Button
                type="button"
                disabled={!canUseConsole || running}
                onClick={() => void executeRequest()}
              >
                <Play className="mr-1.5 h-4 w-4" aria-hidden="true" />
                {running ? "Sending…" : "Send Request"}
              </Button>
            </div>
          </CardContent>
        </Card>

        <div className={sidebarSlot ? "xl:col-span-4" : "xl:col-span-6"}>
          {renderResponse ? (
            renderResponse(response, running)
          ) : (
            <ResponsePanel response={response} running={running} />
          )}
        </div>

        {sidebarSlot && <div className="xl:col-span-3">{sidebarSlot}</div>}
      </div>
    </div>
  );
}
