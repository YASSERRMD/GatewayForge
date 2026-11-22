import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { ConsoleWorkspace } from "./console-workspace";
import { CONSOLE_ENVIRONMENT_PRESETS, interpolatePresetVariables } from "./environment-presets";

describe("Console environment variable presets", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("switches route host preset cleanly without exposing any credentials", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ items: [], total: 0, limit: 100, offset: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    );

    // Ensure no preset variable embeds credentials or secrets
    const varsSerialized = JSON.stringify(CONSOLE_ENVIRONMENT_PRESETS.map((p) => p.variables));
    expect(varsSerialized).not.toMatch(/gwf_|Bearer|password|secret|token/i);

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "dev@acme.test", display_name: "Dev" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "developer" }],
          csrfToken: "csrf-env",
        }}
      >
        <ConsoleWorkspace />
      </AuthProvider>
    );

    expect(screen.getByLabelText("Route Host")).toHaveValue("api.gatewayforge.local");

    fireEvent.change(screen.getByLabelText("Environment preset"), { target: { value: "mock-sandbox" } });
    expect(screen.getByLabelText("Route Host")).toHaveValue("mock.gatewayforge.local");

    fireEvent.change(screen.getByLabelText("Environment preset"), { target: { value: "staging-sim" } });
    expect(screen.getByLabelText("Route Host")).toHaveValue("staging.gatewayforge.local");

    expect(interpolatePresetVariables("/{{api_version}}/orders?env={{env}}", { api_version: "v1", env: "mock" })).toBe(
      "/v1/orders?env=mock"
    );
  });
});
