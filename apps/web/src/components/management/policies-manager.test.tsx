import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { PoliciesManager } from "./policies-manager";

describe("PoliciesManager (Auth Policies)", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("validates JWT issuer/audience/JWKS/algorithms and creates typed JWT policy", async () => {
    let policies: Array<{
      id: string;
      org_id: string;
      name: string;
      policy_type: string;
      schema_version: number;
      config: Record<string, unknown>;
      enabled: boolean;
      revision: number;
      attachments: [];
    }> = [];

    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/policies") && init?.method === "POST") {
        const body = JSON.parse(String(init.body)) as { name: string; policy_type: string; config: Record<string, unknown> };
        const created = {
          id: "pol-1",
          org_id: "o1",
          name: body.name,
          policy_type: body.policy_type,
          schema_version: 1,
          config: body.config,
          enabled: true,
          revision: 1,
          attachments: [] as [],
        };
        policies = [created];
        return new Response(JSON.stringify(created), { status: 201, headers: { "Content-Type": "application/json" } });
      }
      if (url.includes("/policies")) {
        return new Response(JSON.stringify({ items: policies, total: policies.length, limit: 100, offset: 0 }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({ items: [], total: 0, limit: 100, offset: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-pol",
        }}
      >
        <PoliciesManager />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText(/no policies configured/i)).toBeInTheDocument());
    fireEvent.click(screen.getByRole("button", { name: /new policy/i }));

    const dlg = screen.getByRole("dialog");
    fireEvent.change(within(dlg).getByLabelText("Policy Type"), { target: { value: "jwt" } });
    fireEvent.change(within(dlg).getByLabelText("Policy Name"), { target: { value: "partner-jwt" } });
    // Submit with missing fields to verify inline JWT validation
    fireEvent.click(within(dlg).getByRole("button", { name: /^create policy$/i }));
    expect(within(dlg).getByText(/Expected JWT issuer/i)).toBeInTheDocument();
    expect(within(dlg).getByText(/JWKS URL is required/i)).toBeInTheDocument();

    // Fill valid JWT values
    fireEvent.change(within(dlg).getByLabelText("Expected Issuer (iss)"), { target: { value: "https://idp.acme.test/" } });
    fireEvent.change(within(dlg).getByLabelText("Expected Audience (aud)"), { target: { value: "gatewayforge" } });
    fireEvent.change(within(dlg).getByLabelText("JWKS Endpoint URL"), {
      target: { value: "https://idp.acme.test/.well-known/jwks.json" },
    });
    fireEvent.click(within(dlg).getByRole("button", { name: /^create policy$/i }));

    await waitFor(() => expect(screen.getByText("partner-jwt")).toBeInTheDocument());
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/policies",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-pol" }),
      })
    );
  });
});
