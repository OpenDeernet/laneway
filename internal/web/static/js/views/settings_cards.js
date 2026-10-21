// The card designer on the settings page: ui.card_layout as a card whose fields are dragged into place, and
// ui.card_styles as a list of conditions with what they do; both with a preview of sample cards drawn as the board
// draws them (lib/card.js). Each change writes the config file at once (settings_config.js saver).
// Keys: enter on the row starts editing. Layout: h/l pick a field, H/L move it, J/K a line down or up, x
// takes it off the card or puts it back; styles: tab walks the controls. esc leaves.
import { h, clear, debounce } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { compile } from '../lib/cardquery.js';
import { buildCard, fillCard } from '../lib/card.js';
import { FIELDS, SLOTS, COLORS, fieldLabel, layoutOf, layoutIsSet, normStyle, lookOf, layoutValue, styleValue } from '../lib/cardstyle.js';
import { saver } from './settings_config.js';
import { T } from '../lib/i18n.js';

css('board');

const ZONES = [...SLOTS, 'tray'];
const ZONE_NAME = { top: T('Top'), top_right: T('Top right'), bottom: T('Bottom'), bottom_right: T('Bottom right'), tray: T('Not on cards') };
const DRAG_TYPE = 'text/x-laneway-field';

// Sample cards for the preview: something for most conditions to match.
function samples(custom) {
  const day = 864e5, iso = d => new Date(Date.now() + d * day).toISOString();
  const extra = vals => custom.map((n, i) => n + '=' + vals[i % vals.length]).join('\x1f');
  return [
    { Key: 'SHOP-142', Summary: 'Checkout fails when the voucher covers the whole order', Type: 'Bug', Status: 'In Progress', InProgress: true, Priority: 'Highest', Points: '5', Assignee: 'Ada Lovelace', Labels: 'urgent payments', ParentKey: 'SHOP-90', ParentSummary: 'Checkout v2', Due: iso(2), Since: iso(-6), Created: iso(-9), Updated: iso(-0.2), Flagged: true, Extra: extra(['Core', 'e2e']) },
    { Key: 'SHOP-151', Summary: 'Show delivery estimates on the product page', Type: 'Story', Status: 'To Do', Priority: 'Medium', Points: '3', Assignee: '', Labels: 'ui', ParentKey: 'SHOP-90', ParentSummary: 'Checkout v2', Created: iso(-3), Updated: iso(-1), Extra: extra(['Web']) },
    { Key: 'SHOP-138', Summary: 'Review: refund webhook retries', Type: 'Task', Status: 'In Review', InProgress: true, Priority: 'High', Points: '2', Assignee: 'Linus Torvalds', PR: 'OPEN', Deploy: 'staging', Subtasks: 4, SubtasksDone: 2, Since: iso(-1), Created: iso(-12), Updated: iso(-0.1), Extra: extra(['Core']) },
    { Key: 'SHOP-120', Summary: 'Drop the legacy cart cookie', Type: 'Task', Status: 'Done', Done: true, Priority: 'Low', Points: '1', Assignee: 'Grace Hopper', PR: 'MERGED', Labels: 'cleanup', Created: iso(-20), Updated: iso(-2), Extra: extra(['Platform']) },
  ];
}

