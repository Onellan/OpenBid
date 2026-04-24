const { test, expect } = require("@playwright/test");

const ADMIN_USERNAME =
  process.env.E2E_ADMIN_USERNAME ||
  (process.env.E2E_ADMIN_PASSWORD ? "e2e-admin" : "admin");
const ADMIN_PASSWORD =
  process.env.E2E_ADMIN_PASSWORD || "OpenBid!2026-YK4j3z39CEfu0kbFHcEzM8yI";

async function loginAs(page) {
  await page.goto("/login");
  await page.getByLabel("Username").fill(ADMIN_USERNAME);
  await page.getByLabel("Password").fill(ADMIN_PASSWORD);
  await Promise.all([
    page.waitForURL(/\/$/, { waitUntil: "domcontentloaded" }),
    page.getByRole("button", { name: "Sign in to OpenBid" }).click(),
  ]);
}

// The hamburger is identified by its aria-label so we avoid strict-mode
// violations from nested <summary> elements inside the panel.
function hamburger(page) {
  return page.getByLabel("Open navigation menu");
}

test.describe("Mobile navigation (390×844)", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("desktop nav is hidden and mobile menu button is visible", async ({
    page,
  }) => {
    await loginAs(page);
    await expect(page.locator(".desktop-nav")).not.toBeVisible();
    await expect(hamburger(page)).toBeVisible();
  });

  test("mobile menu opens, shows sections, and closes", async ({ page }) => {
    await loginAs(page);
    const menu = page.locator("details.mobile-menu");
    await hamburger(page).click();
    await expect(menu).toHaveAttribute("open", "");
    await expect(menu.locator(".mobile-menu-panel")).toBeVisible();
    await hamburger(page).click();
    await expect(menu).not.toHaveAttribute("open", "");
  });

  test("Find Work section navigates to tenders", async ({ page }) => {
    await loginAs(page);
    await hamburger(page).click();
    await page
      .locator(".mobile-menu-section details")
      .filter({ hasText: "Find Work" })
      .locator("summary")
      .click();
    await page.locator('.mobile-menu-links a[href="/tenders"]').click();
    await expect(page).toHaveURL(/\/tenders/);
  });

  test("Find Work section has all expected links", async ({ page }) => {
    await loginAs(page);
    await hamburger(page).click();
    const findWork = page
      .locator(".mobile-menu-section details")
      .filter({ hasText: "Find Work" });
    await findWork.locator("summary").click();
    await expect(findWork.locator('a[href="/tenders"]')).toBeVisible();
    await expect(findWork.locator('a[href="/keyword-search"]')).toBeVisible();
    await expect(findWork.locator('a[href="/smart-keywords"]')).toBeVisible();
    await expect(findWork.locator('a[href="/bookmarks"]')).toBeVisible();
    await expect(findWork.locator('a[href="/saved-searches"]')).toBeVisible();
  });

  test("Settings section shows admin-only links for admin user", async ({
    page,
  }) => {
    await loginAs(page);
    await hamburger(page).click();
    const settings = page
      .locator(".mobile-menu-section details")
      .filter({ hasText: "Settings" });
    await settings.locator("summary").click();
    await expect(settings.locator('a[href="/settings"]')).toBeVisible();
    await expect(settings.locator('a[href="/admin/users"]')).toBeVisible();
    await expect(settings.locator('a[href="/audit-log"]')).toBeVisible();
    await expect(
      settings.locator('a[href="/audit-log/security"]'),
    ).toBeVisible();
    await expect(settings.locator('a[href="/admin/email"]')).toBeVisible();
    await expect(settings.locator('a[href="/logout"]')).toBeVisible();
  });

  test("Settings section navigates to audit log", async ({ page }) => {
    await loginAs(page);
    await hamburger(page).click();
    const settings = page
      .locator(".mobile-menu-section details")
      .filter({ hasText: "Settings" });
    await settings.locator("summary").click();
    await settings.locator('a[href="/audit-log"]').click();
    await expect(page).toHaveURL(/\/audit-log/);
  });

  test("clicking outside menu closes it", async ({ page }) => {
    await loginAs(page);
    const menu = page.locator("details.mobile-menu");
    await hamburger(page).click();
    await expect(menu).toHaveAttribute("open", "");
    await page.locator("main").click({ force: true });
    await expect(menu).not.toHaveAttribute("open", "");
  });
});

