"use client";

import * as React from "react";
import { Building2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Select } from "@/components/ui/input";
import { useAuth } from "@/lib/auth-context";

export function OrgSwitcher() {
  const { memberships, activeOrgId, activeOrg, selectOrg } = useAuth();

  if (memberships.length === 0) {
    return (
      <div className="flex items-center gap-2 text-sm text-[var(--muted-foreground)]">
        <Building2 className="h-4 w-4" aria-hidden="true" />
        <span>No organization</span>
      </div>
    );
  }

  return (
    <div className="flex min-w-0 items-center gap-1.5 sm:gap-2">
      <Building2 className="hidden h-4 w-4 shrink-0 text-[var(--muted-foreground)] sm:block" aria-hidden="true" />
      <label htmlFor="org-switcher-select" className="sr-only">
        Active organization
      </label>
      <Select
        id="org-switcher-select"
        aria-label="Active organization"
        value={activeOrgId ?? ""}
        onChange={(e) => selectOrg(e.target.value)}
        className="h-8 min-w-0 max-w-[150px] truncate text-xs font-medium sm:w-auto sm:min-w-[170px] sm:max-w-[260px] sm:text-sm"
      >
        {memberships.map((m) => (
          <option key={m.org_id} value={m.org_id}>
            {m.org_name} ({m.org_slug})
          </option>
        ))}
      </Select>
      {activeOrg && (
        <Badge
          variant={
            activeOrg.role === "owner" || activeOrg.role === "admin" ? "success" : "default"
          }
          className="shrink-0"
          data-testid="active-org-role"
        >
          {activeOrg.role}
        </Badge>
      )}
    </div>
  );
}
