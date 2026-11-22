"use client";

import * as React from "react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorBanner } from "@/components/ui/states";
import { ConfigDiffViewer } from "@/components/releases/config-diff-viewer";
import { DraftPublishPanel } from "@/components/releases/draft-publish-panel";
import { GatewayConvergencePanel } from "@/components/releases/gateway-convergence-panel";
import {
  ReleaseHistoryTable,
  type PublicationRecord,
} from "@/components/releases/release-history-table";
import { ApiError, apiRequest } from "@/lib/api/client";
import { useAuth } from "@/lib/auth-context";

interface PendingActivation {
  mode: "activate" | "rollback";
  pub: PublicationRecord;
  currentActiveVersion: number;
  expectedActivationRevision: number;
}

export interface ReleasesWorkspaceProps {
  convergenceSlot?: React.ReactNode;
}

export function ReleasesWorkspace({ convergenceSlot }: ReleasesWorkspaceProps) {
  const { activeOrgId, csrfToken, clearSession } = useAuth();
  const [reloadKey, setReloadKey] = React.useState(0);
  const [pending, setPending] = React.useState<PendingActivation | null>(null);
  const [reason, setReason] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [conflictErr, setConflictErr] = React.useState<string | null>(null);
  const [actionErr, setActionErr] = React.useState<string | null>(null);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  const openAction = (
    mode: "activate" | "rollback",
    pub: PublicationRecord,
    currentActiveVersion: number,
    expectedActivationRevision: number
  ) => {
    setConflictErr(null);
    setActionErr(null);
    setReason(
      mode === "rollback"
        ? `Rollback from v${currentActiveVersion} to v${pub.version}`
        : `Activate immutable release v${pub.version}`
    );
    setPending({ mode, pub, currentActiveVersion, expectedActivationRevision });
  };

  const handleConfirmActivate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!activeOrgId || !pending) return;
    setConflictErr(null);
    setActionErr(null);
    setBusy(true);

    try {
      await apiRequest(`/api/v1/orgs/${activeOrgId}/publications/${pending.pub.version}/activate`, {
        method: "POST",
        csrfToken,
        body: {
          expected_activation_revision: pending.expectedActivationRevision,
          reason: reason.trim(),
        },
        onUnauthorized,
      });
      setPending(null);
      setReloadKey((k) => k + 1);
    } catch (err: unknown) {
      if (err instanceof ApiError && err.isConflict) {
        setConflictErr(err.friendlyMessage());
      } else {
        setActionErr(err instanceof ApiError ? err.friendlyMessage() : "Activation failed.");
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-[var(--foreground)]">Releases</h1>
        <p className="text-sm text-[var(--muted-foreground)]">
          Validate drafts, review semantic configuration diffs, publish immutable snapshots, monitor gateway convergence, and execute audited rollbacks.
        </p>
      </div>

      {convergenceSlot ?? <GatewayConvergencePanel reloadKey={reloadKey} />}

      <DraftPublishPanel
        reloadKey={reloadKey}
        onPublished={() => setReloadKey((k) => k + 1)}
        extraContent={<ConfigDiffViewer reloadKey={reloadKey} />}
      />

      <ReleaseHistoryTable
        reloadKey={reloadKey}
        onActivate={(pub, curVer, curRev) => openAction("activate", pub, curVer, curRev)}
        onRollback={(pub, curVer, curRev) => openAction("rollback", pub, curVer, curRev)}
      />

      <Dialog
        open={pending !== null}
        onClose={() => setPending(null)}
        title={
          pending
            ? pending.mode === "rollback"
              ? `Confirm Rollback to v${pending.pub.version}`
              : `Confirm Activation of v${pending.pub.version}`
            : "Confirm Release Action"
        }
        description={
          pending
            ? `Target checksum ${pending.pub.checksum.slice(0, 16)}… · Protected by optimistic concurrency (activation rev-${pending.expectedActivationRevision}).`
            : undefined
        }
      >
        <form onSubmit={handleConfirmActivate} className="space-y-4">
          {conflictErr && (
            <ErrorBanner
              title="Concurrent Activation Conflict (409)"
              message={conflictErr}
              onRetry={() => {
                setPending(null);
                setReloadKey((k) => k + 1);
              }}
            />
          )}
          {actionErr && <ErrorBanner title="Activation Error" message={actionErr} />}

          <div className="space-y-1.5">
            <Label htmlFor="activation-reason">Audit Reason</Label>
            <Input
              id="activation-reason"
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Explain why this release is being activated or rolled back…"
            />
          </div>

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setPending(null)}>
              Cancel
            </Button>
            <Button
              type="submit"
              variant={pending?.mode === "rollback" ? "destructive" : "default"}
              disabled={busy}
            >
              {busy
                ? "Applying…"
                : pending?.mode === "rollback"
                  ? `Confirm Rollback to v${pending.pub.version}`
                  : `Confirm Activate v${pending?.pub.version ?? ""}`}
            </Button>
          </div>
        </form>
      </Dialog>
    </div>
  );
}
