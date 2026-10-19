// Keyboard manager. Scopes stack by layer (global, view, panel, modal); the highest layer, then
// the newest scope's binding wins, and a `modal` scope hides all below it. `covers` (a function) hides the view
// layers below a scope while it returns true: the issue panel with the focus owns its keys, as the TUI's.
//
//   const k = keys.scope('board');            // k.dispose() when the view goes
//   k.bind('j', fn, 'next card');             // single key, typed character ("J" = shift+j, "?" as is)
//   k.bind('g b', fn, 'go to board');         // chord: keys in sequence
//   k.bind('ctrl+Enter', fn, 'save', {input: true});   // also fires inside inputs
//   k.bind(['j','ArrowDown'], fn, 'down');    // aliases
//   k.bind('Enter', fn, 'open issue', {bar: 'open'});   // also in the key bar at the bottom (lib/keybar.js), as "⏎ open";
//                                                       // aliases show the first key, binds sharing a label one hint ("H L move")
//
// Named keys: Enter Escape Tab ArrowUp ArrowDown ArrowLeft ArrowRight Backspace Delete Home End PageUp PageDown Space.
// Modifiers: ctrl+ alt+ meta+ (shift is implied by the character).
// In inputs/textareas only `input: true` bindings fire (and Escape always reaches modal scopes).
import { actionFor, specFromTUI } from './keymap.js';
import { T } from './i18n.js';
import { h } from './dom.js';

const scopes = [];
// A bind's group as shown: the id stays what the code compares.
const groupName = g => ({ Global: T('Global'), Go: T('Go'), Issue: T('Issue'), Agents: T('Agents'), Roadmap: T('Roadmap'), Reports: T('Reports'), Settings: T('Settings'), Rules: T('Rules'), Planning: T('Planning'), Time: T('Time'), Standup: T('Standup'), Refine: T('Refine'), 'Card designer': T('Card designer'), Board: T('Board'), Actions: T('Actions'), Terminal: T('Terminal'), Pinned: T('Pinned'), 'My work': T('My work'), 'Lane layouts': T('Lane layouts'), JQL: T('JQL'), Inbox: T('Inbox'), Filters: T('Filters'), Editor: T('Editor'), Edit: T('Edit'), App: T('App'), Theme: T('Theme'), Branch: T('Branch') }[g] || g);
let pending = [], timer = 0;
// Remaps from the config file: `web` (ui.web_keys, bind id -> keys) beats `conf` (ui.keys, action -> keys).
let web = {}, conf = {};
const registry = new Map(); // id -> every bind seen, even of views not open now (kept in localStorage for settings)
try { for (const r of JSON.parse(localStorage.getItem('lw:keyreg') || '[]')) registry.set(r.id, r); } catch (e) { /* ignore */ }
let regTimer = 0;
const listeners = new Set();
const CHORD_MS = 2500; // long enough to read the chord hint (lib/chrome.js)

function norm(e) {
  let k = e.key;
  if (k === ' ') k = 'Space';
  if (k.length === 1 && e.shiftKey && /[a-z]/i.test(k)) k = k.toUpperCase();
  if (k === 'Tab' && e.shiftKey) k = 'shift+Tab';
  const mods = (e.ctrlKey ? 'ctrl+' : '') + (e.altKey ? 'alt+' : '') + (e.metaKey ? 'meta+' : '');
  if (['Shift', 'Control', 'Alt', 'Meta'].includes(k)) return null;
  return mods + k;
}
export const keyOf = norm;

// A remap's specs: [] for "none" (unbound), null when it has no key the browser reads.
export const NO_KEY = 'none';
function remap(l) {
  l = [].concat(l || []);
  if (l.length === 1 && l[0] === NO_KEY) return [];
  const r = l.map(specFromTUI).filter(Boolean);
  return r.length ? r : null;
}
// The specs a bind answers to: ui.web_keys for it, else ui.keys for its action, else its own.
const effective = (id, def, action) => remap(web[id]) || (action && remap(conf[action])) || [def];
const seqs = specs => specs.map(sp => sp.split(' '));
function resolve(b) { b.specs = effective(b.id, b.def, b.action); b.spec = b.specs[0] || ''; b.seqs = seqs(b.specs); }
function remember(b) {
  if (b.hidden || !b.desc || b.scope === 'modal') return;
  const r = registry.get(b.id), row = { id: b.id, scope: b.scope, group: b.group, def: b.def, desc: b.desc, action: b.action };
  if (r && r.desc === row.desc && r.group === row.group && r.action === row.action) return;
  registry.set(b.id, row);
  clearTimeout(regTimer);
  regTimer = setTimeout(() => { try { localStorage.setItem('lw:keyreg', JSON.stringify([...registry.values()])); } catch (e) { /* ignore */ } }, 500);
}
const inField = t => t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable);

