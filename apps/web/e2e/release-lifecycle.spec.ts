import { test, expect } from "@playwright/test";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@gatewayforge.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "GatewayForge!Admin2026";
const CONTROL_PLANE_URL = process.env.E2E_CONTROL_PLANE_URL || "http://127.0.0.1:18080";
const GATEWAY_1_URL = process.env.E2E_GATEWAY_1_URL || "http://127.0.0.1:18081";
const GATEWAY_2_URL = process.env.E2E_GATEWAY_2_URL || "http://127.0.0.1:18082";

test.describe("Full Release Lifecycle & 2-Replica Rollback Convergence (AT-04, AT-15, AT-16)", () => {
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

  test("draft -> validate -> publish v1 -> 2 replicas serve 200 -> break & publish v2 -> rollback to v1 -> audit & 2-replica convergence", async ({
    page,
  }) => {
    const suffix = String(Date.now()).slice(-6);
    const hostName = `lc-${suffix}.gatewayforge.local`;

    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    // 1. Login
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/$/);

    // 2. Seed a fresh draft service, mock upstream pool, and route via authenticated session
    const meRes = await page.request.get("/api/v1/auth/me");
    const me = (await meRes.json()) as { csrf_token: string; memberships: Array<{ org_id: string }> };
    const orgId = me.memberships[0]?.org_id;
    expect(orgId).toBeTruthy();
    const csrf = { "X-CSRF-Token": me.csrf_token };

    const svcRes = await page.request.post(`/api/v1/orgs/${orgId}/services`, {
      headers: csrf,
      data: { slug: `lc-svc-${suffix}`, name: `Lifecycle Svc ${suffix}`, description: "" },
    });
    expect(svcRes.status()).toBe(201);
    const svc = (await svcRes.json()) as { id: string };

    const poolRes = await page.request.post(`/api/v1/orgs/${orgId}/upstreams`, {
      headers: csrf,
      data: {
        name: `lc-pool-${suffix}`,
        destination_class: "mock_local",
        targets: [{ url: "http://mock-upstream:8089", weight: 100 }],
      },
    });
    expect(poolRes.status()).toBe(201);
    const pool = (await poolRes.json()) as { id: string };

    const rtRes = await page.request.post(`/api/v1/orgs/${orgId}/routes`, {
      headers: csrf,
      data: {
        service_id: svc.id,
        upstream_pool_id: pool.id,
        name: `lc-route-${suffix}`,
        host: hostName,
        path_prefix: "/echo",
        methods: ["GET"],
        priority: 150,
      },
    });
    expect(rtRes.status()).toBe(201);
    const rt = (await rtRes.json()) as { id: string; revision: number };

    // 3. Visit /releases -> review diff -> publish working release through UI
    await page.getByRole("link", { name: /^releases$/i }).click();
    await expect(page).toHaveURL(/\/releases$/);
    await expect(page.getByText("Ready to Publish")).toBeVisible();

    await page.getByRole("button", { name: /review & publish/i }).click();
    const pubDlg1 = page.getByRole("dialog");
    await pubDlg1.getByLabel("Release Notes").fill(`Working release ${suffix}`);
    await pubDlg1.getByLabel(/i have reviewed/i).click();
    await pubDlg1.getByRole("button", { name: /confirm publish/i }).click();
    await expect(pubDlg1).not.toBeVisible();

    // Determine version just published
    const pubsAfter1 = (await (await page.request.get(`/api/v1/orgs/${orgId}/publications`)).json()) as {
      items: Array<{ version: number; is_active: boolean }>;
    };
    const goodVersion = pubsAfter1.items.find((p) => p.is_active)?.version;
    expect(goodVersion).toBeTruthy();

    // 4. Verify BOTH gateway-1 and gateway-2 converge and serve HTTP 200
    await expect(async () => {
      const [g1, g2] = await Promise.all([
        page.request.get(`${GATEWAY_1_URL}/echo?replica=1`, { headers: { Host: hostName } }),
        page.request.get(`${GATEWAY_2_URL}/echo?replica=2`, { headers: { Host: hostName } }),
      ]);
      expect(g1.status()).toBe(200);
      expect(g2.status()).toBe(200);
    }).toPass({ timeout: 15_000, intervals: [500, 1000, 1500] });

    // 5. Break route in draft (disable route) and publish a broken release via UI
    const patchRes = await page.request.patch(`/api/v1/orgs/${orgId}/routes/${rt.id}`, {
      headers: { ...csrf, "If-Match": `W/"rev-${rt.revision}"` },
      data: { enabled: false, revision: rt.revision },
    });
    expect(patchRes.status()).toBe(200);

    await page.getByRole("button", { name: /re-validate/i }).click();
    await expect(page.getByText("Ready to Publish")).toBeVisible();
    await page.getByRole("button", { name: /review & publish/i }).click();
    const pubDlg2 = page.getByRole("dialog");
    await pubDlg2.getByLabel("Release Notes").fill(`Broken release ${suffix}`);
    await pubDlg2.getByLabel(/i have reviewed/i).click();
    await pubDlg2.getByRole("button", { name: /confirm publish/i }).click();
    await expect(pubDlg2).not.toBeVisible();

    // Both replicas now return 404 because route was disabled
    await expect(async () => {
      const [g1, g2] = await Promise.all([
        page.request.get(`${GATEWAY_1_URL}/echo`, { headers: { Host: hostName } }),
        page.request.get(`${GATEWAY_2_URL}/echo`, { headers: { Host: hostName } }),
      ]);
      expect(g1.status()).toBe(404);
      expect(g2.status()).toBe(404);
    }).toPass({ timeout: 15_000, intervals: [500, 1000, 1500] });

    // 6. Execute Rollback to goodVersion directly from Release History UI
    await page.getByRole("button", { name: `Rollback to v${goodVersion}` }).click();
    const rbDlg = page.getByRole("dialog");
    await rbDlg.getByRole("button", { name: `Confirm Rollback to v${goodVersion}` }).click();
    await expect(rbDlg).not.toBeVisible();

    // 7. Observe BOTH replicas converge back to HTTP 200!
    await expect(async () => {
      const [g1, g2] = await Promise.all([
        page.request.get(`${GATEWAY_1_URL}/echo?after=rollback`, { headers: { Host: hostName } }),
        page.request.get(`${GATEWAY_2_URL}/echo?after=rollback`, { headers: { Host: hostName } }),
      ]);
      expect(g1.status()).toBe(200);
      expect(g2.status()).toBe(200);
    }).toPass({ timeout: 15_000, intervals: [500, 1000, 1500] });

    // 8. Verify publication.rollback appears in Insights & Audit Explorer
    await page.getByRole("link", { name: /^insights$/i }).click();
    await expect(page).toHaveURL(/\/insights$/);
    await expect(page.getByText("publication.rollback").first()).toBeVisible();

    expect(pageErrors).toEqual([]);
  });
});
