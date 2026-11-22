import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { RoutesView } from "./routes-view";

describe("RoutesView", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("displays route priority, methods, resolved service/upstream, and opens details modal", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL) => {
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
      return new Response(
        JSON.stringify({
          items: [
            {
              id: "rt-1",
              org_id: "o1",
              service_id: "svc-1",
              upstream_pool_id: "pool-1",
              name: "Get Orders",
              host: "api.local",
              path_prefix: "/v1/orders",
              methods: ["GET", "POST"],
              strip_prefix: true,
              priority: 120,
              enabled: true,
              revision: 2,
            },
          ],
          total: 1,
          limit: 15,
          offset: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "ops@acme.test", display_name: "Ops" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "viewer" }],
        }}
      >
        <RoutesView onCreate={vi.fn()} onEdit={vi.fn()} onDelete={vi.fn()} />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("Get Orders")).toBeInTheDocument());
    expect(screen.getByText("120")).toBeInTheDocument();
    expect(screen.getByText("Orders API")).toBeInTheDocument();
    expect(screen.getByText("Pool: orders-pool")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /new route/i })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Inspect Get Orders" }));
    const dlg = screen.getByRole("dialog");
    expect(within(dlg).getByText("Yes (strip matched prefix)")).toBeInTheDocument();
    expect(within(dlg).getByText("rev-2")).toBeInTheDocument();
  });
});
