import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { RoutesManager } from "./routes-manager";

describe("RoutesManager", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("shows live conflict preview for overlapping route and submits after resolving conflict", async () => {
    const existingRoute = {
      id: "rt-1",
      org_id: "o1",
      service_id: "svc-1",
      upstream_pool_id: "pool-1",
      name: "Existing Orders",
      host: "api.gatewayforge.local",
      path_prefix: "/v1/orders",
      methods: ["GET"],
      strip_prefix: false,
      priority: 100,
      enabled: true,
      revision: 1,
    };
    let routesList = [existingRoute];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/services")) {
        return new Response(
          JSON.stringify({
            items: [{ id: "svc-1", org_id: "o1", slug: "orders", name: "Orders API", description: "", enabled: true, revision: 1, created_at: "", updated_at: "" }],
            total: 1,
            limit: 100,
            offset: 0,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/upstreams")) {
        return new Response(
          JSON.stringify({
            items: [{ id: "pool-1", org_id: "o1", name: "orders-pool", destination_class: "mock_local", connect_timeout_ms: 3000, read_timeout_ms: 15000, revision: 1 }],
            total: 1,
            limit: 100,
            offset: 0,
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/routes") && init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as { name: string; priority: number; path_prefix: string };
        const created = { ...existingRoute, id: "rt-2", name: body.name, priority: body.priority, path_prefix: body.path_prefix };
        routesList = [...routesList, created];
        return new Response(JSON.stringify(created), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(
        JSON.stringify({ items: routesList, total: routesList.length, limit: 15, offset: 0 }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "ops@acme.test", display_name: "Ops" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-rt",
        }}
      >
        <RoutesManager />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("Existing Orders")).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /new route/i }));

    const dlg = screen.getByRole("dialog");
    // Default host/path/priority/GET overlaps with Existing Orders
    expect(within(dlg).getByText(/Potential Route Conflict Detected/i)).toBeInTheDocument();

    // Resolve conflict by changing priority
    fireEvent.change(within(dlg).getByLabelText("Route Name"), { target: { value: "Orders Priority 200" } });
    fireEvent.change(within(dlg).getByLabelText("Priority"), { target: { value: "200" } });
    expect(within(dlg).queryByText(/Potential Route Conflict Detected/i)).not.toBeInTheDocument();

    fireEvent.click(within(dlg).getByRole("button", { name: /^create route$/i }));
    await waitFor(() => expect(screen.getByText("Orders Priority 200")).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/routes",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-rt" }),
      })
    );
  });
});
