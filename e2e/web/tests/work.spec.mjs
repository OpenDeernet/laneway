import { test, expect, pick } from '../fixtures.mjs';

test('a logs work on the day, the day sums it', async ({ page, app }) => {
  await page.goto(new URL('#/work?tab=day', app.url).href);
  await expect(page.locator('#view')).toContainText('0m of 8h');
  await page.keyboard.press('a');
  await pick(page, 'DEMO-4', /DEMO-4\b/);
  const dialog = page.getByRole('dialog', { name: 'Log work on DEMO-4' });
  await dialog.getByRole('textbox', { name: 'Time' }).fill('2h');
  await dialog.getByRole('button', { name: 'Log work' }).click();
  await expect(page.locator('#view')).toContainText('2h of 8h');
  await expect(page.locator('#view')).toContainText('DEMO-4');
});

test('d hides the done issues', async ({ page, app }) => {
  await page.goto(new URL('#/work', app.url).href);
  const key = k => page.locator('#view').getByText(k, { exact: true });
  await expect(key('DEMO-21')).toBeVisible();
  await page.keyboard.press('d');
  await expect(key('DEMO-21')).toBeHidden();
  await expect(key('DEMO-4')).toBeVisible();
});

// logToday logs 30m on each key from the day's a.
async function logToday(page, keys) {
  for (const [i, key] of keys.entries()) {
    await page.keyboard.press('a');
    await pick(page, key, new RegExp(key + '\\b'));
    const dialog = page.getByRole('dialog', { name: 'Log work on ' + key });
    await dialog.getByRole('textbox', { name: 'Time' }).fill('30m');
    await dialog.getByRole('button', { name: 'Log work' }).click();
    await expect(page.locator('.wlog')).toHaveCount(i + 1);
  }
}

test('x marks worklogs, m moves them to another issue', async ({ page, app }) => {
  await page.goto(new URL('#/work?tab=day', app.url).href);
  await expect(page.locator('#view')).toContainText('0m of 8h');
  await logToday(page, ['DEMO-4', 'DEMO-5']);
  await page.keyboard.press('x');
  await page.keyboard.press('x');
  const bar = page.getByRole('toolbar', { name: 'Selected worklogs' });
  await expect(bar).toContainText('2 selected');
  await page.keyboard.press('m');
  await pick(page, 'DEMO-6', /DEMO-6\b/);
  await expect(page.locator('.wlog', { hasText: 'DEMO-6' })).toHaveCount(2);
  await expect(page.locator('.wlog', { hasText: 'DEMO-4' })).toHaveCount(0);
  await expect(page.locator('#view')).toContainText('1h of 8h');
  await expect(bar).toBeHidden();
});

test('X marks the day, d deletes the marked worklogs', async ({ page, app }) => {
  await page.goto(new URL('#/work?tab=day', app.url).href);
  await expect(page.locator('#view')).toContainText('0m of 8h');
  await logToday(page, ['DEMO-4', 'DEMO-5', 'DEMO-4']);
  await page.keyboard.press('X');
  await expect(page.getByRole('toolbar', { name: 'Selected worklogs' })).toContainText('3 selected');
  await page.keyboard.press('d');
  const dialog = page.getByRole('dialog', { name: 'Delete worklogs' });
  await expect(dialog).toContainText('3 worklogs, 1h 30m');
  await dialog.getByRole('button', { name: 'Delete' }).click();
  await expect(page.locator('#view')).toContainText('0m of 8h');
  await expect(page.locator('.wlog')).toHaveCount(0);
});
