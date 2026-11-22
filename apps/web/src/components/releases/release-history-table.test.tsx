import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { ReleaseHistoryTable } from "./release-history-table";

describe("ReleaseHistoryTable", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("displays ordered immutable versions with pagination and hides release actions for developers", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
      new Response(
        JSON.stringify({
          items: [
            {
              id: "p2",
              org_id: "o1",
              version: 2,
              draft_revision: 5,
              checksum: "222222222222222222222222",
              notes: "Add v2 routes",
              published_at: "2026-11-22T12:10:00Z",
              is_active: true,
              activation_revision: 2,
            },
            {
              id: "p1",
              org_id: "o1",
              version: 1,
              draft_revision: 2,
              checksum: "111111111111111111111111",
              notes: "Initial release",
              published_at: "2026-11-22T12:00:00Z",
              is_active: false,
              activation_revision: 2,
            },
          ],
          total: 12,
          limit: 10,
          offset: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      )
    );

    const onRollback = vi.fn();
    const { unmount } = render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
        }}
      >
        <ReleaseHistoryTable onRollback={onRollback} />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("v2")).toBeInTheDocument());
    expect(screen.getByText("Active")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Rollback to v1" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /next/i }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("offset=10"), expect.anything())
    );

    unmount();

    // Developer sees history read-only without rollback action
    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u2", email: "dev@acme.test", display_name: "Dev" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "developer" }],
        }}
      >
        <ReleaseHistoryTable onRollback={onRollback} />
      </AuthProvider>
    );
    await waitFor(() => expect(screen.getByText("v2")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /rollback/i })).not.toBeInTheDocument();
  });
});
