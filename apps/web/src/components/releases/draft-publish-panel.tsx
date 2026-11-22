"use client";

import * as React from "react";
import { AlertTriangle, CheckCircle2, RefreshCw, Rocket, XCircle } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { Label, Textarea } from "@/components/ui/input";
import { ErrorBanner, Skeleton } from "@/components/ui/states";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { ValidationReport } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

export interface DraftResponse {
  draft_revision: number;
  validation: ValidationReport;
  preview?: {
    checksum?: string;
    routes?: unknown[];
    upstreams?: Record<string, unknown>;
    policies?: Record<string, unknown>;
  };
}

export interface DraftPublishPanelProps {
  reloadKey?: number;
  onPublished?: () => void;
  extraContent?: React.ReactNode;
}

export function DraftPublishPanel({ reloadKey = 0, onPublished, extraContent }: DraftPublishPanelProps) {
  const { activeOrgId, activeOrg, csrfToken, clearSession } = useAuth();
  const canPublish = activeOrg?.role === "owner" || activeOrg?.role === "admin";

  const [draft, setDraft] = React.useState<DraftResponse | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [localTick, setLocalTick] = React.useState(0);

  const [confirmOpen, setConfirmOpen] = React.useState(false);
  const [notes, setNotes] = React.useState("");
  const [activateImmediately, setActivateImmediately] = React.useState(true);
  const [acknowledged, setAcknowledged] = React.useState(false);
  const [publishErr, setPublishErr] = React.useState<string | null>(null);
  const [publishing, setPublishing] = React.useState(false);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    apiRequest<DraftResponse>(`/api/v1/orgs/${activeOrgId}/draft`, {
      signal: ctrl.signal,
      onUnauthorized,
    })
      .then((res) => setDraft(res))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load draft configuration.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, reloadKey, localTick, onUnauthorized]);

  const handlePublish = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId || !draft || !acknowledged) return;
    setPublishErr(null);
    setPublishing(true);
    try {
      await apiRequest(`/api/v1/orgs/${activeOrgId}/publications`, {
        method: "POST",
        csrfToken,
        body: {
          expected_draft_revision: draft.draft_revision,
          notes: notes.trim() || `Publish draft rev-${draft.draft_revision}`,
          activate: activateImmediately,
        },
        onUnauthorized,
      });
      setConfirmOpen(false);
      setNotes("");
      setAcknowledged(false);
      setLocalTick((t) => t + 1);
      onPublished?.();
    } catch (err: unknown) {
      setPublishErr(err instanceof ApiError ? err.friendlyMessage() : "Could not publish draft.");
    } finally {
      setPublishing(false);
    }
  };

  const valid = draft?.validation.valid ?? false;
  const errors = draft?.validation.errors ?? [];
  const warnings = draft?.validation.warnings ?? [];
  const routeCount = draft?.preview?.routes?.length ?? 0;
  const poolCount = draft?.preview?.upstreams ? Object.keys(draft.preview.upstreams).length : 0;

  return (
    <Card>
      <CardHeader className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle>Draft Configuration</CardTitle>
            {draft && (
              <>
                <Badge variant="default">draft rev-{draft.draft_revision}</Badge>
                <Badge variant={valid ? "success" : "destructive"}>
                  {valid ? "Ready to Publish" : `${errors.length} Blocking Error${errors.length === 1 ? "" : "s"}`}
                </Badge>
              </>
            )}
          </div>
          <CardDescription>
            Validate candidate routes, pools, and policies before cutting an immutable release snapshot.
          </CardDescription>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" variant="outline" size="sm" onClick={() => setLocalTick((t) => t + 1)}>
            <RefreshCw className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
            Re-validate
          </Button>
          {canPublish && (
            <Button
              type="button"
              size="sm"
              disabled={!draft || !valid}
              onClick={() => {
                setPublishErr(null);
                setAcknowledged(false);
                setConfirmOpen(true);
              }}
            >
              <Rocket className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
              Review &amp; Publish
            </Button>
          )}
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        {!canPublish && (
          <p className="rounded-md border border-[var(--border)] bg-[var(--muted)]/40 px-3 py-2 text-xs text-[var(--muted-foreground)]">
            Your role ({activeOrg?.role}) can inspect draft validation but cannot publish or activate releases
            (requires <code>admin</code> or <code>owner</code>).
          </p>
        )}

        {error ? (
          <ErrorBanner title="Could not load draft" message={error} onRetry={() => setLocalTick((t) => t + 1)} />
        ) : loading ? (
          <Skeleton className="h-24 w-full" />
        ) : draft ? (
          <div className="space-y-3">
            <div className="grid grid-cols-2 gap-3 text-xs sm:grid-cols-4">
              <div className="rounded-md border border-[var(--border)] p-2.5">
                <p className="text-[var(--muted-foreground)]">Draft Revision</p>
                <p className="mt-0.5 font-mono text-sm font-semibold">rev-{draft.draft_revision}</p>
              </div>
              <div className="rounded-md border border-[var(--border)] p-2.5">
                <p className="text-[var(--muted-foreground)]">Compiled Routes</p>
                <p className="mt-0.5 font-mono text-sm font-semibold">{routeCount}</p>
              </div>
              <div className="rounded-md border border-[var(--border)] p-2.5">
                <p className="text-[var(--muted-foreground)]">Upstream Pools</p>
                <p className="mt-0.5 font-mono text-sm font-semibold">{poolCount}</p>
              </div>
              <div className="rounded-md border border-[var(--border)] p-2.5">
                <p className="text-[var(--muted-foreground)]">Candidate Checksum</p>
                <p className="mt-0.5 truncate font-mono text-xs font-medium">
                  {draft.preview?.checksum?.slice(0, 14) || "—"}
                </p>
              </div>
            </div>

            {errors.length > 0 && (
              <div role="alert" className="space-y-1.5 rounded-md border border-red-200 bg-red-50 p-3 text-xs text-red-900">
                <p className="flex items-center gap-1.5 font-semibold">
                  <XCircle className="h-4 w-4 text-red-700" aria-hidden="true" />
                  Validation Errors — Publication Blocked (Last-Known-Good Preserved)
                </p>
                <ul className="list-disc space-y-0.5 pl-5">
                  {errors.map((err, i) => (
                    <li key={`${err.code}-${i}`}>
                      <span className="font-mono font-medium">{err.resource}</span>: {err.message} (<code>{err.code}</code>)
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {warnings.length > 0 && (
              <div className="space-y-1.5 rounded-md border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900">
                <p className="flex items-center gap-1.5 font-semibold">
                  <AlertTriangle className="h-4 w-4 text-amber-700" aria-hidden="true" />
                  Validation Warnings ({warnings.length})
                </p>
                <ul className="list-disc space-y-0.5 pl-5">
                  {warnings.map((w, i) => (
                    <li key={`${w.code}-${i}`}>
                      <span className="font-mono font-medium">{w.resource}</span>: {w.message}
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {valid && warnings.length === 0 && (
              <div className="flex items-center gap-2 rounded-md border border-emerald-200 bg-emerald-50 px-3 py-2 text-xs text-emerald-900">
                <CheckCircle2 className="h-4 w-4 text-emerald-700" aria-hidden="true" />
                Draft passed all semantic, SSRF, route-conflict, and policy schema checks.
              </div>
            )}

            {extraContent}
          </div>
        ) : null}
      </CardContent>

      <Dialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        title="Confirm Immutable Publication"
        description={
          draft
            ? `Publish draft rev-${draft.draft_revision} (checksum ${draft.preview?.checksum?.slice(0, 12) ?? ""}) as a new immutable release.`
            : undefined
        }
      >
        <form onSubmit={handlePublish} className="space-y-4">
          {publishErr && <ErrorBanner title="Publication Failed" message={publishErr} />}

          <div className="space-y-1.5">
            <Label htmlFor="publish-notes">Release Notes</Label>
            <Textarea
              id="publish-notes"
              rows={3}
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              placeholder="Summarize route, upstream, or policy changes in this release…"
            />
          </div>

          <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
            <input
              type="checkbox"
              checked={activateImmediately}
              onChange={(e) => setActivateImmediately(e.target.checked)}
              className="h-4 w-4 rounded border-[var(--border-strong)]"
            />
            Activate immediately across connected gateway instances
          </label>

          <label className="flex items-center gap-2 rounded-md border border-[var(--border)] bg-[var(--muted)]/40 p-2.5 text-xs text-[var(--foreground)]">
            <input
              type="checkbox"
              checked={acknowledged}
              onChange={(e) => setAcknowledged(e.target.checked)}
              className="h-4 w-4 rounded border-[var(--border-strong)]"
            />
            I have reviewed the validation report and structural changes and confirm publishing this immutable snapshot.
          </label>

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setConfirmOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!acknowledged || publishing}>
              {publishing ? "Publishing…" : "Confirm Publish"}
            </Button>
          </div>
        </form>
      </Dialog>
    </Card>
  );
}
