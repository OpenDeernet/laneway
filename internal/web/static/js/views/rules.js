// Rules: the config's rules, a dry run (TUI: laneway rules test) and the firing log, live.
// The engine runs in the server, so it fires whether or not this page is open.
import { h, clear } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { onRuleEvent } from '../lib/rules_feed.js';
import { enabled, supported, permission, setEnabled } from '../lib/notify.js';
import { T } from '../lib/i18n.js';

const list = v => (Array.isArray(v) ? v : v ? [v] : []);
const MATCH = [['Key', 'key'], ['Type', 'type'], ['Status', 'status'], ['FromStatus', 'from'], ['Assignee', 'assignee'], ['Priority', 'priority']];

// A match as chips: "status Done", "not type Bug".
function matchChips(m, prefix = '') {
  if (!m) return [];
  const out = [];
  for (const [f, label] of MATCH) if (list(m[f]).length) out.push(h('span.chip', prefix + label + ' ' + list(m[f]).join(' | ')));
  if (m.Summary) out.push(h('span.chip', prefix + T('summary /%s/', m.Summary)));
  if (m.ByMe != null) out.push(h('span.chip', prefix + (m.ByMe ? T('by me') : T('by others'))));
  if (m.Not) out.push(...matchChips(m.Not, T('not ')));
  return out;
}

const parseLine = l => {
  const m = l.match(/^(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d) ([^:]*): (.*)$/);
  return m ? { at: m[1], rule: m[2], text: m[3], action: 'log' } : { at: '', rule: '', text: l, action: 'log' };
};
const stamp = d => { const p = n => String(n).padStart(2, '0'); return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds()); };

