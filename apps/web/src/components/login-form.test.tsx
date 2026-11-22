import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { AuthProvider } from "@/lib/auth-context";
import { LoginForm } from "./login-form";

const replaceMock = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: replaceMock, push: vi.fn() }),
}));

describe("LoginForm", () => {
  beforeEach(() => {
    replaceMock.mockReset();
    vi.restoreAllMocks();
  });

  it("shows session-expired notice and validates empty fields", () => {
    render(
      <AuthProvider initialState={{ status: "expired", user: null, memberships: [] }}>
        <LoginForm sessionExpired={true} />
      </AuthProvider>
    );
    expect(screen.getByRole("status")).toHaveTextContent(/session expired/i);
    fireEvent.click(screen.getByRole("button", { name: /^sign in$/i }));
    expect(screen.getByText(/valid email/i)).toBeInTheDocument();
    expect(screen.getByText(/at least 8 characters/i)).toBeInTheDocument();
  });

  it("surfaces Problem Details error on failed login and navigates on success", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ detail: "Invalid email or password." }), {
          status: 401,
          headers: { "Content-Type": "application/problem+json" },
        })
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ csrf_token: "csrf-1" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        })
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            user: { id: "u1", email: "ops@acme.test", display_name: "Ops" },
            memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "owner" }],
            csrf_token: "csrf-1",
          }),
          { status: 200, headers: { "Content-Type": "application/json" } }
        )
      );

    render(
      <AuthProvider initialState={{ status: "unauthenticated", user: null, memberships: [] }}>
        <LoginForm />
      </AuthProvider>
    );

    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "ops@acme.test" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "WrongPassword!1" } });
    fireEvent.click(screen.getByRole("button", { name: /^sign in$/i }));

    await waitFor(() => {
      expect(screen.getByText("Invalid email or password.")).toBeInTheDocument();
    });

    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "RightPassword!99" } });
    fireEvent.click(screen.getByRole("button", { name: /^sign in$/i }));

    await waitFor(() => {
      expect(replaceMock).toHaveBeenCalledWith("/");
    });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});
