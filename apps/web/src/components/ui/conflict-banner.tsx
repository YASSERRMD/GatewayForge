"use client";

import * as React from "react";
import { AlertTriangle, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";

export interface ConflictResolutionBannerProps {
  resourceLabel: string;
  staleRevision?: number;
  message: string;
  onReloadLatest: () => void;
  onDismiss?: () => void;
}

export function ConflictResolutionBanner({
  resourceLabel,
  staleRevision,
  message,
  onReloadLatest,
  onDismiss,
}: ConflictResolutionBannerProps) {
  return (
    <div
      role="alert"
      data-testid="conflict-resolution-banner"
      className="space-y-2.5 rounded-lg border border-amber-300 bg-amber-50 p-3.5 text-xs text-amber-950"
    >
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
        <div className="space-y-0.5">
          <p className="font-semibold">
            Stale Revision Conflict (409) — {resourceLabel}
            {staleRevision !== undefined ? ` (loaded rev-${staleRevision})` : ""}
          </p>
          <p className="text-amber-900">{message}</p>
          <p className="text-amber-800">
            Your changes were not forced over the peer update. Reload the latest server revision to review before
            re-applying your changes.
          </p>
        </div>
      </div>
      <div className="flex flex-wrap justify-end gap-2">
        {onDismiss && (
          <Button type="button" variant="ghost" size="sm" onClick={onDismiss}>
            Keep Editing Locally
          </Button>
        )}
        <Button type="button" variant="outline" size="sm" onClick={onReloadLatest}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
          Reload Latest Server State
        </Button>
      </div>
    </div>
  );
}
