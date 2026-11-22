import Link from "next/link";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export const NAV_ITEMS = [
  { href: "/", label: "Overview" },
  { href: "/services", label: "Services" },
  { href: "/routes", label: "Routes" },
  { href: "/upstreams", label: "Upstreams" },
  { href: "/consumers", label: "Consumers" },
  { href: "/policies", label: "Policies" },
  { href: "/releases", label: "Releases" },
  { href: "/console", label: "Request Console" },
  { href: "/insights", label: "Insights" },
  { href: "/settings", label: "Settings" },
] as const;

export function ShellHome() {
  return (
    <div className="min-h-screen flex flex-col">
      <header className="border-b border-[var(--border)] bg-[var(--card)]">
        <div className="mx-auto flex max-w-7xl items-center justify-between px-6 py-4">
          <div className="flex items-center gap-3">
            <span className="text-lg font-semibold tracking-tight">GatewayForge</span>
            <Badge variant="success">Control Studio</Badge>
          </div>
          <nav aria-label="Primary" className="hidden md:flex items-center gap-4 text-sm">
            {NAV_ITEMS.slice(0, 6).map((item) => (
              <Link
                key={item.href}
                href={item.href}
                className="text-[var(--muted-foreground)] hover:text-[var(--foreground)] transition-colors"
              >
                {item.label}
              </Link>
            ))}
          </nav>
        </div>
      </header>

      <main className="mx-auto w-full max-w-7xl flex-1 px-6 py-8 space-y-6">
        <section className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">GatewayForge Control Plane</h1>
          <p className="text-sm text-[var(--muted-foreground)]">
            Self-hosted API gateway studio for services, routes, policies, and versioned releases.
          </p>
        </section>

        <section className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          <Card>
            <CardHeader>
              <CardTitle>Control Plane API</CardTitle>
              <CardDescription>Configuration, RBAC, validation, and immutable releases.</CardDescription>
            </CardHeader>
            <CardContent className="text-xs text-[var(--muted-foreground)]">
              Health probes served on <code>/health/live</code> and <code>/health/ready</code>.
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Gateway Data Plane</CardTitle>
              <CardDescription>Zero-restart snapshot routing and reverse-proxy enforcement.</CardDescription>
            </CardHeader>
            <CardContent className="text-xs text-[var(--muted-foreground)]">
              Default-deny routing until a validated configuration snapshot is active.
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Operator Modules</CardTitle>
              <CardDescription>Structured navigation across ten operational domains.</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-wrap gap-1.5">
              {NAV_ITEMS.map((item) => (
                <Badge key={item.href}>{item.label}</Badge>
              ))}
            </CardContent>
          </Card>
        </section>
      </main>
    </div>
  );
}
