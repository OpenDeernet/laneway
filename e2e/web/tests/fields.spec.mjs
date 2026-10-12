import { test, expect, openIssue, pick, prompt } from '../fixtures.mjs';

// Each edit lands in the panel and on the card, and stays after a reload
// (the demo keeps writes for its life).
const field = (page, name) => page.locator(`.iss .fld[data-field="${name}"]`);
const card = page => page.locator('.bd-lanes .card', { hasText: 'DEMO-4' });

test.beforeEach(async ({ page, app }) => {
  await openIssue(page, app, 'DEMO-4');
});

test('assignee', async ({ page }) => {
  await page.keyboard.press('a');
  await pick(page, 'Priya', /Priya Nair/);
  await expect(field(page, 'assignee')).toContainText('Priya Nair');
  await expect(card(page).locator('.cav')).toContainText('PN');
  await page.reload();
  await expect(field(page, 'assignee')).toContainText('Priya Nair');
});

test('priority', async ({ page }) => {
  await page.keyboard.press('p');
  await expect(page.getByRole('option', { name: /High current/ })).toBeVisible();
  await pick(page, 'Lowest');
  await expect(field(page, 'priority')).toContainText('Lowest');
  await expect(card(page).locator('.cprio')).toHaveClass(/p4/);
  await page.reload();
  await expect(field(page, 'priority')).toContainText('Lowest');
});

test('points', async ({ page }) => {
  await page.keyboard.press('P');
  await prompt(page, '8');
  await expect(field(page, 'points')).toContainText('8');
  await expect(card(page).locator('.cpts')).toHaveText('8');
  await expect(page.locator('.bd-stats')).toHaveText('10 issues · 28 pts');
});

test('labels: one added, one taken off', async ({ page }) => {
  await page.keyboard.press('l');
  const input = page.locator('.pick-input');
  await expect(input).toBeFocused();
  await input.press('Space'); // the focused option, frontend: off
  await input.fill('checkout');
  await input.press('Tab'); // a new label
  await input.press('Enter');
  await expect(field(page, 'labels')).toContainText('checkout');
  await expect(field(page, 'labels')).not.toContainText('frontend');
  await expect(card(page).locator('.clabels')).toContainText('checkout');
  await page.reload();
  await expect(field(page, 'labels')).toContainText('checkout');
});

test('due date', async ({ page }) => {
  await field(page, 'due').click();
  await prompt(page, '2026-12-24');
  await expect(field(page, 'due')).toContainText('Dec 24');
  await expect(card(page).locator('.cdue')).toContainText('Dec 24');
});

test('description, in the markdown editor', async ({ page }) => {
  await page.keyboard.press('E');
  await expect(page.locator('.iss [role="textbox"][aria-label^="Description"]')).toBeFocused();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.press('Enter');
  await page.keyboard.type('- **Guests** keep their cart');
  await page.keyboard.press('ControlOrMeta+Enter');
  await expect(page.locator('.iss strong', { hasText: 'Guests' })).toBeVisible();
  await page.reload();
  await expect(page.locator('.iss strong', { hasText: 'Guests' })).toBeVisible();
  await expect(page.locator('.iss li', { hasText: 'keep their cart' })).toBeVisible();
});

test('a description being typed is back after a reload', async ({ page }) => {
  await page.keyboard.press('E');
  await expect(page.locator('.iss [role="textbox"][aria-label^="Description"]')).toBeFocused();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type(' Typed just now.');
  await page.reload(); // at once: the page sends it as it goes
  await expect(page.locator('.iss .fld[data-field="status"]')).toBeVisible();
  await page.keyboard.press('E');
  await expect(page.locator('.iss [role="textbox"][aria-label^="Description"]')).toContainText('Typed just now.');
});

