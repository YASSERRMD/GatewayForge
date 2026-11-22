"use client";

import * as React from "react";
import { ChevronLeft, ChevronRight, Download, Eye, Search, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const PAGE_SIZE = 20;
const SENSITIVE_KEY_RE = /(password|secret|token|api_key|apikey|authorization|cookie|hash|private_key)/i;
const GWF_KEY_RE = /\bgwf_[A-Za-z0-9]{6,}[._][A-Za-z0-9_-]{8,}\b/g;

export interface AuditLogRow {
  id: number;
  org_id?: string | null;
  actor_user_id?: string | null;
  actor_email: string;
  action: string;
  target_type: string;
  target_id: string;
  request_id: string;
  before_redacted?: unknown;
  after_redacted?: unknown;
  created_at: string;
}

export function redactSensitiveDeep(value: unknown): unknown {
  if (value === null || value === undefined) return value;
  if (typeof value === "string") {
    return value.replace(GWF_KEY_RE, "[REDACTED]");
  }
  if (Array.isArray(value)) {
    return value.map((item) => redactSensitiveDeep(item));
  }
  if (typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      if (SENSITIVE_KEY_RE.test(k)) {
        out[k] = "[REDACTED]";
      } else {
        out[k] = redactSensitiveDeep(v);
      }
    }
    return out;
  }
  return value;
}

export function AuditExplorer() {
  const { activeOrgId, activeOrg, clearSession } = useAuth();

  const [actorEmail, setActorEmail] = React.useState("");
  const [actionFilter, setActionFilter] = React.useState("");
  const [targetType, setTargetType] = React.useState("");
  const [offset, setOffset] = React.useState(0);

  const [items, setItems] = React.useState<AuditLogRow[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [tick, setTick] = React.useState(0);
  const [selected, setSelected] = React.useState<AuditLogRow | null>(null);

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
    if (actorEmail.trim()) qs.set("actor_email", actorEmail.trim());
    if (actionFilter.trim()) qs.set("action", actionFilter.trim());
    if (targetType.trim()) qs.set("target_type", targetType.trim());

    apiRequest<PaginatedResponse<AuditLogRow>>(
      `/api/v1/orgs/${activeOrgId}/audit-events?${qs.toString()}`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => {
        setItems(res.items ?? []);
        setTotal(res.total ?? 0);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load audit events.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, actorEmail, actionFilter, targetType, offset, tick, onUnauthorized]);

  const handleSafeExport = () => {
    const safePayload = items.map((ev) => ({
      ...ev,
      before_redacted: redactSensitiveDeep(ev.before_redacted),
      after_redacted: redactSensitiveDeep(ev.after_redacted),
    }));
    const blob = new Blob([JSON.stringify(safePayload, null, 2)], {
      type: "application/json;charset=utf-8",
    });
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = `gatewayforge-audit-${activeOrg?.org_slug ?? "org"}.json`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">
            Insights &amp; Audit Explorer
          </h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Append-only, redacted security and release audit log for{" "}
            <span className="font-medium text-[var(--foreground)]">{activeOrg?.org_name}</span>.
          </p>
        </div>
        <Button type="button" variant="outline" size="sm" disabled={items.length === 0} onClick={handleSafeExport}>
          <Download className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
          Export Redacted JSON
        </Button>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-1.5 text-sm">
            <Search className="h-4 w-4" aria-hidden="true" />
            Filter Audit Trail
          </CardTitle>
          <CardDescription>
            All credentials, cookies, JWTs, and API keys are stripped server-side and re-verified before display or export.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <div className="space-y-1">
              <Label htmlFor="audit-actor">Actor Email</Label>
              <Input
                id="audit-actor"
                value={actorEmail}
                onChange={(e) => {
                  setOffset(0);
                  setActorEmail(e.target.value);
                }}
                placeholder="admin@gatewayforge.local"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="audit-action">Action</Label>
              <Input
                id="audit-action"
                value={actionFilter}
                onChange={(e) => {
                  setOffset(0);
                  setActionFilter(e.target.value);
                }}
                placeholder="publication.rollback"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="audit-target">Target Resource Type</Label>
              <Input
                id="audit-target"
                value={targetType}
                onChange={(e) => {
                  setOffset(0);
                  setTargetType(e.target.value);
                }}
                placeholder="publication"
              />
            </div>
          </div>
        </CardContent>
      </Card>

      {error ? (
        <ErrorBanner title="Could not load audit events" message={error} onRetry={() => setTick((t) => t + 1)} />
      ) : loading ? (
        <div className="space-y-2">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          title="No matching audit events"
          description="Try clearing filters or execute management/release actions to generate append-only audit records."
        />
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Timestamp</TableHead>
                <TableHead>Actor</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Target</TableHead>
                <TableHead>Request ID</TableHead>
                <TableHead className="text-right">Details</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {items.map((ev) => (
                <TableRow key={ev.id}>
                  <TableCell className="text-xs text-[var(--muted-foreground)]">
                    {new Date(ev.created_at).toLocaleString()}
                  </TableCell>
                  <TableCell className="text-xs font-medium">{ev.actor_email || "system"}</TableCell>
                  <TableCell>
                    <Badge variant={ev.action.includes("rollback") ? "warning" : "default"}>{ev.action}</Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">
                    {ev.target_type}:{ev.target_id ? ev.target_id.slice(0, 12) : "—"}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">{ev.request_id}</TableCell>
                  <TableCell className="text-right">
                    <Button
                      type="button"
                      variant="ghost"
                      size="sm"
                      aria-label={`Inspect audit event ${ev.id}`}
                      onClick={() => setSelected(ev)}
                    >
                      <Eye className="h-3.5 w-3.5" aria-hidden="true" />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
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

      <Dialog
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected ? `Audit Event #${selected.id}: ${selected.action}` : "Audit Event"}
        description="Redacted before/after snapshots for security investigation."
      >
        {selected && (
          <div className="space-y-3 text-xs">
            <div className="flex items-center gap-1.5 text-emerald-800">
              <ShieldCheck className="h-4 w-4" aria-hidden="true" />
              Sensitive fields are redacted prior to storage and display.
            </div>
            <div className="grid grid-cols-2 gap-2 rounded-md border border-[var(--border)] p-2.5">
              <div>
                <span className="text-[var(--muted-foreground)]">Actor:</span>{" "}
                <span className="font-medium">{selected.actor_email || "system"}</span>
              </div>
              <div>
                <span className="text-[var(--muted-foreground)]">Request ID:</span>{" "}
                <span className="font-mono">{selected.request_id}</span>
              </div>
            </div>
            <div className="space-y-1">
              <p className="font-semibold text-[var(--muted-foreground)]">After (Redacted)</p>
              <pre
                data-testid="audit-after-json"
                className="max-h-60 overflow-auto rounded-md border border-[var(--border)] bg-[var(--muted)]/40 p-2.5 font-mono text-[11px]"
              >
                {JSON.stringify(redactSensitiveDeep(selected.after_redacted ?? {}), null, 2)}
              </pre>
            </div>
          </div>
        )}
      </Dialog>
    </div>
  );
}
