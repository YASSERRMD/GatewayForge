import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { DraftPublishPanel } from "./draft-publish-panel";

describe("DraftPublishPanel", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("requires explicit confirmation to publish valid draft and hides publish button for developers", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/publications") && init?.method === "POST") {
        return new Response(JSON.stringify({ id: "pub-1", version: 1, is_active: true }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(
        JSON.stringify({
          draft_revision: 4,
          validation: {
            valid: true,
            draft_revision: 4,
            errors: [],
            warnings: [{ code: "pool_no_enabled_targets", severity: "warning", resource: "pool:unused", message: "Zero enabled targets" }],
          },
          preview: {
            checksum: "abc123def4567890",
            routes: [{ id: "r1" }],
            upstreams: { p1: {} },
          },
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      );
    });

    const onPublished = vi.fn();
    const { unmount } = render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "admin" }],
          csrfToken: "csrf-pub",
        }}
      >
        <DraftPublishPanel onPublished={onPublished} />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText("Ready to Publish")).toBeInTheDocument());
    expect(screen.getByText(/Zero enabled targets/i)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /review & publish/i }));
    const dlg = screen.getByRole("dialog");
    const confirmBtn = within(dlg).getByRole("button", { name: /confirm publish/i });
    expect(confirmBtn).toBeDisabled();

    fireEvent.change(within(dlg).getByLabelText("Release Notes"), { target: { value: "Release v1" } });
    fireEvent.click(within(dlg).getByLabelText(/i have reviewed/i));
    expect(confirmBtn).not.toBeDisabled();
    fireEvent.click(confirmBtn);

    await waitFor(() => expect(onPublished).toHaveBeenCalledTimes(1));
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/orgs/o1/publications",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ "X-CSRF-Token": "csrf-pub" }),
        body: expect.stringContaining('"expected_draft_revision":4'),
      })
    );

    unmount();

    // Developer cannot publish
    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u2", email: "dev@acme.test", display_name: "Dev" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "developer" }],
        }}
      >
        <DraftPublishPanel />
      </AuthProvider>
    );
    await waitFor(() => expect(screen.getByText(/cannot publish or activate releases/i)).toBeInTheDocument());
    expect(screen.queryByRole("button", { name: /review & publish/i })).not.toBeInTheDocument();
  });

  it("blocks publication and surfaces actionable validation errors while preserving LKG", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          draft_revision: 7,
          validation: {
            valid: false,
            draft_revision: 7,
            errors: [
              {
                code: "invalid_rate_limit_policy",
                severity: "error",
                resource: "policy:unsafe-rl",
                message: "critical security_class cannot use fail_open",
              },
            ],
            warnings: [],
          },
          preview: { checksum: "deadbeef", routes: [], upstreams: {} },
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
        }}
      >
        <DraftPublishPanel />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText(/Publication Blocked \(Last-Known-Good Preserved\)/i)).toBeInTheDocument());
    expect(screen.getByText(/policy:unsafe-rl/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /review & publish/i })).toBeDisabled();
  });
});
