import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { AuditExplorer, redactSensitiveDeep } from "./audit-explorer";

describe("AuditExplorer", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("filters audit events server-side and redacts any sensitive fields in detail modal", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async () =>
      new Response(
        JSON.stringify({
          items: [
            {
              id: 42,
              org_id: "o1",
              actor_email: "admin@acme.test",
              action: "publication.rollback",
              target_type: "publication",
              target_id: "pub-1234567890",
              request_id: "req_abc",
              after_redacted: {
                active_version: 1,
                reason: "rollback bad route",
                api_key: "gwf_deadbeef1234.0123456789abcdef0123456789abcdef",
                secret_token: "must_never_render",
              },
              created_at: "2026-11-22T14:00:00Z",
            },
          ],
          total: 1,
          limit: 20,
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
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "admin" }],
        }}
      >
        <AuditExplorer />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("publication.rollback")).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText("Action"), { target: { value: "publication.rollback" } });
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining("action=publication.rollback"), expect.anything())
    );

    fireEvent.click(screen.getByRole("button", { name: "Inspect audit event 42" }));
    const dlg = screen.getByRole("dialog");
    const jsonText = within(dlg).getByTestId("audit-after-json").textContent ?? "";
    expect(jsonText).toContain('"active_version": 1');
    expect(jsonText).not.toContain("gwf_deadbeef1234");
    expect(jsonText).not.toContain("must_never_render");

    // Direct helper check
    expect(redactSensitiveDeep({ password: "abc", nested: { note: "key gwf_12345678.abcdefgh12345678" } })).toEqual({
      password: "[REDACTED]",
      nested: { note: "key [REDACTED]" },
    });
  });
});
