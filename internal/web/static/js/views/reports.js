// Reports: sprint charts (burndown, burnup, flow), velocity, cycle time, retro, releases.
import { h, clear } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { chart, niceTicks, timeTicks, localDay, num, percentile } from '../lib/charts.js';
import { isZero, shortDate } from '../lib/fmt.js';
import { resolve, switcher, noBoard } from './plan_ctx.js';
import { KINDS } from './report_kinds.js';
import { makeLine, label, past, statusAt, order } from './report_lines.js';
import { put } from './settings_config.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

const SPRINT_KINDS = ['burndown', 'burnup', 'cfd'];
// The reports a line changes; velocity and releases keep Jira's done.
const LINE_KINDS = ['burndown', 'burnup', 'cfd', 'cycle', 'retro'];
const CMP = 'var(--info)';
const DAY = 86400000;
const ms = t => (isZero(t) ? null : +new Date(t));
const nextDay = t => { const d = new Date(t); return new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1).getTime(); };
const dayName = t => new Date(t).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
const days = v => num(v / (DAY * 1e6)); // Go durations arrive as nanoseconds
const toDays = ns => ns / (DAY * 1e6);

// The sprint's issues as the charts count them: points, or one each when none is pointed.
// The numbers a report shows, as a markdown table for y (TUI chartTable); pipes escaped.
export function markdownTable(head, rows) {
  const esc = x => String(x == null ? '' : x).replace(/\|/g, '\\|').replace(/\s*\n\s*/g, ' ');
  const line = r => '| ' + r.map(esc).join(' | ') + ' |';
  return [line(head), line(head.map(() => '---')), ...rows.map(line)].join('\n');
}
const ymd = t => { const d = new Date(t); return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); };
// Row i of a sprint chart's table is the sprint's first day plus i (TUI chartTable).
const sampleDay = (start, i) => { const d = new Date(start); return ymd(new Date(d.getFullYear(), d.getMonth(), d.getDate() + i)); };

function burnRows(issues) {
  const pointed = issues.some(i => i.Points > 0);
  return { unit: pointed ? 'p' : T(' issues'), rows: issues.map(i => ({ p: pointed ? i.Points : 1, added: ms(i.Added), res: ms(i.Resolved), i })) };
}
// Sample times: the start, then each day's end up to now (or the sprint's end).
function sampleTimes(start, end, now) {
  const out = [start], last = Math.min(end, now);
  for (let d = localDay(start); d <= last; d = nextDay(d)) out.push(Math.min(nextDay(d), now, end + DAY));
  return out.filter((t, i, a) => i === 0 || t > a[i - 1]);
}

