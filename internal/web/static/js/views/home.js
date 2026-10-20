// Home: the start screen of ui.home's widgets (internal/home, the TUI's too): my work, inbox, sprint
// health, the running timer and a count per saved search. jk walk every row, enter opens it, esc goes
// to the board. Each widget loads on its own, so a slow one holds up nothing.
import { h, delegate, openURL } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { stateOf, latest } from '../lib/inbox.js';
import { duration, ago } from '../lib/fmt.js';
import { projectOf, boardOf } from './plan_ctx.js';
import { T, Tn } from '../lib/i18n.js';
import { modText } from '../lib/keys.js';

const TITLE = { work: T('My work'), inbox: T('Inbox'), sprint: T('Sprint'), timer: T('Timer'), filters: T('Saved searches') };
const MAX = 8; // rows a list widget shows; its head says how many more

export default function mount(el, { app, scope }) {
  css('home');
  const { api, ui } = app;
  const widgets = (app.session.home && app.session.home.Widgets) || [];
  const state = {}; // widget → {rows: [{label…, open()}], note, head}
  const sums = {}; // the timed issue's summary, by key
  let sel = 0, dead = false;
  const grid = h('div.home');
  el.append(grid);

  const statusPill = c => ui.statusPill(c.Status, c.Done ? 'done' : c.InProgress ? 'indeterminate' : 'new');
  const issueRow = (key, sum, extra, open) => ({ key, kids: [h('span.mono.hk', key), h('span.hs', sum), extra], open: open || (() => app.panel.open(key)) });
  const asView = (jql, name) => app.go('/board?' + new URLSearchParams({ sprint: 'jql:' + jql, vname: name }));

  const loaders = {
    async work() {
      const d = await api.get('/work');
      const open = (d.cards || []).filter(c => !c.Done);
      return { head: Tn(open.length, '%d open issue', '%d open issues', open.length), rows: open.map(c => issueRow(c.Key, c.Summary, statusPill(c))), more: () => app.go('/work'), note: T('Nothing open is yours.') };
    },
    async inbox() {
      const d = await api.get('/inbox');
      const now = Date.now();
      const news = d.threads.filter(t => { const s = stateOf(t, d.marks[t.ID], d.floor, now); return s.unread && !s.done && !s.snoozed; })
        .sort((a, b) => latest(b) - latest(a));
      return {
        head: T('%d unread', news.length), more: () => app.go('/inbox'), note: T('All caught up.'),
        rows: news.map(t => issueRow(t.Key, t.Summary, h('span.faint', ago(latest(t))),
          () => (t.Site !== d.site ? openURL(t.URL) : app.panel.open(t.Key)))), // another site's opens in Jira
      };
    },
    async sprint() {
      const project = projectOf(app);
      const board = project && await boardOf(app, project);
      if (!board) return { note: project ? T('%s has no scrum board.', project) : T('No project configured.'), rows: [] };
      const d = await api.get('/home/sprint/' + board.ID);
      if (!d) return { head: board.Name, note: T('No active sprint on %s.', board.Name), rows: [{ kids: [h('span.hs', T('Plan the next one'))], open: () => app.go('/planning/' + project + '/' + board.ID) }] };
      const s = d.Sprint, pct = n => Math.round(n * 100) + '%';
      const days = s.DaysLeft > 0 ? Tn(s.DaysLeft, '%d day left', '%d days left', s.DaysLeft) : s.DaysLeft === 0 ? T('last day') : Tn(-s.DaysLeft, '%d day over', '%d days over', -s.DaysLeft);
      const done = s.Points > 0 ? T('%s of %s points', s.DonePoints, s.Points) : T('%s of %s issues', s.Done, s.Issues);
      const bar = h('div.hbar' + (d.Behind ? '.behind' : ''), { title: T('%s done, %s of the time gone', pct(d.Progress), pct(s.Elapsed)) },
        h('span.done', { style: { width: pct(d.Progress) } }), h('span.time', { style: { left: pct(s.Elapsed) } }));
      return {
        head: s.Name + ' · ' + days,
        rows: [{ kids: [h('div.hsprint', bar, h('div.hline', h('span', T('%s done', done)), h('span.spacer'), d.Behind ? h('span.warn', T('behind')) : h('span.faint', T('%s of the time gone', pct(s.Elapsed)))), s.Goal && h('div.faint.hgoal', s.Goal))],
          open: () => app.go('/board/' + project + '/' + board.ID) }],
      };
    },
    async timer() {
      const t = app.timer && app.timer.current;
      if (!t) return { note: T('No timer running. T starts one on the selected issue.'), rows: [] };
      if (!(t.key in sums)) {
        try { sums[t.key] = (await api.get('/issues/' + t.key + '/card')).Summary || ''; } catch (e) { sums[t.key] = ''; }
      }
      const ms = app.timer.elapsed();
      return { rows: [issueRow(t.key, sums[t.key], h('span.accent.mono', ms < 60000 ? '<1m' : duration(Math.floor(ms / 1000))))] };
    },
    async filters() {
      const fs = await api.get('/home/filters', { fresh: true });
      return {
        note: modText(T('No saved searches. Star a Jira filter, or a search with ctrl+s in Q.')),
        rows: fs.map(f => ({ kids: [h('span.hs', { title: f.JQL }, f.Name), f.Err ? h('span.err', { title: f.Err }, '!') : h('span.mono.hn', String(f.Count))], open: () => asView(f.JQL, f.Name) })),
      };
    },
  };

  // The rows on screen in order, for jk and enter.
  const flat = () => widgets.flatMap(w => (state[w] && state[w].shown) || []);
  function paintOne(w) {
    const s = state[w], box = grid.querySelector('[data-w="' + w + '"]');
    if (!box) return;
    const rows = s && s.rows ? s.rows.slice(0, MAX) : [];
    if (s) s.shown = rows;
    const more = s && s.rows && s.rows.length > MAX ? s.rows.length - MAX : 0;
    const head = h('div.hhead', h('span.ht', TITLE[w]), h('span.spacer'), s && s.head && h('span.faint', s.head),
      s && s.more && h('button.btn.link.sm', { onclick: s.more }, more ? T('+%d more', more) : T('Open')));
    const body = !s ? h('div.hnote.faint', T('Loading…')) : s.error ? h('div.hnote.err', s.error) : !rows.length ? h('div.hnote.faint', s.note || T('Nothing here.')) : null;
    box.replaceChildren(head, body || h('div.hrows', ...rows.map(r => h('div.hrow', { dataset: { w } }, ...r.kids))));
    mark();
  }
  // mark shows the selection; scroll brings it into view (a move, not a widget loading).
  function mark(scroll) {
    const all = flat();
    sel = Math.max(0, Math.min(sel, all.length - 1));
    grid.querySelectorAll('.hrow.sel').forEach(r => r.classList.remove('sel'));
    const rows = grid.querySelectorAll('.hrow');
    const r = rows[sel]; if (r) { r.classList.add('sel'); if (scroll) r.scrollIntoView({ block: 'nearest' }); }
  }
  async function load(w) {
    try { state[w] = await loaders[w](); } catch (e) { state[w] = { error: e.message, rows: [] }; }
    if (!dead) paintOne(w);
  }
  function loadAll() {
    grid.replaceChildren(...widgets.map(w => h('section.hw', { dataset: { w } })));
    widgets.forEach(w => { delete state[w]; paintOne(w); load(w); });
  }

  const G = T('Home');
  const move = d => { const n = flat().length; if (n) { sel = Math.max(0, Math.min(n - 1, sel + d)); mark(true); } };
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next row'), { group: G });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous row'), { group: G });
  scope.bind('Enter', () => { const r = flat()[sel]; if (r) r.open(); }, T('open'), { group: G, bar: T('open') });
  scope.bind('Escape', () => app.go('/board'), T('to the board'), { group: G, bar: T('board') });
  scope.bind('r', loadAll, T('refresh'), { group: G, bar: T('refresh') });
  delegate(grid, 'click', '.hrow', (e, t) => { sel = [...grid.querySelectorAll('.hrow')].indexOf(t); mark(); const r = flat()[sel]; if (r) r.open(); });

  const offs = [
    app.bus.on('timer', () => widgets.includes('timer') && load('timer')),
    app.bus.on('timer:tick', () => widgets.includes('timer') && load('timer')),
    app.bus.on('issue:changed', () => widgets.includes('work') && load('work')),
  ];
  loadAll();
  return () => { dead = true; offs.forEach(f => f()); };
}
