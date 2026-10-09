import { test, expect, palette } from '../fixtures.mjs';

test.beforeEach(async ({ page, app }) => {
  await page.goto(app.url);
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
});

test('? lists the keys pressable here', async ({ page }) => {
  await page.keyboard.press('?');
  const help = page.getByRole('dialog', { name: 'Keyboard' });
  await expect(help.locator('h3', { hasText: /^Board$/ })).toBeVisible();
  await expect(help.locator('h3', { hasText: /^Global$/ })).toBeVisible();
  await expect(help.locator('dd', { hasText: 'next card' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(help).toHaveCount(0);
});

test('g t takes the next theme, and keeps it', async ({ page }) => {
  const theme = () => page.evaluate(() => document.documentElement.dataset.theme || 'auto');
  const was = await theme();
  await page.keyboard.press('g');
  await page.keyboard.press('t');
  await expect.poll(theme).not.toBe(was);
  const now = await theme();
  await page.reload();
  await expect(page.locator('.card', { hasText: 'DEMO-5' })).toBeVisible();
  expect(await theme()).toBe(now);
});

test('g g jumps to an issue by key', async ({ page }) => {
  await page.keyboard.press('g');
  await (await palette(page, 'g', '13', /DEMO-13/)).click();
  await expect(page.locator('.iss .iss-key')).toHaveText('DEMO-13');
  await expect(page).toHaveURL(/issue=DEMO-13/);
});

test('g and a view key go there', async ({ page }) => {
  for (const [key, url] of [['p', /\/planning/], ['m', /\/roadmap/], ['i', /\/inbox/], ['b', /\/board/]]) {
    await page.keyboard.press('g');
    await page.keyboard.press(key);
    await expect(page).toHaveURL(url);
  }
});

// Your avatar at the bar's right opens the account menu; a click on it again closes it. On Jira no server links.
test('the account menu', async ({ page }) => {
  const me = page.getByRole('button', { name: 'Jamie Rivers · account' });
  await me.click();
  const menu = page.getByRole('menu');
  await expect(menu).toContainText('demo@example.com');
  await expect(menu.getByRole('menuitem', { name: /^Settings/ })).toBeVisible();
  await expect(menu.getByRole('menuitem', { name: /^My sign-in/ })).toHaveCount(0);
  await expect(menu.getByRole('menuitem', { name: /^Sign out/ })).toHaveCount(0);
  await me.click();
  await expect(menu).toHaveCount(0);
  await me.click();
  await menu.getByRole('menuitem', { name: /^Settings/ }).click();
  await expect(page).toHaveURL(/\/settings$/);
});

test.describe('on laneway-server', () => {
  test.use({ flavour: 'laneway' });

  // The server's sign-in page, its admin (the demo account administers a project), and tokens in Settings.
  test('the account menu links the server', async ({ page }) => {
    await page.getByRole('button', { name: 'Jamie Rivers · account' }).click();
    const menu = page.getByRole('menu');
    await expect(menu.getByRole('menuitem', { name: /^My sign-in and passkeys/ })).toBeVisible();
    await expect(menu.getByRole('menuitem', { name: /^Server admin/ })).toBeVisible();
    await menu.getByRole('menuitem', { name: /^API tokens and apps/ }).click();
    await expect(page).toHaveURL(/\/settings\?row=tokens$/);
    await expect(page.locator('.st-row.sel')).toHaveAttribute('aria-label', 'API tokens and apps');
  });
});