test.describe("Desktop navigation (1280×800)", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("desktop nav is visible and mobile menu is hidden", async ({ page }) => {
    await loginAs(page);
    await expect(page.locator(".desktop-nav")).toBeVisible();
    await expect(page.locator("details.mobile-menu")).not.toBeVisible();
  });

  test("Settings dropdown opens and shows all sections", async ({ page }) => {
    await loginAs(page);
    const settingsCascade = page
      .locator(".nav-cascade")
      .filter({ hasText: "Settings" });
    await settingsCascade.locator("summary").click();
    await expect(settingsCascade.locator(".nav-cascade-panel")).toBeVisible();
    // Account section
    await expect(
      settingsCascade.locator('a[href="/settings"]'),
    ).toBeVisible();
    await expect(
      settingsCascade.locator('a[href="/logout"]'),
    ).toBeVisible();
    // Platform section
    await expect(
      settingsCascade.locator('a[href="/audit-log"]'),
    ).toBeVisible();
    await expect(
      settingsCascade.locator('a[href="/audit-log/security"]'),
    ).toBeVisible();
    await expect(
      settingsCascade.locator('a[href="/admin/email"]'),
    ).toBeVisible();
  });

  test("Settings dropdown navigate to audit log", async ({ page }) => {
    await loginAs(page);
    const settingsCascade = page
      .locator(".nav-cascade")
      .filter({ hasText: "Settings" });
    await settingsCascade.locator("summary").click();
    await settingsCascade.locator('a[href="/audit-log"]').click();
    await expect(page).toHaveURL(/\/audit-log/);
  });

  test("Settings dropdown navigate to security audit", async ({ page }) => {
    await loginAs(page);
    const settingsCascade = page
      .locator(".nav-cascade")
      .filter({ hasText: "Settings" });
    await settingsCascade.locator("summary").click();
    await settingsCascade.locator('a[href="/audit-log/security"]').click();
    await expect(page).toHaveURL(/\/audit-log\/security/);
  });

  test("Find Work dropdown opens and shows all links", async ({ page }) => {
    await loginAs(page);
    const findWork = page
      .locator(".nav-cascade")
      .filter({ hasText: "Find Work" });
    await findWork.locator("summary").click();
    await expect(findWork.locator('a[href="/tenders"]')).toBeVisible();
    await expect(findWork.locator('a[href="/keyword-search"]')).toBeVisible();
    await expect(findWork.locator('a[href="/smart-keywords"]')).toBeVisible();
    await expect(findWork.locator('a[href="/bookmarks"]')).toBeVisible();
    await expect(findWork.locator('a[href="/saved-searches"]')).toBeVisible();
  });

  test("only one desktop dropdown open at a time", async ({ page }) => {
    await loginAs(page);
    const findWork = page
      .locator(".nav-cascade")
      .filter({ hasText: "Find Work" });
    const settings = page
      .locator(".nav-cascade")
      .filter({ hasText: "Settings" });
    await findWork.locator("summary").click();
    await expect(findWork).toHaveAttribute("open", "");
    await settings.locator("summary").click();
    await expect(settings).toHaveAttribute("open", "");
    await expect(findWork).not.toHaveAttribute("open", "");
  });
});

test.describe("Responsive layout — mobile (390×844)", () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test("home page renders kpi band", async ({ page }) => {
    await loginAs(page);
    await expect(page.locator(".kpi-band")).toBeVisible();
    await expect(page.locator(".kpi-band .metric-card").first()).toBeVisible();
  });

  test("tenders page loads and shows content", async ({ page }) => {
    await loginAs(page);
    await page.goto("/tenders");
    await expect(
      page.locator("h1.page-title"),
    ).toBeVisible();
  });

  test("settings page shows feature grid", async ({ page }) => {
    await loginAs(page);
    await page.goto("/settings");
    await expect(page.locator(".feature-grid").first()).toBeVisible();
    await expect(page.locator('a[href="/settings/password"]')).toBeVisible();
    await expect(page.locator('a[href="/settings/mfa"]')).toBeVisible();
  });
});

test.describe("Responsive layout — desktop (1280×800)", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("settings page shows platform admin links", async ({ page }) => {
    await loginAs(page);
    await page.goto("/settings");
    // Check the feature-link cards on the settings page specifically
    await expect(page.locator('section.feature-grid a[href="/audit-log"]')).toBeVisible();
    await expect(page.locator('section.feature-grid a[href="/audit-log/security"]')).toBeVisible();
    await expect(page.locator('section.feature-grid a[href="/admin/email"]')).toBeVisible();
  });
});

test.describe("Responsive layout — tablet (768×1024)", () => {
  test.use({ viewport: { width: 768, height: 1024 } });

  test("mobile menu shown at tablet width", async ({ page }) => {
    await loginAs(page);
    await expect(hamburger(page)).toBeVisible();
    await expect(page.locator(".desktop-nav")).not.toBeVisible();
  });
});
