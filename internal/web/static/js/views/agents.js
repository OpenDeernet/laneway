// Agents: every herdr agent on an issue, grouped by state (waiting on you first), with its live terminal beside it
// (lib/term.js: herdr agent attach over a WebSocket), attached once the cursor rests on a row, as in the TUI.
// Enter or a click types into it, ctrl+\ goes back to the list, z full screen. Tab adds the worktrees that have
// no agent. Live over /api/agents/events. /agents?agent=KEY selects KEY's agent (&pane=ID that one), &type=1 types into it.
import { h, clear, delegate, openURL } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { kbd, keyHint } from '../lib/keys.js';
import { ICON, LABEL } from '../lib/agents.js';
import { terminal, LEAVE } from '../lib/term.js';
import { T, Tn } from '../lib/i18n.js';

const GROUPS = ['blocked', 'working', 'done', 'idle', 'worktree'];
const GROUP_NAME = { blocked: T('Waiting on you'), working: T('Working'), done: T('Done'), idle: T('Idle'), unknown: T('Unknown'), worktree: T('Worktrees without an agent') };
const home = p => { const m = p && p.match(/^\/(?:home|Users)\/[^/]+/); return m ? '~' + p.slice(m[0].length) : p || ''; };
const LIVE = ['open', 'connecting', 'retry']; // terminal states that keep keys in it
const REST = 150; // ms the cursor rests on a row before its terminal attaches: holding j attaches nothing

