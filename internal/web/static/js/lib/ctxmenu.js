// Context menu at the pointer (a right-click), the TUI's card menu: it flips at the screen's edges,
// ↑↓ move, enter or → opens a submenu or runs, ← or esc closes a submenu, esc or a click outside closes it.
//
//   ctxMenu(items, x, y[, {head}])  items: [{label, hint, current, run(), sub: () => items | Promise<items>}, '-' …]; head: an element above them
//
// A submenu's items load when it opens; `current` marks the value the issue has.
import { h } from './dom.js';
import { icon } from './icons.js';
import { T } from './i18n.js';

let shown = null;

export function ctxMenu(items, x, y, { head = null } = {}) {
  if (shown) shown.close();
  const levels = [], back = document.activeElement;
  let dead = false;

  function close() {
    if (dead) return;
    dead = true; shown = null;
    for (const l of levels) l.el.remove();
    document.removeEventListener('keydown', onKey, true);
    document.removeEventListener('pointerdown', onDown, true);
    window.removeEventListener('resize', close);
    window.removeEventListener('blur', close);
    document.removeEventListener('scroll', onScroll, true);
    if (back && back.focus && document.contains(back)) back.focus({ preventScroll: true });
  }
  const pick = it => it && it !== '-' && !it.disabled;

  // open shows items as level d, at x, y; a submenu flips to the left of its parent when it would leave the screen.
  function open(list, d, px, py, parent, head = null) {
    while (levels.length > d) levels.pop().el.remove();
    const lv = { items: list, sel: -1, head, el: h('div.ctx', { role: 'menu', tabindex: -1 }) };
    levels.push(lv);
    paint(lv);
    document.body.append(lv.el);
    place(lv.el, px, py, parent);
    return lv;
  }
  function paint(lv) {
    lv.el.replaceChildren(...(lv.head ? [lv.head] : []), ...lv.items.map((it, i) => (it === '-' ? h('div.ctx-sep', { role: 'separator' })
      : h('div.ctx-item' + (i === lv.sel ? '.sel' : '') + (it.disabled ? '.off' : ''), { role: 'menuitem', 'aria-haspopup': it.sub ? 'menu' : null, dataset: { i },
        onpointerenter: () => hover(lv, i), onclick: e => { e.stopPropagation(); activate(lv, i, false); } },
      h('span.ctx-check', it.current ? icon('check') : ''), h('span.ctx-label', it.label), it.hint ? h('span.ctx-hint', it.hint) : null, it.sub ? h('span.ctx-more', icon('chevron-right')) : null))));
  }
  function place(el, px, py, parent) {
    const r = el.getBoundingClientRect(), W = window.innerWidth, H = window.innerHeight, m = 4;
    let left = px, top = py;
    if (left + r.width > W - m) left = parent ? parent.left - r.width : W - r.width - m;
    if (top + r.height > H - m) top = Math.max(m, H - r.height - m);
    el.style.left = Math.max(m, left) + 'px'; el.style.top = Math.max(m, top) + 'px';
  }
  function setSel(lv, i) {
    lv.sel = i;
    lv.el.querySelectorAll('.ctx-item').forEach(n => n.classList.toggle('sel', Number(n.dataset.i) === i));
    const n = lv.el.querySelector(`.ctx-item[data-i="${i}"]`);
    if (n) n.scrollIntoView({ block: 'nearest' });
  }
  let hoverT = 0;
  function hover(lv, i) {
    const d = levels.indexOf(lv);
    setSel(lv, i);
    clearTimeout(hoverT);
    if (!pick(lv.items[i])) return;
    hoverT = setTimeout(() => { if (!dead) { if (lv.items[i].sub) activate(lv, i, false); else while (levels.length > d + 1) levels.pop().el.remove(); } }, 120);
  }
  async function activate(lv, i, keys) {
    const it = lv.items[i];
    if (!pick(it)) return;
    if (!it.sub) { close(); if (it.run) it.run(); return; }
    const d = levels.indexOf(lv), row = lv.el.querySelector(`.ctx-item[data-i="${i}"]`).getBoundingClientRect();
    if (levels[d + 1] && levels[d + 1].from === i) { if (keys) first(levels[d + 1]); return; }
    const sub = open([{ label: T('Loading…'), disabled: true }], d + 1, row.right - 2, row.top - 4, row);
    sub.from = i;
    let list;
    try { list = await it.sub(); } catch (e) { list = [{ label: e.message || T('Could not load'), disabled: true }]; }
    if (dead || levels[d + 1] !== sub) return;
    sub.items = list && list.length ? list : [{ label: T('Nothing here'), disabled: true }];
    paint(sub);
    place(sub.el, row.right - 2, row.top - 4, row);
    if (keys) first(sub);
  }
  function first(lv) {
    const cur = lv.items.findIndex(it => pick(it) && it.current);
    setSel(lv, cur >= 0 ? cur : lv.items.findIndex(pick));
  }
  function step(lv, d) {
    const n = lv.items.length; if (!n) return;
    let i = lv.sel;
    for (let k = 0; k < n; k++) { i = (i + d + n) % n; if (pick(lv.items[i])) return setSel(lv, i); }
  }
  function onKey(e) {
    const lv = levels[levels.length - 1]; if (!lv) return;
    const k = e.key;
    if (k === 'ArrowDown' || (k === 'Tab' && !e.shiftKey)) step(lv, 1);
    else if (k === 'ArrowUp' || (k === 'Tab' && e.shiftKey)) step(lv, -1);
    else if (k === 'Home') { setSel(lv, -1); step(lv, 1); }
    else if (k === 'End') { setSel(lv, 0); step(lv, -1); }
    else if (k === 'Enter' || k === ' ' || (k === 'ArrowRight' && lv.items[lv.sel] && lv.items[lv.sel].sub)) { if (lv.sel >= 0) activate(lv, lv.sel, true); }
    else if (k === 'ArrowLeft' && levels.length > 1) levels.pop().el.remove();
    else if (k === 'Escape') { if (levels.length > 1) levels.pop().el.remove(); else close(); }
    else if (k.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
      // a letter goes to the next item starting with it
      const n = lv.items.length, l = k.toLowerCase();
      for (let j = 1; j <= n; j++) { const i = (lv.sel + j) % n, it = lv.items[i]; if (pick(it) && it.label.toLowerCase().startsWith(l)) { setSel(lv, i); break; } }
    } else return;
    e.preventDefault(); e.stopPropagation();
  }
  function onDown(e) { if (!levels.some(l => l.el.contains(e.target))) close(); }
  function onScroll(e) { if (!levels.some(l => l.el.contains(e.target))) close(); }

  open(items, 0, x, y, null, head);
  document.addEventListener('keydown', onKey, true);
  document.addEventListener('pointerdown', onDown, true);
  window.addEventListener('resize', close);
  window.addEventListener('blur', close);
  document.addEventListener('scroll', onScroll, true);
  levels[0].el.focus({ preventScroll: true });
  shown = { close };
  return shown;
}
