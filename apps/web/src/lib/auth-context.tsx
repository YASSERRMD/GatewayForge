"use client";

import * as React from "react";

export interface OrgMembership {
  org_id: string;
  org_slug: string;
  org_name: string;
  role: "owner" | "admin" | "developer" | "viewer";
}

export interface AuthUser {
  id: string;
  email: string;
  display_name: string;
}

export type AuthStatus = "loading" | "authenticated" | "unauthenticated" | "expired";

export interface AuthContextValue {
  status: AuthStatus;
  user: AuthUser | null;
  memberships: OrgMembership[];
  activeOrgId: string | null;
  activeOrg: OrgMembership | null;
  csrfToken: string;
  cacheVersion: number;
  error: string | null;
  refreshSession: () => Promise<void>;
  setAuthenticated: (user: AuthUser, memberships: OrgMembership[], csrfToken: string) => void;
  selectOrg: (orgId: string) => void;
  clearSession: (expired?: boolean) => void;
  logout: () => Promise<void>;
}

const ORG_STORAGE_KEY = "gatewayforge_active_org_id";

const AuthContext = React.createContext<AuthContextValue | null>(null);

export function resolveApiBase(): string {
  // Prefer same-origin /api/v1 rewrites in the browser so HttpOnly SameSite=Lax cookies work reliably.
  return "";
}

export function AuthProvider({
  children,
  initialState,
}: {
  children: React.ReactNode;
  initialState?: {
    status: AuthStatus;
    user: AuthUser | null;
    memberships: OrgMembership[];
    activeOrgId?: string | null;
    csrfToken?: string;
  };
}) {
  const [status, setStatus] = React.useState<AuthStatus>(initialState?.status ?? "loading");
  const [user, setUser] = React.useState<AuthUser | null>(initialState?.user ?? null);
  const [memberships, setMemberships] = React.useState<OrgMembership[]>(
    initialState?.memberships ?? []
  );
  const [activeOrgId, setActiveOrgId] = React.useState<string | null>(
    initialState?.activeOrgId ?? initialState?.memberships?.[0]?.org_id ?? null
  );
  const [csrfToken, setCsrfToken] = React.useState<string>(initialState?.csrfToken ?? "");
  const [cacheVersion, setCacheVersion] = React.useState<number>(0);
  const [error, setError] = React.useState<string | null>(null);

  const pickValidOrg = React.useCallback((list: OrgMembership[]): string | null => {
    if (list.length === 0) return null;
    try {
      const saved = window.localStorage.getItem(ORG_STORAGE_KEY);
      if (saved && list.some((m) => m.org_id === saved)) {
        return saved;
      }
    } catch {
      // ignore storage errors in restricted contexts
    }
    return list[0]?.org_id ?? null;
  }, []);

  const setAuthenticated = React.useCallback(
    (nextUser: AuthUser, nextMemberships: OrgMembership[], nextCsrf: string) => {
      setUser(nextUser);
      setMemberships(nextMemberships);
      setCsrfToken(nextCsrf);
      const validOrg = pickValidOrg(nextMemberships);
      setActiveOrgId(validOrg);
      setStatus("authenticated");
      setError(null);
      setCacheVersion((v) => v + 1);
    },
    [pickValidOrg]
  );

  const clearSession = React.useCallback((expired = false) => {
    setUser(null);
    setMemberships([]);
    setActiveOrgId(null);
    setCsrfToken("");
    setStatus(expired ? "expired" : "unauthenticated");
    setCacheVersion((v) => v + 1);
  }, []);

  const refreshSession = React.useCallback(async () => {
    try {
      const res = await fetch(`${resolveApiBase()}/api/v1/auth/me`, {
        credentials: "include",
        headers: { Accept: "application/json" },
      });
      if (res.status === 401) {
        clearSession(false);
        return;
      }
      if (!res.ok) {
        setError(`Session check failed (${res.status})`);
        setStatus("unauthenticated");
        return;
      }
      const data = (await res.json()) as {
        user: AuthUser;
        memberships?: OrgMembership[];
        csrf_token?: string;
      };
      setAuthenticated(data.user, data.memberships ?? [], data.csrf_token ?? "");
    } catch {
      setError("Control plane is unreachable.");
      setStatus("unauthenticated");
    }
  }, [clearSession, setAuthenticated]);

  React.useEffect(() => {
    if (!initialState) {
      void refreshSession();
    }
  }, [initialState, refreshSession]);

  const selectOrg = React.useCallback(
    (orgId: string) => {
      if (!memberships.some((m) => m.org_id === orgId)) return;
      if (orgId === activeOrgId) return;
      try {
        window.localStorage.setItem(ORG_STORAGE_KEY, orgId);
      } catch {
        // ignore storage errors
      }
      setActiveOrgId(orgId);
      // Increment cacheVersion to force every org-scoped panel to flush stale tenant data immediately.
      setCacheVersion((v) => v + 1);
    },
    [activeOrgId, memberships]
  );

  const logout = React.useCallback(async () => {
    try {
      await fetch(`${resolveApiBase()}/api/v1/auth/logout`, {
        method: "POST",
        credentials: "include",
        headers: csrfToken ? { "X-CSRF-Token": csrfToken } : {},
      });
    } catch {
      // clear client session even if network call fails
    } finally {
      try {
        window.localStorage.removeItem(ORG_STORAGE_KEY);
      } catch {
        // ignore
      }
      clearSession(false);
    }
  }, [clearSession, csrfToken]);

  const activeOrg = React.useMemo(
    () => memberships.find((m) => m.org_id === activeOrgId) ?? null,
    [memberships, activeOrgId]
  );

  const value = React.useMemo<AuthContextValue>(
    () => ({
      status,
      user,
      memberships,
      activeOrgId,
      activeOrg,
      csrfToken,
      cacheVersion,
      error,
      refreshSession,
      setAuthenticated,
      selectOrg,
      clearSession,
      logout,
    }),
    [
      status,
      user,
      memberships,
      activeOrgId,
      activeOrg,
      csrfToken,
      cacheVersion,
      error,
      refreshSession,
      setAuthenticated,
      selectOrg,
      clearSession,
      logout,
    ]
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = React.useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within AuthProvider");
  }
  return ctx;
}
