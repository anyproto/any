import { defineConfig, devices } from '@playwright/test';

/**
 * Playwright config.
 *
 * E2E spec boots the SPA via `pnpm preview` (against the production
 * Vite build) and assumes a real `any` server is running at
 * 127.0.0.1:7001. CI starts that server in a separate step before
 * invoking `playwright test`. Locally:
 *
 *   ./any run                 # terminal 1
 *   pnpm build && pnpm test:e2e   # terminal 2
 */
export default defineConfig({
  testDir: '../../e2e',
  fullyParallel: true,
  forbidOnly: !!process.env['CI'],
  retries: process.env['CI'] ? 2 : 0,
  workers: process.env['CI'] ? 1 : undefined,
  reporter: process.env['CI'] ? 'github' : 'list',
  use: {
    baseURL: 'http://127.0.0.1:4173',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: 'pnpm preview --port 4173 --host 127.0.0.1',
    url: 'http://127.0.0.1:4173',
    reuseExistingServer: !process.env['CI'],
    stdout: 'ignore',
    stderr: 'pipe',
    timeout: 30_000,
  },
});
