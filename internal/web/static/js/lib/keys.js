// Keyboard manager. Scopes stack by layer (global, view, panel, modal); the highest layer, then
// the newest scope's binding wins, and a `modal` scope hides all below it. `covers` (a function) hides the view
// layers below a scope while it returns true: the issue panel with the focus owns its keys, as the TUI's.
//
//   const k = keys.scope('board');            // k.dispose() when the view goes
//   k.bind('j', fn, 'next card');             // single key, typed character ("J" = shift+j, "?" as is)
//   k.bind('g b', fn, 'go to board');         // chord: keys in sequence
//   k.bind('mod+Enter', fn, 'save', {input: true});    // also fires inside inputs
//   k.bind('H', fn, 'move left', {writes: true});      // a change typing that missed its field must not make (see TYPE_RUN)
//   k.bind(['j','ArrowDown'], fn, 'down');    // aliases
//   k.bind('Enter', fn, 'open issue', {bar: 'open'});   // also in the key bar at the bottom (lib/keybar.js), as "⏎ open";
//                                                       // aliases show the first key, binds sharing a label one hint ("H L move")
//
// Named keys: Enter Escape Tab ArrowUp ArrowDown ArrowLeft ArrowRight Backspace Delete Home End PageUp PageDown Space.
// Modifiers: ctrl+ alt+ meta+ (shift is implied by the character); mod+ is meta+ (⌘) on a mac, ctrl+ elsewhere.
// In inputs/textareas only `input: true` bindings fire (and Escape always reaches modal scopes).
import { actionFor, specFromTUI, plat } from './keymap.js';
export { MAC, isMod, modText } from './keymap.js';
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
// Typing that missed its field: TYPE_RUN different plain keys, each within TYPE_GAP of the last. Nobody presses
// distinct shortcuts that fast; typists do. The burst takes back what its first keys did (the state from before
// them: modals they opened, keep() hooks, the focus), pauses plain keys until TYPE_QUIET passes without one (or
// Escape), and offers what was typed to the field last used. A bind with {writes: true} (a change no hook takes
// back) waits TYPE_GAP on its plain key, and a different plain key in that time drops it.
const TYPE_RUN = 3, TYPE_GAP = 200, TYPE_QUIET = 1000;
const LATE_MS = 1500; // a modal a burst's key opens later (it awaited something) closes too, until this after its last key
let run = [], lastPlain = 0, paused = false, lateUntil = 0, before = null, burst = null, held = null, lastField = null;
const typingListeners = new Set(), keepers = new Set();
// Single-key shortcuts (WCAG 2.1.4): off leaves plain characters to the browser; named keys and mod+ stay.
let single = true;
const isPlain = k => k.length === 1 || k === 'Space';

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
      const b = { fn, desc, input: !!opts.input, writes: !!opts.writes, hidden: !!opts.hidden, help: !!opts.help, bar: (sp === [].concat(spec)[0] && opts.bar) || '', group: opts.group || name, def: plat(sp), id: name + ':' + sp, action: actionFor(name, sp), when: opts.when, scope: name };
      resolve(b); s.binds.push(b); remember(b);
    }
    notify();
    return s;
  };
  s.dispose = () => { const i = scopes.indexOf(s); if (i >= 0) scopes.splice(i, 1); s.disposed = true; notify(); };
  scopes.push(s); notify();
  if (modal && Date.now() < lateUntil) setTimeout(() => takeBack({ seq: s.seq - 1, kept: [] }));
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
  if (!field) {
    if (!single && !pending.length && k.length === 1) return;
    settle(k);
    if (typing(e, k)) { e.preventDefault(); e.stopPropagation(); return; }
  }
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
    if (exact.writes && !field && buf.length === 1 && isPlain(k)) { const fn = exact.fn; held = { k, fn: () => fn(e), t: setTimeout(() => settle(''), TYPE_GAP) }; return; }
    exact.fn(e); return;
  }
  if (prefix) { e.preventDefault(); pending = buf; clearTimeout(timer); timer = setTimeout(clearPending, CHORD_MS); notify(); return; }
  if (pending.length) { clearPending(); if (!field) e.preventDefault(); }
}
// Whether e is a key typed into nothing, to swallow: true while paused, and on the key that shows a burst.
function typing(e, k) {
  if (!isPlain(k)) { if (k === 'Escape') paused = false, lateUntil = 0; run = []; return false; }
  if (e.repeat) return paused;
  const now = e.timeStamp || Date.now(), gap = now - lastPlain, ch = k === 'Space' ? ' ' : k;
  lastPlain = now;
  if (paused && gap < TYPE_QUIET) { burst.text += ch; lateUntil = Date.now() + LATE_MS; return true; }
  paused = false;
  if (run.length && gap < TYPE_GAP) run.push(k); else { run = [k]; before = save(); burst = { text: '' }; }
  burst.text += ch;
  if (run.length < TYPE_RUN || new Set(run).size < TYPE_RUN) return false;
  paused = true; run = []; clearPending(); takeBack(before); lateUntil = Date.now() + LATE_MS;
  const b = burst, f = typable(lastField) ? lastField : null;
  for (const fn of typingListeners) fn({ field: f && (f.lwName || f.getAttribute('aria-label') || f.placeholder || f.title || f.name || T('the field')), put: f && (() => put(f, b.text)) });
  return true;
}
// A held write runs when its time is up or another key than a different plain one comes; that one drops it.
function settle(k) {
  if (!held) return;
  const h0 = held; held = null; clearTimeout(h0.t);
  if (!isPlain(k) || k === h0.k) h0.fn();
}
const save = () => ({ seq, focus: document.activeElement, kept: [...keepers].map(k => [k, k.save()]) });
function takeBack(s) {
  for (const sc of byRank()) {
    if (!sc.modal || sc.seq <= s.seq) continue;
    const esc = sc.binds.find(b => b.def === 'Escape');
    if (esc) esc.fn(new KeyboardEvent('keydown', { key: 'Escape' }));
  }
  for (const [k, v] of s.kept) k.restore(v);
  if (s.focus && s.focus.isConnected && s.focus.focus) s.focus.focus();
}
const typable = f => !!f && f.isConnected && f.getClientRects().length > 0 && (f.lwType || f.isContentEditable || f.tagName === 'TEXTAREA' || (f.tagName === 'INPUT' && typeof f.selectionStart === 'number'));
// put types text into f as if it had the focus: the browser's insertText keeps the field's undo.
function put(f, text) {
  f.focus();
  if (f.lwType) return f.lwType(text);
  if (document.execCommand('insertText', false, text)) return;
  f.setRangeText(text, f.selectionStart, f.selectionEnd, 'end');
  f.dispatchEvent(new Event('input', { bubbles: true }));
}
function clearPending() { pending = []; clearTimeout(timer); notify(); }
function notify() { for (const fn of listeners) fn(); }

