"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { ShieldCheck } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { FieldError, Input, Label } from "@/components/ui/input";
import { ErrorBanner } from "@/components/ui/states";
import { resolveApiBase, useAuth } from "@/lib/auth-context";

export function LoginForm({ sessionExpired = false }: { sessionExpired?: boolean }) {
  const router = useRouter();
  const { refreshSession } = useAuth();
  const [mode, setMode] = React.useState<"login" | "bootstrap">("login");

  const [email, setEmail] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [displayName, setDisplayName] = React.useState("");
  const [orgName, setOrgName] = React.useState("");
  const [orgSlug, setOrgSlug] = React.useState("");

  const [submitting, setSubmitting] = React.useState(false);
  const [formError, setFormError] = React.useState<string | null>(null);
  const [emailErr, setEmailErr] = React.useState<string | null>(null);
  const [passwordErr, setPasswordErr] = React.useState<string | null>(null);

  const validate = (): boolean => {
    let ok = true;
    setEmailErr(null);
    setPasswordErr(null);
    if (!email.trim() || !email.includes("@")) {
      setEmailErr("Enter a valid email address.");
      ok = false;
    }
    if (password.length < 8) {
      setPasswordErr("Password must be at least 8 characters.");
      ok = false;
    }
    return ok;
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError(null);
    if (!validate()) return;

    setSubmitting(true);
    try {
      if (mode === "bootstrap") {
        const bootRes = await fetch(`${resolveApiBase()}/api/v1/auth/bootstrap`, {
          method: "POST",
          credentials: "include",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            email: email.trim(),
            password,
            display_name: displayName.trim() || "Gateway Admin",
            org_name: orgName.trim() || "Default Organization",
            org_slug: orgSlug.trim().toLowerCase() || "default-org",
          }),
        });
        if (!bootRes.ok) {
          const problem = (await bootRes.json().catch(() => null)) as { detail?: string } | null;
          setFormError(problem?.detail ?? `Bootstrap failed (${bootRes.status}).`);
          return;
        }
      }

      const res = await fetch(`${resolveApiBase()}/api/v1/auth/login`, {
        method: "POST",
        credentials: "include",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ email: email.trim(), password }),
      });

      if (!res.ok) {
        const problem = (await res.json().catch(() => null)) as { detail?: string; title?: string } | null;
        setFormError(problem?.detail ?? problem?.title ?? "Invalid email or password.");
        return;
      }

      await refreshSession();
      router.replace("/");
    } catch {
      setFormError("Could not reach the control plane. Check your connection and retry.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-screen items-center justify-center bg-[var(--background)] px-4 py-12">
      <div className="w-full max-w-md space-y-4">
        <div className="flex items-center justify-center gap-2 text-[var(--foreground)]">
          <ShieldCheck className="h-6 w-6 text-[var(--accent)]" aria-hidden="true" />
          <span className="text-xl font-semibold tracking-tight">GatewayForge Studio</span>
        </div>

        {sessionExpired && (
          <div
            role="status"
            className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900"
          >
            Your session expired. Sign in again to continue managing gateway configurations.
          </div>
        )}

        <Card>
          <CardHeader>
            <CardTitle>{mode === "login" ? "Sign in to Control Plane" : "Bootstrap First Admin"}</CardTitle>
            <CardDescription>
              {mode === "login"
                ? "Authenticate with your organization credentials to manage services, routes, and releases."
                : "Initialize a fresh development instance with its first organization owner."}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleSubmit} noValidate className="space-y-4">
              {formError && <ErrorBanner title="Authentication error" message={formError} />}

              <div className="space-y-1.5">
                <Label htmlFor="login-email">Email</Label>
                <Input
                  id="login-email"
                  name="email"
                  type="email"
                  autoComplete="email"
                  required
                  aria-invalid={Boolean(emailErr)}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="admin@company.local"
                />
                <FieldError message={emailErr} />
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="login-password">Password</Label>
                <Input
                  id="login-password"
                  name="password"
                  type="password"
                  autoComplete={mode === "login" ? "current-password" : "new-password"}
                  required
                  aria-invalid={Boolean(passwordErr)}
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="••••••••••••"
                />
                <FieldError message={passwordErr} />
              </div>

              {mode === "bootstrap" && (
                <>
                  <div className="space-y-1.5">
                    <Label htmlFor="boot-display-name">Display Name</Label>
                    <Input
                      id="boot-display-name"
                      value={displayName}
                      onChange={(e) => setDisplayName(e.target.value)}
                      placeholder="Platform Admin"
                    />
                  </div>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <div className="space-y-1.5">
                      <Label htmlFor="boot-org-name">Organization Name</Label>
                      <Input
                        id="boot-org-name"
                        value={orgName}
                        onChange={(e) => setOrgName(e.target.value)}
                        placeholder="Acme Platform"
                      />
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor="boot-org-slug">Organization Slug</Label>
                      <Input
                        id="boot-org-slug"
                        value={orgSlug}
                        onChange={(e) => setOrgSlug(e.target.value)}
                        placeholder="acme-platform"
                      />
                    </div>
                  </div>
                </>
              )}

              <Button type="submit" className="w-full" disabled={submitting}>
                {submitting
                  ? mode === "login"
                    ? "Signing in…"
                    : "Bootstrapping…"
                  : mode === "login"
                    ? "Sign in"
                    : "Create Admin & Sign in"}
              </Button>
            </form>

            <div className="mt-4 border-t border-[var(--border)] pt-3 text-center">
              <button
                type="button"
                onClick={() => {
                  setFormError(null);
                  setMode((m) => (m === "login" ? "bootstrap" : "login"));
                }}
                className="text-xs font-medium text-[var(--accent)] hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)] rounded"
              >
                {mode === "login"
                  ? "Fresh installation? Bootstrap first admin"
                  : "Already initialized? Return to Sign in"}
              </button>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
