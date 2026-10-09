import { test, expect } from '../fixtures.mjs';

// Settings > Site: the projects the picker lists first, picked from the site's.
test('projects are removed and picked again', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const row = page.getByRole('group', { name: 'Projects' });
  await expect(row.locator('.st-proj')).toHaveText(['DEMO×']);
  await row.getByRole('button', { name: 'Remove DEMO' }).click();
  await expect(row).toContainText('none');
  await row.getByRole('button', { name: 'Edit' }).click();
  await page.getByRole('option', { name: 'DEMO Demo Shop' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Apply' }).click();
  await expect(row.locator('.st-proj')).toHaveText(['DEMO×']);
});

// Restart serves the app again and the page comes back by itself.
test('restart comes back', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  await page.getByRole('button', { name: 'Advanced' }).click();
  const reloaded = page.waitForEvent('load');
  await page.getByRole('group', { name: 'Restart' }).getByRole('button', { name: 'Restart' }).click();
  await reloaded;
  await expect(page.getByRole('group', { name: 'Projects' })).toBeVisible();
});

// Keys sit folded by group: a click opens one, the filter finds a key in a folded one.
test('key groups fold', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const keys = page.getByRole('group', { name: 'Keyboard', exact: true });
  const global = keys.locator('.st-fold', { hasText: 'Global' });
  await expect(global).toBeVisible();
  await expect(keys.getByRole('group', { name: 'jump to issue by key' })).toHaveCount(0);
  await global.click();
  await expect(keys.getByRole('group', { name: 'jump to issue by key' })).toBeVisible();
  await expect(global.locator('xpath=following-sibling::*[1]')).not.toHaveClass(/st-fold/); // its keys right under it
  await global.click();
  await expect(keys.getByRole('group', { name: 'jump to issue by key' })).toHaveCount(0);
  await page.getByRole('searchbox', { name: 'Filter settings' }).fill('jump to issue by key');
  await expect(keys.getByRole('group', { name: 'jump to issue by key' })).toBeVisible();
});

// The sidebar lists the sections and jumps to one.
test('sidebar jumps to a section', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const nav = page.getByRole('navigation', { name: 'Settings sections' });
  await nav.getByRole('link', { name: 'Keyboard' }).click();
  await expect(nav.getByRole('link', { name: 'Keyboard' })).toHaveClass(/on/);
  await expect(page.locator('.st-fold').first()).toBeInViewport();
});

// A lane layout is on every board it fits, or on the boards ticked.
test('a lane layout goes on every board it fits', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const g = page.getByRole('group', { name: 'lane_layouts' });
  await g.getByRole('button', { name: 'New layout' }).click();
  const every = g.getByRole('checkbox', { name: 'Every board it fits' }), board = g.getByRole('checkbox', { name: 'DEMO board' });
  await expect(every).toBeChecked();
  await board.check();
  await expect(every).not.toBeChecked();
  await every.check();
  await expect(board).not.toBeChecked();
  await every.uncheck();
  await expect(board).toBeChecked();
});

// A column splits into its statuses: one moved to another lane stays there after a reload, and gathering puts the
// column back whole.
test('a lane layout splits a column over lanes', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const g = page.getByRole('group', { name: 'lane_layouts' });
  await g.getByRole('button', { name: 'New layout' }).click();
  await g.getByRole('button', { name: 'Split In Review into statuses' }).click();
  const testing = g.getByRole('button', { name: 'In Review › Testing', exact: true });
  await expect(testing).toBeVisible();
  await testing.click();
  await page.keyboard.press('L');
  const done = g.locator('.ln-lane').filter({ has: page.getByRole('button', { name: 'Done', exact: true }) });
  await expect(done.getByRole('button', { name: 'In Review › Testing', exact: true })).toBeVisible();
  await expect(done.getByRole('textbox', { name: 'Lane name' })).toHaveAttribute('placeholder', 'Done');
  await page.waitForTimeout(600); // the debounced save
  await page.reload();
  await expect(done.getByRole('button', { name: 'In Review › Testing', exact: true })).toBeVisible();
  await expect(g.getByRole('button', { name: 'In Review › In Review', exact: true })).toBeVisible();
  await done.getByRole('button', { name: 'Gather In Review here' }).click();
  await expect(done.getByRole('button', { name: 'In Review', exact: true })).toBeVisible();
  await expect(g.getByRole('button', { name: 'In Review › Testing', exact: true })).toHaveCount(0);
});

// A drag in the card designer keeps the page where it is: the drop redraws the row, not the scroll.
test('a card designer drag keeps the scroll', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const g = page.getByRole('group', { name: 'card_layout' });
  const chip = g.locator('.cd-tray .cd-chip').first(), top = g.locator('.cd-top .cd-chip');
  await expect(chip).toBeVisible();
  const n = await top.count();
  await chip.dragTo(g.locator('.cd-top'));
  await expect(top).toHaveCount(n + 1);
  await expect(g.locator('.cd-card')).toBeInViewport();
});

