// Roadmap: the project's epics as bars on a time axis. H/L move a bar, </> its end, e grips one end for
// h/l, a drag moves it (its edges resize); the dates are written once the moves pause, u takes them back.
// f shows the epic's issues as a board view, F filters by key or summary words. Epics with a plan-level
// parent (an initiative) sit under it, one foldable row whose bar spans them.
import { h, clear } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { remPx, onChange as onMetrics } from '../lib/metrics.js';
import { hwheel } from '../lib/hscroll.js';
import { isZero, shortDate } from '../lib/fmt.js';
import { resolve, switcher, noBoard } from './plan_ctx.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

const DAY = 86400000;
const ZOOMS = [2, 4, 8, 14, 24, 40, 64]; // px per day
const labelW = () => (parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--rm-lw')) || 20) * remPx(); // .rm-label / .rm-corner, in rem
const ms = t => (isZero(t) ? null : +new Date(t));
const midnight = t => { const d = new Date(t); return new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime(); };
const addDays = (t, n) => { const d = new Date(t); return new Date(d.getFullYear(), d.getMonth(), d.getDate() + n).getTime(); };
const ymd = t => { if (t == null) return ''; const d = new Date(t); return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0'); };
const iso = t => (t == null ? '0001-01-01T00:00:00Z' : new Date(t).toISOString());
const SAVE_MS = 800; // as the TUI's roadmapSaveDelay

export default async function mount(el, { app, params, scope, context, toolbar }) {
  css('roadmap');
  const sc = await resolve(app, params, { scrum: false });
  let { project } = sc;
  let canStart = true, grip = '', saveT = 0, saving = 0;
  const pending = new Map(); // key → {it, was: {Start, End, DatesFromSprints}} until written
  let epics = [], zoom = Number(app.prefs.get('roadmap.zoom', 3)), cur = 0, open = new Set(), shut = new Set(), rows = [], token = 0;
  if (!(zoom >= 0 && zoom < ZOOMS.length)) zoom = 3;
  let t0 = 0, t1 = 0;
  const scroller = h('div.rm', { tabindex: -1 }); hwheel(scroller);
  el.append(scroller);
  switcher(app, { scope, context, project, boards: false, group: T('Roadmap'), onPick: r => { project = r.project; epics = []; app.setURL('/roadmap/' + project, { push: true }); load(); } });
  toolbar.append(h('span.spacer'),
    h('button.btn', { title: T('Zoom out (-)'), 'aria-label': T('Zoom out'), onclick: () => setZoom(zoom - 1) }, icon('minus'), keyHint('-')),
    h('button.btn', { title: T('Zoom in (+)'), 'aria-label': T('Zoom in'), onclick: () => setZoom(zoom + 1) }, icon('plus'), keyHint('+')),
    h('button.btn', { title: T('Today (.)'), onclick: () => today() }, T('Today'), keyHint('.')));
  const said = h('span.rm-said.dim', { role: 'status', 'aria-live': 'polite' });
  let filter = '';
  const find = h('input.input.rm-find', { type: 'search', placeholder: T('Filter (F)'), spellcheck: false, 'aria-label': T('Filter epics by key or summary'),
    oninput: () => { filter = find.value.trim(); cur = 0; draw(); } });
  find.addEventListener('keydown', e => {
    if (e.key === 'Escape') { e.stopPropagation(); if (find.value) { find.value = ''; filter = ''; draw(); } find.blur(); }
    if (e.key === 'Enter') { e.stopPropagation(); find.blur(); }
  });
  toolbar.prepend(find, said);

  const ppd = () => ZOOMS[zoom] * remPx() / 14; // px per day, scales with the font size
  const xOf = t => (t - t0) / DAY * ppd();

  async function load(fresh) {
    const my = ++token;
    if (!project) { clear(scroller).append(noBoard(T('Roadmap'), '')); return; }
    if (!epics.length) clear(scroller).append(h('div.loading', T('Loading…')));
    try {
      const d = await app.api.get('/roadmap/' + encodeURIComponent(project), { fresh });
      if (my !== token) return;
      epics = d.Epics || []; canStart = d.CanSetStart !== false;
      cur = Math.min(cur, Math.max(epics.length - 1, 0));
      draw(true);
    } catch (e) {
      if (my !== token) return;
      clear(scroller).append(h('div.empty', h('h2', T('Could not load')), h('p', e.message), h('button.btn', { onclick: () => load(true) }, T('Retry'))));
    }
  }

  // The roadmap's open epics blocking it (TUI roadmapBlock): done or off-roadmap blockers don't count.
  const openBlockers = it => {
    const bs = (it.BlockedBy || []).map(k => epics.find(x => x.Key === k)).filter(b => b && !b.Done);
    return bs.length ? bs : null;
  };
  // A blocker that ends after it starts.
  const blockConflict = (it, bs) => { const s = ms(it.Start); return s != null && bs.some(b => { const e = ms(b.End); return e != null && e > s; }); };
  const span = it => {
    let s = ms(it.Start), e = ms(it.End);
    if (s == null && e == null) return null;
    if (s == null) s = e - 7 * DAY;
    if (e == null) e = s + 7 * DAY;
    return [midnight(s), midnight(Math.max(e, s)) + DAY];
  };
  // TUI roadmapGroups + groupEpic: the parents in the order first met, each one bar from its epics' first
  // start to last end, their progress summed; derived, so drawn soft.
  function groups() {
    const at = new Map();
    for (const e of epics) {
      if (!e.Parent) continue;
      let g = at.get(e.Parent);
      if (!g) at.set(e.Parent, g = { Key: e.Parent, Summary: e.ParentSummary || '', epics: [], Done: true, Children: 0, DoneChildren: 0, Points: 0, DonePoints: 0, DatesFromSprints: true, Start: iso(null), End: iso(null), Status: '' });
      g.epics.push(e);
      g.Done = g.Done && e.Done;
      g.Children += e.Children || 0; g.DoneChildren += e.DoneChildren || 0; g.Points += e.Points || 0; g.DonePoints += e.DonePoints || 0;
      for (const t of [ms(e.Start), ms(e.End)]) {
        if (t == null) continue;
        if (ms(g.Start) == null || t < ms(g.Start)) g.Start = iso(t);
        if (ms(g.End) == null || t > ms(g.End)) g.End = iso(t);
      }
    }
    for (const g of at.values()) g.Status = Tn(g.epics.length, '%d epic', '%d epics', g.epics.length);
    return [...at.values()];
  }
  const matches = it => { const t = (it.Key + ' ' + it.Summary).toLowerCase(); return filter.toLowerCase().split(/\s+/).every(w => t.includes(w)); };
  const cat = e => (e.Done ? 'done' : e.DoneChildren > 0 || /progress|review/i.test(e.Status) ? 'indeterminate' : 'new');

  function draw(reset) {
    const keepScroll = scroller.scrollLeft, keepTop = scroller.scrollTop;
    const now = Date.now();
    let lo = now, hi = now;
    for (const e of epics) { const s = span(e); if (s) { lo = Math.min(lo, s[0]); hi = Math.max(hi, s[1]); } }
    t0 = addDays(midnight(lo), -21); t1 = addDays(midnight(hi), 28);
    const w = xOf(t1);
    rows = [];
    // A filter keeps the epics it matches and those with issues it matches, those shown (TUI roadmapMatch).
    const shown = e => { const kids = filter ? (e.Kids || []).filter(k => matches(k)) : open.has(e.Key) ? e.Kids || [] : []; return filter && !kids.length && !matches(e) ? null : kids; };
    const epicRows = (e, inGroup) => {
      const kids = shown(e); if (!kids) return;
      rows.push({ e, inGroup });
      for (const k of kids) rows.push({ e: k, kid: true, parent: e, inGroup });
    };
    for (const e of epics) if (!e.Parent) epicRows(e);
    for (const g of groups()) {
      if (filter && !g.epics.some(shown)) continue;
      rows.push({ e: g, group: true });
      if (!shut.has(g.Key)) for (const e of g.epics) epicRows(e, true);
    }
    cur = Math.min(cur, Math.max(rows.length - 1, 0));

    const inner = h('div.rm-inner', { style: { width: labelW() + w + 'px', '--wk': 7 * ppd() + 'px', '--wko': xOf(mondayOnOrBefore(t0)) + 'px' } });
    inner.append(header(w));
    const list = h('div.rm-rows', { role: 'list' });
    rows.forEach((r, i) => list.append(rowEl(r, i, w)));
    if (!epics.length) list.append(h('div.empty', T('No epics in %s.', project)));
    else if (!rows.length) list.append(h('div.empty', T('No epic matches %s.', filter)));
    inner.append(list);
    inner.append(h('div.rm-today', { style: { left: labelW() + xOf(midnight(now)) + ppd() / 2 + 'px' }, title: T('Today') }));
    clear(scroller).append(inner);
    // A fresh draw puts today a third into the timeline (beside the sticky labels).
    if (reset) keepScrollApply(Math.max(xOf(now) - (scroller.clientWidth - labelW()) / 3, 0), keepTop); else keepScrollApply(keepScroll, keepTop);
    markCur();
  }
  const keepScrollApply = (l, t) => { scroller.scrollLeft = l; scroller.scrollTop = t; };
  const mondayOnOrBefore = t => { const d = new Date(t); return addDays(t, -((d.getDay() + 6) % 7)); };

  function header(w) {
    const months = h('div.rm-months'), ticks = h('div.rm-ticks');
    for (let d = new Date(t0); d.getTime() < t1;) {
      const from = d.getTime(); d = new Date(d.getFullYear(), d.getMonth() + 1, 1);
      const to = Math.min(d.getTime(), t1);
      months.append(h('span', { style: { left: xOf(from) + 'px', width: xOf(to) - xOf(from) + 'px' } }, new Date(from).toLocaleDateString(undefined, { month: 'long', year: 'numeric' })));
    }
    const dayMode = ZOOMS[zoom] >= 14, step = dayMode ? 1 : 7;
    for (let t = dayMode ? t0 : mondayOnOrBefore(t0); t < t1; t = addDays(t, step)) {
      const d = new Date(t);
      ticks.append(h('span', { class: dayMode && (d.getDay() === 0 || d.getDay() === 6) ? 'wk' : '', style: { left: xOf(t) + 'px', width: step * ppd() + 'px' } }, dayMode ? d.getDate() : ZOOMS[zoom] >= 4 ? d.getDate() : ''));
    }
    return h('div.rm-head', h('div.rm-corner', project), h('div.rm-axis', { style: { width: w + 'px' } }, months, ticks));
  }

  function rowEl(r, i, w) {
    const it = r.e, s = span(it), kid = r.kid, grp = r.group, c = kid ? (it.Done ? 'done' : 'new') : cat(it);
    const isOpen = grp ? !shut.has(it.Key) : !kid && open.has(it.Key);
    const foldable = grp || (it.Kids && it.Kids.length);
    const blocked = kid || grp ? null : openBlockers(it), bad = blocked && blockConflict(it, blocked);
    const pct = kid ? (it.Done ? 100 : 0) : it.Points > 0 ? Math.round(it.DonePoints / it.Points * 100) : it.Children ? Math.round(it.DoneChildren / it.Children * 100) : it.Done ? 100 : 0;
    const label = h('div.rm-label', { onclick: () => select(i) },
      !kid ? h('button.rm-fold', { 'aria-label': isOpen ? T('Fold') : T('Unfold'), 'aria-expanded': isOpen, tabindex: -1, onclick: ev => { ev.stopPropagation(); select(i); toggle(); } }, foldable ? icon(isOpen ? 'chevron-down' : 'chevron-right') : '') : h('span.rm-fold'),
      h('span.rm-key', it.Key), h('span.rm-sum', { title: it.Summary }, it.Summary),
      blocked && h('span.rm-block' + (bad ? '.bad' : ''), { title: T('Blocked by %s', blocked.map(b => b.Key).join(', ')) + (bad ? T(', ending after this starts') : '') }, icon(bad ? 'ban' : 'link')),
      grp ? h('span.rm-cnt', { title: it.Status }, it.epics.length) : !kid && it.Children > 0 && h('span.rm-cnt', it.DoneChildren + '/' + it.Children));
    const track = h('div.rm-track', { style: { width: w + 'px' }, onclick: () => select(i) });
    if (s) {
      const when = shortDate(s[0]) + ' – ' + shortDate(s[1] - DAY) + (it.DatesFromSprints ? T(' (from sprints)') : '');
      track.append(h('div.rm-bar.c-' + c + (it.DatesFromSprints ? '.soft' : '') + (grip && i === cur ? '.grip-' + grip : ''), {
        dataset: { i }, onpointerdown: grp ? null : ev => dragStart(ev, i),
        style: { left: xOf(s[0]) + 'px', width: Math.max(xOf(s[1]) - xOf(s[0]), 6) + 'px', '--pct': pct + '%' }, title: `${it.Key} ${it.Summary}\n${when}\n${it.Status}` + (kid ? '' : it.Points > 0 ? T(', %d%% of %s points', pct, it.Points) : T(', %d%% of %s issues', pct, it.Children)),
        onclick: ev => { ev.stopPropagation(); if (dragged) { dragged = false; return; } select(i); app.panel.open(it.Key); },
      }, h('span', it.Summary)));
    } else track.append(h('span.rm-nodate', T('no dates')));
    return h('div.rm-row' + (kid ? '.kid' : '') + (grp ? '.group' : '') + (r.inGroup ? '.ingroup' : ''), { role: 'listitem', dataset: { i } }, label, track);
  }

  function select(i) { if (i !== cur) letGo(); cur = i; markCur(); sayBlockers(); }
  // TUI roadmapSayBlockers: landing on a blocked epic names its blockers.
  function sayBlockers() {
    const r = rows[cur];
    if (r && !r.kid && r.e.BlockedBy && r.e.BlockedBy.length) said.textContent = T('%s is blocked by %s', r.e.Key, r.e.BlockedBy.join(', '));
  }
  const curKey = () => rows[cur] && rows[cur].e.Key;
  // The epic's issues (a parent's for an issue row) as a view of the board (TUI RoadmapIssues).
  function issuesView() {
    const r = rows[cur]; if (!r) return;
    const k = r.kid ? r.parent.Key : r.e.Key;
    if (pending.size) save();
    app.go('/board/' + encodeURIComponent(project) + '?' + new URLSearchParams({ sprint: 'jql:parent = ' + k + ' ORDER BY rank', vname: r.group ? T('Parent: %s', k) : T('Epic: %s', k) }));
  }
  // y: every epic as a markdown table (TUI roadmapTable), with the parent column when there are parents.
  async function copyTable() {
    if (!epics.length) return;
    const { markdownTable } = await import('./reports.js');
    const parents = epics.some(e => e.Parent), day = t => ymd(ms(t));
    const head = ['Epic', 'Summary', 'Status', 'Start', 'End', 'Done'].concat(parents ? ['Parent'] : []);
    const body = epics.map(e => [`[${e.Key}](${app.session.baseURL}/browse/${e.Key})`, e.Summary, e.Status, day(e.Start), day(e.End),
      e.Points > 0 ? `${e.DonePoints}/${e.Points}p` : e.Children > 0 ? `${e.DoneChildren}/${e.Children}` : ''].concat(parents ? [e.Parent || ''] : []));
    copy(markdownTable(head, body), Tn(epics.length, '%d epic as a markdown table', '%d epics as a markdown table', epics.length));
  }
  const copy = (text, what) => (navigator.clipboard ? navigator.clipboard.writeText(text).then(() => app.ui.toast(T('Copied %s', what)), app.ui.errToast) : app.ui.toast(T('No clipboard here'), { kind: 'err' }));

  // ---- dates. shift moves the row's start by ds days and its end by de (TUI shiftRoadmap); no dates: from today.
  const step = () => Math.max(1, Math.round(14 / ZOOMS[zoom])); // about a cell a press
  function shift(ds, de, { quiet } = {}) {
    const r = rows[cur]; if (!r) return false;
    const it = r.e;
    if (r.group) { app.ui.toast(T('A parent spans its epics: move those')); return false; }
    if (ds && !canStart) { app.ui.toast(T('No start date field in Jira: < > move the end')); return false; }
    if (!pending.has(it.Key)) pending.set(it.Key, { it, was: { Start: it.Start, End: it.End, DatesFromSprints: it.DatesFromSprints } });
    let s = ms(it.Start), e = ms(it.End);
    if (s == null && e == null) s = midnight(Date.now());
    if (s != null) { s = addDays(midnight(s), ds); if (!de && e != null && s > e) s = midnight(e); } // a start grip stops at the end
    e = addDays(midnight(e != null ? e : s), de);
    if (s != null && e < s) e = s;
    it.Start = iso(s); it.End = iso(e); it.DatesFromSprints = false;
    if (!quiet) { say(it); draw(); scheduleSave(); }
    return true;
  }
  const say = it => { said.textContent = `${it.Key} ${ms(it.Start) != null ? shortDate(ms(it.Start)) : '?'} – ${shortDate(ms(it.End))}`; };
  function scheduleSave() { clearTimeout(saveT); saveT = setTimeout(save, SAVE_MS); }
  async function save() {
    clearTimeout(saveT);
    const todo = [...pending.values()]; pending.clear();
    if (!todo.length) return;
    saving++;
    try {
      for (const { it, was } of todo) {
        await app.api.post('/roadmap/' + it.Key + '/dates', { Start: ymd(ms(it.Start)), End: ymd(ms(it.End)) });
        import('./fields.js').then(m => m.pushUndo(app, T('the dates of %s', it.Key), async () => {
          await app.api.post('/roadmap/' + it.Key + '/dates', { Start: ymd(ms(was.Start)), End: ymd(ms(was.End)) });
          app.bus.emit('issue:changed', { key: it.Key, what: T('%s dates back', it.Key) });
        }));
      }
      said.textContent = T('Saved %s', todo.map(t => t.it.Key).join(', '));
    } catch (e) {
      app.ui.errToast(new Error(T('Dates not saved: %s', e.message)));
      saving--; load(true); return; // Jira's dates again
    }
    saving--;
    for (const { it } of todo) app.bus.emit('issue:changed', { key: it.Key, what: T('%s dates', it.Key) });
  }
  function letGo() { if (grip) { grip = ''; said.textContent = T('Bar let go'); draw(); } }
  function cycleGrip() {
    if (!rows[cur]) return;
    grip = { '': 'start', start: 'end', end: '' }[grip];
    said.textContent = grip === 'start' ? T("Holding the bar's start · h/l move it · e the other end · esc let go") : grip ? T("Holding the bar's end · h/l move it · e the other end · esc let go") : T('Bar let go');
    draw();
  }

  // A drag moves the bar a day at a time; within 6px of an edge it moves that end.
  let drag = null, dragged = false;
  function dragStart(ev, i) {
    if (ev.button !== 0) return;
    const bar = ev.currentTarget, r = bar.getBoundingClientRect();
    const edge = ev.clientX - r.left < 6 ? 'start' : r.right - ev.clientX < 6 ? 'end' : '';
    if (edge === 'start' && !canStart) return;
    select(i);
    const it = rows[i].e;
    drag = { x: ev.clientX, it, days: 0, edge, from: { Start: it.Start, End: it.End, DatesFromSprints: it.DatesFromSprints } };
    bar.setPointerCapture(ev.pointerId);
    bar.addEventListener('pointermove', dragMove);
    bar.addEventListener('pointerup', dragEnd, { once: true });
    bar.addEventListener('pointercancel', dragEnd, { once: true });
  }
  function dragMove(ev) {
    const days = Math.round((ev.clientX - drag.x) / ppd());
    if (days === drag.days) return;
    drag.days = days; dragged = true;
    const { it, from } = drag;
    Object.assign(it, from);
    const was = pending.get(it.Key); pending.delete(it.Key);
    if (!canStart && !drag.edge) shift(0, days, { quiet: true }); // no start field: the end only
    else shift(drag.edge === 'end' ? 0 : days, drag.edge === 'start' ? 0 : days, { quiet: true });
    if (was) pending.set(it.Key, was);
    const s = span(it), bar = ev.currentTarget;
    bar.style.left = xOf(s[0]) + 'px'; bar.style.width = Math.max(xOf(s[1]) - xOf(s[0]), 6) + 'px';
    bar.classList.remove('soft');
    say(it);
  }
  function dragEnd(ev) {
    ev.currentTarget.removeEventListener('pointermove', dragMove);
    const d = drag; drag = null;
    if (!d || !d.days) { dragged = false; return; }
    draw(); scheduleSave();
  }

  function markCur() {
    scroller.querySelectorAll('.rm-row.cur').forEach(n => n.classList.remove('cur'));
    const n = scroller.querySelector(`.rm-row[data-i="${cur}"]`);
    if (n) { n.classList.add('cur'); n.scrollIntoView({ block: 'nearest' }); }
  }
  function toggle() {
    const r = rows[cur]; if (!r) return;
    if (r.group) { shut.has(r.e.Key) ? shut.delete(r.e.Key) : shut.add(r.e.Key); draw(); return; }
    const e = r.kid ? r.parent : r.e;
    if (!(e.Kids && e.Kids.length)) return;
    open.has(e.Key) ? open.delete(e.Key) : open.add(e.Key);
    cur = rows.findIndex(x => x.e === e); draw();
  }
  function setZoom(z) {
    z = Math.min(Math.max(z, 0), ZOOMS.length - 1); if (z === zoom) return;
    const centre = t0 + (scroller.scrollLeft + (scroller.clientWidth - labelW()) / 2) / ppd() * DAY;
    zoom = z; app.prefs.set('roadmap.zoom', z);
    draw();
    scroller.scrollLeft = Math.max(xOf(centre) - (scroller.clientWidth - labelW()) / 2, 0);
  }
  function today() { scroller.scrollTo({ left: Math.max(xOf(Date.now()) - (scroller.clientWidth - labelW()) / 2, 0), behavior: 'smooth' }); }
  const move = d => { if (rows.length) { letGo(); cur = Math.min(Math.max(cur + d, 0), rows.length - 1); markCur(); sayBlockers(); } };
  const pan = d => scroller.scrollBy({ left: d * 120, behavior: 'smooth' });

  const held = () => !!grip;
  scope.bind(['h', 'ArrowLeft'], () => (grip === 'start' ? shift(-step(), 0) : shift(0, -step())), T('move the held end earlier'), { group: T('Roadmap'), when: held, writes: true });
  scope.bind(['l', 'ArrowRight'], () => (grip === 'start' ? shift(step(), 0) : shift(0, step())), T('move the held end later'), { group: T('Roadmap'), when: held, writes: true });
  scope.bind('Escape', letGo, T('let the bar go'), { group: T('Roadmap'), when: held, bar: T('let go') });
  scope.bind('Escape', () => { find.value = ''; filter = ''; cur = 0; draw(); said.textContent = T('Filter cleared'); }, T('clear the filter'), { group: T('Roadmap'), when: () => !grip && !!filter });
  scope.bind('e', cycleGrip, T("grip the bar's start, end, let go"), { group: T('Roadmap'), bar: T('grip') });
  scope.bind('H', () => shift(-step(), -step()), T('move the bar earlier'), { group: T('Roadmap'), bar: T('move'), writes: true });
  scope.bind('L', () => shift(step(), step()), T('move the bar later'), { group: T('Roadmap'), bar: T('move'), writes: true });
  scope.bind('<', () => shift(0, -step()), T('end earlier'), { group: T('Roadmap'), writes: true });
  scope.bind('>', () => shift(0, step()), T('end later'), { group: T('Roadmap'), writes: true });
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next row'), { group: T('Roadmap') });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous row'), { group: T('Roadmap') });
  scope.bind('Home', () => move(-rows.length), T('first row'), { group: T('Roadmap'), hidden: true });
  scope.bind('End', () => move(rows.length), T('last row'), { group: T('Roadmap'), hidden: true });
  scope.bind(['h', 'ArrowLeft'], () => pan(-1), T('scroll left'), { group: T('Roadmap') });
  scope.bind(['l', 'ArrowRight'], () => pan(1), T('scroll right'), { group: T('Roadmap') });
  scope.bind(['+', '='], () => setZoom(zoom + 1), T('zoom in'), { group: T('Roadmap'), bar: T('zoom'), writes: true });
  scope.bind(['-', '_'], () => setZoom(zoom - 1), T('zoom out'), { group: T('Roadmap'), bar: T('zoom'), writes: true });
  scope.bind('.', today, T('scroll to today'), { group: T('Roadmap'), bar: T('today') });
  scope.bind('Space', toggle, T('fold epic issues'), { group: T('Roadmap'), bar: T('fold') });
  scope.bind('Enter', () => { const r = rows[cur]; if (r) app.panel.open(r.e.Key); }, T('open'), { group: T('Roadmap'), bar: T('open') });
  scope.bind('R', () => load(true), T('reload'), { group: T('Roadmap') });
  scope.bind('f', issuesView, T("the epic's issues as a board view"), { group: T('Roadmap') });
  scope.bind('F', () => find.focus(), T('filter by key or summary'), { group: T('Roadmap'), bar: T('filter') });
  scope.bind('E', () => { const k = curKey(); if (k && app.actions.menu) app.actions.menu(k); }, T("quick edit the row's issue"), { group: T('Roadmap') });
  scope.bind('o', () => { const k = curKey(); if (k) window.open(app.session.baseURL + '/browse/' + k, '_blank', 'noopener'); }, T('open in Jira'), { group: T('Roadmap'), writes: true });
  scope.bind('y', copyTable, T('copy the roadmap as a markdown table'), { group: T('Roadmap'), writes: true });
  scope.bind('Y', () => { const k = curKey(); if (k) copy(app.session.baseURL + '/browse/' + k, T('link')); }, T('copy link'), { group: T('Roadmap'), writes: true });
  scope.bind('n', () => app.actions.create({ project, type: app.session.ui.RoadmapEpicType || 'Epic' }), T('new epic'), { group: T('Roadmap') });
  const offBus = app.bus.on('issue:changed', () => { if (!saving && !pending.size) load(true); });
  const offM = onMetrics(() => rows.length && draw());
  const off = () => { offBus(); offM(); };

  await load();
  return () => { token++; off(); if (pending.size) save(); }; // moves not yet written go now
}
