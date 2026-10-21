// The board: swim lanes by column or a sortable list, with filters, keyboard
// navigation, drag and drop, and quiet refreshes. Cards render through lib/vlist.js
// so a lane or list of thousands stays smooth.
import { h, clear, delegate, debounce } from '../lib/dom.js';
import { icon, setIcon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { vlist } from '../lib/vlist.js';
import { onChange as onMetrics } from '../lib/metrics.js';
import { hwheel } from '../lib/hscroll.js';
import { isZero, date, shortDate } from '../lib/fmt.js';
import { workdays } from '../lib/worktime.js';
import { goDate } from '../lib/godate.js';
import * as cq from '../lib/cardquery.js';
import * as pins from '../lib/pins.js';
import { confetti } from '../lib/delight.js';
import { passesWho, pickWho as pickPeople, whoLabel } from '../lib/who.js';
import { openFilterBuilder } from './board_filter.js';
import { comparators, sortCards, prioOrd, num } from '../lib/cardsort.js';
import { selBar } from '../lib/selbar.js';
import { COLS, DEFAULT_COLS, SORTS, gridCols, fixCols, nextSort, listHead, paintHead as paintListHead, pickCols as pickListCols, dragCols, resizeCols, readWidths, buildRow as listRow, fillCells } from '../lib/cardlist.js';
import { buildCard as buildCardEl, fillCard as fillCardEl, catClass } from '../lib/card.js';
import { layoutOf, lookOf, colour } from '../lib/cardstyle.js';
import { lastProject, lastBoard, setCtx, openProject, pickBoard as pickBoardOf, recover } from './plan_ctx.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

css('board');

const FIELD_COL = { type: null, priority: 'priority', status: 'status', points: 'points', assignee: 'assignee', parent: 'epic', due: 'due', age: 'age' };
const SWIMS = ['none', 'assignee', 'epic', 'priority'];
const SWIMNAME = { assignee: T('assignee'), epic: T('epic'), priority: T('priority') };

const who = c => c.AssigneeID || c.Assignee || '';
const firstOf = (api, path) => new Promise((res, rej) => {
  let got = false;
  api.swr(path, d => { if (!got) { got = true; res(d); } }).catch(e => { if (!got) rej(e); });
});

function parseEvery(s) {
  s = String(s || '').trim().toLowerCase();
  if (s === 'off' || s === '0') return 0;
  const m = s.match(/^(\d+)\s*(s|m|h)$/);
  return m ? Number(m[1]) * { s: 1e3, m: 6e4, h: 36e5 }[m[2]] : 12e4;
}

// embed mounts the board inside another view (the standup's): it leaves the URL, the context crumbs, the palette's
// board commands and app.lastView alone, takes its keys from the scope it is given (none, for the standup), and
// gains embed.setWho(ids) and embed.select(key); embed.onWho(ids) hears the assignee filter change on it.
export default function mount(el, { app, params, query, scope, context, toolbar, embed }) {
  const { api, bus, ui } = app;
  const me = (app.session.me && app.session.me.AccountID) || '';
  const UI = app.session.ui || {};
  // ui.stale_after: how old the board may be when the tab comes back before it refetches; the TUI's minute unset.
  const staleMs = /^\d+\s*[smh]$/i.test(String(UI.StaleAfter || '').trim()) ? parseEvery(UI.StaleAfter) : 6e4;
  const DF = UI.DateFormat || '';
  const CF = UI.CardFields && UI.CardFields.length ? new Set(UI.CardFields.map(x => String(x).toLowerCase().trim())) : null;
  const CUSTOM = (UI.CustomFields || []).map(n => 'x:' + n);
  const LAYOUT = layoutOf(UI); // ui.card_layout, else ui.card_fields in the usual places
  const LOOK = lookOf(UI.CardStyles, { me, get pins() { return S.pins; }, get notes() { return S.notes; } }, UI.CustomFields || []); // ui.card_styles
  const allCols = () => [...Object.keys(COLS), ...CUSTOM];
  const S = {
    project: '', boards: [], board: null, bundle: null, people: new Map(),
    cards: [], total: 0, loaded: false, path: '', fetched: 0, busy: 0,
    scope: query.sprint || 'active', saved: [], queues: [],
    qf: new Set(), mine: false, who: null, text: '', textFn: null,
    mode: 'lanes', sort: 'rank', dir: 1, swim: 'none', fold: new Set(), compact: false, hideEmpty: false, hidden: 0, cols: DEFAULT_COLS, colw: readWidths(app.prefs.get('board.colw', '')),
    layout: '', layoutHidden: 0, secFold: new Set(), laneFold: new Set(),
    past: null, closed: null, pins: new Set(), notes: new Set(), starred: [], colors: null, lastEdit: null,
    sel: null, marks: new Set(), rowMem: 0,
    panes: [], where: new Map(), visible: [], built: '', rowH: 0,
    drag: null, dead: false,
  };
  const timers = new Set();
  const later = (fn, ms) => { const t = setTimeout(() => { timers.delete(t); fn(); }, ms); timers.add(t); return t; };
  const offs = [], unreg = [];
  offs.push(onMetrics(() => { if (!S.dead) layout(); }));
  let lastInput = Date.now(), autoT = 0;

  // ---- skeleton
  const filterIn = h('input.input.bd-filter', { type: 'text', placeholder: T('Filter  (f)'), spellcheck: false, autocomplete: 'off', title: T('words, status:review,test  points>2  prio>=high  is:flagged  age>3d  due<7d  epic:  -negate  (F builds a query)') });
  const chips = h('div.bd-chips');
  const stats = h('span.bd-stats.dim'), sprintEl = h('span.bd-sprint.dim');
  const bar = h('div.bd-bar', chips, h('span.sp'), filterIn, stats);
  const banner = h('div.bd-banner', { hidden: true });
  const main = h('div.bd-main');
  const selbar = selBar({ edit: () => bulk(), clear: () => clearMarks() });
  const root = h('div.bd', bar, banner, main, selbar.el);
  el.append(root);
  const bdMsg = text => clear(main).append(h('div.empty', text));

  // ---- toolbar
  const projectBtn = app.chrome.crumb(T('Project  (alt+p): switches to its last board'), () => openProject(app));
  const boardBtn = app.chrome.crumb(T('Board  (B)'), () => pickBoard());
  const sprintBtn = app.chrome.crumb(T('View: sprint, backlog, whole board, your views  (v, [ ])'), () => pickSprint());
  const modeBtn = h('button.btn', { title: T('Lanes / list  (t)'), onclick: () => setMode(S.mode === 'lanes' ? 'list' : 'lanes') });
  const swimBtn = h('button.btn', { title: T('Swimlanes: none, assignee, epic, priority  (O)'), onclick: () => cycleSwim() });
  const colsBtn = h('button.btn', { title: T('List columns  (C)'), onclick: () => pickCols() }, icon('columns-3'), T('Columns'), keyHint('C'));
  const compactBtn = h('button.btn', { title: T('Compact (one-line) cards  (c)'), 'aria-pressed': 'false', onclick: () => setCompact(!S.compact) });
  const emptyBtn = h('button.btn', { title: T('Hide / show empty lanes  (alt+e)'), 'aria-pressed': 'false', onclick: () => setHideEmpty(!S.hideEmpty) });
  const layoutBtn = h('button.btn', { title: T('Lane layout: the board\'s columns or a ui.lane_layouts entry  (alt+l)'), onclick: () => cycleLayout() });
  const refreshBtn = h('button.btn.ghost.bd-refresh', { title: T('Refresh  (r)'), 'aria-label': T('Refresh'), onclick: () => refresh(true) }, h('span.ico-spin', { 'aria-hidden': 'true' }, icon('refresh-cw')));
  if (!embed) context.append(projectBtn, boardBtn, sprintBtn, sprintEl);
  toolbar.append(h('span.spacer'), layoutBtn, swimBtn, colsBtn, emptyBtn, compactBtn, modeBtn, refreshBtn);

  const keyIcon = (b, spec, ...a) => { setIcon(b, ...a); if (!b.querySelector('.btn-key')) b.append(keyHint(spec)); };
  function renderToolbar() {
    app.chrome.label(projectBtn, S.project || '…');
    app.chrome.label(boardBtn, S.board ? S.board.Name : '…');
    const sp = scopeLabel();
    sprintBtn.textContent = sp;
    sprintBtn.hidden = !isScrum() && !viewItems().some(i => i.id.includes(':') && !i.id.startsWith('closed'));
    if (S.mode === 'lanes') keyIcon(modeBtn, 't', 'kanban', T('Lanes')); else keyIcon(modeBtn, 't', 'list', T('List'));
    swimBtn.hidden = S.mode !== 'lanes'; colsBtn.hidden = S.mode !== 'list'; compactBtn.hidden = S.mode !== 'lanes'; emptyBtn.hidden = S.mode !== 'lanes';
    keyIcon(swimBtn, 'O', 'rows-3', S.swim === 'none' ? T('Swimlanes') : T('by %s', SWIMNAME[S.swim]));
    if (S.compact) keyIcon(compactBtn, 'c', 'fold-vertical', T('Compact')); else keyIcon(compactBtn, 'c', 'unfold-vertical', T('Full'));
    compactBtn.setAttribute('aria-pressed', S.compact ? 'true' : 'false');
    keyIcon(emptyBtn, 'alt+e', S.hideEmpty ? 'eye-off' : 'eye', S.hideEmpty ? (S.hidden ? T('%d empty hidden', S.hidden) : T('Empty hidden')) : T('Empty shown'));
    emptyBtn.setAttribute('aria-pressed', S.hideEmpty ? 'true' : 'false');
    const L = activeLayout();
    layoutBtn.hidden = S.mode !== 'lanes' || !layouts().length;
    keyIcon(layoutBtn, 'alt+l', 'layout-panel-left', L ? L.Name + (S.layoutHidden ? ' · ' + T('%d hidden', S.layoutHidden) : '') : T('Columns'));
  }
  const isScrum = () => !!S.board && S.board.Type !== 'kanban';
  const sprints = () => (S.bundle && S.bundle.sprints) || [];
  // Everything [ ] walks through: sprints, backlog, whole board, then ui.views, starred filters and service desk queues.
  function viewItems() {
    const out = [];
    if (isScrum()) {
      sprints().forEach(s => out.push({ id: String(s.ID), name: s.Name, info: s.State }));
      out.push({ id: 'backlog', name: T('Backlog') }, { id: 'all', name: T('Whole board') });
    } else {
      out.push({ id: 'active', name: T('Board') });
      const cols = (S.bundle && S.bundle.config && S.bundle.config.Columns) || [];
      if (cols.length && cols[0].Name.toLowerCase() === 'backlog') out.push({ id: 'backlog', name: T('Backlog') });
    }
    ((app.session.ui && app.session.ui.Views) || []).forEach((v, i) => { if (v.Name && v.JQL) out.push({ id: 'view:' + i, name: v.Name, info: T('view'), jql: v.JQL, kind: 'jql' }); });
    for (const f of S.saved) out.push({ id: 'filter:' + f.ID, name: f.Name, info: T('saved filter'), jql: f.JQL, kind: 'filter' });
    for (const qu of S.queues) out.push({ id: 'queue:' + qu.Desk + ':' + qu.ID, name: qu.Project + ' · ' + qu.Name, info: T('queue · %d', qu.Count), jql: qu.JQL, kind: 'filter' });
    // A named query opened from elsewhere (a release's issues): ?sprint=jql:…&vname=…
    if (String(query.sprint || '').startsWith('jql:') && !out.some(i => i.id === query.sprint)) out.push({ id: query.sprint, name: query.vname || 'JQL', info: T('query'), jql: query.sprint.slice(4), kind: 'filter' });
    for (const q of S.starred) out.push({ id: 'star:' + q, name: '★ ' + (q.length > 30 ? q.slice(0, 29) + '…' : q), info: T('starred search'), jql: q, kind: 'filter' });
    return out;
  }
  function scopeLabel() {
    if (S.closed) return T('%s (closed)', S.closed.Name);
    const it = viewItems().find(i => i.id === S.scope);
    if (it && (it.kind || !isScrum())) return it.name;
    if (S.scope === 'backlog') return T('Backlog');
    if (S.scope === 'all') return T('Whole board');
    const r = resolveScope();
    const sp = r.sprint && sprints().find(s => s.ID === r.sprint);
    return sp ? sp.Name : T('Whole board');
  }

  // ---- filter bar
  function qfs() { return (S.bundle && S.bundle.quickFilters) || []; }
  const anyFilter = () => S.mine || S.who != null || S.qf.size || S.text.trim();
  function renderBar() {
    const kids = [];
    kids.push(h('button.fchip' + (S.mine ? '.on' : ''), { dataset: { act: 'mine' }, title: T('Assigned to me  (m)') }, h('kbd', 'm'), T('Mine')));
    kids.push(h('button.fchip' + (S.who != null ? '.on' : ''), { dataset: { act: 'who' }, title: T('Assignee  (A)') }, h('kbd', 'A'), S.who == null ? T('Assignee') : whoLabel(S.who, whoName)));
    qfs().slice(0, 9).forEach((q, i) => kids.push(h('button.fchip' + (S.qf.has(q.ID) ? '.on' : ''), { dataset: { qf: q.ID }, title: q.JQL }, h('kbd', i + 1), q.Name)));
    cq.words(S.text).forEach((w, i) => kids.push(h('button.fchip.term', { dataset: { term: i }, title: T('Remove %s', w) }, w, icon('x'))));
    if (anyFilter()) kids.push(h('button.fchip.clear', { dataset: { act: 'clear' }, title: T('Clear filters  (0)') }, icon('x'), T('clear'), keyHint('0')));
    clear(chips).append(...kids);
  }
  const whoName = id => {
    if (id === '-') return T('Unassigned');
    const c = S.cards.find(c => who(c) === id);
    return c ? c.Assignee : (S.people.get(id) || id);
  };
  // The project's assignable people, fetched once per project: names for a remembered filter's people without cards.
  const peopleCache = (app._boardPeople = app._boardPeople || new Map());
  async function loadPeople() {
    const p = S.project;
    if (!p) return;
    if (!peopleCache.has(p)) peopleCache.set(p, api.get('/users?project=' + encodeURIComponent(p)).catch(() => []));
    const us = await peopleCache.get(p);
    if (p !== S.project) return;
    S.people = new Map((us || []).map(u => [u.AccountID, u.DisplayName]));
  }
  delegate(chips, 'click', 'button', (e, b) => {
    if (b.dataset.term != null) { filterIn.value = cq.removeTerm(S.text, Number(b.dataset.term)); setText(filterIn.value); }
    else if (b.dataset.qf) toggleQF(Number(b.dataset.qf));
    else if (b.dataset.act === 'mine') toggleMine();
    else if (b.dataset.act === 'who') pickWho();
    else clearFilters();
  });
  const env = () => ({ me, pins: S.pins, notes: S.notes, now: Date.now() });
  const setText = t => { S.text = t; S.textFn = cq.compile(t, env()); layout(); renderBar(); };
  const onText = debounce(() => setText(filterIn.value), 70);
  filterIn.addEventListener('input', onText);
  filterIn.addEventListener('keydown', e => {
    if (e.key === 'Enter' || e.key === 'Escape') { e.stopPropagation(); if (e.key === 'Escape' && filterIn.value) { filterIn.value = ''; onText(); } filterIn.blur(); }
  });

  // The sprint's share done (points, else issues), days and workdays left and goal (TUI jiraSprintBar, jiraSprintLine).
  function renderSprint() {
    const id = resolveScope().sprint, sp = S.closed || (id && sprints().find(s => s.ID === id));
    if (!embed) context.parentElement.classList.toggle('bd-sprinted', !!sp);
    if (!sp) { clear(sprintEl); return; }
    const now = Date.now(), DAY = 864e5, start = date(sp.Start), end = date(sp.End), closed = date(sp.Complete);
    const kids = [];
    if (!S.closed) {
      const pointed = S.cards.some(c => c.Points);
      let done = 0, total = 0;
      for (const c of S.cards) { const n = pointed ? Number(c.Points) || 0 : 1; total += n; if (c.Done) done += n; }
      if (total) kids.push(h('span.bd-prog', { title: T('%d%% done', Math.round(done / total * 100)), style: { '--pct': done / total * 100 + '%' } }), (Math.round(done * 10) / 10) + '/' + (Math.round(total * 10) / 10) + (pointed ? 'p' : ''));
    }
    const left = end && Math.ceil((end.getTime() - now) / DAY);
    let when = '';
    if (closed) when = T('closed %s', shortDate(sp.Complete));
    else if (start && start.getTime() > now) when = T('starts %s', shortDate(sp.Start));
    else if (left > 0) {
      const wd = workdays(app), last = new Date(end); last.setHours(0, 0, 0, 0);
      let n = 0;
      for (const d = new Date(now); d.setHours(0, 0, 0, 0) < last.getTime(); d.setDate(d.getDate() + 1)) if (wd.includes(d.getDay())) n++;
      when = Tn(n, '%dd left · %d workday', '%dd left · %d workdays', left, n);
    } else if (end) when = T('ended %s', shortDate(sp.End));
    if (when) kids.push(h('span.bd-when', when));
    const goal = (sp.Goal || '').trim().split(/\s+/).join(' ');
    if (goal) kids.push(h('span.bd-goal', { title: goal }, goal));
    clear(sprintEl).append(...kids);
  }
  function renderStats() {
    renderSprint();
    const vis = S.visible;
    let pts = 0;
    for (const c of vis) pts += Number(c.Points) || 0;
    const filtered = vis.length !== S.cards.length;
    stats.textContent = (filtered ? T('%d of %d issues', vis.length, S.cards.length) : Tn(S.cards.length, '%d issue', '%d issues', S.cards.length)) + (pts ? ' · ' + T('%s pts', Math.round(pts * 10) / 10) : '') + (S.total > S.cards.length ? ' ' + T('(%d in Jira)', S.total) : '');
  }

  // ---- data
  function resolveScope() {
    const sps = sprints();
    if (S.closed) return { sprint: S.closed.ID };
    const vi = viewItems().find(i => i.id === S.scope);
    if (vi && vi.kind) return { view: vi };
    if (S.scope === 'backlog') return { backlog: 1 };
    if (!isScrum()) return { kanban: 1 };
    if (S.scope === 'all') return {};
    if (S.scope === 'active') {
      const a = sps.find(s => s.State === 'active') || sps[0];
      return a ? { sprint: a.ID } : {};
    }
    return { sprint: Number(S.scope) };
  }
  // A new issue joins the open sprint the board shows (TUI createSprint).
  function createOpts() {
    const r = S.closed || S.past ? {} : resolveScope();
    const sp = r.sprint && sprints().find(s => s.ID === r.sprint && s.State !== 'closed');
    return { project: S.project, sprint: sp ? sp.ID : 0 };
  }
  // The view shown, for the team standup to walk (TUI: the standup walks the board's current view).
  function rememberView() {
    if (!S.board || S.past || embed) return;
    const r = resolveScope(), p = new URLSearchParams();
    if (r.sprint) p.set('sprint', r.sprint);
    else if (r.backlog) p.set('backlog', 1);
    else if (r.view) { p.set('kind', r.view.kind); p.set('jql', r.view.jql); }
    app.lastView = { board: S.board.ID, query: p.toString(), park: r.sprint ? String(r.sprint) : r.view ? r.view.name : r.backlog ? 'backlog' : '', name: scopeLabel() };
  }
  function cardsPath() {
    const p = new URLSearchParams(), r = resolveScope();
    if (r.sprint) p.set('sprint', r.sprint);
    if (r.backlog) p.set('backlog', 1);
    if (r.kanban) p.set('kanban', 1);
    const jql = qfs().filter(q => S.qf.has(q.ID)).map(q => '(' + q.JQL + ')').join(' AND ');
    if (r.view) {
      p.set('kind', r.view.kind); p.set('jql', r.view.jql);
      if (jql) p.set('filter', jql);
      return '/boards/' + S.board.ID + '/viewcards?' + p;
    }
    if (jql) p.set('jql', jql);
    const s = p.toString();
    return '/boards/' + S.board.ID + '/cards' + (s ? '?' + s : '');
  }
  function setBusy(d) { S.busy += d; refreshBtn.classList.toggle('busy', S.busy > 0); }

  // Show cached data at once, then the fresh answer.
  function loadCards() {
    loadPeople().then(renderBar);
    rememberView();
    const path = cardsPath();
    if (path === S.path) return;
    S.path = path;
    setBusy(1);
    api.swr(path, d => { if (S.path === path && !S.dead) setCards(d); })
      .catch(e => { if (S.path === path) fail(e); })
      .finally(() => { setBusy(-1); S.fetched = Date.now(); });
  }
  // A quiet refetch: no spinner in the lanes, only changed cards touched.
  function refresh(loud) {
    if (!S.board || S.dead) return Promise.resolve();
    const path = S.path || cardsPath();
    S.path = path;
    setBusy(1);
    let dropped = false;
    const reqs = [api.get(path, { fresh: true }).then(d => { if (S.path === path && !S.dead) setCards(d); })];
    // Quiet too on a scrum board: a sprint closed or started elsewhere shows.
    if (loud || isScrum()) reqs.push(api.get('/boards/' + S.board.ID, { fresh: true }).then(b => { if (!S.dead && (loud || sprintsMoved(b))) dropped = setBundle(b); }));
    return Promise.all(reqs)
      .then(() => { S.fetched = Date.now(); if (loud && !dropped) ui.toast(T('Refreshed')); })
      .catch(e => { if (loud || !S.loaded) fail(e); })
      .finally(() => setBusy(-1));
  }
  function fail(e) {
    if (S.loaded) ui.errToast(e);
    else bdMsg(h('div', h('h2', T('Could not load the board')), h('pre', e.message)));
  }
  const sprintsMoved = b => JSON.stringify((b && b.sprints) || []) !== JSON.stringify(sprints());
  function setBundle(b, meta) {
    const first = !S.bundle, prev = sprints();
    S.bundle = b;
    if (b && b.quickFilters) for (const id of S.qf) if (!b.quickFilters.some(q => q.ID === id)) S.qf.delete(id); // a remembered one since removed
    const dropped = !(meta && meta.cached) && dropClosed(prev);
    if (first && UI.CardColors !== 'off') {
      api.swr('/boards/' + S.board.ID + '/cardcolors?scope=' + encodeURIComponent('project = ' + S.project), c => { S.colors = c; for (const p of S.panes) p.vl.refresh(); }).catch(() => {});
    }
    registerQFCommands();
    renderToolbar(); renderBar();
    if (first || S.scope === 'active') loadCards();
    layout();
    return dropped;
  }
  // The sprint shown has closed (or is gone): the active one takes its place.
  function dropClosed(prev) {
    if (S.closed || !isScrum() || !/^\d+$/.test(S.scope) || sprints().some(s => String(s.ID) === S.scope)) return false;
    const was = prev.find(s => String(s.ID) === S.scope);
    if (S.past) leavePast(true);
    S.scope = 'active';
    if (!embed) app.setQuery({ sprint: null });
    ui.toast(was ? T('%s has closed  ·  showing the active sprint', was.Name) : T('That sprint has closed  ·  showing the active sprint'));
    return true;
  }
  function setCards(d) {
    const list = d.cards || [];
    if (S.loaded && sameCards(S.cards, list)) { S.total = d.total || list.length; return; }
    S.cards = list; S.total = d.total || list.length; S.loaded = true;
    layout();
    renderBar();
    if (S.past && !S.past.moves && !S.past.fetching) { S.past.fetching = true; fetchMoves(); }
  }
  function sameCards(a, b) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) if (a[i] !== b[i] && JSON.stringify(a[i]) !== JSON.stringify(b[i])) return false;
    return true;
  }

  // ---- filtering and layout
  function passes(c) {
    if (S.mine && c.AssigneeID !== me) return false;
    if (!passesWho(S.who, c)) return false;
    if (S.textFn && !S.textFn(c)) return false;
    return true;
  }
  const CMP = comparators(c => colIndexOf(c));
  function columns() {
    const L = activeLayout();
    return L ? L.Lanes : (S.bundle && S.bundle.config && S.bundle.config.Columns) || [];
  }
  // ui.lane_layouts fitting the board, arranged by the server as the TUI does (internal/lanes): lanes shaped as
  // columns, with the columns stacked in them as Sections, and the statuses whose columns they hide.
  const layouts = () => (S.bundle && S.bundle.layouts) || [];
  const activeLayout = () => (S.layout && layouts().find(l => l.Name === S.layout)) || null;
  function cycleLayout() {
    const ls = layouts();
    if (!ls.length) return ui.toast(T('No lane layout fits this board: add one to ui.lane_layouts'));
    const i = ls.findIndex(l => l.Name === S.layout);
    S.layout = i < 0 ? ls[0].Name : i + 1 < ls.length ? ls[i + 1].Name : '';
    app.prefs.set('board.layout.' + S.board.ID, S.layout);
    if (S.mode !== 'lanes') setMode('lanes');
    layout(); renderToolbar();
    ui.toast(S.layout ? (S.layoutHidden ? T('Lanes: %s, %d hidden', S.layout, S.layoutHidden) : T('Lanes: %s', S.layout)) : T('Lanes: the board\'s columns'));
  }
  function statusCol() {
    const m = new Map();
    columns().forEach((c, i) => (c.StatusIDs || []).forEach(id => m.set(String(id), i)));
    return m;
  }
  let sc = new Map();
  const colIndexOf = c => { const i = sc.get(String(c.StatusID)); return i == null ? columns().length : i; };

  // ---- the time machine: the cards where their status changes had them at the end of a day
  const asOf = p => {
    if (p.at) return p.at;
    const n = new Date();
    return new Date(n.getFullYear(), n.getMonth(), n.getDate() - p.days + 1).getTime();
  };
  function statusAt(moves, t, cur) {
    if (!moves || !moves.length) return cur;
    for (let i = moves.length - 1; i >= 0; i--) if (Date.parse(moves[i].When) <= t) return moves[i].To;
    return moves[0].From;
  }
  function shownCards() {
    const p = S.past;
    if (!p || !p.moves) return S.cards;
    const at = asOf(p), last = columns().length - 1, names = (S.bundle && S.bundle.statusNames) || {};
    const out = [];
    for (const c of S.cards) {
      if (!isZero(c.Created) && Date.parse(c.Created) >= at) continue;
      const id = statusAt(p.moves[c.Key], at, c.StatusID);
      if (String(id) === String(c.StatusID)) { out.push(c); continue; }
      const col = sc.get(String(id));
      out.push({ ...c, StatusID: String(id), Status: names[id] || c.Status, Done: col === last, InProgress: col != null && col > 0 && col < last });
    }
    return out;
  }

  // ---- swimlanes: bands by assignee, epic or priority
  const groupName = c => (S.swim === 'assignee' ? c.Assignee || T('Unassigned') : S.swim === 'epic' ? c.ParentSummary || T('No epic') : c.Priority || T('No priority'));
  function groupsOf(cards) {
    const m = new Map();
    for (const c of cards) { const n = groupName(c); (m.get(n) || m.set(n, []).get(n)).push(c); }
    const out = [...m].map(([name, list]) => ({ name, cards: list }));
    const last = S.swim === 'assignee' ? T('Unassigned') : S.swim === 'epic' ? T('No epic') : T('No priority');
    out.sort((a, b) => (a.name === last) - (b.name === last)
      || (S.swim === 'priority' ? prioOrd(a.cards[0]) - prioOrd(b.cards[0]) : 0) || a.name.localeCompare(b.name));
    return out;
  }

  function layout() {
    if (!S.bundle || !S.loaded || S.dead) return;
    sc = statusCol();
    const L = activeLayout(), hid = new Set(L ? L.Hidden : []), shown = shownCards();
    const cols = columns(), base = S.base = hid.size ? shown.filter(c => !hid.has(String(c.StatusID))) : shown, vis = base.filter(passes);
    if (shown.length - base.length !== S.layoutHidden) { S.layoutHidden = shown.length - base.length; renderToolbar(); }
    S.visible = vis;
    const lanes = S.mode === 'lanes', swim = lanes && S.swim !== 'none';
    const other = lanes && base.some(c => !sc.has(String(c.StatusID)));
    const groups = swim ? groupsOf(vis) : null;
    const sig = S.mode + '|' + (L ? L.Name + ':' : '') + cols.map(c => c.Name).join(',') + (other ? '|other' : '') + (swim ? '|swim:' + S.swim + ':' + groups.map(g => g.name).join('\x1e') : '') + (lanes ? '' : '|' + S.cols.join(','));
    root.classList.toggle('compact', S.compact);
    if (sig !== S.built) build(sig, lanes, cols, other, groups);
    if (swim) {
      const nc = S.ncols;
      S.colTotal.fill(0);
      S.panes.forEach(p => {
        const g = groups[p.group];
        p.all = []; p.total = 0; p.folded = S.fold.has(g.name);
      });
      for (const c of base) S.colTotal[Math.min(colIndexOf(c), nc - 1)]++;
      groups.forEach((g, gi) => { for (const c of g.cards) S.panes[gi * nc + Math.min(colIndexOf(c), nc - 1)].all.push(c); });
    } else if (lanes) {
      for (const p of S.panes) { p.all = []; p.total = 0; p.heads = null; p.shown = null; p.folded = S.laneFold.has(p.name); p.el.classList.toggle('collapsed', p.folded); }
      for (const c of base) S.panes[Math.min(colIndexOf(c), S.panes.length - 1)].total++;
      for (const c of vis) S.panes[Math.min(colIndexOf(c), S.panes.length - 1)].all.push(c);
      for (const p of S.panes) stack(p, cols[p.col]);
    } else {
      const l = sortCards(vis, CMP, S.sort, S.dir);
      S.panes[0].all = l; S.panes[0].total = base.length;
      S.panes[0].heads = groupHeads(l);
    }
    for (const p of S.panes) p.cards = p.folded ? [] : p.shown || p.all;
    S.where.clear();
    for (const p of S.panes) p.cards.forEach((c, i) => S.where.set(c.Key, { p, i }));
    if (S.sel && !S.where.has(S.sel)) S.sel = null;
    const rh = swim ? 0 : probeHeight(lanes);
    for (const p of S.panes) {
      if (p.heads) { p.rh = rh; const gh = lanes ? 26 : Math.round(rh * 0.85); p.body.style.setProperty('--gh', gh + 'px'); p.vl.setRowHeight(i => rh + (p.heads[i] ? gh : 0)); p.grouped = true; }
      else if (rh !== p.rh || p.grouped) { p.rh = rh; p.grouped = false; p.vl.setRowHeight(rh); }
      p.vl.setCount(p.cards.length);
      if (!swim) paintHead(p);
    }
    if (swim) paintSwim(groups);
    hideEmptyLanes(swim);
    renderStats(); renderBanner();
  }

  // A layout's lane of several columns stacks them: its cards by column, rank kept within each, a header over each
  // column's first (TUI jiraLane.stack). An empty column draws no header. A folded column (z, a click on its
  // header) leaves p.shown for the strip under the lane's head; the TUI draws its header in place.
  const secKey = (lane, sec) => lane + '\x1f' + sec;
  function stack(p, col) {
    const secs = (col && col.Sections) || [];
    if (p.folds) { p.folds.hidden = true; p.folds.replaceChildren(); }
    if (secs.length < 2) return;
    const sec = c => secs.findIndex(s => (s.StatusIDs || []).includes(String(c.StatusID)));
    p.all = p.all.map((c, i) => [sec(c), i, c]).sort((a, b) => a[0] - b[0] || a[1] - b[1]).map(x => x[2]);
    const name = k => (k >= 0 ? secs[k].Name : T('Other')), folded = k => S.secFold.has(secKey(p.name, name(k)));
    const n = new Map();
    for (const c of p.all) n.set(sec(c), (n.get(sec(c)) || 0) + 1);
    p.shown = p.all.filter(c => !folded(sec(c)));
    p.heads = new Array(p.shown.length).fill(null);
    p.shown.forEach((c, i) => {
      const k = sec(c);
      if (i === 0 || k !== sec(p.shown[i - 1])) p.heads[i] = { name: name(k), n: n.get(k) };
    });
    const strip = [...n.keys()].filter(folded).map(k => h('button.bd-fold', { type: 'button', dataset: { sec: name(k), lane: p.name }, title: T('Unfold  (Z: all)') }, '▸ ' + name(k) + ' · ' + n.get(k)));
    if (p.folds && strip.length) { p.folds.hidden = false; p.folds.append(...strip); }
  }
  function secHead(w, head, lane) {
    w.classList.toggle('shead', !!head);
    if (!head) { if (w._sec) w._sec.hidden = true; return; }
    if (!w._sec) w.append(w._sec = h('div.bd-sec', { title: T('Fold  (z)') }));
    w._sec.hidden = false;
    w._sec.dataset.sec = head.name; w._sec.dataset.lane = lane;
    w._sec.textContent = head.name + ' · ' + head.n;
  }
  function toggleSection(lane, sec) {
    const k = secKey(lane, sec), keep = S.sel;
    S.secFold.has(k) ? S.secFold.delete(k) : S.secFold.add(k);
    app.prefs.set('board.secfold.' + S.board.ID, [...S.secFold].join('\n'));
    layout();
    if (keep && !S.where.has(keep)) {
      S.sel = null;
      const p = S.panes.find(q => q.name === lane && q.cards.length) || S.panes.find(q => q.cards.length);
      if (p) select(p.cards[0].Key);
    }
  }

  // Columns the filters left without a card hide (alt+e, ui.empty_lanes:
  // hide), unless every one is; a card moved into one shows it again.
  function hideEmptyLanes(swim) {
    const was = S.hidden;
    const used = new Set(S.mode === 'lanes' ? S.panes.filter(p => p.all.length).map(p => p.col) : []);
    const off = col => S.hideEmpty && used.size > 0 && !used.has(col);
    for (const p of S.panes) if (S.mode === 'lanes') p.el.hidden = off(p.col);
    const n = swim ? S.ncols : S.mode === 'lanes' ? S.panes.length : 0;
    S.hidden = 0;
    for (let col = 0; col < n; col++) if (off(col)) S.hidden++;
    if (swim) {
      S.heads.forEach((hd, i) => { hd.el.hidden = off(i); });
      main.querySelector('.bd-swim').style.setProperty('--ncols', n - S.hidden);
    }
    if (S.hidden !== was) renderToolbar();
  }
  function setHideEmpty(v) {
    S.hideEmpty = v; app.prefs.set('board.empty_lanes', v ? 'hide' : 'show');
    layout(); renderToolbar();
    ui.toast(v ? (S.hidden ? Tn(S.hidden, '%d empty lane hidden', '%d empty lanes hidden', S.hidden) : T('Empty lanes hide once a filter leaves one')) : T('Empty lanes shown'));
  }

  // Row height is measured from a real card, so density and fonts decide it.
  function probeHeight(lanes) {
    const w = lanes ? buildCard() : buildRow();
    const c = { Key: 'X-1', Summary: 'Probe', Type: 'Task', Status: 'x', Priority: 'High', Points: '1', Labels: 'a', ParentKey: 'X-0', Assignee: 'A B', Subtasks: 1, Due: '2026-01-01T00:00:00Z' };
    lanes ? fillCard(w, c) : fillRow(w, c);
    w.style.cssText = 'position:absolute;visibility:hidden;left:0;right:0;top:0';
    // a lane hidden for being empty, or collapsed, has no layout, and would measure nothing
    const host = main.firstChild && main.firstChild.querySelector('.bd-lane:not([hidden]):not(.collapsed) > .bd-lane-body, .bd-lbody');
    (host || main).append(w);
    const hgt = Math.ceil(w.getBoundingClientRect().height) || (lanes ? 96 : 32);
    w.remove();
    return hgt;
  }

  // A list of rows without virtualisation, for a swimlane's small cells. Same surface as vlist.
  function plainList(body, o) {
    const rows = [];
    return {
      setCount(n) {
        while (rows.length < n) { const el = o.create(); el.classList.add('vl-row', 'vl-static'); body.append(el); rows.push(el); }
        while (rows.length > n) rows.pop().remove();
        rows.forEach((el, i) => o.bind(el, i));
      },
      setRowHeight() {},
      refresh(i) { if (i == null) rows.forEach((el, j) => o.bind(el, j)); else if (rows[i]) o.bind(rows[i], i); },
      scrollTo(i) { const el = rows[i]; if (el && el.scrollIntoView) el.scrollIntoView({ block: 'nearest', inline: 'nearest' }); },
      rowEl: i => rows[i] || null,
      destroy() { rows.length = 0; body.replaceChildren(); },
    };
  }

  // ---- building the lanes or list
  function laneNames(cols, other) {
    return cols.map((c, i) => ({ name: c.Name, max: c.Max || 0, col: i })).concat(other ? [{ name: T('Other'), max: 0, col: cols.length }] : []);
  }
  const catOf = (i, n, other) => (i === 0 ? 'new' : i === n - 1 && !other ? 'done' : 'prog');
  function build(sig, lanes, cols, other, groups) {
    for (const p of S.panes) p.vl.destroy();
    S.panes = []; S.built = sig; clear(main);
    if (lanes && groups) {
      const names = laneNames(cols, other);
      const wrap = h('div.bd-swim'); hwheel(wrap);
      wrap.style.setProperty('--ncols', names.length);
      S.ncols = names.length;
      S.heads = names.map((n, i) => {
        const hd = { max: n.max };
        hd.count = h('span.bd-count'); hd.wip = h('span.bd-wip');
        hd.el = h('div.bd-lane-head.k' + catOf(i, names.length, other), h('span.bd-lane-name', n.name), hd.count, hd.wip);
        return hd;
      });
      S.colTotal = names.map(() => 0);
      wrap.append(h('div.bd-swim-head', S.heads.map(x => x.el)));
      S.bands = groups.map((g, gi) => {
        const band = { name: g.name, caret: h('span.bd-caret'), count: h('span.bd-count') };
        band.head = h('div.bd-band-head', { dataset: { fold: g.name }, title: T('Fold / unfold  (z, Z)') }, band.caret, h('span.bd-band-name', g.name), band.count);
        const row = h('div.bd-band-row');
        names.forEach(n => {
          const pane = { col: n.col, group: gi, band: g.cards[0], name: n.name, cards: [], all: [], total: 0, rh: 0 };
          pane.body = h('div.bd-lane-body.static');
          pane.el = h('section.bd-lane.cell', { dataset: { pane: S.panes.length } }, pane.body);
          pane.vl = plainList(pane.body, { create: buildCard, bind: (w, j) => fillCard(w, pane.cards[j]) });
          S.panes.push(pane);
          row.append(pane.el);
        });
        band.el = h('section.bd-band', band.head, row);
        wrap.append(band.el);
        return band;
      });
      main.append(wrap);
    } else if (lanes) {
      const wrap = h('div.bd-lanes'); hwheel(wrap);
      const names = laneNames(cols, other);
      names.forEach((n, i) => {
        const pane = { col: n.col, name: n.name, max: n.max, cards: [], all: [], total: 0, rh: 0 };
        pane.count = h('span.bd-count');
        pane.wip = h('span.bd-wip');
        const fold = h('button.bd-lane-fold', { type: 'button', title: T('Collapse / expand'), 'aria-label': T('Collapse / expand %s', n.name) }, icon('chevron-right'));
        pane.head = h('div.bd-lane-head.k' + catOf(i, names.length, other), fold, h('span.bd-lane-name', n.name), pane.count, pane.wip);
        pane.body = h('div.bd-lane-body', { role: 'list', 'aria-label': n.name });
        pane.drop = h('div.bd-drop', { hidden: true });
        pane.body.append(pane.drop);
        pane.el = h('section.bd-lane', { dataset: { pane: i }, 'aria-label': n.name }, pane.head, pane.body);
        pane.folds = h('div.bd-folds', { hidden: true });
        pane.el.insertBefore(pane.folds, pane.body);
        pane.vl = vlist(pane.body, { rowHeight: 96, create: buildCard, bind: (w, j) => { fillCard(w, pane.cards[j]); secHead(w, pane.heads && pane.heads[j], pane.name); } });
        S.panes.push(pane);
        wrap.append(pane.el);
      });
      main.append(wrap);
    } else {
      const pane = { col: -1, name: 'list', cards: [], all: [], total: 0, rh: 0 };
      pane.head = listHead(S.cols, S.sort, S.dir);
      dragCols(pane.head, () => S.cols, setCols);
      pane.body = h('div.bd-lbody');
      pane.drop = h('div.bd-drop', { hidden: true });
      pane.body.append(pane.drop);
      pane.vl = vlist(pane.body, { rowHeight: 32, create: buildRow, bind: (w, j) => fillRow(w, pane.cards[j], pane.heads && pane.heads[j]) });
      S.panes.push(pane);
      const list = h('div.bd-list', pane.head, pane.body);
      const grid = () => list.style.setProperty('--lcols', gridCols(S.cols, S.colw));
      grid();
      resizeCols(pane.head, { widths: S.colw, apply: grid, save: () => app.prefs.set('board.colw', JSON.stringify(S.colw)) });
      pane.el = list;
      main.append(list);
    }
  }
  function paintHead(p) {
    if (S.mode !== 'lanes') {
      paintListHead(p.head, S.sort, S.dir);
      return;
    }
    const filtered = p.all.length !== p.total;
    p.count.textContent = filtered ? p.all.length + '/' + p.total : p.total;
    const over = p.max && p.total > p.max;
    p.wip.textContent = p.max ? T('max %d', p.max) : '';
    p.head.classList.toggle('over', !!over);
    p.wip.title = over ? T('Over the WIP limit of %d', p.max) : T('WIP limit');
    p.body.classList.toggle('empty', !(p.shown ? p.all : p.cards).length);
  }
  function paintSwim(groups) {
    S.heads.forEach((hd, i) => {
      const shown = S.panes.reduce((n, p) => n + (p.col === i ? p.all.length : 0), 0);
      hd.count.textContent = shown !== S.colTotal[i] ? shown + '/' + S.colTotal[i] : S.colTotal[i];
      const over = hd.max && S.colTotal[i] > hd.max;
      hd.wip.textContent = hd.max ? T('max %d', hd.max) : '';
      hd.el.classList.toggle('over', !!over);
    });
    S.bands.forEach((b, gi) => {
      const f = S.fold.has(b.name);
      setIcon(b.caret, f ? 'chevron-right' : 'chevron-down');
      b.count.textContent = groups[gi].cards.length;
      b.el.classList.toggle('folded', f);
    });
    for (const p of S.panes) p.body.classList.toggle('empty', !p.all.length);
  }

  // A list sorted by assignee, priority, epic or status heads each group with its name, count and points
  // (TUI jiraGroupHeader); heads[i] is the header on row i, null when sorted otherwise.
  const listGroup = {
    assignee: c => c.Assignee || T('Unassigned'), priority: c => c.Priority || T('No priority'),
    epic: c => c.ParentSummary || T('No epic'), status: c => c.Status,
  };
  function groupHeads(l) {
    const g = listGroup[S.sort];
    if (!g) return null;
    const heads = new Array(l.length).fill(null);
    let at = -1;
    l.forEach((c, i) => {
      const name = g(c);
      if (at < 0 || heads[at].name !== name) heads[at = i] = { name, n: 0, pts: 0 };
      heads[at].n++; heads[at].pts = Math.round((heads[at].pts + Math.max(0, num(c))) * 100) / 100;
    });
    return heads;
  }

  // ---- cards
  const hourTick = () => Math.floor(Date.now() / 36e5);
  const tmark = key => (app.timer ? app.timer.mark(key) : '');
  const ribbonOf = c => {
    const cc = S.colors;
    if (!cc || !cc.By) return '';
    if (cc.By === 'custom') return (cc.Keys && cc.Keys[c.Key]) || '';
    const v = cc.By === 'priority' ? c.Priority : cc.By === 'issuetype' ? c.Type : cc.By === 'assignee' ? c.Assignee : '';
    const hit = v && (cc.Colors || []).find(x => x.Value.toLowerCase() === v.toLowerCase());
    return hit ? hit.Color : '';
  };
  const hlOf = k => (app.highlights && app.highlights.has(k) ? app.highlights.get(k) : null); // a rule's highlight colour, '' for the theme's
  const sigOf = c => [c.Key, c.Summary, c.Type, c.Status, c.Priority, c.AssigneeID, c.AvatarURL, c.Points, c.ParentKey, c.Subtasks, c.SubtasksDone, c.Due, c.Done, c.Flagged, c.InProgress, c.PR, c.Deploy, c.Labels, c.Updated, c.Extra, S.sel === c.Key, S.marks.has(c.Key), S.pins.has(c.Key), hlOf(c.Key), ribbonOf(c), hourTick(), tmark(c.Key)].join('|');
  const fdate = (t, fallback) => (DF ? goDate(t, DF) : fallback);

  const buildCard = () => buildCardEl(LAYOUT);
  function fillCard(w, c) {
    if (!c) return;
    const sig = sigOf(c);
    if (w._sig === sig) return;
    w._sig = sig;
    fillCardEl(w, c, {
      look: LOOK && LOOK(c), sel: S.sel === c.Key, marked: S.marks.has(c.Key), pinned: k => S.pins.has(k), hl: hlOf, tmark,
      ribbon: ribbonOf(c), fdate, stamp: (e, k) => app.agents && app.agents.stamp(e, k),
    });
  }

  const buildRow = () => listRow(S.cols);
  const cellCtx = { marked: k => S.marks.has(k), pinned: k => S.pins.has(k), hl: hlOf, tmark, fdate, stamp: (e, k) => app.agents && app.agents.stamp(e, k) };
  function fillRow(w, c, head) {
    if (!c) return;
    const ht = head ? head.name + ' · ' + head.n + (head.pts > 0 ? ' · ' + head.pts + 'p' : '') : '';
    const sig = sigOf(c) + '|' + ht;
    if (w._sig === sig) return;
    w._sig = sig;
    const r = w._r;
    w.dataset.key = c.Key;
    w.className = 'vl-row lrow ' + catClass(c) + (S.sel === c.Key ? ' sel' : '') + (S.marks.has(c.Key) ? ' mark' : '') + (c.Flagged ? ' flagged' : '') + (head ? ' ghead' : '');
    if (head) r.ghead.replaceChildren(S.sort === 'assignee' && head.name !== T('Unassigned') ? ui.avatar(head.name, c.AvatarURL, 18) : '', h('span', ht));
    const rib = ribbonOf(c);
    if (rib) w.style.setProperty('--ribbon', rib); else w.style.removeProperty('--ribbon');
    w.classList.toggle('ribbon', !!rib);
    const lk = LOOK && LOOK(c);
    for (const k of ['edge', 'tint']) if (lk && lk[k]) w.style.setProperty('--' + k, colour(lk[k])); else w.style.removeProperty('--' + k);
    w.classList.toggle('edged', !!(lk && lk.edge)); w.classList.toggle('tinted', !!(lk && lk.tint));
    w.classList.toggle('faded', !!(lk && lk.fade)); w.classList.toggle('bold', !!(lk && lk.bold));
    fillCells(w, S.cols, c, cellCtx);
  }

  // ---- selection and navigation
  function rebind(key) { const w = S.where.get(key); if (w) w.p.vl.refresh(w.i); }
  const openIssue = key => app.panel.open(key, { card: S.cards.find(c => c.Key === key) });
  // The panel follows the cursor once it rests, unless it went elsewhere meanwhile ([ ], a link).
  const followPanel = debounce(was => { if (S.sel && app.panel.key && app.panel.key === was && app.panel.key !== S.sel) openIssue(S.sel); }, 110);
  const openPanel = () => followPanel(app.panel.key);
  function select(key, { scroll = true, row = true, anchor = true } = {}) {
    const old = S.sel;
    S.sel = key;
    if (anchor) S.anchor = key;
    if (old && old !== key) rebind(old);
    if (key) {
      const w = S.where.get(key);
      if (w) { if (row) S.rowMem = w.i; if (scroll) w.p.vl.scrollTo(w.i); w.p.vl.refresh(w.i); }
      openPanel();
      prefetch();
    }
  }
  // Once the cursor rests, the cards either side of it load, so opening one shows it at once (TUI prefetch.go).
  const prefetch = debounce(() => {
    const w = S.sel && S.where.get(S.sel);
    if (!w || S.dead) return;
    for (let d = 1; d <= 2; d++) for (const i of [w.i + d, w.i - d]) { const c = w.p.cards[i]; if (c) api.warm('/issues/' + c.Key); }
  }, 300);
  const cur = () => S.where.get(S.sel);
  const curCard = () => (cur() ? cur().p.cards[cur().i] : null);
  const cardEl = key => { const w = S.where.get(key); const e = w && w.p.vl.rowEl(w.i); return e ? e.querySelector('.card') || e : null; };
  const swimming = () => S.mode === 'lanes' && S.swim !== 'none';
  function move(dx, dy) {
    const w = cur();
    if (!w) {
      const p = S.panes.find(p => p.cards.length);
      if (p) { S.rowMem = 0; select(p.cards[0].Key); }
      return;
    }
    if (dy) {
      const n = w.p.cards.length, i = w.i + dy;
      if (swimming() && Math.abs(dy) === 1 && (i < 0 || i >= n)) {
        // past the end of a cell: the same column in the next band that has cards
        for (let g = w.p.group + dy; g >= 0 && g < S.panes.length / S.ncols; g += dy) {
          const q = S.panes[g * S.ncols + w.p.col];
          if (q.cards.length) return select(dy > 0 ? q.cards[0].Key : q.cards[q.cards.length - 1].Key);
        }
        return;
      }
      return select(w.p.cards[Math.max(0, Math.min(n - 1, i))].Key);
    }
    if (dx && S.mode === 'lanes') {
      const P = swimming() ? S.panes.filter(q => q.group === w.p.group) : S.panes;
      let pi = P.indexOf(w.p) + dx;
      while (P[pi] && !P[pi].cards.length) pi += dx;
      const p = P[pi];
      if (p) select(p.cards[Math.min(S.rowMem, p.cards.length - 1)].Key, { row: false });
    }
  }
  function edge(end) {
    const w = cur(); const p = w ? w.p : S.panes.find(p => p.cards.length);
    if (p && p.cards.length) select(p.cards[end ? p.cards.length - 1 : 0].Key);
  }
  // } {: the next or previous swimlane's first card, in the cursor's column else the nearest one holding
  // one; in a grouped list, the next or previous group's (TUI jumpJiraBand).
  function jumpBand(d) {
    const w = cur();
    if (!w) return move(0, 1);
    if (S.mode === 'list') {
      const heads = w.p.heads;
      if (!heads) return ui.toast(T('Bands need swimlanes or a grouped list  (O)'));
      let i = w.i;
      while (i > 0 && !heads[i]) i--;
      do i += d; while (i > 0 && i < heads.length && !heads[i]);
      if (i >= 0 && i < w.p.cards.length) select(w.p.cards[i].Key);
      return;
    }
    if (!swimming()) return ui.toast(T('Bands need swimlanes or a grouped list  (O)'));
    const at = S.panes.indexOf(w.p) % S.ncols;
    for (let g = w.p.group + d; g >= 0 && g < S.panes.length / S.ncols; g += d) {
      for (let k = 0; k < 2 * S.ncols; k++) {
        const col = at + Math.ceil(k / 2) * (k % 2 ? 1 : -1), q = col >= 0 && col < S.ncols && S.panes[g * S.ncols + col];
        if (q && q.cards.length) return select(q.cards[0].Key);
      }
    }
  }
  const page = d => { const w = cur(); if (w) move(0, d * Math.max(1, Math.floor(w.p.body.clientHeight / (w.p.rh || 60)) - 1)); else move(0, 1); };

  function toggleMark(key) {
    if (!key) return;
    S.marks.has(key) ? S.marks.delete(key) : S.marks.add(key);
    rebind(key);
    marksChanged();
  }
  // ctrl+click starts marking with the card the cursor was on; shift+click marks the run from the anchor
  // (the last card picked other than by shift+click) to the clicked one, within its lane or the list.
  function clickMark(key, shift, ctrl) {
    const from = S.anchor && S.where.get(S.anchor), to = S.where.get(key);
    if (shift && from && to && from.p === to.p) {
      const [a, b] = from.i < to.i ? [from.i, to.i] : [to.i, from.i];
      for (const c of to.p.cards.slice(a, b + 1)) S.marks.add(c.Key);
      to.p.vl.refresh();
      marksChanged();
    } else {
      if (ctrl && !S.marks.size && from && S.anchor !== key) { S.marks.add(S.anchor); rebind(S.anchor); }
      toggleMark(key);
    }
    select(key, { scroll: false, anchor: !shift });
  }
  function clearMarks() { const ks = [...S.marks]; S.marks.clear(); ks.forEach(rebind); marksChanged(); }
  // With any marked every card shows its checkbox, and the bar counts them.
  function marksChanged() { selbar.set(S.marks.size); root.classList.toggle('marking', S.marks.size > 0); }

  // ---- mouse
  delegate(main, 'click', '[data-sec]', (e, t) => toggleSection(t.dataset.lane, t.dataset.sec));
  delegate(main, 'click', '[data-key]', (e, t) => {
    const key = t.dataset.key;
    if (e.target.closest('[data-sec]')) return;
    if (e.target.closest('.cparent') && e.target.closest('.cparent').dataset.open) { app.panel.open(e.target.closest('.cparent').dataset.open); return; }
    if (e.target.closest('.chk, .l-mark') || e.ctrlKey || e.metaKey || e.shiftKey) return clickMark(key, e.shiftKey, e.ctrlKey || e.metaKey);
    select(key, { scroll: false });
    openIssue(key);
  });
  // shift+click would select the text between the two cards
  delegate(main, 'mousedown', '[data-key]', e => { if (e.shiftKey) e.preventDefault(); });
  delegate(main, 'click', '[data-fold]', (e, t) => foldBand(t.dataset.fold));
  // A lane's chevron collapses it to a bar until clicked again or the board reloads; not remembered.
  delegate(main, 'click', '.bd-lane-fold, .bd-lane.collapsed', (e, t) => {
    const p = S.panes[Number(t.closest('.bd-lane').dataset.pane)];
    S.laneFold.has(p.name) ? S.laneFold.delete(p.name) : S.laneFold.add(p.name);
    layout();
  });
  delegate(main, 'click', '[data-sort]', (e, t) => { if (t.dataset.sort) setSort(...nextSort(S.sort, S.dir, t.dataset.sort)); });
  delegate(main, 'dblclick', '[data-key]', (e, t) => window.open(app.session.baseURL + '/browse/' + t.dataset.key, '_blank', 'noopener'));

  // ---- drag and drop
  const paneOfEl = t => {
    const s = t.closest && t.closest('.bd-lane');
    if (s) return S.panes[Number(s.dataset.pane)];
    return S.mode === 'list' && t.closest && t.closest('.bd-lbody') ? S.panes[0] : null;
  };
  const listRanks = () => S.mode === 'list' && S.sort === 'rank' && S.dir > 0;
  main.addEventListener('dragstart', e => {
    const w = e.target.closest && e.target.closest('[data-key]');
    if (!w || S.past || (S.mode === 'list' && !listRanks())) { if (w) e.preventDefault(); return; }
    S.drag = w.dataset.key;
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', S.drag);
    (w.firstChild.classList ? w.firstChild : w).classList.add('dragging');
    const key = S.drag;
    setTimeout(() => { if (S.drag === key) showZones(key); }, 0);
  });
  function dropIndex(p, e) {
    if (!p.rh) {
      // a swimlane cell: count the cards whose middle is above the pointer
      let n = 0;
      for (let i = 0; i < p.cards.length; i++) { const el = p.vl.rowEl(i); if (el && el.getBoundingClientRect().top + el.offsetHeight / 2 < e.clientY) n = i + 1; }
      return n;
    }
    const r = p.body.getBoundingClientRect();
    const y = e.clientY - r.top + p.body.scrollTop;
    return Math.max(0, Math.min(p.cards.length, Math.round(y / (p.rh || 96))));
  }
  function clearDrop() {
    for (const p of S.panes) { if (p.drop) p.drop.hidden = true; if (p.el) p.el.classList.remove('drop'); }
    for (const z of main.querySelectorAll('.bd-zone.over')) z.classList.remove('over');
  }

  // While a card is dragged, a lane of several statuses splits into one labelled zone per status,
  // dimmed where the card's workflow has no transition.
  let catsP = null;
  const cats = new Map(); // status name -> category key
  function loadCats() {
    if (!catsP) {
      catsP = api.get('/projects/' + encodeURIComponent(S.project) + '/statuses')
        .then(l => { for (const t of l || []) for (const s of t.Statuses || []) cats.set(s.Name, s.Category); })
        .catch(() => { catsP = null; });
    }
    return catsP;
  }
  // Transitions per issue and status: drag zones and H/L share them; a change or another status asks again.
  const trsCache = new Map(); // 'KEY status' -> Promise of its transitions
  const metaCache = new Map(); // 'KEY status' -> Promise of its moves' screens and required fields
  offs.push(bus.on('issue:changed', ({ key }) => { for (const m of [trsCache, metaCache]) for (const k of m.keys()) if (k.startsWith(key + ' ')) m.delete(k); }));
  function metaOf(card) {
    const k = card.Key + ' ' + card.StatusID;
    if (!metaCache.has(k)) metaCache.set(k, api.get('/issues/' + card.Key + '/transitionmeta').catch(e => { metaCache.delete(k); throw e; }));
    return metaCache.get(k);
  }
  function transitionsOf(card) {
    const k = card.Key + ' ' + card.StatusID;
    if (!trsCache.has(k)) trsCache.set(k, api.get('/issues/' + card.Key + '/transitions').catch(e => { trsCache.delete(k); throw e; }));
    return trsCache.get(k);
  }
  function clearZones() { for (const z of main.querySelectorAll('.bd-zones')) z.remove(); }
  async function showZones(key) {
    const card = S.cards.find(c => c.Key === key);
    const multi = S.mode === 'lanes' ? S.panes.filter(p => { const c = columns()[p.col]; return c && (c.StatusIDs || []).length > 1; }) : [];
    if (!card || !multi.length) return;
    const [trs] = await Promise.all([transitionsOf(card).catch(() => null), loadCats()]);
    if (S.drag !== key) return;
    const names = S.bundle.statusNames || {};
    const can = trs && new Set(trs.flatMap(t => [String(t.StatusID), t.Name]));
    const last = columns().length - 1;
    clearZones();
    for (const p of multi) {
      const zs = h('div.bd-zones');
      for (const id of columns()[p.col].StatusIDs) {
        const name = names[id] || id, cur = String(card.StatusID) === String(id);
        const ok = !trs || can.has(String(id)) || can.has(name);
        const cat = cats.get(name) || (p.col === 0 ? 'new' : p.col === last ? 'done' : 'indeterminate');
        zs.append(h('div.bd-zone.cat-' + cat + (cur ? '.cur' : ok ? '' : '.no'), { dataset: { status: id }, title: cur ? T('Current status') : ok ? T('Move to %s', name) : T('No transition from %s to %s', card.Status, name) },
          h('span.bd-zone-name', name), cur ? h('span.bd-zone-tag', T('current')) : ok ? '' : h('span.bd-zone-tag', T('no transition'))));
      }
      p.el.insertBefore(zs, p.body);
    }
  }
  main.addEventListener('dragover', e => {
    const p = S.drag && paneOfEl(e.target);
    if (!p) return;
    const z = e.target.closest && e.target.closest('.bd-zone');
    if (z) {
      clearDrop();
      if (z.classList.contains('no') || z.classList.contains('cur')) return;
      e.preventDefault(); e.dataTransfer.dropEffect = 'move';
      z.classList.add('over'); p.el.classList.add('drop');
      return;
    }
    e.preventDefault(); e.dataTransfer.dropEffect = 'move';
    clearDrop();
    if (S.mode === 'lanes') p.el.classList.add('drop');
    if (p.drop) { p.drop.hidden = false; p.drop.style.top = (dropIndex(p, e) * p.rh - 3) + 'px'; }
  });
  main.addEventListener('dragleave', e => { if (!main.contains(e.relatedTarget)) clearDrop(); });
  main.addEventListener('dragend', () => { S.drag = null; clearDrop(); clearZones(); for (const d of main.querySelectorAll('.dragging')) d.classList.remove('dragging'); });
  main.addEventListener('drop', e => {
    const p = S.drag && paneOfEl(e.target);
    if (!p) return;
    e.preventDefault();
    const z = e.target.closest && e.target.closest('.bd-zone');
    const key = S.drag, idx = z ? 0 : dropIndex(p, e);
    S.drag = null; clearDrop(); clearZones();
    if (z) { const card = S.cards.find(c => c.Key === key); if (card) moveCol(card, p.col, null, z.dataset.status); return; }
    dropCard(key, p, idx);
  });

  function dropCard(key, pane, idx) {
    const card = S.cards.find(c => c.Key === key); if (!card) return;
    const from = S.where.get(key);
    const same = from && from.p === pane;
    if (swimming()) {
      if (same) return ui.toast(T('Ranking needs the swimlanes off  (O)'));
      if (from && from.p.group !== pane.group) setBand(card, pane.band);
      if (from && from.p.col === pane.col) return;
      return moveCol(S.cards.find(c => c.Key === key), pane.col, null);
    }
    if (same && (idx === from.i || idx === from.i + 1)) return;
    const L = pane.cards.filter(c => c.Key !== key);
    let k = idx; if (same && from.i < idx) k--;
    const at = k < L.length ? { other: L[k].Key, after: false } : L.length ? { other: L[L.length - 1].Key, after: true } : null;
    if (same) return rankTo(card, at);
    moveCol(card, pane.col, at);
  }

  // ---- writes (optimistic, rolled back with a toast)
  function replaceCard(card, patch) {
    const next = { ...card, ...patch };
    S.cards = S.cards.map(c => (c === card ? next : c));
    return next;
  }
  function reorder(card, at) {
    const rest = S.cards.filter(c => c.Key !== card.Key);
    let i = at ? rest.findIndex(c => c.Key === at.other) : -1;
    if (i < 0) rest.push(card); else rest.splice(at.after ? i + 1 : i, 0, card);
    S.cards = rest;
  }
  const writable = () => { if (S.past) { ui.toast(T('The time machine only looks  ·  esc back to now')); return false; } return true; };
  const pushUndo = (what, run) => import('./fields.js').then(m => m.pushUndo(app, what, run));
  // Undo one failed move: only this card goes back, other moves made meanwhile stay.
  function revertCard(key, before) {
    const orig = before.find(c => c.Key === key); if (!orig) return;
    const rest = S.cards.filter(c => c.Key !== key);
    const prev = before.slice(0, before.indexOf(orig)).reverse().find(c => rest.some(x => x.Key === c.Key));
    rest.splice(prev ? rest.findIndex(c => c.Key === prev.Key) + 1 : 0, 0, orig);
    S.cards = rest; layout();
  }
  async function rankTo(card, at) {
    if (!at) return;
    const before = S.cards, i = before.findIndex(c => c.Key === card.Key);
    const back = before[i + 1] ? { other: before[i + 1].Key, after: false } : before[i - 1] ? { other: before[i - 1].Key, after: true } : null;
    reorder(card, at); layout();
    try {
      await api.post('/issues/' + card.Key + '/rank', { Other: at.other, After: at.after });
      if (back) pushUndo(T("%s's rank", card.Key), async () => { await api.post('/issues/' + card.Key + '/rank', { Other: back.other, After: back.after }); bus.emit('issue:changed', { key: card.Key }); });
      bus.emit('issue:changed', { key: card.Key });
    } catch (e) { revertCard(card.Key, before); ui.errToast(e); }
  }
  // A move into column to (an index into columns()): to statusID when given, else one of ids (default: the column's).
  async function moveCol(card, to, at, statusID, ids) {
    const col = columns()[to];
    if (!col || !writable()) return;
    const want = ids || (col.StatusIDs || []).map(String);
    S.lastEdit = { what: '→ ' + col.Name, run: key => { const c = S.cards.find(x => x.Key === key); return c && moveCol(c, to, null); } };
    let trs;
    const metaP = metaOf(card); metaP.catch(() => {});
    try { trs = await transitionsOf(card); } catch (e) { return ui.errToast(e); }
    const names = want.map(id => (S.bundle.statusNames || {})[id]);
    let opts = trs.filter(t => want.includes(String(t.StatusID)) || names.includes(t.Name));
    if (statusID) opts = opts.filter(t => String(t.StatusID) === String(statusID) || t.Name === (S.bundle.statusNames || {})[statusID]);
    else if (opts.length > 1) { const other = opts.filter(t => String(t.StatusID) !== String(card.StatusID)); if (other.length) opts = other; }
    if (!opts.length) return ui.toast(T('No transition from %s to %s', card.Status, col.Name), { kind: 'err' });
    let t = opts[0];
    if (opts.length > 1) {
      t = await ui.pick({ title: card.Key + ' → ' + col.Name, items: opts, label: o => o.Name });
      if (!t) return;
    }
    const before = S.cards;
    const sid = String(t.StatusID || want[0]), bc = S.bundle.config.Columns || [];
    // A layout's lanes are no guide to done: the board's last column is.
    const done = activeLayout() ? !!bc.length && bc[bc.length - 1].StatusIDs.map(String).includes(sid) : to === columns().length - 1;
    const first = activeLayout() ? !!bc.length && bc[0].StatusIDs.map(String).includes(sid) : to === 0;
    const next = replaceCard(card, { Status: t.Name, StatusID: sid, Done: done, InProgress: !done && !first });
    if (at) reorder(next, at);
    layout();
    if (S.sel === card.Key) select(card.Key);
    // A move the workflow wants fields for opens its form, as the TUI's (fields.js moveForm); the card goes back meanwhile.
    let meta = null;
    try { meta = ((await metaP).Transitions || []).find(x => x.ID === t.ID); } catch (e) { /* unknown: try the move, Jira says no if it must */ }
    if (meta && meta.NeedsInput) {
      revertCard(card.Key, before);
      return import('./fields.js').then(m => m.moveForm(app, card.Key, meta));
    }
    try {
      await api.post('/issues/' + card.Key + '/transition', { ID: t.ID });
      if (at) await api.post('/issues/' + card.Key + '/rank', { Other: at.other, After: at.after }).catch(e => ui.errToast(e));
      pushUndo(card.Key + ' → ' + t.Name, async () => { await api.put('/issues/' + card.Key + '/field/status', { To: card.Status }); bus.emit('issue:changed', { key: card.Key }); });
      bus.emit('issue:changed', { key: card.Key, what: card.Key + ' → ' + t.Name });
      if (done) confetti(app, [...main.querySelectorAll('.bd-lane-head.kdone')].find(e => e.querySelector('.bd-lane-name').textContent === col.Name));
    } catch (e) {
      revertCard(card.Key, before); ui.toast(T('Could not move %s: %s', card.Key, e.message), { kind: 'err' });
    }
  }
  // A drop into another swimlane gives the card that band's assignee or epic, as Jira's board (TUI jiraSetBand).
  async function setBand(card, band) {
    if (!band || !writable()) return;
    let field, body, patch;
    if (S.swim === 'assignee' && (band.AssigneeID || '') !== (card.AssigneeID || '')) {
      field = 'assignee'; body = { ID: band.AssigneeID || '' }; patch = { Assignee: band.Assignee || '', AssigneeID: band.AssigneeID || '' };
    } else if (S.swim === 'epic' && (band.ParentKey || '') !== (card.ParentKey || '')) {
      field = 'parent'; body = { Text: band.ParentKey || '' }; patch = { ParentKey: band.ParentKey || '', ParentSummary: band.ParentSummary || '' };
    } else return;
    const before = S.cards;
    replaceCard(card, patch); layout();
    if (S.sel === card.Key) select(card.Key);
    try {
      const r = await api.put('/issues/' + card.Key + '/field/' + field, body);
      const u = r && r.Undo;
      if (u) pushUndo(T('%s back to its %s', card.Key, field), async () => { await api.put('/issues/' + card.Key + '/field/' + u.Field, u); bus.emit('issue:changed', { key: card.Key }); });
      bus.emit('issue:changed', { key: card.Key, what: T('%s %s → %s', card.Key, field, field === 'assignee' ? patch.Assignee || T('unassigned') : patch.ParentKey || T('no epic')) });
    } catch (e) { revertCard(card.Key, before); ui.errToast(e); }
  }
  // H and L step through the lanes, a stacked lane's sections one by one (TUI moveJiraCardBy).
  function stepCol(d) {
    const c = curCard(); if (!c) return;
    const stops = [];
    columns().forEach((col, i) => {
      const secs = col.Sections && col.Sections.length > 1 ? col.Sections : [col];
      for (const s of secs) stops.push({ to: i, ids: (s.StatusIDs || []).map(String), sec: secs.length > 1 });
    });
    const from = stops.findIndex(s => s.ids.includes(String(c.StatusID))), st = stops[from + d];
    if (from < 0 || !st) return;
    moveCol(c, st.to, null, st.sec && st.ids.length === 1 ? st.ids[0] : null, st.sec ? st.ids : null);
  }
  // d is -1/1, or -Infinity/Infinity for the top/bottom. A list sorted by anything but rank still ranks in rank order.
  function stepRank(d) {
    if (!writable()) return;
    if (swimming()) return ui.toast(T('Ranking needs the swimlanes off  (O)'));
    const w = cur(), c = curCard(); if (!c) return;
    const L = S.mode === 'list' && !listRanks() ? S.base.filter(passes) : w.p.cards;
    const i = L.findIndex(x => x.Key === c.Key);
    const j = Math.max(0, Math.min(L.length - 1, i + (Number.isFinite(d) ? d : d * L.length)));
    if (j === i) return ui.toast(d < 0 ? T('%s is ranked first already', c.Key) : T('%s is ranked last already', c.Key));
    rankTo(c, { other: L[j].Key, after: j > i });
  }

  // ---- repeat the last change (.) on the selected card
  offs.push(bus.on('issue:patch', ({ key, field, value }) => {
    patchCard(key, field, value);
    if (field === 'summary' || value === undefined) return;
    S.lastEdit = { what: field + ' → ' + (value && value.DisplayName ? value.DisplayName : Array.isArray(value) ? value.join(' ') : value == null || value === '' ? T('none') : value), run: key2 => repeatField(key2, field, value) };
  }));
  // An edit from a picker (status, priority…) shows on its card at once; the refetch after the write settles it.
  function patchCard(key, field, value) {
    const card = S.cards.find(c => c.Key === key);
    if (!card || value === undefined) return;
    let patch;
    switch (field) {
      case 'status': {
        const names = (S.bundle && S.bundle.statusNames) || {};
        const id = Object.keys(names).find(k => names[k] === value);
        if (!id) return;
        const to = sc.get(String(id)), last = columns().length - 1;
        patch = { Status: value, StatusID: id, Done: to === last, InProgress: to != null && to > 0 && to < last };
        break;
      }
      case 'priority': patch = { Priority: value }; break;
      case 'points': patch = { Points: value }; break;
      case 'summary': patch = { Summary: value }; break;
      case 'assignee': patch = { Assignee: value ? value.DisplayName : '', AssigneeID: value ? value.AccountID : '' }; break;
      default: return;
    }
    replaceCard(card, patch);
    layout();
    if (S.sel === key) select(key);
  }
  async function repeatField(key, field, value) {
    let body;
    switch (field) {
      case 'status': body = { To: value }; break;
      case 'priority': {
        const p = (await api.get('/priorities')).find(x => x.Name === value);
        if (!p) return ui.toast(T('No priority %s', value), { kind: 'err' });
        body = { ID: p.ID }; break;
      }
      case 'assignee': body = { ID: value ? value.AccountID : '' }; break;
      case 'points': case 'duedate': body = { Text: String(value == null ? '' : value) }; break;
      case 'labels': body = { Labels: value }; break;
      case 'flag': body = { On: !!value }; break;
      default: return ui.toast(T('Cannot repeat %s', field));
    }
    try {
      const r = await api.put('/issues/' + key + '/field/' + field, body);
      bus.emit('issue:changed', { key });
      const u = r && r.Undo;
      if (u) pushUndo(T('%s %s', key, field), async () => { await api.put('/issues/' + key + '/field/' + u.Field, u); bus.emit('issue:changed', { key }); });
      ui.toast(T('%s %s repeated', key, field), { kind: 'ok' });
    } catch (e) { ui.errToast(e); bus.emit('issue:changed', { key }); }
  }
  // M (TUI move_sprint): the marked cards, else the selected one, to an open sprint or the backlog.
  async function moveSprint() {
    if (!writable()) return;
    if (!isScrum()) return ui.toast(T("Sprints are a scrum board's"));
    const ks = bulkKeys();
    if (!ks.length) return ui.toast(T('Select a card first'));
    pickSprintFor(ks);
  }
  const issuesOf = ks => (ks.length === 1 ? ks[0] : T('%d issues', ks.length));
  async function pickSprintFor(ks) {
    const items = [...sprints().filter(s => s.State !== 'closed'), { ID: 0, Name: T('Backlog') }];
    const o = await ui.pick({ title: T('%s → sprint', issuesOf(ks)), items, label: s => s.Name, detail: s => s.State || '' });
    if (o) toSprint(ks, o);
  }
  async function toSprint(ks, o) {
    S.lastEdit = { what: '→ ' + o.Name, run: key => toSprint([key], o) };
    const from = new Map(ks.map(k => { const c = S.cards.find(x => x.Key === k), sp = c && sprints().find(s => s.Name === c.Sprint); return [k, c && !c.Sprint ? 0 : sp ? sp.ID : null]; }));
    try {
      await api.post('/plan/move', { Keys: ks, Sprint: o.ID });
    } catch (e) { return ui.errToast(e); }
    for (const k of ks) bus.emit('issue:changed', { key: k, what: k + ' → ' + o.Name });
    // Undo puts each card back where it was, when that was the backlog or a sprint the board knows.
    const back = ks.filter(k => from.get(k) != null);
    const step = back.length && await pushUndo(T('%s → %s', issuesOf(ks), o.Name), async () => {
      for (const id of new Set(back.map(k => from.get(k)))) await api.post('/plan/move', { Keys: back.filter(k => from.get(k) === id), Sprint: id });
      for (const k of back) bus.emit('issue:changed', { key: k });
    });
    ui.toast(T('%s → %s', issuesOf(ks), o.Name), { kind: 'ok', action: step && { label: T('Undo'), run: () => import('./fields.js').then(m => m.undo(app, step)) } });
  }
  function repeat() {
    const c = curCard();
    if (!S.lastEdit) return ui.toast(T('Nothing to repeat yet'));
    if (!c) return ui.toast(T('Select a card first'));
    if (!writable()) return;
    ui.toast(T('Repeating %s on %s…', S.lastEdit.what, c.Key));
    S.lastEdit.run(c.Key);
  }

  // ---- filters
  // Filters outlive the view (planning and back, a reload) unless ui.remember_filters is off, as the TUI's: mine and the
  // assignee filter in board.assignee for every board, the quick filters in board.quick.<id>. An embedded board keeps its own.
  const rememberFilters = !embed && String(UI.RememberFilters || '').trim().toLowerCase() !== 'off';
  function saveFilters() {
    if (!rememberFilters) return;
    app.prefs.set('board.assignee', JSON.stringify({ mine: S.mine, who: S.who ? [...S.who] : null }));
    app.prefs.set('board.quick.' + S.board.ID, [...S.qf].join(','));
  }
  function restoreFilters() {
    if (!rememberFilters) return;
    let a = {};
    try { a = JSON.parse(app.prefs.get('board.assignee', '{}')) || {}; } catch (e) { /* a broken one: none */ }
    S.mine = !!a.mine; S.who = Array.isArray(a.who) ? new Set(a.who) : null;
    S.qf = new Set(String(app.prefs.get('board.quick.' + S.board.ID, '')).split(',').filter(Boolean).map(Number));
  }
  function toggleQF(id) { S.qf.has(id) ? S.qf.delete(id) : S.qf.add(id); saveFilters(); renderBar(); S.path === cardsPath() || loadCards(); }
  // whoChanged tells the view the board is embedded in who its assignee filter shows, null for anyone.
  const whoChanged = () => { if (embed && embed.onWho) embed.onWho(S.mine ? [me] : S.who ? [...S.who] : null); };
  function toggleMine() { S.mine = !S.mine; if (S.mine) S.who = null; saveFilters(); layout(); renderBar(); whoChanged(); }
  function clearFilters() {
    const reload = S.qf.size > 0;
    S.mine = false; S.who = null; S.qf.clear(); S.text = ''; S.textFn = null; filterIn.value = '';
    saveFilters(); layout(); renderBar(); whoChanged();
    if (reload) loadCards();
  }
  async function pickWho() {
    // The people with cards on the board (mine and the assignee filter narrow only the view), and anyone ticked.
    const seen = new Map();
    for (const c of S.cards) if (who(c)) seen.set(who(c), c.Assignee);
    for (const id of S.who || []) if (id !== '-' && !seen.has(id)) seen.set(id, whoName(id));
    const r = await pickPeople(app, seen, S.who);
    if (r === undefined) return;
    S.who = r; if (r) S.mine = false; saveFilters(); layout(); renderBar(); whoChanged();
  }
  function setSort(col, dir) {
    S.sort = col; S.dir = dir;
    app.prefs.set('board.sort.' + S.board.ID, col + ':' + dir);
    if (S.mode !== 'list') setMode('list'); else { S.built = ''; layout(); }
  }
  function setMode(m) {
    if (m === S.mode) return;
    S.mode = m; S.built = '';
    app.prefs.set('board.mode.' + S.board.ID, m);
    renderToolbar(); layout();
    if (S.sel) { const w = S.where.get(S.sel); if (w) { w.p.vl.scrollTo(w.i, 'center'); w.p.vl.refresh(w.i); } }
  }

  // ---- swimlanes, fold, compact, list columns
  function setSwim(v) {
    S.swim = v; S.fold.clear(); S.built = '';
    if (S.board) app.prefs.set('board.swim.' + S.board.ID, v);
    renderToolbar();
    if (S.mode !== 'lanes') return;
    layout();
    ui.toast(v === 'none' ? T('No swimlanes') : T('Swimlanes by %s', SWIMNAME[v]));
  }
  function foldBand(name) {
    if (!swimming() && name == null && S.mode === 'lanes') {
      const w = cur(), col = w && columns()[w.p.col];
      if (col && (col.Sections || []).length > 1) {
        const c = w.p.cards[w.i], s = col.Sections.find(x => (x.StatusIDs || []).includes(String(c.StatusID)));
        if (s) return toggleSection(w.p.name, s.Name);
      }
    }
    if (!swimming()) return ui.toast(T('Folding needs swimlanes  (O) or a stacked lane'));
    const w = cur(), g = name != null ? name : w && groupName(w.p.cards[w.i]);
    if (g == null) return;
    S.fold.has(g) ? S.fold.delete(g) : S.fold.add(g);
    const keep = S.sel;
    layout();
    if (keep && !S.where.has(keep)) {
      S.sel = null;
      const p = S.panes.find(q => q.cards.length && !q.folded);
      if (p) select(p.cards[0].Key);
    }
  }
  function unfoldAll() { S.fold.clear(); S.secFold.clear(); app.prefs.set('board.secfold.' + S.board.ID, ''); layout(); }
  function setCompact(v) { S.compact = v; app.prefs.set('board.compact', v ? '1' : '0'); renderToolbar(); layout(); }
  function setCols(list) {
    S.cols = fixCols(list);
    app.prefs.set('board.cols', S.cols.join(','));
    S.built = ''; if (!SORTS.includes(S.sort) && !S.cols.includes(S.sort)) { S.sort = 'rank'; S.dir = 1; }
    layout();
  }
  async function pickCols() {
    const r = await pickListCols(ui, allCols(), S.cols);
    if (r) setCols(r);
  }

  // ---- filter builder, named filters
  function openBuilder() {
    if (!S.loaded) return;
    openFilterBuilder({ app, cards: S.base || S.cards, env: env(), query: S.text, apply: t => { filterIn.value = t; setText(t); } });
  }
  let filterCmds = [];
  function registerNamedFilters() {
    filterCmds.forEach(u => u()); filterCmds = [];
    ((app.session.ui && app.session.ui.Filters) || []).forEach((f, i) => {
      if (f.Name && f.Query) filterCmds.push(app.commands.register({ id: 'board:filter:' + i, title: T('Filter: %s', f.Name), group: T('Filters'), run: () => { filterIn.value = f.Query; setText(f.Query); } }));
    });
  }

  // ---- time machine and closed sprints
  async function fetchMoves() {
    const p = S.past; if (!p) return;
    try {
      const r = await api.post('/cards/statusmoves', { Keys: S.cards.map(c => c.Key) });
      if (S.past !== p) return;
      p.moves = r || {}; p.loading = false;
    } catch (e) { if (S.past === p) S.past = null; ui.errToast(e); }
    layout();
  }
  function openPast() {
    if (S.past) return;
    if (!S.loaded) return;
    S.past = { days: 1, at: 0, moves: null, loading: true, fetching: true };
    renderBanner(); fetchMoves();
  }
  function stepPast(d) {
    const p = S.past;
    if (!p) return false;
    if (p.loading) { ui.toast(T('Still reading the history  ·  esc cancels')); return true; }
    if (p.at) { ui.toast(T('A closed sprint shows as it closed  ·  esc leaves it')); return true; }
    p.days = Math.min(p.days + d, 90);
    if (p.days <= 0) leavePast(); else layout();
    return true;
  }
  function leavePast() {
    if (S.closed) return leaveClosed();
    S.past = null; layout();
  }
  async function pickClosed() {
    if (!isScrum()) return ui.toast(T("Closed sprints are a scrum board's"));
    let list;
    try { list = (await api.get('/boards/' + S.board.ID + '?closed=1')).closedSprints || []; } catch (e) { return ui.errToast(e); }
    if (!list.length) return ui.toast(T('No closed sprints'));
    const closedOn = s => (!isZero(s.Complete) ? s.Complete : s.End);
    const r = await ui.pick({ title: T('Closed sprints'), items: list, label: s => s.Name, detail: s => [isZero(closedOn(s)) ? '' : T('closed %s', shortDate(closedOn(s))), s.Goal].filter(Boolean).join('  ·  ') });
    if (r) openClosed(r, closedOn(r));
  }
  function openClosed(sp, closedAt) {
    if (!S.closed) S.closedFrom = S.scope;
    S.closed = sp;
    S.past = { days: 0, at: isZero(closedAt) ? Date.now() : Date.parse(closedAt), moves: null, loading: true };
    S.loaded = false; S.cards = []; S.path = '';
    renderToolbar(); renderBanner(); loadCards();
  }
  function leaveClosed() {
    const from = S.closedFrom || 'active';
    S.closed = null; S.past = null; S.path = '';
    setScope(from);
  }
  // What a closed sprint came to: done, and where the rest went.
  function closedLine() {
    let done = 0, carried = 0;
    const to = {};
    for (const c of S.cards) {
      if (c.Sprint) to[c.Sprint] = (to[c.Sprint] || 0) + 1;
      else if (c.Done) { done++; continue; } else to.backlog = (to.backlog || 0) + 1;
      carried++;
    }
    if (!carried) return T('%d done', done);
    return T('%d done  ·  %d carried over: %s', done, carried, Object.keys(to).sort().map(k => to[k] + ' → ' + (k === 'backlog' ? T('backlog') : k)).join(', '));
  }
  function renderBanner() {
    const p = S.past;
    banner.hidden = !p;
    if (!p) return;
    const day = t => new Date(t).toLocaleDateString(undefined, { weekday: 'short', day: 'numeric', month: 'short' });
    let t;
    if (S.closed) t = p.loading ? T('%s, reading history…', S.closed.Name) : T('%s, as it closed %s  ·  %s  ·  esc leaves it', S.closed.Name, day(p.at), closedLine());
    else t = p.loading ? T('reading the board’s history…  ·  esc cancels') : T('as of %s  ·  ← earlier  ·  → later  ·  esc back to now', day(asOf(p) - 60000));
    setIcon(banner, 'rotate-ccw-clock', t);
  }

  // ---- pins (★, first in the palette), mark all, copy
  function loadPins() {
    S.pins = new Set(pins.list(app).map(p => p[0]));
    if (S.textFn) S.textFn = cq.compile(S.text, env());
  }
  function togglePin(c) { if (c) pins.toggle(app, c.Key, c.Summary); }
  offs.push(bus.on('pins:changed', () => { loadPins(); for (const p of S.panes) p.vl.refresh(); }));
  // The site's change stream: a change in this project refreshes the board
  // quietly, once for a burst.
  let siteTimer = 0;
  offs.push(bus.on('site:change', ch => {
    if (!S.project || siteTimer) return;
    if (!(String(ch.key || '').startsWith(S.project + '-') || ch.kind === 'sprint' || ch.kind === 'board')) return;
    siteTimer = setTimeout(() => { siteTimer = 0; if (!document.hidden) refresh(false); }, 1000);
  }));
  offs.push(bus.on('highlights', () => { for (const p of S.panes) p.vl.refresh(); }));
  for (const ev of ['timer', 'timer:tick']) offs.push(bus.on(ev, () => { for (const p of S.panes) p.vl.refresh(); }));
  function markAll() {
    const w = cur();
    const list = S.mode === 'list' ? S.visible : w ? w.p.cards : S.panes.reduce((a, p) => a.concat(p.cards), []);
    if (!list.length) return;
    const all = list.every(c => S.marks.has(c.Key));
    for (const c of list) all ? S.marks.delete(c.Key) : S.marks.add(c.Key);
    for (const p of S.panes) p.vl.refresh();
    marksChanged();
    ui.toast(all ? T('Cleared marks') : T('%d marked  ·  X edits them  ·  esc clears', S.marks.size));
  }
  const copy = (text, what) => (navigator.clipboard ? navigator.clipboard.writeText(text).then(() => ui.toast(T('Copied %s', what)), ui.errToast) : ui.toast(T('No clipboard here'), { kind: 'err' }));
  function copyTable() {
    const cards = S.visible.filter(c => S.marks.has(c.Key));
    const esc = x => String(x || '').replace(/\|/g, '\\|');
    const rows = cards.map(c => `| [${c.Key}](${app.session.baseURL}/browse/${c.Key}) | ${esc(c.Summary)} | ${esc(c.Status)} | ${esc(c.Assignee)} | ${esc(c.Points)} |`);
    copy(['| Key | Summary | Status | Assignee | Points |', '| --- | --- | --- | --- | --- |', ...rows].join('\n'), Tn(cards.length, '%d row as a table', '%d rows as a table', cards.length));
  }
  const slug = t => {
    const words = t.toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);
    let out = '';
    for (const w of words) { const next = out ? out + '-' + w : w; if (next.length > 40 && out) break; out = next; }
    return out || 'work';
  };
  function copyBranch(c) {
    const tmpl = (app.session.ui && app.session.ui.BranchTemplate) || '{key}-{summary}';
    const name = tmpl.replace(/\{key\}/g, c.Key).replace(/\{summary\}/g, slug(c.Summary)).replace(/\{type\}/g, slug(c.Type || '')).replace(/\{project\}/g, c.Key.split('-')[0]);
    copy(name, name);
  }

  // ---- pickers
  async function pickBoard() {
    const b = await pickBoardOf(app, S.project, { scrum: false });
    if (b) app.go('/board/' + S.project + '/' + b.ID);
  }
  async function pickSprint() {
    const items = viewItems();
    if (items.length < 2) return;
    const r = await ui.pick({ title: T('View'), items, label: i => i.name, detail: i => i.info || '' });
    if (r) setScope(r.id);
  }
  function setScope(id) {
    if (S.past) leavePast(true);
    S.closed = null;
    S.scope = id;
    if (S.bundle) dropClosed(sprints()); // back from a closed sprint to one closed since
    if (!embed) app.setQuery({ sprint: S.scope === 'active' ? null : S.scope });
    renderToolbar();
    loadCards();
  }
  function cycleScope(d) {
    const ids = viewItems().map(i => i.id);
    if (ids.length < 2) return;
    const r = resolveScope();
    const now = S.closed ? (S.closedFrom || ids[0]) : S.scope === 'active' && isScrum() ? String(r.sprint || 'all') : S.scope;
    const i = ids.indexOf(now);
    setScope(ids[(i + d + ids.length) % ids.length]);
  }

  // ---- actions
  const need = fn => () => { const c = curCard(); if (c) fn(c); else ui.toast(T('Select a card first')); };
  const needW = fn => need(c => { if (writable()) fn(c); });
  const edit = field => needW(c => app.actions.edit(c.Key, field, cardEl(c.Key)));
  app.marked = () => [...S.marks];
  app.listed = () => S.visible || [];
  // The card menu's board entries (lib/pwa.js), with their current values.
  app.menuItems = key => {
    const c = S.cards.find(x => x.Key === key);
    if (!c) return [];
    const out = [{ label: S.pins.has(key) ? T('Unpin') : T('Pin'), hint: S.pins.has(key) ? T('pinned') : '', run: () => togglePin(c) }];
    // The board's open sprints and the backlog, the card's own marked (TUI cardMenu); a move as M makes it.
    if (isScrum()) out.unshift({ id: 'sprint', label: T('Sprint'), hint: c.Sprint || T('backlog'), sub: () => [...sprints().filter(sp => sp.State !== 'closed'), { ID: 0, Name: T('Backlog') }].map(sp => ({
      label: sp.Name, hint: sp.State || '', current: sp.ID ? sp.Name === c.Sprint : !c.Sprint,
      run: () => { if (!writable() || (sp.ID ? sp.Name === c.Sprint : !c.Sprint)) return; select(key); toSprint([key], sp); } })) });
    return out;
  };
  const bulkKeys = () => (S.marks.size ? [...S.marks] : S.sel ? [S.sel] : []);
  // Marks survive view and filter changes; after a bulk edit only the cards it did not change stay marked (TUI).
  async function bulk() {
    const ks = bulkKeys();
    if (!writable() || !ks.length) return;
    const res = await app.actions.bulk(ks);
    if (!res) return;
    const done = new Set(res.Done);
    const was = [...S.marks];
    S.marks = new Set(ks.filter(k => !done.has(k)));
    new Set([...was, ...S.marks]).forEach(rebind);
    marksChanged();
  }
  const cycleO = () => {
    if (S.mode === 'lanes') setSwim(SWIMS[(SWIMS.indexOf(S.swim) + 1) % SWIMS.length]);
    else setSort(SORTS[(SORTS.indexOf(S.sort) + 1) % SORTS.length], 1);
  };
  const cycleSwim = () => setSwim(SWIMS[(SWIMS.indexOf(S.swim) + 1) % SWIMS.length]);

  function bindKeys() {
    const k = scope, G = T('Board'), E = T('Board: edit'), F = T('Board: filter'), V = T('Board: view');
    k.bind(['j', 'ArrowDown'], () => move(0, 1), T('next card'), { group: G });
    k.bind(['k', 'ArrowUp'], () => move(0, -1), T('previous card'), { group: G });
    k.bind('h', () => move(-1, 0), T('previous lane'), { group: G });
    k.bind('l', () => move(1, 0), T('next lane'), { group: G });
    k.bind('ArrowLeft', () => (S.past ? stepPast(1) : move(-1, 0)), T('previous lane (time machine: a day earlier)'), { group: G, hidden: true });
    k.bind('ArrowRight', () => (S.past ? stepPast(-1) : move(1, 0)), T('next lane (time machine: a day later)'), { group: G, hidden: true });
    k.bind('Home', () => edge(false), T('first card in lane'), { group: G });
    k.bind('End', () => edge(true), T('last card in lane'), { group: G });
    k.bind('PageDown', () => page(1), T('page down'), { group: G, hidden: true });
    k.bind('PageUp', () => page(-1), T('page up'), { group: G, hidden: true });
    k.bind('Enter', need(c => openIssue(c.Key)), T('open issue'), { group: G, bar: T('open') });
    k.bind('Escape', () => {
      if (S.past) leavePast();
      else if (app.panel.key) app.panel.close();
      else if (S.marks.size) clearMarks();
      else if (S.sel) { const o = S.sel; S.sel = null; rebind(o); }
    }, T('leave time machine / close panel / clear selection'), { group: G });
    k.bind('s', needW(c => app.actions.transition(c.Key)), T('change status'), { group: E, bar: T('status') });
    k.bind('e', edit('summary'), T('edit summary'), { group: E });
    k.bind('a', edit('assignee'), T('assign'), { group: E, bar: T('assign') });
    k.bind('p', edit('priority'), T('set priority'), { group: E });
    k.bind('P', edit('points'), T('set points'), { group: E });
    k.bind('H', () => { if (writable()) stepCol(-1); }, T('move card to previous column'), { group: E, bar: T('move'), writes: true });
    k.bind('L', () => { if (writable()) stepCol(1); }, T('move card to next column'), { group: E, bar: T('move'), writes: true });
    k.bind('J', () => stepRank(1), T('rank card down'), { group: E, writes: true });
    k.bind('K', () => stepRank(-1), T('rank card up'), { group: E, writes: true });
    k.bind('alt+j', () => stepRank(Infinity), T('rank card to the bottom'), { group: E });
    k.bind('alt+k', () => stepRank(-Infinity), T('rank card to the top'), { group: E });
    k.bind('M', moveSprint, T('move to a sprint or the backlog'), { group: E });
    k.bind('E', need(c => app.actions.menu && app.actions.menu(c.Key)), T('quick edit: status, assignee, priority, points, labels, sprint, pin'), { group: E, bar: T('edit') });
    k.bind('.', repeat, T('repeat the last change on this card'), { group: E, writes: true });
    k.bind('x', () => { toggleMark(S.sel); move(0, 1); }, T('mark card (multi-select)'), { group: E, bar: T('mark') });
    k.bind('mod+a', markAll, T('mark all in the lane / list'), { group: E });
    k.bind('X', bulk, T('bulk edit marked cards'), { group: E });
    k.bind('n', () => app.actions.create(createOpts()), T('new issue (into the sprint shown)'), { group: E });
    app.createHere = () => app.actions.create(createOpts()); // the top bar's Create
    k.bind('*', need(togglePin), T('pin / unpin issue (first in the palette)'), { group: G, writes: true });
    k.bind('o', need(c => window.open(app.session.baseURL + '/browse/' + c.Key, '_blank', 'noopener')), T('open in Jira'), { group: G, writes: true });
    k.bind('y', () => { if (S.marks.size) return copyTable(); const c = curCard(); if (c) copy(c.Key, c.Key); else ui.toast(T('Select a card first')); }, T('copy key (marked cards: as a table)'), { group: G, writes: true });
    k.bind('Y', need(c => copy(app.session.baseURL + '/browse/' + c.Key, T('link'))), T('copy link'), { group: G, writes: true });
    k.bind('mod+y', need(copyBranch), T('copy branch name'), { group: G });
    k.bind('r', () => refresh(true), T('refresh'), { group: G, bar: T('refresh') });
    k.bind('t', () => setMode(S.mode === 'lanes' ? 'list' : 'lanes'), T('lanes / list'), { group: V, bar: T('lanes / list'), writes: true });
    k.bind('O', cycleO, T('lanes: cycle swimlanes · list: cycle sort'), { group: V, writes: true });
    k.bind('z', () => foldBand(), T('fold the swimlane, or the stacked lane\'s section'), { group: V });
    k.bind('Z', unfoldAll, T('unfold all swimlanes and sections'), { group: V, writes: true });
    k.bind('}', () => jumpBand(1), T('next swimlane, or the list\'s group'), { group: G });
    k.bind('{', () => jumpBand(-1), T('previous swimlane, or the list\'s group'), { group: G });
    k.bind('c', () => setCompact(!S.compact), T('one-line cards'), { group: V, writes: true });
    k.bind('alt+e', () => { if (S.mode === 'lanes') setHideEmpty(!S.hideEmpty); }, T('hide / show empty lanes'), { group: V });
    k.bind('alt+l', cycleLayout, T('next lane layout: the board\'s columns or a ui.lane_layouts entry'), { group: V });
    k.bind('C', pickCols, T('list columns'), { group: V });
    k.bind('V', () => app.go('/reports/releases'), T('releases: versions and their progress'), { group: V });
    k.bind('alt+t', openPast, T('time machine: the board on earlier days'), { group: V });
    k.bind('alt+o', pickClosed, T('closed sprints: one as it ended'), { group: V });
    k.bind('B', () => pickBoard(), T('switch board (same project)'), { group: V });
    k.bind('v', () => pickSprint(), T('pick a view: sprint, backlog, your views'), { group: V, bar: T('view') });
    k.bind('[', () => cycleScope(-1), T('previous view'), { group: V });
    k.bind(']', () => cycleScope(1), T('next view'), { group: V });
    k.bind('f', () => { filterIn.focus(); filterIn.select(); }, T('filter cards'), { group: F, bar: T('filter') });
    k.bind('F', openBuilder, T('filter builder'), { group: F, bar: T('filter builder') });
    k.bind('m', () => toggleMine(), T('only my cards'), { group: F, bar: T('mine'), writes: true });
    k.bind('A', () => pickWho(), T('filter by assignee'), { group: F });
    k.bind('0', () => clearFilters(), T('clear filters'), { group: F, writes: true });
    for (let i = 1; i <= 9; i++) k.bind(String(i), () => { const q = qfs()[i - 1]; if (q) toggleQF(q.ID); }, i === 1 ? T('toggle quick filter 1-9') : '', { group: F, hidden: i > 1, writes: true });
  }

  let qfCmds = [];
  function registerQFCommands() {
    qfCmds.forEach(u => u()); qfCmds = [];
    for (const q of qfs()) qfCmds.push(app.commands.register({ id: 'board:qf:' + q.ID, title: T('Board: toggle quick filter %s', q.Name), group: T('Board'), run: () => toggleQF(q.ID) }));
  }
  function registerCommands() {
    const C = (id, title, run) => unreg.push(app.commands.register({ id: 'board:' + id, title, group: T('Board'), run }));
    C('lanes', T('Board: show lanes'), () => setMode('lanes'));
    C('list', T('Board: show list'), () => setMode('list'));
    C('toggle', T('Board: toggle lanes / list'), () => setMode(S.mode === 'lanes' ? 'list' : 'lanes'));
    C('refresh', T('Board: refresh'), () => refresh(true));
    C('mine', T('Board: only my cards'), toggleMine);
    C('who', T('Board: filter by assignee'), pickWho);
    C('clear', T('Board: clear filters'), clearFilters);
    C('filter', T('Board: filter cards'), () => { filterIn.focus(); filterIn.select(); });
    C('switch', T('Board: switch board'), pickBoard);
    C('sprint', T('Board: pick a view: sprint, backlog, whole board'), pickSprint);
    C('new', T('Board: new issue'), () => app.actions.create(createOpts()));
    for (const s of SORTS) C('sort:' + s, T('Board: sort list by %s', s), () => setSort(s, 1));
    for (const v of SWIMS) C('swim:' + v, v === 'none' ? T('Board: no swimlanes') : T('Board: swimlanes by %s', SWIMNAME[v]), () => { if (S.mode !== 'lanes') setMode('lanes'); setSwim(v); });
    C('fold', T('Board: fold the swimlane'), () => foldBand());
    C('unfold', T('Board: unfold all swimlanes'), unfoldAll);
    C('compact', T('Board: one-line cards'), () => setCompact(!S.compact));
    C('layout', T('Board: next lane layout (ui.lane_layouts)'), cycleLayout);
    C('empty_lanes', T('Board: hide / show empty lanes'), () => { if (S.mode !== 'lanes') setMode('lanes'); setHideEmpty(!S.hideEmpty); });
    C('columns', T('Board: list columns'), pickCols);
    C('builder', T('Board: filter builder'), openBuilder);
    C('past', T('Board: time machine: the board on earlier days'), openPast);
    C('closed', T('Board: closed sprints: one as it ended'), pickClosed);
    C('pin', T('Board: pin / unpin the selected issue'), () => togglePin(curCard()));
    C('repeat', T('Board: repeat the last change'), repeat);
    C('top', T('Board: rank to the top'), () => stepRank(-Infinity));
    C('bottom', T('Board: rank to the bottom'), () => stepRank(Infinity));
    C('markall', T('Board: mark all in the lane / list'), markAll);
    C('branch', T('Board: copy branch name'), () => { const c = curCard(); if (c) copyBranch(c); });
    C('table', T('Board: copy marked cards as a table'), copyTable);
    registerNamedFilters();
    unreg.push(() => filterCmds.forEach(u => u()));
    unreg.push(() => qfCmds.forEach(u => u()));
  }

  // ---- lifecycle
  function touch() { lastInput = Date.now(); }
  document.addEventListener('keydown', touch, true);
  document.addEventListener('pointermove', touch, { passive: true });
  function armAuto() {
    const pr = app.prefs.get('board.refresh', ''); const every = pr !== '' ? Number(pr) * 1000 : parseEvery(app.session.ui && app.session.ui.AutoRefresh);
    if (!every) return;
    const tick = () => {
      if (S.dead) return;
      const idle = Date.now() - lastInput > 8000 && !document.hidden && !S.drag;
      if (idle) { refresh(false); autoT = later(tick, every); } else autoT = later(tick, 8000);
    };
    autoT = later(tick, every);
  }
  offs.push(bus.on('focus', () => { if (Date.now() - S.fetched > staleMs) refresh(false); }));
  const onChanged = debounce(() => refresh(false), 250);
  offs.push(bus.on('issue:changed', onChanged));
  if (!embed) offs.push(bus.on('panel', ({ key }) => { if (key && key !== S.sel && S.where.has(key)) select(key); })); // back and forward move the cursor along
  // Searches starred in the palette (ctrl+s): views of every board, shared with the TUI.
  const loadStarred = () => api.get('/jql/starred', { fresh: true }).then(l => { S.starred = l || []; renderToolbar(); }).catch(() => {});
  const starredP = loadStarred();
  offs.push(bus.on('jql:starred', loadStarred));
  // Keys with private notes, for is:notes.
  const refilter = () => { if (S.textFn) { S.textFn = cq.compile(S.text, env()); layout(); } };
  api.get('/notes').then(ks => { S.notes = new Set(ks || []); refilter(); }).catch(() => {});
  offs.push(bus.on('notes:changed', ({ key, has }) => { has ? S.notes.add(key) : S.notes.delete(key); refilter(); }));

  function listCols() {
    const all = allCols(), want = app.prefs.get('board.cols', '');
    let cols;
    if (want) cols = want.split(',').filter(c => all.includes(c));
    else if (CF) cols = all.filter(c => ['mark', 'key', 'summary'].includes(c) || [...CF].some(f => FIELD_COL[f] === c) || CUSTOM.includes(c));
    else cols = all.filter(c => DEFAULT_COLS.includes(c) || CUSTOM.includes(c));
    return fixCols(cols);
  }

  async function start() {
    bdMsg(T('Loading…'));
    let project = params.project, bid = params.board ? Number(params.board) : 0;
    try {
      if (!project) project = lastProject(app) || (app.session.projects || [])[0];
      if (project && !bid) bid = lastBoard(app, project);
      if (project) app.chrome.label(projectBtn, project);
      if (!project) { const ps = await firstOf(api, '/projects'); project = ps[0] && ps[0].Key; }
      if (!project) return bdMsg(h('div', h('h2', T('No project')), h('p', T('Add one under jira.projects in the config.'))));
      S.project = project;
      S.boards = await firstOf(api, '/projects/' + project + '/boards');
      S.board = S.boards.find(b => b.ID === bid) || S.boards[0];
      if (!S.board) return bdMsg(h('div', h('h2', T('No board in %s', project))));
    } catch (e) {
      if (e.status === 404 && project && (recover(app, project) || params.project)) { params = { ...params, project: '', board: '' }; return start(); }
      return fail(e);
    }
    if (S.dead) return;
    if (!embed) {
      const canon = '/board/' + S.project + '/' + S.board.ID;
      if (location.pathname !== canon) app.setURL(canon + location.search);
      if (app.route) { app.route.params.project = S.project; app.route.params.board = String(S.board.ID); }
      setCtx(app, S.project, S.board);
    }
    const dm = app.session.ui && app.session.ui.DefaultMode;
    S.mode = app.prefs.get('board.mode.' + S.board.ID, app.prefs.get('board.mode', dm === 'list' ? 'list' : 'lanes')) === 'list' ? 'list' : 'lanes';
    const [sort, dir] = String(app.prefs.get('board.sort.' + S.board.ID, 'rank:1')).split(':');
    S.sort = SORTS.includes(sort) || allCols().includes(sort) ? sort : 'rank'; S.dir = dir === '-1' ? -1 : 1;
    const sw = app.prefs.get('board.swim.' + S.board.ID, 'none'); S.swim = SWIMS.includes(sw) ? sw : 'none';
    S.compact = app.prefs.get('board.compact', '0') === '1';
    const el = String((app.session.ui && app.session.ui.EmptyLanes) || '').toLowerCase() === 'hide' ? 'hide' : 'show';
    S.hideEmpty = app.prefs.get('board.empty_lanes', el) === 'hide';
    S.layout = app.prefs.get('board.layout.' + S.board.ID, '');
    S.secFold = new Set(String(app.prefs.get('board.secfold.' + S.board.ID, '')).split('\n').filter(Boolean));
    restoreFilters();
    S.cols = listCols();
    loadPins(); pins.register(app);
    renderToolbar(); renderBar();
    const savedP = UI.SavedFilters === 'off' ? Promise.resolve() : new Promise(res => {
      api.swr('/filters/favourite', d => { S.saved = d || []; renderToolbar(); res(); }).catch(() => res());
    });
    const queuesP = UI.SavedFilters === 'off' ? Promise.resolve() : new Promise(res => {
      api.swr('/servicedesk/queues', d => { S.queues = d || []; renderToolbar(); res(); }).catch(() => res());
    });
    if (S.scope.startsWith('filter:')) await savedP;
    if (S.scope.startsWith('queue:')) await queuesP;
    if (S.scope.startsWith('star:')) await starredP;
    if (S.dead) return;
    if (!isScrum()) loadCards(); // kanban: no sprints to wait for
    setBusy(1);
    api.swr('/boards/' + S.board.ID, d => { if (!S.dead) setBundle(d); })
      .catch(fail).finally(() => setBusy(-1));
    if (query.issue) { // the app opened the panel; the cursor goes to it once the cards are in, unless it moved on
      const t0 = Date.now();
      const pickIssue = () => {
        if (S.dead || app.panel.key !== query.issue) return;
        if (S.where.has(query.issue)) select(query.issue);
        else if (Date.now() - t0 < 8000) later(pickIssue, 150);
      };
      later(pickIssue, 100);
    }
    armAuto();
  }

  bindKeys();
  if (!embed) registerCommands();
  start();
  if (embed) {
    embed.setWho = ids => { S.mine = false; S.who = ids && ids.length ? new Set(ids) : null; layout(); renderBar(); };
    embed.select = key => { if (key && S.where.has(key)) select(key); };
  }

  // A console handle for testing: laneway.boardView.synth(1500).
  if (!embed) app.boardView = {
    state: S,
    setCards(cards) { S.cards = cards; S.total = cards.length; S.loaded = true; layout(); renderBar(); },
    synth(n = 1500) {
      const cols = columns(), base = S.cards.length ? S.cards : [{ Key: 'X-1', Summary: 's', Type: 'Task', Status: 'To Do', StatusID: '1', Priority: 'Medium' }];
      const out = [];
      for (let i = 0; i < n; i++) {
        const b = base[i % base.length], col = cols[i % Math.max(1, cols.length)];
        out.push({ ...b, Key: 'SYN-' + (i + 1), Summary: 'Synthetic card ' + (i + 1) + ' ' + b.Summary, StatusID: col ? col.StatusIDs[0] : b.StatusID, Status: col ? col.Name : b.Status, Points: String(1 + (i % 8)) });
      }
      this.setCards(out);
    },
    layout,
  };

  return () => {
    S.dead = true;
    app.createHere = null;
    for (const t of timers) clearTimeout(t);
    offs.forEach(f => f()); unreg.forEach(f => f());
    document.removeEventListener('keydown', touch, true);
    document.removeEventListener('pointermove', touch);
    for (const p of S.panes) p.vl.destroy();
    if (!embed) { delete app.boardView; context.parentElement.classList.remove('bd-sprinted'); }
  };
}
