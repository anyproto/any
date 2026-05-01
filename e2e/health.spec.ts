import { test, expect } from '@playwright/test';

/**
 * E2E: SPA loads, /v1/health is queried, the OK card renders, and the
 * theme toggle works in both modes. Assumes a live `any` server at
 * 127.0.0.1:7001 — CI spins one up before this runs.
 */

test.describe('Health page', () => {
  test('renders the OK card with the live account id', async ({ page }) => {
    await page.goto('/');
    // Wait for /v1/health to resolve (Tanstack Query hydrates).
    await expect(page.getByText(/Server: ok/)).toBeVisible({ timeout: 5_000 });
    // The account id is base58, length 49 in the staging fixture.
    const account = page.locator('dd code').first();
    await expect(account).toBeVisible();
    const text = (await account.textContent())?.trim() ?? '';
    expect(text.length).toBeGreaterThanOrEqual(40);
  });

  test('theme toggle persists across reload', async ({ page }) => {
    await page.goto('/');
    // Default is `system` — pick `dark` explicitly.
    await page.getByRole('radio', { name: 'Dark' }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

    await page.reload();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');

    // Switch back to light.
    await page.getByRole('radio', { name: 'Light' }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  });
});
