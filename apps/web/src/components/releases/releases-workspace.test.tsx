import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { ReleasesWorkspace } from "./releases-workspace";

describe("ReleasesWorkspace rollback and conflict handling", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("executes confirmed rollback to v1 with optimistic activation_revision and surfaces 409 conflicts", async () => {
    let activeVer = 2;
    let actRev = 2;
    let conflictOnce = true;

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/draft/diff")) {
        return new Response(
          JSON.stringify({ base_version: activeVer, has_changes: false, summary: { added: 0, modified: 0, removed: 0 }, changes: [] }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.endsWith("/draft")) {
        return new Response(
          JSON.stringify({ draft_revision: 5, validation: { valid: true, draft_revision: 5, errors: [], warnings: [] }, preview: { checksum: "2222", routes: [], upstreams: {} } }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/publications/1/activate") && init?.method === "POST") {
        if (conflictOnce) {
          conflictOnce = false;
          return new Response(
            JSON.stringify({ status: 409, code: "activation_conflict", title: "Conflict", detail: "Active publication changed concurrently." }),
            { status: 409, headers: { "Content-Type": "application/json" } }
          );
        }
        activeVer = 1;
        actRev = 3;
        return new Response(JSON.stringify({ version: 1, is_rollback: true, activation_revision: actRev }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(
        JSON.stringify({
          items: [
            { id: "p2", org_id: "o1", version: 2, draft_revision: 5, checksum: "2222222222222222", notes: "bad route", published_at: "2026-11-22T12:10:00Z", is_active: activeVer === 2, activation_revision: actRev },
            { id: "p1", org_id: "o1", version: 1, draft_revision: 2, checksum: "1111111111111111", notes: "good v1", published_at: "2026-11-22T12:00:00Z", is_active: activeVer === 1, activation_revision: actRev },
          ],
          total: 2,
          limit: 10,
          offset: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-rb",
        }}
      >
        <ReleasesWorkspace />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByRole("button", { name: "Rollback to v1" })).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Rollback to v1" }));

    const dlg = screen.getByRole("dialog");
    fireEvent.click(within(dlg).getByRole("button", { name: /confirm rollback to v1/i }));

    // First attempt returns 409 conflict banner
    await waitFor(() => expect(within(dlg).getByText(/Concurrent Activation Conflict/i)).toBeInTheDocument());

    // Retry succeeds and sends expected_activation_revision + CSRF token
    fireEvent.click(within(dlg).getByRole("button", { name: /confirm rollback to v1/i }));
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());

    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/publications/1/activate",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-rb" }),
        body: expect.stringContaining('"expected_activation_revision":2'),
      })
    );
  });
});