// design gives the settings rows of ui.card_layout and ui.card_styles their editors.
export function designCards(app, host, options) {
  const L = options.find(o => o.name === 'card_layout'), S = options.find(o => o.name === 'card_styles');
  if (!L || !S) return;
  const ui = () => (app.session && app.session.ui) || {};
  const custom = () => ui().CustomFields || [];
  const state = {
    layout: layoutOf(ui()),
    styles: (ui().CardStyles || []).map(s => normStyle(s, custom())),
  };
  const previews = new Set(); // the preview nodes on the page, redrawn on each change
  const drawPreviews = () => { for (const p of previews) if (p.isConnected) paintPreview(p); else previews.delete(p); };
  function paintPreview(box) {
    const look = lookOf(state.styles, { me: app.session && app.session.me && app.session.me.AccountID }, custom());
    clear(box);
    for (const c of samples(custom())) {
      const w = buildCard(state.layout);
      fillCard(w, c, { look: look && look(c) });
      box.append(w);
    }
  }
  const preview = () => { const p = h('div.cd-prev', { 'aria-label': T('Preview') }); previews.add(p); paintPreview(p); return p; };

  const saveLayout = saver(app, L, () => (state.layoutSet ? layoutValue(state.layout) : null));
  const saveStyles = debounce(saver(app, S, () => { const v = state.styles.filter(s => s.when.trim()).map(styleValue); return v.length ? v : null; }), 400);

  // ---- keyboard: a scope over the settings page's own while editing
  let keys = null;
  const outside = e => { if (!e.target.closest || !e.target.closest('.cd, .modal, .pick')) { leave(); redrawAll(); } };
  const leave = () => { if (keys) { keys.dispose(); keys = null; document.removeEventListener('mousedown', outside, true); host.end(); } };
  const enter = (bind) => {
    if (keys) keys.dispose();
    else document.addEventListener('mousedown', outside, true);
    keys = app.keys.scope('cards', { layer: 2, covers: () => true });
    keys.bind('Escape', () => { leave(); redrawAll(); }, T('leave the designer'), { group: T('Card designer'), bar: T('leave'), input: true });
    bind(keys);
  };
  const redrawAll = () => { host.redraw(L); host.redraw(S); };

  // ---- layout
  state.layoutSet = layoutIsSet(ui());
  let pick = ''; // the field the keys move
  const zoneOf = f => ZONES.find(z => z !== 'tray' && state.layout[z].includes(f)) || 'tray';
  const tray = () => [...Object.keys(FIELDS), ...custom()].filter(f => zoneOf(f) === 'tray');
  const list = z => (z === 'tray' ? tray() : state.layout[z]);
  const order = () => ZONES.flatMap(list);
  function move(f, to, at) {
    if (f === 'key' && to === 'tray') { app.ui.toast(T('The key stays on the card'), { kind: 'err' }); return; }
    const from = zoneOf(f);
    if (from !== 'tray') {
      const i = state.layout[from].indexOf(f);
      state.layout[from].splice(i, 1);
      if (from === to && at > i) at--;
    }
    if (to !== 'tray') state.layout[to].splice(Math.max(0, Math.min(at, state.layout[to].length)), 0, f);
    state.layoutSet = true;
    host.redraw(L); drawPreviews(); saveLayout();
  }
  function step(d) { // H/L: one place along, onto the next line at an end
    const f = pick, z = zoneOf(f);
    if (!f) return;
    const i = list(z).indexOf(f), zi = ZONES.indexOf(z);
    if (z !== 'tray' && i + d >= 0 && i + d < list(z).length) return move(f, z, d > 0 ? i + 2 : i - 1);
    const next = ZONES[zi + d];
    if (!next || next === 'tray') return;
    move(f, next, d > 0 ? 0 : list(next).length);
  }
  const BELOW = { top: 'bottom', top_right: 'bottom_right', bottom: 'tray', bottom_right: 'tray' };
  const ABOVE = { tray: 'bottom', bottom: 'top', bottom_right: 'top_right' };
  function jump(d) { // J/K: to the end of the line below or above, on the same side
    const next = (d > 0 ? BELOW : ABOVE)[zoneOf(pick)];
    if (pick && next) move(pick, next, list(next).length);
  }
  const toggle = () => { if (pick) zoneOf(pick) === 'tray' ? move(pick, 'bottom', state.layout.bottom.length) : move(pick, 'tray', 0); };
  const choose = d => { const o = order(); if (!o.length) return; pick = o[(Math.max(0, o.indexOf(pick)) + d + o.length) % o.length]; host.redraw(L); };
  L.wide = true;
  L.activate = L.change = () => {
    pick = pick || state.layout.top[0] || 'key';
    host.begin({ o: L, commit: () => {}, cancel: () => { leave(); host.redraw(L); } });
    enter(k => {
      const g = { group: T('Card designer') };
      k.bind(['h', 'ArrowLeft'], () => choose(-1), T('previous field'), { ...g, bar: T('h l pick') });
      k.bind(['l', 'ArrowRight'], () => choose(1), T('next field'), g);
      k.bind('H', () => step(-1), T('move the field left'), { ...g, bar: T('H L move'), writes: true });
      k.bind('L', () => step(1), T('move the field right'), { ...g, writes: true });
      k.bind('J', () => jump(1), T('move the field a line down'), { ...g, bar: T('J K line'), writes: true });
      k.bind('K', () => jump(-1), T('move the field a line up'), { ...g, writes: true });
      k.bind(['x', 'Delete', 'Backspace'], toggle, T('take the field off the card, or put it back'), { ...g, bar: T('x off/on'), writes: true });
      k.bind(['Enter', 'Space'], () => { leave(); host.redraw(L); }, T('done'), g);
    });
    host.redraw(L);
  };
  L.reset = () => { state.layout = layoutOf({ ...ui(), CardLayout: null }); state.layoutSet = false; host.redraw(L); drawPreviews(); saveLayout(); };
  L.render = () => {
    const editing = !!keys && pick;
    let marker = null;
    const chip = f => h('button.cd-chip' + (editing && f === pick ? '.on' : '') + (FIELDS[f] ? '' : '.custom'), {
      type: 'button', tabindex: -1, draggable: true, dataset: { field: f }, title: FIELDS[f] ? f : T('custom field %s', f),
      ondragstart: e => { e.dataTransfer.setData(DRAG_TYPE, f); e.dataTransfer.effectAllowed = 'move'; e.currentTarget.classList.add('dragging'); },
      ondragend: e => { e.currentTarget.classList.remove('dragging'); if (marker) marker.remove(); },
      onclick: () => { pick = f; if (!keys) L.activate(); else host.redraw(L); },
    }, fieldLabel(f));
    const at = (zone, e) => { // the index the pointer is before, and where to show it
      const cs = [...zone.querySelectorAll('.cd-chip:not(.dragging)')];
      const i = cs.findIndex(c => { const r = c.getBoundingClientRect(); return e.clientX < r.left + r.width / 2; });
      return { i: i < 0 ? cs.length : i, before: cs[i] || null };
    };
    const zone = z => h('div.cd-zone.cd-' + z.replace('_', '-'), {
      dataset: { zone: z }, 'aria-label': ZONE_NAME[z],
      ondragover: e => {
        if (!e.dataTransfer.types.includes(DRAG_TYPE)) return;
        e.preventDefault(); e.dataTransfer.dropEffect = 'move';
        const { before } = at(e.currentTarget, e);
        marker = marker || h('i.cd-mark');
        if (z === 'tray') { marker.remove(); e.currentTarget.classList.add('over'); return; }
        e.currentTarget.insertBefore(marker, before);
      },
      ondragleave: e => { if (!e.currentTarget.contains(e.relatedTarget)) { e.currentTarget.classList.remove('over'); if (marker && marker.parentNode === e.currentTarget) marker.remove(); } },
      ondrop: e => {
        const f = e.dataTransfer.getData(DRAG_TYPE);
        if (!f) return;
        e.preventDefault();
        if (marker) marker.remove();
        e.currentTarget.classList.remove('over');
        pick = f;
        move(f, z, at(e.currentTarget, e).i);
      },
    }, list(z).map(chip), z === 'tray' && !tray().length && h('span.faint', T('every field is on the card')));
    return h('div.cd',
      h('div.cd-card',
        h('div.cd-line', zone('top'), zone('top_right')),
        h('div.cd-sum', T('Summary')),
        h('div.cd-line', zone('bottom'), zone('bottom_right'))),
      h('div.cd-trayrow', h('span.cd-label', ZONE_NAME.tray), zone('tray')),
      preview(),
      h('div.row.cd-foot',
        h('span.faint', keys && pick ? T('h l pick · H L move · J K line · x off/on · esc done') : state.layoutSet ? T('Drag fields onto the card; enter for keys. List rows keep card_fields.') : T('From card_fields until you move a field. Drag fields onto the card; enter for keys.')),
        h('span.spacer'), h('span.cd-status'),
        state.layoutSet && h('button.btn.ghost', { tabindex: -1, onclick: () => L.reset() }, T('Reset'))));
  };

  // ---- styles
  S.wide = true;
  const changed = () => { drawPreviews(); saveStyles(); };
  const env = () => ({ me: app.session && app.session.me && app.session.me.AccountID });
  const hits = s => { const f = s.when.trim() ? compile(s.when.trim().toLowerCase(), env()) : null; return f ? samples(custom()).filter(f).length : 0; };
  function swatches(s, k, label) {
    const set = v => { s[k] = v; host.redraw(S); changed(); };
    return h('span.cd-sw',
      h('button.st-dot.none' + (s[k] ? '' : '.on'), { type: 'button', title: T('None'), 'aria-label': T('No %s', label), onclick: () => set('') }, icon('ban')),
      COLORS.map(c => h('button.st-dot' + (s[k] === c ? '.on' : ''), { type: 'button', title: c, 'aria-label': label + ' ' + c, style: { background: 'var(--' + c + ')' }, onclick: () => set(c) })),
      h('input.st-color', { type: 'color', title: T('Your own colour'), 'aria-label': T('%s colour', label), value: /^#[0-9a-f]{6}$/i.test(s[k]) ? s[k] : '#888888', onchange: e => set(e.target.value), class: s[k] && s[k].startsWith('#') ? 'on' : '' }));
  }
  function fieldsPick(s, k, title) {
    const all = () => [...Object.keys(FIELDS), ...custom()];
    return h('span.cd-fields',
      s[k].map(f => h('span.chip.cd-fchip', fieldLabel(f), h('button.cd-fx', { type: 'button', 'aria-label': T('Remove %s', f), onclick: () => { s[k] = s[k].filter(x => x !== f); host.redraw(S); changed(); } }, '×'))),
      h('button.btn.ghost.cd-add', { type: 'button', onclick: async () => {
        const r = await app.ui.pick({ title, items: all(), label: fieldLabel, multi: true, selected: s[k] });
        if (r) { s[k] = all().filter(f => [].concat(r).includes(f)); host.redraw(S); changed(); }
      } }, icon('plus'), s[k].length ? '' : T('fields')));
  }
  let dragRule = -1;
  function rule(s, i) {
    const n = hits(s);
    const when = h('input.input.cd-when', { type: 'text', value: s.when, placeholder: 'prio>=high  label:urgent  age>3d  "team":core', spellcheck: false, autocomplete: 'off', 'aria-label': T('When'),
      oninput: e => { s.when = e.target.value; const c = e.target.closest('.cd-rule').querySelector('.cd-hits'); if (c) c.textContent = hitText(hits(s)); changed(); } });
    const tog = (k, label) => h('button.btn.ghost.st-pick' + (s[k] ? '.on' : ''), { type: 'button', 'aria-pressed': !!s[k], onclick: () => { s[k] = !s[k]; host.redraw(S); changed(); } }, label);
    return h('div.cd-rule', {
      dataset: { i },
      ondragover: e => { if (dragRule >= 0) { e.preventDefault(); e.currentTarget.classList.add('over'); } },
      ondragleave: e => e.currentTarget.classList.remove('over'),
      ondrop: e => { if (dragRule < 0) return; e.preventDefault(); const [r] = state.styles.splice(dragRule, 1); state.styles.splice(i, 0, r); dragRule = -1; host.redraw(S); changed(); },
    },
    h('div.cd-rhead',
      h('span.cd-grip', { draggable: true, title: T('Drag to reorder: a later style wins'), ondragstart: e => { dragRule = i; e.dataTransfer.setData('text/plain', String(i)); e.dataTransfer.effectAllowed = 'move'; }, ondragend: () => { dragRule = -1; } }, icon('grip-vertical')),
      h('span.cd-label', T('When')), when, h('span.cd-hits.faint', hitText(n)),
      h('button.btn.ghost.st-x', { type: 'button', title: T('Remove this style'), 'aria-label': T('Remove style'), onclick: () => { state.styles.splice(i, 1); host.redraw(S); changed(); } }, '×')),
    h('div.cd-rbody',
      h('span.cd-label', T('Edge')), swatches(s, 'edge', T('Edge')),
      h('span.cd-label', T('Tint')), swatches(s, 'tint', T('Tint')),
      h('span.cd-label', T('Text')), h('span.cd-tog', tog('fade', T('Fade')), tog('bold', T('Bold summary'))),
      h('span.cd-label', T('Hide')), fieldsPick(s, 'hide', T('Fields this style hides')),
      h('span.cd-label', { title: T('These fields stay off every card this style does not match') }, T('Only show')), fieldsPick(s, 'show', T('Fields shown only when this matches'))));
  }
  const hitText = n => T('%d of %d samples', n, samples(custom()).length);
  const editStyles = () => { if (!keys) { host.begin({ o: S, commit: () => {}, cancel: leave }); enter(() => {}); } };
  S.activate = S.change = () => {
    editStyles();
    const first = S.el && S.el.querySelector('.cd-when, .cd-new');
    if (first) first.focus();
  };
  S.reset = () => { state.styles = []; host.redraw(S); drawPreviews(); saveStyles(); };
  S.render = () => h('div.cd', { onfocusin: editStyles },
    state.styles.length ? state.styles.map(rule) : h('div.faint', T('No styles yet. A style changes how the cards a board query matches look.')),
    h('div.row', h('button.btn.cd-new', { type: 'button', onclick: () => { state.styles.push(normStyle({ when: '' })); host.redraw(S); const ins = S.el.querySelectorAll('.cd-when'); if (ins.length) ins[ins.length - 1].focus(); } }, icon('plus'), T('Add style')),
      h('span.spacer'), h('span.cd-status'), state.styles.length > 0 && h('button.btn.ghost', { type: 'button', onclick: () => S.reset() }, T('Remove all'))),
    state.styles.length > 0 && preview());
  return () => leave();
}
