import { test, expect, palette } from '../fixtures.mjs';

const panel = page => page.locator('.iss');
const openIssue = async (page, app) => {
  await page.goto(app.url);
  await page.locator('.card', { hasText: 'DEMO-4' }).click();
  await expect(panel(page).locator('.iss-title')).toHaveText('Checkout without an account');
};

test('Jira without a service desk: no request, comments for everyone', async ({ page, app }) => {
  await openIssue(page, app);
  await page.keyboard.press('c');
  await expect(panel(page).locator('.vis-chip')).toContainText('everyone');
  await expect(panel(page).locator('.request')).toBeEmpty();
});

test.describe('on a service desk', () => {
  test.use({ flavour: 'laneway' });

  test('the panel shows the request; a comment is an internal note till ctrl+o makes it a reply to the customer', async ({ page, app }) => {
    await openIssue(page, app);
    const p = panel(page);
    await expect(p.locator('.request')).toContainText('Customer');
    await expect(p.locator('.request')).toContainText('Time to resolution');
    await page.keyboard.press('c');
    await expect(p.locator('.vis-chip')).toContainText('internal note');
    await page.keyboard.type('Looking into it');
    await page.keyboard.press('Control+Enter');
    await expect(p.locator('article.cm', { hasText: 'Looking into it' }).locator('.cm-lock')).toContainText('internal note');

    await page.locator('.composer .ed-ta').click();
    await page.keyboard.press('Control+o');
    await expect(p.locator('.vis-chip')).toContainText('reply to the customer');
    await page.keyboard.type('Fixed for you');
    await page.keyboard.press('Control+Enter');
    await expect(p.locator('article.cm', { hasText: 'Fixed for you' })).toBeVisible();
    await expect(p.locator('article.cm', { hasText: 'Fixed for you' }).locator('.cm-lock')).toHaveCount(0);
    await expect(p.locator('.vis-chip')).toContainText('internal note'); // the next one is a note again
  });

  test('the queues are saved lists: on home and in the palette', async ({ page, app }) => {
    await page.goto(new URL('#/home', app.url).href);
    const row = page.locator('[data-w="filters"] .hrow', { hasText: 'DEMO · Open requests' });
    await expect(row).toBeVisible();
    await row.click();
    await expect(page).toHaveURL(/sprint=jql/);
    await expect(page.locator('.card').first()).toBeVisible();

    await (await palette(page, '/', '#', /DEMO · Unassigned/)).click();
    await expect(page.getByRole('textbox', { name: 'Palette' })).toHaveValue(/assignee is EMPTY/);
  });
});