// A fold opens and shuts in place: the cursor stays on it, j steps into its keys or past them.
test('key groups fold by keyboard, the cursor stays', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const keys = page.getByRole('group', { name: 'Keyboard', exact: true });
  const global = keys.locator('.st-fold', { hasText: 'Global' }), sel = page.locator('.st-row.sel');
  await global.click(); // opens, and selects it
  await expect(global).toHaveClass(/\bsel\b/);
  await page.keyboard.press('j');
  await expect(sel).toHaveAttribute('aria-label', 'command palette');
  await page.keyboard.press('k');
  await page.keyboard.press('Enter'); // shuts
  await expect(keys.getByRole('group', { name: 'command palette' })).toHaveCount(0);
  await expect(global).toHaveClass(/\bsel\b/);
  await page.keyboard.press('j');
  await expect(sel).toHaveClass(/\bst-fold\b/);
  await expect(sel).not.toContainText('Global');
});

// Advanced hides the options for specifics and tuning (a group of only those too); the filter still finds one,
// marked, and the choice outlasts a reload.
test('advanced options hide behind a toggle', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const pw = page.getByRole('group', { name: 'panel_width' }), adv = page.getByRole('button', { name: 'Advanced' });
  await expect(page.getByRole('group', { name: 'lane_layouts' })).toBeVisible();
  await expect(pw).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Refresh', exact: true })).toHaveCount(0);
  const filter = page.getByRole('searchbox', { name: 'Filter settings' });
  await filter.fill('panel_width');
  await expect(pw.locator('.chip', { hasText: 'advanced' })).toBeVisible();
  await filter.fill('');
  await expect(pw).toHaveCount(0);
  await adv.click();
  await expect(adv).toHaveAttribute('aria-pressed', 'true');
  await expect(pw).toBeVisible();
  await expect(pw.locator('.chip', { hasText: 'advanced' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Refresh', exact: true })).toBeVisible();
  await page.reload();
  await expect(pw).toBeVisible();
  await page.keyboard.press('a');
  await expect(pw).toHaveCount(0);
  await expect(adv).toHaveAttribute('aria-pressed', 'false');
});

// Settings > Jira site on Jira: Atlassian's token page, nothing asked of laneway-server's.
test('api tokens on jira link to atlassian', async ({ page, app }) => {
  await page.goto(new URL('#/settings', app.url).href);
  const row = page.getByRole('group', { name: 'API tokens and apps' });
  await expect(row.getByRole('link', { name: 'Atlassian\'s API token page' })).toHaveAttribute('href', /id\.atlassian\.com/);
  await expect(row.getByRole('button', { name: 'Make a token' })).toHaveCount(0);
});

test.describe('on laneway-server', () => {
  test.use({ flavour: 'laneway' });

  // A token made shows its secret once; one revoked after a confirm, then the rest at once.
  test('api tokens are made and revoked', async ({ page, app }) => {
    await page.goto(new URL('#/settings', app.url).href);
    const row = page.getByRole('group', { name: 'API tokens and apps' });
    const ci = row.getByRole('group', { name: 'CI pipeline' });
    await expect(ci).toContainText('read:jira-work');
    await expect(ci).toContainText('expires');
    await expect(row.getByRole('group', { name: 'old laptop' })).toContainText('never used');
    await row.getByRole('button', { name: 'Make a token' }).click();
    const form = page.getByRole('dialog', { name: 'Make a token' });
    await form.getByRole('textbox', { name: 'Label' }).fill('deploy script');
    await form.getByRole('spinbutton', { name: 'Days it lives' }).fill('7');
    await form.getByRole('button', { name: 'Make' }).click();
    await expect(row.getByRole('status')).toContainText('deploy script: copy it now');
    await expect(row.locator('.st-tok-secret')).toHaveText(/^lws_/);
    await expect(row.getByRole('group', { name: 'deploy script' })).toBeVisible();
    await ci.getByRole('button', { name: 'Revoke' }).click();
    await page.getByRole('dialog', { name: 'Revoke CI pipeline' }).getByRole('button', { name: 'Revoke' }).click();
    await expect(ci).toHaveCount(0);
    await row.getByRole('button', { name: 'Revoke all' }).click();
    await page.getByRole('dialog', { name: 'Revoke all tokens' }).getByRole('button', { name: 'Revoke all' }).click();
    await expect(row.locator('.st-tok-row')).toHaveCount(3); // the apps' only
    await expect(row.locator('.st-tok-secret')).toHaveCount(0);
  });

  // The signed-in apps, this one marked (not laneway on another device); another revoked.
  test('signed-in apps are revoked', async ({ page, app }) => {
    await page.goto(new URL('#/settings', app.url).href);
    const row = page.getByRole('group', { name: 'API tokens and apps' });
    const lw = row.getByRole('group', { name: 'laneway', exact: true });
    await expect(lw).toHaveCount(2);
    await expect(lw.filter({ hasText: 'this app' })).toHaveCount(1);
    const cal = row.getByRole('group', { name: 'Calendar sync' });
    await expect(cal).not.toContainText('this app');
    await cal.getByRole('button', { name: 'Revoke' }).click();
    await page.getByRole('dialog', { name: 'Revoke Calendar sync' }).getByRole('button', { name: 'Revoke' }).click();
    await expect(cal).toHaveCount(0);
  });
});
