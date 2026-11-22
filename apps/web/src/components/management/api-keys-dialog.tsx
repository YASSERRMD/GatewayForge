"use client";

import * as React from "react";
import { Check, Copy, RefreshCw, ShieldAlert, Trash2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { Input, Label } from "@/components/ui/input";
import { ErrorBanner, Skeleton } from "@/components/ui/states";
import { ApiError, apiRequest } from "@/lib/api/client";
import type { APIKeyMetadata, ConsumerItem, IssuedAPIKey } from "@/lib/api/types";
import { useAuth } from "@/lib/auth-context";

export interface ApiKeysDialogProps {
  consumer: ConsumerItem | null;
  onClose: () => void;
}

interface RotateResponse {
  revoked_key_id: string;
  replacement: IssuedAPIKey;
}

export function ApiKeysDialog({ consumer, onClose }: ApiKeysDialogProps) {
  const { activeOrgId, activeOrg, csrfToken, clearSession } = useAuth();
  const canManage = activeOrg?.role === "owner" || activeOrg?.role === "admin" || activeOrg?.role === "developer";

  const [keys, setKeys] = React.useState<APIKeyMetadata[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);
  const [reloadTick, setReloadTick] = React.useState(0);

  const [label, setLabel] = React.useState("");
  const [scopesText, setScopesText] = React.useState("read");
  const [busy, setBusy] = React.useState(false);

  // Ephemeral in-memory only; wiped on dismiss, dialog close, or unmount. Never persisted.
  const [oneTimeSecret, setOneTimeSecret] = React.useState<string | null>(null);
  const [copied, setCopied] = React.useState(false);

  const onUnauthorized = React.useCallback(() => clearSession(true), [clearSession]);

  // Wipe any previously revealed secret whenever the selected consumer changes or dialog closes/unmounts.
  const consumerId = consumer?.id ?? null;
  React.useEffect(() => {
    setOneTimeSecret(null);
    setCopied(false);
    setError(null);
    return () => {
      setOneTimeSecret(null);
    };
  }, [consumerId]);

  React.useEffect(() => {
    if (!consumer || !activeOrgId) {
      setKeys([]);
      return;
    }
    const ctrl = new AbortController();
    setLoading(true);
    apiRequest<{ items: APIKeyMetadata[]; total: number }>(
      `/api/v1/orgs/${activeOrgId}/consumers/${consumer.id}/keys`,
      { signal: ctrl.signal, onUnauthorized }
    )
      .then((res) => setKeys(res.items ?? []))
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof ApiError ? err.friendlyMessage() : "Could not load API keys.");
      })
      .finally(() => setLoading(false));

    return () => {
      ctrl.abort();
    };
  }, [consumer, activeOrgId, reloadTick, onUnauthorized]);

  const handleClose = () => {
    setOneTimeSecret(null);
    setCopied(false);
    onClose();
  };

  const handleIssue = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!consumer || !activeOrgId) return;
    setError(null);
    setBusy(true);
    const scopes = scopesText
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);

    try {
      const issued = await apiRequest<IssuedAPIKey>(
        `/api/v1/orgs/${activeOrgId}/consumers/${consumer.id}/keys`,
        {
          method: "POST",
          csrfToken,
          body: { label: label.trim() || "default", scopes },
          onUnauthorized,
        }
      );
      setOneTimeSecret(issued.api_key);
      setCopied(false);
      setLabel("");
      setReloadTick((t) => t + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not issue API key.");
    } finally {
      setBusy(false);
    }
  };

  const handleRotate = async (keyId: string) => {
    if (!activeOrgId) return;
    setError(null);
    setBusy(true);
    try {
      const res = await apiRequest<RotateResponse>(`/api/v1/orgs/${activeOrgId}/keys/${keyId}/rotate`, {
        method: "POST",
        csrfToken,
        onUnauthorized,
      });
      setOneTimeSecret(res.replacement.api_key);
      setCopied(false);
      setReloadTick((t) => t + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not rotate API key.");
    } finally {
      setBusy(false);
    }
  };

  const handleRevoke = async (keyId: string) => {
    if (!activeOrgId) return;
    setError(null);
    setBusy(true);
    try {
      await apiRequest<APIKeyMetadata>(`/api/v1/orgs/${activeOrgId}/keys/${keyId}/revoke`, {
        method: "POST",
        csrfToken,
        onUnauthorized,
      });
      setReloadTick((t) => t + 1);
    } catch (err: unknown) {
      setError(err instanceof ApiError ? err.friendlyMessage() : "Could not revoke API key.");
    } finally {
      setBusy(false);
    }
  };

  const handleCopy = async () => {
    if (!oneTimeSecret) return;
    try {
      await navigator.clipboard.writeText(oneTimeSecret);
      setCopied(true);
    } catch {
      // Clipboard API may be unavailable in non-HTTPS contexts
    }
  };

  return (
    <Dialog
      open={consumer !== null}
      onClose={handleClose}
      title={consumer ? `API Keys: ${consumer.name}` : "API Keys"}
      description="Keys are hashed with HMAC-SHA256 on the server. Plaintext credentials are revealed exactly once on issuance or rotation."
    >
      <div className="space-y-4">
        {error && <ErrorBanner title="API Key Operation Failed" message={error} />}

        {oneTimeSecret && (
          <div
            role="alert"
            data-testid="one-time-secret-box"
            className="space-y-2.5 rounded-lg border border-amber-300 bg-amber-50 p-3.5 text-xs text-amber-950"
          >
            <div className="flex items-start gap-2">
              <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
              <div>
                <p className="font-semibold">Copy this API key now — it will never be shown again</p>
                <p className="text-amber-900">
                  GatewayForge stores only a non-reversible cryptographic hash. Closing or dismissing wipes this value
                  from memory.
                </p>
              </div>
            </div>
            <div className="flex items-center gap-2">
              <code
                data-testid="one-time-secret-value"
                className="flex-1 overflow-x-auto rounded border border-amber-200 bg-white px-2.5 py-1.5 font-mono text-xs select-all text-slate-900"
              >
                {oneTimeSecret}
              </code>
              <Button type="button" variant="outline" size="sm" onClick={handleCopy}>
                {copied ? <Check className="mr-1 h-3.5 w-3.5" aria-hidden="true" /> : <Copy className="mr-1 h-3.5 w-3.5" aria-hidden="true" />}
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
            <div className="flex justify-end">
              <Button type="button" size="sm" variant="outline" onClick={() => setOneTimeSecret(null)}>
                I have stored this key securely
              </Button>
            </div>
          </div>
        )}

        {canManage && (
          <form onSubmit={handleIssue} className="grid grid-cols-1 gap-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/40 p-3 sm:grid-cols-3">
            <div className="space-y-1">
              <Label htmlFor="key-label">Key Label</Label>
              <Input
                id="key-label"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                placeholder="prod-ingest"
              />
            </div>
            <div className="space-y-1">
              <Label htmlFor="key-scopes">Scopes (comma-separated)</Label>
              <Input
                id="key-scopes"
                value={scopesText}
                onChange={(e) => setScopesText(e.target.value)}
                placeholder="read, write"
              />
            </div>
            <div className="flex items-end">
              <Button type="submit" disabled={busy} className="w-full">
                {busy ? "Issuing…" : "Issue API Key"}
              </Button>
            </div>
          </form>
        )}

        <div className="space-y-2">
          <h3 className="text-xs font-semibold uppercase tracking-wider text-[var(--muted-foreground)]">
            Issued Credentials (Metadata Only)
          </h3>
          {loading ? (
            <Skeleton className="h-16 w-full" />
          ) : keys.length === 0 ? (
            <p className="text-xs text-[var(--muted-foreground)]">No API keys have been issued for this consumer yet.</p>
          ) : (
            <ul className="divide-y divide-[var(--border)] rounded-lg border border-[var(--border)]">
              {keys.map((k) => {
                const revoked = Boolean(k.revoked_at);
                return (
                  <li key={k.id} className="flex flex-wrap items-center justify-between gap-2 px-3 py-2.5 text-xs">
                    <div className="space-y-0.5">
                      <div className="flex items-center gap-2">
                        <span className="font-mono font-medium text-[var(--foreground)]">{k.key_prefix}…</span>
                        <Badge variant={revoked ? "destructive" : "success"}>
                          {revoked ? "Revoked" : "Active"}
                        </Badge>
                        {k.label && <span className="text-[var(--muted-foreground)]">{k.label}</span>}
                      </div>
                      <div className="flex flex-wrap gap-1 text-[var(--muted-foreground)]">
                        <span>Scopes: {k.scopes?.length ? k.scopes.join(", ") : "none"}</span>
                      </div>
                    </div>
                    {canManage && !revoked && (
                      <div className="flex items-center gap-1">
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          disabled={busy}
                          aria-label={`Rotate key ${k.key_prefix}`}
                          onClick={() => handleRotate(k.id)}
                        >
                          <RefreshCw className="mr-1 h-3 w-3" aria-hidden="true" />
                          Rotate
                        </Button>
                        <Button
                          type="button"
                          variant="ghost"
                          size="sm"
                          disabled={busy}
                          aria-label={`Revoke key ${k.key_prefix}`}
                          onClick={() => handleRevoke(k.id)}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-[var(--destructive)]" aria-hidden="true" />
                        </Button>
                      </div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      </div>
    </Dialog>
  );
}
