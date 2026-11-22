import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { GatewayConvergencePanel } from "./gateway-convergence-panel";

describe("GatewayConvergencePanel", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("highlights stale/lagging replicas cautiously and shows converged state when all match", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          expected_version: 3,
          items: [
            {
              instance_id: "gateway-1",
              org_id: "o1",
              active_version: 3,
              checksum: "aaaaaaaaaaaaaaaa",
              ready: true,
              last_seen_at: "2026-11-22T14:00:00Z",
              status: "healthy",
              converged: true,
              age_sec: 2,
            },
            {
              instance_id: "gateway-2",
              org_id: "o1",
              active_version: 2,
              checksum: "bbbbbbbbbbbbbbbb",
              ready: true,
              last_seen_at: "2026-11-22T13:59:00Z",
              status: "stale",
              converged: false,
              age_sec: 65,
            },
          ],
          data_available: true,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      )
    );

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "admin" }],
        }}
      >
        <GatewayConvergencePanel />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("1/2 Converged")).toBeInTheDocument());
    expect(screen.getByText("gateway-1")).toBeInTheDocument();
    expect(screen.getByText("gateway-2")).toBeInTheDocument();
    expect(screen.getByText("stale")).toBeInTheDocument();
    expect(screen.getByText("Lagging")).toBeInTheDocument();
    expect(screen.getByText(/1 gateway instance is lagging, stale, or offline/i)).toBeInTheDocument();
  });
});
