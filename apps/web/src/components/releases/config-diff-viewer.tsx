"use client";

import * as React from "react";
import { Badge } from "@/components/ui/badge";
import { ErrorBanner, Skeleton } from "@/components/ui/states";
import { ApiError, apiRequest } from "@/lib/api/client";
import { useAuth } from "@/lib/auth-context";

export interface DiffEntry {
  resource_type: string;
  resource_id: string;
  name: string;
  change_type: "added" | "modified" | "removed";
  details: string[];
}

export interface ConfigDiffResponse {
  base_version: number;
  has_changes: boolean;
  summary: {
    added: number;
    modified: number;
    removed: number;
  };
  changes: DiffEntry[];
}

export function ConfigDiffViewer({ reloadKey = 0 }: { reloadKey?: number }) {
  const { activeOrgId, clearSession } = useAuth();
  const [diff, setDiff] = React.useState<ConfigDiffResponse | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);
    apiRequest<ConfigDiffResponse>(`/api/v1/orgs/${activeOrgId}/draft/diff`, {
      signal: ctrl.signal,
      onUnauthorized,
    })
      .then((res) => setDiff(res))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not compute configuration diff.");
      })
      .finally(() => setLoading(false));
    return () => ctrl.abort();
  }, [activeOrgId, reloadKey, onUnauthorized]);

  if (error) {
    return <ErrorBanner title="Config Diff Unavailable" message={error} />;
  }
  if (loading) {
    return <Skeleton className="h-16 w-full" />;
  }
  if (!diff) return null;

  return (
    <div className="space-y-2 rounded-lg border border-[var(--border)] bg-[var(--muted)]/20 p-3.5">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs font-semibold uppercase tracking-wider text-[var(--muted-foreground)]">
          Structural Diff vs {diff.base_version > 0 ? `Active Release v${diff.base_version}` : "Empty Baseline"}
        </p>
        <div className="flex items-center gap-1.5 text-xs">
          <Badge variant="success">+{diff.summary.added} added</Badge>
          <Badge variant="warning">~{diff.summary.modified} modified</Badge>
          <Badge variant="destructive">-{diff.summary.removed} removed</Badge>
        </div>
      </div>

      {!diff.has_changes ? (
        <p className="text-xs text-[var(--muted-foreground)]">
          Draft matches the currently active release snapshot with zero structural differences.
        </p>
      ) : (
        <ul className="divide-y divide-[var(--border)] rounded-md border border-[var(--border)] bg-[var(--card)]">
          {diff.changes.map((c) => (
            <li key={`${c.resource_type}-${c.resource_id}-${c.change_type}`} className="px-3 py-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <Badge
                  variant={
                    c.change_type === "added"
                      ? "success"
                      : c.change_type === "removed"
                        ? "destructive"
                        : "warning"
                  }
                >
                  {c.change_type}
                </Badge>
                <span className="font-mono text-[11px] uppercase text-[var(--muted-foreground)]">
                  {c.resource_type}
                </span>
                <span className="font-medium text-[var(--foreground)]">{c.name}</span>
              </div>
              {c.details.length > 0 && (
                <p className="mt-1 font-mono text-[11px] text-[var(--muted-foreground)]">
                  {c.details.join(" · ")}
                </p>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