// Layers decide who wins, not age: global 0 < view 1 < panel 2 < modal 3; within a layer the newer scope wins.
const LAYER = { global: 0, chrome: 0, timer: 0, undo: 0, sites: 0, ask: 0, 'agents-global': 0, actions: 0, issue: 2, 'issue-notes': 2, refine: 2, mdedit: 3 };
let seq = 0;
export function scope(name, { modal = false, layer, covers } = {}) {
  if (layer == null) layer = modal ? 3 : name in LAYER ? LAYER[name] : 1;
  const s = { name, modal, layer, covers, seq: ++seq, binds: [], disposed: false };
  s.bind = (spec, fn, desc = '', opts = {}) => {
    for (const sp of [].concat(spec)) {
      const b = { fn, desc, input: !!opts.input, hidden: !!opts.hidden, help: !!opts.help, bar: (sp === [].concat(spec)[0] && opts.bar) || '', group: opts.group || name, def: sp, id: name + ':' + sp, action: actionFor(name, sp), when: opts.when, scope: name };
      resolve(b); s.binds.push(b); remember(b);
    }
    notify();
    return s;
  };
  s.dispose = () => { const i = scopes.indexOf(s); if (i >= 0) scopes.splice(i, 1); s.disposed = true; notify(); };
  scopes.push(s); notify();
  return s;
}

const byRank = () => [...scopes].sort((a, b) => b.layer - a.layer || b.seq - a.seq);
// A covered scope: a view layer under a scope whose covers() holds. Global ones (layer 0) stay.
function covered(sorted) {
  const c = sorted.find(s => s.covers && s.covers());
  return s => !!c && s.layer > 0 && s.layer < c.layer;
}
function visible() {
  const out = [], sorted = byRank(), under = covered(sorted);
  for (const s of sorted) { if (under(s)) continue; out.push(s); if (s.modal) break; }
  return out;
}
const match = (seq, buf) => buf.length <= seq.length && buf.every((k, i) => seq[i] === k);
// How a bind's sequences match buf: 2 whole, 1 a prefix, 0 not.
function matches(b, buf) {
  let r = 0;
  for (const seq of b.seqs) if (match(seq, buf)) { if (seq.length === buf.length) return 2; r = 1; }
  return r;
}

function onKey(e) {
  if (e.isComposing) return;
  const k = norm(e); if (!k) return;
  // Tab walks the bars like normal; only the page (view, panel) gives it a meaning.
  if (k === 'Tab' && e.target.closest && e.target.closest('#top, #viewbar')) return;
  const field = inField(e.target);
  const buf = pending.concat(k);
  const vis = visible();
  let exact = null, prefix = false;
  for (const s of vis) {
    for (const b of s.binds) {
      if (field && !b.input) continue;
      if (b.when && !b.when()) continue;
      const m = matches(b, buf);
      if (m === 2) { if (!exact) exact = b; } else if (m === 1) prefix = true;
    }
    if (exact) break;
  }
  if (exact) {
    clearPending(); e.preventDefault(); e.stopPropagation();
    exact.fn(e); return;
  }
  if (prefix) { e.preventDefault(); pending = buf; clearTimeout(timer); timer = setTimeout(clearPending, CHORD_MS); notify(); return; }
  if (pending.length) { clearPending(); if (!field) e.preventDefault(); }
}
function clearPending() { pending = []; clearTimeout(timer); notify(); }
function notify() { for (const fn of listeners) fn(); }

document.addEventListener('keydown', onKey, true);

