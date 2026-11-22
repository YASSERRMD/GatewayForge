import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { ConfigDiffViewer } from "./config-diff-viewer";

describe("ConfigDiffViewer", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("renders added, modified, and removed semantic config changes against active version", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          base_version: 2,
          has_changes: true,
          summary: { added: 1, modified: 1, removed: 0 },
          changes: [
            {
              resource_type: "route",
              resource_id: "r2",
              name: "Orders V2",
              change_type: "added",
              details: ["GET api.local/v2/orders (priority 120, 0 policies)"],
            },
            {
              resource_type: "upstream_pool",
              resource_id: "p1",
              name: "orders-pool",
              change_type: "modified",
              details: ["targets changed (1 -> 2)"],
            },
          ],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      )
    );

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "admin" }],
        }}
      >
        <ConfigDiffViewer />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText(/Active Release v2/i)).toBeInTheDocument());
    expect(screen.getByText("+1 added")).toBeInTheDocument();
    expect(screen.getByText("~1 modified")).toBeInTheDocument();
    expect(screen.getByText("Orders V2")).toBeInTheDocument();
    expect(screen.getByText("targets changed (1 -> 2)")).toBeInTheDocument();
  });
});
