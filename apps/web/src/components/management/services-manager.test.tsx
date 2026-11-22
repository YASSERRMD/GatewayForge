import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { AuthProvider } from "@/lib/auth-context";
import { ServicesManager } from "./services-manager";

describe("ServicesManager", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("validates slug/name and creates a service with CSRF token", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ items: [], total: 0, limit: 10, offset: 0 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: "svc-new", slug: "payments", name: "Payments" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            items: [
              {
                id: "svc-new",
                org_id: "o1",
                slug: "payments",
                name: "Payments",
                description: "",
                enabled: true,
                revision: 1,
                created_at: "2026-11-22T12:00:00Z",
                updated_at: "2026-11-22T12:00:00Z",
              },
            ],
            total: 1,
            limit: 10,
            offset: 0,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        )
      );

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-svc",
        }}
      >
        <ServicesManager />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText(/no services yet/i)).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /new service/i }));

    const dlg = screen.getByRole("dialog");
    fireEvent.change(within(dlg).getByLabelText("Slug"), { target: { value: "BAD SLUG!" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /^create service$/i }));
    expect(within(dlg).getByText(/lowercase letters/i)).toBeInTheDocument();

    fireEvent.change(within(dlg).getByLabelText("Slug"), { target: { value: "payments" } });
    fireEvent.change(within(dlg).getByLabelText("Display Name"), { target: { value: "Payments" } });
    fireEvent.click(within(dlg).getByRole("button", { name: /^create service$/i }));

    await waitFor(() => expect(screen.getByText("Payments")).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/services",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-svc" }),
      })
    );
  });
});
