import { test, expect } from "@playwright/test";

const ADMIN_EMAIL = process.env.E2E_ADMIN_EMAIL || "admin@gatewayforge.local";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "GatewayForge!Admin2026";
const CONTROL_PLANE_URL = process.env.E2E_CONTROL_PLANE_URL || "http://127.0.0.1:18080";

const AUTHENTICATED_ROUTES = [
  "/",
  "/services",
  "/upstreams",
  "/routes",
  "/consumers",
  "/policies",
  "/releases",
  "/console",
  "/insights",
  "/settings",
];

test.describe("Final UI Quality, Accessibility, and Responsive Review", () => {
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

  test("login error state, keyboard focus, and responsive integrity", async ({ page }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/login");

    // Verify invalid login displays accessible error feedback without crashing
    await page.getByLabel("Email").fill("wrong@gatewayforge.local");
    await page.getByLabel("Password").fill("WrongPassword!123");
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page.getByRole("alert")).toBeVisible();

    // Check zero horizontal overflow on mobile login
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - window.innerWidth,
    );
    expect(overflow).toBeLessThanOrEqual(2);

    // Keyboard tab reachability on login controls
    await page.getByLabel("Email").focus();
    await expect(page.getByLabel("Email")).toBeFocused();
    await page.keyboard.press("Tab");
    await expect(page.getByLabel("Password")).toBeFocused();

    expect(pageErrors).toEqual([]);
  });

  test("all authenticated Studio routes pass desktop/mobile responsive and axe-compatible a11y checks", async ({
    page,
  }) => {
    const pageErrors: string[] = [];
    page.on("pageerror", (err) => pageErrors.push(err.message));

    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: /^sign in$/i }).click();
    await expect(page).toHaveURL(/\/$/);

    const viewports = [
      { label: "desktop", width: 1440, height: 900 },
      { label: "mobile", width: 390, height: 844 },
    ];

    for (const vp of viewports) {
      await page.setViewportSize({ width: vp.width, height: vp.height });

      for (const route of AUTHENTICATED_ROUTES) {
        await page.goto(route);
        await expect(page.locator("main")).toBeVisible();

        // Wait briefly for route data fetch to settle
        await page.waitForLoadState("networkidle").catch(() => {});

        const audit = await page.evaluate(() => {
          const h1Count = document.querySelectorAll("h1").length;
          const overflowPx = document.documentElement.scrollWidth - window.innerWidth;
          const culprits: string[] = [];
          if (overflowPx > 2) {
            for (const el of Array.from(document.querySelectorAll("*"))) {
              const r = el.getBoundingClientRect();
              if (r.right > window.innerWidth + 2) {
                culprits.push(`${el.tagName.toLowerCase()}.${String(el.className).slice(0, 60)} (right=${Math.round(r.right)})`);
                if (culprits.length >= 5) break;
              }
            }
          }

          const unlabeledButtons: string[] = [];
          for (const btn of Array.from(document.querySelectorAll("button"))) {
            const rect = btn.getBoundingClientRect();
            if (rect.width === 0 || rect.height === 0) continue;
            const text = (btn.textContent || "").trim();
            const aria = (btn.getAttribute("aria-label") || "").trim();
            const title = (btn.getAttribute("title") || "").trim();
            if (!text && !aria && !title) {
              unlabeledButtons.push(btn.outerHTML.slice(0, 120));
            }
          }

          const unlabeledInputs: string[] = [];
          for (const el of Array.from(document.querySelectorAll("input, select, textarea"))) {
            const rect = el.getBoundingClientRect();
            if (rect.width === 0 || rect.height === 0) continue;
            const id = el.getAttribute("id");
            const hasForLabel = id ? Boolean(document.querySelector(`label[for="${CSS.escape(id)}"]`)) : false;
            const insideLabel = Boolean(el.closest("label"));
            const aria = (el.getAttribute("aria-label") || el.getAttribute("aria-labelledby") || "").trim();
            if (!hasForLabel && !insideLabel && !aria) {
              unlabeledInputs.push(el.outerHTML.slice(0, 120));
            }
          }

          const missingAltImages = Array.from(document.querySelectorAll("img"))
            .filter((img) => img.getAttribute("alt") === null)
            .map((img) => img.outerHTML.slice(0, 120));

          return { h1Count, overflowPx, culprits, unlabeledButtons, unlabeledInputs, missingAltImages };
        });

        expect(audit.h1Count, `${vp.label} ${route}: expected >= 1 <h1>`).toBeGreaterThanOrEqual(1);
        expect(
          audit.overflowPx,
          `${vp.label} ${route}: horizontal overflow ${audit.overflowPx}px culprits=${JSON.stringify(audit.culprits)}`,
        ).toBeLessThanOrEqual(2);
        expect(
          audit.unlabeledButtons,
          `${vp.label} ${route}: unlabeled buttons`,
        ).toEqual([]);
        expect(
          audit.unlabeledInputs,
          `${vp.label} ${route}: unlabeled form controls`,
        ).toEqual([]);
        expect(
          audit.missingAltImages,
          `${vp.label} ${route}: images missing alt`,
        ).toEqual([]);
      }
    }

    expect(pageErrors).toEqual([]);
  });
});
