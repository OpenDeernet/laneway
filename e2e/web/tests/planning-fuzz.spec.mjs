import { test, expect, pick } from '../fixtures.mjs';

// Planning under random use while Jira is slow and its lists trail its writes: seeded moves (m, [ ],
// drag, J/K, marks with m or the bulk X, u, R, a page reload). After each, no issue shows twice and
// each is where the moves put it; at the end the view is the server's, order and all.
// More: FUZZ_SEEDS=1,2,3,4,5 FUZZ_STEPS=80 npx playwright test planning-fuzz
const SEEDS = (process.env.FUZZ_SEEDS || '1,2,3').split(',').map(Number);
const STEPS = Number(process.env.FUZZ_STEPS || 30);
const REFUSE = Number(process.env.FUZZ_REFUSE ?? 0.08); // how often Jira refuses a move or a rank
// Jira: how far a list it answers may trail, at most, and planning.js's HOLD, how long a moved section
// stays as planning has it. laneway-server's lists don't trail.
const FLAVOURS = { jira: { flavour: '', lag: 2500, hold: 10000 }, 'laneway-server': { flavour: 'laneway', lag: 0, hold: 0 } };

test.use({ viewport: { width: 1400, height: 2400 }, actionTimeout: 15000 }); // every row painted; a stuck step says so

function mulberry32(a) {
  return () => { a |= 0; a = (a + 0x6d2b79f5) | 0; let t = Math.imul(a ^ (a >>> 15), 1 | a); t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t; return ((t ^ (t >>> 14)) >>> 0) / 4294967296; };
}
const sleep = ms => new Promise(r => setTimeout(r, ms));
const esc = s => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const row = (page, key) => page.locator('.lrow', { has: page.locator('.l-key', { hasText: new RegExp('^' + key + '$') }) });

// The left pane's sections and their issues, top to bottom, as the next frame paints them.
const view = page => page.evaluate(async () => {
  await new Promise(r => requestAnimationFrame(() => r()));
  const ns = [...document.querySelector('.pl-pane').querySelectorAll('.pl-abs')]
    .map(n => ({ n, y: new DOMMatrix(getComputedStyle(n).transform).m42 })).sort((a, b) => a.y - b.y);
  const out = []; let cur = null;
  for (const { n } of ns) {
    const name = n.querySelector('.pl-name');
    if (name) { cur = { name: name.textContent, keys: [] }; out.push(cur); continue; }
    const r = n.querySelector('.lrow[data-key]');
    if (r && cur) cur.keys.push(r.dataset.key);
  }
  return out;
});
const doneKeys = page => page.evaluate(() => [...document.querySelectorAll('.lrow[data-key]')].filter(r => r.querySelector('.cat-done')).map(r => r.dataset.key));

// slowJira: writes wait up to 600ms; each list in a /plan answer is the server's from up to LAG
// ago, every list from a moment of its own (so an issue moved can show in both, or neither).
async function slowJira(page, LAG) {
  const hist = [];
  let url = '', writes = 0, poll = 0;
  const snap = async () => {
    try { hist.push({ t: Date.now(), d: await (await page.request.get(url)).json() }); } catch { /* page gone */ }
    while (hist.length && Date.now() - hist[0].t > LAG + 1000) hist.shift();
  };
  page.on('request', r => { if (r.method() === 'POST' && r.url().includes('/api/')) writes++; });
  const done = r => { if (r.method() === 'POST' && r.url().includes('/api/')) writes--; };
  page.on('requestfinished', done); page.on('requestfailed', done);
  const errs = [];
  page.on('response', async res => {
    if (res.url().includes('/api/') && res.status() >= 400) errs.push(`${res.request().method()} ${res.url().replace(/.*\/api/, '')} ${res.request().postData() || ''} → ${res.status()} ${await res.text().catch(() => '')}`);
  });
  let refused = 0;
  await page.route(/\/api\/(plan\/(move|rank)|bulk)$/, async route => {
    await sleep(Math.random() * 600);
    if (!route.request().url().endsWith('/bulk') && Math.random() < REFUSE) {
      refused++;
      return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ error: 'jira: refused by the fuzz' }) });
    }
    await route.continue();
  });
  let loading = 0;
  await page.route(/\/api\/plan\/\d+/, async route => {
    loading++;
    try { await answer(route); } finally { loading--; }
  });
  const answer = async route => {
    if (!url) { url = route.request().url(); poll = setInterval(snap, 200); }
    await sleep(Math.random() * 300);
    const res = await route.fetch(), d = await res.json(), now = Date.now();
    const recent = hist.filter(h => now - h.t <= LAG).map(h => h.d);
    const old = () => (recent.length ? recent[Math.floor(Math.random() * recent.length)] : d);
    const out = {
      ...d,
      Sprints: d.Sprints.map(s => { const o = old().Sprints.find(x => x.ID === s.ID); return o ? { ...s, Cards: o.Cards, Total: o.Total } : s; }),
      Backlog: old().Backlog,
    };
    await route.fulfill({ response: res, json: out });
  };
  return {
    errs,
    refused: () => refused,
    // still: no load on the way, so the rows stay where they are while the pointer aims
    still: async () => { await expect.poll(() => loading, { timeout: 20000 }).toBe(0); await sleep(150); },
    truth: async () => {
      const d = await (await page.request.get(url)).json();
      return [...d.Sprints.map(s => ({ name: s.Name, keys: s.Cards.map(c => c.Key) })), { name: 'Backlog', keys: d.Backlog.Cards.map(c => c.Key) }];
    },
    // settled: no write on the way, and Jira's lists have caught up
    // settled: no write on the way for a while (a queued one may not have gone out yet), and Jira's
    // lists have caught up
    settle: async (ms = LAG + 300) => {
      for (let quiet = 0; quiet < 3; quiet = writes ? 0 : quiet + 1) await sleep(100);
      await sleep(ms);
    },
    stop: () => clearInterval(poll),
  };
}

