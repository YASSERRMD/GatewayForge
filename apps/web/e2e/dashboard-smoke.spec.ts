import { test, expect } from "@playwright/test";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@gatewayforge.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "GatewayForge!Admin2026";
const CONTROL_PLANE_URL = process.env.E2E_CONTROL_PLANE_URL || "http://127.0.0.1:18080";

test.describe("Dashboard End-to-End Smoke", () => {
  test.beforeAll(async ({ request }) => {
    // Ensure fresh local Compose instances have an initialized first admin without failing if already bootstrapped.
    await request.post(`${CONTROL_PLANE_URL}/api/v1/auth/bootstrap`, {
      data: {
        email: ADMIN_EMAIL,
        password: ADMIN_PASSWORD,
        display_name: "Studio Admin",
        org_name: "GatewayForge Demo",
        org_slug: "gatewayforge-demo",
      },
    });
  });

  test("unauthenticated redirect -> login -> overview aggregates -> time range switch -> logout", async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    // 1. Visiting / unauthenticated redirects cleanly to /login
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);
    await expect(page.getByRole("heading", { name: /sign in to control plane/i })).toBeVisible();

    // 2. Sign in with real credentials
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: /^sign in$/i }).click();

    // 3. Lands on Overview dashboard with real organization context and metric cards
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("heading", { name: /^overview$/i })).toBeVisible();
    await expect(page.getByLabel("Active organization")).toBeVisible();
    await expect(page.getByTestId("metric-rps")).toBeVisible();
    await expect(page.getByTestId("metric-error-rate")).toBeVisible();
    await expect(page.getByTestId("metric-p95")).toBeVisible();
    await expect(page.getByTestId("metric-gateways")).toBeVisible();

    // 4. Switch time range from 1h to 15m and verify accessible pressed state updates
    const btn15m = page.getByRole("button", { name: "15m" });
    await btn15m.click();
    await expect(btn15m).toHaveAttribute("aria-pressed", "true");

    // 5. Verify Services & Routes Summary and Recent Activity panels render cleanly
    await expect(page.getByRole("heading", { name: /services & routes summary/i })).toBeVisible();
    await expect(page.getByRole("heading", { name: /recent activity/i })).toBeVisible();

    // 6. Sign out and confirm protected route redirects back to /login
    await page.getByRole("button", { name: /sign out/i }).click();
    await expect(page).toHaveURL(/\/login/);
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);

    expect(pageErrors).toEqual([]);
  });
});
