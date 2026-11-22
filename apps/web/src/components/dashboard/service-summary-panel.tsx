"use client";

import * as React from "react";
import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import type { RouteItem, ServiceItem } from "@/lib/api/types";

export interface ServiceSummaryPanelProps {
  services: ServiceItem[];
  routes: RouteItem[];
  loading: boolean;
  error: string | null;
  onRetry?: () => void;
}

export function ServiceSummaryPanel({
  services,
  routes,
  loading,
  error,
  onRetry,
}: ServiceSummaryPanelProps) {
  const routesByService = React.useMemo(() => {
    const map = new Map<string, RouteItem[]>();
    for (const rt of routes) {
      const list = map.get(rt.service_id) ?? [];
      list.push(rt);
      map.set(rt.service_id, list);
    }
    return map;
  }, [routes]);

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <div>
          <CardTitle>Services &amp; Routes Summary</CardTitle>
          <CardDescription>Configured services and active routing rules in this organization</CardDescription>
        </div>
        <Link
          href="/services"
          className="inline-flex items-center gap-1 text-xs font-medium text-[var(--accent)] hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)] rounded"
        >
          Manage services
          <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
        </Link>
      </CardHeader>
      <CardContent>
        {error ? (
          <ErrorBanner title="Services unavailable" message={error} onRetry={onRetry} />
        ) : loading ? (
          <div className="space-y-2.5">
            {Array.from({ length: 3 }).map((_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : services.length === 0 ? (
          <EmptyState
            title="No services configured"
            description="Add a service and attach upstream pools and routes to start routing traffic through GatewayForge."
          />
        ) : (
          <div className="space-y-4">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Service</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Routes</TableHead>
                  <TableHead>Revision</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {services.slice(0, 6).map((svc) => {
                  const attached = routesByService.get(svc.id) ?? [];
                  return (
                    <TableRow key={svc.id}>
                      <TableCell>
                        <p className="font-medium text-[var(--foreground)]">{svc.name}</p>
                        <p className="text-xs font-mono text-[var(--muted-foreground)]">{svc.slug}</p>
                      </TableCell>
                      <TableCell>
                        <Badge variant={svc.enabled ? "success" : "default"}>
                          {svc.enabled ? "Enabled" : "Disabled"}
                        </Badge>
                      </TableCell>
                      <TableCell className="tabular-nums">{attached.length}</TableCell>
                      <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">
                        rev-{svc.revision}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>

            {routes.length > 0 && (
              <div className="space-y-1.5">
                <p className="text-xs font-medium uppercase tracking-wide text-[var(--muted-foreground)]">
                  Top Configured Routes
                </p>
                <ul className="divide-y divide-[var(--border)] rounded-md border border-[var(--border)] text-xs">
                  {routes.slice(0, 5).map((rt) => (
                    <li
                      key={rt.id}
                      className="flex flex-wrap items-center justify-between gap-2 px-3 py-2"
                    >
                      <div className="flex flex-wrap items-center gap-2 font-mono">
                        <Badge variant="default">{rt.methods.join(",")}</Badge>
                        <span className="text-[var(--foreground)]">
                          {rt.host}
                          {rt.path_prefix}
                        </span>
                      </div>
                      <span className="text-[var(--muted-foreground)]">
                        priority {rt.priority}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}
          </div>
        )}
      </CardContent>
    </Card>
  );
}
