"use client";

import * as React from "react";
import { AlertTriangle, ShieldAlert } from "lucide-react";
import { FieldError, Input, Label } from "@/components/ui/input";

export type ResiliencePolicyType = "timeout" | "retry" | "circuit_breaker" | "cors";

export const IDEMPOTENT_RETRY_METHODS = ["GET", "HEAD", "OPTIONS", "PUT", "DELETE"] as const;

export function defaultTimeoutConfig(): Record<string, unknown> {
  return {
    connect_timeout_ms: 2000,
    total_timeout_ms: 10000,
  };
}

export function defaultRetryConfig(): Record<string, unknown> {
  return {
    max_attempts: 3,
    base_backoff_ms: 50,
    max_backoff_ms: 1000,
    jitter_fraction: 0.25,
    retryable_methods: ["GET", "HEAD", "OPTIONS"],
    retryable_statuses: [502, 503, 504],
  };
}

export function defaultCircuitBreakerConfig(): Record<string, unknown> {
  return {
    failure_threshold: 5,
    success_threshold: 2,
    open_duration_ms: 15000,
    half_open_max_in_flight: 2,
  };
}

export function defaultCorsConfig(): Record<string, unknown> {
  return {
    allowed_origins: ["http://localhost:3000"],
    allowed_methods: ["GET", "POST", "OPTIONS"],
    allow_credentials: false,
  };
}

export function validateResiliencePolicyConfig(
  policyType: ResiliencePolicyType,
  config: Record<string, unknown>
): Record<string, string> {
  const errs: Record<string, string> = {};

  if (policyType === "timeout") {
    const ct = Number(config.connect_timeout_ms ?? 0);
    const tt = Number(config.total_timeout_ms ?? 0);
    if (!Number.isFinite(tt) || tt < 100 || tt > 120000) {
      errs.total_timeout_ms = "Total timeout must be between 100 and 120,000 ms.";
    }
    if (ct !== 0 && (!Number.isFinite(ct) || ct < 50 || ct > 30000)) {
      errs.connect_timeout_ms = "Connect timeout must be between 50 and 30,000 ms.";
    } else if (ct > tt) {
      errs.connect_timeout_ms = "Connect timeout cannot exceed total timeout.";
    }
    return errs;
  }

  if (policyType === "retry") {
    const maxAttempts = Number(config.max_attempts ?? 0);
    const baseMs = Number(config.base_backoff_ms ?? 0);
    const maxMs = Number(config.max_backoff_ms ?? 0);
    const methods = Array.isArray(config.retryable_methods) ? (config.retryable_methods as string[]) : [];

    if (!Number.isFinite(maxAttempts) || maxAttempts < 1 || maxAttempts > 5) {
      errs.max_attempts = "Max attempts must be between 1 and 5.";
    }
    if (!Number.isFinite(baseMs) || baseMs < 10 || baseMs > 5000) {
      errs.base_backoff_ms = "Base backoff must be between 10 and 5,000 ms.";
    }
    if (!Number.isFinite(maxMs) || maxMs < baseMs || maxMs > 30000) {
      errs.max_backoff_ms = "Max backoff must be >= base backoff and <= 30,000 ms.";
    }
    if (methods.length === 0) {
      errs.retryable_methods = "Select at least one idempotent HTTP method.";
    } else if (
      methods.some((m) => !IDEMPOTENT_RETRY_METHODS.includes(m.toUpperCase() as (typeof IDEMPOTENT_RETRY_METHODS)[number]))
    ) {
      errs.retryable_methods = "Non-idempotent methods (POST, PATCH) are forbidden from automatic retries.";
    }
    return errs;
  }

  if (policyType === "circuit_breaker") {
    const ft = Number(config.failure_threshold ?? 0);
    const st = Number(config.success_threshold ?? 0);
    const openMs = Number(config.open_duration_ms ?? 0);
    const ho = Number(config.half_open_max_in_flight ?? 0);

    if (!Number.isFinite(ft) || ft < 1 || ft > 100) {
      errs.failure_threshold = "Failure threshold must be between 1 and 100.";
    }
    if (!Number.isFinite(st) || st < 1 || st > 20) {
      errs.success_threshold = "Success threshold must be between 1 and 20.";
    }
    if (!Number.isFinite(openMs) || openMs < 500 || openMs > 300000) {
      errs.open_duration_ms = "Open duration must be between 500 and 300,000 ms.";
    }
    if (!Number.isFinite(ho) || ho < 1 || ho > 20) {
      errs.half_open_max_in_flight = "Half-open max in-flight probes must be between 1 and 20.";
    }
    return errs;
  }

  // cors
  const origins = Array.isArray(config.allowed_origins) ? (config.allowed_origins as string[]) : [];
  const allowCreds = Boolean(config.allow_credentials);
  if (origins.length === 0) {
    errs.allowed_origins = "Specify at least one allowed origin.";
  } else if (allowCreds && origins.some((o) => o.trim() === "*")) {
    errs.allow_credentials = "CORS cannot combine wildcard '*' origin with allow_credentials=true.";
  }
  return errs;
}

