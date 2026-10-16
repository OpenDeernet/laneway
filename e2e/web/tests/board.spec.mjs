import { test, expect, inLane, selectCard, pick } from '../fixtures.mjs';

const lane = (page, name) => page.locator('.bd-lane', { has: page.locator('.bd-lane-name', { hasText: name }) });
const card = (page, key) => page.locator('.card', { hasText: key });

test.beforeEach(async ({ page, app }) => {
  await page.goto(app.url);
  await expect(card(page, 'DEMO-5')).toBeVisible();
});

test('the active sprint in lanes', async ({ page }) => {
  for (const name of ['TO DO', 'IN PROGRESS', 'IN REVIEW', 'DONE']) await expect(lane(page, name)).toBeVisible();
  await expect(lane(page, 'IN PROGRESS').locator('.card:visible')).toHaveCount(2);
  await expect(inLane(page, 'IN PROGRESS', 'DEMO-4')).toBeVisible();
  await expect(page.locator('.bd-stats')).toHaveText('10 issues · 25 pts');
});

test('L moves a card a lane on, and Jira keeps it', async ({ page }) => {
  await selectCard(page, 'DEMO-5');
  await page.keyboard.press('L');
  await expect(inLane(page, 'IN PROGRESS', 'DEMO-5')).toBeVisible();
  await page.reload();
  await expect(inLane(page, 'IN PROGRESS', 'DEMO-5')).toBeVisible();
  await expect(inLane(page, 'TO DO', 'DEMO-5')).toHaveCount(0);
});

test('the filter and a quick filter narrow the cards', async ({ page }) => {
  await page.locator('.bd-filter').fill('address');
  await expect(page.locator('.bd-lanes .card:visible')).toHaveCount(2);
  await page.locator('.bd-filter').fill('');
  await expect(page.locator('.bd-lanes .card:visible')).toHaveCount(10);
  await page.locator('.fchip', { hasText: 'Bugs' }).click();
  await expect(page.locator('.bd-lanes .card:visible')).toHaveCount(3);
  for (const c of await page.locator('.bd-lanes .card:visible').all()) await expect(c.locator('.ctype')).toHaveAttribute('title', 'Bug');
});

test('the assignee picker: a name picks it alone, the box ticks', async ({ page }) => {
  const dialog = page.getByRole('dialog');
  const row = name => dialog.locator('.pick-row', { hasText: name });
  await page.keyboard.press('A');
  await row('Mira Jansen').locator('.pick-label').click();
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('.fchip', { hasText: 'Mira Jansen' })).toBeVisible();
  await page.keyboard.press('A');
  await row('Priya Nair').locator('.check').click();
  await expect(dialog).toBeVisible();
  await row('Sam Okafor').locator('.check').click();
  await dialog.getByRole('button', { name: 'Apply' }).click();
  await expect(page.locator('.fchip', { hasText: 'Mira Jansen +2' })).toBeVisible();
});

test('a list column dragged onto another moves there, and stays', async ({ page }) => {
  await page.keyboard.press('t');
  const head = page.locator('.bd-lhead');
  await expect(head.locator('[data-col]').nth(1)).toHaveAttribute('data-col', 'key');
  await head.locator('[data-col=status]').dragTo(head.locator('[data-col=key]'), { targetPosition: { x: 2, y: 5 } });
  await expect(head.locator('[data-col]').nth(1)).toHaveAttribute('data-col', 'status');
  await expect(page.locator('.lrow', { hasText: 'DEMO-5' }).locator('> span').nth(1)).toHaveClass(/l-status/);
  await page.reload();
  await expect(page.locator('.bd-lhead [data-col]').nth(1)).toHaveAttribute('data-col', 'status');
});

test('a column drops anywhere over the header, below a label or in a gap', async ({ page }) => {
  await page.keyboard.press('t');
  const head = page.locator('.bd-lhead'), cols = () => head.locator('[data-col]');
  const box = async c => head.locator(`[data-col=${c}]`).boundingBox();
  const hb = await head.boundingBox(), key = await box('key');
  // under the key label, near the header's bottom edge
  await head.locator('[data-col=status]').dragTo(head, { targetPosition: { x: key.x - hb.x + 4, y: hb.height - 3 } });
  await expect(cols().nth(1)).toHaveAttribute('data-col', 'status');
  // in the gap just right of the key: after it
  const k2 = await box('key');
  await head.locator('[data-col=priority]').dragTo(head, { targetPosition: { x: k2.x + k2.width - hb.x + 3, y: 3 } });
  await expect(cols().nth(3)).toHaveAttribute('data-col', 'priority');
});

