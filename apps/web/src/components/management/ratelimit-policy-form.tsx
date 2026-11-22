"use client";

import * as React from "react";
import { ShieldAlert } from "lucide-react";
import { FieldError, Input, Label, Select } from "@/components/ui/input";

export function defaultRateLimitConfig(): Record<string, unknown> {
  return {
    algorithm: "token_bucket",
    key_by: "consumer",
    requests_per_window: 100,
    window_seconds: 60,
    burst: 100,
    security_class: "standard",
    fail_mode: "fail_open",
    include_headers: true,
  };
}

export function defaultQuotaConfig(): Record<string, unknown> {
  return {
    key_by: "consumer",
    limit: 10000,
    period: "daily",
    security_class: "standard",
    fail_mode: "fail_open",
  };
}

export function validateRateLimitPolicyConfig(
  policyType: "rate_limit" | "quota",
  config: Record<string, unknown>
): Record<string, string> {
  const errs: Record<string, string> = {};
  const secClass = String(config.security_class ?? "standard");
  const failMode = String(config.fail_mode ?? "fail_open");

  if (secClass === "critical" && failMode === "fail_open") {
    errs.fail_mode = "Critical security_class cannot use fail_open; select fail_closed to protect Redis outages.";
  }

  if (policyType === "rate_limit") {
    const rpw = Number(config.requests_per_window);
    const win = Number(config.window_seconds);
    const burst = Number(config.burst);
    const keyBy = String(config.key_by ?? "consumer");
    const headerName = String(config.header_name ?? "").trim();

    if (!Number.isFinite(rpw) || rpw < 1 || rpw > 1_000_000) {
      errs.requests_per_window = "Requests per window must be between 1 and 1,000,000.";
    }
    if (!Number.isFinite(win) || win < 1 || win > 86400) {
      errs.window_seconds = "Window duration must be between 1 and 86,400 seconds.";
    }
    if (!Number.isFinite(burst) || burst < 1 || burst > 1_000_000) {
      errs.burst = "Burst capacity must be between 1 and 1,000,000.";
    } else if (Number.isFinite(rpw) && burst < rpw) {
      errs.burst = "Burst must be greater than or equal to requests per window.";
    }
    if (keyBy === "header" && (!headerName || /\s|:/.test(headerName))) {
      errs.header_name = "Valid HTTP header_name is required when key_by is header.";
    }
    return errs;
  }

  const limit = Number(config.limit);
  if (!Number.isFinite(limit) || limit < 1 || limit > 100_000_000) {
    errs.limit = "Quota limit must be between 1 and 100,000,000 requests per UTC period.";
  }
  return errs;
}

