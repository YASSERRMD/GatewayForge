import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AuthProvider } from "@/lib/auth-context";
import { AppShell } from "./app-shell";
import { LoginForm } from "./login-form";

vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
}));

describe("Responsive & Keyboard Accessibility", () => {
  it("provides skip link, ARIA landmarks, current-page nav marker, and Escape-dismissible mobile drawer", () => {
    render(
      <AuthProvider
        initialState={{
          status: "authenticated",
          user: { id: "u1", email: "a11y@acme.test", display_name: "A11y Tester" },
          memberships: [{ org_id: "o1", org_slug: "acme", org_name: "Acme", role: "admin" }],
        }}
      >
        <AppShell>
          <h1>Accessible Overview</h1>
        </AppShell>
      </AuthProvider>
    );

    const skip = screen.getByRole("link", { name: /skip to main content/i });
    expect(skip).toHaveAttribute("href", "#main-content");
    expect(screen.getByRole("main")).toHaveAttribute("id", "main-content");
    expect(screen.getByRole("banner")).toBeInTheDocument();
    expect(screen.getByRole("navigation", { name: /primary navigation/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Overview" })).toHaveAttribute("aria-current", "page");

    const openBtn = screen.getByRole("button", { name: /open navigation menu/i });
    expect(openBtn).toHaveAttribute("aria-expanded", "false");
    fireEvent.click(openBtn);
    expect(openBtn).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByRole("dialog", { name: /mobile navigation/i })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("dialog", { name: /mobile navigation/i })).not.toBeInTheDocument();
    expect(openBtn).toHaveAttribute("aria-expanded", "false");
  });

  it("exposes accessible labels and aria-invalid states on login controls", () => {
    render(
      <AuthProvider initialState={{ status: "unauthenticated", user: null, memberships: [] }}>
        <LoginForm />
      </AuthProvider>
    );
    const emailInput = screen.getByLabelText("Email");
    const pwInput = screen.getByLabelText("Password");
    expect(emailInput).toHaveAttribute("aria-invalid", "false");
    expect(pwInput).toHaveAttribute("aria-invalid", "false");

    fireEvent.click(screen.getByRole("button", { name: /^sign in$/i }));
    expect(emailInput).toHaveAttribute("aria-invalid", "true");
    expect(pwInput).toHaveAttribute("aria-invalid", "true");
    expect(screen.getAllByRole("alert").length).toBeGreaterThanOrEqual(2);
  });
});