export const keys = {
  scope,
  pending: () => pending.join(' '),
  onChange: fn => { listeners.add(fn); return () => listeners.delete(fn); },
  // Remapping. configure({web, conf}) re-resolves every bind; web = ui.web_keys, conf = ui.keys of the config.
  configure(o) {
    if (o.web) web = o.web;
    if (o.conf) conf = o.conf;
    for (const s of scopes) for (const b of s.binds) resolve(b);
    notify();
  },
  // Every bind seen in this browser: [{id, scope, group, desc, def, specs, changed, from}]; from names the config
  // option that remaps it: 'ui.web_keys', 'ui.keys' or ''.
  registry() {
    return [...registry.values()].map(r0 => {
      const r = { ...r0, group: groupName(r0.group) };
      const specs = effective(r.id, r.def, r.action);
      const from = remap(web[r.id]) ? 'ui.web_keys' : r.action && remap(conf[r.action]) ? 'ui.keys' : '';
      return { ...r, specs, changed: specs.length !== 1 || specs[0] !== r.def, from };
    });
  },
  // The bind already answering to spec in the scope of `id` (or a global one), or null. An editor's keys
  // and the rest never meet: the editor's win while it has the focus, and only then.
  conflict(id, spec) {
    const me = registry.get(id); if (!me) return null;
    for (const r of keys.registry()) {
      if (r.id === id || FIELD.has(r.scope) !== FIELD.has(me.scope)) continue;
      const shared = r.scope === me.scope || GLOBAL.has(r.scope) || GLOBAL.has(me.scope);
      if (shared && r.specs.some(x => x === spec || (x.startsWith(spec + ' ')) || spec.startsWith(x + ' '))) return r;
    }
    return null;
  },
  // Bindings a user can press now, newest scope first: [{group, spec, desc, bar, rank, run}].
  // rank 0 view, 1 a panel waiting for focus, 2 global. `help: true` binds are listed while their `when` is false.
  active() {
    const out = [], seen = new Set();
    for (const s of visible()) for (const b of s.binds) {
      if (b.hidden || !b.desc || !b.spec) continue;
      const on = !b.when || b.when();
      if (!on && !b.help) continue;
      const id = b.spec + (on ? '' : '~'); if (seen.has(id)) continue; seen.add(id);
      out.push({ gid: b.group, group: on ? groupName(b.group) : T('%s (Tab to focus)', groupName(b.group)), spec: b.spec, specs: b.specs, desc: b.desc, bar: b.bar, rank: !on ? 1 : GLOBAL.has(s.name) ? 2 : 0, run: () => press(b) });
    }
    return out;
  },
  // The open screen's own bindings (view and panel, under any modal), for the palette's rows: [{id, group, spec, desc, run}].
  // Moving the cursor is left out, as the TUI's palette does; an unbound one stays, its spec ''.
  screen() {
    const out = [], seen = new Set(), sorted = byRank(), under = covered(sorted);
    for (const s of sorted) {
      if (s.modal || s.layer < 1 || s.layer > 2 || under(s)) continue;
      for (const b of s.binds) {
        const k = b.spec || b.id;
        if (b.hidden || !b.desc || (b.when && !b.when()) || MOVES.test(b.spec || b.def) || seen.has(k)) continue;
        seen.add(k);
        out.push({ id: b.id, group: groupName(b.group), spec: b.spec, desc: b.desc, run: () => press(b) });
      }
    }
    return out;
  },
};
// Runs a bind as its key would, for a click on a palette row or a key bar hint.
const press = b => b.fn(new KeyboardEvent('keydown', { key: (b.spec || b.def).split(' ').pop().replace(/^(ctrl|alt|meta)\+/, '') }));
const MOVES = /^(j|k|h|l|ArrowUp|ArrowDown|ArrowLeft|ArrowRight|Home|End|PageUp|PageDown|Escape|Tab|shift\+Tab|g g|G)$/;
const GLOBAL = new Set(['global', 'timer', 'undo']);
// Scopes that exist only while a text field has the focus.
const FIELD = new Set(['mdedit']);
export const kbd = spec => spec.split(' ').map(k => k.replace('ArrowUp', '↑').replace('ArrowDown', '↓').replace('ArrowLeft', '←').replace('ArrowRight', '→').replace('Escape', 'esc').replace('Enter', '⏎').replace('Space', '␣').replace('ctrl+', '⌃').replace('alt+', '⌥').replace('meta+', '⌘'));
// keyHint is a button's shortcut, drawn in the button: h('button.btn', T('Save'), keyHint('ctrl+Enter')).
// Every button with a key shows it; Appearance › Keys on buttons hides them all (html[data-btn-keys=hide]).
export const keyHint = spec => h('kbd.btn-key', { 'aria-hidden': 'true' }, kbd(spec).join(' '));
export default keys;
