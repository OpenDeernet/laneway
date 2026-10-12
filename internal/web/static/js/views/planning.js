// Planning: the backlog and the open sprints as stacked, collapsible sections.
// One scroller per pane, rows of known height, only the visible ones in the DOM; | splits off a second pane.
import { h, clear, frame, delegate } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { rowPx, px14, onChange as onMetrics } from '../lib/metrics.js';
import { isZero, shortDate } from '../lib/fmt.js';
import { resolve, switcher, noBoard } from './plan_ctx.js';
import { openFilterBuilder } from './board_filter.js';
import * as cq from '../lib/cardquery.js';
import * as pins from '../lib/pins.js';
import { whoOf, passesWho, whoKey, whoLabel, pickWho as pickPeople } from '../lib/who.js';
import { comparators, sortCards, cmpOf } from '../lib/cardsort.js';
import { selBar } from '../lib/selbar.js';
import { COLS, SORTS, gridCols, fixCols, nextSort, listHead, paintHead, pickCols as pickListCols, dragCols, resizeCols, readWidths, buildRow, fillCells } from '../lib/cardlist.js';
import { goDate } from '../lib/godate.js';
import { T, Tn } from '../lib/i18n.js';

const pts = c => Number(c.Points) || 0;
const fmtP = n => String(Math.round(n * 10) / 10);
const isoDay = t => { const d = new Date(t); return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); };
const plusDays = n => { const d = new Date(); d.setDate(d.getDate() + n); return isoDay(d); };
const cat = c => (c.Done ? 'done' : c.InProgress ? 'indeterminate' : 'new');
const PLAN_COLS = ['mark', 'key', 'summary', 'status', 'points', 'assignee', 'epic'];

function nextSprintName(name) {
  const m = (name || '').match(/^(.*?)(\d+)(\D*)$/);
  return m ? m[1] + (Number(m[2]) + 1) + m[3] : '';
}

// sprintCheer is what a completed sprint adds to its toast with ui.delight: its points and how they rank
// among the last closed sprints, oldest first (TUI sprintCheer); '' with too few.
export function sprintCheer(vel) {
  if (!vel || vel.length < 2) return '';
  const last = vel[vel.length - 1];
  if (!(last.Done > 0)) return '';
  const rank = 1 + vel.slice(0, -1).filter(v => v.Done > last.Done).length;
  const pts = String(Math.round(last.Done * 10) / 10);
  if (rank === 1) return T('%sp, best of the last %d', pts, vel.length);
  if (rank <= 3) return T('%sp, #%d of the last %d', pts, rank, vel.length);
  return T('%sp done', pts);
}

