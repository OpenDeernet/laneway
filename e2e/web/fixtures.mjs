import { test as base, expect } from '@playwright/test';
import { spawn } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const vitalsJS = fileURLToPath(new URL('node_modules/web-vitals/dist/web-vitals.attribution.iife.js', import.meta.url));

// start runs `laneway web -demo` on a free port, in a home of its own, and
// resolves to the URL it prints. bulk is a number of issues more in it;
// flavour 'laneway' makes it laneway-server instead of Jira Cloud.
function start(unhandled, bulk, flavour) {
  const home = mkdtempSync(path.join(tmpdir(), 'laneway-e2e-'));
  const proc = spawn(process.env.LANEWAY, ['web', '-demo', '-no-open', '-addr', '127.0.0.1:0'], {
    env: { ...process.env, HOME: home, XDG_CONFIG_HOME: path.join(home, '.config'), XDG_STATE_HOME: path.join(home, '.state'),
      XDG_CACHE_HOME: path.join(home, '.cache'), LANEWAY_DEMO_UNHANDLED: unhandled, LANEWAY_DEMO_BULK: String(bulk), LANEWAY_DEMO_FLAVOUR: flavour },
    stdio: ['ignore', 'ignore', 'pipe'],
  });
  let log = '';
  const url = new Promise((resolve, reject) => {
    proc.stderr.on('data', d => {
      log += d;
      const m = /laneway web on (\S+)/.exec(log);
      if (m) resolve(m[1]);
    });
    proc.on('exit', code => reject(new Error(`laneway web exited ${code}:\n${log}`)));
    setTimeout(() => reject(new Error(`laneway web didn't start:\n${log}`)), 15000);
  });
  // SIGINT: laneway web's own way out, so it ends as a person ends it.
  const stop = async () => {
    const exited = new Promise(r => proc.exitCode !== null ? r() : proc.on('exit', r));
    proc.kill('SIGINT');
    await Promise.race([exited, new Promise(r => setTimeout(r, 3000))]);
    rmSync(home, { recursive: true, force: true });
  };
  return { url, stop, log: () => log };
}

// watchVitals keeps the test's worst INP and CLS, over every page load, in
// vitals. INP counts interactions from 16ms (web-vitals' floor is 40).
async function watchVitals(page, vitals) {
  await page.exposeBinding('__vital', (_, m) => {
    if (m.value < (vitals[m.name]?.value ?? -1)) return;
    vitals[m.name] = m;
  });
  // One script: Playwright runs each in a scope of its own.
  await page.addInitScript(readFileSync(vitalsJS, 'utf8') + `
    const send = ({ name, value, attribution: a }) => {
      const ls = a.longestScript && a.longestScript.entry;
      window.__vital({ name, value, target: a.interactionTarget || a.largestShiftTarget || '', type: a.interactionType || '', url: location.pathname + location.search,
        // INP: where the time went, and the longest script in it
        phases: name === 'INP' ? [a.inputDelay, a.processingDuration, a.presentationDelay].map(Math.round) : undefined,
        script: ls ? [Math.round(ls.duration) + 'ms', ls.invoker, ls.sourceFunctionName, ls.sourceURL.replace(location.origin, '') + ':' + ls.sourceCharPosition].join(' ') : undefined });
    };
    webVitals.onINP(send, { reportAllChanges: true, durationThreshold: 16 });
    webVitals.onCLS(send, { reportAllChanges: true });`);
}