for (const [site, { flavour, lag: LAG, hold: HOLD }] of Object.entries(FLAVOURS)) test.describe(site, () => {
  test.use({ flavour });
  for (const seed of SEEDS) {
    test(`planning fuzz, seed ${seed}`, async ({ page, app }) => {
      test.setTimeout(60000 + STEPS * (4000 + REFUSE * 45000)); // a refusal waits out the hold
      const r = mulberry32(seed), any = xs => xs[Math.floor(r() * xs.length)];
      const log = [];
      const jira = await slowJira(page, LAG);
      await page.goto(new URL('#/planning', app.url).href);
      await expect(row(page, 'DEMO-17')).toBeVisible();

      const where = new Map(); // issue → the section the moves put it in
      const adopt = v => { where.clear(); for (const s of v) for (const k of s.keys) where.set(k, s.name); };
      adopt(await view(page));
      let v = [];
      const fail = msg => { throw new Error(`${msg}\nseed ${seed}, steps:\n  ${log.join('\n  ')}\nview:\n  ${v.map(s => s.name + ': ' + s.keys.join(' ')).join('\n  ')}\nfailed requests:\n  ${jira.errs.join('\n  ')}`); };
      const check = async () => {
        v = await view(page); const seen = new Map();
        for (const s of v) for (const k of s.keys) {
          if (seen.has(k)) fail(`${k} in ${seen.get(k)} and ${s.name}`);
          seen.set(k, s.name);
        }
        for (const [k, s] of where) if (seen.get(k) !== s) fail(`${k} in ${seen.get(k) || 'no section'}, moved to ${s}`);
        for (const k of seen.keys()) if (!where.has(k)) fail(`${k} showed up in ${seen.get(k)}`);
        return v;
      };
      const select = async key => {
        await row(page, key).click();
        await expect(page.locator('.iss .iss-key')).toHaveText(key);
        await page.keyboard.press('Escape');
        await expect(page.locator('.iss')).toHaveCount(0);
      };
      const pickSection = name => pick(page, name, new RegExp(esc(name)));

      // A refused write: where things went is the server's to say, once it settles.
      let refused = 0;
      const resync = async () => {
        log.push('  (refused)');
        await jira.settle(HOLD + LAG); await page.keyboard.press('R'); await sleep(800);
        refused = jira.refused();
        adopt(await view(page));
      };
      for (let step = 0; step < STEPS; step++) {
        try { await act(); } catch (e) {
          const dlg = await page.getByRole('dialog').getAttribute('aria-label').catch(() => null);
          if (!String(e.message).includes('\nseed ')) fail(`${e.message.split('\n')[0]}${dlg ? ` (dialog open: ${dlg})` : ''}`);
          throw e;
        }
      }
      async function act() {
        if (jira.refused() > refused) await resync();
        try { await check(); } catch (e) { if (jira.refused() === refused) throw e; await resync(); await check(); }
        if (process.env.FUZZ_TRACE) log.push('    ' + v.map(s => s.name.slice(0, 9) + ': ' + s.keys.join(' ')).join(' | '));
        const names = v.map(s => s.name), done = new Set(await doneKeys(page));
        // A done issue moved to the backlog is on no list after, as in Jira: planning asks first and
        // takes it out. The bulk dialog doesn't ask, so it leaves those be.
        const fits = (ks, to) => to !== 'Backlog' || !ks.some(x => done.has(x));
        const moved = async (ks, to) => {
          const out = to === 'Backlog' ? ks.filter(x => done.has(x) && where.get(x) !== 'Backlog') : [];
          if (out.length) {
            const dlg = page.getByRole('dialog');
            await expect(dlg).toContainText('Jira leaves done issues out of the backlog');
            await dlg.getByRole('button', { name: 'Move' }).click();
            await expect(dlg).toHaveCount(0);
          }
          for (const x of ks) out.includes(x) ? where.delete(x) : where.set(x, to);
        };
        const keys = v.flatMap(s => s.keys);
        const k = any(keys), from = where.get(k), at = names.indexOf(from);
        const roll = r();
        if (roll < 0.2) {
          const to = any(names);
          log.push(`${k}: m → ${to}`);
          await select(k); await page.keyboard.press('m'); await pickSection(to);
          await moved([k], to);
        } else if (roll < 0.35) {
          const d = r() < 0.5 ? -1 : 1, to = names[at + d];
          log.push(`${k}: ${d < 0 ? '[' : ']'}`);
          await select(k); await page.keyboard.press(d < 0 ? '[' : ']');
          if (to) await moved([k], to);
        } else if (roll < 0.5) {
          log.push(`${k}: ${r() < 0.5 ? 'J' : 'K'}`);
          await select(k); await page.keyboard.press(log.at(-1).slice(-1));
        } else if (roll < 0.7) {
          const t = any(keys), after = r() < 0.5;
          log.push(`${k}: drag ${after ? 'below' : 'above'} ${t}`);
          await jira.still();
        await expect(row(page, t)).toBeVisible();
          const box = await row(page, t).boundingBox();
          await row(page, k).dragTo(row(page, t), { targetPosition: { x: 40, y: after ? box.height - 4 : 4 } });
          if (t !== k) await moved([k], where.get(t));
        } else if (roll < 0.85) {
          const ks = [...new Set([k, any(keys), any(keys)].slice(0, 1 + Math.floor(r() * 3)))];
          const bulk = r() < 0.5, to = any(names.filter(n => !bulk || fits(ks, n)));
          log.push(`${ks.join(',')}: ${bulk ? 'X' : 'm'} → ${to}`);
          for (const x of ks) { await select(x); await page.keyboard.press('x'); }
          if (bulk) {
            await page.keyboard.press('X'); await pick(page, 'Sprint / backlog'); await pickSection(to);
            if (ks.length > 1) await page.getByRole('dialog').getByRole('button', { name: 'Apply' }).click();
            await expect(page.getByRole('dialog')).toHaveCount(0);
            await page.keyboard.press('Escape'); // the marks
            for (const x of ks) where.set(x, to);
          } else {
            await page.keyboard.press('m'); await pickSection(to);
            await moved(ks, to);
          }
        } else if (roll < 0.9) {
          // u takes back the last move, a bulk one too: where it all went is the server's to say
          log.push('u');
          await page.keyboard.press('u');
          await jira.settle(); await page.keyboard.press('R'); await sleep(800);
          adopt(await view(page));
        } else if (roll < 0.97) {
          log.push('R');
          await page.keyboard.press('R');
        } else {
          // a page reload drops what is queued: wait for the writes, not for Jira's lists
          log.push('reload');
          await expect.poll(async () => (await view(page)).length).toBeGreaterThan(0);
          await jira.settle();
          await page.reload();
          await expect(row(page, k)).toBeVisible();
        }
        await sleep(r() * 400);
      }

      await jira.settle(HOLD + LAG);
      if (jira.refused() > refused) await resync();
      await page.keyboard.press('R');
      await sleep(800);
      const final = await check(), truth = await jira.truth();
      jira.stop();
      for (let i = app.errors.length - 1; i >= 0; i--) if (/^400 POST .*\/api\/plan\/(move|rank)$/.test(app.errors[i])) app.errors.splice(i, 1); // refused on purpose
      if (JSON.stringify(final) !== JSON.stringify(truth)) fail(`the view is not the server's:\n view   ${JSON.stringify(final)}\n server ${JSON.stringify(truth)}`);
    });
  }
});
