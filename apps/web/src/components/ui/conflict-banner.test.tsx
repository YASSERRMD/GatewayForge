import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { ServicesManager } from "@/components/management/services-manager";

describe("Optimistic concurrency conflict resolution UX", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("handles 409 stale revision conflict, reloads latest rev-2 without overwriting peer edits, and saves cleanly", async () => {
    let serverSvc = {
      id: "svc-1",
      org_id: "o1",
      slug: "orders",
      name: "Orders API",
      description: "Initial",
      enabled: true,
      revision: 1,
      created_at: "2026-11-22T12:00:00Z",
      updated_at: "2026-11-22T12:00:00Z",
    };

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/services/svc-1") && init?.method === "PATCH") {
        const headers = init.headers as Record<string, string>;
        if (headers["If-Match"] === 'W/"rev-1"') {
          // Simulate peer already updated to rev-2
          serverSvc = { ...serverSvc, name: "Orders API (Peer Updated)", revision: 2 };
          return new Response(
            JSON.stringify({
              status: 409,
              code: "revision_conflict",
              title: "Conflict",
              detail: "Service was modified by another user (expected rev-1, current rev-2).",
            }),
            { status: 409, headers: { "Content-Type": "application/json" } }
          );
        }
        const body = JSON.parse(String(init.body)) as { name: string };
        serverSvc = { ...serverSvc, name: body.name, revision: 3 };
        return new Response(JSON.stringify(serverSvc), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      if (url.endsWith("/services/svc-1")) {
        return new Response(JSON.stringify(serverSvc), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(
        JSON.stringify({ items: [serverSvc], total: 1, limit: 10, offset: 0 }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "userA@acme.test", display_name: "User A" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-cc",
        }}
      >
        <ServicesManager />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("Orders API")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: "Edit Orders API" }));

    const dlg = screen.getByRole("dialog");
    fireEvent.change(within(dlg).getByLabelText("Display Name"), { target: { value: "Orders A Edit" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /save changes/i }));

    // 409 conflict banner surfaces safely
    await waitFor(() => expect(within(dlg).getByTestId("conflict-resolution-banner")).toBeInTheDocument());
    expect(within(dlg).getByText(/Stale Revision Conflict \(409\)/i)).toBeInTheDocument();

    // Reload latest server revision (rev-2)
    fireEvent.click(within(dlg).getByRole("button", { name: /reload latest server state/i }));
    await waitFor(() => expect(within(dlg).getByLabelText("Display Name")).toHaveValue("Orders API (Peer Updated)"));

    // Re-apply merged edit on rev-2 -> succeeds with If-Match: W/"rev-2"
    fireEvent.change(within(dlg).getByLabelText("Display Name"), { target: { value: "Orders Merged v3" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(screen.getByText("Orders Merged v3")).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/services/svc-1",
      expect.objectContaining({
        method: "PATCH",
        headers: expect.objectContaining({ "If-Match": 'W/"rev-2"' }),
      })
    );
  });
});
