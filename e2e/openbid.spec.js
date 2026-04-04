const { test, expect } = require('@playwright/test');
const { authenticator } = require('otplib');

const ADMIN_USERNAME = 'e2e-admin';
const ADMIN_PASSWORD = process.env.E2E_ADMIN_PASSWORD || 'OpenBidE2E!2026';
const SEEDED_TENANT = 'OpenBid E2E Tenant';

async function login(page, overrides = {}) {
  await page.goto('/login');
  await page.getByLabel('Username').fill(overrides.username || ADMIN_USERNAME);
  await page.getByLabel('Password').fill(overrides.password || ADMIN_PASSWORD);
  if (overrides.mfaCode) {
    await page.getByLabel('MFA or recovery code').fill(overrides.mfaCode);
  }
  await page.getByRole('button', { name: 'Sign in to OpenBid' }).click();
}

async function expectHome(page) {
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('heading', { name: 'One home for daily bidding work and operational visibility' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Browse tenders' })).toBeVisible();
}

test.describe.serial('OpenBid critical browser journeys', () => {
  test('login and logout flow works end to end', async ({ page }) => {
    await login(page);
    await expectHome(page);
    await page.goto('/logout');
    await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
  });

  test('admin can switch tenants, manage sources, and retry queue jobs', async ({ page }) => {
    await login(page);
    await page.goto('/admin/tenants');
    await expect(page.getByRole('heading', { name: 'Tenant administration' })).toBeVisible();
    const switchForm = page.locator('form[action="/tenant/switch"]');
    const tenantValue = await page.locator('select[name="tenant_id"] option').filter({ hasText: SEEDED_TENANT }).first().getAttribute('value');
    expect(tenantValue).toBeTruthy();
    await switchForm.locator('select[name="tenant_id"]').selectOption(tenantValue);
    await switchForm.getByRole('button', { name: 'Switch workspace' }).click();
    await expect(page).toHaveURL(/\/admin\/tenants$/);
    await expect(page.locator('.workspace-title')).toHaveText(SEEDED_TENANT);

    const sourceKey = `e2e-source-${Date.now()}`;
    await page.goto('/sources');
    await expect(page.getByRole('heading', { name: 'Source checks, schedules, and sync health' })).toBeVisible();

    await page.getByLabel('Display name').fill(`E2E Source ${sourceKey}`);
    await page.getByLabel('Source key').fill(sourceKey);
    await page.getByLabel('Feed URL').fill('https://example.org/e2e-feed.json');
    await page.getByLabel('Source type').selectOption('json_feed');
    await page.getByRole('button', { name: 'Add source' }).click();

    await expect(page.getByText('Source added')).toBeVisible();
    const operationsDisclosure = page.locator('details.sources-ops-disclosure');
    if (!(await operationsDisclosure.evaluate((node) => node.hasAttribute('open')))) {
      await operationsDisclosure.locator('summary').click();
    }
    const row = operationsDisclosure.locator('tr').filter({ hasText: sourceKey });
    await expect(row).toBeVisible();
    await row.locator('form[action="/sources/check"] button').click();
    await expect(page.getByText('Source check queued')).toBeVisible();

    await page.goto('/queue');
    const failedSection = page.locator('details.queue-state-failed');
    if (!(await failedSection.evaluate((node) => node.hasAttribute('open')))) {
      await failedSection.locator('summary').click();
    }
    const queueRow = page.locator('tr').filter({ hasText: 'E2E Failed Queue Tender' });
    await expect(queueRow).toBeVisible();
    page.once('dialog', (dialog) => dialog.accept());
    await queueRow.getByRole('button', { name: 'Retry' }).click();
    await expect(page.getByText('Job requeued')).toBeVisible();
    await expect(page.locator('.kpi-band')).toContainText('Queued');
  });

  test('MFA setup and MFA login flow work', async ({ page }) => {
    await login(page);
    await page.goto('/settings/mfa');
    await page.getByRole('link', { name: 'Set up MFA' }).click();
    await expect(page.getByRole('heading', { name: 'Enable multi-factor authentication' })).toBeVisible();

    const secret = (await page.locator('.card-soft .mono').first().textContent() || '').trim();
    expect(secret).not.toBe('');
    const otp = authenticator.generate(secret);

    await page.getByLabel('Authenticator code').fill(otp);
    await page.getByRole('button', { name: 'Enable MFA' }).click();
    await expect(page.getByText('MFA enabled. Save your recovery codes now.')).toBeVisible();

    await page.goto('/logout');
    await login(page, { mfaCode: authenticator.generate(secret) });
    await expectHome(page);
  });
});
