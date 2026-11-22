"use client";

import * as React from "react";
import { ChevronLeft, ChevronRight, Clock, Eye } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Dialog } from "@/components/ui/dialog";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import type { AuditEventItem } from "@/lib/api/types";

export interface RecentActivityPanelProps {
  events: AuditEventItem[];
  total: number;
  limit: number;
  offset: number;
  loading: boolean;
  error: string | null;
  onPageChange: (nextOffset: number) => void;
  onRetry?: () => void;
}

export function RecentActivityPanel({
  events,
  total,
  limit,
  offset,
  loading,
  error,
  onPageChange,
  onRetry,
}: RecentActivityPanelProps) {
  const [selected, setSelected] = React.useState<AuditEventItem | null>(null);
  const hasPrev = offset > 0;
  const hasNext = offset + limit < total;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <div>
          <CardTitle>Recent Activity</CardTitle>
          <CardDescription>Authorized audit &amp; publication events for this organization</CardDescription>
        </div>
        <span className="text-xs text-[var(--muted-foreground)] tabular-nums">
          {total.toLocaleString()} total
        </span>
      </CardHeader>
      <CardContent className="space-y-3">
        {error ? (
          <ErrorBanner title="Activity unavailable" message={error} onRetry={onRetry} />
        ) : loading ? (
          <div className="space-y-2.5">
            {Array.from({ length: 4 }).map((_, i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : events.length === 0 ? (
          <EmptyState
            title="No audit activity yet"
            description="Configuration changes, key lifecycle events, and publications will appear here automatically."
          />
        ) : (
          <>
            <ul className="divide-y divide-[var(--border)] rounded-md border border-[var(--border)]">
              {events.map((ev) => (
                <li
                  key={ev.id}
                  className="flex flex-wrap items-center justify-between gap-2 px-3.5 py-2.5 text-sm"
                >
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium text-[var(--foreground)]">{ev.action}</span>
                      <Badge variant={ev.outcome === "success" ? "success" : "warning"}>
                        {ev.outcome}
                      </Badge>
                      <span className="text-xs text-[var(--muted-foreground)] font-mono">
                        {ev.resource_type}
                      </span>
                    </div>
                    <p className="mt-0.5 flex items-center gap-1.5 text-xs text-[var(--muted-foreground)]">
                      <Clock className="h-3 w-3" aria-hidden="true" />
                      <span>{new Date(ev.created_at).toLocaleString()}</span>
                      {ev.request_id && <span className="font-mono">· {ev.request_id}</span>}
                    </p>
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => setSelected(ev)}
                    aria-label={`Inspect ${ev.action}`}
                  >
                    <Eye className="mr-1 h-3.5 w-3.5" aria-hidden="true" />
                    Details
                  </Button>
                </li>
              ))}
            </ul>

            <div className="flex items-center justify-between pt-1 text-xs text-[var(--muted-foreground)]">
              <span className="tabular-nums">
                Showing {offset + 1}–{Math.min(offset + events.length, total)} of {total}
              </span>
              <div className="flex items-center gap-1.5">
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!hasPrev}
                  onClick={() => onPageChange(Math.max(0, offset - limit))}
                >
                  <ChevronLeft className="h-3.5 w-3.5" aria-hidden="true" />
                  Prev
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  disabled={!hasNext}
                  onClick={() => onPageChange(offset + limit)}
                >
                  Next
                  <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />
                </Button>
              </div>
            </div>
          </>
        )}
      </CardContent>

      <Dialog
        open={selected !== null}
        onClose={() => setSelected(null)}
        title={selected ? `Event: ${selected.action}` : "Event Details"}
        description={selected ? `${selected.resource_type} · ${selected.outcome}` : undefined}
      >
        {selected && (
          <div className="space-y-3 text-xs">
            <dl className="grid grid-cols-2 gap-2 rounded-md bg-[var(--surface-subtle)] p-3">
              <div>
                <dt className="text-[var(--muted-foreground)]">Event ID</dt>
                <dd className="font-mono break-all">{selected.id}</dd>
              </div>
              <div>
                <dt className="text-[var(--muted-foreground)]">Request ID</dt>
                <dd className="font-mono break-all">{selected.request_id ?? "—"}</dd>
              </div>
              <div>
                <dt className="text-[var(--muted-foreground)]">Resource ID</dt>
                <dd className="font-mono break-all">{selected.resource_id ?? "—"}</dd>
              </div>
              <div>
                <dt className="text-[var(--muted-foreground)]">Timestamp</dt>
                <dd>{new Date(selected.created_at).toISOString()}</dd>
              </div>
            </dl>
            <div>
              <p className="mb-1 font-medium text-[var(--foreground)]">Redacted Details</p>
              <pre className="max-h-56 overflow-auto rounded-md border border-[var(--border)] bg-slate-950 p-3 text-xs text-slate-100">
                {JSON.stringify(selected.details ?? {}, null, 2)}
              </pre>
            </div>
          </div>
        )}
      </Dialog>
    </Card>
  );
}
