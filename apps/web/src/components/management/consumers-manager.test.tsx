import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { ConsumersManager } from "./consumers-manager";

describe("ConsumersManager", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("creates and suspends a consumer for operators and hides write controls for viewers", async () => {
    let consumers = [
      {
        id: "c1",
        org_id: "o1",
        name: "billing-worker",
        description: "Cron jobs",
        owner_email: "billing@acme.test",
        status: "active" as "active" | "suspended",
        revision: 1,
        created_at: "2026-11-22T12:00:00Z",
      },
    ];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/consumers") && init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as { name: string; owner_email: string };
        const created = {
          id: "c2",
          org_id: "o1",
          name: body.name,
          description: "",
          owner_email: body.owner_email,
          status: "active" as const,
          revision: 1,
          created_at: "2026-11-22T12:05:00Z",
        };
        consumers = [...consumers, created];
        return new Response(JSON.stringify(created), { status: 201, headers: { "Content-Type": "application/json" } });
      }
      if (url.includes("/consumers/c1") && init?.method === "PATCH") {
        consumers = consumers.map((c) => (c.id === "c1" ? { ...c, status: "suspended", revision: 2 } : c));
        return new Response(JSON.stringify(consumers[0]), { status: 200, headers: { "Content-Type": "application/json" } });
      }
      return new Response(
        JSON.stringify({ items: consumers, total: consumers.length, limit: 50, offset: 0 }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    const { unmount } = render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "admin" }],
          csrfToken: "csrf-c",
        }}
      >
        <ConsumersManager />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("billing-worker")).toBeInTheDocument());

    fireEvent.click(screen.getByRole("button", { name: "Suspend billing-worker" }));
    await waitFor(() => expect(screen.getByText("Suspended")).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/consumers/c1",
      expect.objectContaining({
        method: "PATCH",
        headers: expect.objectContaining({ "If-Match": 'W/"rev-1"', "X-CSRF-Token": "csrf-c" }),
      })
    );

    fireEvent.click(screen.getByRole("button", { name: /new consumer/i }));
    const dlg = screen.getByRole("dialog");
    fireEvent.change(within(dlg).getByLabelText("Consumer Name"), { target: { value: "mobile-ios" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /^create consumer$/i }));
    await waitFor(() => expect(screen.getByText("mobile-ios")).toBeInTheDocument());

    unmount();

    // Viewer cannot create or suspend
    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u2", email: "viewer@acme.test", display_name: "Viewer" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "viewer" }],
        }}
      >
        <ConsumersManager />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("billing-worker")).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /new consumer/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /suspend/i })).not.toBeInTheDocument();
  });
});
