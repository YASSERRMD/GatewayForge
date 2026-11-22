import { test, expect } from "@playwright/test";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@gatewayforge.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "GatewayForge!Admin2026";
const CONTROL_PLANE_URL = process.env.E2E_CONTROL_PLANE_URL || "http://127.0.0.1:18080";

test.describe("Management UI End-to-End Workflow", () => {
  test.beforeAll(async ({ request }) => {
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

  test("service -> upstream (with SSRF check) -> route -> consumer + one-time key -> policy -> admin API verification", async ({
    page,
  }) => {
    const suffix = String(Date.now()).slice(-6);
    const svcSlug = `e2e-orders-${suffix}`;
    const svcName = `E2E Orders ${suffix}`;
    const poolName = `e2e-pool-${suffix}`;
    const routeName = `E2E Route ${suffix}`;
    const routePath = `/v1/e2e-${suffix}`;
    const consumerName = `e2e-consumer-${suffix}`;
    const policyName = `e2e-ratelimit-${suffix}`;

    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    // 1. Login
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/$/);

    // 2. Create Service
    await page.getByRole("link", { name: /^services$/i }).click();
    await expect(page).toHaveURL(/\/services$/);
    await page.getByRole("button", { name: /new service/i }).click();
    const svcDialog = page.getByRole("dialog");
    await svcDialog.getByLabel("Slug").fill(svcSlug);
    await svcDialog.getByLabel("Display Name").fill(svcName);
    await svcDialog.getByRole("button", { name: /^create service$/i }).click();
    await expect(page.getByText(svcName)).toBeVisible();

    // 3. Create Upstream Pool & verify SSRF target rejection
    await page.getByRole("link", { name: /^upstreams$/i }).click();
    await expect(page).toHaveURL(/\/upstreams$/);
    await page.getByRole("button", { name: /create upstream pool/i }).first().click();
    const poolDialog = page.getByRole("dialog");
    await poolDialog.getByLabel("Pool Name").fill(poolName);
    await poolDialog.getByLabel("Destination Class").selectOption("mock_local");
    await poolDialog.getByLabel("Initial Target URL").fill("http://mock-upstream:8089");
    await poolDialog.getByRole("button", { name: /^create pool$/i }).click();
    await expect(page.getByText(poolName)).toBeVisible();

    // Attempt SSRF metadata target and confirm server rejects it safely
    await page.getByRole("button", { name: "Add Target" }).first().click();
    const targetDialog = page.getByRole("dialog");
    await targetDialog.getByLabel("Target URL").fill("http://169.254.169.254/latest/meta-data");
    await targetDialog.getByRole("button", { name: /^add target$/i }).click();
    await expect(targetDialog.getByRole("alert")).toBeVisible();
    await targetDialog.getByRole("button", { name: /^cancel$/i }).click();

    // 4. Create Route attached to the new Service & Pool
    await page.getByRole("link", { name: /^routes$/i }).click();
    await expect(page).toHaveURL(/\/routes$/);
    await page.getByRole("button", { name: /new route/i }).click();
    const routeDialog = page.getByRole("dialog");
    await routeDialog.getByLabel("Route Name").fill(routeName);
    await routeDialog.getByLabel("Service").selectOption({ label: `${svcName} (${svcSlug})` });
    await routeDialog.getByLabel("Upstream Pool").selectOption({ label: poolName });
    await routeDialog.getByLabel("Host").fill("api.gatewayforge.local");
    await routeDialog.getByLabel("Path Prefix", { exact: true }).fill(routePath);
    await routeDialog.getByRole("button", { name: /^create route$/i }).click();
    await expect(page.getByText(routeName)).toBeVisible();

    // 5. Create Consumer & issue one-time API key
    await page.getByRole("link", { name: /^consumers$/i }).click();
    await expect(page).toHaveURL(/\/consumers$/);
    await page.getByRole("button", { name: /new consumer/i }).click();
    const consDialog = page.getByRole("dialog");
    await consDialog.getByLabel("Consumer Name").fill(consumerName);
    await consDialog.getByLabel("Owner Email").fill("e2e@gatewayforge.local");
    await consDialog.getByRole("button", { name: /^create consumer$/i }).click();
    await expect(page.getByText(consumerName)).toBeVisible();

    await page.getByRole("button", { name: `Manage API Keys for ${consumerName}` }).click();
    const keyDialog = page.getByRole("dialog");
    await keyDialog.getByLabel("Key Label").fill("e2e-key");
    await keyDialog.getByRole("button", { name: /^issue api key$/i }).click();

    const secretEl = keyDialog.getByTestId("one-time-secret-value");
    await expect(secretEl).toBeVisible();
    const secretValue = (await secretEl.textContent()) ?? "";
    expect(secretValue).toMatch(/^gwf_[a-f0-9]+\.[a-f0-9]+$/);

    // Dismiss wipes plaintext secret and localStorage never stores it
    await keyDialog.getByRole("button", { name: /i have stored this key securely/i }).click();
    await expect(keyDialog.getByTestId("one-time-secret-value")).not.toBeVisible();
    const leaked = await page.evaluate((s) => JSON.stringify(window.localStorage).includes(s), secretValue);
    expect(leaked).toBe(false);
    await page.keyboard.press("Escape");

    // 6. Create Rate-Limit Policy & attach to Organization
    await page.getByRole("link", { name: /^policies$/i }).click();
    await expect(page).toHaveURL(/\/policies$/);
    await page.getByRole("button", { name: /new policy/i }).click();
    const polDialog = page.getByRole("dialog");
    await polDialog.getByLabel("Policy Name").fill(policyName);
    await polDialog.getByLabel("Policy Type").selectOption("rate_limit");
    await polDialog.getByRole("button", { name: /^create policy$/i }).click();
    await expect(page.getByText(policyName)).toBeVisible();

    // 7. Verify all created resources directly via authenticated Control Plane Admin API
    const meRes = await page.request.get("/api/v1/auth/me");
    expect(meRes.ok()).toBe(true);
    const me = (await meRes.json()) as { memberships: Array<{ org_id: string }> };
    const orgId = me.memberships[0]?.org_id;
    expect(orgId).toBeTruthy();

    const [svcsRes, poolsRes, routesRes, consRes, polsRes] = await Promise.all([
      page.request.get(`/api/v1/orgs/${orgId}/services?limit=100`),
      page.request.get(`/api/v1/orgs/${orgId}/upstreams?limit=100`),
      page.request.get(`/api/v1/orgs/${orgId}/routes?limit=100`),
      page.request.get(`/api/v1/orgs/${orgId}/consumers?limit=100`),
      page.request.get(`/api/v1/orgs/${orgId}/policies?limit=100`),
    ]);

    expect(((await svcsRes.json()) as { items: Array<{ slug: string }> }).items.some((s) => s.slug === svcSlug)).toBe(true);
    expect(((await poolsRes.json()) as { items: Array<{ name: string }> }).items.some((p) => p.name === poolName)).toBe(true);
    expect(((await routesRes.json()) as { items: Array<{ name: string }> }).items.some((r) => r.name === routeName)).toBe(true);
    expect(((await consRes.json()) as { items: Array<{ name: string }> }).items.some((c) => c.name === consumerName)).toBe(true);
    expect(((await polsRes.json()) as { items: Array<{ name: string }> }).items.some((p) => p.name === policyName)).toBe(true);

    expect(pageErrors).toEqual([]);
  });
});