export default function mount(el, { app, scope, toolbar }) {
  css('rules');
  const { api, ui } = app;
  let info = null, sel = 0, dead = false, live = [], file = [];
  const root = h('div.rules');
  const listEl = h('div.rules-list', { tabindex: 0 });
  const testEl = h('form.rules-test', { onsubmit: e => { e.preventDefault(); test(); } });
  const resEl = h('div.rules-res');
  const logEl = h('div.rules-log');
  root.append(h('section.rules-col', h('h3', T('Rules')), listEl),
    h('section.rules-col', h('h3', T('Try a change')), testEl, resEl),
    h('section.rules-log-wrap', h('h3', T('Firing log')), logEl));
  // The page shows once its rules, form and first test are in: filling in, they pushed the log down.
  el.append(h('div.loading', T('Loading…')));
  const show = () => { if (!root.isConnected) el.replaceChildren(root); };

  const notifyBtn = h('button.btn.ghost', { onclick: async () => { await setEnabled(!enabled()); paintNotify(); } });
  function paintNotify() {
    notifyBtn.hidden = !supported() || permission() === 'denied';
    notifyBtn.textContent = enabled() ? T('Notify: on') : T('Notify: off');
    notifyBtn.title = T('Show notify actions as browser notifications (N)');
  }
  toolbar.append(notifyBtn);
  paintNotify();

  function paintList() {
    clear(listEl);
    if (!info) return listEl.append(h('div.loading', T('Loading…')));
    if (!info.Rules.length) listEl.append(h('div.empty', h('p', T('No rules in the config.')), h('p.dim', T('Add a rules: list; docs/guide has the syntax.'))));
    info.Rules.forEach((r, i) => {
      const on = list(r.On);
      listEl.append(h('div.rule' + (i === sel ? '.sel' : ''), { dataset: { i }, onclick: () => { sel = i; paintList(); } },
        h('div.rule-top', h('b', r.Name || T('(unnamed)')), h('span.spacer'), info.Counts[r.Name] ? h('span.chip', T('%d fired', info.Counts[r.Name])) : null),
        h('div.rule-on.dim', T('on %s', on.length ? on.join(', ') : T('any change')) + (r.Watch ? T(' of %s every %s', r.Watch, r.Every || '5m') : T(' (board)'))),
        h('div.rule-match', matchChips(r.Match)),
        h('div.rule-acts', (r.Actions || []).map(a => h('span.chip.act-' + a.Type, a.Type + (a.To ? ' → ' + a.To : '') + (a.Command ? ' ' + a.Command.join(' ') : ''))))));
    });
    for (const w of info.Warnings || []) listEl.append(h('div.rule-warn', T('Skipped: %s', w)));
    const s = listEl.querySelector('.rule.sel'); s && s.scrollIntoView({ block: 'nearest' });
  }

  // ---- test form
  const F = {};
  const field = (name, label, node) => { F[name] = node; return h('label.rt-f', h('span', label), node); };
  const txt = (name, label, ph = '', val = '') => field(name, label, h('input.input', { name, type: 'text', placeholder: ph, value: val, autocomplete: 'off', spellcheck: false }));
  function buildForm() {
    clear(testEl);
    const kinds = field('On', T('Change'), h('select.input', { name: 'On' }, info.Kinds.map(k => h('option', { value: k }, k))));
    const watch = field('Watch', T('Seen by'), h('select.input', { name: 'Watch' }, h('option', { value: '' }, T('a board')), (info.Watches || []).map(w => h('option', { value: w.JQL }, T('watch: %s', w.JQL)))));
    const byMe = field('ByMe', T('Made by you'), h('select.input', { name: 'ByMe' }, h('option', { value: '' }, T('unknown')), h('option', { value: 'true' }, T('yes')), h('option', { value: 'false' }, T('no'))));
    testEl.append(kinds, txt('Key', T('Key'), 'TEST-1'), txt('Summary', T('Summary')), txt('Type', T('Type'), info.Test.Type || 'Task'), txt('Status', T('Status'), info.Test.Status || 'To Do'),
      txt('FromStatus', T('From status')), txt('Assignee', T('Assignee'), T('empty: unassigned')), txt('Priority', T('Priority'), 'Medium'), txt('Points', T('Points')), watch, byMe,
      h('div.rt-go', h('button.btn.primary', { type: 'submit' }, T('Test')), h('span.dim', T('Nothing runs; it shows what would fire.'))));
    let t = 0;
    testEl.addEventListener('input', () => { clearTimeout(t); t = setTimeout(test, 300); });
  }
  async function test() {
    const body = {};
    for (const [k, n] of Object.entries(F)) body[k] = n.value;
    let res;
    try { res = await api.post('/rules/test', body); } catch (e) { return ui.errToast(e); }
    clear(resEl);
    if (!res || !res.length) return resEl.append(h('div.dim', T('No rules.')));
    for (const r of res) {
      resEl.append(h('div.rt-row' + (r.Fires ? '.fires' : ''),
        h('span.rt-mark', icon(r.Fires ? 'check' : 'x')), h('b', r.Rule || T('(unnamed)')),
        r.Fires ? h('span.rt-acts', r.Actions.map(a => h('div.rt-act', h('span.chip', a.Type), ' ', a.Note ? h('span.dim', a.Note) : a.Text)))
          : h('span.dim', r.Why)));
    }
  }

  // ---- log
  const rows = () => {
    const seen = new Set(file.map(f => f.at + f.rule + f.text));
    const all = file.concat(live.filter(e => e.action !== 'log' || !seen.has(e.at + e.rule + e.text)));
    return all.sort((a, b) => (a.at < b.at ? 1 : a.at > b.at ? -1 : 0)).slice(0, 300);
  };
  function paintLog() {
    clear(logEl);
    const r = rows();
    if (!r.length) return logEl.append(h('div.dim', T('Nothing has fired yet.') + (info && info.Log ? T(' Log actions are kept in %s.', info.Log) : '')));
    for (const e of r) logEl.append(h('div.lg' + (e.err ? '.err' : ''), h('span.dim.lg-at', e.at), e.action !== 'log' && h('span.chip', e.action), h('b.lg-rule', e.rule), h('span', e.err || e.text)));
  }
  const toRow = ev => ({ at: stamp(new Date(ev.Time)), rule: ev.Rule || T('(unnamed)'), text: ev.Text, action: ev.Action, err: ev.Err });
  let logTimer = 0;
  async function loadLog() {
    try { file = (await api.get('/rules/log?n=300', { fresh: true })).Lines.map(parseLine); } catch (e) { file = []; }
    if (!dead) paintLog();
  }
  const off = onRuleEvent(ev => {
    live.push(toRow(ev)); if (live.length > 300) live.shift();
    if (ev.Action === 'log') { clearTimeout(logTimer); logTimer = setTimeout(loadLog, 400); }
    if (info) info.Counts[ev.Rule] = (info.Counts[ev.Rule] || 0) + 1;
    paintLog(); paintList();
  });

  async function load() {
    try { info = await api.get('/rules', { fresh: true }); } catch (e) { clear(listEl).append(h('div.empty', e.message)); show(); return; }
    if (dead) return;
    sel = Math.min(sel, Math.max(info.Rules.length - 1, 0));
    paintList(); if (!testEl.firstChild) buildForm(); await test();
    if (dead) return;
    show(); loadLog();
  }

  const move = d => { if (!info || !info.Rules.length) return; sel = (sel + d + info.Rules.length) % info.Rules.length; paintList(); };
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next rule'), { group: T('Rules') });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous rule'), { group: T('Rules') });
  scope.bind('t', () => { const f = F.Key; f && f.focus(); }, T('try a change (form)'), { group: T('Rules'), bar: T('try') });
  scope.bind('R', () => load(), T('reload'), { group: T('Rules') });
  scope.bind('N', async () => { paintNotify(); await setEnabled(!enabled()); paintNotify(); }, T('toggle browser notifications'), { group: T('Rules'), bar: T('notifications'), writes: true });
  scope.bind('Escape', () => { if (document.activeElement && root.contains(document.activeElement)) document.activeElement.blur(); }, '', { input: true, hidden: true });
  load();
  return () => { dead = true; off(); clearTimeout(logTimer); };
}
