import { test, expect, pick } from '../fixtures.mjs';

const head = (page, name) => page.locator('.pl-head', { has: page.locator('.pl-name', { hasText: new RegExp('^' + name + '$') }) });
const row = (page, key) => page.locator('.lrow', { has: page.locator('.l-key', { hasText: new RegExp('^' + key + '$') }) });
async function select(page, key) {
  await row(page, key).click();
  await expect(page.locator('.iss .iss-key')).toHaveText(key);
  await page.keyboard.press('Escape');
  await expect(page.locator('.iss')).toHaveCount(0);
}
const keysOf = page => page.evaluate(() => [...document.querySelectorAll('.pl-pane .pl-abs')]
  .sort((a, b) => a.getBoundingClientRect().y - b.getBoundingClientRect().y)
  .map(n => (n.querySelector('.pl-name') || {}).textContent || (n.querySelector('.lrow[data-key]') || { dataset: {} }).dataset.key).filter(Boolean));

test.beforeEach(async ({ page, app }) => {
  await page.goto(new URL('#/planning', app.url).href);
  await expect(row(page, 'DEMO-17')).toBeVisible();
});

test('jira refuses a move', async ({ page, app }) => {
  const before = await keysOf(page);
  await page.route('**/api/plan/move', route => route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'jira: DEMO-17 cannot be moved: the sprint is closed' }) }));
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await expect(page.getByRole('alert')).toContainText('the sprint is closed');
  await expect.poll(() => keysOf(page)).toEqual(before);
  app.errors.length = 0;
});

// Jira's lists trail: a move that went through stays put when the next one is refused.
test('a refused move leaves the one before it be', async ({ page, app }) => {
  let stale = null;
  await page.route(/\/api\/plan\/\d+/, async route => {
    const res = await route.fetch();
    if (!stale) stale = await res.text();
    await route.fulfill({ response: res, body: stale });
  });
  await page.keyboard.press('R');
  await expect.poll(() => stale).not.toBeNull();
  await select(page, 'DEMO-18');
  await page.keyboard.press('m');
  const ok = page.waitForResponse('**/api/plan/move');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await ok;
  await page.route('**/api/plan/move', route => route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'jira: DEMO-17 cannot be moved' }) }));
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await expect(page.getByRole('alert')).toContainText('cannot be moved');
  await page.waitForTimeout(1500);
  const ks = await keysOf(page);
  expect(ks.indexOf('DEMO-18')).toBeLessThan(ks.indexOf('Backlog'));
  expect(ks.indexOf('DEMO-17')).toBeGreaterThan(ks.indexOf('Backlog'));
  app.errors.length = 0;
});

// u has nothing to take back for a move Jira refused (with nothing to undo, u does nothing).
test('a refused move leaves no undo', async ({ page, app }) => {
  await page.route('**/api/plan/move', route => route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'jira: no' }) }));
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await expect(page.getByRole('alert')).toContainText('no');
  const before = await keysOf(page);
  await page.keyboard.press('u');
  await page.waitForTimeout(500);
  await expect(page.getByText(/Undid/)).toHaveCount(0);
  expect(await keysOf(page)).toEqual(before);
  app.errors.length = 0;
});

// The move went through but the rank didn't: the issue stays in the sprint, where Jira ranks it.
test('a refused rank keeps the move, in Jira order', async ({ page, app }) => {
  await page.route('**/api/plan/rank', route => route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'jira: DEMO-17 not ranked: Rank not possible' }) }));
  await select(page, 'DEMO-17');
  await page.keyboard.press('m');
  await pick(page, 'Sprint 13', /Sprint 13/);
  await expect(page.getByRole('alert')).toContainText('Rank not possible');
  await page.unroute('**/api/plan/rank');
  const board = await page.evaluate(() => performance.getEntriesByType('resource').map(e => e.name).find(u => /\/api\/plan\/\d+/.test(u)));
  const d = await (await page.request.get(board)).json();
  const truth = [...d.Sprints.flatMap(s => [s.Name, ...s.Cards.map(c => c.Key)]), 'Backlog', ...d.Backlog.Cards.map(c => c.Key)];
  expect(truth.indexOf('DEMO-17')).toBeLessThan(truth.indexOf('Backlog'));
  await expect.poll(() => keysOf(page)).toEqual(truth);
  app.errors.length = 0;
});