export default async function mount(el, { app, params, scope, context, toolbar }) {
  css('board'); css('planning');
  const sc = await resolve(app, params);
  let { project, board } = sc;
  switcher(app, { scope, context, project, board, group: T('Planning'), onPick: r => app.go('/planning/' + r.project + (r.board ? '/' + r.board.ID : '')) });
  if (!board) { el.append(noBoard(T('Planning'), project)); return; }

  const caps = (app.session.ui && app.session.ui.Capacity) || {};
  let data = null, sections = [], cur = '', anchor = '', filter = '', who = null, mine = false, velAvg = 0, velN = 0, columns = [];
  const sel = new Set(), folded = new Set();
  app.marked = () => [...sel];
  let ROW = 32, drag = null, token = 0, writing = 0, lastWrite = 0;

  // A pane is one virtual list of sections. The split (|) adds a second that keeps one section in
  // view (side: its id, kept per board) and leaves it out of the first.
  const sideKey = 'planning.split.' + board.ID;
  let side = app.prefs.get(sideKey, '');
  side = side === '' ? null : Number(side);
  // A phone has no room for two panes: there the split waits, and the list shows every section.
  const wide = matchMedia('(min-width: 800px)');
  const sideOn = () => side != null && wide.matches;
  function makePane(secs) {
    const scroller = h('div.pl-scroll', { tabindex: -1 }), space = h('div.pl-space'), dropEl = h('div.pl-drop', { hidden: true });
    space.append(dropEl); scroller.append(space);
    const el = h('div.pl-pane', scroller);
    const p = { el, head: null, scroller, space, dropEl, live: new Map(), rows: [], tops: [], total: 0, last: '', sb: -1, secs };
    p.paint = frame(() => paintPane(p));
    return p;
  }
  // The list's columns (lib/cardlist.js, as the board's list), kept for planning apart from the board's.
  const UI = app.session.ui || {};
  const allCols = () => [...Object.keys(COLS), ...(UI.CustomFields || []).map(n => 'x:' + n)];
  let cols = fixCols(String(app.prefs.get('planning.cols', PLAN_COLS.join(','))).split(',').filter(c => allCols().includes(c)));
  const DF = UI.DateFormat || '';
  const pinned = new Set(pins.list(app).map(p => p[0]));
  const cellCtx = {
    marked: k => sel.has(k), pinned: k => pinned.has(k),
    hl: k => (app.highlights && app.highlights.has(k) ? app.highlights.get(k) : null), tmark: k => tmark(k),
    fdate: (t, f) => (DF ? goDate(t, DF) : f), stamp: (e, k) => app.agents && app.agents.stamp(e, k),
  };
  // Each pane's sort: 'rank' (the board's order, which J/K and dropping between issues change) or a column,
  // within each section; kept per board, the split's apart.
  let statusAt = new Map();
  const CMP = comparators(c => { const i = statusAt.get(String(c.StatusID)); return i == null ? columns.length : i; });
  function readSort(p, key) {
    p.sortKey = key;
    const [s, d] = String(app.prefs.get(key, 'rank:1')).split(':');
    p.sort = s === 'rank' || cmpOf(CMP, s) ? s : 'rank'; p.dir = d === '-1' ? -1 : 1;
  }
  const sorted = p => p.sort !== 'rank';
  const left = makePane(() => sections.filter(s => !sideOn() || s.id !== side));
  const right = makePane(() => sections.filter(s => sideOn() && s.id === side));
  readSort(left, 'planning.sort.' + board.ID); readSort(right, 'planning.sort.split.' + board.ID);
  const panes = [left, right];
  const allRows = () => panes.flatMap(p => p.rows);
  app.listed = () => allRows().filter(r => r.k === 'c').map(r => r.c);
  const sidePick = h('select.input.pl-side-pick', { 'aria-label': T('Section kept in view'), onchange: () => setSide(Number(sidePick.value)) });
  const sideEl = h('div.pl-side', h('div.pl-side-bar', sidePick, h('button.btn.ghost', { title: T('Close the split (|)'), 'aria-label': T('Close the split'), onclick: () => setSide(null) }, icon('x'))), right.el);
  sideEl.hidden = !sideOn();
  const bar = selBar({ edit: () => { const ks = targets(); if (ks.length) app.actions.bulk(ks); }, clear: () => clearSel() });
  const root = h('div.pl.pl-panes', left.el, sideEl, bar.el);
  el.append(root);

  const filterIn = h('input.input.pl-filter', { type: 'search', placeholder: T('Filter  f'), 'aria-label': T('Filter issues'), title: T('words, status:review  points>2  is:mine  -label:ui  (F builds a query)'), oninput: () => setFilter(filterIn.value) });
  // The board's filter chips: Mine, Assignee, its quick filters (Jira narrows the issues: ?jql=), clear.
  const chips = h('div.bd-chips.pl-chips');
  const qf = new Set();
  delegate(chips, 'click', 'button', (e, b) => {
    if (b.dataset.qf) toggleQF(Number(b.dataset.qf));
    else if (b.dataset.act === 'mine') toggleMine();
    else if (b.dataset.act === 'who') pickWho();
    else clearFilters();
  });
  const colsBtn = h('button.btn.ghost', { title: T('List columns'), onclick: () => pickCols() }, icon('columns-3'), T('Columns'));
  const splitBtn = h('button.btn.ghost.pl-split', { title: T('Keep a sprint in view beside the list (|)'), onclick: () => setSide(side == null ? defaultSide() : null) }, T('Split'));
  toolbar.append(filterIn, chips, h('span.spacer'), colsBtn, splitBtn, h('button.btn.nw', { title: T('New sprint (N)'), onclick: () => newSprint() }, T('+ Sprint')));

  // ---- data
  const secOf = id => sections.find(s => s.id === id);
  function build(d, jql) {
    data = d; columns = d.Columns || [];
    for (const id of qf) if (!qfs().some(q => q.ID === id)) qf.delete(id);
    renderChips();
    statusAt = new Map();
    columns.forEach((c, i) => (c.StatusIDs || []).forEach(id => statusAt.set(String(id), i)));
    // rest: the issues the card limit left in Jira, which moves leave be.
    const sec = (id, sprint, name, cards, total) => ({ id, sprint, name, cards, rest: Math.max((total || 0) - cards.length, 0) });
    sections = (d.Sprints || []).map(s => sec(s.ID, s, s.Name, s.Cards || [], s.Total));
    sections.push(sec(0, null, T('Backlog'), (d.Backlog && d.Backlog.Cards) || [], d.Backlog && d.Backlog.Total));
    const now = Date.now();
    ops = ops.filter(o => !o.done || now - o.done < OP_TTL);
    for (const o of ops) place(o, o.jql === jql);
    for (const k of [...sel]) if (!sections.some(s => s.cards.some(c => c.Key === k))) sel.delete(k);
    if (side != null && !secOf(side)) side = defaultSide();
    paintSide();
  }
  // The next sprint to plan: the first planned one, else the active one, else the backlog.
  const defaultSide = () => (sections.find(s => s.sprint && s.sprint.State === 'future') || sections.find(s => s.sprint) || sections[sections.length - 1] || { id: 0 }).id;
  function paintSide() {
    sideEl.hidden = !sideOn();
    splitBtn.classList.toggle('on', side != null);
    clear(sidePick).append(...sections.map(s => h('option', { value: s.id, selected: s.id === side }, s.name)));
  }
  function setSide(id) {
    side = id;
    app.prefs.set(sideKey, id == null ? '' : String(id));
    paintSide(); relayout();
    if (id == null && paneOf(cur) !== left) setCur(left.last || stopsOf(left)[0] || '');
  }
  const jqlOf = () => qfs().filter(q => qf.has(q.ID)).map(q => '(' + q.JQL + ')').join(' AND ');
  async function load(fresh) {
    const my = ++token;
    try {
      const jql = jqlOf();
      const d = await app.api.get('/plan/' + board.ID + (jql ? '?jql=' + encodeURIComponent(jql) : ''), { fresh });
      if (my !== token) return;
      if (d.Calendar && !data) app.ui.toast('ui.calendar: ' + d.Calendar, { kind: 'err' });
      build(d, jql); relayout(true);
    } catch (e) {
      if (my !== token) return;
      if (!data) clear(left.scroller).append(h('div.empty', h('h2', T('Could not load')), h('p', e.message), h('button.btn', { onclick: () => load(true) }, T('Retry'))));
      else app.ui.errToast(e);
    }
  }
  function loadVelocity() {
    app.api.get('/reports/velocity/' + board.ID).then(v => {
      if (!v || !v.length) return;
      velAvg = v.reduce((a, x) => a + x.Done, 0) / v.length; velN = v.length; relayout();
    }).catch(() => {});
  }

  // ---- layout
  // The board's query language; a plain word matches the key, summary, assignee, status or labels.
  const text = c => [c.Key, c.Summary, c.Assignee, c.Status, c.Labels].filter(Boolean).join(' ').toLowerCase();
  const me = (app.session.me && app.session.me.AccountID) || '';
  const env = () => ({ me, pins: new Set(pins.list(app).map(p => p[0])), text });
  let match = null;
  function setFilter(t) { filter = t.trim(); match = cq.compile(filter, env()); renderChips(); relayout(); }
  function openBuilder() {
    if (!data) return;
    openFilterBuilder({ app, cards: sections.flatMap(s => s.cards), env: env(), query: filter, apply: t => { filterIn.value = t; setFilter(t); } });
  }
  // who: null for anyone, else a Set of AccountIDs ('-' unassigned), lib/who.js.
  const tmark = key => (app.timer ? app.timer.mark(key) : '');
  const filtering = () => !!match || who != null || mine || qf.size > 0;
  const visible = c => (!mine || c.AssigneeID === me) && passesWho(who, c) && (!match || match(c));
  const shown = (s, p) => sortCards(filtering() ? s.cards.filter(visible) : s.cards, CMP, p.sort, p.dir);
  const names = new Map();
  const qfs = () => (data && data.QuickFilters) || [];
  function renderChips() {
    const kids = [
      h('button.fchip' + (mine ? '.on' : ''), { dataset: { act: 'mine' }, title: T('Assigned to me') }, T('Mine')),
      h('button.fchip' + (who != null ? '.on' : ''), { dataset: { act: 'who' }, title: T('Assignee  (A)') }, h('kbd', 'A'), who == null ? T('Assignee') : whoLabel(who, id => names.get(id) || id)),
      ...qfs().slice(0, 9).map((q, i) => h('button.fchip' + (qf.has(q.ID) ? '.on' : ''), { dataset: { qf: q.ID }, title: q.JQL }, h('kbd', i + 1), q.Name)),
    ];
    if (filtering()) kids.push(h('button.fchip.clear', { dataset: { act: 'clear' }, title: T('Clear filters  (0)') }, icon('x'), T('clear')));
    clear(chips).append(...kids);
  }
  function setWho(w) {
    who = w;
    if (w != null) mine = false;
    renderChips(); relayout();
  }
  function toggleMine() {
    mine = !mine;
    if (mine) who = null;
    renderChips(); relayout();
  }
  function toggleQF(id) {
    qf.has(id) ? qf.delete(id) : qf.add(id);
    renderChips(); load();
  }
  function clearFilters() {
    const reload = qf.size > 0;
    filterIn.value = ''; filter = ''; match = null; who = null; mine = false; qf.clear();
    renderChips(); relayout();
    if (reload) load();
  }
  async function pickWho() {
    const seen = new Map();
    for (const s of sections) for (const c of s.cards) if (whoOf(c)) seen.set(whoOf(c), c.Assignee);
    for (const [id, n] of seen) names.set(id, n);
    const r = await pickPeople(app, seen, who);
    if (r !== undefined) setWho(r);
  }
  // The header of each pane: a click sorts by its column, again reverses it, a third time goes back to the rank.
  const colw = readWidths(app.prefs.get('planning.colw', ''));
  const grid = () => root.style.setProperty('--lcols', gridCols(cols, colw));
  function paintHeads() {
    grid();
    for (const p of panes) {
      const head = listHead(cols, p.sort, p.dir);
      head.addEventListener('click', e => { const t = e.target.closest('[data-sort]'); if (t && t.dataset.sort) setSort(p, ...nextSort(p.sort, p.dir, t.dataset.sort)); });
      dragCols(head, () => cols, setCols);
      resizeCols(head, { widths: colw, apply: grid, save: () => app.prefs.set('planning.colw', JSON.stringify(colw)) });
      if (p.head) p.head.replaceWith(head); else p.el.prepend(head);
      p.head = head; p.sb = -1;
    }
  }
  function setSort(p, s, d) {
    p.sort = s; p.dir = d;
    app.prefs.set(p.sortKey, s + ':' + d);
    paintHead(p.head, s, d);
    relayout();
  }
  async function pickCols() {
    const r = await pickListCols(app.ui, allCols(), cols);
    if (r) setCols(r);
  }
  function setCols(r) {
    cols = r;
    app.prefs.set('planning.cols', cols.join(','));
    for (const p of panes) if (!SORTS.includes(p.sort) && !cols.includes(p.sort)) { p.sort = 'rank'; p.dir = 1; app.prefs.set(p.sortKey, 'rank:1'); }
    paintHeads(); relayout();
  }
  paintHeads();
  const headH = s => ROW + px14(s.sprint ? 34 : 6);

  function relayout() {
    ROW = rowPx();
    for (const p of panes) {
      p.rows = [];
      for (const s of p.secs()) {
        p.rows.push({ k: 'h', key: 'h:' + s.id, s, h: headH(s) });
        if (folded.has(s.id)) continue;
        const cs = shown(s, p);
        if (!cs.length) p.rows.push({ k: 'e', key: 'e:' + s.id, s, h: ROW });
        for (const c of cs) p.rows.push({ k: 'c', key: 'c:' + c.Key, s, c, h: ROW });
      }
      p.tops = []; p.total = 0;
      for (const r of p.rows) { p.tops.push(p.total); p.total += r.h; }
      p.space.style.height = p.total + 'px';
      for (const [k, n] of p.live) { n.remove(); p.live.delete(k); }
    }
    if (!paneOf(cur)) cur = stopsOf(left)[0] || stopsOf(right)[0] || '';
    paint();
  }
  const idOf = r => (r.k === 'h' ? 'h:' + r.s.id : r.k === 'c' ? r.c.Key : '');
  const stopsOf = p => p.rows.filter(r => r.k !== 'e').map(idOf);
  const paneOf = id => (id ? panes.find(p => p.rows.some(r => r.k !== 'e' && idOf(r) === id)) : null);
  const stops = () => stopsOf(paneOf(cur) || left);
  const rowAtY = (p, y) => {
    let lo = 0, hi = p.rows.length - 1;
    while (lo < hi) { const mid = (lo + hi + 1) >> 1; if (p.tops[mid] <= y) lo = mid; else hi = mid - 1; }
    return lo;
  };

  // ---- paint the visible window
  const paint = () => { for (const p of panes) p.paint(); bar.set(sel.size); root.classList.toggle('marking', sel.size > 0); };
  function clearSel() { sel.clear(); for (const p of panes) for (const n of p.live.values()) n._sig = ''; paint(); }
  function paintPane(p) {
    const { rows, tops, live, scroller, space } = p;
    const sb = scroller.offsetWidth - scroller.clientWidth;
    if (sb !== p.sb) { p.sb = sb; p.head.style.paddingRight = `calc(var(--pad) + ${sb}px)`; }
    if (!rows.length) return;
    const vt = scroller.scrollTop, vb = vt + scroller.clientHeight;
    const a = Math.max(rowAtY(p, vt) - 6, 0), b = Math.min(rowAtY(p, vb) + 6, rows.length - 1);
    const want = new Set();
    for (let i = a; i <= b; i++) {
      const r = rows[i]; want.add(r.key);
      let n = live.get(r.key);
      const sig = sigOf(r);
      if (!n) { n = h('div.pl-abs'); live.set(r.key, n); space.append(n); }
      if (n._sig !== sig) { n._sig = sig; n.replaceChildren(rowNode(r)); }
      n.style.transform = `translateY(${tops[i]}px)`; n.style.height = r.h + 'px';
      const inner = n.firstChild;
      if (inner) {
        inner.classList.toggle(r.k === 'c' ? 'sel' : 'cur', idOf(r) === cur);
        if (r.k === 'c') inner.classList.toggle('mark', sel.has(r.c.Key));
      }
    }
    for (const [k, n] of live) if (!want.has(k) && !(drag && drag.keys.some(x => k === 'c:' + x))) { n.remove(); live.delete(k); }
  }
  for (const p of panes) {
    p.scroller.addEventListener('scroll', () => p.paint(), { passive: true });
    new ResizeObserver(() => p.paint()).observe(p.scroller);
  }

  function sigOf(r) {
    if (r.k === 'c') { const c = r.c; return [cols, c.Key, c.Summary, c.Status, c.Done, c.InProgress, c.Priority, c.Assignee, c.AvatarURL, c.Points, c.Flagged, c.ParentSummary, c.Labels, c.Reporter, c.Due, c.Updated, c.Created, c.Since, c.Extra, sel.has(c.Key), pinned.has(c.Key), cellCtx.hl(c.Key), tmark(c.Key)].join('|'); }
    if (r.k === 'e') return 'e' + (filtering() ? 'f' : '');
    const s = r.s;
    return ['h', s.id, s.name, s.sprint && s.sprint.State, s.sprint && s.sprint.Start, s.sprint && s.sprint.End, s.sprint && s.sprint.Goal, s.sprint && s.sprint.Mine && s.sprint.Mine.Capacity, folded.has(s.id), velAvg, velN, filter, whoKey(who),
      s.cards.map(c => c.Key + c.Points + c.Assignee + c.Done).join(',')].join('|');
  }

  function rowNode(r) {
    if (r.k === 'e') return h('div.pl-row.pl-empty', { style: { height: r.h + 'px' } }, filtering() ? T('No matches') : r.s.sprint ? T('Drop issues here to plan them') : T('The backlog is empty'));
    if (r.k === 'h') return headNode(r.s, r.h);
    const c = r.c, w = buildRow(cols);
    w.className = 'lrow ' + (c.Done ? 'done' : c.InProgress ? 'prog' : 'todo') + (c.Flagged ? ' flagged' : '');
    w.dataset.key = c.Key; w.style.height = r.h + 'px';
    fillCells(w, cols, c, cellCtx);
    return w;
  }

  function headNode(s, height) {
    const sp = s.sprint, open = !folded.has(s.id), cs = s.cards;
    const sum = cs.reduce((a, c) => a + pts(c), 0), done = cs.reduce((a, c) => a + (c.Done ? pts(c) : 0), 0);
    const over = sp && velAvg > 0 && sum > velAvg * 1.001;
    const dates = sp && !isZero(sp.Start) ? shortDate(sp.Start) + ' – ' + shortDate(sp.End) : '';
    const h1 = h('div.pl-h1',
      h('button.pl-fold', { tabindex: -1, 'aria-label': open ? T('Fold %s', s.name) : T('Unfold %s', s.name), 'aria-expanded': open, dataset: { act: 'fold' } }, icon(open ? 'chevron-down' : 'chevron-right')),
      h('span.pl-name', s.name),
      sp && h('span.pill.cat-' + (sp.State === 'active' ? 'indeterminate' : 'new'), sp.State === 'active' ? T('active') : T('planned')),
      dates && h('span.pl-dates', dates),
      sp && h('span.pl-goal', { title: sp.Goal || T('No goal') }, sp.Goal || ''),
      !sp || !sp.Goal ? h('span.spacer') : null,
      h('span.pl-tot' + (over ? '.over' : ''), s.rest ? T('%d of %d issues', cs.length, cs.length + s.rest) : Tn(cs.length, '%d issue', '%d issues', cs.length), ' · ', h('b', fmtP(sum) + 'p'), sp && velAvg > 0 && T(' of ~%sp (avg last %d)', fmtP(velAvg), velN),
        unest(cs) ? h('span.pl-unest', { title: T('Issues without story points') }, T(' · %d unestimated', unest(cs))) : null),
      sp && sp.State === 'future' && h('button.btn.pl-act', { dataset: { act: 'start' }, tabindex: -1 }, T('Start')),
      sp && sp.State === 'active' && h('button.btn.pl-act', { dataset: { act: 'close' }, tabindex: -1 }, T('Complete')),
      sp && h('button.btn.ghost.pl-act', { dataset: { act: 'edit' }, tabindex: -1, title: T('Edit sprint (E)') }, T('Edit')));
    const out = h('div.pl-row.pl-head', { style: { height: height + 'px' } }, h1);
    if (sp) out.append(h('div.pl-h2', h('span.pl-prog', { title: T('%sp done', fmtP(done)), role: 'progressbar', 'aria-valuenow': sum ? Math.round(done / sum * 100) : 0, 'aria-valuemin': 0, 'aria-valuemax': 100 }, h('i', { style: { width: (sum ? done / sum * 100 : 0) + '%' } })), capsNode(cs, sp)));
    return out;
  }

  const unest = cs => cs.filter(c => !c.Done && (c.Points === '' || c.Points == null)).length;
  // Yours comes less your meetings in ui.calendar when the server worked them out (sprint.Mine).
  function capFor(name, sp) {
    if (sp && sp.Mine && sp.Mine.Name === name) return sp.Mine.Capacity;
    if (name in caps) return caps[name];
    return name !== 'unassigned' && 'default' in caps ? caps.default : null;
  }
  function capsNode(cs, sp) {
    const by = new Map();
    for (const c of cs) by.set(c.Assignee || 'unassigned', (by.get(c.Assignee || 'unassigned') || 0) + pts(c));
    const list = [...by].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]));
    return h('div.pl-caps', list.map(([name, p]) => {
      const cap = capFor(name, sp), over = cap != null && p > cap, dn = name === 'unassigned' ? T('unassigned') : name;
      const meet = sp && sp.Mine && sp.Mine.Name === name ? T(' (%sh of meetings off)', fmtP(sp.Mine.Hours)) : '';
      return h('span.pl-cap' + (over ? '.over' : ''), { title: cap != null ? T('%s: %s of %sp%s', dn, fmtP(p), fmtP(cap), meet) : T('%s: %sp', dn, fmtP(p)) },
        h('span', dn), cap != null && h('span.bar', h('i', { style: { width: Math.min(p / (cap || 1), 1) * 100 + '%' } })), cap != null ? `${fmtP(p)}/${fmtP(cap)}${over ? '!' : ''}` : fmtP(p));
    }));
  }

  // ---- cursor and selection
  function setCur(id, scrollTo = true, keepAnchor = false) {
    cur = id; paint();
    if (!keepAnchor) anchor = id;
    const p = paneOf(id); if (!p) return;
    p.last = id;
    if (!scrollTo) return;
    const i = p.rows.findIndex(r => idOf(r) === id);
    const top = p.tops[i], bot = top + p.rows[i].h, vt = p.scroller.scrollTop, vh = p.scroller.clientHeight;
    if (top < vt) p.scroller.scrollTop = top; else if (bot > vt + vh) p.scroller.scrollTop = bot - vh;
  }
  // tab: the cursor to the other pane, where it was last
  function otherPane() {
    if (!sideOn()) return;
    const p = paneOf(cur) === right ? left : right;
    const st = stopsOf(p);
    if (st.length) setCur(st.includes(p.last) ? p.last : st[0]);
  }
  const move = d => { const st = stops(); if (!st.length) return; setCur(st[Math.min(Math.max(st.indexOf(cur) + d, 0), st.length - 1)]); };
  const curCard = () => { for (const s of sections) { const c = s.cards.find(x => x.Key === cur); if (c) return { c, s }; } return null; };
  const curSection = () => { const cc = curCard(); if (cc) return cc.s; return cur.startsWith('h:') ? secOf(Number(cur.slice(2))) : sections[0]; };
  // the cards an action applies to, in display order
  function targets() {
    if (sel.size) return allRows().filter(r => r.k === 'c' && sel.has(r.c.Key)).map(r => r.c.Key);
    const cc = curCard(); return cc ? [cc.c.Key] : [];
  }
  function toggleSel(key) { sel.has(key) ? sel.delete(key) : sel.add(key); for (const p of panes) { const n = p.live.get('c:' + key); if (n) n._sig = ''; } paint(); }
  // ctrl+click starts selecting with the issue the cursor was on; shift+click selects the run from the anchor
  // (the last row picked other than by shift+click) to the clicked one, within its pane.
  function clickSel(p, key, shift, ctrl) {
    const ks = p.rows.filter(r => r.k === 'c').map(r => r.c.Key), a = ks.indexOf(anchor), b = ks.indexOf(key);
    if (shift && a >= 0) {
      for (const k of ks.slice(Math.min(a, b), Math.max(a, b) + 1)) sel.add(k);
      for (const q of panes) for (const n of q.live.values()) n._sig = '';
      paint();
    } else {
      if (ctrl && !sel.size && a >= 0 && anchor !== key) toggleSel(anchor);
      toggleSel(key);
    }
    setCur(key, false, shift);
  }

  // ---- writes: optimistic and queued. Jira's agile lists trail a write by seconds, so every load
  // replays the recent moves (ops) over what it got; a failed write drops its own and reloads.
  const OP_TTL = 20000;
  let queue = Promise.resolve(), ops = [];
  function enqueue(fn, keys, op) {
    writing++; lastWrite = Date.now();
    ops.push(op);
    queue = queue.then(fn).then(() => { op.done = Date.now(); for (const k of keys) app.bus.emit('issue:changed', { key: k }); })
      .catch(e => { ops = ops.filter(o => o !== op); app.ui.errToast(e); load(true); })
      .finally(() => { writing--; lastWrite = Date.now(); });
    return queue;
  }

  // place puts op.keys, in order, in section op.to before/after op.anchor (at its end when the anchor
  // is not there). A card no section has comes from op.cards when inject.
  function place(op, inject) {
    const to = secOf(op.to); if (!to) return;
    const have = new Map();
    for (const s of sections) for (const c of s.cards) if (op.keys.includes(c.Key)) have.set(c.Key, c);
    for (const s of sections) s.cards = s.cards.filter(c => !op.keys.includes(c.Key));
    const cards = op.keys.map(k => have.get(k) || (inject && op.cards.find(c => c.Key === k))).filter(Boolean)
      .map(c => ({ ...c, Sprint: to.sprint ? to.sprint.Name : '' }));
    let idx = op.anchor ? to.cards.findIndex(c => c.Key === op.anchor) : -1;
    idx = idx < 0 ? to.cards.length : idx + (op.after ? 1 : 0);
    to.cards.splice(idx, 0, ...cards);
  }

  // Move keys to section `to`, before/after anchor. Without one they go to the edge nearest where
  // they came from: the top of a section further down, the bottom of one further up.
  function moveCards(keyList, to, anchor, after, { undoable = true } = {}) {
    if (!keyList.length || !to || (anchor && keyList.includes(anchor))) return;
    const order = [], from = new Map();
    for (const s of sections) for (const c of s.cards) if (keyList.includes(c.Key)) { order.push(c); from.set(c.Key, s); }
    if (!order.length) return;
    const keys = order.map(c => c.Key);
    const crossing = keys.filter(k => from.get(k) !== to);
    if (!anchor) {
      const rest = to.cards.filter(c => !keyList.includes(c.Key));
      const down = sections.indexOf(from.get(keys[0])) < sections.indexOf(to);
      if (rest.length) { anchor = down ? rest[0].Key : rest[rest.length - 1].Key; after = !down; }
    }
    if (crossing.length && undoable) {
      // u puts them back beside the issue each left (TUI recordSprintUndo)
      const back = [...new Set(crossing.map(k => from.get(k)))].map(s => {
        const ks = crossing.filter(k => from.get(k) === s), i = s.cards.findIndex(c => c.Key === ks[0]);
        const prev = s.cards.slice(0, i).findLast(c => !keyList.includes(c.Key)), next = s.cards.slice(i).find(c => !keyList.includes(c.Key));
        return { id: s.id, keys: ks, anchor: (prev || next || {}).Key || null, after: !!prev };
      });
      import('./fields.js').then(m => m.pushUndo(app, T('the move of %s to %s', crossing.length === 1 ? crossing[0] : Tn(crossing.length, '%d issue', '%d issues', crossing.length), to.name), () => {
        for (const b of back) moveCards(b.keys, secOf(b.id), b.anchor, b.after, { undoable: false });
        relayout();
        return queue;
      }));
    }
    const op = { keys, to: to.id, anchor, after, cards: order, jql: jqlOf() };
    place(op, true);
    relayout();
    return enqueue(async () => {
      if (crossing.length) await app.api.post('/plan/move', { Keys: crossing, Sprint: to.id });
      if (anchor) await app.api.post('/plan/rank', { Keys: keys, Other: anchor, After: after });
    }, keys, op);
  }

  // J/K: swap with the neighbour
  function rankStep(d) {
    const cc = curCard(); if (!cc || filtering()) return;
    const p = paneOf(cur) || left;
    if (sorted(p)) { app.ui.toast(T('Sorted by %s: J and K rank in the rank order, a click on the header brings it back', p.sort)); return; }
    const cs = cc.s.cards, j = cs.indexOf(cc.c) + d;
    if (j < 0 || j >= cs.length) return;
    moveCards([cc.c.Key], cc.s, cs[j].Key, d > 0, { undoable: false });
    setCur(cc.c.Key);
  }

  async function moveTo() {
    const keysToMove = targets(); if (!keysToMove.length) return;
    const to = await app.ui.pick({ title: T('Move %d to…', keysToMove.length), items: sections, label: s => s.name, detail: s => Tn(s.cards.length, '%d issue', '%d issues', s.cards.length) });
    if (to) { moveCards(keysToMove, to, null, false); if (keysToMove.length === 1) cur = keysToMove[0]; sel.clear(); relayout(); setCur(cur); }
  }
  // >: to the sprint kept in view
  function toSide() {
    const to = sideOn() && secOf(side), ks = targets();
    if (!to || !ks.length) return;
    const p = paneOf(cur) || left, st = stopsOf(p), next = st.slice(st.indexOf(cur) + 1).find(id => !ks.includes(id));
    moveCards(ks, to, null, false); sel.clear(); relayout();
    setCur(next || stopsOf(p)[0] || '');
    app.ui.toast(T('%s to %s', ks.length > 1 ? Tn(ks.length, '%d issue', '%d issues', ks.length) : ks[0], to.name));
  }
  function shift(d) {
    const cc = curCard(); if (!cc) return;
    const to = sections[sections.indexOf(cc.s) + d];
    if (!to) return;
    const ks = targets(); moveCards(ks, to, null, false); sel.clear(); relayout(); setCur(cc.c.Key);
    app.ui.toast(T('%s to %s', ks.length > 1 ? Tn(ks.length, '%d issue', '%d issues', ks.length) : ks[0], to.name));
  }

  // ---- sprint actions
  const sprintOf = pred => { const s = curSection(); return s && s.sprint && (!pred || pred(s.sprint)) ? s : sections.find(x => x.sprint && (!pred || pred(x.sprint))); };
  async function newSprint() {
    const last = [...sections].reverse().find(s => s.sprint);
    const name = await app.ui.prompt({ title: T('New sprint'), value: nextSprintName(last && last.name), placeholder: T('Sprint name'), ok: T('Create') });
    if (!name || !name.trim()) return;
    try { await app.api.post('/plan/sprints', { Board: board.ID, Name: name.trim() }); app.ui.toast(T('Created %s', name.trim()), { kind: 'ok' }); load(true); } catch (e) { app.ui.errToast(e); }
  }
  function dialog(title, fields, ok, run) {
    const form = h('form.pl-form', { onsubmit: async e => {
      e.preventDefault();
      try { await run(form); m.close(); load(true); } catch (err) { app.ui.errToast(err); }
    } }, fields, h('div.row.end', h('button.btn', { type: 'button', onclick: () => m.close() }, T('Cancel')), h('button.btn.primary', { type: 'submit' }, ok)));
    const m = app.ui.modal(form, { title });
    return m;
  }
  function startSprint() {
    const s = sprintOf(sp => sp.State === 'future'); if (!s) return app.ui.toast(T('No planned sprint to start'));
    dialog(T('Start %s', s.name), [
      h('label', T('Ends on'), h('input.input', { type: 'date', name: 'end', value: plusDays(14), min: plusDays(1), required: true, autofocus: true })),
      h('p.pl-note', T('Starts now with %s, %sp.', Tn(s.cards.length, '%d issue', '%d issues', s.cards.length), fmtP(s.cards.reduce((a, c) => a + pts(c), 0))))],
    T('Start sprint'), async f => { await app.api.post('/plan/sprints/' + s.id + '/start', { End: f.end.value }); app.ui.toast(T('%s started', s.name), { kind: 'ok' }); });
  }
  function closeSprint() {
    const s = sprintOf(sp => sp.State === 'active'); if (!s) return app.ui.toast(T('No active sprint'));
    const lastCol = columns.length ? columns[columns.length - 1].StatusIDs || [] : [];
    const open = s.cards.filter(c => !c.Done && !lastCol.includes(c.StatusID));
    const next = sections.filter(x => x.sprint && x.sprint.State === 'future');
    const dest = h('select.input', { name: 'to' }, next.map(x => h('option', { value: x.id }, x.name)), h('option', { value: 0 }, T('Backlog')));
    dialog(T('Complete %s', s.name), [
      h('p', T('%d done, %d unfinished.', s.cards.length - open.length, open.length)),
      open.length ? h('label', T('Move unfinished issues to'), dest) : null],
    T('Complete sprint'), async f => {
      const r = await app.api.post('/plan/sprints/' + s.id + '/close', { Board: board.ID, MoveTo: Number(dest.value) || 0 });
      const cheer = String((app.session.ui && app.session.ui.Delight) || '').toLowerCase() === 'off' ? '' : await app.api.get('/reports/velocity/' + board.ID, { fresh: true }).then(sprintCheer, () => '');
      app.ui.toast(T('%s completed', s.name) + (r && r.Moved ? T(', %d moved', r.Moved) : '') + (cheer ? ' · ' + cheer : ''), { kind: 'ok', ms: cheer ? 6000 : undefined });
    });
  }
  function editSprint() {
    const s = sprintOf(); if (!s) return;
    const sp = s.sprint, end = isZero(sp.End) ? '' : isoDay(sp.End);
    dialog(T('Edit %s', s.name), [
      h('label', T('Name'), h('input.input', { name: 'name', value: sp.Name, required: true, autofocus: true })),
      h('label', T('Goal'), h('textarea.input', { name: 'goal', rows: 3 }, sp.Goal || '')),
      h('label', T('Ends on'), h('input.input', { type: 'date', name: 'end', value: end }))],
    T('Save'), async f => {
      const body = {};
      if (f.name.value.trim() !== sp.Name) body.Name = f.name.value.trim();
      if (f.goal.value.trim() !== (sp.Goal || '')) body.Goal = f.goal.value.trim();
      if (f.end.value && f.end.value !== end) body.End = f.end.value;
      if (Object.keys(body).length) await app.api.post('/plan/sprints/' + s.id + '/update', body);
    });
  }
  const act = { new: newSprint, start: startSprint, close: closeSprint, edit: editSprint };
  const onAction = e => { const f = act[e.detail]; if (f) f(); };
  document.addEventListener('plan:action', onAction);

  // ---- mouse
  // shift+click would select the text between the two rows
  for (const p of panes) p.scroller.addEventListener('mousedown', e => { if (e.shiftKey && e.target.closest('.pl-row, .lrow')) e.preventDefault(); });
  for (const p of panes) p.scroller.addEventListener('click', e => {
    const n = e.target.closest('.pl-row, .lrow'); if (!n) return;
    const holder = n.parentElement, r = p.rows.find(x => p.live.get(x.key) === holder);
    if (!r) return;
    const a = e.target.closest('[data-act]');
    if (r.k === 'h') {
      const f = a && a.dataset.act;
      setCur('h:' + r.s.id, false);
      if (f === 'start' || f === 'close' || f === 'edit') return act[f]();
      return toggleFold(r.s);
    }
    if (r.k !== 'c') return;
    if (e.target.closest('.l-mark') || e.ctrlKey || e.metaKey || e.shiftKey) return clickSel(p, r.c.Key, e.shiftKey, e.ctrlKey || e.metaKey);
    setCur(r.c.Key, false);
    app.panel.open(r.c.Key);
  });
  // y: the section under the cursor as a markdown table (TUI copy table).
  function copyTable() {
    const s = curSection(); if (!s) return;
    const esc = x => String(x == null ? '' : x).replace(/\|/g, '\\|');
    const p = paneOf(cur) || left;
    const rows = sortCards(s.cards, CMP, p.sort, p.dir).map(c => `| ${c.Key} | ${esc(c.Summary)} | ${esc(c.Assignee)} | ${esc(c.Points)} |`);
    const text = ['| Key | Summary | Assignee | Points |', '| --- | --- | --- | --- |', ...rows].join('\n');
    navigator.clipboard.writeText(text).then(() => app.ui.toast(T('Copied %s (%s) as a markdown table', s.name, Tn(s.cards.length, '%d issue', '%d issues', s.cards.length)), { kind: 'ok' }), e => app.ui.errToast(e));
  }
  function toggleFold(s) {
    if (!s) return;
    folded.has(s.id) ? folded.delete(s.id) : folded.add(s.id);
    relayout(); setCur('h:' + s.id);
  }

  // ---- drag and drop
  function dropAt(p, e) {
    const { scroller, rows, tops, total } = p;
    if (!rows.length) return null;
    const rect = scroller.getBoundingClientRect(), y = e.clientY - rect.top + scroller.scrollTop;
    if (y >= total) { const s = rows[rows.length - 1].s; return { s, anchor: null, after: false, y: total - 1 }; }
    const i = rowAtY(p, Math.max(y, 0)), r = rows[i];
    if (sorted(p)) { const at = rows.findLastIndex(x => x.s === r.s); return { s: r.s, anchor: null, after: false, y: tops[at] + rows[at].h }; }
    if (r.k === 'c') { const after = y - tops[i] > r.h / 2; return { s: r.s, anchor: r.c.Key, after, y: tops[i] + (after ? r.h : 0) }; }
    if (r.k === 'h') { const first = shown(r.s, p)[0]; return { s: r.s, anchor: first ? first.Key : null, after: false, y: tops[i] + r.h }; }
    return { s: r.s, anchor: null, after: false, y: tops[i] + 2 };
  }
  const dragNodes = ks => panes.flatMap(p => ks.map(k => p.live.get('c:' + k))).filter(n => n && n.firstChild);
  for (const p of panes) {
    const { scroller, dropEl } = p;
    scroller.addEventListener('dragstart', e => {
      const n = e.target.closest('.lrow[data-key]'); if (!n) return;
      const key = n.dataset.key;
      const ks = sel.has(key) ? targets() : [key];
      drag = { keys: ks };
      e.dataTransfer.effectAllowed = 'move'; e.dataTransfer.setData('text/plain', ks.join(','));
      for (const l of dragNodes(ks)) l.firstChild.classList.add('dragging');
    });
    scroller.addEventListener('dragover', e => {
      if (!drag) return;
      const t = dropAt(p, e); if (!t) return;
      e.preventDefault(); e.dataTransfer.dropEffect = 'move';
      const rect = scroller.getBoundingClientRect();
      if (e.clientY < rect.top + 50) scroller.scrollTop -= 14; else if (e.clientY > rect.bottom - 50) scroller.scrollTop += 14;
      dropEl.hidden = false; dropEl.style.top = t.y - 1 + 'px';
    });
    scroller.addEventListener('dragleave', e => { if (!scroller.contains(e.relatedTarget)) dropEl.hidden = true; });
    scroller.addEventListener('drop', e => {
      if (!drag) return;
      const t = dropAt(p, e); if (!t) return;
      e.preventDefault();
      const ks = drag.keys;
      dropEl.hidden = true; drag = null;
      if (sorted(p) && ks.every(k => t.s.cards.some(c => c.Key === k))) return;
      moveCards(ks, t.s, t.anchor, t.after);
    });
    scroller.addEventListener('dragend', () => {
      for (const q of panes) q.dropEl.hidden = true;
      for (const q of panes) for (const n of q.live.values()) n.firstChild && n.firstChild.classList.remove('dragging');
      drag = null; paint();
    });
  }

  // ---- keys
  const G = { group: T('Planning') };
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next'), G);
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous'), G);
  scope.bind('Home', () => setCur(stops()[0]), T('first'), { ...G, hidden: true });
  scope.bind('End', () => setCur(stops().at(-1)), T('last'), { ...G, hidden: true });
  const page = () => Math.max(1, Math.floor((paneOf(cur) || left).scroller.clientHeight / rowPx()) - 1);
  scope.bind('PageDown', () => move(page()), T('page down'), { ...G, hidden: true });
  scope.bind('PageUp', () => move(-page()), T('page up'), { ...G, hidden: true });
  scope.bind('J', () => rankStep(1), T('rank down'), { ...G, bar: T('rank') });
  scope.bind('K', () => rankStep(-1), T('rank up'), { ...G, bar: T('rank') });
  scope.bind('m', moveTo, T('move to a sprint or the backlog'), { ...G, bar: T('move') });
  scope.bind(']', () => shift(1), T('move to the next sprint'), G);
  scope.bind('[', () => shift(-1), T('move to the previous sprint'), G);
  scope.bind('|', () => setSide(side == null ? defaultSide() : null), T('split: keep a sprint in view beside the list'), G);
  scope.bind('>', toSide, T('move to the sprint kept in view (split)'), { ...G, when: sideOn });
  scope.bind('Tab', otherPane, T('the other pane (split)'), { ...G, when: sideOn });
  scope.bind(['h', 'ArrowLeft', 'l', 'ArrowRight'], otherPane, T('the other pane (split)'), { ...G, when: sideOn, hidden: true });
  scope.bind(['x', 'Space'], () => { const cc = curCard(); if (cc) { toggleSel(cc.c.Key); move(1); } else if (cur.startsWith('h:')) toggleFold(curSection()); }, T('select'), { ...G, bar: T('select') });
  scope.bind('Enter', () => { const cc = curCard(); if (cc) app.panel.open(cc.c.Key); else toggleFold(curSection()); }, T('open'), { ...G, bar: T('open') });
  scope.bind('z', () => toggleFold(curSection()), T('fold or unfold the section'), G);
  scope.bind('P', () => { const cc = curCard(); if (cc) app.actions.edit(cc.c.Key, 'points'); }, T('story points'), G);
  scope.bind('a', () => { const cc = curCard(); if (cc) app.actions.edit(cc.c.Key, 'assignee'); }, T('assignee'), G);
  scope.bind('e', () => { const cc = curCard(); if (cc && app.actions.menu) app.actions.menu(cc.c.Key); }, T('quick edit: status, assignee, priority, points, labels, sprint, pin'), { ...G, bar: T('edit') });
  scope.bind('X', () => { const ks = targets(); if (ks.length) app.actions.bulk(ks); }, T('bulk edit the marked issues (else the one under the cursor)'), G);
  scope.bind('o', () => { const cc = curCard(); if (cc) window.open(app.session.baseURL + '/browse/' + cc.c.Key, '_blank', 'noopener'); }, T('open in Jira'), G);
  scope.bind('y', copyTable, T('copy the section as a markdown table'), G);
  scope.bind('N', newSprint, T('new sprint'), { ...G, bar: T('new sprint') });
  scope.bind('Z', startSprint, T('start the sprint (S starts work on the issue)'), G);
  scope.bind('C', closeSprint, T('complete the active sprint'), G);
  scope.bind('E', editSprint, T('edit sprint name, goal, end'), G);
  scope.bind('R', () => load(true), T('reload'), G);
  scope.bind('O', () => { const p = paneOf(cur) || left; setSort(p, SORTS[(SORTS.indexOf(p.sort) + 1) % SORTS.length], 1); }, T('sort the pane: rank, priority, points, assignee, epic, key, status…'), G);
  scope.bind('f', () => filterIn.focus(), T('filter'), { ...G, bar: T('filter') });
  scope.bind('F', openBuilder, T('filter builder'), { ...G, bar: T('filter builder') });
  scope.bind('A', pickWho, T('filter by assignee'), G);
  scope.bind('0', clearFilters, T('clear filters'), G);
  for (let i = 1; i <= 9; i++) scope.bind(String(i), () => { const q = qfs()[i - 1]; if (q) toggleQF(q.ID); }, i === 1 ? T('toggle quick filter 1-9') : '', { ...G, hidden: i > 1 });
  scope.bind('Escape', () => {
    if (document.activeElement === filterIn) { filterIn.value = ''; filterIn.blur(); setFilter(''); return; }
    if (filtering()) { clearFilters(); return; }
    clearSel();
  }, T('clear filters and selection'), { ...G, input: true, when: () => document.activeElement === filterIn || filtering() || sel.size > 0 });

  const offTimer = ['timer', 'timer:tick'].map(ev => app.bus.on(ev, paint));
  const offBus = app.bus.on('issue:changed', () => { if (writing || Date.now() - lastWrite < 1500) return; load(true); });
  const onWide = () => { paintSide(); relayout(); };
  wide.addEventListener('change', onWide);
  const off = () => { offBus(); offM(); offTimer.forEach(f => f()); wide.removeEventListener('change', onWide); };
  const offM = onMetrics(() => relayout(true));

  await load();
  loadVelocity();
  return () => { token++; off(); document.removeEventListener('plan:action', onAction); };
}
