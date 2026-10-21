// The work timer: T starts it on the selected issue, stops it into the log-work dialog, or
// switches it. It lives in the state file where the TUI keeps its own (GET/PUT /api/timer), so either
// stops what the other started; localStorage mirrors it for the first paint and other tabs.
import { h } from './dom.js';
import { duration } from './fmt.js';
import { logDialog, parseDuration, hm } from './worktime.js';
import { installBadge } from './inbox.js';
import * as store from './store.js';
import { T } from './i18n.js';

const MIN5 = 5 * 60000;

function load() {
  try { const t = JSON.parse(store.get('timer', 'null')); return t && t.key && t.start ? t : null; } catch (e) { return null; }
}
function save(t) {
  try { store.set('timer', t ? JSON.stringify(t) : ''); } catch (e) { /* private mode */ }
}
const same = (a, b) => (a && a.key) === (b && b.key) && (a && a.start) === (b && b.start);

// The issue `T` and `w` act on: the panel's while it has the focus, else the selected row of the view, else the
// open panel, else the route's issue.
export function target(app) {
  if (app.panel.key && app.panel.focused()) return app.panel.key;
  const row = document.querySelector('#view [data-key].sel, #view [data-key].selected, #view [data-key].focused, #view [data-key].cursor, #view [data-key] > .sel, #view [data-key].cur');
  const k = row && row.closest('[data-key]').dataset.key;
  if (k) return k;
  return app.panel.key || (app.route && ((app.route.params && app.route.params.key) || (app.route.query && app.route.query.issue))) || '';
}

export function install(app) {
  let timer = load();
  const chip = app.chrome.add(h('button.timer-chip.accent', { hidden: true, onclick: () => toggle('') }), 30);
  let tick = 0;

  const elapsed = () => Date.now() - timer.start;
  const since = () => (elapsed() < 60000 ? '<1m' : duration(Math.floor(elapsed() / 1000)));
  const label = () => [h('span.tk', timer.key), since()];
  function paint() {
    chip.hidden = !timer;
    if (timer) { chip.replaceChildren(...label()); chip.title = T('Timer on %s since %s · T stops it', timer.key, hm(new Date(timer.start))); }
    arm();
  }
  // Redraw while the tab is visible and a timer runs; nothing otherwise.
  function arm() {
    clearInterval(tick); tick = 0;
    if (timer && !document.hidden) tick = setInterval(() => { chip.replaceChildren(...label()); app.bus.emit('timer:tick'); }, 20000);
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden && timer) chip.replaceChildren(...label()); arm(); });
  window.addEventListener('storage', e => { if (e.key === store.key('timer')) { timer = load(); paint(); app.bus.emit('timer', timer); } });

  function show(t) {
    if (same(t, timer)) return;
    timer = t; save(t); paint(); app.bus.emit('timer', timer);
  }
  let gen = 0; // a sync that started before a local change must not undo it
  function set(t) {
    gen++;
    show(t);
    app.api.put('/timer', t ? { Key: t.key, Start: new Date(t.start).toISOString() } : { Key: '' })
      .catch(e => app.ui.toast(T('Timer kept in this browser only: %s', e.message), { kind: 'err' }));
  }
  // What the TUI (or another window) did: read on start, on coming back to the tab and twice a minute.
  async function sync() {
    const g = gen;
    try {
      const t = await app.api.get('/timer', { fresh: true });
      if (g !== gen) return;
      // A timer from before the state file kept it: hand it over once.
      if (!(t && t.Key) && timer && !store.get('timer.shared')) { store.set('timer.shared', '1'); return set(timer); }
      store.set('timer.shared', '1');
      show(t && t.Key ? { key: t.Key, start: Date.parse(t.Start) } : null);
    } catch (e) { /* offline: the mirror stands */ }
  }
  document.addEventListener('visibilitychange', () => { if (!document.hidden) sync(); });
  setInterval(() => { if (!document.hidden) sync(); }, 30000);

  function round(ms) {
    const step = parseDuration(String(app.prefs.get('timer_round', (app.session && app.session.ui && app.session.ui.TimerRound) || '')) || '');
    const secs = ms / 1000;
    return step > 0 ? Math.max(Math.ceil(secs / step), 1) * step : Math.max(Math.round(secs / 60) * 60, 60);
  }

  function start(key) {
    set({ key, start: Math.floor(Date.now() / 1000) * 1000 }); // the state file keeps seconds
    app.ui.toast(T('Timer started on %s', key));
  }

  // Stop into the log dialog. The timer keeps running until the work is in; `next` starts afterwards.
  async function stop(next) {
    if (!timer) return;
    const t = timer;
    const res = await logDialog(app, { key: t.key, seconds: round(Date.now() - t.start), started: new Date(t.start), discard: true, move: next, note: next && T('Then the timer starts on %s', next) });
    if (res === 'move') return move(next);
    if (res === 'discard') {
      if (Date.now() - t.start >= MIN5 && !(await app.ui.confirm({ title: T('Discard timer'), text: T('%s on %s will not be logged.', duration(Math.floor((Date.now() - t.start) / 1000)), t.key), ok: T('Discard'), danger: true }))) return;
    } else if (res !== 'logged') return;
    if (timer && timer.start === t.start) set(null);
    if (next) start(next);
  }

  // Move the running timer to key, its time along, nothing logged (the TUI's ctrl+t at the stop prompt).
  function move(key) {
    if (!timer || !key || key === timer.key) return;
    set({ key, start: timer.start });
    app.ui.toast(T('Timer moved to %s, %s on it', key, since()));
  }

  function toggle(key) {
    if (!timer) return key ? start(key) : app.ui.toast(T('No timer running · T on an issue starts one'));
    if (!key || key === timer.key) return stop();
    return stop(key);
  }

  const k = app.keys.scope('timer');
  k.bind('T', () => { const key = target(app); if (!key && !timer) return app.ui.toast(T('Select an issue first')); toggle(key); }, T('start / stop timer'), { group: 'Time', writes: true });
  k.bind('w', () => { const key = target(app); if (key) logDialog(app, { key }); else app.ui.toast(T('Select an issue first')); }, T('log work'), { group: 'Time' });

  const cmd = (id, title, run, when) => app.commands.register({ id, group: 'Time', get title() { return title(); }, run, when });
  cmd('timer:toggle', () => !timer ? T('Start timer on %s', target(app)) : target(app) && target(app) !== timer.key ? T('Switch timer to %s', target(app)) : T('Stop timer and log %s', timer.key),
    () => toggle(target(app)), () => !!timer || !!target(app));
  cmd('timer:stop', () => T('Stop timer on %s', timer.key), () => toggle(''), () => !!timer && !!target(app) && target(app) !== timer.key);
  cmd('timer:move', () => T('Move timer to %s (%s, nothing logged)', target(app), since()), () => move(target(app)), () => !!timer && !!target(app) && target(app) !== timer.key);
  cmd('worklog:add', () => T('Log work on %s', target(app)), () => logDialog(app, { key: target(app) }), () => !!target(app));

  // mark(key): "12m" (beside a timer icon) on the timed issue's card, row and panel, '' on any other. Redraw on bus 'timer' and 'timer:tick'.
  const mark = key => (timer && timer.key === key ? since() : '');
  app.timer = { get current() { return timer; }, toggle, start, stop, move, mark, elapsed: () => (timer ? elapsed() : 0) };
  paint();
  sync();
  installBadge(app);
}
