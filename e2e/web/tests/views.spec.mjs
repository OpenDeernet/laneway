import { test, expect } from '../fixtures.mjs';

// Every view draws from the demo with no error in the page and no request
// the demo can't answer (fixtures.mjs checks both).
const views = {
  '/home': 'My work',
  '/planning': 'Backlog',
  '/reports': 'Velocity',
  '/roadmap': 'Guest checkout',
  '/work': 'DEMO-',
  '/inbox': 'DEMO-',
  '/standup': 'Jamie Rivers',
  '/mrs': '!',
  '/rules': 'Rules',
  '/settings': 'Appearance',
  '/issue/DEMO-4': 'Checkout without an account',
};

for (const [route, text] of Object.entries(views)) {
  test(`${route} draws`, async ({ page, app }) => {
    await page.goto(new URL('#' + route, app.url).href);
    await expect(page.locator('main, #app, body').first()).toContainText(text);
  });
}

// With Jira slow, a view draws in place: no part of it pushed aside as its
// data comes in (CLS within the web's "good").
test.describe('a slow Jira', () => {
  test.use({ cls: 0.1 });
  for (const [route, text] of Object.entries(views)) {
    test(`${route} draws in place`, async ({ page, app }) => {
      await page.route(/\/api\//, async r => { await new Promise(x => setTimeout(x, 300)); await r.continue().catch(() => {}); });
      await page.goto(new URL('#' + route, app.url).href);
      await expect(page.locator('main, #app, body').first()).toContainText(text);
      await page.waitForTimeout(1000); // what loads after
    });
  }
});

// A view left while it still loads stays gone: Planning resolving its board
// after the board took its place draws nothing over it.
test('a view left mid-load draws nothing', async ({ page, app }) => {
  let release, held, first = true;
  const seen = new Promise(r => { held = r; });
  const gate = new Promise(r => { release = r; });
  await page.route('**/api/projects/*/boards', async r => {
    if (!first) return r.continue();
    first = false;
    held();
    await gate;
    await r.continue().catch(() => {});
  });
  await page.goto(new URL('/settings', app.url).href);
  await expect(page.locator('#view')).toContainText('Appearance');
  const nav = page.getByRole('navigation', { name: 'Views' });
  await nav.getByRole('link', { name: 'Planning' }).click();
  await seen;
  await nav.getByRole('link', { name: 'Board' }).click();
  release();
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
  await page.waitForTimeout(500);
  await expect(page.locator('#view .pl')).toHaveCount(0);
  const crumbs = await page.locator('#context .crumb').count();
  await page.reload();
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
  await expect(page.locator('#context .crumb')).toHaveCount(crumbs);
});
