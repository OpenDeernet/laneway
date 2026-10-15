// My work: what's assigned to me, and the time I logged (day and week).
import { h, clear, delegate, debounce } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { vlist } from '../lib/vlist.js';
import { rowPx, onChange as onMetrics } from '../lib/metrics.js';
import { isZero, date as toDate, shortDate, duration } from '../lib/fmt.js';
import { ymd, addDays, weekStart, hm, dayStart, targetSeconds, workdays, logDialog } from '../lib/worktime.js';
import * as cq from '../lib/cardquery.js';
import * as pins from '../lib/pins.js';
import { check, setCheck, selBar } from '../lib/selbar.js';
import { openFilterBuilder } from './board_filter.js';
import { T, Tn } from '../lib/i18n.js';

const KEY_RE = /^[A-Z][A-Z0-9]+-\d+$/;
const today = () => { const n = new Date(); return new Date(n.getFullYear(), n.getMonth(), n.getDate()); };
const catOf = c => (c.Done ? 'done' : c.InProgress ? 'indeterminate' : 'new');
const rank = c => (c.Done ? 2 : c.InProgress ? 1 : 0);
const kids = a => a.flat().filter(Boolean);
const sum = ws => ws.reduce((n, w) => n + w.Seconds, 0);

export default function mount(el, { app, scope, toolbar, query }) {
  css('work');
  const { api, ui, bus, prefs } = app;
  let tab = ['issues', 'day', 'week'].includes(query.tab) ? query.tab : prefs.get('work_tab', 'issues');
  if (!['issues', 'day', 'week'].includes(tab)) tab = 'issues';
  let cards = [], loaded = false, filter = null, group = prefs.get('work_group', 'status'), hideDone = prefs.get('work_hide_done', 'false') === 'true';
  let rows = [], sel = -1, list = null, empty = null;
  let day = today(), logs = [], logsPath = '', wsel = 0, cell = { row: 0, col: 0 }, weekRows = [];
  const extra = new Map(); // rows + added: key → summary
  let dead = false;

  const root = h('div.work');
  el.append(root);
  const body = h('div.work-body');
  const inTab = (...t) => ({ when: () => t.includes(tab) });

  function setTab(t) {
    if (t === tab) return;
    tab = t; prefs.set('work_tab', t); app.setQuery({ tab: t });
    marks.clear(); paintToolbar(); render();
  }

  // ---- toolbar
  // The board's query language; a plain word matches the key, summary or status.
  const text = c => (c.Key + ' ' + c.Summary + ' ' + c.Status).toLowerCase();
  const me = (app.session.me && app.session.me.AccountID) || '';
  const env = () => ({ me, pins: new Set(pins.list(app).map(p => p[0])), text });
  const setFilter = t => { filter = cq.compile(t, env()); buildRows(); };
  const filterIn = h('input.input.work-filter', { type: 'search', placeholder: T('Filter (f)'), spellcheck: false, title: T('words, status:review  prio>=high  is:overdue  -type:bug  (F builds a query)'), oninput: debounce(() => setFilter(filterIn.value), 80) });
  function openBuilder() {
    if (!loaded) return;
    openFilterBuilder({ app, cards, env: env(), query: filterIn.value.trim(), apply: t => { filterIn.value = t; setFilter(t); } });
  }
  filterIn.addEventListener('keydown', e => { if (e.key === 'Escape' || e.key === 'Enter') { e.stopPropagation(); filterIn.blur(); } });
  const groupSel = h('select.input.work-group', { title: T('Group by (v)'), onchange: () => { group = groupSel.value; prefs.set('work_group', group); buildRows(); } },
    ['status', 'project', 'none'].map(g => h('option', { value: g, selected: g === group }, g === 'none' ? T('No grouping') : g === 'status' ? T('By status') : T('By project'))));
  const doneBtn = h('button.btn.ghost', { title: T('Hide done (d)'), onclick: () => toggleDone() });
  function paintToolbar() {
    clear(toolbar).append(...kids([
      h('div.seg', [['issues', T('Issues'), '1'], ['day', T('Day'), '2'], ['week', T('Week'), '3']].map(([t, n, k]) => h('button.btn' + (tab === t ? '.on' : ''), { onclick: () => setTab(t), title: k }, n))),
      tab === 'issues' && [filterIn, groupSel, doneBtn], h('span.spacer')]));
    doneBtn.textContent = hideDone ? T('Done hidden') : T('Hide done');
    doneBtn.classList.toggle('on', hideDone);
  }
  function toggleDone() { hideDone = !hideDone; prefs.set('work_hide_done', hideDone); paintToolbar(); buildRows(); }

  // ---- issues
  function buildRows() {
    const shown = cards.filter(c => (!hideDone || !c.Done) && (!filter || filter(c)));
    const by = new Map();
    for (const c of shown) {
      const g = group === 'status' ? c.Status : group === 'project' ? c.Key.split('-')[0] : '';
      (by.get(g) || by.set(g, []).get(g)).push(c);
    }
    const order = [...by.keys()];
    if (group === 'status') order.sort((a, b) => rank(by.get(a)[0]) - rank(by.get(b)[0]) || a.localeCompare(b));
    else order.sort();
    const keep = rows[sel] && rows[sel].card ? rows[sel].card.Key : null;
    rows = [];
    for (const g of order) {
      if (group !== 'none') rows.push({ head: g, count: by.get(g).length });
      for (const c of by.get(g)) rows.push({ card: c });
    }
    sel = keep ? rows.findIndex(r => r.card && r.card.Key === keep) : -1;
    if (sel < 0) sel = rows.findIndex(r => r.card);
    paintList();
  }
  const rowHeight = rowPx;
  function paintList() {
    if (!list) return;
    list.setCount(rows.length);
    empty.hidden = rows.length > 0;
    empty.textContent = cards.length ? T('Nothing matches') : loaded ? T('Nothing assigned to you') : T('Loading…');
    if (sel >= 0) list.scrollTo(sel);
  }
  function bindRow(elRow, i) {
    const r = rows[i];
    if (!r) return;
    if (r.head !== undefined) {
      elRow.className = 'vl-row wrow head';
      delete elRow.dataset.key;
      elRow.replaceChildren(h('span.wname', r.head || T('Assigned to me')), h('span.chip', r.count));
      return;
    }
    const c = r.card, t = app.timer && app.timer.current;
    const due = isZero(c.Due) ? null : toDate(c.Due);
    elRow.className = 'vl-row wrow' + (i === sel ? ' sel' : '') + (c.Done ? ' done' : '');
    elRow.dataset.key = c.Key;
    elRow.replaceChildren(...kids([
      h('span.wkey.mono', c.Key),
      h('span.wsum', c.Summary),
      t && t.key === c.Key && h('span.wtimer', { title: T('Timer running · T stops it') }, icon('timer'), ' ' + app.timer.mark(c.Key)),
      c.Flagged && h('span.wflag', { title: T('Flagged') }, icon('flag', true)),
      due && h('span.wdue' + (!c.Done && due < new Date() ? '.late' : ''), shortDate(c.Due)),
      c.Points && h('span.chip', c.Points),
      group !== 'status' && ui.statusPill(c.Status, catOf(c)),
      h('span.wprio.dim', c.Priority)]));
  }
  function move(d) {
    let i = sel + d;
    while (i >= 0 && i < rows.length && !rows[i].card) i += d;
    if (i >= 0 && i < rows.length) select(i);
  }
  function select(i) {
    const old = sel; sel = i;
    list.refresh(old); list.refresh(sel);
    list.scrollTo(sel);
    if (i > 0 && rows[i - 1].head !== undefined) list.scrollTo(i - 1);
    if (app.panel.key) app.panel.open(rows[i].card.Key);
  }
  const cur = () => (rows[sel] && rows[sel].card) || null;
  function loadWork() {
    return api.swr('/work', d => { cards = d.cards || []; loaded = true; buildRows(); }).catch(e => { loaded = true; ui.errToast(e); paintList(); });
  }

  // ---- time
  function loadLogs() {
    const s = tab === 'week' ? weekStart(day) : day, path = '/worklogs?from=' + ymd(s) + '&to=' + ymd(addDays(s, tab === 'week' ? 7 : 1));
    logsPath = path;
    return api.swr(path, d => {
      if (dead || logsPath !== path) return;
      logs = (d.worklogs || []).map(w => ({ ...w, at: new Date(w.Started) }));
      canMove = d.canMove !== false;
      if (tab !== 'issues') paintTime();
    }).catch(e => { if (!dead) ui.errToast(e); });
  }
  const dayLogs = d => logs.filter(w => ymd(w.at) === ymd(d));
  const curLog = () => dayLogs(day)[wsel];
  // Marked worklogs (x) by Key/ID: d deletes and m moves them, else the one under the cursor.
  const marks = new Set(), idOf = w => w.Key + '/' + w.ID;
  let canMove = true; // false once the site turned a move away (Server, DC)
  const targets = () => { const ws = dayLogs(day).filter(w => marks.has(idOf(w))); return ws.length ? ws : kids([curLog()]); };
  const byIssue = ws => { const m = new Map(); for (const w of ws) (m.get(w.Key) || m.set(w.Key, []).get(w.Key)).push(w.ID); return m; };
  const selbar = selBar({ clear: () => { marks.clear(); paintTime(); }, label: T('Selected worklogs'), actions: [[T('Move…'), 'm', () => moveLogs()], [T('Delete'), 'd', () => delLog()]] });
  const toggleMark = w => { if (w) marks.has(idOf(w)) ? marks.delete(idOf(w)) : marks.add(idOf(w)); };
  function markAll() {
    const ws = dayLogs(day), all = ws.length && ws.every(w => marks.has(idOf(w)));
    for (const w of ws) all ? marks.delete(idOf(w)) : marks.add(idOf(w));
    paintTime();
  }
  // Proposed worklogs from git and ui.activity (p), listed under the day's logs.
  let props = [], propsDay = '';
  const dayProps = () => (propsDay === ymd(day) ? props : []);
  const curProp = () => dayProps()[wsel - dayLogs(day).length];
  async function propose() {
    const d = day;
    ui.toast(T('Reading git and ui.activity…'));
    try {
      const r = await api.get('/worklog/proposals?day=' + ymd(d), { fresh: true });
      props = r.Items || []; propsDay = ymd(d);
      const failed = (r.Failed.length ? T(' · ui.activity failed: %s', r.Failed.join(', ')) : '') + (r.Calendar ? T(' · ui.calendar: %s', r.Calendar) : '');
      ui.toast((props.length ? T('%d proposed', props.length) : T('Nothing to propose: every session is logged')) + failed, failed ? { kind: 'err' } : undefined);
      wsel = dayLogs(d).length; paintTime();
    } catch (e) { ui.errToast(e); }
  }
  const sources = p => Object.entries(p.Sources).map(([k, n]) => n + ' ' + k).join(', '); // "3 commit, 1 calendar"
  async function logProposal() {
    const p = curProp(); if (!p) return;
    const started = new Date(p.Start);
    if (await logDialog(app, { key: p.Key, summary: (cards.find(c => c.Key === p.Key) || {}).Summary, seconds: p.Seconds, started, comment: p.Comment || '', note: T('Proposed from %s', sources(p)) }) === 'logged') {
      props = props.filter(x => x !== p); refreshSoon();
    }
  }
  function paintTime() { clear(body); tab === 'week' ? paintWeek() : paintDay(); selbar.set(tab === 'day' ? marks.size : 0); }
  function nav(label, sub) {
    return h('div.time-head',
      h('button.btn.ghost', { onclick: () => step(-1), title: T('Previous (h)'), 'aria-label': T('Previous') }, icon('chevron-left')),
      h('div.time-title', h('h2', label), h('div.dim', sub)),
      h('button.btn.ghost', { onclick: () => step(1), title: T('Next (l)'), 'aria-label': T('Next') }, icon('chevron-right')),
      h('button.btn', { onclick: goToday, title: '0' }, T('Today')),
      h('span.spacer'),
      h('button.btn', { onclick: () => addLog(), title: 'a' }, T('Log work')),
      h('button.btn', { onclick: copyText, title: 'y' }, T('Copy')));
  }
  // As the TUI: nothing after today (day) or this week (week) to step to.
  const step = n => {
    const next = addDays(day, tab === 'week' ? 7 * n : n);
    if (n > 0 && (tab === 'week' ? weekStart(next) > today() : next > today())) return ui.toast(tab === 'week' ? T('This week is the last to show') : T('Today is the last day to show'));
    day = next; reload();
  };
  const goToday = () => { day = today(); reload(); };
  function reload() { logs = []; logsPath = ''; wsel = 0; marks.clear(); paintTime(); loadLogs(); }
  function openDay(d) { day = d; tab = 'day'; paintToolbar(); reload(); }

  function paintDay() {
    const ws = dayLogs(day), total = sum(ws), target = targetSeconds(app);
    wsel = Math.min(wsel, Math.max(ws.length + dayProps().length - 1, 0));
    for (const id of marks) if (!ws.some(w => idOf(w) === id)) marks.delete(id); // deleted or moved meanwhile
    selbar.buttons[0].hidden = !canMove;
    body.append(nav(day.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'long' }), T('%s of %s', duration(total), duration(target))),
      h('div.wbar', h('div.wbar-fill' + (total >= target ? '.full' : ''), { style: { width: Math.min(100, (total / target) * 100) + '%' } })));
    if (!ws.length && !dayProps().length) { body.append(h('div.empty', logsPath ? T('Nothing logged. ') : T('Loading…'), logsPath && h('button.btn.link', { onclick: () => addLog() }, T('Log work (a)')), logsPath && h('button.btn.link', { onclick: propose }, T(' Propose from git (p)')))); return; }
    const t = h('div.wlist' + (marks.size ? '.marking' : ''));
    const chk = w => { const c = check(); setCheck(c, marks.has(idOf(w))); return c; };
    ws.forEach((w, i) => t.append(h('div.wlog' + (i === wsel ? '.sel' : '') + (marks.has(idOf(w)) ? '.mark' : ''), { dataset: { key: w.Key, i } },
      chk(w), h('span.wtime.dim', hm(w.at)), h('span.wkey.mono', w.Key),
      h('span.wsum', w.Summary, w.Comment && h('span.wcomment.dim', ' — ' + w.Comment.replace(/\s+/g, ' '))),
      h('span.wdur', duration(w.Seconds)))));
    const ps = dayProps();
    if (ps.length) {
      t.append(h('div.wprop-head.dim', T('Proposed · enter logs one')));
      ps.forEach((p, i) => t.append(h('div.wlog.wprop' + (ws.length + i === wsel ? '.sel' : ''), { dataset: { i: ws.length + i } },
        h('span.wtime.dim', '≈ ' + hm(new Date(p.Start))), h('span.wkey.mono', p.Key),
        h('span.wsum.dim', p.Comment || sources(p)), h('span.wdur', duration(p.Seconds)))));
    }
    body.append(t);
  }

  function paintWeek() {
    const s = weekStart(day), days = Array.from({ length: 7 }, (_, i) => addDays(s, i)), wd = workdays(app), target = targetSeconds(app), now = today();
    const byKey = new Map();
    for (const w of logs) {
      const r = byKey.get(w.Key) || byKey.set(w.Key, { key: w.Key, summary: w.Summary, days: days.map(() => []) }).get(w.Key);
      const di = Math.round((new Date(w.at.getFullYear(), w.at.getMonth(), w.at.getDate()) - s) / 864e5);
      if (r.days[di]) r.days[di].push(w);
    }
    for (const k of extra.keys()) if (!byKey.has(k)) byKey.set(k, { key: k, summary: extra.get(k) || (cards.find(c => c.Key === k) || {}).Summary || '', days: days.map(() => []) });
    weekRows = [...byKey.values()].sort((a, b) => a.key.localeCompare(b.key, undefined, { numeric: true }));
    cell.row = Math.min(cell.row, Math.max(weekRows.length - 1, 0));
    body.append(nav(s.toLocaleDateString(undefined, { day: 'numeric', month: 'short' }) + ' – ' + addDays(s, 6).toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric' }), T('%s this week', duration(sum(logs)))));
    const tbl = h('table.wweek');
    tbl.append(h('thead', h('tr', h('th.wissue', h('button.btn.link', { onclick: addRow, title: T('Add row (+)') }, T('+ row'))),
      days.map(d => h('th.wcol' + (wd.includes(d.getDay()) ? '' : '.off') + (ymd(d) === ymd(now) ? '.today' : ''), { onclick: () => openDay(d) },
        d.toLocaleDateString(undefined, { weekday: 'short' }), h('div.dim', d.getDate()))), h('th.wcol', T('Total')))));
    const tb = h('tbody');
    weekRows.forEach((r, ri) => tb.append(h('tr' + (ri === cell.row ? '.sel' : ''), { dataset: { key: r.key } },
      h('td.wissue', h('span.wkey.mono', r.key), h('span.wsum', r.summary)),
      r.days.map((ws, ci) => h('td.wcell' + (ri === cell.row && ci === cell.col ? '.cur' : '') + (ws.length ? '.has' : ''), { dataset: { r: ri, c: ci } }, ws.length ? duration(sum(ws)) : '')),
      h('td.wcell.tot', duration(sum(r.days.flat()))))));
    if (!weekRows.length) tb.append(h('tr', h('td', { colSpan: 9 }, h('div.empty', logsPath ? T('Nothing logged this week.') : T('Loading…')))));
    tbl.append(tb, h('tfoot', h('tr', h('td.wissue', T('Total')), days.map(d => {
      const t = sum(dayLogs(d)), past = d < now && wd.includes(d.getDay()), miss = target - t;
      return h('td.wcell.foot' + (past ? (miss > 0 ? '.short' : '.ok') : ''), t ? duration(t) : '', past && miss > 0 && h('div.miss', '−' + duration(miss)));
    }), h('td.wcell.tot', duration(sum(logs))))));
    body.append(tbl);
  }

  async function pickIssue(title = T('Log work on')) {
    const c = await ui.pick({ title, items: cards, label: c => c.Key + ' ' + c.Summary, placeholder: T('Issue key, or filter your work'),
      create: q => ({ Key: q.trim().toUpperCase(), Summary: '' }), empty: T('Type an issue key'),
      first: q => { const k = q.trim().toUpperCase(); return KEY_RE.test(k) ? cards.find(c => c.Key === k) || { Key: k, Summary: '' } : null; } });
    if (!c) return null;
    if (!KEY_RE.test(c.Key)) { ui.toast(T('Not an issue key: %s', c.Key), { kind: 'err' }); return null; }
    return c;
  }
  async function addLog(key, d) {
    const c = key ? { Key: key, Summary: extra.get(key) || (cards.find(x => x.Key === key) || {}).Summary } : await pickIssue();
    if (!c) return;
    const at = d || day;
    if (await logDialog(app, { key: c.Key, summary: c.Summary, started: ymd(at) === ymd(today()) ? undefined : dayStart(app, at) }) === 'logged') refreshSoon();
  }
  // + : the issue's row, its summary looked up in Jira when it isn't one of yours; the cursor on it, in today's
  // column when the week is this one.
  async function addRow() {
    const c = await pickIssue();
    if (!c) return;
    let summary = c.Summary;
    if (!summary) {
      try { summary = (await api.get('/issues/' + c.Key + '/card')).Summary || ''; } catch (e) { return ui.errToast(e); }
    }
    extra.set(c.Key, summary); paintTime();
    const d = Math.floor((today() - weekStart(day)) / 864e5);
    cell = { row: Math.max(0, weekRows.findIndex(r => r.key === c.Key)), col: d >= 0 && d < 7 ? d : cell.col };
    paintTime();
  }
  async function editLog() {
    const w = curLog(); if (!w) return;
    if (await logDialog(app, { key: w.Key, summary: w.Summary, seconds: w.Seconds, started: w.at, comment: w.Comment, edit: w.ID }) === 'logged') refreshSoon();
  }
  async function delLog() {
    const ws = targets(); if (!ws.length) return;
    const w = ws[0], text = ws.length > 1 ? T('%d worklogs, %s', ws.length, duration(sum(ws))) : T('%s on %s', duration(w.Seconds), w.Key) + (w.Comment ? ' — ' + w.Comment : '');
    if (!(await ui.confirm({ title: ws.length > 1 ? T('Delete worklogs') : T('Delete worklog'), text, ok: T('Delete'), danger: true }))) return;
    try {
      for (const [key, IDs] of byIssue(ws)) await api.del('/worklog/' + key, { IDs }); // the API takes one issue's at a time
      ui.toast(ws.length > 1 ? T('Deleted %d worklogs', ws.length) : T('Deleted %s on %s', duration(w.Seconds), w.Key));
    } catch (e) { ui.errToast(e); }
    for (const key of byIssue(ws).keys()) bus.emit('issue:changed', { key });
  }
  // m: the marked worklogs (else the one under the cursor) to another issue, keeping their start.
  async function moveLogs() {
    const ws = targets(); if (!ws.length) return;
    if (!canMove) return ui.toast(T("This Jira can't move worklogs"), { kind: 'err' });
    const c = await pickIssue(Tn(ws.length, 'Move %d worklog to', 'Move %d worklogs to', ws.length));
    if (!c) return;
    const from = byIssue(ws);
    try {
      for (const [key, IDs] of from) if (key !== c.Key) await api.post('/worklog/' + key + '/move', { IDs, To: c.Key });
      ui.toast(Tn(ws.length, 'Moved %d worklog to %s', 'Moved %d worklogs to %s', ws.length, c.Key));
    } catch (e) { ui.errToast(e); }
    for (const key of new Set([...from.keys(), c.Key])) bus.emit('issue:changed', { key });
  }
  // y: the day or week as the TUI copies it, a markdown table with a Total row.
  async function copyText() {
    const { markdownTable } = await import('./reports.js');
    let t;
    if (tab === 'week') {
      const s = weekStart(day), cell = ws => (ws.length ? duration(sum(ws)) : '·');
      const totals = Array.from({ length: 7 }, (_, i) => weekRows.flatMap(r => r.days[i]));
      t = markdownTable(['Issue', 'Summary', ...Array.from({ length: 7 }, (_, i) => addDays(s, i).toLocaleDateString(undefined, { weekday: 'short' }) + ' ' + addDays(s, i).getDate()), 'Total'],
        [...weekRows.map(r => [r.key, r.summary, ...r.days.map(cell), duration(sum(r.days.flat()))]), ['', 'Total', ...totals.map(cell), duration(sum(totals.flat()))]]);
    } else {
      const ws = dayLogs(day);
      t = markdownTable(['Started', 'Time', 'Issue', 'Summary', 'Comment'],
        [...ws.map(w => [hm(new Date(w.Started)), duration(w.Seconds), w.Key, w.Summary, w.Comment || '']), ['', duration(sum(ws)), 'total', '', '']]);
    }
    navigator.clipboard.writeText(t).then(() => ui.toast(T('Copied as a markdown table')), e => ui.errToast(e));
  }
  const refreshSoon = debounce(() => { if (dead) return; if (tab !== 'issues') loadLogs(); loadWork(); }, 150);

  function cellEnter() {
    const r = weekRows[cell.row]; if (!r) return;
    const d = addDays(weekStart(day), cell.col);
    addLog(r.key, d); // as the TUI: enter logs work, a day's head opens the day
  }
  function moveCell(dr, dc) {
    cell = { row: Math.min(Math.max(cell.row + dr, 0), Math.max(weekRows.length - 1, 0)), col: Math.min(Math.max(cell.col + dc, 0), 6) };
    paintTime();
  }

  // ---- layout
  function render() {
    if (list) { list.destroy(); list = null; }
    clear(root);
    if (tab === 'issues') {
      const scroller = h('div.work-scroll');
      empty = h('div.empty', { hidden: true });
      root.append(empty, scroller);
      list = vlist(scroller, { count: rows.length, rowHeight: rowHeight(), create: () => h('div'), bind: bindRow });
      paintList();
    } else {
      root.append(body, selbar.el);
      paintTime();
      loadLogs();
    }
  }

  delegate(root, 'click', '.vl-row', (e, row) => {
    const i = rows.findIndex(r => r.card && r.card.Key === row.dataset.key);
    if (i >= 0) { select(i); app.panel.open(row.dataset.key); }
  });
  delegate(root, 'click', '.wlog', (e, row) => {
    wsel = +row.dataset.i;
    if (e.target.closest('.chk') || e.ctrlKey || e.metaKey) { toggleMark(curLog()); return paintTime(); }
    paintTime(); if (row.dataset.key) app.panel.open(row.dataset.key);
  });
  delegate(root, 'dblclick', '.wlog', () => editLog());
  delegate(root, 'click', '.wcell:not(.tot):not(.foot)', (e, td) => { cell = { row: +td.dataset.r, col: +td.dataset.c }; paintTime(); });
  delegate(root, 'dblclick', '.wcell:not(.tot):not(.foot)', () => cellEnter());
  delegate(root, 'click', '.wweek td.wissue', (e, td) => { const k = td.parentNode.dataset.key; if (k) app.panel.open(k); });

  // ---- keys
  const G = { group: T('My work') };
  const I = inTab('issues'), D = inTab('day'), W = inTab('week'), TT = inTab('day', 'week');
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next'), { ...G, ...I });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous'), { ...G, ...I });
  scope.bind('Enter', () => { const c = cur(); if (c) app.panel.open(c.Key); }, T('open issue'), { ...G, ...I, bar: T('open') });
  scope.bind('f', () => filterIn.focus(), T('filter'), { ...G, ...I, bar: T('filter') });
  scope.bind('F', openBuilder, T('filter builder'), { ...G, ...I });
  scope.bind('v', () => { group = { status: 'project', project: 'none', none: 'status' }[group]; prefs.set('work_group', group); groupSel.value = group; buildRows(); }, T('group by status / project / none'), { ...G, ...I, bar: T('group') });
  scope.bind('d', toggleDone, T('hide done'), { ...G, ...I, bar: T('hide done') });
  scope.bind('r', () => { api.forget(); loadWork(); ui.toast(T('Refreshed')); }, T('refresh'), { ...G, ...I });
  scope.bind('1', () => setTab('issues'), T('issues'), { ...G, bar: T('tabs') });
  scope.bind('2', () => setTab('day'), T('worklogs of a day'), { ...G, bar: T('tabs') });
  scope.bind('3', () => setTab('week'), T('worklogs of a week'), { ...G, bar: T('tabs') });
  scope.bind('W', () => setTab(tab === 'day' ? 'week' : 'day'), T('worklogs: day / week'), G);
  scope.bind('h', () => step(-1), T('previous day / week'), { ...G, ...TT, bar: T('day / week') });
  scope.bind('l', () => step(1), T('next day / week'), { ...G, ...TT, bar: T('day / week') });
  scope.bind('0', goToday, T('today / this week'), { ...G, ...TT });
  scope.bind('y', copyText, T('copy as a markdown table'), { ...G, ...TT });
  scope.bind('a', () => addLog(), T('log work'), { ...G, ...TT, bar: T('log work') });
  scope.bind(['j', 'ArrowDown'], () => { wsel = Math.min(wsel + 1, dayLogs(day).length + dayProps().length - 1); paintTime(); }, T('next'), { ...G, ...D });
  scope.bind(['k', 'ArrowUp'], () => { wsel = Math.max(wsel - 1, 0); paintTime(); }, T('previous'), { ...G, ...D });
  scope.bind('ArrowLeft', () => step(-1), T('previous day'), { ...G, hidden: true, ...D });
  scope.bind('ArrowRight', () => step(1), T('next day'), { ...G, hidden: true, ...D });
  scope.bind('e', editLog, T('edit worklog'), { ...G, ...D, bar: T('edit') });
  scope.bind(['d', 'Delete'], delLog, T('delete worklog (the marked ones)'), { ...G, ...D });
  scope.bind('x', () => { toggleMark(curLog()); wsel = Math.min(wsel + 1, Math.max(dayLogs(day).length - 1, 0)); paintTime(); }, T('mark worklog'), { ...G, ...D, bar: T('mark') });
  scope.bind('X', markAll, T('mark every worklog of the day'), { ...G, ...D });
  scope.bind('m', moveLogs, T('move worklog (the marked ones) to another issue'), { ...G, ...D, bar: T('move') });
  scope.bind('Escape', () => { marks.clear(); paintTime(); }, T('clear marks'), { ...G, ...D, when: () => tab === 'day' && marks.size > 0 });
  scope.bind('Enter', () => { const w = curLog(); if (w) app.panel.open(w.Key); else logProposal(); }, T('open issue / log the proposed work'), { ...G, ...D });
  scope.bind('p', propose, T('propose worklogs from git commits and ui.activity'), { ...G, ...D, bar: T('propose') });
  scope.bind(['j', 'ArrowDown'], () => moveCell(1, 0), T('next row'), { ...G, ...W });
  scope.bind(['k', 'ArrowUp'], () => moveCell(-1, 0), T('previous row'), { ...G, ...W });
  scope.bind('ArrowLeft', () => moveCell(0, -1), T('previous day'), { ...G, ...W });
  scope.bind('ArrowRight', () => moveCell(0, 1), T('next day'), { ...G, ...W });
  scope.bind('Enter', cellEnter, T('log work in the cell, or open its day'), { ...G, ...W, bar: T('log') });
  scope.bind('+', addRow, T('add an issue row'), { ...G, ...W, bar: T('add row') });
  scope.bind('o', () => { const r = weekRows[cell.row]; if (r) app.panel.open(r.key); }, T('open issue'), { ...G, ...W });

  const off = [bus.on('issue:changed', refreshSoon), bus.on('focus', refreshSoon), bus.on('timer', () => list && list.refresh()), bus.on('timer:tick', () => list && list.refresh()), onMetrics(() => render())];
  paintToolbar();
  render();
  loadWork();

  return () => { dead = true; if (list) list.destroy(); off.forEach(f => f()); };
}