export function RateLimitPolicyForm({
  policyType,
  config,
  errors,
  onChange,
}: {
  policyType: "rate_limit" | "quota";
  config: Record<string, unknown>;
  errors: Record<string, string>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const secClass = String(config.security_class ?? "standard");
  const failMode = String(config.fail_mode ?? "fail_open");
  const isCriticalFailOpen = secClass === "critical" && failMode === "fail_open";

  if (policyType === "quota") {
    return (
      <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="space-y-1">
            <Label htmlFor="pol-quota-keyby">Key Dimension</Label>
            <Select
              id="pol-quota-keyby"
              value={String(config.key_by ?? "consumer")}
              onChange={(e) => onChange({ ...config, key_by: e.target.value })}
            >
              <option value="consumer">Consumer</option>
              <option value="org">Organization</option>
              <option value="ip">Client IP</option>
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-quota-limit">Quota Limit (requests)</Label>
            <Input
              id="pol-quota-limit"
              type="number"
              value={String(config.limit ?? 10000)}
              onChange={(e) => onChange({ ...config, limit: Number.parseInt(e.target.value, 10) || 0 })}
            />
            <FieldError message={errors.limit} />
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-quota-period">UTC Reset Period</Label>
            <Select
              id="pol-quota-period"
              value={String(config.period ?? "daily")}
              onChange={(e) => onChange({ ...config, period: e.target.value })}
            >
              <option value="hourly">Hourly (UTC)</option>
              <option value="daily">Daily (UTC)</option>
              <option value="monthly">Monthly (UTC)</option>
            </Select>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <Label htmlFor="pol-quota-class">Security Class</Label>
            <Select
              id="pol-quota-class"
              value={secClass}
              onChange={(e) => onChange({ ...config, security_class: e.target.value })}
            >
              <option value="standard">Standard</option>
              <option value="critical">Critical (requires fail_closed)</option>
            </Select>
          </div>
          <div className="space-y-1">
            <Label htmlFor="pol-quota-failmode">Redis Outage Behavior</Label>
            <Select
              id="pol-quota-failmode"
              value={failMode}
              onChange={(e) => onChange({ ...config, fail_mode: e.target.value })}
            >
              <option value="fail_open">fail_open (fallback local limiter)</option>
              <option value="fail_closed">fail_closed (503 during Redis outage)</option>
            </Select>
            <FieldError message={errors.fail_mode} />
          </div>
        </div>
      </div>
    );
  }

  const keyBy = String(config.key_by ?? "consumer");

  return (
    <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="pol-rl-algo">Algorithm</Label>
          <Select
            id="pol-rl-algo"
            value={String(config.algorithm ?? "token_bucket")}
            onChange={(e) => onChange({ ...config, algorithm: e.target.value })}
          >
            <option value="token_bucket">Token Bucket (atomic Redis Lua)</option>
            <option value="gcra">GCRA (smooth arrival)</option>
          </Select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="pol-rl-keyby">Key Dimension</Label>
          <Select
            id="pol-rl-keyby"
            value={keyBy}
            onChange={(e) => onChange({ ...config, key_by: e.target.value })}
          >
            <option value="consumer">Consumer ID</option>
            <option value="ip">Client IP</option>
            <option value="org">Organization</option>
            <option value="route">Route</option>
            <option value="header">Custom Header</option>
          </Select>
        </div>
      </div>

      {keyBy === "header" && (
        <div className="space-y-1">
          <Label htmlFor="pol-rl-header">Rate-Limit Header Name</Label>
          <Input
            id="pol-rl-header"
            value={String(config.header_name ?? "")}
            onChange={(e) => onChange({ ...config, header_name: e.target.value })}
            placeholder="X-Tenant-Tier"
          />
          <FieldError message={errors.header_name} />
        </div>
      )}

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
        <div className="space-y-1">
          <Label htmlFor="pol-rl-rpw">Requests / Window</Label>
          <Input
            id="pol-rl-rpw"
            type="number"
            value={String(config.requests_per_window ?? 100)}
            onChange={(e) => onChange({ ...config, requests_per_window: Number.parseInt(e.target.value, 10) || 0 })}
          />
          <FieldError message={errors.requests_per_window} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="pol-rl-window">Window (seconds)</Label>
          <Input
            id="pol-rl-window"
            type="number"
            value={String(config.window_seconds ?? 60)}
            onChange={(e) => onChange({ ...config, window_seconds: Number.parseInt(e.target.value, 10) || 0 })}
          />
          <FieldError message={errors.window_seconds} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="pol-rl-burst">Burst (requests)</Label>
          <Input
            id="pol-rl-burst"
            type="number"
            value={String(config.burst ?? 100)}
            onChange={(e) => onChange({ ...config, burst: Number.parseInt(e.target.value, 10) || 0 })}
          />
          <FieldError message={errors.burst} />
        </div>
      </div>

      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="pol-rl-class">Security Class</Label>
          <Select
            id="pol-rl-class"
            value={secClass}
            onChange={(e) => onChange({ ...config, security_class: e.target.value })}
          >
            <option value="standard">Standard</option>
            <option value="critical">Critical (auth / financial endpoints)</option>
          </Select>
        </div>
        <div className="space-y-1">
          <Label htmlFor="pol-rl-failmode">Redis Outage Mode</Label>
          <Select
            id="pol-rl-failmode"
            value={failMode}
            onChange={(e) => onChange({ ...config, fail_mode: e.target.value })}
          >
            <option value="fail_open">fail_open (degraded local fallback)</option>
            <option value="fail_closed">fail_closed (503 on Redis outage)</option>
          </Select>
          <FieldError message={errors.fail_mode} />
        </div>
      </div>

      {isCriticalFailOpen && (
        <div
          role="alert"
          className="flex items-start gap-2 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-900"
        >
          <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-red-700" aria-hidden="true" />
          <span>
            Semantic guardrail: <strong>critical</strong> policies cannot run in <code>fail_open</code> mode. Switch
            Redis Outage Mode to <code>fail_closed</code>.
          </span>
        </div>
      )}

      <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
        <input
          type="checkbox"
          checked={config.include_headers !== false}
          onChange={(e) => onChange({ ...config, include_headers: e.target.checked })}
          className="h-4 w-4 rounded border-[var(--border-strong)]"
        />
        Emit RFC RateLimit / RateLimit-Policy / Retry-After response headers
      </label>
    </div>
  );
}
