import { defineConfig } from '@playwright/test';

// Each test gets its own `laneway web -demo` (fixtures.mjs): the demo keeps
// writes in memory, so tests can't see each other's.
export default defineConfig({
  testDir: 'tests',
  globalSetup: './build.mjs',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [...(process.env.CI ? [['github'], ['list']] : [['list']]), ['./vitals-reporter.mjs']],
  use: {
    viewport: { width: 1400, height: 900 },
    // The app's service worker would take requests past page.route, which many tests answer themselves.
    serviceWorkers: 'block',
    trace: 'retain-on-failure',
    launchOptions: process.env.CHROMIUM ? { executablePath: process.env.CHROMIUM } : {},
  },
});
