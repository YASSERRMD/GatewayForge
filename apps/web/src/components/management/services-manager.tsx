"use client";

import * as React from "react";
import { Button } from "@/components/ui/button";
import { ConflictResolutionBanner } from "@/components/ui/conflict-banner";
import { Dialog } from "@/components/ui/dialog";
import { FieldError, Input, Label, Textarea } from "@/components/ui/input";
import { ErrorBanner } from "@/components/ui/states";
import { ServicesView } from "@/components/management/services-view";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { ServiceItem } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

const SLUG_RE = /^[a-z0-9][a-z0-9-]{0,62}$/;

export function ServicesManager() {
  const { activeOrgId, csrfToken, clearSession } = useAuth();
  const [reloadKey, setReloadKey] = React.useState(0);

  const [editorOpen, setEditorOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<ServiceItem | null>(null);
  const [slug, setSlug] = React.useState("");
  const [name, setName] = React.useState("");
  const [description, setDescription] = React.useState("");
  const [enabled, setEnabled] = React.useState(true);

  const [slugErr, setSlugErr] = React.useState<string | null>(null);
  const [nameErr, setNameErr] = React.useState<string | null>(null);
  const [formErr, setFormErr] = React.useState<string | null>(null);
  const [conflictErr, setConflictErr] = React.useState<string | null>(null);
  const [saving, setSaving] = React.useState(false);

  const [deleting, setDeleting] = React.useState<ServiceItem | null>(null);
  const [deleteErr, setDeleteErr] = React.useState<string | null>(null);

  const openCreate = () => {
    setEditing(null);
    setSlug("");
    setName("");
    setDescription("");
    setEnabled(true);
    setSlugErr(null);
    setNameErr(null);
    setFormErr(null);
    setConflictErr(null);
    setEditorOpen(true);
  };

  const openEdit = (svc: ServiceItem) => {
    setEditing(svc);
    setSlug(svc.slug);
    setName(svc.name);
    setDescription(svc.description);
    setEnabled(svc.enabled);
    setSlugErr(null);
    setNameErr(null);
    setFormErr(null);
    setConflictErr(null);
    setEditorOpen(true);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId) return;
    setSlugErr(null);
    setNameErr(null);
    setFormErr(null);
    setConflictErr(null);

    let ok = true;
    if (!editing && !SLUG_RE.test(slug.trim())) {
      setSlugErr("Slug must be 1–63 lowercase letters, digits, or hyphens.");
      ok = false;
    }
    if (!name.trim() || name.trim().length > 120) {
      setNameErr("Name must be between 1 and 120 characters.");
      ok = false;
    }
    if (!ok) return;

    setSaving(true);
    try {
      if (editing) {
        await apiRequest<ServiceItem>(`/api/v1/orgs/${activeOrgId}/services/${editing.id}`, {
          method: "PATCH",
          csrfToken,
          ifMatch: `W/"rev-${editing.revision}"`,
          body: {
            name: name.trim(),
            description: description.trim(),
            enabled,
            revision: editing.revision,
          },
          onUnauthorized: () => clearSession(true),
        });
      } else {
        await apiRequest<ServiceItem>(`/api/v1/orgs/${activeOrgId}/services`, {
          method: "POST",
          csrfToken,
          body: {
            slug: slug.trim(),
            name: name.trim(),
            description: description.trim(),
          },
          onUnauthorized: () => clearSession(true),
        });
      }
      setEditorOpen(false);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      if (err instanceof ApiError) {
        if (err.isConflict) {
          setConflictErr(err.friendlyMessage());
        } else {
          setFormErr(err.friendlyMessage());
        }
      } else {
        setFormErr("Unexpected error while saving service.");
      }
    } finally {
      setSaving(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!activeOrgId || !deleting) return;
    setDeleteErr(null);
    setSaving(true);
    try {
      await apiRequest<void>(`/api/v1/orgs/${activeOrgId}/services/${deleting.id}`, {
        method: "DELETE",
        csrfToken,
        onUnauthorized: () => clearSession(true),
      });
      setDeleting(null);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      setDeleteErr(err instanceof ApiError ? err.friendlyMessage() : "Could not delete service.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <ServicesView
        reloadKey={reloadKey}
        onCreate={openCreate}
        onEdit={openEdit}
        onDelete={(svc) => {
          setDeleteErr(null);
          setDeleting(svc);
        }}
      />

      <Dialog
        open={editorOpen}
        onClose={() => setEditorOpen(false)}
        title={editing ? `Edit Service: ${editing.name}` : "Create Service"}
        description={
          editing
            ? `Updating revision rev-${editing.revision} with optimistic concurrency protection.`
            : "Define a new logical backend service in your draft configuration."
        }
      >
        <form onSubmit={handleSave} noValidate className="space-y-4">
          {conflictErr && (
            <ConflictResolutionBanner
              resourceLabel={editing ? `Service "${editing.name}"` : "Service"}
              staleRevision={editing?.revision}
              message={conflictErr}
              onReloadLatest={() => {
                if (!editing || !activeOrgId) {
                  setEditorOpen(false);
                  setReloadKey((k) => k + 1);
                  return;
                }
                void apiRequest<ServiceItem>(`/api/v1/orgs/${activeOrgId}/services/${editing.id}`, {
                  onUnauthorized: () => clearSession(true),
                })
                  .then((latest) => {
                    setEditing(latest);
                    setName(latest.name);
                    setDescription(latest.description);
                    setEnabled(latest.enabled);
                    setConflictErr(null);
                    setReloadKey((k) => k + 1);
                  })
                  .catch(() => {
                    setEditorOpen(false);
                    setReloadKey((k) => k + 1);
                  });
              }}
            />
          )}
          {formErr && <ErrorBanner title="Validation / Save Error" message={formErr} />}

          <div className="space-y-1.5">
            <Label htmlFor="svc-slug">Slug</Label>
            <Input
              id="svc-slug"
              value={slug}
              disabled={Boolean(editing)}
              onChange={(e) => setSlug(e.target.value.toLowerCase())}
              placeholder="orders-api"
            />
            <FieldError message={slugErr} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="svc-name">Display Name</Label>
            <Input
              id="svc-name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Orders API"
            />
            <FieldError message={nameErr} />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="svc-desc">Description</Label>
            <Textarea
              id="svc-desc"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Internal order processing API"
            />
          </div>

          <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
            <input
              type="checkbox"
              checked={enabled}
              onChange={(e) => setEnabled(e.target.checked)}
              className="h-4 w-4 rounded border-[var(--border-strong)]"
            />
            Service enabled for draft compilation
          </label>

          <div className="flex justify-end gap-2 pt-2">
            <Button type="button" variant="outline" onClick={() => setEditorOpen(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={saving}>
              {saving ? "Saving…" : editing ? "Save Changes" : "Create Service"}
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog
        open={deleting !== null}
        onClose={() => setDeleting(null)}
        title="Delete Service"
        description={deleting ? `Remove "${deleting.name}" (${deleting.slug}) from draft configuration?` : undefined}
      >
        <div className="space-y-4">
          {deleteErr && <ErrorBanner title="Cannot delete service" message={deleteErr} />}
          <p className="text-sm text-[var(--muted-foreground)]">
            This action soft-deletes the service and updates the draft revision. Active routes referencing
            this service must be removed first.
          </p>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setDeleting(null)}>
              Cancel
            </Button>
            <Button type="button" variant="destructive" disabled={saving} onClick={handleConfirmDelete}>
              {saving ? "Deleting…" : "Confirm Delete"}
            </Button>
          </div>
        </div>
      </Dialog>
    </>
  );
}
