import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import { MAX_CONSOLE_BODY_BYTES, RequestConsole } from "./request-console";

describe("RequestConsole", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("enforces body size cap, supports keyboard Ctrl+Enter, and prefills route host/path", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          items: [
            {
              id: "r1",
              org_id: "o1",
              service_id: "s1",
              upstream_pool_id: "p1",
              name: "Orders API",
              host: "orders.local",
              path_prefix: "/v1/orders",
              methods: ["POST", "GET"],
              strip_prefix: false,
              priority: 100,
              enabled: true,
              revision: 1,
            },
          ],
          total: 1,
          limit: 100,
          offset: 0,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } }
      )
    );

    const onExecute = vi.fn().mockResolvedValue({
      status: 200,
      duration_ms: 14,
      headers: { "content-type": ["application/json"] },
      body: '{"ok":true}',
      body_truncated: false,
      body_bytes: 11,
      is_binary: false,
      content_type: "application/json",
      gateway_target: "http://gateway:8081",
    });

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "dev@acme.test", display_name: "Dev" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "developer" }],
          csrfToken: "csrf-con",
        }}
      >
        <RequestConsole onExecuteOverride={onExecute} />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText(/Orders API/i)).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Prefill from Published/Draft Route"), { target: { value: "r1" } });
    expect(screen.getByLabelText("Route Host")).toHaveValue("orders.local");
    expect(screen.getByLabelText("Path & Query")).toHaveValue("/v1/orders");

    // Exceed 64 KiB body cap
    fireEvent.change(screen.getByLabelText("Request Body"), {
      target: { value: "x".repeat(MAX_CONSOLE_BODY_BYTES + 10) },
    });
    fireEvent.click(screen.getByRole("button", { name: /send request/i }));
    expect(screen.getByText(/exceeds maximum cap/i)).toBeInTheDocument();
    expect(onExecute).not.toHaveBeenCalled();

    // Valid body + Ctrl+Enter shortcut
    fireEvent.change(screen.getByLabelText("Request Body"), { target: { value: '{"item":"book"}' } });
    fireEvent.keyDown(screen.getByLabelText("Request Body"), { key: "Enter", ctrlKey: true });

    await waitFor(() => expect(screen.getByText("HTTP 200")).toBeInTheDocument());
    expect(onExecute).toHaveBeenCalledWith(
      expect.objectContaining({ method: "POST", host: "orders.local", path: "/v1/orders" })
    );
  });

  it("blocks viewers from executing console requests", () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ items: [], total: 0, limit: 100, offset: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      })
    );

    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u2", email: "viewer@acme.test", display_name: "Viewer" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "viewer" }],
        }}
      >
        <RequestConsole />
      </AuthProvider>
    );

    expect(screen.getByText(/Read-only role/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /send request/i })).toBeDisabled();
  });
});