// test is Playwright's with app: a fresh demo, the page on its board. A
// test fails on an error in the page or a request the demo can't answer.
// Its INP and CLS go along as the attachment vitals (vitals-reporter.mjs).
// test.use({ bulk: n }) gives the demo n issues more, { flavour: 'laneway' }
// makes it laneway-server; { inp: ms } is the test's INP budget, failed on
// with INP_BUDGETS=1 (a serial run: parallel workers' contention is no
// app's); { cls: n } its CLS budget, always. Playwright presses keys faster
// than anyone types: { typing: true } keeps the guard against typing that
// missed its field (lib/keys.js), off for the rest.
export const test = base.extend({
  bulk: [0, { option: true }],
  flavour: ['', { option: true }],
  inp: [0, { option: true }],
  cls: [0, { option: true }],
  typing: [false, { option: true }],
  app: async ({ page, bulk, flavour, inp, cls, typing }, use, info) => {
    const unhandled = info.outputPath('unhandled.txt');
    const server = start(unhandled, bulk, flavour);
    const errors = [];
    const vitals = {};
    await watchVitals(page, vitals);
    if (!typing) await page.addInitScript(() => localStorage.setItem('lw:typing', 'off'));
    // CPU_SLOWDOWN=4: a slower machine's INP (Chromium only).
    if (process.env.CPU_SLOWDOWN) await (await page.context().newCDPSession(page)).send('Emulation.setCPUThrottlingRate', { rate: +process.env.CPU_SLOWDOWN });
    page.on('pageerror', e => errors.push(`pageerror: ${e.message}`));
    // A failed request is reported with its URL below, not as the console's
    // "Failed to load resource".
    page.on('console', m => {
      if (m.type() === 'error' && !m.text().startsWith('Failed to load resource')) errors.push(`console: ${m.text()}`);
    });
    page.on('response', r => { if (r.status() >= 400) errors.push(`${r.status()} ${r.request().method()} ${r.url()}`); });
    try {
      const url = await server.url;
      await use({ url, errors });
      // web-vitals reports when idle or hidden; a test can end before either.
      await page.evaluate(() => {
        Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true });
        document.dispatchEvent(new Event('visibilitychange'));
      }).catch(() => {});
      // The page first: one still loading would fail on the server gone.
      await page.close();
    } finally {
      await server.stop();
    }
    await info.attach('vitals', { body: JSON.stringify(vitals), contentType: 'application/json' });
    if (inp && process.env.INP_BUDGETS) expect(vitals.INP?.value ?? 0, `INP, on ${vitals.INP?.target}`).toBeLessThanOrEqual(inp);
    if (cls) expect(vitals.CLS?.value ?? 0, `CLS, on ${vitals.CLS?.target}`).toBeLessThanOrEqual(cls);
    let missed = '';
    try { missed = readFileSync(unhandled, 'utf8'); } catch {}
    expect(missed, 'requests the demo could not answer').toBe('');
    expect(errors, 'errors in the page').toEqual([]);
  },
});

// palette opens the palette with key, types text once its input has the
// focus, and waits for an option holding pick (text itself without one).
export async function palette(page, key, text, pick = text) {
  await page.keyboard.press(key);
  const input = page.getByRole('textbox', { name: 'Palette' });
  await expect(input).toBeFocused();
  await input.pressSequentially(text);
  await expect(page.getByRole('option', { name: pick }).first()).toBeVisible();
  return page.getByRole('option', { name: pick }).first();
}

// inLane is key's card shown in the lane named lane. A lane keeps hidden
// rows from its virtual list, so a lane's text is no proof.
export function inLane(page, lane, key) {
  return page.locator('.bd-lane', { has: page.locator('.bd-lane-name', { hasText: lane }) })
    .locator('.card:visible', { has: page.locator('.ckey', { hasText: new RegExp('^' + key + '$') }) });
}

// selectCard puts the board's cursor on key: a click opens it in the
// panel, esc (once it is open) gives the keys back to the board.
export async function selectCard(page, key) {
  await page.locator('.bd-lanes .card:visible', { has: page.locator('.ckey', { hasText: new RegExp('^' + key + '$') }) }).click();
  await expect(page.locator('.iss .iss-key')).toHaveText(key);
  await page.keyboard.press('Escape');
  await expect(page.locator('.iss')).toHaveCount(0);
}

// openIssue loads the board and opens key in the panel.
export async function openIssue(page, app, key) {
  await page.goto(app.url);
  await page.locator('.card', { hasText: key }).click();
  await expect(page.locator('.iss .iss-key')).toHaveText(key);
  await expect(page.locator('.iss .fld[data-field="status"]')).toBeVisible(); // its keys are bound by now
}

// pick waits for a picker's input to have the focus, types text and clicks
// the option named name (text itself without one).
export async function pick(page, text, name = text) {
  const input = page.locator('.pick-input');
  await expect(input).toBeFocused();
  await input.fill(text);
  await page.getByRole('option', { name }).first().click();
}

// prompt fills the focused input of a one-field dialog and presses enter.
export async function prompt(page, value) {
  const input = page.getByRole('dialog').locator('input');
  await expect(input).toBeFocused();
  await input.fill(value);
  await input.press('Enter');
}

export { expect };
