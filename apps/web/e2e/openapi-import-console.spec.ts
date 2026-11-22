import { test, expect } from "@playwright/test";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@gatewayforge.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "GatewayForge!Admin2026";
const CONTROL_PLANE_URL = process.env.E2E_CONTROL_PLANE_URL || "http://127.0.0.1:18080";

test.describe("OpenAPI Import-to-Live-Traffic Golden Flow (AT-17)", () => {
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

  test("import OpenAPI spec -> review & commit draft -> publish -> invoke live route via Console", async ({
    page,
  }) => {
    const suffix = String(Date.now()).slice(-6);
    const poolName = `oa-mock-pool-${suffix}`;
    const hostName = `oa-${suffix}.gatewayforge.local`;
    const serviceTitle = `OpenAPI Echo ${suffix}`;

    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    // 1. Login
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/$/);

    // 2. Create mock upstream pool via Upstreams UI
    await page.getByRole("link", { name: /^upstreams$/i }).click();
    await expect(page).toHaveURL(/\/upstreams$/);
    await page.getByRole("button", { name: /create upstream pool/i }).first().click();
    const poolDlg = page.getByRole("dialog");
    await poolDlg.getByLabel("Pool Name").fill(poolName);
    await poolDlg.getByLabel("Destination Class").selectOption("mock_local");
    await poolDlg.getByLabel("Initial Target URL").fill("http://mock-upstream:8089");
    await poolDlg.getByRole("button", { name: /^create pool$/i }).click();
    await expect(page.getByText(poolName)).toBeVisible();

    // 3. Navigate to Routes -> Import OpenAPI 3.0.3 YAML spec
    await page.getByRole("link", { name: /^routes$/i }).click();
    await expect(page).toHaveURL(/\/routes$/);
    await page.getByRole("button", { name: /import openapi/i }).click();

    const importDlg = page.getByRole("dialog");
    const sampleSpec = [
      `openapi: "3.0.3"`,
      `info:`,
      `  title: "${serviceTitle}"`,
      `  version: "1.0.0"`,
      `servers:`,
      `  - url: "https://${hostName}"`,
      `paths:`,
      `  /echo:`,
      `    get:`,
      `      operationId: getEcho`,
    ].join("\n");

    await importDlg.getByLabel("OpenAPI Spec (YAML or JSON)").fill(sampleSpec);
    await importDlg.getByRole("button", { name: /preview import/i }).click();

    // Review preview and select target upstream pool
    await expect(importDlg.getByText(`Service: ${serviceTitle}`)).toBeVisible();
    await expect(importDlg.getByText(`${hostName}/echo`)).toBeVisible();
    await importDlg.getByLabel("Target Upstream Pool").selectOption({ label: poolName });
    await importDlg.getByRole("button", { name: /commit draft import/i }).click();

    // Imported route is now listed on /routes
    await expect(page.getByText(`${hostName}/echo`)).toBeVisible();

    // 4. Publish draft via authenticated Control Plane API so gateway replicas converge
    const meRes = await page.request.get("/api/v1/auth/me");
    const me = (await meRes.json()) as { csrf_token: string; memberships: Array<{ org_id: string }> };
    const orgId = me.memberships[0]?.org_id;
    expect(orgId).toBeTruthy();

    const pubRes = await page.request.post(`/api/v1/orgs/${orgId}/publications`, {
      headers: { "X-CSRF-Token": me.csrf_token },
      data: { notes: `Publish OpenAPI import ${suffix}` },
    });
    expect(pubRes.status()).toBe(201);

    // 5. Invoke the newly published imported route through the Request Console
    await page.getByRole("link", { name: /request console/i }).click();
    await expect(page).toHaveURL(/\/console$/);

    await page.getByLabel("Route Host").fill(hostName);
    await page.getByLabel("Path & Query").fill("/echo?source=openapi-e2e");

    // Wait briefly for gateway Redis hint / sync convergence then assert live HTTP 200
    await expect(async () => {
      await page.getByRole("button", { name: /send request/i }).click();
      await expect(page.getByText("HTTP 200")).toBeVisible({ timeout: 2500 });
    }).toPass({ timeout: 12_000, intervals: [1000, 1500, 2000] });

    const bodyText = await page.getByTestId("console-response-body").textContent();
    expect(bodyText).toContain("/echo");
    expect(bodyText).toContain("openapi-e2e");

    expect(pageErrors).toEqual([]);
  });
});
