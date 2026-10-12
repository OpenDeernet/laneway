import { test, expect } from '../fixtures.mjs';

test('n creates an issue in the sprint shown', async ({ page, app }) => {
  await page.goto(app.url);
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
  await page.keyboard.press('n');
  await page.getByPlaceholder(/^Summary/).fill('Gift cards at checkout');
  await page.locator('[role="textbox"][aria-label^="Description"]').click();
  await page.keyboard.type('Pay part with a card.');
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  const added = page.locator('.bd-lanes .card', { hasText: 'Gift cards at checkout' });
  await expect(added).toBeVisible();
  await expect(page.locator('.bd-stats')).toContainText('11 issues');
});

test('an unsent issue is back after a reload', async ({ page, app }) => {
  await page.goto(app.url);
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
  await page.keyboard.press('n');
  await page.getByPlaceholder(/^Summary/).fill('Gift cards at checkout');
  await page.locator('[role="textbox"][aria-label^="Description"]').click();
  await page.keyboard.type('Pay part with a card.');
  await page.reload(); // at once: the page sends it as it goes
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
  await page.keyboard.press('n');
  await expect(page.getByPlaceholder(/^Summary/)).toHaveValue('Gift cards at checkout');
  await expect(page.locator('[role="textbox"][aria-label^="Description"]')).toContainText('Pay part with a card.');
});
