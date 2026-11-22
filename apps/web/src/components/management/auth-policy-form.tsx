"use client";

import * as React from "react";
import { FieldError, Input, Label } from "@/components/ui/input";

export interface ApiKeyPolicyConfig {
  header_name: string;
  required_scopes: string[];
  strip_credential: boolean;
}

export interface JwtPolicyConfig {
  issuer: string;
  audience: string;
  jwks_url: string;
  algorithms: string[];
  required_scopes: string[];
}

export const ALLOWED_JWT_ALGS = ["RS256", "ES256"] as const;

export function defaultApiKeyConfig(): ApiKeyPolicyConfig {
  return {
    header_name: "X-Api-Key",
    required_scopes: [],
    strip_credential: true,
  };
}

export function defaultJwtConfig(): JwtPolicyConfig {
  return {
    issuer: "",
    audience: "",
    jwks_url: "",
    algorithms: ["RS256"],
    required_scopes: [],
  };
}

export function validateAuthPolicyConfig(
  policyType: "api_key" | "jwt",
  config: Record<string, unknown>
): Record<string, string> {
  const errs: Record<string, string> = {};
  if (policyType === "api_key") {
    const header = String(config.header_name ?? "").trim();
    if (!header || header.length > 64 || /\s|:/.test(header)) {
      errs.header_name = "Header name must be a valid HTTP header (e.g. X-Api-Key).";
    }
    return errs;
  }

  const issuer = String(config.issuer ?? "").trim();
  const audience = String(config.audience ?? "").trim();
  const jwks = String(config.jwks_url ?? "").trim();
  const algs = Array.isArray(config.algorithms) ? (config.algorithms as string[]) : [];

  if (!issuer) {
    errs.issuer = "Expected JWT issuer (iss) is required.";
  }
  if (!audience) {
    errs.audience = "Expected JWT audience (aud) is required.";
  }
  if (!jwks) {
    errs.jwks_url = "JWKS URL is required.";
  } else {
    try {
      const parsed = new URL(jwks);
      if (parsed.protocol !== "https:" && parsed.protocol !== "http:") {
        errs.jwks_url = "JWKS URL must use https:// (or http:// for approved local mock endpoints).";
      }
    } catch {
      errs.jwks_url = "JWKS URL must be a valid URL.";
    }
  }
  if (algs.length === 0) {
    errs.algorithms = "Select at least one asymmetric signing algorithm (RS256 or ES256).";
  } else if (algs.some((a) => !ALLOWED_JWT_ALGS.includes(a as (typeof ALLOWED_JWT_ALGS)[number]))) {
    errs.algorithms = "Only asymmetric RS256 and ES256 algorithms are permitted; none and HS256 are forbidden.";
  }
  return errs;
}

