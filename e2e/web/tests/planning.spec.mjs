import { test, expect, pick } from '../fixtures.mjs';

const head = (page, name) => page.locator('.pl-head', { has: page.locator('.pl-name', { hasText: new RegExp('^' + name + '$') }) });
const row = (page, key) => page.locator('.lrow', { has: page.locator('.l-key', { hasText: new RegExp('^' + key + '$') }) });

// select puts the cursor on key's row: a click opens it beside the list,
// esc gives the keys back to the list.
async function select(page, key) {
  await row(page, key).click();
  await expect(page.locator('.iss .iss-key')).toHaveText(key);
  await page.keyboard.press('Escape');
  await expect(page.locator('.iss')).toHaveCount(0);
}

test.beforeEach(async ({ page, app }) => {
  await page.goto(new URL('#/planning', app.url).href);
  await expect(row(page, 'DEMO-17')).toBeVisible();
});

test('m moves a backlog issue into the next sprint and back', async ({ page }) => {
  await expect(head(page, 'Sprint 13')).toContainText('3 issues');
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await expect(head(page, 'Sprint 13')).toContainText('4 issues');
  await expect(head(page, 'Backlog')).toContainText('3 issues');
  await page.reload();
  await expect(head(page, 'Sprint 13')).toContainText('4 issues');
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  await pick(page, 'Backlog');
  await expect(head(page, 'Sprint 13')).toContainText('3 issues');
  await expect(head(page, 'Backlog')).toContainText('4 issues');
});

// Jira's agile lists trail a write by seconds: a reload that still has the old
// sprints keeps the move instead of putting the issue back.
test('a stale reload keeps a move', async ({ page }) => {
  let stale = null;
  await page.route('**/api/plan/*', async route => {
    const res = await route.fetch();
    if (!stale) stale = await res.text();
    await route.fulfill({ response: res, body: stale });
  });
  await page.keyboard.press('R');
  await expect.poll(() => stale).not.toBeNull();
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  const moved = page.waitForResponse('**/api/plan/move');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await expect(head(page, 'Sprint 13')).toContainText('4 issues');
  await moved;
  await page.waitForTimeout(800);
  const reloaded = page.waitForResponse('**/api/plan/*');
  await page.keyboard.press('R');
  await reloaded;
  await expect(head(page, 'Sprint 13')).toContainText('4 issues');
  await expect(head(page, 'Backlog')).toContainText('3 issues');
});

// A move without a place to drop goes to the edge nearest where it came from, in Jira too.
test('a backlog issue moved up lands at the bottom of the sprint', async ({ page }) => {
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  const ranked = page.waitForResponse('**/api/plan/rank');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await ranked;
  await page.reload();
  const y = async l => (await l.boundingBox()).y;
  await expect(row(page, 'DEMO-17')).toBeVisible();
  const at = await y(row(page, 'DEMO-17')), backlog = await y(head(page, 'Backlog'));
  expect(at).toBeLessThan(backlog);
  expect(backlog - at).toBeLessThan(50);
});

