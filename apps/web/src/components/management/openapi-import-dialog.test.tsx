import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { OpenAPIImportDialog } from "./openapi-import-dialog";

describe("OpenAPIImportDialog conflict resolution", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("defaults colliding routes to skip (never silent overwrite) and submits explicit rename choices", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/openapi/preview")) {
        return new Response(
          JSON.stringify({
            openapi_version: "3.0.3",
            service: { slug: "orders-api", name: "Orders API", description: "" },
            service_exists: true,
            existing_service_id: "svc-1",
            default_host: "api.gatewayforge.local",
            base_path: "",
            routes: [
              {
                key: "api.gatewayforge.local:/v1/orders",
                name: "orders-api-v1-orders",
                host: "api.gatewayforge.local",
                path_prefix: "/v1/orders",
                methods: ["GET"],
                strip_prefix: false,
                priority: 110,
                source_path: "/v1/orders",
                conflict: true,
                existing_route_id: "rt-existing",
                conflict_reason: "An existing route already uses this host and path_prefix.",
                default_action: "skip",
              },
            ],
            warnings: [{ code: "path_template_normalized", message: "Normalized /v1/orders/{id}" }],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        );
      }
      return new Response(JSON.stringify({ imported_routes: 1, skipped_routes: 0 }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      });
    });

    const onImported = vi.fn();
    const onClose = vi.fn();

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-oa",
        }}
      >
        <OpenAPIImportDialog
          open={true}
          pools={[
            {
              id: "pool-1",
              org_id: "o1",
              name: "mock-pool",
              destination_class: "mock_local",
              connect_timeout_ms: 3000,
              read_timeout_ms: 15000,
              revision: 1,
            },
          ]}
          onClose={onClose}
          onImported={onImported}
        />
      </AuthProvider>
    );

    fireEvent.change(screen.getByLabelText("OpenAPI Spec (YAML or JSON)"), {
      target: { value: "openapi: '3.0.3'\ninfo:\n  title: Orders API\npaths:\n  /v1/orders:\n    get: {}" },
    });
    fireEvent.click(screen.getByRole("button", { name: /preview import/i }));

    await waitFor(() => expect(screen.getByText("Collision")).toBeInTheDocument());
    expect(screen.getByText(/Normalized \/v1\/orders\/\{id\}/i)).toBeInTheDocument();

    // Colliding route MUST default to skip (never silent overwrite)
    const select = screen.getByLabelText("Resolution for /v1/orders");
    expect(select).toHaveValue("skip");

    // Operator chooses rename and commits
    fireEvent.change(select, { target: { value: "rename" } });
    fireEvent.change(screen.getByLabelText("New path prefix for /v1/orders"), {
      target: { value: "/v2/orders" },
    });
    fireEvent.click(screen.getByRole("button", { name: /commit draft import/i }));

    await waitFor(() => expect(onImported).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/openapi/import",
      expect.objectContaining({
        method: "POST",
        body: expect.stringContaining('"/v2/orders"'),
      })
    );
  });
});
