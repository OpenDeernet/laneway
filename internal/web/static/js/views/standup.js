// Standup: a strip that goes round the people (← →), Everyone first with the board walked right to left, then each
// person's cards and what they did, those heard ticked; with ui.standup_timer on, a timer beside it, each turn counting
// down (ui.standup_length split, or ui.standup_timebox) and the whole standup's time; below, the stop's rows, over the view the board showed last.
// A picks who takes part, kept per board. Park a card for after (P, kept per view): Everyone's parking lot comes last.
// Beside it, past a grip like the issue panel's, the board view itself on the same view, filtered to the person shown.
// Stops come from /standup/lines, built like the TUI's.
import { h, clear, delegate } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { avatar } from '../lib/ui.js';
import { projectOf, boardsOf, lastBoard, setCtx, switcher, recover } from './plan_ctx.js';
import { ymd, addDays, workdays } from '../lib/worktime.js';
import { grip } from '../lib/grip.js';
import mountBoard from './board.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

const PARK = T('Parking lot');

export default function mount(el, { app, scope, context, toolbar }) {
  css('work'); css('standup');
  const { api, ui, prefs } = app;
  const wd = workdays(app);
  const prevWorkday = d => { let x = addDays(d, -1); while (!wd.includes(x.getDay())) x = addDays(x, -1); return x; };
  const midnight = new Date(); midnight.setHours(0, 0, 0, 0);
  let since = null, data = null, err = '', dead = false, seq = 0, opened = false, loading = false;
  let project = projectOf(app, {});
  let sw = null, board = null, sprint = null, lines = [], folded = [], sel = 0, parkedKeys = [];
  // The round: the stops in the order gone round, the one shown, who was heard; the timer (ms, 0 unset).
  let stops = [], at = 0, shuffled = false, started = 0, turn = 0, paused = 0;
  const heard = new Set();

  // The standup on the left, the board on the right, split at a share of the width kept in prefs (50% by default).
  const root = h('div.standup'), boardEl = h('div.stboard'), gripEl = h('div.stgrip', { title: T('Drag to resize'), role: 'separator' });
  const split = h('div.stsplit', root, gripEl, boardEl);
  el.append(split);
  const showSplit = p => split.style.setProperty('--st-split', p + '%');
  const savedSplit = Number(prefs.get('standupSplit', ''));
  showSplit(savedSplit >= 25 && savedSplit <= 75 ? savedSplit : 50);
  grip(gripEl, { def: 50, min: 25, max: 75, show: showSplit, keep: p => { showSplit(p); prefs.set('standupSplit', p === 50 ? '' : String(p)); },
    pct: ev => { const b = split.getBoundingClientRect(); return (ev.clientX - b.left) * 100 / b.width; } });

  const picks = l => !!(l.Key || l.Unfold);
  const person = () => (stops[at] && stops[at].Person) || {};
  // The board view the walk takes: the one the board showed last, else the active sprint; parked per view.
  let view = null;
  const parkId = () => 'standup_park.' + (board ? board.ID : 0) + '.' + (view ? view.park : sprint || 0);
  const loadParked = () => { parkedKeys = (prefs.get(parkId(), '') || '').split(/\s+/).filter(Boolean); };
  // Who takes part, by account, kept per board; none: everyone.
  let inPeople = [];
  const peopleId = () => 'standup_people.' + (board ? board.ID : 0);
  const loadPeople = () => { inPeople = (prefs.get(peopleId(), '') || '').split(/\s+/).filter(Boolean); };
  // turnMs is each person's turn: the timebox, else the length split over who takes part.
  const turnMs = () => { const s = data.Settings, n = stops.length - 1; return 1000 * (s.Timebox || (n ? Math.floor(s.Length / n) : 0)); };

  // The project's board and its active sprint (sprint null till looked up), for the walk and the parking lot: a
  // board picked here gets its sprint as one opened with the page does, not the whole board.
  async function resolveBoard() {
    if (!project) return;
    try {
      if (!board) {
        const bs = await boardsOf(app, project), last = lastBoard(app, project);
        board = bs.find(b => b.ID === last) || bs.find(b => /scrum/i.test(b.Type)) || bs[0] || null;
        if (board && !last) setCtx(app, project, board);
      }
      if (board && sprint === null) {
        sprint = 0;
        if (!(app.lastView && app.lastView.board === board.ID)) {
          const b = await api.get('/boards/' + board.ID);
          const a = (b.sprints || []).find(s => s.State === 'active');
          sprint = a ? a.ID : 0;
        }
      }
    } catch (e) {
      // A project this site lacks (remembered on another): forget it, take the default.
      if (e.status === 404 && recover(app, project)) { project = projectOf(app, {}); return resolveBoard(); }
    }
  }

  // order lays the stops out, Everyone first: the board's order, or shuffled (again when reshuffle, else as gone round
  // so far, so a reload keeps it), and stays on who.
  function order(who, reshuffle) {
    const people = (data.Stops || []).slice(1).filter(s => !inPeople.length || inPeople.includes(s.Person.ID)), prev = stops.map(s => s.Person.ID);
    if (shuffled) for (let i = people.length - 1; i > 0; i--) { const j = Math.floor(Math.random() * (i + 1)); [people[i], people[j]] = [people[j], people[i]]; }
    const was = s => { const i = prev.indexOf(s.Person.ID); return i < 0 ? prev.length : i; }; // someone new goes last
    if (shuffled && !reshuffle && prev.length) people.sort((a, b) => was(a) - was(b));
    stops = [...(data.Stops || []).slice(0, 1), ...people];
    at = Math.max(0, stops.findIndex(s => (s.Person.ID || '') === (who || '')));
  }

  // build puts the shown stop's rows up, parked rows marked, and on Everyone's the parking lot last.
  function build() {
    const st = stops[at] || { Rows: [], Folded: [] };
    const mark = l => (l.Key && parkedKeys.includes(l.Key) ? { ...l, Marks: [l.Marks, 'parked'].filter(Boolean).join(' · ') } : l);
    lines = (st.Rows || []).map(mark);
    folded = (st.Folded || []).map(mark);
    if (at !== 0) return;
    const all = [...(st.Rows || []), ...(st.Folded || [])];
    const byKey = new Map(all.filter(l => l.Key).map(l => [l.Key, l]));
    const lot = parkedKeys.map(k => ({ ...(byKey.get(k) || { Key: k, Title: k }), parked: true }));
    if (lot.length) lines.push({ Head: PARK + ' (' + lot.length + ')' }, ...lot);
  }

  const cells = l => [l.Who, l.Age, l.Marks].filter(Boolean);
  const linked = l => l.Key && l.URL && l.Title.startsWith(l.Key) ? '[' + l.Key + '](' + l.URL + ')' + l.Title.slice(l.Key.length) : l.Title;
  const rowText = l => [linked(l), l.Who, l.Age, l.Marks, l.What].filter(Boolean).join(' · ');
  const copyText = () => {
    const st = stops[at];
    if (!st) return '';
    let t = st.Person.ID ? st.Person.Name + '\n\n' + st.Text : (data.Head ? data.Head + '\n\n' : '') + st.Text;
    const i = lines.findIndex(l => l.Head && l.Head.startsWith(PARK));
    if (i >= 0) t += '\n\n' + PARK + '\n' + lines.slice(i + 1).map(l => '- ' + rowText(l)).join('\n');
    return t.trim();
  };

  // ---- the timer

  const now = () => Date.now();
  const clock = ms => { const s = Math.max(0, Math.floor(ms / 1000)); return Math.floor(s / 60) + ':' + String(s % 60).padStart(2, '0'); };
  let tick = 0;
  function run() { clearInterval(tick); tick = setInterval(paintTimer, 1000); }
  const timerOn = () => !!(data && data.Settings.Timer);
  function pause() {
    const t = now();
    if (!started) { started = t; run(); ui.toast(T("The standup's timer runs")); }
    else if (!paused) { paused = t; clearInterval(tick); ui.toast(T('The timer is paused')); }
    else { const away = t - paused; started += away; if (turn) turn += away; paused = 0; run(); ui.toast(T('The timer runs again')); }
    paintTimer();
  }
  // The timer's parts keep their width whatever it shows, so nothing beside them moves: the turn and the total in
  // fixed mono slots (a turn's length, dim, before one runs), one button that starts, pauses or resumes.
  const timerEl = h('div.sttimer');
  function paintTimer() {
    clear(timerEl);
    const s = data && data.Settings;
    if (!s) return;
    const length = s.Length * 1000, t = paused || now(), total = started ? t - started : 0;
    const left = started && turn && turnMs() ? turnMs() - (t - turn) : null;
    timerEl.append(
      h('span.stleft' + (left !== null && left < 0 ? '.over' + (paused ? '' : '.flash') : '') + (left === null ? '.idle' : ''),
        h('span.stnum', left === null ? clock(turnMs()) : left < 0 ? '+' + clock(-left) : clock(left + 999)), h('span.dim', left !== null && left < 0 ? T('over') : T('left'))),
      h('span.sttotal' + (total > length ? '.over' : ''), h('span.stnum', clock(total)), T(' of '), h('span.stnum', clock(length))),
      h('button.btn.ghost.sm.stgo' + (paused ? '.on' : ''), { onclick: pause, title: started ? 'space' : T('space or → starts the timer') },
        icon(!started || paused ? 'play' : 'pause'), !started ? T('Start') : paused ? T('Resume') : T('Pause'), keyHint('Space')));
  }

  // go moves to stop i, marking the person left heard; a turn starts on a person's stop, the timer with the first.
  function go(i) {
    if (stops.length < 2) return ui.toast(T('No one is assigned a card on the board'));
    if (person().ID) heard.add(person().ID);
    at = (i + stops.length) % stops.length;
    turn = 0;
    if (at !== 0) {
      turn = paused || now();
      if (!started && timerOn()) { started = turn; run(); }
    }
    build(); sel = 0; if (lines.length && !picks(lines[0])) stepSel(1);
    paint();
  }

  // ---- painting

  // paintBoard keeps the board beside the standup: the board view itself, embedded on the standup's board and view
  // (its buttons in the page's toolbar, as on its own page; its keys left to the standup), its assignee filter the person shown (anyone on
  // Everyone) and the row picked selected on it. Picking someone in its own assignee filter goes to their stop.
  const boardView = h('div.stbview');
  boardEl.append(boardView);
  let embedded = null;
  const viewKey = () => board.ID + '|' + (view ? view.query : 'sprint=' + (sprint || 0));
  function boardQuery() {
    const q = new URLSearchParams(view ? view.query : '');
    if (q.get('sprint')) return { sprint: q.get('sprint') };
    if (q.get('backlog')) return { sprint: 'backlog' };
    if (q.get('jql')) return { sprint: 'jql:' + q.get('jql'), vname: view.name };
    return { sprint: sprint ? String(sprint) : 'active' };
  }
  function dropBoard() { if (embedded) embedded.dispose(); embedded = null; clear(toolbar); clear(boardView); }
  function paintBoard() {
    if (!board || !data) return;
    if (!embedded || embedded.id !== viewKey()) {
      dropBoard();
      const ctl = { onWho: followWho };
      const dispose = mountBoard(boardView, { app, params: { project, board: String(board.ID) }, query: boardQuery(), scope: { bind() {} }, context: null, toolbar, embed: ctl });
      embedded = { id: viewKey(), ctl, dispose, who: undefined };
    }
    const who = person().ID || '';
    if (embedded.who !== who) { embedded.who = who; embedded.ctl.setWho(who ? [who] : null); }
    embedded.ctl.select(lines[sel] && lines[sel].Key);
  }
  // followWho goes where the board's own assignee filter points: one person's stop, Everyone for anyone.
  function followWho(ids) {
    if (!ids || !ids.length) { if (at !== 0) go(0); return; }
    if (ids.length > 1) return;
    const i = stops.findIndex(s => s.Person.ID === ids[0]);
    if (i < 0) return ui.toast(T("They aren't in the round: A picks who takes part"));
    embedded.who = ids[0];
    if (i !== at) go(i);
  }

  // strip is the round: on its own line the people, each chip the same size whoever is shown (a ring marks them, a
  // badge those heard), scrolling sideways between the arrows; under them the one shown, the tools and the timer.
  function strip() {
    const chips = stops.map((st, i) => {
      const p = st.Person;
      const cls = 'button.stchip' + (i === at ? '.cur' : '') + (p.ID && heard.has(p.ID) ? '.heard' : '') + (st.Quiet ? '.quiet' : '');
      return h(cls, { onclick: () => go(i), title: p.ID ? p.Name + (st.Quiet ? T(' · no changes') : '') : T('Everyone: the board walked right to left') },
        p.ID ? avatar(p.Name, p.Avatar, 26) : h('span.stall', T('Everyone')),
        p.ID && heard.has(p.ID) && h('span.sttick', '✓'));
    });
    const st = stops[at] || { Person: {} }, all = (data.Stops || []).length - 1;
    return h('div.ststrip',
      h('div.stround', h('button.btn.ghost.sm', { onclick: () => go(at - 1), title: '←' }, icon('chevron-left'), keyHint('ArrowLeft')), h('div.stpeople', ...chips),
        h('button.btn.ghost.sm', { onclick: () => go(at + 1), title: '→' }, icon('chevron-right'), keyHint('ArrowRight'))),
      h('div.stbar',
        h('div.stwhom', st.Person.ID ? [h('span.stname', st.Person.Name), st.Quiet && h('span.dim', T(' · no changes'))] : h('span.stname', T('Everyone')),
          h('span.dim', ' · ' + (at ? T('%d of %d', at, stops.length - 1) : T('the board')))),
        h('button.btn.ghost.sm', { onclick: pickPeople, title: 'A' }, T("Who's in "), h('span.stnum.stcount', (stops.length - 1) + '/' + all), keyHint('A')),
        h('button.btn.ghost.sm' + (shuffled ? '.on' : ''), { onclick: shuffle, title: 's' }, icon('shuffle'), T('Shuffle'), keyHint('s')),
        timerOn() && timerEl));
  }

  // seePerson keeps the people's scroll across a repaint, moved only as far as shows the one shown.
  function seePerson(was) {
    const row = root.querySelector('.stpeople'), cur = row && row.querySelector('.stchip.cur');
    if (!row) return;
    row.scrollLeft = was;
    if (!cur) return;
    const l = cur.offsetLeft, r = l + cur.offsetWidth; // the row is the chips' offsetParent
    if (l < row.scrollLeft) row.scrollLeft = l;
    else if (r > row.scrollLeft + row.clientWidth) row.scrollLeft = r - row.clientWidth;
  }

  function paint() { paintList(); paintBoard(); }

  function paintList() {
    const was = (root.querySelector('.stpeople') || {}).scrollLeft || 0;
    clear(root);
    const label = since ? since.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'short' }) : '…';
    root.append(h('div.sthead', h('div.sttitle', h('h2', T('Standup')), view && view.name && h('span.chip', { title: T('The view the board showed last') }, view.name), h('span.dim', T('since %s', label)), data && data.Head && h('span.dim', data.Head), loading && data && h('span.dim', T('loading…'))),
      h('div.stbtns', h('button.btn.ghost', { onclick: () => step(-1), title: '[' }, icon('chevron-left'), T('earlier'), keyHint('[')), h('button.btn.ghost', { onclick: () => step(1), title: ']' }, T('later'), icon('chevron-right'), keyHint(']')),
        h('button.btn', { onclick: copy, title: 'y' }, T('Copy'), keyHint('y')))));
    if (err) return root.append(h('div.empty', err));
    if (!data) return root.append(h('div.loading', T('Loading…')));
    root.append(strip());
    seePerson(was);
    paintTimer();
    if (!lines.length) return root.append(h('div.empty', T('No changes since %s', label)));
    sel = Math.max(0, Math.min(sel, lines.length - 1));
    if (!picks(lines[sel])) stepSel(1);
    let sec = null;
    lines.forEach((l, i) => {
      if (l.Head) {
        sec = h('section.stsec', h('h3', l.Head, l.Unfold && h('button.btn.ghost.sm', { onclick: () => { sel = i; unfold(); } }, T(' show (z)'), keyHint('z'))));
        return root.append(sec);
      }
      (sec || root).append(rowEl(l, i));
    });
    const r = root.querySelector('.strow.sel'); if (r) r.scrollIntoView({ block: 'nearest' });
    seePerson(was); // again with the rows in: they may have changed the width
  }

  function rowEl(l, i) {
    return h('div.strow' + (i === sel ? '.sel' : '') + (l.parked ? '.parked' : ''), { dataset: l.Key ? { key: l.Key, i } : { i } },
      h('span.wkey.mono', l.Key), h('div.main', h('span.wsum', l.Key ? l.Title.slice(l.Key.length + 1) : l.Title),
        h('span.what' + (l.What === 'no activity' ? '.quiet' : ''), l.What === 'no activity' ? T('no activity') : l.What || '')),
      h('span.stwho' + ((l.Age || '').includes('stale') ? '.stale' : ''), cells(l).join(' · ')));
  }

  function stepSel(d) {
    for (let i = sel + d; i >= 0 && i < lines.length; i += d) if (picks(lines[i])) { sel = i; return true; }
    if (!picks(lines[sel]) && d > 0) return stepSel(-1);
    return false;
  }
  function move(d) { if (stepSel(d)) { paint(); if (app.panel.key && lines[sel].Key) app.panel.open(lines[sel].Key); } }

  function unfold() {
    const i = lines.findIndex(l => l.Unfold);
    if (i < 0) return ui.toast(T('Nothing folded'));
    lines[i] = { ...lines[i], Unfold: false };
    lines.splice(i + 1, 0, ...folded);
    folded = [];
    sel = i; stepSel(1); paint();
  }

  function park() {
    const l = lines[sel];
    if (!l || !l.Key) return ui.toast(T('No card to park'));
    const i = parkedKeys.indexOf(l.Key);
    if (i >= 0) parkedKeys.splice(i, 1); else parkedKeys.push(l.Key);
    prefs.set(parkId(), parkedKeys.join(' '));
    const was = l.Key;
    build();
    sel = Math.max(0, lines.findIndex(x => x.Key === was && !x.parked));
    ui.toast(i >= 0 ? T('%s out of the parking lot', was) : T('%s parked for after the standup', was));
    paint();
  }

  async function pickPeople() {
    const all = (data && data.Stops || []).slice(1).map(s => s.Person);
    if (!all.length) return ui.toast(T('No one is assigned a card on the board'));
    const everyone = { ID: '', Name: T('Everyone on the board') };
    const r = await ui.pick({ title: T('Who takes part'), items: [everyone, ...all], multi: true, enterPicks: true, selected: all.filter(p => inPeople.includes(p.ID)), label: p => p.Name, placeholder: T('People…') });
    if (!r || dead) return;
    inPeople = r.includes(everyone) ? [] : r.map(p => p.ID);
    prefs.set(peopleId(), inPeople.join(' '));
    order(person().ID, false);
    build(); sel = 0; if (lines.length && !picks(lines[0])) stepSel(1);
    ui.toast(inPeople.length ? Tn(stops.length - 1, '%d person takes part', '%d people take part', stops.length - 1) : T('Everyone on the board takes part'));
    paint();
  }

  function shuffle() {
    shuffled = !shuffled;
    if (data) { order(person().ID, true); build(); }
    ui.toast(shuffled ? T('A random order') : T("The board's order"));
    paint();
  }

  async function load() {
    const my = ++seq;
    err = ''; loading = true; paint();
    try {
      await resolveBoard();
      if (sw && !dead) sw.label(project, board);
      if (!board) throw new Error(project ? T('No board found for %s. alt+p picks a project.', project) : T('No board found for this site. alt+p picks a project.'));
      view = app.lastView && app.lastView.board === board.ID ? app.lastView : null;
      loadParked(); loadPeople();
      const q = (since ? 'since=' + ymd(since) + '&' : '') + 'board=' + board.ID + '&' + (view ? view.query : 'sprint=' + (sprint || 0));
      const d = await api.get('/standup/lines?' + q, { fresh: true });
      if (dead || my !== seq) return;
      const who = person().ID;
      data = d; since = new Date(d.Since + 'T00:00:00');
      if (!opened) shuffled = !!d.Settings.Shuffle;
      order(who, false);
      build(); sel = 0; if (lines.length && !picks(lines[0])) stepSel(1);
      if (!opened && d.Settings.First && stops.length > 1) { opened = true; return go(1); }
      opened = true;
    } catch (e) { if (dead || my !== seq) return; err = e.message; }
    loading = false;
    paint();
  }

  // reset starts the standup over, for another board: its people, unheard, the timer stopped.
  function reset() {
    data = null; stops = []; at = 0; lines = []; folded = []; sel = 0; heard.clear(); dropBoard();
    started = turn = paused = 0; clearInterval(tick); opened = false; since = null;
  }

  function step(n) {
    if (!since) return;
    if (n < 0) since = prevWorkday(since);
    else {
      let x = addDays(since, 1);
      while (!wd.includes(x.getDay())) x = addDays(x, 1);
      if (x < midnight) since = x; else return ui.toast(T('The previous workday is the latest to start from'));
    }
    sel = 0; load();
  }
  function copy() {
    if (!data) return;
    navigator.clipboard.writeText(copyText()).then(() => ui.toast(T('Copied the standup')), e => ui.errToast(e));
  }
  const open = () => { const l = lines[sel]; if (!l) return; if (l.Unfold) unfold(); else if (l.Key) app.panel.open(l.Key); };

  delegate(root, 'click', '.strow', (e, row) => { sel = +row.dataset.i; paint(); if (row.dataset.key) app.panel.open(row.dataset.key); });

  const G = { group: T('Standup') };
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next'), G);
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous'), G);
  scope.bind(['l', 'ArrowRight'], () => go(at + 1), T('next person'), { ...G, bar: T('person') });
  scope.bind(['h', 'ArrowLeft'], () => go(at - 1), T('previous person'), G);
  scope.bind('Space', pause, T('start / pause the timer'), { ...G, bar: T('timer'), when: timerOn });
  scope.bind('A', pickPeople, T('who takes part'), { ...G, bar: T("who's in") });
  scope.bind('s', shuffle, T("a random order / the board's"), G);
  scope.bind('Enter', open, T('open issue (or show Off the board)'), { ...G, bar: T('open') });
  scope.bind('z', unfold, T('show Off the board'), G);
  scope.bind('P', park, T('park the card for after the standup'), { ...G, bar: T('park') });
  scope.bind('[', () => step(-1), T('a workday further back'), { ...G, bar: T('day') });
  scope.bind(']', () => step(1), T('a workday forward'), { ...G, bar: T('day') });
  sw = switcher(app, { scope, context, project, board: null, scrum: false, group: T('Standup'), onPick: r => { project = r.project; board = r.board; sprint = null; view = null; reset(); load(); } });
  scope.bind('y', copy, T('copy the stop as markdown, parking lot included'), { ...G, bar: T('copy') });
  scope.bind('r', load, T('refresh'), G);

  clear(toolbar);
  load();
  return () => { dead = true; clearInterval(tick); dropBoard(); };
}
