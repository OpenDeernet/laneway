import { test, expect } from '../fixtures.mjs';

// The demo as Jira: the dashboard's gadgets that list issues draw here, the rest link to the site.
test('a dashboard shows its gadgets in their columns', async ({ page, app }) => {
  await page.goto(new URL('#/dashboards', app.url).href);
  const view = page.locator('#view');
  await expect(view.getByRole('heading', { name: 'Team dashboard' })).toBeVisible();
  const gadget = t => view.locator('section.db-g', { has: page.locator('.ht', { hasText: t }) });
  await expect(gadget('Assigned to me').locator('.hrow').first()).toBeVisible();
  await expect(gadget('Recently updated').locator('.hrow')).not.toHaveCount(0);
  await expect(gadget('Open by status')).toContainText('This gadget shows on the site.');
  // An issue row opens the issue in the panel.
  const key = (await gadget('Assigned to me').locator('.hk').first().textContent()).trim();
  await gadget('Assigned to me').locator('.hrow').first().click();
  await expect(page.locator('#panel')).toContainText(key);
});

test.describe('on laneway-server', () => {
  test.use({ flavour: 'laneway' });
  test('every gadget draws from what the server computes', async ({ page, app }) => {
    await page.goto(new URL('#/dashboards', app.url).href);
    const view = page.locator('#view');
    const gadget = t => view.locator('section.db-g', { has: page.locator('.ht', { hasText: t }) });
    await expect(gadget('Open by status').locator('.db-bucket')).not.toHaveCount(0);
    await expect(gadget('Open by status')).toContainText('To Do');
    await expect(gadget('Created vs resolved').locator('svg')).toBeVisible();
    await expect(gadget('Assigned to me').locator('.db-foot')).toContainText(' of ');
  });
});
