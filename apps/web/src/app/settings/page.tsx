"use client";

import * as React from "react";
import { AppShell } from "@/components/app-shell";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ErrorBanner, Skeleton } from "@/components/ui/states";
import { ApiError, apiRequest } from "@/lib/api/client";
import { useAuth } from "@/lib/auth-context";

interface MemberRow {
  user_id: string;
  email: string;
  display_name: string;
  role: string;
}

function SettingsContent() {
  const { activeOrgId, activeOrg, clearSession } = useAuth();
  const [members, setMembers] = React.useState<MemberRow[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    apiRequest<{ items: MemberRow[] }>(`/api/v1/orgs/${activeOrgId}/members?limit=50&offset=0`, {
      signal: ctrl.signal,
      onUnauthorized: () => clearSession(true),
    })
      .then((res) => setMembers(res.items ?? []))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load organization members.");
      })
      .finally(() => setLoading(false));
    return () => ctrl.abort();
  }, [activeOrgId, clearSession]);

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Organization Settings</h1>
        <p className="text-sm text-[var(--muted-foreground)]">
          Tenant identity, role assignments, and RBAC boundaries for{" "}
          <span className="font-medium text-[var(--foreground)]">{activeOrg?.org_name}</span>.
        </p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Organization Members &amp; Roles</CardTitle>
          <CardDescription>
            Permissions follow least privilege: viewer (read-only), developer (draft &amp; console), admin (releases &amp; keys), owner (org lifecycle).
          </CardDescription>
        </CardHeader>
        <CardContent>
          {error ? (
            <ErrorBanner title="Members Unavailable" message={error} />
          ) : loading ? (
            <Skeleton className="h-20 w-full" />
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Email</TableHead>
                  <TableHead>Role</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {members.map((m) => (
                  <TableRow key={m.user_id}>
                    <TableCell className="font-medium">{m.display_name}</TableCell>
                    <TableCell className="text-sm text-[var(--muted-foreground)]">{m.email}</TableCell>
                    <TableCell>
                      <Badge variant={m.role === "owner" || m.role === "admin" ? "success" : "default"}>
                        {m.role}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

export default function SettingsPage() {
  return (
    <AppShell>
      <SettingsContent />
    </AppShell>
  );
}
