const { test, expect } = require("@playwright/test");
const { authenticator } = require("otplib");

const ADMIN_USERNAME = "e2e-admin";
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || "OpenBidE2E!2026";
const SEEDED_TENANT = "OpenBid E2E Tenant";

async function login(page, overrides = {}) {
  await page.goto("/login");
  await page.getByLabel("Username").fill(overrides.username || ADMIN_USERNAME);
  await page.getByLabel("Password").fill(overrides.password || ADMIN_PASSWORD);
  if (overrides.mfaCode) {
    await page.getByLabel("MFA or recovery code").fill(overrides.mfaCode);
  }
  await Promise.all([
    page.waitForURL(/\/$/, { waitUntil: "domcontentloaded" }),
    page.getByRole("button", { name: "Sign in to OpenBid" }).click(),
  ]);
}

async function expectHome(page) {
  await expect(page).toHaveURL(/\/$/);
  await expect(
    page.getByRole("heading", {
      name: "One home for daily bidding work and operational visibility",
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Browse tenders" }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: /Smart Keyword Extraction/ }),
  ).toBeVisible();
}

test.describe.serial("OpenBid critical browser journeys", () => {
  test("login and logout flow works end to end", async ({ page }) => {
    await login(page);
    await expectHome(page);
    await page.goto("/logout");
    await expect(
      page.getByRole("heading", { name: "Welcome back" }),
    ).toBeVisible();
  });

  test("admin can switch tenants, manage sources, and retry queue jobs", async ({
    page,
  }) => {
    await login(page);
    await page.goto("/admin/tenants");
    await expect(
      page.getByRole("heading", { name: "Tenant administration" }),
    ).toBeVisible();
    const switchForm = page.locator('form[action="/tenant/switch"]');
    const tenantValue = await page
      .locator('select[name="tenant_id"] option')
      .filter({ hasText: SEEDED_TENANT })
      .first()
      .getAttribute("value");
    expect(tenantValue).toBeTruthy();
    await switchForm
      .locator('select[name="tenant_id"]')
      .selectOption(tenantValue);
    await switchForm.getByRole("button", { name: "Switch workspace" }).click();
    await expect(page).toHaveURL(/\/admin\/tenants$/);
    await expect(page.locator(".workspace-title")).toHaveText(SEEDED_TENANT);

    const sourceKey = `e2e-source-${Date.now()}`;
    await page.goto("/sources");
    await expect(
      page.getByRole("heading", {
        name: "Source checks, schedules, and sync health",
      }),
    ).toBeVisible();

    await page.getByLabel("Display name").fill(`E2E Source ${sourceKey}`);
    await page.getByLabel("Source key").fill(sourceKey);
    await page.getByLabel("Feed URL").fill("https://example.org/e2e-feed.json");
    await page.getByLabel("Source type").selectOption("json_feed");
    await page.getByRole("button", { name: "Add source" }).click();

    await expect(page.getByText("Source added")).toBeVisible();
    const operationsDisclosure = page.locator("details.sources-ops-disclosure");
    if (
      !(await operationsDisclosure.evaluate((node) =>
        node.hasAttribute("open"),
      ))
    ) {
      await operationsDisclosure.locator("summary").click();
    }
    const row = operationsDisclosure
      .locator("tr")
      .filter({ hasText: sourceKey });
    await expect(row).toBeVisible();
    await row.locator('form[action="/sources/check"] button').click();
    await expect(page.getByText("Source check queued")).toBeVisible();

    await page.goto("/queue");
    const failedSection = page.locator("details.queue-state-failed");
    if (!(await failedSection.evaluate((node) => node.hasAttribute("open")))) {
      await failedSection.locator("summary").click();
    }
    const queueRow = page
      .locator("tr")
      .filter({ hasText: "E2E Failed Queue Tender" });
    await expect(queueRow).toBeVisible();
    page.once("dialog", (dialog) => dialog.accept());
    await queueRow.getByRole("button", { name: "Retry" }).click();
    await expect(page.getByText("Job requeued")).toBeVisible();
    await expect(page.locator(".kpi-band")).toContainText("Queued");
  });

  test("Smart Keyword Extraction group tags, saved views, and alerts work", async ({
    page,
  }) => {
    await login(page);
    await page.goto("/smart-keywords");
    await expect(
      page.getByRole("heading", { name: "Smart Keyword Extraction" }),
    ).toBeVisible();

    const groupName = `E2E Smart ${Date.now()}`;
    const createGroupForm = page
      .locator('form[action="/smart-keywords/groups"]')
      .first();
    await createGroupForm.getByLabel("Group name").fill(groupName);
    await createGroupForm.getByLabel("Group Tag").fill(groupName);
    await createGroupForm.getByLabel("Match mode").selectOption("ANY");
    await createGroupForm.getByLabel("Enabled").check();
    await createGroupForm.getByLabel("Minimum matches").fill("1");
    await createGroupForm.getByLabel("Priority").fill("7");
    await createGroupForm.getByRole("button", { name: "Create group" }).click();
    await expect(page.getByText("Keyword group saved")).toBeVisible();
    await expect(page).toHaveURL(/\/smart-keywords\/groups\//);

    const addKeywordForm = page.locator('form[action="/smart-keywords/keywords"]').last();
    await addKeywordForm.getByLabel("Add keyword").fill("Queue");
    await addKeywordForm.getByRole("button", { name: "Add to group" }).click();
    await expect(page.getByText("Keyword saved")).toBeVisible();

    await page.goto("/smart-keywords");
    const settingsForm = page.locator('form[action="/smart-keywords/settings"]');
    await settingsForm.getByLabel("Enable smart extraction").check();
    await settingsForm.getByLabel("Global smart alerts").check();
    await settingsForm.getByRole("button", { name: "Save settings" }).click();
    await expect(
      page.getByText("Smart Keyword Extraction settings saved"),
    ).toBeVisible();

    page.once("dialog", (dialog) => dialog.accept());
    await page
      .locator('form[action="/smart-keywords/reprocess"]')
      .getByRole("button", { name: "Reprocess matches" })
      .click();
    await expect(
      page.locator('[role="status"]').getByText(/Reprocessed \d+ tenders/),
    ).toBeVisible();

    await page.goto(`/tenders?group_tag=${encodeURIComponent(groupName)}`);
    await expect(page.getByText("E2E Failed Queue Tender")).toBeVisible();
    await expect(
      page.locator("tr", { hasText: "E2E Failed Queue Tender" }).getByText(groupName),
    ).toBeVisible();

    await page.goto("/smart-keywords");
    const viewForm = page.locator('form[action="/smart-keywords/views"]').first();
    await viewForm.getByLabel("Name").fill(`${groupName} View`);
    await viewForm.getByLabel("Group Tags").fill(groupName);
    await viewForm.getByLabel("Enable alerts").check();
    await viewForm.getByLabel("Frequency").selectOption("immediate");
    await viewForm.getByLabel("Email").check();
    await viewForm
      .locator('input[name="email_destination"]')
      .fill("smart-alerts@example.org");
    await viewForm.getByRole("button", { name: "Save view" }).click();
    await expect(page.getByText("Saved Smart View saved")).toBeVisible();

    page.once("dialog", (dialog) => dialog.accept());
    await page
      .locator('form[action="/smart-keywords/reprocess"]')
      .getByRole("button", { name: "Reprocess matches" })
      .click();
    await expect(
      page.locator('[role="status"]').getByText(/Reprocessed \d+ tenders/),
    ).toBeVisible();
    await expect(page.getByText("smart-alerts@example.org").first()).toBeVisible();
    await expect(page.getByText("skipped").first()).toBeVisible();
  });

  test("mobile navigation drawer keeps nested sections open and routes links", async ({
    page,
  }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await login(page);

    const mobileMenu = page.locator("details.mobile-menu");
    await page.getByLabel("Open navigation menu").click();
    await expect(mobileMenu).toHaveAttribute("open", "");
    await page.locator("details.mobile-menu summary", { hasText: "Find Work" }).click();
    await expect(mobileMenu).toHaveAttribute("open", "");
    await expect(mobileMenu.getByRole("link", { name: "Smart Keyword Extraction", exact: true })).toBeVisible();
    await Promise.all([
      page.waitForURL(/\/smart-keywords$/),
      mobileMenu.getByRole("link", { name: "Smart Keyword Extraction", exact: true }).click(),
    ]);
    await expect(page.getByRole("heading", { name: "Smart Keyword Extraction" })).toBeVisible();

    await page.getByLabel("Open navigation menu").click();
    await page.locator("details.mobile-menu summary", { hasText: "Data Pipes" }).click();
    await Promise.all([
      page.waitForURL(/\/queue$/),
      mobileMenu.getByRole("link", { name: "Queue", exact: true }).click(),
    ]);
    await expect(page.getByRole("heading", { name: "Queue and extraction monitoring" })).toBeVisible();

    await page.getByLabel("Open navigation menu").click();
    await page.locator("details.mobile-menu summary", { hasText: "Settings" }).click();
    await expect(mobileMenu.getByRole("link", { name: "Email", exact: true })).toBeVisible();
    await Promise.all([
      page.waitForURL(/\/admin\/email$/),
      mobileMenu.getByRole("link", { name: "Email", exact: true }).click(),
    ]);
    await expect(page.getByRole("heading", { name: "Email settings" })).toBeVisible();
  });

  test("MFA setup and MFA login flow work", async ({ page }) => {
    await login(page);
    await page.goto("/settings/mfa");
    await page.getByRole("link", { name: "Set up MFA" }).click();
    await expect(
      page.getByRole("heading", { name: "Enable multi-factor authentication" }),
    ).toBeVisible();

    const secret = (
      (await page.locator(".card-soft .mono").first().textContent()) || ""
    ).trim();
    expect(secret).not.toBe("");
    const otp = authenticator.generate(secret);

    await page.getByLabel("Authenticator code").fill(otp);
    await page.getByRole("button", { name: "Enable MFA" }).click();
    await expect(
      page.getByText("MFA enabled. Save your recovery codes now."),
    ).toBeVisible();

    await page.goto("/logout");
    await login(page, { mfaCode: authenticator.generate(secret) });
    await expectHome(page);
  });
});
