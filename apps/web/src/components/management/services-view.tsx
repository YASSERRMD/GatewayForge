"use client";

import * as React from "react";
import { ChevronLeft, ChevronRight, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, Select } from "@/components/ui/input";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { PaginatedResponse, ServiceItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const PAGE_SIZE = 10;

export type ServiceSort = "updated_desc" | "name_asc" | "name_desc";

export interface ServicesViewProps {
  onCreate?: () => void;
  onEdit?: (svc: ServiceItem) => void;
  onDelete?: (svc: ServiceItem) => void;
  reloadKey?: number;
}

export function ServicesView({ onCreate, onEdit, onDelete, reloadKey = 0 }: ServicesViewProps) {
  const { activeOrgId, activeOrg, clearSession } = useAuth();
  const canWrite = activeOrg?.role !== "viewer";

  const [search, setSearch] = React.useState("");
  const [sort, setSort] = React.useState<ServiceSort>("updated_desc");
  const [offset, setOffset] = React.useState(0);
  const [items, setItems] = React.useState<ServiceItem[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [localTick, setLocalTick] = React.useState(0);

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
    if (search.trim()) {
      qs.set("search", search.trim());
    }

    apiRequest<PaginatedResponse<ServiceItem>>(
      `/api/v1/orgs/${activeOrgId}/services?${qs.toString()}`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => {
        setItems(res.items ?? []);
        setTotal(res.total ?? 0);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load services.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, search, offset, reloadKey, localTick, onUnauthorized]);

  const sortedItems = React.useMemo(() => {
    const copy = [...items];
    if (sort === "name_asc") {
      copy.sort((a, b) => a.name.localeCompare(b.name));
    } else if (sort === "name_desc") {
      copy.sort((a, b) => b.name.localeCompare(a.name));
    }
    return copy;
  }, [items, sort]);

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Services</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Logical backend APIs managed in{" "}
            <span className="font-medium text-[var(--foreground)]">{activeOrg?.org_name}</span>.
          </p>
        </div>
        {canWrite && (
          <Button type="button" onClick={onCreate}>
            <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
            New Service
          </Button>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative flex-1 min-w-[220px]">
          <Search
            className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-[var(--muted-foreground)]"
            aria-hidden="true"
          />
          <Input
            aria-label="Search services"
            placeholder="Search by name or slug…"
            value={search}
            onChange={(e) => {
              setOffset(0);
              setSearch(e.target.value);
            }}
            className="pl-9"
          />
        </div>
        <Select
          aria-label="Sort services"
          value={sort}
          onChange={(e) => setSort(e.target.value as ServiceSort)}
          className="w-auto min-w-[180px]"
        >
          <option value="updated_desc">Recently updated</option>
          <option value="name_asc">Name (A–Z)</option>
          <option value="name_desc">Name (Z–A)</option>
        </Select>
      </div>

      {error ? (
        <ErrorBanner
          title="Could not load services"
          message={error}
          onRetry={() => setLocalTick((n) => n + 1)}
        />
      ) : loading ? (
        <div className="space-y-2.5">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-12 w-full" />
          ))}
        </div>
      ) : sortedItems.length === 0 ? (
        <EmptyState
          title={search.trim() ? "No matching services" : "No services yet"}
          description={
            search.trim()
              ? "Try a different search query or clear your filter."
              : "Create your first logical service to organize upstream pools and routes."
          }
          action={
            canWrite && !search.trim() ? (
              <Button type="button" onClick={onCreate}>
                <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
                Create Service
              </Button>
            ) : undefined
          }
        />
      ) : (
        <>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name &amp; Slug</TableHead>
                <TableHead>Description</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>Revision</TableHead>
                {canWrite && <TableHead className="text-right">Actions</TableHead>}
              </TableRow>
            </TableHeader>
            <TableBody>
              {sortedItems.map((svc) => (
                <TableRow key={svc.id}>
                  <TableCell>
                    <p className="font-medium text-[var(--foreground)]">{svc.name}</p>
                    <p className="font-mono text-xs text-[var(--muted-foreground)]">{svc.slug}</p>
                  </TableCell>
                  <TableCell className="max-w-xs truncate text-[var(--muted-foreground)]">
                    {svc.description || "—"}
                  </TableCell>
                  <TableCell>
                    <Badge variant={svc.enabled ? "success" : "default"}>
                      {svc.enabled ? "Enabled" : "Disabled"}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">
                    rev-{svc.revision}
                  </TableCell>
                  {canWrite && (
                    <TableCell className="text-right">
                      <div className="inline-flex items-center gap-1">
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={`Edit ${svc.name}`}
                          onClick={() => onEdit?.(svc)}
                        >
                          <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={`Delete ${svc.name}`}
                          onClick={() => onDelete?.(svc)}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-[var(--destructive)]" aria-hidden="true" />
                        </Button>
                      </div>
                    </TableCell>
                  )}
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="flex items-center justify-between text-xs text-[var(--muted-foreground)]">
            <span className="tabular-nums">
              Showing {offset + 1}–{Math.min(offset + sortedItems.length, total)} of {total}
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
    </div>
  );
}
