import * as React from "react";
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AuthProvider, useAuth } from "@/lib/auth-context";
import { AppShell } from "./app-shell";

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
}));

function TenantStatefulPanel() {
  const { activeOrg } = useAuth();
  const [draftNote, setDraftNote] = React.useState(`loaded:${activeOrg?.org_slug ?? "none"}`);
  return (
    <div>
      <p data-testid="tenant-note">{draftNote}</p>
      <button type="button" onClick={() => setDraftNote("dirty-tenant-a-secret")}>
        Mutate Local State
      </button>
    </div>
  );
}

describe("OrgSwitcher tenant isolation", () => {
  it("lists only accessible orgs and flushes stale local state when switching orgs", () => {
    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "multi@acme.test", display_name: "Multi" },
          memberships: [
            { org_id: "org-a", org_slug: "acme-a", org_name: "Acme Alpha", role: "owner" },
            { org_id: "org-b", org_slug: "beta-b", org_name: "Beta Corp", role: "viewer" },
          ],
          activeOrgId: "org-a",
        }}
      >
        <AppShell>
          <TenantStatefulPanel />
        </AppShell>
      </AuthProvider>
    );

    const select = screen.getByLabelText("Active organization");
    expect(select).toHaveValue("org-a");
    expect(screen.getByTestId("active-org-role")).toHaveTextContent("owner");

    // Mutate tenant-specific local state in Org A
    fireEvent.click(screen.getByRole("button", { name: "Mutate Local State" }));
    expect(screen.getByTestId("tenant-note")).toHaveTextContent("dirty-tenant-a-secret");

    // Switch to Org B -> AppShell remounts subtree so Org A state is completely purged!
    fireEvent.change(select, { target: { value: "org-b" } });
    expect(screen.getByTestId("active-org-role")).toHaveTextContent("viewer");
    expect(screen.getByTestId("tenant-note")).toHaveTextContent("loaded:beta-b");
    expect(screen.queryByText("dirty-tenant-a-secret")).not.toBeInTheDocument();
  });
});
