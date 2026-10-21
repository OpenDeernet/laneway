// Coding agents (herdr): the live snapshot, agent state on cards and in the panel, and the keys
// that start work. app.agents.stateFor(key) → {status, count, agents, worktree} | null; bus 'agents' fires on change.
// Everything hides itself while herdr isn't running.
import { h, openURL } from './dom.js';
import { setIcon } from './icons.js';
import { css } from './css.js';
import { target } from './timer.js';
import { notify } from './notify.js';
import { T } from './i18n.js';
import { stream } from './streams.js';

export const GLYPH = { working: '⚙', blocked: '✋', done: '✓', idle: '○', unknown: '?', worktree: '◌' };
// ICON: the same states drawn (lib/icons.js); GLYPH stays for plain-text labels (palette rows).
export const ICON = { working: 'cog', blocked: 'hand', done: 'circle-check', idle: 'circle', unknown: 'circle-help', worktree: 'circle-dashed' };
export const LABEL = { working: T('working'), blocked: T('waiting on you'), done: T('done'), idle: T('idle'), unknown: T('unknown'), worktree: T('worktree, no agent') };
export const RANK = { blocked: 0, working: 1, done: 2, idle: 3, unknown: 4 };

export function install(app) {
  css('agents');
  const { api, bus, ui } = app;
  let snap = { Available: false, Agents: [], Worktrees: {} }, sig = '', byKey = new Map(), first = true;
  const tool = { GH: false, GLab: false };

  function index() {
    byKey = new Map();
    for (const a of snap.Agents) if (a.Key) (byKey.get(a.Key) || byKey.set(a.Key, []).get(a.Key)).push(a);
    for (const as of byKey.values()) as.sort((a, b) => (RANK[a.Status] ?? 9) - (RANK[b.Status] ?? 9));
  }
  function stateFor(key) {
    const as = byKey.get(key);
    if (as) return { status: as[0].Status, count: as.length, agents: as, worktree: snap.Worktrees[key] || '' };
    return snap.Worktrees && snap.Worktrees[key] ? { status: 'worktree', count: 0, agents: [], worktree: snap.Worktrees[key] } : null;
  }

  // One chip: icon, a count beyond one; title says which agents.
  function paintChip(el, key) {
    const s = stateFor(key);
    el.hidden = !s;
    if (!s) return;
    el.className = 'agent-chip st-' + s.status;
    setIcon(el, ICON[s.status] || ICON.unknown, s.count > 1 ? String(s.count) : '');
    el.title = (s.count ? s.agents.map(a => a.Name + ': ' + (LABEL[a.Status] || a.Status)).join('\n') : LABEL.worktree + ' ' + s.worktree) + '\n' + T('click: show in Agents');
  }
  const chip = key => { const el = h('span.agent-chip', { dataset: { agentKey: key } }); paintChip(el, key); return el; };
  // A card's or row's key element carries the chip as its last child.
  function stamp(keyEl, key) {
    let el = keyEl.querySelector(':scope > .agent-chip');
    if (!stateFor(key)) { if (el) el.remove(); return; }
    if (!el) { el = h('span.agent-chip'); keyEl.append(el); }
    paintChip(el, key);
  }
  function paintAll() {
    for (const el of document.querySelectorAll('.agent-chip[data-agent-key]')) paintChip(el, el.dataset.agentKey);
    for (const row of document.querySelectorAll('#view [data-key]')) {
      const k = row.querySelector('.ckey, .l-key');
      if (k) stamp(k, row.dataset.key);
    }
    const nav = document.querySelector('#nav a[data-name=agents]');
    if (nav) nav.hidden = !snap.Available;
  }

  function set(next) {
    const s = JSON.stringify(next);
    if (s === sig) return;
    const was = new Map(snap.Agents.map(a => [a.PaneID, a.Status]));
    sig = s; snap = next; index(); paintAll(); listAgents();
    if (!first) for (const a of snap.Agents) {
      if (a.Status === 'blocked' && was.get(a.PaneID) !== 'blocked' && a.Key) {
        const show = () => app.go('/agents?agent=' + encodeURIComponent(a.Key));
        ui.toast(T('%s: the agent waits on you', a.Key), { action: { label: T('Open'), run: show }, ms: 8000 });
        notify(T('%s: the agent waits on you', a.Key), a.Name || '', show, 'agent:' + a.PaneID);
      }
    }
    first = false;
    bus.emit('agents', snap);
  }

  // A palette row per running agent, as the TUI's.
  let rows = [];
  function listAgents() {
    rows.forEach(u => u()); rows = [];
    for (const a of [...snap.Agents].filter(a => a.Key).sort((x, y) => x.Key.localeCompare(y.Key))) {
      rows.push(app.commands.register({ id: 'agent:' + a.PaneID, title: GLYPH[a.Status] + ' ' + a.Key + '  ' + (LABEL[a.Status] || a.Status) + '  ' + a.Name, group: 'Agents',
        run: () => app.go('/agents?agent=' + encodeURIComponent(a.Key)) }));
    }
  }

  // Server-Sent Events, with a poll while the stream is down.
  let poll = 0;
  const fetchOnce = () => api.get('/agents', { fresh: true }).then(set).catch(() => set({ Available: false, Agents: [], Worktrees: {} }));
  const stopPoll = () => { clearInterval(poll); poll = 0; };
  function connect() {
    if (!window.EventSource) { poll = setInterval(fetchOnce, 5000); fetchOnce(); return; }
    // A stream another tab opened sends its next snapshot only on a change.
    fetchOnce();
    const es = stream('/api/agents/events');
    es.on('message', e => { stopPoll(); try { set(JSON.parse(e.data)); } catch (err) { console.error(err); } });
    es.on('error', () => { if (!poll) { fetchOnce(); poll = setInterval(fetchOnce, 5000); } });
  }
  connect();

  api.get('/agents/status').then(s => {
    tool.GH = s.GH; tool.GLab = s.GLab;
    const nav = document.querySelector('#nav a[data-name=review]');
    if (nav) nav.hidden = !(s.GH || s.GLab);
  }).catch(() => {});

  // start(key, {another, path}): the one start work modal (views/agent_start.js openStartAgent).
  const start = (key, opts) => import('../views/agent_start.js').then(m => m.openStartAgent(app, key, opts)).catch(e => ui.errToast(e));
  app.agents = {
    get available() { return snap.Available; },
    get snapshot() { return snap; },
    get tools() { return tool; },
    stateFor, chip, stamp, refresh: fetchOnce, start, startWork: (key, o = {}) => start(key, { another: !!o.force }),
    worktree: key => snap.Worktrees[key] || '',
  };

  // ---- keys and commands
  const demo = () => !!(app.session && app.session.demo);
  const pick = fn => () => { const k = target(app); if (k) fn(k); else ui.toast(T('Select an issue first')); };
  const need = fn => () => (demo() ? ui.toast(T('Not available in demo')) : pick(fn)());
  const herdr = () => !demo() && snap.Available; // as the TUI: start work needs herdr
  const copyBranch = async key => {
    try {
      const { Name } = await api.get('/issues/' + key + '/branch');
      await navigator.clipboard.writeText(Name);
      ui.toast(T('Copied %s', Name));
    } catch (e) { ui.errToast(e); }
  };
  const draftPR = async key => {
    if (!await ui.confirm({ title: T('Draft pull request'), text: T('Push the branch of %s and open a draft pull request?', key), ok: T('Open draft') })) return;
    const close = ui.toast(T('Pushing %s and opening a draft…', key), { ms: 120000 });
    try {
      const r = await api.post('/issues/' + key + '/pr');
      close();
      ui.toast(T('Draft opened: %s', r.URL), { action: { label: T('Open'), run: () => openURL(r.URL) }, ms: 10000 });
    } catch (e) { close(); ui.errToast(e); }
  };
  // A chip (card, row, panel) opens the agents screen on that issue's agent.
  document.addEventListener('click', e => {
    const c = e.target.closest && e.target.closest('.agent-chip');
    if (!c || c.hidden) return;
    const key = c.dataset.agentKey || (c.closest('[data-key]') || {}).dataset?.key;
    if (!key) return;
    e.preventDefault(); e.stopPropagation();
    app.go('/agents?agent=' + encodeURIComponent(key));
  }, true);
  const k = app.keys.scope('agents-global');
  k.bind('S', need(key => start(key)), T('start work: the form (agent, branch, prompt); focuses its agent if one runs'), { group: 'Agents', when: herdr, writes: true });
  k.bind('alt+s', need(key => start(key, { another: true })), T('start another agent in the issue\'s worktree'), { group: 'Agents', when: herdr });
  k.bind('mod+y', pick(copyBranch), T('copy branch name'), { group: 'Agents' });
  // The issue's agent's terminal in the issue's Terminal tab, typing (ctrl+\ there goes back to the issue, as the TUI's agent_back).
  const typeInto = key => { const s = stateFor(key); if (!s || !s.count) return ui.toast(T('%s has no agent: S starts one', key)); import('../views/issue.js').then(m => m.showTerminal(app, key)).catch(e => ui.errToast(e)); };
  k.bind('ctrl+\\', need(typeInto), T('type into the issue\'s agent (its terminal in the panel)'), { group: 'Agents', when: () => !demo() && snap.Available });
  const cmd = (id, title, run, when, keys) => app.commands.register({ id, group: 'Agents', get title() { return title(); }, keys, run, when });
  cmd('agents:start', () => { const s = stateFor(target(app)); return s && s.count ? T('Focus the agent of %s', target(app)) : T('Start work on %s', target(app)); }, need(key => start(key)), () => herdr() && !!target(app), 'S');
  cmd('agents:another', () => T('Start another agent on %s', target(app)), need(key => start(key, { another: true })), () => herdr() && !!target(app), 'alt+s');
  cmd('agents:terminal', () => T('Type into the agent of %s (its terminal in the panel)', target(app)), need(typeInto), () => !demo() && !!target(app) && !!(stateFor(target(app)) || {}).count, 'ctrl+\\');
  cmd('agents:show', () => T('Show the agent of %s in Agents', target(app)), need(key => app.go('/agents?agent=' + encodeURIComponent(key))), () => !demo() && !!target(app) && !!stateFor(target(app)));
  cmd('agents:branch', () => T('Copy branch name of %s', target(app)), pick(copyBranch), () => !!target(app), 'mod+y');
  const hasRepo = key => !!key && ((app.session && app.session.repos) || []).includes(key.slice(0, key.lastIndexOf('-'))); // as the TUI: jira.repos has it
  cmd('agents:pr', () => T('Open draft pull request for %s', target(app)), need(draftPR), () => !demo() && hasRepo(target(app)));
  app.agents.draftPR = demo() ? () => ui.toast(T('Not available in demo')) : draftPR;
}