test('a description changed in Jira meanwhile is not saved over unasked', async ({ page, app }) => {
  await page.keyboard.press('E');
  await expect(page.locator('.iss [role="textbox"][aria-label^="Description"]')).toBeFocused();
  // A colleague's edit, unchecked, while the editor is open.
  await page.request.put(new URL('/api/issues/DEMO-4/description', app.url).href, { data: { Markdown: 'Acceptance: guests check out.' } });
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type(' Mine.');
  await page.keyboard.press('ControlOrMeta+Enter');
  const dialog = page.getByRole('dialog', { name: 'Description changed in Jira' });
  await expect(dialog).toContainText('- Acceptance: guests check out.');
  await dialog.getByRole('button', { name: 'Keep editing' }).click();
  await expect(page.locator('.iss [role="textbox"][aria-label^="Description"]')).toBeVisible();
  // Kept editing, the next save goes over theirs.
  await page.locator('.iss [role="textbox"][aria-label^="Description"]').click();
  await page.keyboard.press('ControlOrMeta+Enter');
  await expect(page.locator('.iss .desc:not(.doc)')).toContainText('Mine.');
  await expect(page.locator('.iss .desc:not(.doc)')).not.toContainText('Acceptance');
  app.errors.splice(0, app.errors.length, ...app.errors.filter(e => !/^409 PUT .*\/description$/.test(e)));
});

test('a custom field: Team, under More fields', async ({ page }) => {
  await page.getByRole('button', { name: /^More fields/ }).click();
  const team = page.locator('.iss').getByRole('button', { name: /^Team/ });
  await team.click();
  const dialog = page.getByRole('dialog', { name: 'DEMO-4 Team' });
  await dialog.getByRole('combobox').selectOption('Platform');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(team).toContainText('Platform');
  await page.reload();
  await expect(page.getByRole('button', { name: /^More fields/ })).toHaveAttribute('aria-expanded', 'true');
  await expect(team).toContainText('Platform');
});

// Team is required (the demo's field configuration): marked *, and its form won't send it empty.
test('a required field: Team stays set', async ({ page }) => {
  await page.getByRole('button', { name: /^More fields/ }).click();
  const team = page.locator('.iss').getByRole('button', { name: /^Team/ });
  await expect(team.locator('.k.req')).toBeVisible();
  await expect(page.locator('.iss').getByRole('button', { name: /^Legacy ref/ }).locator('.k.req')).toHaveCount(0);
  await team.click();
  let dialog = page.getByRole('dialog', { name: 'DEMO-4 Team' });
  await expect(dialog.locator('.form-label.req')).toHaveText('Team');
  await dialog.getByRole('combobox').selectOption('Platform');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(team).toContainText('Platform');
  await team.click();
  dialog = page.getByRole('dialog', { name: 'DEMO-4 Team' });
  await dialog.getByRole('combobox').selectOption('');
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog.getByRole('alert')).toHaveText('Fill in Team');
  await expect(dialog.getByRole('combobox')).toHaveValue('');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await page.reload();
  await expect(team).toContainText('Platform');
});

test('a rich-text custom field: Test notes', async ({ page }) => {
  const head = page.locator('.iss .sec-head', { hasText: 'Test notes' });
  await head.getByRole('button', { name: 'Edit' }).click();
  await expect(page.locator('.iss [contenteditable]:focus')).toBeVisible();
  await page.keyboard.press('ControlOrMeta+End');
  await page.keyboard.type(' Also on iOS.');
  await page.keyboard.press('ControlOrMeta+Enter');
  await expect(page.locator('.iss')).toContainText('Also on iOS.');
  await page.reload();
  await expect(page.locator('.iss')).toContainText('Also on iOS.');
});

// The full-screen editor covers the page, in the panel and on the issue page (#view's containment off).
for (const where of ['panel', 'page']) test(`the editor goes full screen, in the ${where}`, async ({ page, app }) => {
  if (where === 'page') await page.goto(new URL('#/issue/DEMO-4', app.url).href);
  await expect(page.locator('.iss .iss-key')).toHaveText('DEMO-4');
  await page.keyboard.press('E');
  const ed = page.locator('.iss .ed', { has: page.locator('[role="textbox"][aria-label^="Description"]') });
  await ed.getByRole('button', { name: /^Full screen/ }).click();
  await expect(ed).toHaveClass(/\bfull\b/);
  const vp = page.viewportSize();
  await expect.poll(async () => { const b = await ed.boundingBox(); return b.x === 0 && b.width === vp.width && b.y < 10 && b.height > vp.height - 20; }).toBe(true); // its margin aside
  await page.keyboard.press('Escape');
  await expect(ed).not.toHaveClass(/\bfull\b/);
});
