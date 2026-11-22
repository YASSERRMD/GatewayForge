"use client";

import * as React from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import {
  Activity,
  BarChart3,
  Boxes,
  GitBranch,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Menu,
  Network,
  Settings,
  Shield,
  Terminal,
  X,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/states";
import { OrgSwitcher } from "@/components/org-switcher";
import { useAuth } from "@/lib/auth-context";
import { cn } from "@/lib/utils";

export const APP_NAV_ITEMS = [
  { href: "/", label: "Overview", icon: LayoutDashboard },
  { href: "/services", label: "Services", icon: Boxes },
  { href: "/routes", label: "Routes", icon: Network },
  { href: "/upstreams", label: "Upstreams", icon: Activity },
  { href: "/consumers", label: "Consumers", icon: KeyRound },
  { href: "/policies", label: "Policies", icon: Shield },
  { href: "/releases", label: "Releases", icon: GitBranch },
  { href: "/console", label: "Request Console", icon: Terminal },
  { href: "/insights", label: "Insights", icon: BarChart3 },
  { href: "/settings", label: "Settings", icon: Settings },
] as const;

export function AppShell({
  children,
  topbarSlot,
}: {
  children: React.ReactNode;
  topbarSlot?: React.ReactNode;
}) {
  const { status, user, activeOrgId, cacheVersion, logout } = useAuth();
  const pathname = usePathname();
  const router = useRouter();
  const [mobileOpen, setMobileOpen] = React.useState(false);

  React.useEffect(() => {
    if (status === "unauthenticated") {
      router.replace("/login");
    } else if (status === "expired") {
      router.replace("/login?reason=expired");
    }
  }, [status, router]);

  React.useEffect(() => {
    setMobileOpen(false);
  }, [pathname]);

  if (status === "loading") {
    return (
      <div className="min-h-screen bg-[var(--background)] p-8" aria-busy="true">
        <div className="mx-auto max-w-5xl space-y-4">
          <Skeleton className="h-10 w-64" />
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
            <Skeleton className="h-28" />
          </div>
          <Skeleton className="h-72" />
        </div>
      </div>
    );
  }

  if (status !== "authenticated" || !user) {
    return (
      <div className="flex min-h-screen items-center justify-center bg-[var(--background)] p-6">
        <p className="text-sm text-[var(--muted-foreground)]">Redirecting to sign in…</p>
      </div>
    );
  }

  const navList = (
    <nav aria-label="Primary navigation" className="space-y-1 px-3 py-4">
      {APP_NAV_ITEMS.map((item) => {
        const Icon = item.icon;
        const active = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
        return (
          <Link
            key={item.href}
            href={item.href}
            aria-current={active ? "page" : undefined}
            className={cn(
              "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
              active
                ? "bg-[var(--primary)] text-[var(--primary-foreground)]"
                : "text-[var(--muted-foreground)] hover:bg-[var(--muted)] hover:text-[var(--foreground)]"
            )}
          >
            <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
            <span>{item.label}</span>
          </Link>
        );
      })}
    </nav>
  );

  return (
    <div className="min-h-screen bg-[var(--background)] flex">
      <a href="#main-content" className="skip-link">
        Skip to main content
      </a>

      {/* Desktop Sidebar */}
      <aside
        aria-label="Sidebar"
        className="hidden lg:flex lg:w-60 lg:flex-col lg:border-r lg:border-[var(--border)] lg:bg-[var(--card)]"
      >
        <div className="flex h-14 items-center gap-2.5 border-b border-[var(--border)] px-4">
          <span className="text-base font-semibold tracking-tight text-[var(--foreground)]">
            GatewayForge
          </span>
          <Badge variant="default">Studio</Badge>
        </div>
        <div className="flex-1 overflow-y-auto">{navList}</div>
        <div className="border-t border-[var(--border)] p-3 text-xs text-[var(--muted-foreground)]">
          <p className="truncate font-medium text-[var(--foreground)]">{user.display_name}</p>
          <p className="truncate">{user.email}</p>
        </div>
      </aside>

      {/* Mobile Drawer */}
      {mobileOpen && (
        <div className="fixed inset-0 z-40 flex lg:hidden" role="dialog" aria-modal="true" aria-label="Mobile navigation">
          <div
            className="fixed inset-0 bg-slate-950/40"
            onClick={() => setMobileOpen(false)}
          />
          <div className="relative z-10 flex w-64 flex-col bg-[var(--card)] shadow-xl">
            <div className="flex h-14 items-center justify-between border-b border-[var(--border)] px-4">
              <span className="text-base font-semibold">GatewayForge</span>
              <button
                type="button"
                aria-label="Close navigation menu"
                onClick={() => setMobileOpen(false)}
                className="rounded-md p-1.5 hover:bg-[var(--muted)] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
              >
                <X className="h-4 w-4" aria-hidden="true" />
              </button>
            </div>
            <div className="flex-1 overflow-y-auto">{navList}</div>
          </div>
        </div>
      )}

      {/* Main Content Area */}
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center justify-between gap-3 border-b border-[var(--border)] bg-[var(--card)] px-4 lg:px-6">
          <div className="flex items-center gap-3">
            <button
              type="button"
              aria-label="Open navigation menu"
              aria-expanded={mobileOpen}
              onClick={() => setMobileOpen((v) => !v)}
              className="rounded-md p-1.5 text-[var(--foreground)] hover:bg-[var(--muted)] lg:hidden focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
            >
              <Menu className="h-5 w-5" aria-hidden="true" />
            </button>
            {topbarSlot ?? <OrgSwitcher />}
          </div>

          <div className="flex items-center gap-2">
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => {
                void logout().then(() => router.replace("/login"));
              }}
            >
              <LogOut className="mr-1.5 h-3.5 w-3.5" aria-hidden="true" />
              Sign out
            </Button>
          </div>
        </header>

        <main
          id="main-content"
          key={`${activeOrgId ?? "none"}:${cacheVersion}`}
          className="mx-auto w-full max-w-7xl flex-1 px-4 py-6 lg:px-6"
        >
          {children}
        </main>
      </div>
    </div>
  );
}