export function AuthPolicyForm({
  policyType,
  config,
  errors,
  onChange,
}: {
  policyType: "api_key" | "jwt";
  config: Record<string, unknown>;
  errors: Record<string, string>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  if (policyType === "api_key") {
    const headerName = String(config.header_name ?? "X-Api-Key");
    const scopes = Array.isArray(config.required_scopes) ? (config.required_scopes as string[]).join(", ") : "";
    const strip = config.strip_credential !== false;

    return (
      <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
        <div className="space-y-1">
          <Label htmlFor="pol-apikey-header">API Key Header Name</Label>
          <Input
            id="pol-apikey-header"
            value={headerName}
            onChange={(e) => onChange({ ...config, header_name: e.target.value })}
            placeholder="X-Api-Key"
          />
          <p className="text-xs text-[var(--muted-foreground)]">
            Incoming request header verified in constant time against hashed consumer keys.
          </p>
          <FieldError message={errors.header_name} />
        </div>

        <div className="space-y-1">
          <Label htmlFor="pol-apikey-scopes">Required Scopes (comma-separated, optional)</Label>
          <Input
            id="pol-apikey-scopes"
            value={scopes}
            onChange={(e) =>
              onChange({
                ...config,
                required_scopes: e.target.value
                  .split(",")
                  .map((s) => s.trim())
                  .filter(Boolean),
              })
            }
            placeholder="orders:read, orders:write"
          />
        </div>

        <label className="flex items-center gap-2 text-sm text-[var(--foreground)]">
          <input
            type="checkbox"
            checked={strip}
            onChange={(e) => onChange({ ...config, strip_credential: e.target.checked })}
            className="h-4 w-4 rounded border-[var(--border-strong)]"
          />
          Strip API key header before proxying upstream (recommended)
        </label>
      </div>
    );
  }

  const issuer = String(config.issuer ?? "");
  const audience = String(config.audience ?? "");
  const jwksUrl = String(config.jwks_url ?? "");
  const algs = Array.isArray(config.algorithms) ? (config.algorithms as string[]) : ["RS256"];
  const scopes = Array.isArray(config.required_scopes) ? (config.required_scopes as string[]).join(", ") : "";

  const toggleAlg = (alg: string) => {
    const next = algs.includes(alg) ? algs.filter((a) => a !== alg) : [...algs, alg];
    onChange({ ...config, algorithms: next });
  };

  return (
    <div className="space-y-3 rounded-lg border border-[var(--border)] bg-[var(--muted)]/30 p-3.5">
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <Label htmlFor="pol-jwt-issuer">Expected Issuer (iss)</Label>
          <Input
            id="pol-jwt-issuer"
            value={issuer}
            onChange={(e) => onChange({ ...config, issuer: e.target.value })}
            placeholder="https://idp.example.com/"
          />
          <FieldError message={errors.issuer} />
        </div>
        <div className="space-y-1">
          <Label htmlFor="pol-jwt-audience">Expected Audience (aud)</Label>
          <Input
            id="pol-jwt-audience"
            value={audience}
            onChange={(e) => onChange({ ...config, audience: e.target.value })}
            placeholder="gatewayforge-api"
          />
          <FieldError message={errors.audience} />
        </div>
      </div>

      <div className="space-y-1">
        <Label htmlFor="pol-jwt-jwks">JWKS Endpoint URL</Label>
        <Input
          id="pol-jwt-jwks"
          value={jwksUrl}
          onChange={(e) => onChange({ ...config, jwks_url: e.target.value })}
          placeholder="https://idp.example.com/.well-known/jwks.json"
        />
        <p className="text-xs text-[var(--muted-foreground)]">
          Fetched through SSRF-guarded bounded cache. Token verification fails closed if JWKS is unavailable.
        </p>
        <FieldError message={errors.jwks_url} />
      </div>

      <fieldset className="space-y-1.5">
        <legend className="text-xs font-medium text-[var(--foreground)]">Allowed Signing Algorithms</legend>
        <div className="flex flex-wrap gap-3">
          {ALLOWED_JWT_ALGS.map((alg) => (
            <label
              key={alg}
              className="inline-flex cursor-pointer items-center gap-1.5 rounded border border-[var(--border)] bg-[var(--card)] px-2.5 py-1 text-xs font-mono"
            >
              <input
                type="checkbox"
                checked={algs.includes(alg)}
                onChange={() => toggleAlg(alg)}
              />
              {alg}
            </label>
          ))}
        </div>
        <p className="text-xs text-[var(--muted-foreground)]">
          Symmetric HS256 and unsigned &quot;none&quot; tokens are always rejected to prevent algorithm confusion.
        </p>
        <FieldError message={errors.algorithms} />
      </fieldset>

      <div className="space-y-1">
        <Label htmlFor="pol-jwt-scopes">Required Scopes (comma-separated, optional)</Label>
        <Input
          id="pol-jwt-scopes"
          value={scopes}
          onChange={(e) =>
            onChange({
              ...config,
              required_scopes: e.target.value
                .split(",")
                .map((s) => s.trim())
                .filter(Boolean),
            })
          }
          placeholder="api:read"
        />
      </div>
    </div>
  );
}