test('a column edge drags to resize, a double-click resets it', async ({ page }) => {
  await page.keyboard.press('t');
  const head = page.locator('.bd-lhead'), key = head.locator('[data-col=key]'), grip = key.locator('.lh-grip');
  const w0 = (await key.boundingBox()).width, g = await grip.boundingBox();
  await page.mouse.move(g.x + g.width / 2, g.y + g.height / 2);
  await page.mouse.down();
  await page.mouse.move(g.x + 60, g.y + g.height / 2, { steps: 5 });
  await page.mouse.up();
  const w1 = (await key.boundingBox()).width;
  expect(w1).toBeGreaterThan(w0 + 50);
  const cell = page.locator('.lrow', { hasText: 'DEMO-5' }).locator('.l-key');
  expect(Math.abs((await cell.boundingBox()).width - w1)).toBeLessThan(2); // the rows follow
  await expect(key.locator('svg')).toHaveCount(0); // no sort
  await expect(head.locator('[data-col]').nth(1)).toHaveAttribute('data-col', 'key'); // no move
  await page.reload();
  await expect(page.locator('.bd-lhead [data-col=key]')).toBeVisible();
  expect(Math.abs((await page.locator('.bd-lhead [data-col=key]').boundingBox()).width - w1)).toBeLessThan(2);
  await page.locator('.bd-lhead [data-col=key] .lh-grip').dblclick();
  expect(Math.abs((await page.locator('.bd-lhead [data-col=key]').boundingBox()).width - w0)).toBeLessThan(2);
  await expect(page.locator('.bd-lhead [data-col=key] svg')).toHaveCount(0);
});

test('a phone keeps the key, summary and status, whatever the order', async ({ page }) => {
  await page.keyboard.press('t');
  const head = page.locator('.bd-lhead');
  await head.locator('[data-col=updated]').dragTo(head.locator('[data-col=key]'), { targetPosition: { x: 2, y: 5 } });
  await page.setViewportSize({ width: 400, height: 800 });
  await expect(head.locator('[data-col=updated]')).toBeHidden();
  for (const c of ['key', 'summary', 'status']) await expect(head.locator(`[data-col=${c}]`)).toBeVisible();
});

test('j k h l walk the cards, enter opens one, ] the next', async ({ page }) => {
  const sel = page.locator('.bd-lanes .card.sel .ckey');
  await page.keyboard.press('j');
  await expect(sel).toHaveText('DEMO-5');
  await page.keyboard.press('j');
  await expect(sel).toHaveText('DEMO-9');
  await page.keyboard.press('l'); // the same row, a lane on
  await expect(sel).toHaveText('DEMO-6');
  await page.keyboard.press('k');
  await expect(sel).toHaveText('DEMO-4');
  // [ ] are the panel's once it has the focus, the board's views before.
  const panel = page.locator('body.panel-focus .iss .iss-key');
  await page.keyboard.press('Enter');
  await expect(panel).toHaveText('DEMO-4');
  await page.keyboard.press(']');
  await expect(panel).toHaveText('DEMO-6');
  await expect(sel).toHaveText('DEMO-6');
  await page.keyboard.press('[');
  await expect(page.locator('.iss .iss-key')).toHaveText('DEMO-4');
});

test('u undoes a move', async ({ page }) => {
  await selectCard(page, 'DEMO-5');
  await page.keyboard.press('L');
  await expect(inLane(page, 'IN PROGRESS', 'DEMO-5')).toBeVisible();
  // The card moves at once, u is bound once Jira has the move: press it till then.
  await expect(async () => {
    await page.keyboard.press('u');
    await expect(inLane(page, 'TO DO', 'DEMO-5')).toBeVisible({ timeout: 500 });
  }).toPass();
  await page.reload();
  await expect(inLane(page, 'TO DO', 'DEMO-5')).toBeVisible();
});

// A sprint closed elsewhere while it is shown: the next refresh shows the active one, and the views drop it.
test('a sprint closed elsewhere gives way to the active one', async ({ page }) => {
  await page.keyboard.press('v');
  await pick(page, 'Sprint 12 - Checkout');
  await expect(page).toHaveURL(/sprint=12/);
  const board = Number(new URL(page.url()).pathname.split('/').pop());
  const closed = await page.request.post(new URL('/api/plan/sprints/12/close', page.url()).href, { data: { Board: board, MoveTo: 0 } });
  expect(closed.ok()).toBe(true);
  await page.keyboard.press('r');
  await expect(page.getByText('Sprint 12 - Checkout has closed')).toBeVisible();
  await expect(page).not.toHaveURL(/sprint=12/);
  await expect(page.locator('.crumb', { hasText: 'Sprint 13' })).toBeVisible();
  await page.keyboard.press('v');
  await expect(page.getByRole('dialog').locator('.pick-row', { hasText: 'Sprint 12' })).toHaveCount(0);
});
