import { test, expect } from '../fixtures.mjs';

// Typing that missed its field (lib/keys.js): the shortcuts it set off are taken back, the text offered to the field.
test.use({ typing: true });

test.beforeEach(async ({ page, app }) => {
  await page.goto(app.url);
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
});

test('"later" on the board opens no assignee picker', async ({ page }) => {
  const url = page.url();
  await page.keyboard.type('later');
  await expect(page.getByText('Typing outside a field: shortcuts paused')).toBeVisible();
  await page.waitForTimeout(1600); // past the time a late picker is still closed in
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(page.url()).toBe(url);
});

test('typed beside the filter, the toast types it in', async ({ page }) => {
  const filter = page.locator('.bd-filter');
  await filter.click();
  await page.locator('.bd-lanes').click({ position: { x: 5, y: 5 } });
  await page.keyboard.type('hello you');
  await page.getByRole('button', { name: /Type it in Filter/ }).click();
  await expect(filter).toHaveValue('hello you');
  await expect(filter).toBeFocused();
});

test('a quick hand is no typing: j j l', async ({ page }) => {
  const sel = page.locator('.bd-lanes .card.sel .ckey');
  await page.keyboard.press('j');
  await page.keyboard.press('j');
  await page.keyboard.press('l');
  await expect(sel).toHaveText('DEMO-6');
  await expect(page.getByText('Typing outside a field')).toHaveCount(0);
});

test('view keys wait for no one; a burst takes them back: "tea"', async ({ page }) => {
  await page.keyboard.press('t');
  await expect(page.locator('.bd-lanes')).toHaveCount(0);
  await page.keyboard.press('t');
  await expect(page.locator('.bd-lanes')).toBeVisible();
  await page.waitForTimeout(1200);
  await page.keyboard.type('tea');
  await expect(page.getByText('Typing outside a field: shortcuts paused')).toBeVisible();
  await expect(page.locator('.bd-lanes')).toBeVisible();
});
