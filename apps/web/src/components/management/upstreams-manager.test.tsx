import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { UpstreamsManager } from "./upstreams-manager";

function renderWithRole(role: "owner" | "admin" | "developer" | "viewer") {
  return render(
    <AuthProvider
      initialState={{
        status: "authenticated",
        user: { id: "u1", email: "ops@example.com", display_name: "Ops" },
        memberships: [{ org_id: "org-1", org_slug: "acme", org_name: "Acme", role }],
        csrfToken: "csrf-123",
      }}
    >
      <UpstreamsManager />
    </AuthProvider>
  );
}

describe("UpstreamsManager", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("rejects SSRF metadata target with safe error display and recovers with approved target", async () => {
    let addedTargets = [
      { id: "t1", pool_id: "p1", url: "http://mock-upstream:8089", weight: 100, enabled: true },
    ];

    vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/upstreams/health")) {
        return new Response(
          JSON.stringify({
            items: [
              {
                pool_id: "p1",
                pool_name: "orders-pool",
                destination_class: "mock_local",
                status: "healthy",
                enabled_targets: addedTargets.length,
                disabled_targets: 0,
                total_targets: addedTargets.length,
              },
            ],
            total: 1,
            visibility: "operator",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      if (url.includes("/targets") && init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as { url: string; weight: number };
        if (body.url.includes("169.254.169.254")) {
          return new Response(
            JSON.stringify({
              status: 422,
              code: "validation_error",
              title: "Validation Failed",
              detail: "Invalid target URL.",
              errors: [{ field: "url", message: "blocked link-local/metadata IP address" }],
            }),
            { status: 422, headers: { "Content-Type": "application/json" } }
          );
        }
        addedTargets = [
          ...addedTargets,
          { id: "t2", pool_id: "p1", url: body.url, weight: body.weight, enabled: true },
        ];
        return new Response(JSON.stringify(addedTargets[1]), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(
        JSON.stringify({
          items: [
            {
              id: "p1",
              org_id: "org-1",
              name: "orders-pool",
              destination_class: "mock_local",
              connect_timeout_ms: 3000,
              read_timeout_ms: 15000,
              revision: 1,
              targets: addedTargets,
            },
          ],
          total: 1,
          limit: 50,
          offset: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    renderWithRole("owner");
    await waitFor(() => expect(screen.getByText("orders-pool")).toBeInTheDocument());
    expect(screen.getByText("healthy")).toBeInTheDocument();

    // Attempt bad SSRF metadata target
    fireEvent.click(screen.getByRole("button", { name: "Add Target" }));
    const dialog = screen.getByRole("dialog");
    fireEvent.change(within(dialog).getByLabelText("Target URL"), {
      target: { value: "http://169.254.169.254/latest/meta-data" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add Target" }));

    await waitFor(() =>
      expect(within(dialog).getByText(/blocked link-local\/metadata IP address/i)).toBeInTheDocument()
    );

    // Recover with valid target
    fireEvent.change(within(dialog).getByLabelText("Target URL"), {
      target: { value: "http://mock-upstream:8089/v2" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "Add Target" }));

    await waitFor(() => expect(screen.getByText("http://mock-upstream:8089/v2")).toBeInTheDocument());
  });

  it("hides mutation buttons for viewer role", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ items: [], total: 0, limit: 50, offset: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    );

    renderWithRole("viewer");
    await waitFor(() => expect(screen.getByText(/No upstream pools configured/i)).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: "Create Upstream Pool" })).not.toBeInTheDocument();
  });
});