export default async function mount(el, { app, scope, query }) {
  css('agents');
  for (let i = 0; !app.agents && i < 60; i++) await new Promise(r => setTimeout(r, 50));
  const { api, ui, bus } = app;
  let rows = [], sel = 0, bare = app.prefs.get('agents_bare', 'false') === 'true', stopAsk = '';
  const issues = new Map(); // key → {Card, Site, URL, Found}, looked up once on every site, the shown one first
  const card = key => (issues.get(key) || {}).Card;
  const away = key => { const is = issues.get(key); return !!(is && is.Found && is.Site !== app.session.site); };
  const siteName = key => issues.get(key).Site || app.session.defaultName || 'jira';
  const openIssue = key => (away(key) ? openURL(issues.get(key).URL) : app.panel.open(key));
  const asked = new Set();
  let dead = false;

  const list = h('div.ag-scroll', { tabindex: -1 }), head = h('div.ag-head');
  const empty = h('div.empty', { hidden: true });
  const info = h('div.ag-info'); // title, meta, actions: repainted when the row changes
  const host = h('div.ag-term'), hint = h('div.ag-hint', { 'aria-live': 'polite' });
  const box = h('div.ag-termbox', host, hint), none = h('div.dim', { hidden: true }, T('A worktree without an agent. S starts one in it.'));
  const detail = h('div.ag-detail', info, box, none);
  const root = h('div.agents', h('div.ag-list', head, list, empty), detail);
  el.append(root);

  const snap = () => app.agents.snapshot;
  const cur = () => rows[sel] || null;

  function build() {
    const keep = cur() && (cur().pane || cur().key);
    const s = snap();
    rows = s.Agents.filter(a => a.Key).map(a => ({ key: a.Key, pane: a.PaneID, agent: a, group: GROUPS.includes(a.Status) ? a.Status : 'idle', path: a.CWD }));
    const have = new Set(rows.map(r => r.key));
    if (bare) for (const [k, p] of Object.entries(s.Worktrees || {})) if (!have.has(k)) rows.push({ key: k, pane: '', agent: null, group: 'worktree', path: p });
    rows.sort((a, b) => GROUPS.indexOf(a.group) - GROUPS.indexOf(b.group) || a.key.localeCompare(b.key, undefined, { numeric: true }) || (a.agent ? a.agent.Name : '').localeCompare(b.agent ? b.agent.Name : ''));
    const i = rows.findIndex(r => (r.pane || r.key) === keep);
    sel = i >= 0 ? i : Math.min(sel, Math.max(rows.length - 1, 0));
    lookUp();
    paintList();
    paintDetail();
  }

  async function lookUp() {
    const miss = [...new Set(rows.map(r => r.key))].filter(k => !issues.has(k) && !asked.has(k));
    if (!miss.length) return;
    miss.forEach(k => asked.add(k));
    try {
      const r = await api.get('/agents/issues?keys=' + encodeURIComponent(miss.join(',')));
      for (const [k, is] of Object.entries(r.issues || {})) issues.set(k, is);
      if (!dead) { paintList(); paintDetail(); }
    } catch (e) { miss.forEach(k => asked.delete(k)); /* summaries are optional: asked again on the next change */ }
  }
  // As the TUI: … while looking, the site of another site's issue, or that none has it.
  function summary(key) {
    const is = issues.get(key);
    if (!is) return '…';
    if (!is.Found) return T('not found on any site');
    return (away(key) ? '[' + siteName(key) + '] ' : '') + is.Card.Summary;
  }

  function paintList() {
    const s = snap();
    const n = s.Agents.filter(a => a.Key).length, blocked = s.Agents.filter(a => a.Status === 'blocked').length;
    clear(head).append(h('span', Tn(n, '%d agent', '%d agents', n)), blocked ? h('span.chip', { style: { color: 'var(--warn)' } }, T('%d waiting on you', blocked)) : '',
      h('span.spacer'), h('button.btn.ghost.sm', { title: 'Tab', onclick: toggleBare }, bare ? T('Hide worktrees') : T('Show worktrees')));
    const kids = [];
    let g = '';
    rows.forEach((r, i) => {
      if (r.group !== g) { g = r.group; kids.push(h('div.ag-group.st-' + g, GROUP_NAME[g] || g)); }
      const c = card(r.key);
      kids.push(h('div.ag-row.st-' + r.group + (i === sel ? '.sel' : ''), { dataset: { key: r.key, i } },
        h('span.g', icon(ICON[r.group] || ICON.unknown)), h('span.mono.ag-key', r.key), h('span.sum', summary(r.key)), h('span.name', [r.agent && r.agent.Agent, c && c.Status].filter(Boolean).join(' · ')),
        h('span.sub', (r.agent && r.agent.Title ? r.agent.Title + ' · ' : '') + home(r.path))));
    });
    list.replaceChildren(...kids);
    empty.hidden = rows.length > 0;
    empty.textContent = !s.Available ? T('herdr is not running.') : bare ? T('No agents or worktrees for an issue.') : T('No agent works on an issue. S on an issue starts one, Tab lists worktrees.');
    const r = list.querySelector('.ag-row.sel'); if (r) r.scrollIntoView({ block: 'nearest' });
  }

  function mark() {
    list.querySelectorAll('.ag-row').forEach(r => r.classList.toggle('sel', +r.dataset.i === sel));
    const r = list.querySelector('.ag-row.sel'); if (r) r.scrollIntoView({ block: 'nearest' });
    stopAsk = '';
    paintDetail();
  }

  // ---- detail
  function paintDetail() {
    const r = cur();
    const c = r && card(r.key), a = r && r.agent;
    const sig = r ? (r.pane || r.key) + '|' + (a ? a.Status + a.Title : '') + '|' + summary(r.key) + (c ? c.Status + c.Assignee : '') : '';
    if (info._sig !== sig) {
      info._sig = sig;
      const btn = (label, key, fn, cls = '') => h('button.btn' + cls, { onclick: fn }, label, keyHint(key));
      clear(info);
      if (r) info.append(
        h('h2', h('a.issue-ref', { href: away(r.key) ? issues.get(r.key).URL : '/issue/' + r.key, onclick: e => { e.preventDefault(); openIssue(r.key); } }, r.key), ' ' + summary(r.key)),
        h('div.ag-meta', h('span.chip', icon(ICON[r.group] || ICON.unknown), ' ' + (LABEL[r.group] || r.group)), c && h('span', c.Status + ' · ' + (c.Assignee || T('unassigned'))), away(r.key) && h('span', T('on %s', siteName(r.key))),
          a && h('span', a.Agent + ' · ' + a.Name), h('span.mono', home(r.path)), a && a.Title && h('span', a.Title)),
        h('div.ag-actions',
          a && btn(T('Focus in herdr'), 'f', () => act('focus')), a && btn(T('Prompt'), 'p', () => act('prompt')), a && btn(T('New agent here'), 'N', () => act('new')),
          btn(a ? T('Another agent') : T('Start agent'), a ? 'alt+s' : 'S', () => app.agents.start(r.key, { another: !!a, path: a ? a.CWD : '' })),
          a && btn(T('Stop'), 'd', () => act('stop'), '.danger')));
    }
    box.hidden = !a; none.hidden = !r || !!a;
    schedule();
  }

  // ---- the terminal: one, re-attached to the cursor's agent once it rests there
  let tc = null, making = null, restTimer = 0, full = false;
  let ts = { state: 'connecting', text: T('loading the terminal…'), typing: false }; // as the terminal last said
  const visible = () => host.clientWidth > 0 && host.clientHeight > 0;
  function schedule() {
    clearTimeout(restTimer);
    const r = cur(), pane = r && r.agent ? r.pane : '';
    if (tc && tc.pane === pane) return;
    restTimer = setTimeout(() => attach(pane), REST);
  }
  async function ensure() {
    if (tc || dead) return tc;
    making = making || terminal(host, { app, onState: st => { ts = st; if (st.typing && tc && !LIVE.includes(st.state)) { tc.blur(); list.focus({ preventScroll: true }); } paintHint(); }, onLeave: leave }).then(t => { if (dead) { t.dispose(); return null; } tc = t; return t; });
    try { return await making; } catch (e) { making = null; ts = { state: 'closed', text: T('the terminal did not load: %s', e.message) }; paintHint(); return null; }
  }
  async function attach(pane, o) {
    if (!pane) { if (tc) tc.detach(); return; }
    if (!visible() && !full) return; // no size to give it (a phone: Enter goes full screen first)
    const t = await ensure();
    if (!t || dead) return;
    const r = cur();
    if (!r || r.pane !== pane) return; // moved on meanwhile
    t.attach(pane, o);
  }
  // Type into it: attach (again) where needed, full screen when the list leaves no room.
  async function type(o = {}) {
    const r = cur();
    if (!r || !r.agent) return r && openIssue(r.key);
    if (!visible() && !full) setFull(true);
    const t = await ensure();
    if (!t || dead) return;
    const again = !LIVE.includes(t.state) || t.pane !== r.pane;
    if (again || o.takeover) t.attach(r.pane, { again: true, takeover: o.takeover });
    t.focus();
  }
  function leave() {
    if (tc) tc.blur();
    if (full) setFull(false);
    list.focus({ preventScroll: true });
  }
  function setFull(on) {
    full = on;
    root.classList.toggle('term-full', on);
    if (tc) tc.fit();
    paintHint();
  }

  const leaveKey = () => { const b = app.keys.registry().find(x => x.id === 'terminal:' + LEAVE); return kbd((b && b.specs[0]) || LEAVE).join(' '); };
  const K = s => h('kbd', s);
  function paintHint() {
    const s = ts, dot = h('span.ag-dot.st-' + s.state);
    const parts = [];
    const add = (...xs) => { if (parts.length) parts.push(h('span.dim', ' · ')); parts.push(...xs); };
    if (s.state === 'open') {
      add(dot, s.typing ? h('b', T('typing')) : T('attached'));
      if (s.typing) { add(K(leaveKey()), T(' back to the list')); add(K('⌃⇧C'), T(' copy')); add(T('shift+drag selects')); }
      else { add(K('⏎'), T(' or click to type')); add(K('z'), full ? T(' leave full screen') : T(' full screen')); add(K('t'), T(' take over input')); }
    } else if (LIVE.includes(s.state)) {
      add(dot, s.text || T('connecting…'));
    } else {
      add(dot, s.text || s.state);
      if (s.state !== 'gone') add(K('⏎'), s.state === 'taken' ? T(' take it back') : T(' attach again'));
      if (s.state === 'exited' || s.state === 'closed') add(K('t'), T(' take over input'));
    }
    hint.replaceChildren(...parts);
    root.classList.toggle('term-typing', !!s.typing);
  }

  // ---- actions
  async function act(what) {
    const r = cur(); if (!r || !r.agent) return r && ui.toast(T('%s has no agent here, only its worktree', r.key));
    const a = r.agent;
    try {
      if (what === 'focus') { await api.post('/agents/' + a.PaneID + '/focus'); ui.toast(T('Focused %s in herdr', a.Name)); }
      else if (what === 'prompt') {
        const text = await ui.prompt({ title: T('Prompt for %s', a.Name), multiline: true, placeholder: T('What should it do next?'), ok: T('Send') });
        if (text && text.trim()) { await api.post('/agents/' + a.PaneID + '/prompt', { Text: text }); ui.toast(T('Prompt sent')); }
      } else if (what === 'new') {
        return app.agents.start(r.key, { another: true, path: a.CWD });
      } else if (what === 'stop') {
        if (stopAsk !== a.PaneID) { stopAsk = a.PaneID; ui.toast(T('Stop %s and close its tab? d again', a.Name)); return; }
        stopAsk = '';
        await api.post('/agents/' + a.PaneID + '/stop'); ui.toast(T('Stopped %s', a.Name));
      }
    } catch (e) { ui.errToast(e); }
    app.agents.refresh();
  }

  function toggleBare() { bare = !bare; app.prefs.set('agents_bare', bare); build(); }
  const move = d => { if (!rows.length) return; sel = Math.max(0, Math.min(rows.length - 1, sel + d)); mark(); };

  const G = T('Agents');
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next'), { group: G });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous'), { group: G });
  scope.bind('Home', () => move(-rows.length), T('first'), { group: G, hidden: true });
  scope.bind('End', () => move(rows.length), T('last'), { group: G, hidden: true });
  scope.bind('Tab', toggleBare, T('show / hide worktrees without an agent'), { group: G, when: () => !app.panel.key });
  scope.bind('Enter', () => type(), T("type into the agent's terminal (%s back to the list; a worktree: open the issue)", LEAVE), { group: G, bar: T('type') });
  scope.bind(LEAVE, () => type(), T('type into the terminal / back to the list'), { group: G });
  scope.bind('z', () => { setFull(!full); if (full) type(); }, T('terminal full screen'), { group: G, bar: T('full screen') });
  scope.bind('t', () => type({ takeover: true }), T("take over the agent's input from another herdr attach"), { group: G });
  scope.bind('Escape', () => setFull(false), T('leave full screen'), { group: G, when: () => full });
  scope.bind('v', () => { const r = cur(); if (r) openIssue(r.key); }, T("open issue (another site's in Jira)"), { group: G, bar: T('issue') });
  scope.bind('o', () => { const r = cur(); if (r) openURL(away(r.key) ? issues.get(r.key).URL : app.session.baseURL + '/browse/' + r.key); }, T('open in Jira'), { group: G });
  scope.bind('f', () => act('focus'), T('focus the agent in herdr'), { group: G });
  scope.bind('p', () => act('prompt'), T('send the agent a prompt'), { group: G, bar: T('prompt') });
  scope.bind('N', () => act('new'), T('new agent in its directory (the start form)'), { group: G, bar: T('new agent') });
  scope.bind('S', () => { const r = cur(); if (r) app.agents.start(r.key, { another: !!r.agent, path: r.agent ? r.agent.CWD : '' }); }, T('start work on the issue (the start form; with an agent: another one beside it)'), { group: G, bar: T('start work') });
  scope.bind('d', () => act('stop'), T('stop the agent (twice)'), { group: G, bar: T('stop') });
  scope.bind('y', () => { const r = cur(); if (r && navigator.clipboard) navigator.clipboard.writeText(r.key).then(() => ui.toast(T('Copied %s', r.key))); }, T('copy key'), { group: G });
  scope.bind('r', () => { app.agents.refresh(); ui.toast(T('Refreshed')); }, T('refresh'), { group: G });

  delegate(list, 'click', '.ag-row', (e, t) => { sel = +t.dataset.i; mark(); });
  delegate(list, 'dblclick', '.ag-row', (e, t) => openIssue(t.dataset.key));
  hint.addEventListener('click', e => { if (e.target.closest('kbd')) return; type(); });

  const off = bus.on('agents', () => { if (!dead) build(); });
  const offDone = bus.on('issue:changed', () => { asked.clear(); });
  build();
  const want = query && query.agent;
  if (want) {
    const p = query.pane ? rows.findIndex(r => r.pane === query.pane) : -1, i = p >= 0 ? p : rows.findIndex(r => r.key === want);
    if (i >= 0) { sel = i; mark(); } else if (!bare && (snap().Worktrees || {})[want]) { bare = true; build(); sel = Math.max(0, rows.findIndex(r => r.key === want)); mark(); }
    if (query.type) app.setQuery({ type: null }); // done once: not again on back or reload
    if (query.type && cur() && cur().key === want && cur().agent) { if (app.panel.key) app.panel.close({ replace: true }); type(); }
  }
  paintHint();
  return () => { dead = true; off(); offDone(); clearTimeout(restTimer); if (tc) tc.dispose(); };
}