export default async function mount(el, { app, params, query, scope, context, toolbar }) {
  css('reports');
  let kind = KINDS.some(k => k[0] === params.kind) ? params.kind : 'burndown';
  const sc = await resolve(app, params);
  let { project, board } = sc;
  let token = 0, charts = [], cur = 0, curItems = [], weeks = 8, sprintId = Number(query.sprint) || 0;
  let compare = query.compare || ''; // a second line's column, for this visit (kept in the URL)
  let table = null; // the shown report's numbers, for y
  const body = h('div.rp');
  const doneBtn = h('button.rp-line', { type: 'button', onclick: () => pickDone() });
  const cmpBtn = h('button.rp-line.cmp', { type: 'button', onclick: () => toggleCompare() });
  const bar = h('div.rp-lines', doneBtn, cmpBtn);
  const show = (...nodes) => clear(body).append(bar, ...nodes);
  el.append(body);
  const cleanupCharts = () => { charts.forEach(c => c.destroy()); charts = []; };
  const open = key => app.panel.open(key);

  const tabs = h('div.rp-tabs', { role: 'tablist' }, KINDS.map(([id, label], i) =>
    h('button', { role: 'tab', dataset: { kind: id }, title: label + ' (' + (i + 1) + ')', onclick: () => go(id) }, label)));
  const sprintBtn = app.chrome.crumb(T('Sprint (s)'), () => pickSprint());
  sprintBtn.textContent = T('Sprint');
  const sw = switcher(app, { scope, context, project, board, scrum: true, group: T('Reports'), onPick: r => { project = r.project; board = r.board; sprintId = 0; compare = ''; go(kind); } });
  context.append(sprintBtn);
  toolbar.append(tabs);

  function go(k) {
    kind = k;
    const q = new URLSearchParams();
    if (sprintId) q.set('sprint', sprintId);
    if (compare) q.set('compare', compare);
    app.setURL('/reports/' + k + (project ? '/' + project + (board ? '/' + board.ID : '') : '') + (q.size ? '?' + q : ''));
    document.title = KINDS.find(x => x[0] === k)[1] + ' · laneway';
    load();
  }
  const step = d => { const i = KINDS.findIndex(k => k[0] === kind); go(KINDS[(i + d + KINDS.length) % KINDS.length][0]); };

  // ---- lines: done counts from ui.report_done's column for the board (Jira's resolution when unset); c sets a second.
  const doneName = () => (board && (app.session.ui.ReportDone || {})[board.ID]) || '';
  const lines = cols => {
    const first = app.session.ui.ReportBackwards === 'first';
    return { done: makeLine(cols, doneName(), first), compare: makeLine(cols, compare, first) };
  };
  const colsCache = {};
  const boardCols = async () => {
    if (!colsCache[board.ID]) colsCache[board.ID] = app.api.get('/boards/' + board.ID).then(d => (d.config && d.config.Columns) || []);
    return colsCache[board.ID];
  };
  function lineBar() {
    const on = LINE_KINDS.includes(kind) && !!board, why = on ? '' : (board ? T("Velocity and releases count Jira's done") : T('Lines need a board'));
    doneBtn.disabled = cmpBtn.disabled = !on;
    doneBtn.replaceChildren(doneName() ? T('Done: %s →', doneName()) : T('Done: Jira'), keyHint('d'));
    doneBtn.title = why || T('The column done counts from: it and those right of it (d)');
    cmpBtn.replaceChildren(compare ? T('vs %s ×', compare) : T('+ Compare'), keyHint('c'));
    cmpBtn.title = why || (compare ? T('One line again (c)') : T('Set a second line beside done, for this visit (c)'));
  }
  async function pickDone() {
    if (doneBtn.disabled) return;
    const cols = await boardCols();
    const items = [{ name: '', text: T('Jira: the resolution date') }, ...cols.map(c => ({ name: c.Name, text: c.Name + ' →' }))];
    const it = await app.ui.pick({ title: T('Count done from'), items, label: x => x.text, current: items.find(x => x.name === doneName()) });
    if (!it || it.name === doneName()) return;
    const map = { ...(app.session.ui.ReportDone || {}) };
    if (it.name) map[board.ID] = it.name; else delete map[board.ID];
    try { await put(app, 'report_done', { Value: Object.keys(map).length ? map : null }); } catch (e) { return app.ui.errToast(e); }
    app.session.ui.ReportDone = map;
    if (compare === it.name) compare = '';
    go(kind);
  }
  async function toggleCompare() {
    if (cmpBtn.disabled) return;
    if (compare) { compare = ''; return go(kind); }
    const cols = (await boardCols()).filter(c => c.Name !== doneName());
    const it = await app.ui.pick({ title: T('Compare with a line at'), items: cols, label: c => c.Name + ' →' });
    if (it) { compare = it.Name; go(kind); }
  }
  // lineQuery is the lines as /reports/cycle and /reports/retro take them.
  const lineQuery = () => (doneName() ? '&done=' + encodeURIComponent(doneName()) : '') + (compare ? '&compare=' + encodeURIComponent(compare) : '');
  // byLine says what a chart counts done by, beside its title.
  const byLine = L => h('span.rp-by', L.done ? T('done = %s →', L.done.name) : T('done = resolved'), L.compare ? T(' · vs %s →', L.compare.name) : '');

  let lastSprints = [];
  async function pickSprint() {
    if (!lastSprints.length) return;
    const s = await app.ui.pick({ title: T('Sprint'), items: lastSprints, label: x => x.Name, detail: x => x.State, selected: [] });
    if (s) { sprintId = s.ID; go(kind); }
  }

  async function load(fresh) {
    const my = ++token;
    cleanupCharts(); cur = 0;
    tabs.querySelectorAll('button').forEach(b => { const on = b.dataset.kind === kind; b.classList.toggle('on', on); b.setAttribute('aria-selected', on); });
    const sprintKind = SPRINT_KINDS.includes(kind);
    sprintBtn.hidden = !sprintKind;
    lineBar();
    sw.label(project, board);
    const needsBoard = kind !== 'cycle' && kind !== 'releases';
    if (needsBoard && !board) { show(noBoard(T('Charts'), project)); return; }
    if (!project) { show(noBoard(T('Reports'), '')); return; }
    show(h('div.loading', T('Loading…')));
    try {
      const view = await render(kind, fresh);
      if (my !== token) return;
      show(view);
    } catch (e) {
      if (my !== token) return;
      show(h('div.empty', h('h2', T('Could not load')), h('p', e.message), h('button.btn', { onclick: () => load(true) }, T('Retry'))));
    }
  }
  const get = (path, fresh) => app.api.get(path, { fresh });

  async function render(k, fresh) {
    table = null;
    const b = board && board.ID;
    switch (k) {
      case 'burndown': case 'burnup': case 'cfd': {
        const d = await get('/reports/sprint/' + b + (sprintId ? '?sprint=' + sprintId : ''), fresh);
        lastSprints = (d.Sprints || []).slice().sort((x, y) => (x.State === 'active' ? -1 : y.State === 'active' ? 1 : 0) || (+new Date(y.Start) - +new Date(x.Start)));
        if (!d.Sprint) return h('div.empty', h('h2', T('No sprint')), h('p', T('This board has no active or closed sprint yet.')));
        sprintBtn.textContent = d.Sprint.Name;
        return k === 'burndown' ? burndown(d) : k === 'burnup' ? burnup(d) : flow(d);
      }
      case 'velocity': return velocity(await get('/reports/velocity/' + b, fresh));
      case 'cycle': {
        const [d, cols] = await Promise.all([get('/reports/cycle/' + encodeURIComponent(project) + '?weeks=' + weeks + (b ? '&board=' + b + lineQuery() : ''), fresh), b ? boardCols() : []]);
        return cycleView(d, lines(cols));
      }
      case 'retro': {
        const [d, cols] = await Promise.all([get('/reports/retro/' + b + '?n=2' + lineQuery(), fresh), boardCols()]);
        return retro(d, lines(cols));
      }
      case 'releases': return releases(await get('/reports/versions/' + encodeURIComponent(project), fresh));
    }
  }

  const card = (title, ...kids) => h('section.rp-card', title && h('h3', title), ...kids);
  const stat = (v, label, cls = '') => h('div.rp-stat' + (cls ? '.' + cls : ''), h('b', v), h('span', label));
  const head = (title, sub) => h('div.rp-head', h('h2', title), h('span.rp-sub', sub));
  const noDates = sp => h('div.empty', h('h2', sp.Name), h('p', T('This sprint has no dates, so there is nothing to plot.')));

  // xEnd is where a sprint chart's time axis ends: today while the sprint runs on past its end; closed, its last
  // sample (sampleTimes: at most a day past the end).
  const xEnd = (sp, end, now) => (sp.State === 'closed' ? Math.max(end, Math.min(now, end + DAY)) : Math.max(end, now));
  function sprintHead(sp, L) {
    const s = ms(sp.Start), e = ms(sp.End);
    const el = head(sp.Name, (s && e ? shortDate(sp.Start) + ' – ' + shortDate(sp.End) : '') + (sp.State === 'closed' ? T(' · closed') : ''));
    if (L) el.append(byLine(L));
    return el;
  }
  // gapArea shades between two stepped series of [t, v], as their lines are drawn.
  const gapArea = (a, b) => {
    const pts = [];
    a.forEach(([t, v], i) => {
      const w = b[i][1];
      if (i) pts.push([t, ...pts[pts.length - 1].slice(1)]);
      pts.push([t, Math.min(v, w), Math.max(v, w)]);
    });
    return { type: 'area', pts, color: CMP, opacity: 0.18 };
  };

  function burndown({ Sprint: sp, Issues: issues = [], Columns: cols = [] }) {
    const start = ms(sp.Start), end = ms(sp.End), now = Date.now();
    if (!start || !end) return noDates(sp);
    const { unit, rows } = burnRows(issues);
    const total = rows.reduce((a, r) => a + r.p, 0);
    const L = lines(cols);
    if (total === 0) return card('', sprintHead(sp, L), h('p.dim', T('No issues in this sprint.')));
    const leftBy = (line, t) => rows.reduce((a, r) => a + ((r.added == null || r.added < t) && !past(line, r.i, t) ? r.p : 0), 0);
    const left = t => leftBy(L.done, t);
    const added = rows.reduce((a, r) => a + (r.added != null && r.added > start ? r.p : 0), 0);
    const span = Math.max(end - start, DAY), ideal = t => total * (1 - Math.min(Math.max((t - start) / span, 0), 1));
    const ts = sampleTimes(start, end, now);
    const pts = ts.map((t, i) => [t, i === 0 ? left(start) : left(t)]);
    const before = L.compare && ts.map(t => [t, leftBy(L.compare, t)]);
    const gap = before ? Math.abs(before[before.length - 1][1] - pts[pts.length - 1][1]) : 0;
    const [first, last] = order(L.done, L.compare);
    const curLeft = pts[pts.length - 1][1], idealNow = ideal(Math.min(now, end)), d = curLeft - idealNow;
    const pace = total === 0 ? '' : d >= 0.5 ? T('%s behind', num(d) + unit) : d <= -0.5 ? T('%s ahead', num(-d) + unit) : T('on track');
    const max = niceTicks(Math.max(total, ...pts.map(p => p[1]), ...(before || []).map(p => p[1]), 1));
    const wrap = h('div');
    const spec = {
      title: T('Burndown of %s', sp.Name), desc: T('%s of %s left, ideal %s. %s', num(curLeft), num(total) + unit, num(idealNow), pace),
      x: { min: start, max: xEnd(sp, end, now), ticks: timeTicks(start, xEnd(sp, end, now), 7) }, y: { max: max.max, ticks: max.ticks },
      layers: [
        { type: 'line', pts: [[start, total], [end, 0]], color: 'var(--fg-3)', dash: true, width: 1.5 },
        now > start && now < end && { type: 'vline', x: now, color: 'var(--warn)', label: T('today') },
        before && gapArea(pts, before),
        { type: 'line', pts, color: 'var(--accent)', area: !before, step: true, width: 2.5 },
        before && { type: 'line', pts: before, color: CMP, step: true, width: 2 },
        { type: 'dots', pts: pts.slice(1), color: 'var(--accent)', r: 3, opacity: 1 },
      ].filter(Boolean),
      targets: pts.map(([t, v], i) => ({ x: t, y: v, head: dayName(t), rows: [
        { color: 'var(--accent)', label: T('Left'), value: num(v) + unit, y: v },
        before && { color: CMP, label: T('Before %s', L.compare.name), value: num(before[i][1]) + unit, y: before[i][1] },
        { color: 'var(--fg-3)', label: T('Ideal'), value: num(ideal(t)) + unit, y: ideal(t) }].filter(Boolean) })),
      legend: [{ name: T('Left'), color: 'var(--accent)' }, before && { name: T('Before %s', L.compare.name), color: CMP }, { name: T('Ideal'), color: 'var(--fg-3)', dash: true }].filter(Boolean),
    };
    const view = h('div.rp-in', sprintHead(sp, L),
      h('div.rp-stats', stat(num(curLeft) + unit, T('remaining')), stat(num(total) + unit, T('in sprint')), stat(num(idealNow) + unit, now < end ? T('ideal today') : T('ideal at the end')),
        pace && stat(pace, T('pace'), d >= 0.5 ? 'bad' : d <= -0.5 ? 'ok' : ''), added > 0 && stat('+' + num(added) + unit, T('added after start'), 'warn'),
        before && stat(num(gap) + unit, T('past %s, not %s', label(first), label(last)), 'cmp')),
      card('', wrap), h('p.rp-note', lineNote(L) + T(' Left/right arrows step through the days.')));
    charts.push(chart(wrap, spec));
    table = { head: ['Day', unit === 'p' ? 'Points left' : 'Issues left', ...(before ? ['Before ' + L.compare.name] : [])],
      rows: pts.map(([, v], i) => [sampleDay(start, i), num(v), ...(before ? [num(before[i][1])] : [])]) };
    return view;
  }
  // lineNote says what counts as done, for the note under a chart.
  function lineNote(L) {
    const done = L.done ? T('Done counts from %s and the columns right of it.', L.done.name) : T("Done is Jira's: the resolution date; d picks a column instead.");
    if (!L.done && !L.compare) return done;
    return done + (app.session.ui.ReportBackwards === 'first' ? T(' An issue moved back still counts.') : T(' An issue moved back stops counting.'));
  }

  function burnup({ Sprint: sp, Issues: issues = [], Columns: cols = [] }) {
    const start = ms(sp.Start), end = ms(sp.End), now = Date.now();
    if (!start || !end) return noDates(sp);
    const { unit, rows } = burnRows(issues);
    const L = lines(cols);
    // [scope, done, past the compared line] at t.
    const at = t => {
      let s = 0, dn = 0, c = 0;
      for (const r of rows) {
        if (r.added != null && r.added >= t) continue;
        s += r.p;
        if (past(L.done, r.i, t)) dn += r.p;
        if (L.compare && past(L.compare, r.i, t)) c += r.p;
      }
      return [s, dn, c];
    };
    const ts = sampleTimes(start, end, now), data = ts.map(t => [t, ...at(t)]);
    const top = niceTicks(Math.max(...data.map(d => d[1]), 1)), [, s, dn, cn] = data[data.length - 1];
    const doneS = data.map(d => [d[0], d[2]]), cmpS = L.compare && data.map(d => [d[0], d[3]]);
    const [first, last] = order(L.done, L.compare);
    const wrap = h('div');
    charts.push(chart(wrap, {
      title: T('Burnup of %s', sp.Name), desc: T('%s of %s done', num(dn), num(s) + unit) + (cmpS ? T(', %s past %s', num(cn) + unit, L.compare.name) : ''),
      x: { min: start, max: xEnd(sp, end, now), ticks: timeTicks(start, xEnd(sp, end, now), 7) }, y: { max: top.max, ticks: top.ticks },
      layers: [
        now > start && now < end && { type: 'vline', x: now, color: 'var(--warn)', label: T('today') },
        { type: 'line', pts: data.map(d => [d[0], d[1]]), color: 'var(--fg-3)', dash: true, step: true },
        cmpS && gapArea(doneS, cmpS),
        { type: 'line', pts: doneS, color: 'var(--ok)', area: !cmpS, step: true, width: 2.5 },
        cmpS && { type: 'line', pts: cmpS, color: CMP, step: true, width: 2 },
      ].filter(Boolean),
      targets: data.map(d => ({ x: d[0], y: d[2], head: dayName(d[0]), rows: [{ color: 'var(--ok)', label: T('Done'), value: num(d[2]) + unit, y: d[2] },
        cmpS && { color: CMP, label: T('Past %s', L.compare.name), value: num(d[3]) + unit, y: d[3] },
        { color: 'var(--fg-3)', label: T('Scope'), value: num(d[1]) + unit, y: d[1] }].filter(Boolean) })),
      legend: [{ name: T('Done'), color: 'var(--ok)' }, cmpS && { name: T('Past %s', L.compare.name), color: CMP }, { name: T('Scope'), color: 'var(--fg-3)', dash: true }].filter(Boolean),
    }));
    table = { head: ['Day', 'Scope', 'Done', ...(cmpS ? ['Past ' + L.compare.name] : [])], rows: data.map((d, i) => [sampleDay(start, i), num(d[1]), num(d[2]), ...(cmpS ? [num(d[3])] : [])]) };
    return h('div.rp-in', sprintHead(sp, L), h('div.rp-stats', stat(num(dn) + unit, T('done')), cmpS && stat(num(cn) + unit, T('past %s', L.compare.name), 'cmp'),
      cmpS && stat(num(Math.abs(cn - dn)) + unit, T('past %s, not %s', label(first), label(last)), 'cmp'), stat(num(s) + unit, T('scope'))),
      card('', wrap), h('p.rp-note', lineNote(L)));
  }

  const FLOW = ['var(--cat-new)', 'var(--info)', 'var(--warn)', 'var(--cat-indeterminate)', 'var(--accent)'];
  function flow({ Sprint: sp, Issues: issues = [], Columns: cols = [] }) {
    const start = ms(sp.Start), end = ms(sp.End), now = Date.now();
    if (!start || !end) return noDates(sp);
    const L = lines(cols);
    const colOf = {};
    cols.forEach((c, i) => (c.StatusIDs || []).forEach(id => { colOf[id] = i; }));
    const color = i => (i === cols.length - 1 ? 'var(--cat-done)' : i === 0 ? FLOW[0] : FLOW[1 + ((i - 1) % 3)]);
    const data = sampleTimes(start, end, now).map(t => {
      const counts = cols.map(() => 0);
      for (const is of issues) {
        const a = ms(is.Added); if (a != null && a >= t) continue;
        const c = colOf[statusAt(is, t - 1000)]; if (c != null) counts[c]++;
      }
      return [t, counts];
    });
    const top = niceTicks(Math.max(...data.map(d => d[1].reduce((a, b) => a + b, 0)), 1));
    const layers = [];
    let lows = data.map(() => 0);
    for (let k = cols.length - 1; k >= 0; k--) {
      const his = data.map((d, i) => lows[i] + d[1][k]);
      layers.push({ type: 'area', pts: data.map((d, i) => [d[0], lows[i], his[i]]), color: color(k), opacity: 0.85 });
      lows = his;
    }
    // A line is the edge between its column's band and the one before: the issues in it and right of it.
    const marks = [L.done, L.compare].filter(Boolean);
    for (const l of marks) layers.push({ type: 'line', pts: data.map(d => [d[0], d[1].slice(l.at).reduce((a, b) => a + b, 0)]), color: l === L.compare ? CMP : 'var(--fg)', width: 2, dash: true });
    const wrap = h('div');
    charts.push(chart(wrap, {
      title: T('Cumulative flow of %s', sp.Name), desc: T('Issues per board column, day by day.'),
      x: { min: start, max: xEnd(sp, end, now), ticks: timeTicks(start, xEnd(sp, end, now), 7) }, y: { max: top.max, ticks: top.ticks },
      layers,
      targets: data.map(d => ({ x: d[0], head: dayName(d[0]), rows: cols.map((c, k) => ({ color: color(k), label: c.Name, value: String(d[1][k]) })).reverse() })),
      legend: [...cols.map((c, k) => ({ name: c.Name, color: color(k) })).reverse(), ...marks.map(l => ({ name: l === L.compare ? T('vs %s →', l.name) : T('done %s →', l.name), color: l === L.compare ? CMP : 'var(--fg)', dash: true }))],
    }));
    table = { head: ['Day', ...cols.map(c => c.Name)], rows: data.map((d, i) => [sampleDay(start, i), ...d[1]]) };
    return h('div.rp-in', sprintHead(sp, L.done || L.compare ? L : null), card(T('Issues per column'), wrap));
  }

  function velocity(vel) {
    if (!vel.length) return h('div.empty', h('h2', T('Velocity')), h('p', T('No closed sprints yet.')));
    const avg = vel.reduce((a, v) => a + v.Done, 0) / vel.length;
    const top = niceTicks(Math.max(...vel.map(v => Math.max(v.Done, v.Committed)), 1));
    const abbr = n => { const m = n.match(/(\d+)\D*$/); return m ? '#' + m[1] : n.slice(0, 8); };
    const every = Math.ceil(vel.length / Math.max(Math.floor(((el.clientWidth || 800) - 100) / 48), 1));
    const items = [];
    vel.forEach((v, i) => {
      items.push({ x: i - 0.21, y: v.Committed, color: 'var(--cat-new)', opacity: 0.5 }, { x: i + 0.21, y: v.Done, color: 'var(--ok)' });
    });
    const wrap = h('div');
    charts.push(chart(wrap, {
      title: T('Velocity'), desc: T('Committed and completed points of the last %d sprints, average %s completed.', vel.length, num(avg)),
      x: { min: -0.6, max: vel.length - 0.4, ticks: vel.map((v, i) => ({ v: i, label: abbr(v.Name) })).filter((_, i) => i % every === 0) }, y: { max: top.max, ticks: top.ticks },
      layers: [{ type: 'bars', items, bw: 0.4 }, { type: 'hline', y: avg, color: 'var(--accent)', label: T('avg %sp', num(avg)) }],
      targets: vel.map((v, i) => ({ x: i, head: v.Name, rows: [{ color: 'var(--ok)', label: T('Completed'), value: num(v.Done) + 'p' }, { color: 'var(--cat-new)', label: T('Committed'), value: num(v.Committed) + 'p' }, { label: T('Closed'), value: shortDate(v.End) }] })),
      legend: [{ name: T('Completed'), color: 'var(--ok)' }, { name: T('Committed'), color: 'var(--cat-new)' }, { name: T('Average'), color: 'var(--accent)', dash: true }],
    }));
    const last = vel[vel.length - 1];
    table = { head: ['Sprint', 'Committed', 'Done'], rows: vel.map(v => [v.Name, num(v.Committed), num(v.Done)]) };
    return h('div.rp-in', head(T('Velocity'), T('last %d sprints', vel.length)),
      h('div.rp-stats', stat(num(avg) + 'p', T('average completed')), stat(num(last.Done) + 'p', T('last sprint: %s', last.Name)),
        stat(Math.round(vel.reduce((a, v) => a + v.Done, 0) / Math.max(vel.reduce((a, v) => a + v.Committed, 0), 1) * 100) + '%', T('of committed, completed'))),
      card('', wrap));
  }

  // cycleView: with a second line the cycle runs to the line further right and splits at the other (the server counts).
  function cycleView(list, L) {
    list = list || [];
    const now = Date.now(), from = now - weeks * 7 * DAY;
    const split = !!L.compare;
    const [first, last] = split ? order(L.done, L.compare) : [null, L.done];
    const end = label(last);
    const cyc = list.filter(i => i.Cycle > 0), cycD = cyc.map(i => toDays(i.Cycle)), leadD = list.map(i => toDays(i.Lead));
    const actD = cyc.map(i => toDays(i.Cycle - i.Wait)), waitD = cyc.map(i => toDays(i.Wait));
    const weeksBtn = h('button.btn', { title: T('Weeks (W)'), onclick: nextWeeks }, Tn(weeks, '%d week', '%d weeks', weeks), keyHint('W'));
    const sub = T('in progress to %s', end);
    const title = () => { const el = head(T('Cycle time'), sub); if (L.done || L.compare) el.append(byLine(L)); return el; };
    if (!list.length) return h('div.rp-in', title(), h('div.empty', T('Nothing got to %s in the last %d weeks.', end, weeks)), weeksBtn);
    const p50 = percentile(cycD, 50), p85 = percentile(cycD, 85);
    const top = niceTicks(Math.max(...cycD, p85 * 1.2, 1));
    const wrap = h('div');
    const bw = (now - from) / 160, t = i => +new Date(i.Resolved);
    const marks = split
      ? [{ type: 'bars', bw, items: cyc.flatMap(i => [{ x: t(i), y: toDays(i.Cycle - i.Wait), color: 'var(--accent)' }, { x: t(i), y0: toDays(i.Cycle - i.Wait), y: toDays(i.Cycle), color: CMP }]) }]
      : [{ type: 'dots', pts: cyc.map(i => [t(i), toDays(i.Cycle)]), color: 'var(--accent)', r: 4 }];
    charts.push(chart(wrap, {
      title: T('Cycle time'), desc: T('%d issues to %s; half within %s days, 85%% within %s.', cyc.length, end, num(p50), num(p85)), nearest: 'xy',
      x: { min: from, max: now, ticks: timeTicks(from, now, 7) }, y: { max: top.max, ticks: top.ticks, fmt: v => num(v) + 'd' },
      layers: [
        { type: 'hline', y: p50, color: 'var(--ok)', label: '50% ' + num(p50) + 'd' }, { type: 'hline', y: p85, color: 'var(--warn)', label: '85% ' + num(p85) + 'd' },
        ...marks,
      ],
      targets: cyc.map(i => ({ x: t(i), y: toDays(i.Cycle), head: i.Key + ' ' + i.Summary.slice(0, 50), onclick: () => open(i.Key),
        rows: [{ label: T('Cycle'), value: T('%s days', days(i.Cycle)), color: 'var(--accent)', y: toDays(i.Cycle) },
          ...(split ? [{ label: T('To %s', label(first)), value: T('%s days', days(i.Cycle - i.Wait)), color: 'var(--accent)' }, { label: T('%s to %s', label(first), end), value: T('%s days', days(i.Wait)), color: CMP }] : []),
          { label: T('Lead'), value: T('%s days', days(i.Lead)) }, { label: T('Done'), value: dayName(t(i)) }] })),
      legend: split ? [{ name: T('In progress to %s', label(first)), color: 'var(--accent)' }, { name: T('%s to %s', label(first), end), color: CMP }] : undefined,
    }));
    table = { head: ['Issue', 'Done', 'Cycle days', 'Lead days', ...(split ? ['Waiting days'] : [])],
      rows: list.map(i => [i.Key, ymd(t(i)), i.Cycle > 0 ? days(i.Cycle) : '', days(i.Lead), ...(split ? [days(i.Wait)] : [])]) };
    const slow = cyc.slice().sort((a, b) => b.Cycle - a.Cycle).slice(0, 8);
    curItems = slow.map(i => i.Key);
    return h('div.rp-in', title(),
      h('div.rp-stats', stat(num(p50) + 'd', T('cycle, 50%')), stat(num(p85) + 'd', T('cycle, 85%')),
        split && stat(num(percentile(actD, 50)) + 'd', T('to %s, 50%', label(first))), split && stat(num(percentile(waitD, 50)) + 'd', T('%s to %s, 50%', label(first), end), 'cmp'),
        split && stat(num(percentile(waitD, 85)) + 'd', T('%s to %s, 85%', label(first), end), 'cmp'),
        stat(num(percentile(leadD, 50)) + 'd', T('lead, 50%')), stat(num(percentile(leadD, 85)) + 'd', T('lead, 85%')), stat(list.length, T('done')), weeksBtn),
      card('', wrap),
      card(T('Slowest'), h('ul.rp-list', slow.map((i, n) => h('li', { class: n === 0 ? 'cur' : '', dataset: { key: i.Key }, onclick: () => open(i.Key) }, h('span.key', i.Key), h('span.sum', i.Summary),
        h('span.dim', T('%s days', days(i.Cycle)) + (split ? T(', %s days after %s', days(i.Wait), label(first)) : '')))))),
      h('p.rp-note', T('Lead time runs from created to %s; cycle time from first in progress.', end) + (split ? T(' The cycle runs to the line further right; the bar changes colour at the other.') : '')));
  }
  function nextWeeks() { const w = [4, 8, 12, 26]; weeks = w[(w.indexOf(weeks) + 1) % w.length]; load(); }

  function retro({ Sprints: rs = [], Cards: cards = {} }, L) {
    if (!rs.length) return h('div.empty', h('h2', T('Retro')), h('p', T('No closed sprints yet.')));
    const last = rs[rs.length - 1], n = k => String((k || []).length);
    const [left, right] = order(L.done, L.compare), between = L.compare && T('Past %s, not %s', label(left), label(right));
    const metric = [[T('Committed'), r => n(r.Committed)], [T('Added during'), r => n(r.Added)], [T('Done'), r => n(r.Done)], [T('Carried over'), r => n(r.Carried)], [T('Moved backwards'), r => n(r.Back)], [T('Points done'), r => T('%s of %s', num(r.DonePoints), num(r.Points))],
      ...(between ? [[between, r => n(r.Between)]] : [])];
    const list = (title, keys, cls) => keys && keys.length ? card(title + ' (' + keys.length + ')', h('ul.rp-list', keys.map(k => {
      const c = cards[k] || {};
      return h('li', { dataset: { key: k }, onclick: () => open(k) }, h('span.key', k), h('span.sum', c.Summary || ''), c.Status && h('span.dim', c.Status));
    }))) : null;
    curItems = [...(last.Carried || []), ...(last.Done || []), ...(between ? last.Between || [] : [])];
    table = { head: ['', ...rs.map(r => r.Name)], rows: metric.map(([label, f]) => [label, ...rs.map(f)]) };
    const top = head(T('Retro: %s', last.Name), shortDate(last.Start) + ' – ' + shortDate(last.End));
    if (L.done || L.compare) top.append(byLine(L));
    return h('div.rp-in', top,
      card('', h('table', h('thead', h('tr', h('th', ''), rs.map(r => h('th.n', r.Name)))),
        h('tbody', metric.map(([label, f]) => h('tr', h('td', label), rs.map(r => h('td.n', f(r)))))))),
      h('div.rp-grid', list(T('Shipped'), last.Done), list(T('Slipped'), last.Carried), between && list(between, last.Between), list(T('Added during the sprint'), last.Added), list(T('Moved backwards'), last.Back)));
  }

  function releases(vs) {
    const list = vs.filter(v => !v.Archived);
    if (!list.length) return h('div.empty', h('h2', T('Releases')), h('p', T('%s has no versions.', project)));
    const open_ = list.filter(v => !v.Released), shipped = list.filter(v => v.Released);
    curItems = [...open_, ...shipped];
    table = { head: ['Version', 'Release date', 'Released', 'Done', 'Issues'], rows: curItems.map(v => [v.Name, v.ReleaseDate || '', v.Released ? 'yes' : 'no', v.Done, v.Total]) };
    const row = v => {
      const pct = v.Total ? Math.round(v.Done / v.Total * 100) : 0;
      const today = new Date().toISOString().slice(0, 10), late = !v.Released && v.ReleaseDate && v.ReleaseDate < today;
      return h('div.rp-ver' + (v.Released ? '.released' : ''), { role: 'listitem', dataset: { id: v.ID }, tabindex: -1, title: T('Enter or double-click: its issues on the board'), onclick: () => { cur = curItems.indexOf(v); mark(); }, ondblclick: () => openVersion(v) },
        h('span.name', v.Name), h('span' + (late ? '.dim' : '.faint'), { style: late ? { color: 'var(--err)' } : null }, v.ReleaseDate || (v.Released ? T('released') : T('no date'))),
        h('div.rp-bar', { role: 'progressbar', 'aria-valuenow': pct, 'aria-valuemin': 0, 'aria-valuemax': 100, 'aria-label': T('%s %d%% done', v.Name, pct) }, h('i', { style: { width: pct + '%' } })),
        h('span.cnt', v.Done + '/' + v.Total,
          !v.Released && h('button.btn.link', { title: T('Release (r)'), onclick: e => { e.stopPropagation(); release(v); } }, T('release'))));
    };
    const view = h('div.rp-in', head(T('Releases'), project),
      card(T('Unreleased'), h('div', { role: 'list' }, open_.length ? open_.map(row) : h('p.dim', T('Everything is released.')))),
      shipped.length ? card(T('Released'), h('div', { role: 'list' }, shipped.map(row))) : null);
    queueMicrotask(mark);
    return view;
  }
  function mark() {
    body.querySelectorAll('.rp-ver, .rp-list li').forEach(n => n.classList.remove('cur'));
    const k = curItems[cur];
    const n = k && (typeof k === 'string' ? body.querySelector(`.rp-list li[data-key="${k}"]`) : body.querySelector(`.rp-ver[data-id="${k.ID}"]`));
    if (n) { n.classList.add('cur'); n.scrollIntoView({ block: 'nearest' }); }
  }
  // A version's issues as a view of the board (TUI releases.go).
  function openVersion(v) {
    const q = new URLSearchParams({ sprint: 'jql:fixVersion = ' + v.ID + ' ORDER BY status, rank', vname: T('Release: %s', v.Name) });
    app.go('/board/' + encodeURIComponent(project) + '?' + q);
  }
  async function release(v) {
    if (!v || v.Released) return;
    if (!await app.ui.confirm({ title: T('Release %s', v.Name), text: v.Total - v.Done ? Tn(v.Total - v.Done, '%d issue is not done yet. Release it today anyway?', '%d issues are not done yet. Release it today anyway?', v.Total - v.Done) : T('Release it today?'), ok: T('Release') })) return;
    try { await app.api.post('/reports/versions/' + encodeURIComponent(v.ID) + '/release', {}); app.ui.toast(T('%s released', v.Name), { kind: 'ok' }); load(true); } catch (e) { app.ui.errToast(e); }
  }

  // ---- keys
  scope.bind(['l', ']', 'ArrowRight'], () => step(1), T('next report'), { group: T('Reports') });
  scope.bind(['h', '[', 'ArrowLeft'], () => step(-1), T('previous report'), { group: T('Reports') });
  KINDS.forEach(([id], i) => scope.bind(String(i + 1), () => go(id), T('report %d', i + 1), { hidden: true }));
  scope.bind('s', () => { if (SPRINT_KINDS.includes(kind)) pickSprint(); }, T('pick sprint'), { group: T('Reports'), bar: T('sprint') });
  scope.bind('W', () => { if (kind === 'cycle') nextWeeks(); }, T('cycle time: weeks'), { group: T('Reports') });
  scope.bind('d', () => pickDone(), T('the column done counts from'), { group: T('Reports'), bar: T('done') });
  scope.bind('c', () => toggleCompare(), T('a second line beside done (again: remove it)'), { group: T('Reports'), bar: T('compare') });
  scope.bind('R', () => load(true), T('reload'), { group: T('Reports') });
  scope.bind('y', () => {
    if (!table || !table.rows.length) return app.ui.toast(T('Nothing to copy here'));
    navigator.clipboard.writeText(markdownTable(table.head, table.rows)).then(() => app.ui.toast(T('Copied the numbers as a markdown table'), { kind: 'ok' }), e => app.ui.errToast(e));
  }, T('copy the numbers as a markdown table'), { group: T('Reports') });
  const move = d => { if (curItems.length) { cur = Math.min(Math.max(cur + d, 0), curItems.length - 1); mark(); } };
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next item'), { group: T('Reports') });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous item'), { group: T('Reports') });
  scope.bind('Enter', () => { const k = curItems[cur]; if (typeof k === 'string') open(k); else if (k && kind === 'releases') openVersion(k); }, T('open issue (a release: its issues on the board)'), { group: T('Reports'), bar: T('open') });
  scope.bind('r', () => { if (kind === 'releases') release(curItems[cur]); }, T('release the version'), { group: T('Reports') });

  await load();
  return () => { token++; cleanupCharts(); };
}