export function ResiliencePolicyForm({
  policyType,
  config,
  errors,
  onChange,
}: {
  policyType: ResiliencePolicyType;
  config: Record<string, unknown>;
  errors: Record<string, string>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  if (policyType === "timeout") {
    return (
      <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label htmlFor="pol-to-connect">Connect Timeout (ms)</Label>
            <Input
              id="pol-to-connect"
              type="number"
              value={String(config.connect_timeout_ms ?? 2000)}
              onChange={(e) => onChange({ ...config, connect_timeout_ms: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.connect_timeout_ms} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-to-total">Total Request Timeout (ms)</Label>
            <Input
              id="pol-to-total"
              type="number"
              value={String(config.total_timeout_ms ?? 10000)}
              onChange={(e) => onChange({ ...config, total_timeout_ms: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.total_timeout_ms} />
          </div>
        </div>
        <p className="text-xs text-[var(--muted-foreground)]">
          Connect timeout governs TCP/TLS dial; total timeout bounds the entire request including retries.
        </p>
      </div>
    );
  }

  if (policyType === "retry") {
    const methods = Array.isArray(config.retryable_methods) ? (config.retryable_methods as string[]) : ["GET", "HEAD", "OPTIONS"];
    const toggleMethod = (m: string) => {
      const next = methods.includes(m) ? methods.filter((x) => x !== m) : [...methods, m];
      onChange({ ...config, retryable_methods: next });
    };

    return (
      <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
        <div
          role="note"
          className="flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-950"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-700" aria-hidden="true" />
          <span>
            <strong>Non-idempotent retry safety:</strong> Automatic retries for <code>POST</code> and{" "}
            <code>PATCH</code> are intentionally disabled to prevent duplicate side effects (double billing or duplicate
            writes).
          </span>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="space-y-1">
            <Label htmlFor="pol-retry-attempts">Max Attempts (1–5)</Label>
            <Input
              id="pol-retry-attempts"
              type="number"
              value={String(config.max_attempts ?? 3)}
              onChange={(e) => onChange({ ...config, max_attempts: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.max_attempts} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-retry-base">Base Backoff (ms)</Label>
            <Input
              id="pol-retry-base"
              type="number"
              value={String(config.base_backoff_ms ?? 50)}
              onChange={(e) => onChange({ ...config, base_backoff_ms: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.base_backoff_ms} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-retry-max">Max Backoff (ms)</Label>
            <Input
              id="pol-retry-max"
              type="number"
              value={String(config.max_backoff_ms ?? 1000)}
              onChange={(e) => onChange({ ...config, max_backoff_ms: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.max_backoff_ms} />
          </div>
        </div>

        <fieldset className="space-y-1.5">
          <legend className="text-xs font-medium text-[var(--foreground)]">Retryable Idempotent Methods</legend>
          <div className="flex flex-wrap gap-2">
            {IDEMPOTENT_RETRY_METHODS.map((m) => (
              <label
                key={m}
                className="inline-flex cursor-pointer items-center gap-1.5 rounded border border-[var(--border)] bg-[var(--card)] px-2.5 py-1 text-xs font-mono"
              >
                <input
                  type="checkbox"
                  checked={methods.includes(m)}
                  onChange={() => toggleMethod(m)}
                />
                {m}
              </label>
            ))}
          </div>
          <FieldError message={errors.retryable_methods} />
        </fieldset>
      </div>
    );
  }

  if (policyType === "circuit_breaker") {
    return (
      <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label htmlFor="pol-cb-fail">Failure Threshold (1–100)</Label>
            <Input
              id="pol-cb-fail"
              type="number"
              value={String(config.failure_threshold ?? 5)}
              onChange={(e) => onChange({ ...config, failure_threshold: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.failure_threshold} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-cb-succ">Half-Open Success Threshold (1–20)</Label>
            <Input
              id="pol-cb-succ"
              type="number"
              value={String(config.success_threshold ?? 2)}
              onChange={(e) => onChange({ ...config, success_threshold: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.success_threshold} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-cb-open">Open Cooldown Duration (ms)</Label>
            <Input
              id="pol-cb-open"
              type="number"
              value={String(config.open_duration_ms ?? 15000)}
              onChange={(e) => onChange({ ...config, open_duration_ms: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.open_duration_ms} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-cb-inflight">Half-Open Max Probes (1–20)</Label>
            <Input
              id="pol-cb-inflight"
              type="number"
              value={String(config.half_open_max_in_flight ?? 2)}
              onChange={(e) =>
                onChange({ ...config, half_open_max_in_flight: Number.parseInt(e.target.value, 10) || 0 })
              }
            />
            <FieldError message={errors.half_open_max_in_flight} />
          </div>
        </div>
      </div>
    );
  }

  // cors
  const origins = Array.isArray(config.allowed_origins) ? (config.allowed_origins as string[]) : [];
  const allowCreds = Boolean(config.allow_credentials);
  const unsafeCors = allowCreds && origins.some((o) => o.trim() === "*");

  return (
    <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
      <div className="space-y-1">
        <Label htmlFor="pol-cors-origins">Allowed Origins (comma-separated)</Label>
        <Input
          id="pol-cors-origins"
          value={origins.join(", ")}
          onChange={(e) =>
            onChange({
              ...config,
              allowed_origins: e.target.value
                .split(",")
                .map((s) => s.trim())
                .filter(Boolean),
            })
          }
          placeholder="https://app.example.com"
        />
        <FieldError message={errors.allowed_origins} />
      </div>

      <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
        <input
          type="checkbox"
          checked={allowCreds}
          onChange={(e) => onChange({ ...config, allow_credentials: e.target.checked })}
          className="h-4 w-4 rounded border-[var(--border-strong)]"
        />
        Allow credentials (cookies / Authorization header)
      </label>
      <FieldError message={errors.allow_credentials} />

      {unsafeCors && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-900"
        >
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-red-700" aria-hidden="true" />
          <span>Wildcard origin (*) cannot be combined with allow_credentials=true. Specify explicit origins.</span>
        </div>
      )}
    </div>
  );
}
