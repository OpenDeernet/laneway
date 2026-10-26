// An issue key input that searches as you type (/find): a key, words of the summary, or a bare number in
// o.project. With o.parentOf (an issue key), or o.project and o.type (a new issue's), it offers only the
// issues that can be its parent, the open ones as soon as it has the focus. ↑↓ pick, enter or tab takes
// one, esc closes the list. The issue the key names shows under it.
import { h, clear, debounce } from './dom.js';
import api from './api.js';
import { scope } from './keys.js';
import { statusPill } from './badge.js';
import { typeBadge } from './card.js';
import { T } from './i18n.js';

const KEY_RE = /^[A-Z][A-Z0-9_]*-\d+$/;
const catOf = c => (c.Done ? 'done' : c.InProgress ? 'indeterminate' : 'new');
const row = c => [typeBadge(c), h('b.iin-key', c.Key), h('span.iin-text', c.Summary), c.Status && statusPill(c.Status, catOf(c))];

// issueInput(o) → {el, input, get() → the key typed, '' for none}: o.value, o.placeholder, o.label,
// o.parentOf, o.project, o.type, o.oninput(key).
export function issueInput(o = {}) {
  const input = h('input.input.mono.iin-in', { type: 'text', value: o.value || '', placeholder: o.placeholder || T('Key or words of its summary'),
    spellcheck: false, autocomplete: 'off', autocapitalize: 'off', 'aria-label': o.label || T('Issue'), role: 'combobox', 'aria-autocomplete': 'list', 'aria-expanded': 'false' });
  const pop = h('div.mention-pop.iin-pop', { role: 'listbox', hidden: true });
  const chosen = h('div.iin-sum');
  const scoped = !!(o.parentOf || o.type);
  const known = new Map(); // key → its card, from the lists shown
  let items = [], sel = 0, seq = 0, keys = null;
  const get = () => input.value.trim().toUpperCase();

  // The issue the key names, under the input: its type, summary and status, or why it can't be.
  const show = debounce(() => {
    const k = get();
    if (!k) return clear(chosen);
    if (!KEY_RE.test(k)) return clear(chosen).append(h('span.faint', T('Not an issue key')));
    const c = known.get(k);
    if (c) return clear(chosen).append(...row(c));
    find(k).then(cs => {
      if (k !== get()) return;
      const hit = cs.find(x => x.Key === k);
      clear(chosen).append(...(hit ? row(hit) : [h('span.faint', scoped ? T('%s can’t be the parent, or doesn’t exist', k) : T('%s not found', k))]));
    }, () => {});
  }, 250);

  const find = q => {
    const p = new URLSearchParams({ q, n: '15' });
    if (o.parentOf) p.set('parentOf', o.parentOf);
    else { if (o.project) p.set('project', o.project); if (o.type) p.set('type', o.type); }
    return api.get('/find?' + p).then(d => {
      const cs = (d.cards || []).filter(c => c.Key !== o.parentOf);
      for (const c of cs) known.set(c.Key, c);
      return cs;
    });
  };

  const close = () => {
    seq++; pop.hidden = true; items = []; input.setAttribute('aria-expanded', 'false');
    removeEventListener('scroll', place, true); removeEventListener('resize', place);
  };
  const paint = () => {
    if (document.activeElement !== input) return close();
    const q = input.value.trim();
    clear(pop).append(...(items.length ? items.map((c, i) => h('div.mp.iin-row' + (i === sel ? '.sel' : ''), { role: 'option', 'aria-selected': String(i === sel),
      onmousedown: e => { e.preventDefault(); accept(i); } }, ...row(c)))
      : [h('div.iin-none.faint', q ? T('No issue matches “%s”', q) : T('Nothing open to choose'))]));
    const was = pop.hidden;
    pop.hidden = false; input.setAttribute('aria-expanded', 'true');
    place();
    if (was) { addEventListener('scroll', place, true); addEventListener('resize', place); }
    const s = pop.querySelector('.sel'); if (s) s.scrollIntoView({ block: 'nearest' });
  };
  // Fixed to the viewport, so the dialog's own scrolling box doesn't cut it off: under the input, or over it
  // when there is more room there.
  const GAP = 2, EDGE = 8;
  function place() {
    if (pop.hidden) return;
    const r = input.getBoundingClientRect(), below = innerHeight - r.bottom - GAP - EDGE, above = r.top - GAP - EDGE;
    const up = below < Math.min(pop.scrollHeight, 240) && above > below;
    Object.assign(pop.style, { left: r.left + 'px', width: r.width + 'px', maxHeight: Math.max(up ? above : below, 80) + 'px',
      top: up ? 'auto' : r.bottom + GAP + 'px', bottom: up ? innerHeight - r.top + GAP + 'px' : 'auto' });
  }
  const search = debounce(n => {
    const q = input.value.trim();
    if (!q && !scoped) return close();
    find(q).then(cs => { if (n === seq) { items = cs; sel = 0; paint(); } }, () => { if (n === seq) close(); });
  }, 200);
  const suggest = () => { const n = ++seq; if (!input.value.trim() && !scoped) return close(); search(n); };

  function accept(i) {
    const c = items[i]; if (!c) return;
    input.value = c.Key;
    close();
    clear(chosen).append(...row(c));
    if (o.oninput) o.oninput(c.Key);
  }

  input.addEventListener('input', () => { suggest(); show(); if (o.oninput) o.oninput(get()); });
  input.addEventListener('focus', () => {
    // Over the dialog the input is in: while the list is open, its keys are the list's.
    keys = scope('issueinput', { layer: 3 });
    const open = () => !pop.hidden;
    const step = d => () => { if (items.length) { sel = (sel + d + items.length) % items.length; paint(); } };
    keys.bind('ArrowDown', step(1), '', { input: true, hidden: true, when: open });
    keys.bind('ArrowUp', step(-1), '', { input: true, hidden: true, when: open });
    keys.bind(['Enter', 'Tab'], () => accept(sel), '', { input: true, hidden: true, when: () => open() && items.length > 0 });
    keys.bind('Escape', close, '', { input: true, hidden: true, when: open });
    keys.bind('ArrowDown', suggest, '', { input: true, hidden: true, when: () => !open() });
    if (scoped && !input.value.trim()) suggest();
  });
  input.addEventListener('blur', () => { close(); if (keys) { keys.dispose(); keys = null; } });
  if (get()) show();

  return { el: h('div.iin', h('div.iin-box', input, pop), chosen), input, get };
}
