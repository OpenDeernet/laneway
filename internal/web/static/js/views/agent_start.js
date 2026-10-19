// Start work on an issue: the TUI's start work form (internal/ui/jira_work.go) as a modal, one for every
// entry point (S, alt+s, the palette, the panel's button, the agents screen's S and N): openStartAgent.
// Rows as there: Agent, Branch, Prompt, Also; plus Where when the issue has a worktree or agents already
// (another agent beside one, its worktree, focus one, or a new worktree). The cursor waits on the button:
// enter starts as filled in. The server streams each step; a failure stays in the modal with its fix.
// S on an issue whose agent runs focuses that agent, as the TUI attaches to it.
import { h, clear } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { LABEL } from '../lib/agents.js';
import { T } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

const home = p => { const m = p && p.match(/^\/(?:home|Users)\/[^/]+/); return m ? '~' + p.slice(m[0].length) : p || ''; };
const ICON = { run: 'circle-dashed', ok: 'check', warn: 'triangle-alert', err: 'x' };
let openFor = '';

// refProblem is why git would refuse a branch name (git check-ref-format --branch), '' when it takes it.
export function refProblem(b) {
  if (!b) return '';
  if (b.startsWith('-')) return T("a branch name can't start with -");
  if (/[\s~^:?*[\\\x00-\x1f\x7f]/.test(b)) return T('no spaces or ~ ^ : ? * [ \\ in a branch name');
  if (b.includes('..')) return T('no .. in a branch name');
  if (b.includes('@{') || b === '@') return T('no @{ in a branch name');
  if (b.startsWith('/') || b.endsWith('/') || b.includes('//')) return T('no empty parts between slashes');
  if (b.endsWith('.')) return T("a branch name can't end with .");
  if (b.split('/').some(p => p.startsWith('.') || p.endsWith('.lock'))) return T('no part may start with . or end in .lock');
  return '';
}

// openStartAgent(app, key, {another, path}): another preselects another agent in the issue's worktree
// (path: which one, as an agent's directory).
export async function openStartAgent(app, key, { another = false, path = '' } = {}) {
  const { api, ui } = app;
  if (app.session && app.session.demo) return ui.toast(T('Not available in demo'));
  if (!key || openFor === key) return;
  css('agent_start');
  openFor = key;
  let f;
  try { f = await api.get('/issues/' + encodeURIComponent(key) + '/work', { fresh: true }); } catch (e) { openFor = ''; return ui.errToast(e); }
  let notice = '';
  if (!another && f.Herdr && f.Running.length) {
    const a = f.Running[0];
    try {
      await api.post('/agents/' + encodeURIComponent(a.PaneID) + '/focus');
      openFor = '';
      return ui.toast(T('%s: focused %s in herdr', key, a.Name), { ms: 6000, action: { label: T('Another agent'), run: () => openStartAgent(app, key, { another: true, path: a.CWD }) } });
    } catch (e) { notice = T('Focus failed: %s', e.message); another = true; path = a.CWD; }
  }
  try { form(app, key, f, { another, path, notice }); } catch (e) { openFor = ''; ui.errToast(e); }
}

// startWork is the old name, kept for callers: force was "another".
export const startWork = (app, key, { force = false, ...o } = {}) => openStartAgent(app, key, { another: force, ...o });

function form(app, key, f, { another, path, notice }) {
  const { ui } = app;
  const problems = [...f.Problems];

  // Where the work goes: the issue's worktrees and agents, then a new worktree.
  const where = [];
  for (const w of f.Worktrees) {
    if (w.Agents.length) where.push({ id: 'another', path: w.Path, branch: w.Branch, label: T('Another agent in %s', home(w.Path)), detail: w.Agents.map(a => a.Agent + ' ' + (LABEL[a.Status] || a.Status)).join(', ') });
    else if (w.Branch) where.push({ id: 'in', path: w.Path, branch: w.Branch, label: T('Its worktree %s', home(w.Path)), detail: w.Branch });
  }
  for (const a of f.Running) where.push({ id: 'focus', pane: a.PaneID, label: T('Focus %s in herdr', a.Name), detail: a.Agent + ' ' + (LABEL[a.Status] || a.Status) });
  where.push({ id: 'new', label: T('New worktree'), detail: T('on the branch below') });
  let at = where.findIndex(w => another ? w.id === 'another' && (!path || w.path === path) : w.id === 'in');
  if (at < 0 && another) at = where.findIndex(w => w.id === 'another');
  if (at < 0) at = where.findIndex(w => w.id === 'in');
  if (at < 0) at = where.length - 1;
  let kind = f.Agent;

  // ---- controls
  const pickBtn = (id, cls = '') => h('button.sw-pick' + cls, { type: 'button', id, 'aria-haspopup': 'listbox' });
  const agentBtn = pickBtn('sw-agent'), whereBtn = pickBtn('sw-where');
  const agentHint = h('span.sw-hint-l');
  const list = h('datalist#sw-branches', f.Existing.map(b => h('option', { value: b })));
  const branch = h('input.input.mono#sw-branch', { type: 'text', value: f.Branch, placeholder: f.Branch, autocomplete: 'off', autocapitalize: 'off' });
  branch.setAttribute('list', 'sw-branches'); // a getter only: h() can't set it
  const branchHint = h('span.sw-hint-l');
  const prompt = h('textarea.input.mono#sw-prompt', { rows: 6, value: f.Prompt, placeholder: T('Empty starts the agent without a prompt') });
  for (const x of [branch, prompt]) x.setAttribute('spellcheck', 'false'); // h() drops false values
  const promptHint = h('span.sw-hint-l');
  const also = h('input#sw-also', { type: 'checkbox', checked: true });
  const alsoText = h('span', f.Actions.join(' · '));
  const err = h('div.form-err.sw-err', { role: 'alert' });
  const steps = h('ol.sw-steps', { hidden: true, 'aria-live': 'polite' });
  const go = h('button.btn.primary.sw-go', { type: 'submit', autofocus: true });
  const cancel = h('button.btn', { type: 'button', onclick: () => m.close() }, T('Cancel'), keyHint('Escape'));
  const hint = h('div.sw-keys');

  const row = (id, label, forId, ...kids) => h('div.sw-row', { dataset: { row: id } }, h('label.sw-label', { htmlFor: forId }, label), h('div.sw-field', ...kids));
  const rows = {
    agent: row('agent', T('Agent'), 'sw-agent', agentBtn, agentHint),
    where: row('where', T('Where'), 'sw-where', whereBtn),
    branch: row('branch', T('Branch'), 'sw-branch', branch, list, branchHint),
    prompt: row('prompt', T('Prompt'), 'sw-prompt', prompt, promptHint),
    also: row('also', T('Also'), 'sw-also', h('label.check.sw-check', also, alsoText)),
  };
  rows.where.hidden = where.length < 2;
  rows.also.hidden = !f.Actions.length;

  const cur = () => where[at];
  function paint() {
    const w = cur();
    clear(agentBtn).append(h('span.sw-val', kind), h('span.sw-caret', icon('chevron-down')));
    agentHint.textContent = f.Missing.includes(kind) ? T('%s is not on PATH here: herdr may not find it', kind) : '';
    clear(whereBtn).append(h('span.sw-val', w.label), h('span.sw-detail', w.detail), h('span.sw-caret', icon('chevron-down')));
    const fixed = w.id === 'in' || w.id === 'another';
    branch.readOnly = fixed;
    if (fixed) branch.value = w.branch || '';
    else if (branch.dataset.fixed) branch.value = branch.dataset.typed || f.Branch;
    branch.dataset.fixed = fixed ? '1' : '';
    rows.branch.hidden = w.id === 'focus' || (fixed && !w.branch);
    rows.prompt.hidden = rows.agent.hidden = w.id === 'focus';
    rows.also.hidden = !f.Actions.length || w.id !== 'new' && w.id !== 'in';
    paintBranch();
    paintPrompt();
    go.replaceChildren(w.id === 'focus' ? T('Focus agent') : w.id === 'another' ? T('Start agent') : T('Start work'), keyHint('ctrl+Enter'));
    go.disabled = busy || problems.length > 0;
  }
  function paintBranch() {
    const w = cur(), v = branch.value.trim();
    const bad = w.id === 'new' ? refProblem(v) : '';
    branchHint.className = 'sw-hint-l' + (bad ? ' bad' : '');
    branch.setAttribute('aria-invalid', bad ? 'true' : 'false');
    if (bad) { branchHint.textContent = bad; return; }
    if (w.id !== 'new') { branchHint.textContent = T("the worktree's branch"); return; }
    const reuse = (v || f.Branch) === f.Branch ? f.BranchExists : f.Existing.includes(v);
    branchHint.textContent = (reuse ? T('exists: its worktree opens, or one is made on it') : f.Base ? T('new, from %s', f.Base) : T("new, from herdr's default base")) + ' · ' + home(f.Repo);
  }
  function paintPrompt() {
    const v = prompt.value;
    promptHint.textContent = !v.trim() ? T('the agent starts without a prompt') : v.includes('{key}') ? T('{key} becomes %s', key) : '';
  }
  branch.addEventListener('input', () => { if (!branch.readOnly) branch.dataset.typed = branch.value; paintBranch(); });
  prompt.addEventListener('input', paintPrompt);

  // ---- pickers: enter, a click or typing opens one (typing filters); ←/→ cycle
  const kindDetail = k => (k === f.Agent ? 'ui.work_agent' : '') + (f.Missing.includes(k) ? (k === f.Agent ? ' · ' : '') + T('not on PATH') : '');
  async function pickAgent(query = '') {
    const k = await ui.pick({ title: T('Agent — %s', key), items: f.Agents, current: kind, query, detail: kindDetail });
    if (k) { kind = k; err.textContent = ''; paint(); }
    agentBtn.focus();
  }
  async function pickWhere(query = '') {
    const w = await ui.pick({ title: T('Where — %s', key), items: where, current: cur(), query, label: w => w.label, detail: w => w.detail });
    if (w) { at = where.indexOf(w); err.textContent = ''; paint(); }
    whereBtn.focus();
  }
  agentBtn.addEventListener('click', () => pickAgent());
  whereBtn.addEventListener('click', () => pickWhere());
  const cycle = (btn, d) => {
    if (btn === agentBtn) { kind = f.Agents[(f.Agents.indexOf(kind) + d + f.Agents.length) % f.Agents.length]; }
    else at = (at + d + where.length) % where.length;
    paint();
  };

  // ---- keys: tab/shift+tab and ↑↓ between fields (↑ at the prompt's start, ↓ at its end leave it),
  // enter picks, toggles, or starts; ctrl+s or ctrl+enter start from anywhere; esc cancels.
  const stops = () => [agentBtn, whereBtn, branch, prompt, also, go].filter(x => !x.closest('[hidden]') && !x.disabled);
  const move = (from, d) => {
    const s = stops(), i = s.indexOf(from), n = s[Math.max(0, Math.min(s.length - 1, (i < 0 ? s.length - 1 : i) + d))];
    if (!n) return;
    n.focus();
    if (n === prompt && n !== from) { const at = d > 0 ? 0 : prompt.value.length; prompt.setSelectionRange(at, at); } // entered at the edge it came from
  };
  const body = h('form.sw', { onsubmit: e => { e.preventDefault(); submit(); } },
    h('div.sw-sum', f.Summary),
    notice && h('div.sw-notice', notice),
    problems.length > 0 && h('ul.sw-problems', problems.map(p => h('li', p))),
    h('div.sw-rows', rows.agent, rows.where, rows.branch, rows.prompt, rows.also),
    steps, err,
    h('div.sw-foot', cancel, go),
    hint);
  body.addEventListener('keydown', e => {
    if (busy || e.defaultPrevented || e.isComposing) return;
    const t = e.target, mods = e.ctrlKey || e.altKey || e.metaKey;
    if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !mods) {
      const d = e.key === 'ArrowDown' ? 1 : -1;
      if (t === prompt && (d < 0 ? prompt.selectionStart > 0 : prompt.selectionEnd < prompt.value.length)) return;
      e.preventDefault(); move(t, d); return;
    }
    if (t === agentBtn || t === whereBtn) {
      if ((e.key === 'ArrowLeft' || e.key === 'ArrowRight') && !mods) { e.preventDefault(); cycle(t, e.key === 'ArrowLeft' ? -1 : 1); return; }
      if (e.key.length === 1 && e.key !== ' ' && !mods) { e.preventDefault(); (t === agentBtn ? pickAgent : pickWhere)(e.key); }
      return;
    }
    if (t === also && e.key === 'Enter') { e.preventDefault(); also.checked = !also.checked; }
  });
  const hints = {
    agent: T('↵ or type to pick · ←→ cycle · tab/↑↓ field · ctrl+s start · esc cancel'),
    where: T('↵ or type to pick · ←→ cycle · tab/↑↓ field · ctrl+s start · esc cancel'),
    branch: T('↵ start · tab/↑↓ field · ctrl+s start · esc cancel'),
    prompt: T('↵ newline · ctrl+s or ctrl+↵ start · tab field · esc cancel'),
    also: T('↵ or space toggle · tab/↑↓ field · ctrl+s start · esc cancel'),
    go: T('↵ start · tab/↑↓ field · esc cancel'),
  };
  body.addEventListener('focusin', e => {
    const r = e.target.closest('.sw-row');
    hint.textContent = busy ? T('esc closes; the start goes on and reports when done') : hints[r ? r.dataset.row : 'go'] || hints.go;
  });

  // ---- start
  let busy = false, closed = false;
  const m = ui.modal(body, { title: T('Start work on %s', key), wide: true, className: 'sw-modal', onClose: () => { closed = true; openFor = ''; } });
  m.scope.bind(['ctrl+s', 'ctrl+Enter'], () => submit(), '', { input: true, hidden: true });
  paint();
  (problems.length ? cancel : go).focus(); // as the TUI: the cursor waits on the button
  hint.textContent = problems.length ? T('esc cancel') : hints.go;

  const stepEls = new Map();
  function showStep(s) {
    let li = stepEls.get(s.Step);
    if (!li) { li = h('li.sw-step'); stepEls.set(s.Step, li); steps.append(li); }
    li.className = 'sw-step st-' + s.State;
    clear(li).append(h('span.sw-g', ICON[s.State] ? icon(ICON[s.State]) : '·'), h('span', s.Text));
  }
  function lock(on) {
    busy = on;
    for (const x of [agentBtn, whereBtn, branch, prompt, also]) x.disabled = on;
    go.disabled = on || problems.length > 0;
    if (on) go.textContent = T('Starting…');
    body.classList.toggle('busy', on);
    if (!on) paint();
  }

  async function submit() {
    if (busy) return;
    const w = cur();
    err.textContent = '';
    if (problems.length) { err.textContent = problems[0]; return; }
    if (w.id === 'focus') {
      try { await app.api.post('/agents/' + encodeURIComponent(w.pane) + '/focus'); m.close(); ui.toast(T('%s: focused in herdr', key)); }
      catch (e) { err.textContent = T('%s (the herdr CLI does the focusing: is it on PATH?)', e.message); }
      return;
    }
    if (!kind) { err.textContent = T('pick an agent'); agentBtn.focus(); return; }
    const b = branch.value.trim();
    if (w.id === 'new' && refProblem(b)) { branch.focus(); return; } // said under the field
    const req = {
      Agent: kind, Prompt: prompt.value, Actions: !rows.also.hidden && also.checked,
      Another: w.id === 'another', Path: w.id === 'another' ? w.path : '',
      Branch: w.id === 'new' ? b : w.id === 'in' ? w.branch : '',
    };
    lock(true);
    clear(steps); stepEls.clear(); steps.hidden = false;
    go.focus();
    hint.textContent = T('esc closes; the start goes on and reports when done');
    let done = null, fail = '';
    try {
      const res = await fetch('/api/issues/' + encodeURIComponent(key) + '/work', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/x-ndjson' }, body: JSON.stringify(req) });
      if (!res.ok) { let e = ''; try { e = (await res.json()).error; } catch (x) { /* no body */ } throw new Error(e || res.statusText); }
      const rd = res.body.getReader(), dec = new TextDecoder();
      let buf = '';
      const line = l => { if (!l.trim()) return; const v = JSON.parse(l); if (v.Done) done = v.Done; else if (v.Error) fail = v.Error; else showStep(v); };
      for (;;) {
        const { value, done: end } = await rd.read();
        if (value) { buf += dec.decode(value, { stream: true }); const ls = buf.split('\n'); buf = ls.pop(); ls.forEach(line); }
        if (end) { line(buf); break; }
      }
      if (!done && !fail) fail = T('the server stopped answering mid-start: look in herdr, then start again');
    } catch (e) { fail = e.message || String(e); }
    app.agents && app.agents.refresh();
    if (fail) {
      lock(false);
      if (closed) return ui.toast(key + ': ' + fail, { kind: 'err', ms: 12000 });
      err.textContent = fail;
      (/branch/i.test(fail) && !rows.branch.hidden ? branch : go).focus();
      return;
    }
    finish(done);
  }

  function finish(r) {
    if (!closed) m.close();
    const show = { label: T('Agents'), run: () => app.go('/agents?agent=' + encodeURIComponent(key)) };
    if (r.Running) {
      app.api.post('/agents/' + encodeURIComponent(r.Pane) + '/focus').catch(() => {});
      return ui.toast(T('%s: its agent runs already, focused', key), { action: show });
    }
    const did = [...(r.Did || [])];
    if (r.Timer && app.timer && !app.timer.current) { app.timer.start(key); did.push(T('timer started')); }
    ui.toast(T('%s: %s started in %s', key, r.Agent, home(r.Path)) + (did.length ? ' · ' + did.join(', ') : ''), { ms: 8000, action: show });
    if (r.Warn) ui.toast(key + ': ' + r.Warn, { kind: 'err', ms: 10000 });
    app.bus.emit('issue:changed', { key });
  }
}
