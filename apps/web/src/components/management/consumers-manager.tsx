"use client";

import * as React from "react";
import { KeyRound, PauseCircle, PlayCircle, Plus, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { FieldError, Input, Label, Textarea } from "@/components/ui/input";
import { EmptyState, ErrorBanner, Skeleton } from "@/components/ui/states";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { ConsumerItem, PaginatedResponse } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

export interface ConsumersManagerProps {
  renderKeysDialog?: (consumer: ConsumerItem | null, onClose: () => void) => React.ReactNode;
}

export function ConsumersManager({ renderKeysDialog }: ConsumersManagerProps) {
  const { activeOrgId, activeOrg, csrfToken, clearSession } = useAuth();
  const canWrite = activeOrg?.role !== "viewer";
  const canManageKeys = activeOrg?.role === "owner" || activeOrg?.role === "admin" || activeOrg?.role === "developer";

  const [items, setItems] = React.useState<ConsumerItem[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadKey, setReloadKey] = React.useState(0);

  const [createOpen, setCreateOpen] = React.useState(false);
  const [name, setName] = React.useState("");
  const [ownerEmail, setOwnerEmail] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [nameErr, setNameErr] = React.useState<string | null>(null);
  const [formErr, setFormErr] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  const [deleting, setDeleting] = React.useState<ConsumerItem | null>(null);
  const [deleteErr, setDeleteErr] = React.useState<string | null>(null);
  const [keysConsumer, setKeysConsumer] = React.useState<ConsumerItem | null>(null);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  React.useEffect(() => {
    if (!activeOrgId) return;
    const ctrl = new AbortController();
    setLoading(true);
    setError(null);

    apiRequest<PaginatedResponse<ConsumerItem>>(
      `/api/v1/orgs/${activeOrgId}/consumers?limit=50&offset=0`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => setItems(res.items ?? []))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load consumers.");
      })
      .finally(() => setLoading(false));

    return () => ctrl.abort();
  }, [activeOrgId, reloadKey, onUnauthorized]);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId) return;
    setNameErr(null);
    setFormErr(null);
    if (!name.trim() || name.trim().length > 120) {
      setNameErr("Consumer name must be 1–120 characters.");
      return;
    }

    setSaving(true);
    try {
      await apiRequest<ConsumerItem>(`/api/v1/orgs/${activeOrgId}/consumers`, {
        method: "POST",
        csrfToken,
        body: {
          name: name.trim(),
          owner_email: ownerEmail.trim(),
          description: description.trim(),
          status: "active",
        },
        onUnauthorized,
      });
      setCreateOpen(false);
      setName("");
      setOwnerEmail("");
      setDescription("");
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setFormErr(err instanceof ApiError ? err.friendlyMessage() : "Could not create consumer.");
    } finally {
      setSaving(false);
    }
  };

  const handleToggleStatus = async (c: ConsumerItem) => {
    if (!activeOrgId) return;
    setError(null);
    const nextStatus = c.status === "active" ? "suspended" : "active";
    try {
      await apiRequest<ConsumerItem>(`/api/v1/orgs/${activeOrgId}/consumers/${c.id}`, {
        method: "PATCH",
        csrfToken,
        ifMatch: `W/"rev-${c.revision}"`,
        body: {
          status: nextStatus,
          revision: c.revision,
        },
        onUnauthorized,
      });
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not update consumer status.");
    }
  };

  const handleDelete = async () => {
    if (!activeOrgId || !deleting) return;
    setDeleteErr(null);
    setSaving(true);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/consumers/${deleting.id}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized,
      });
      setDeleting(null);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setDeleteErr(err instanceof ApiError ? err.friendlyMessage() : "Could not delete consumer.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Consumers</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Tenant-scoped API clients, lifecycle status, and scoped credentials in{" "}
            <span className="font-medium text-[var(--foreground)]">{activeOrg?.org_name}</span>.
          </p>
        </div>
        {canWrite && (
          <Button
            type="button"
            onClick={() => {
              setNameErr(null);
              setFormErr(null);
              setCreateOpen(true);
            }}
          >
            <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
            New Consumer
          </Button>
        )}
      </div>

      {error && <ErrorBanner title="Consumer operation failed" message={error} onRetry={() => setReloadKey((k) => k + 1)} />}

      {loading ? (
        <div className="space-y-2.5">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : items.length === 0 ? (
        <EmptyState
          title="No consumers registered"
          description="Register API consumers to issue scoped, hashed API keys and apply per-consumer rate limits."
          action={
            canWrite ? (
              <Button type="button" onClick={() => setCreateOpen(true)}>
                <Plus className="mr-1.5 h-4 w-4" aria-hidden="true" />
                Create Consumer
              </Button>
            ) : undefined
          }
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Consumer</TableHead>
              <TableHead>Owner Email</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Revision</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.map((c) => (
              <TableRow key={c.id}>
                <TableCell>
                  <p className="font-medium text-[var(--foreground)]">{c.name}</p>
                  <p className="text-xs text-[var(--muted-foreground)]">{c.description || "—"}</p>
                </TableCell>
                <TableCell className="text-sm text-[var(--muted-foreground)]">{c.owner_email || "—"}</TableCell>
                <TableCell>
                  <Badge variant={c.status === "active" ? "success" : "warning"}>
                    {c.status === "active" ? "Active" : "Suspended"}
                  </Badge>
                </TableCell>
                <TableCell className="font-mono text-xs text-[var(--muted-foreground)]">rev-{c.revision}</TableCell>
                <TableCell className="text-right">
                  <div className="inline-flex items-center gap-1">
                    {renderKeysDialog && (
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        aria-label={`Manage API Keys for ${c.name}`}
                        onClick={() => setKeysConsumer(c)}
                      >
                        <KeyRound className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
                        {canManageKeys ? "API Keys" : "View Keys"}
                      </Button>
                    )}
                    {canWrite && (
                      <>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={c.status === "active" ? `Suspend ${c.name}` : `Activate ${c.name}`}
                          onClick={() => handleToggleStatus(c)}
                        >
                          {c.status === "active" ? (
                            <PauseCircle className="h-4 w-4 text-amber-600" aria-hidden="true" />
                          ) : (
                            <PlayCircle className="h-4 w-4 text-emerald-600" aria-hidden="true" />
                          )}
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          aria-label={`Delete ${c.name}`}
                          onClick={() => {
                            setDeleteErr(null);
                            setDeleting(c);
                          }}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-[var(--destructive)]" aria-hidden="true" />
                        </Button>
                      </>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}

      <Dialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        title="Create Consumer"
        description="Register a new API consumer in this organization."
      >
        <form onSubmit={handleCreate} noValidate className="space-y-4">
          {formErr && <ErrorBanner title="Validation / Save Error" message={formErr} />}
          <div className="space-y-1.5">
            <Label htmlFor="consumer-name">Consumer Name</Label>
            <Input
              id="consumer-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="partner-checkout-app"
            />
            <FieldError message={nameErr} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="consumer-email">Owner Email</Label>
            <Input
              id="consumer-email"
              type="email"
              value={ownerEmail}
              onChange={(e) => setOwnerEmail(e.target.value)}
              placeholder="platform@partner.example"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="consumer-desc">Description</Label>
            <Textarea
              id="consumer-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Checkout storefront client"
            />
          </div>
          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setCreateOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Creating…" : "Create Consumer"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title="Delete Consumer"
        description={deleting ? `Delete consumer "${deleting.name}" and revoke access?` : undefined}
      >
        <div className="space-y-4">
          {deleteErr && <ErrorBanner title="Cannot delete consumer" message={deleteErr} />}
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={saving} onClick={handleDelete}>
              {saving ? "Deleting…" : "Confirm Delete"}
            </Button>
          </div>
        </div>
      </Dialog>

      {renderKeysDialog?.(keysConsumer, () => setKeysConsumer(null))}
    </div>
  );
}
