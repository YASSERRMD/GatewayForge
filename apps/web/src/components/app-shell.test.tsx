import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AuthProvider } from "@/lib/auth-context";
import { AppShell } from "./app-shell";

const replaceMock = vi.fn();

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ replace: replaceMock, push: vi.fn() }),
}));

describe("AppShell", () => {
  beforeEach(() => {
    replaceMock.mockReset();
  });

  it("redirects unauthenticated users to /login", () => {
    render(
      <AuthProvider initialState={{ status: "unauthenticated", user: null, memberships: [] }}>
        <AppShell>
          <div>Protected Dashboard</div>
        </AppShell>
      </AuthProvider>
    );
    expect(replaceMock).toHaveBeenCalledWith("/login");
    expect(screen.queryByText("Protected Dashboard")).not.toBeInTheDocument();
  });

  it("renders sidebar navigation, user context, and mobile drawer toggle when authenticated", () => {
    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "ops@acme.test", display_name: "Ops Lead" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme Corp", role: "owner" }],
        }}
      >
        <AppShell>
          <h1>Overview Content</h1>
        </AppShell>
      </AuthProvider>
    );
    expect(screen.getByRole("heading", { name: "Overview Content" })).toBeInTheDocument();
    expect(screen.getByText("Acme Corp")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Services" })).toBeInTheDocument();

    const menuBtn = screen.getByRole("button", { name: /open navigation menu/i });
    fireEvent.click(menuBtn);
    expect(screen.getByRole("dialog", { name: /mobile navigation/i })).toBeInTheDocument();
  });
});