test('two issues dragged into a sprint: one move, one rank', async ({ page }) => {
  const writes = [];
  page.on('request', r => { if (r.method() === 'POST' && /\/api\/(plan|issues)\//.test(r.url())) writes.push(r.url().replace(/.*\/api/, '') + ' ' + r.postData()); });
  await select(page, 'DEMO-17');
  await page.keyboard.press('x'); // marks it, steps to the next
  await page.keyboard.press('x');
  const ranked = page.waitForResponse('**/api/plan/rank');
  const first = await page.evaluate(() => {
    const heads = [...document.querySelectorAll('.pl-abs')].sort((a, b) => a.getBoundingClientRect().y - b.getBoundingClientRect().y);
    const i = heads.findIndex(n => /^Sprint 13/.test(n.textContent));
    return heads[i + 1].querySelector('.l-key').textContent;
  });
  await row(page, 'DEMO-17').dragTo(row(page, first), { targetPosition: { x: 40, y: 4 } });
  await ranked;
  await expect(head(page, 'Sprint 13')).toContainText('5 issues');
  expect(writes.filter(w => w.startsWith('/plan/move'))).toHaveLength(1);
  expect(writes.filter(w => w.includes('/rank'))).toHaveLength(1);
  const y = async k => (await row(page, k).boundingBox()).y;
  const order = async () => [await y('DEMO-17'), await y('DEMO-18'), await y(first)];
  const before = await order();
  expect(before).toEqual([...before].sort((a, b) => a - b));
  await page.reload();
  await expect(head(page, 'Sprint 13')).toContainText('5 issues');
  expect(await order()).toEqual(before);
});

// A bulk move (X) while Jira's lists still have the issues where they were: no
// issue in two places, none back where it came from.
test('a bulk move holds while Jira catches up', async ({ page }) => {
  let stale = null;
  await page.route('**/api/plan/*', async route => {
    const res = await route.fetch();
    if (!stale) stale = await res.text();
    await route.fulfill({ response: res, body: stale });
  });
  await page.keyboard.press('R');
  await expect.poll(() => stale).not.toBeNull();
  await select(page, 'DEMO-17');
  await page.keyboard.press('x');
  await page.keyboard.press('x');
  await page.keyboard.press('X');
  await pick(page, 'Sprint / backlog');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await page.getByRole('dialog').getByRole('button', { name: 'Apply' }).click();
  await expect(head(page, 'Sprint 13')).toContainText('5 issues');
  await expect(head(page, 'Backlog')).toContainText('2 issues');
  await page.waitForTimeout(3500);
  await expect(head(page, 'Sprint 13')).toContainText('5 issues');
  await expect(head(page, 'Backlog')).toContainText('2 issues');
  await expect(row(page, 'DEMO-17')).toHaveCount(1);
});

// Jira's sprint and backlog lists can disagree for a moment; an issue in both shows once,
// where its sprint field says.
test('an issue Jira lists twice shows once', async ({ page }) => {
  await page.route('**/api/plan/*', async route => {
    const res = await route.fetch(), d = await res.json();
    const c = d.Backlog.Cards.find(x => x.Key === 'DEMO-17');
    d.Sprints.find(s => s.Name === 'Sprint 13').Cards.push(c);
    await route.fulfill({ response: res, json: d });
  });
  await page.keyboard.press('R');
  await expect(head(page, 'Backlog')).toContainText('4 issues');
  await expect(row(page, 'DEMO-17')).toHaveCount(1);
  await expect(head(page, 'Sprint 13')).toContainText('3 issues');
});

// Jira's backlog leaves done issues out: moving one there asks, and it goes from the list.
test('a done issue to the backlog asks first, and u brings it back', async ({ page }) => {
  const key = await page.locator('.lrow[data-key]', { has: page.locator('.cat-done') }).first().getAttribute('data-key');
  await select(page, key);
  await page.keyboard.press('m');
  await pick(page, 'Backlog');
  const dlg = page.getByRole('dialog');
  await expect(dlg).toContainText(key + ' is done');
  await dlg.getByRole('button', { name: 'Cancel' }).click();
  await expect(row(page, key)).toHaveCount(1);
  await page.keyboard.press('m');
  await pick(page, 'Backlog');
  const moved = page.waitForResponse('**/api/plan/move');
  await dlg.getByRole('button', { name: 'Move' }).click();
  await expect(row(page, key)).toHaveCount(0);
  await moved;
  await page.keyboard.press('u');
  await expect(row(page, key)).toHaveCount(1);
  await page.reload();
  await expect(row(page, key)).toHaveCount(1);
});

test('a bulk move of a done issue to the backlog asks first', async ({ page }) => {
  const key = await page.locator('.lrow[data-key]', { has: page.locator('.cat-done') }).first().getAttribute('data-key');
  await select(page, key);
  await page.keyboard.press('x');
  await page.keyboard.press('X');
  await pick(page, 'Sprint / backlog');
  await expect(page.getByRole('dialog')).toContainText('Move 1 issues to');
  await pick(page, 'Backlog');
  const dlg = page.getByRole('dialog');
  await expect(dlg).toContainText(key + ' is done');
  await dlg.getByRole('button', { name: 'Move' }).click();
  await expect(row(page, key)).toHaveCount(0);
  await page.reload();
  await expect(row(page, 'DEMO-17')).toBeVisible();
  await expect(row(page, key)).toHaveCount(0);
});

test('K ranks an issue up', async ({ page }) => {
  const above = async (a, b) => (await row(page, a).boundingBox()).y < (await row(page, b).boundingBox()).y;
  expect(await above('DEMO-18', 'DEMO-19')).toBe(true);
  await select(page, 'DEMO-19');
  await page.keyboard.press('K');
  await expect.poll(() => above('DEMO-19', 'DEMO-18')).toBe(true);
  await page.reload();
  await expect(row(page, 'DEMO-19')).toBeVisible();
  expect(await above('DEMO-19', 'DEMO-18')).toBe(true);
});

test('a sprint goal', async ({ page }) => {
  await head(page, 'Sprint 13').getByRole('button', { name: 'Edit' }).click();
  const dialog = page.getByRole('dialog');
  await dialog.getByLabel(/goal/i).fill('Invoices out of the door');
  await dialog.getByRole('button', { name: /save|ok/i }).click();
  await expect(head(page, 'Sprint 13')).toContainText('Invoices out of the door');
  await page.reload();
  await expect(head(page, 'Sprint 13')).toContainText('Invoices out of the door');
});

test('complete the active sprint, start the next', async ({ page }) => {
  await head(page, 'Sprint 12 - Checkout').getByRole('button', { name: 'Complete' }).click();
  await page.getByRole('dialog').getByRole('button', { name: /complete/i }).click();
  await expect(head(page, 'Sprint 12 - Checkout')).toHaveCount(0);
  await head(page, 'Sprint 13').getByRole('button', { name: 'Start' }).click();
  await page.getByRole('dialog').getByRole('button', { name: /start/i }).click();
  await expect(head(page, 'Sprint 13')).toContainText('active');
  // The open issues of the sprint closed went on: the board is Sprint 13's.
  await page.goto(new URL('/board', page.url()).href);
  await expect(page.locator('.crumb', { hasText: 'Sprint 13' })).toBeVisible();
  await expect(page.locator('.bd-lanes .card', { hasText: 'DEMO-14' })).toBeVisible();
});

test('a column edge drags to resize', async ({ page }) => {
  const key = page.locator('.bd-lhead [data-col=key]').first();
  const w0 = (await key.boundingBox()).width, g = await key.locator('.lh-grip').boundingBox();
  await page.mouse.move(g.x + g.width / 2, g.y + g.height / 2);
  await page.mouse.down();
  await page.mouse.move(g.x + 40, g.y + g.height / 2, { steps: 5 });
  await page.mouse.up();
  expect((await key.boundingBox()).width).toBeGreaterThan(w0 + 30);
  const cell = page.locator('.lrow', { hasText: 'DEMO-5' }).locator('.l-key');
  expect(Math.abs((await cell.boundingBox()).width - (await key.boundingBox()).width)).toBeLessThan(2);
});

// laneway-server's lists show a write at once: after your own move, someone else's shows on the
// next load, not when the hold for Jira's lag runs out.
test.describe('on laneway-server', () => {
  test.use({ flavour: 'laneway' });
  test("someone else's move shows at once after yours", async ({ page }) => {
    await select(page, 'DEMO-17');
    await page.keyboard.press('m');
    const moved = page.waitForResponse('**/api/plan/move');
    await pick(page, 'Sprint 13', /Sprint 13/);
    await moved;
    const board = await page.evaluate(() => performance.getEntriesByType('resource').map(e => e.name).find(u => /\/api\/plan\/\d+/.test(u)));
    const sprint = (await (await page.request.get(board)).json()).Sprints.find(x => x.Name === 'Sprint 13').ID;
    expect((await page.request.post(new URL('/api/plan/move', board).href, { data: { Keys: ['DEMO-18'], Sprint: sprint } })).ok()).toBe(true);
    await page.keyboard.press('R');
    await expect(head(page, 'Sprint 13')).toContainText('5 issues');
  });

  // The site's change stream (the demo has none: this stands in for it) loads planning again.
  test("someone else's move shows without R", async ({ page }) => {
    let tell;
    const told = new Promise(r => { tell = r; });
    await page.addInitScript(() => { window.SharedWorker = undefined; }); // page.route misses a shared worker's requests
    await page.route('**/api/site/events', async route => {
      const ch = await told;
      await route.fulfill({ contentType: 'text/event-stream', body: `retry: 60000\n\nevent: change\ndata: ${JSON.stringify(ch)}\n\n` });
    });
    await page.reload();
    await expect(row(page, 'DEMO-17')).toBeVisible();
    const board = await page.evaluate(() => performance.getEntriesByType('resource').map(e => e.name).find(u => /\/api\/plan\/\d+/.test(u)));
    const sprint = (await (await page.request.get(board)).json()).Sprints.find(x => x.Name === 'Sprint 13').ID;
    expect((await page.request.post(new URL('/api/plan/move', board).href, { data: { Keys: ['DEMO-18'], Sprint: sprint } })).ok()).toBe(true);
    await expect(head(page, 'Sprint 13')).toContainText('3 issues');
    tell({ seq: 1, kind: 'issue', action: 'put', key: 'DEMO-18' });
    await expect(head(page, 'Sprint 13')).toContainText('4 issues');
  });
});