document.addEventListener('keydown', onKey, true);
document.addEventListener('focusin', e => { if (inField(e.target)) lastField = e.target; }, true);

export const keys = {
  scope,
  pending: () => pending.join(' '),
  onChange: fn => { listeners.add(fn); return () => listeners.delete(fn); },
  // fn({field, put}) when a burst of typing outside any field pauses the plain keys: field names the field last
  // used, put() types the burst into it; both null without one.
  onTyping: fn => { typingListeners.add(fn); return () => typingListeners.delete(fn); },
  // keep({save, restore}): state a burst of typing takes back; save() before its first key, restore(saved) after.
  keep: k => { keepers.add(k); return () => keepers.delete(k); },
  // Remapping. configure({web, conf}) re-resolves every bind; web = ui.web_keys, conf = ui.keys of the config.
  // {single: false} turns the single-character keys off.
  configure(o) {
    if (o.single != null) single = o.single;
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
      if (b.hidden || !b.desc || !b.spec || (!single && b.spec.split(' ')[0].length === 1)) continue;
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
export const kbd = spec => plat(spec).split(' ').map(k => k.replace('ArrowUp', '↑').replace('ArrowDown', '↓').replace('ArrowLeft', '←').replace('ArrowRight', '→').replace('Escape', 'esc').replace('Enter', '⏎').replace('Space', '␣').replace('ctrl+', '⌃').replace('alt+', '⌥').replace('meta+', '⌘'));
// keyHint is a button's shortcut, drawn in the button: h('button.btn', T('Save'), keyHint('mod+Enter')).
// Every button with a key shows it; Appearance › Keys on buttons hides them all (html[data-btn-keys=hide]).
export const keyHint = spec => h('kbd.btn-key', { 'aria-hidden': 'true' }, kbd(spec).join(' '));
export default keys;
