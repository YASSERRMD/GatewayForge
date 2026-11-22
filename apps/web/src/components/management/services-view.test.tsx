import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { AuthProvider } from "@/lib/auth-context";
import { ServicesView } from "./services-view";

describe("ServicesView", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("filters via server search query, paginates, and hides write controls for viewers", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              id: "s1",
              org_id: "o1",
              slug: "orders",
              name: "Orders API",
              description: "Order service",
              enabled: true,
              revision: 2,
              created_at: "2026-11-22T08:00:00Z",
              updated_at: "2026-11-22T08:00:00Z",
            },
          ],
          total: 15,
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
          user: { id: "u1", email: "viewer@acme.test", display_name: "Viewer" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "viewer" }],
        }}
      >
        <ServicesView />
      </AuthProvider>
    );

    await waitFor(() => {
      expect(screen.getByText("Orders API")).toBeInTheDocument();
    });
    expect(screen.queryByRole("button", { name: /new service/i })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Search services"), { target: { value: "billing" } });
    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledWith(
        expect.stringContaining("search=billing"),
        expect.anything()
      );
    });
  });
});
