import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "@/lib/auth-context";
import type { ConsumerItem } from "@/lib/api/types";
import { ApiKeysDialog } from "./api-keys-dialog";

const CONSUMER: ConsumerItem = {
  id: "c-1",
  org_id: "o1",
  name: "checkout-client",
  description: "",
  owner_email: "ops@acme.test",
  status: "active",
  revision: 1,
  created_at: "2026-11-22T12:00:00Z",
};

describe("ApiKeysDialog", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    window.localStorage.clear();
    window.sessionStorage.clear();
  });

  it("reveals plaintext API key once, never persists it, and permanently wipes it on dismiss/close", async () => {
    const SECRET = "gwf_a1b2c3d4e5f6.0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef";
    let keyList = [] as Array<{
      id: string;
      consumer_id: string;
      key_prefix: string;
      label: string;
      scopes: string[];
      revoked_at: string | null;
      created_at: string;
    }>;

    vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/keys") && init?.method === "POST") {
        const meta = {
          id: "k-1",
          consumer_id: "c-1",
          key_prefix: "gwf_a1b2c3d4e5f6",
          label: "prod",
          scopes: ["read", "write"],
          revoked_at: null,
          created_at: "2026-11-22T12:10:00Z",
        };
        keyList = [meta];
        return new Response(JSON.stringify({ ...meta, api_key: SECRET }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      if (url.includes("/keys/k-1/revoke") && init?.method === "POST") {
        keyList = keyList.map((k) => ({ ...k, revoked_at: "2026-11-22T12:12:00Z" }));
        return new Response(JSON.stringify(keyList[0]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response(JSON.stringify({ items: keyList, total: keyList.length }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    });

    const onClose = vi.fn();
    const { rerender } = render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-k",
        }}
      >
        <ApiKeysDialog consumer={CONSUMER} onClose={onClose} />
      </AuthProvider>
    );

    await waitFor(() => expect(screen.getByText(/no api keys have been issued/i)).toBeInTheDocument());

    fireEvent.change(screen.getByLabelText("Key Label"), { target: { value: "prod" } });
    fireEvent.change(screen.getByLabelText("Scopes (comma-separated)"), { target: { value: "read, write" } });
    fireEvent.click(screen.getByRole("button", { name: "Issue API Key" }));

    // Secret shown once
    await waitFor(() => expect(screen.getByTestId("one-time-secret-value")).toHaveTextContent(SECRET));

    // Verify zero persistence in browser storage
    expect(JSON.stringify(window.localStorage)).not.toContain(SECRET);
    expect(JSON.stringify(window.sessionStorage)).not.toContain(SECRET);

    // Dismiss wipes secret permanently
    fireEvent.click(screen.getByRole("button", { name: /i have stored this key securely/i }));
    expect(screen.queryByText(SECRET)).not.toBeInTheDocument();

    // Metadata prefix remains visible; revoke works
    expect(screen.getByText("gwf_a1b2c3d4e5f6…")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Revoke key gwf_a1b2c3d4e5f6" }));
    await waitFor(() => expect(screen.getByText("Revoked")).toBeInTheDocument());

    // Closing and reopening never re-renders the plaintext secret
    rerender(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-k",
        }}
      >
        <ApiKeysDialog consumer={null} onClose={onClose} />
      </AuthProvider>
    );
    rerender(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "admin@acme.test", display_name: "Admin" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
          csrfToken: "csrf-k",
        }}
      >
        <ApiKeysDialog consumer={CONSUMER} onClose={onClose} />
      </AuthProvider>
    );
    await waitFor(() => expect(screen.getByText("gwf_a1b2c3d4e5f6…")).toBeInTheDocument());
    expect(screen.queryByText(SECRET)).not.toBeInTheDocument();
  });
});
