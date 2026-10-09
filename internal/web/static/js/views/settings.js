// Settings: topic headings, `/` filters by name. j/k move, enter/space/→ change, ← back, del resets, esc leaves.
// Sections in four groups (GROUPS): the web app's own, saved on the server for every browser (Appearance, Fonts, Board, Notifications), Jira and server (Jira site
// and Server: settings_site.js; GitLab, Data), every ui: option of the config file by topic (settings_config.js),
// shared with the terminal app, and Keyboard (remaps, settings_keys.js, a row per folded group). With room a sidebar
// lists the sections and marks the one in view.
// Advanced (a; per browser) shows the options for specifics and tuning (`advanced`: config.SettingsBasic lacks the
// ui: one); off, one shows only when the filter finds it or the config file sets it, marked.
//
// Board prefs (app.prefs, per site) for the board views:
//   board.mode     'lanes' | 'list'                  (default: session.ui.DefaultMode or 'lanes')
//   board.refresh  seconds between idle refetches, '0' = off (default 120)
//   board.empty_lanes  'show' | 'hide'               (default: session.ui.EmptyLanes or 'show'; alt+e)
// Card fields and the card limit are the config's ui.card_fields and ui.card_limit (below); ui.card_layout and
// ui.card_styles get the card designer (settings_cards.js); ui.quick_filters, ui.views and ui.my_work_jql JQL
// editors with completion (settings_jql.js).
// Changing one emits bus 'prefs' {key, value}.
import { h, clear } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import theme from '../lib/theme.js';
import { onChange as onMetrics } from '../lib/metrics.js';
import api from '../lib/api.js';
import * as notifier from '../lib/notify.js';
import { load as loadConfig } from './settings_config.js';
import { designCards } from './settings_cards.js';
import { designJQL } from './settings_jql.js';
import { designLanes } from './settings_lanes.js';
import { keyOptions } from './settings_keys.js';
import { fontOptions } from './settings_fonts.js';
import { siteOptions } from './settings_site.js';
import { accountOptions } from './settings_account.js';
import { T } from '../lib/i18n.js';

css('settings');

// GROUPS order the sections; a section none lists is the config file's (its topics in the server's order).
const GROUPS = [
  { title: T('Web app'), desc: T('every browser on this laneway web; the terminal has its own'), sections: ['Appearance', 'Fonts', 'Board', 'Notifications'] },
  { title: T('Jira and server'), sections: ['Account', 'Jira site', 'Server', 'GitLab', 'Data'] },
  { title: T('Config file'), desc: T('ui: options, shared with the terminal app'), sections: null },
  { title: T('Keyboard'), sections: ['Keyboard'] },
];
// A section's name is its id (options carry it, the sidebar and anchors key on it); this is what shows.
const SECTIONS = { Appearance: T('Appearance'), Fonts: T('Fonts'), Board: T('Board'), Notifications: T('Notifications'), Account: T('Account'), 'Jira site': T('Jira site'), Server: T('Server'), GitLab: 'GitLab', Data: T('Data'), Keyboard: T('Keyboard') };
const secName = sec => SECTIONS[sec] || T(sec);
const VAL = { compact: T('compact'), normal: T('normal'), roomy: T('roomy'), show: T('show'), hide: T('hide'), lanes: T('lanes'), list: T('list') };
const CONFIG = GROUPS.findIndex(g => !g.sections);
const groupOf = sec => { const i = GROUPS.findIndex(g => g.sections && g.sections.includes(sec)); return i < 0 ? CONFIG : i; };
const slug = sec => 'st-' + sec.replace(/\W+/g, '-');

const cycle = (list, cur, d) => list[(Math.max(0, list.indexOf(cur)) + d + list.length) % list.length];

export default function mount(el, { app, scope, toolbar, query }) {
  const pref = (k, d) => app.prefs.get(k, d);
  const setPref = (k, v) => { app.prefs.set(k, v); app.bus.emit('prefs', { key: k, value: String(v) }); };
  const ui = (app.session && app.session.ui) || {};
  const s = app.session || {};

  // Font size: 11-20px in steps, or auto (the density's size). Always shows the size in use.
  const FS_MIN = 11, FS_MAX = 20;
  const setSize = v => { theme.setFontSize(v); refresh(); };
  const stepSize = d => { const cur = theme.fontSize || Math.round(theme.effectiveFontSize); setSize(Math.min(FS_MAX, Math.max(FS_MIN, cur + d))); };
  const fontSize = {
    name: T('Font size'), desc: T('scales all text and spacing; h/l or enter steps 1px, del = auto'), section: 'Appearance',
    render: () => {
      const eff = Math.round(theme.effectiveFontSize), auto = !theme.fontSize;
      return h('span.st-val.st-step',
        h('button.btn.ghost', { tabindex: -1, 'aria-label': T('Smaller'), disabled: !auto && theme.fontSize <= FS_MIN, onclick: () => stepSize(-1) }, icon('minus')),
        h('span.st-num.mono', { title: auto ? T('from the density') : T('fixed') }, auto ? T('auto · %dpx (from density)', eff) : eff + 'px'),
        h('button.btn.ghost', { tabindex: -1, 'aria-label': T('Larger'), disabled: !auto && theme.fontSize >= FS_MAX, onclick: () => stepSize(1) }, icon('plus')),
        h('button.btn.ghost' + (auto ? '.on' : ''), { tabindex: -1, title: T('Follow the density'), onclick: () => setSize(0) }, T('auto')));
    },
    change: stepSize, reset: () => setSize(0),
  };
  // An option: {name, desc, section, render() → control node, change(dir)?}
  const choice = (name, desc, section, list, get, set, label = x => VAL[x] || x) => ({
    name, desc, section,
    render: () => h('span.st-val', h('button.btn.ghost', { tabindex: -1, onclick: () => set(cycle(list, get(), 1)) }, label(get()))),
    change: d => set(cycle(list, get(), d)),
  });
  const info = (name, value, section, desc = '') => ({ name, desc, section, render: () => h('span.st-val.mono', value) });

  const options = [
    { name: T('Theme'), desc: T('g t cycles through them anywhere'), section: 'Appearance',
      render: themeGroups,
      change: d => { theme.set(cycle(theme.presets.map(p => p.id), theme.current, d)); refresh(); } },
    { name: T('Accent'), desc: T('highlights, focus, primary buttons'), section: 'Appearance',
      render: () => h('span.st-swatches',
        h('button.st-dot.none', { tabindex: -1, title: T('Theme default'), class: theme.accent ? '' : 'on', onclick: () => { theme.setAccent(''); refresh(); } }, icon('ban')),
        theme.accents.map(c => h('button.st-dot', { tabindex: -1, title: c, class: theme.accent === c ? 'on' : '', style: { background: c }, onclick: () => { theme.setAccent(c); refresh(); } })),
        h('input.st-color', { type: 'color', title: T('Custom colour'), value: /^#[0-9a-f]{6}$/i.test(theme.accent) ? theme.accent : '#5b8def', tabindex: -1, oninput: e => { theme.setAccent(e.target.value); }, onchange: refresh })),
      change: d => { const l = ['', ...theme.accents]; theme.setAccent(cycle(l, theme.accent, d)); refresh(); } },
    choice(T('Density'), T('spacing of rows and panels'), 'Appearance', ['compact', 'normal', 'roomy'], () => theme.density, v => { theme.setDensity(v); refresh(); }),
    fontSize,
    ...fontOptions(app, () => refresh()),
    choice(T('Motion'), T('animations and transitions'), 'Appearance', ['auto', 'reduce'], () => theme.motion, v => { theme.setMotion(v); refresh(); }, v => (v === 'reduce' ? T('reduced') : T('system'))),
    choice(T('Key bar'), T('the main keys here at the bottom, a click presses one; messages show in it'), 'Appearance', ['show', 'hide'], () => pref('keybar', 'show'), v => { setPref('keybar', v); refresh(); }),
    { name: T('Custom tokens'), desc: T('CSS variables as JSON, e.g. {"--bg": "#101010", "--radius": "2px"}; ctrl+enter applies'), section: 'Appearance', wide: true, advanced: true, render: customEditor, change: () => editor && editor.focus() },

    { name: T('Browser notifications'), desc: T('inbox news while this tab is in the background; the browser asks when you turn them on'), section: 'Notifications',
      render: () => { const on = notifier.enabled(); return h('span.st-val', h('button.st-switch' + (on ? '.on' : ''), { role: 'switch', 'aria-checked': on, 'aria-label': T('Browser notifications'), tabindex: -1, onclick: () => toggleNotify() }, h('i')), h('span.st-state', notifier.permission() === 'denied' ? T('blocked by the browser') : on ? T('on') : T('off'))); },
      change: () => toggleNotify() },
    { ...action(T('Test notification'), T('shows one now'), 'Notifications', () => { if (!notifier.notify('laneway', T('Notifications work.'))) app.ui.toast(T('Turn notifications on first'), { kind: 'err' }); }), advanced: true },

    choice(T('Default mode'), T('how the board opens'), 'Board', ['lanes', 'list'], () => pref('board.mode', ui.DefaultMode || 'lanes'), v => { setPref('board.mode', v); refresh(); }),
    choice(T('Empty lanes'), T('columns the filters leave without a card (alt+e)'), 'Board', ['show', 'hide'], () => pref('board.empty_lanes', String(ui.EmptyLanes || '').toLowerCase() === 'hide' ? 'hide' : 'show'), v => { setPref('board.empty_lanes', v); refresh(); }),
    { ...choice(T('Auto refresh'), T('refetch an idle board'), 'Board', ['0', '30', '60', '120', '300'], () => pref('board.refresh', '120'), v => { setPref('board.refresh', v); refresh(); }, v => (v === '0' ? 'off' : v >= 60 ? v / 60 + 'm' : v + 's')), advanced: true },

    s.autostart && { name: T('Start at login'), desc: T('runs laneway web in the background when you log in, on %s (%s)', location.host, s.autostart.path), section: 'Server', advanced: true,
      render: () => { const on = s.autostart.enabled; return h('span.st-val', h('button.st-switch' + (on ? '.on' : ''), { role: 'switch', 'aria-checked': on, 'aria-label': T('Start at login'), tabindex: -1, onclick: () => toggleLogin() }, h('i')), h('span.st-state', on ? T('on') : T('off'))); },
      change: () => toggleLogin() },
    ...siteOptions(app, () => refresh()),
    info(T('Site'), s.site || '-', 'Jira site'), info('Jira', s.baseURL || '-', 'Jira site'),
    info(T('Signed in as'), (s.me && s.me.DisplayName) || '-', 'Jira site'), { ...info(T('Version'), s.version || 'dev', 'Server'), advanced: true },
    { ...action(T('Clear cached data'), T('the browser copy of API answers; reloaded on demand'), 'Data', () => { api.forget(); app.ui.toast(T('Caches cleared'), { kind: 'ok' }); }), advanced: true },
    { ...action(T('Clear recent issues'), T('the palette’s recent list and search history'), 'Data', () => {
      try { for (const k of Object.keys(localStorage)) if (/^lw:(recent|jqlhist|cmdrecent):/.test(k)) localStorage.removeItem(k); } catch (e) { /* ignore */ }
      app.ui.toast(T('Recents cleared'), { kind: 'ok' });
    }), advanced: true },
  ].filter(Boolean);
  async function toggleLogin() {
    try {
      s.autostart = await api.put('/autostart', { On: !s.autostart.enabled });
      app.ui.toast(s.autostart.enabled ? T('laneway web starts when you log in') : T('No longer starts at login; this one runs until you stop it'), { kind: 'ok' });
    } catch (e) { app.ui.errToast(e); }
    refresh();
  }
  async function toggleNotify() {
    const on = await notifier.setEnabled(!notifier.enabled());
    if (!on && notifier.permission() === 'denied') app.ui.toast(T('The browser blocks notifications for this site'), { kind: 'err' });
    refresh();
  }
  function action(name, desc, section, run) {
    return { name, desc, section, render: () => h('span.st-val', h('button.btn', { tabindex: -1, onclick: run }, T('Run'))), change: run };
  }
  let allThemes = false;
  function themeGroups() {
    const groups = new Map();
    for (const p of theme.presets) groups.set(p.group, [...(groups.get(p.group) || []), p]);
    const cur = (theme.presets.find(p => p.id === theme.current) || theme.presets[0]).group;
    const shown = allThemes ? [...groups] : [...groups].filter(([g]) => g === cur);
    return h('div.st-themes', shown.map(([g, ps]) => h('div.st-tg', h('span.st-tg-name', g), h('span.st-swatches', ps.map(swatch)))),
      h('button.btn', { tabindex: -1, onclick: () => { allThemes = !allThemes; refresh(); } }, allThemes ? T('Fewer themes') : T('More themes…')));
  }
  function swatch(p) {
    const t = theme.tokens(p.id);
    return h('button.st-sw', { tabindex: -1, class: theme.current === p.id ? 'on' : '', title: p.name, onclick: () => { theme.set(p.id); refresh(); } },
      h('span.st-sw-box', { style: { background: t.bg, borderColor: t.border } },
        h('i', { style: { background: t['bg-2'] } }), h('i', { style: { background: t.fg } }), h('i', { style: { background: t.accent } }), h('i', { style: { background: t.ok } })),
      h('span.st-sw-name', p.name));
  }
  let editor = null;
  function customEditor() {
    const cur = theme.custom;
    const ta = editor = h('textarea.input.st-json', { rows: 5, spellcheck: false, placeholder: '{\n  "--bg": "#101010"\n}', value: Object.keys(cur).length ? JSON.stringify(cur, null, 2) : '' });
    const err = h('div.st-err');
    const apply = () => {
      try {
        const v = ta.value.trim() ? JSON.parse(ta.value) : {};
        if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error(T('Expected an object of "--token": "value"'));
        theme.setCustom(v); err.textContent = ''; app.ui.toast(T('Tokens applied'), { kind: 'ok' });
      } catch (e) { err.textContent = e.message; }
    };
    ta.addEventListener('keydown', e => {
      if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) { e.preventDefault(); apply(); }
    });
    return h('div.st-edit', ta, err, h('div.row', h('button.btn', { tabindex: -1, onclick: apply }, T('Apply')),
      h('button.btn.ghost', { tabindex: -1, onclick: () => { ta.value = ''; apply(); } }, T('Reset'))));
  }

  // ---- view
  let q = '', sel = 0, rows = [], editing = null, cfg = null, keyRows = [];
  const folds = new Set(); // the key groups open
  const filter = h('input.input.st-filter', { type: 'search', placeholder: T('Filter settings  (/)'), spellcheck: false, 'aria-label': T('Filter settings'), oninput: e => { q = e.target.value.trim().toLowerCase(); sel = 0; draw(); } });
  const list = h('div.st-list');
  const foot = h('div.st-foot');
  const nav = h('nav.st-nav', { 'aria-label': T('Settings sections') });
  let adv = false;
  try { adv = localStorage.getItem('lw:settings.advanced') === '1'; } catch (e) { /* ignore */ }
  const advBtn = h('button.btn', { 'aria-pressed': String(adv), title: T('Show the options for specifics and tuning too  (a)'), onclick: () => setAdv(!adv) }, T('Advanced'));
  toolbar.append(filter, advBtn);
  el.append(h('div.st', nav, h('div.st-main', list, foot)));
  const setFoot = () => { foot.textContent = T('j/k move · enter/space change · ←/→ cycle · del reset · / filter · a advanced · esc leaves') + (cfg && cfg.path ? T(' · ui: options write to %s', cfg.path) : ''); };
  setFoot();

  const host = {
    redraw: o => { if (o.el && o.el.isConnected) { const n = rowFor(o); o.el.replaceWith(n); o.el = n; mark(false); } },
    begin: e => { editing = e; },
    end: () => { editing = null; el.focus(); },
    commit: () => editing && editing.commit(),
    cancel: () => editing && editing.cancel(),
    reload: () => { rebuildKeys(); draw(); },
    // fold opens or shuts a key group in place: its rows, not the page (draw() redoes some 1200 elements).
    // A filter decides which keys show, so with one it draws.
    fold: g => {
      folds.has(g) ? folds.delete(g) : folds.add(g);
      const head = options.find(o => o.fold === g);
      if (q || !head || !head.el || !head.el.isConnected) return draw();
      const keys = options.filter(o => o.key && o.group === g);
      if (folds.has(g)) { let at = head.el; for (const o of keys) { o.el = rowFor(o); at.after(o.el); at = o.el; } }
      else for (const o of keys) { if (o.el) o.el.remove(); o.el = null; }
      rows = shown().filter(o => !o.static);
      host.redraw(head); // its chevron
      spy();
    },
    folds,
  };
  function rebuildKeys() {
    options.splice(0, options.length, ...options.filter(o => !keyRows.includes(o)));
    keyRows = keyOptions(app, host);
    options.push(...keyRows);
  }

  const hay = o => (o.name + ' ' + o.name.replace(/_/g, ' ') + ' ' + (o.desc || '') + ' ' + o.section + ' ' + (o.meta || '') + ' ' + (o.advanced ? T('advanced') + ' ' : '') + (o.key ? app.keys.registry().filter(r => r.desc === o.name).map(r => r.specs.join(' ')).join(' ') : '')).toLowerCase();
  const hid = o => o.advanced && !adv && !(o.st && o.st.Set); // a config option the file sets stays in sight
  const shown = () => {
    const words = q.split(/\s+/).filter(Boolean);
    const match = o => { const t = hay(o); return words.every(w => t.includes(w)); };
    // A folded group's keys show only when the filter finds them; its row shows while any key of it does.
    const vis = options.filter(o => {
      if (o.key) return folds.has(o.group) || (words.length > 0 && match(o));
      if (o.fold) return !words.length || match(o) || options.some(k => k.key && k.group === o.fold && match(k));
      return words.length ? match(o) : !hid(o);
    });
    const topics = (cfg && cfg.groups) || [];
    const seen = []; for (const o of vis) if (!seen.includes(o.section)) seen.push(o.section);
    const at = sec => {
      const g = groupOf(sec), list = GROUPS[g].sections;
      const i = list ? list.indexOf(sec) : topics.includes(sec) ? topics.indexOf(sec) : topics.length + seen.indexOf(sec);
      return g * 1000 + i;
    };
    return vis.map((o, i) => [o, at(o.section), i]).sort((a, b) => a[1] - b[1] || a[2] - b[2]).map(x => x[0]);
  };
  function rowFor(o) {
    return h('div.st-row' + (o.wide ? '.wide' : '') + (o.fold ? '.st-fold' : ''), { role: 'group', 'aria-label': o.name, onclick: e => { const i = rows.indexOf(o); if (i >= 0) { sel = i; mark(); } if (o.fold) o.activate(); } },
      h('div.st-name', h('div', o.name, o.meta && h('span.chip', o.meta), o.advanced && !adv && h('span.chip', T('advanced'))), o.desc && h('div.st-desc', o.desc)), o.render());
  }
  function draw() {
    const vis = shown(), was = rows[sel];
    rows = vis.filter(o => !o.static);
    sel = rows.includes(was) ? rows.indexOf(was) : Math.min(sel, Math.max(0, rows.length - 1)); // options loaded above it keep the cursor
    clear(list); clear(nav);
    let grp = -1, sec = null, body = null;
    for (const o of vis) {
      if (o.section !== sec) {
        sec = o.section;
        const g = groupOf(sec), G = GROUPS[g];
        if (g !== grp) {
          grp = g;
          list.append(h('div.st-gh', h('h2', G.title), G.desc && h('span.st-desc', G.desc)));
          if (!(G.sections && G.sections.length === 1)) nav.append(h('div.st-nav-g', G.title));
        }
        const one = !!(G.sections && G.sections.length === 1);
        if (!one) list.append(h('h3.st-h', { id: slug(sec) }, secName(sec)));
        body = h('div.st-sec' + (o.fold || o.key ? '.st-keys' : ''), { role: 'group', 'aria-label': secName(sec), id: one ? slug(sec) : null });
        list.append(body);
        const target = slug(sec);
        nav.append(h('a.st-nav-a', { href: '/settings', dataset: { sec: target }, onclick: e => { e.preventDefault(); const t = list.querySelector('#' + target); if (t) { jumped = target; t.scrollIntoView({ block: 'start' }); spy(); } } }, secName(sec)));
      }
      o.el = rowFor(o);
      body.append(o.el);
    }
    if (!vis.length) list.append(h('div.empty', T('No settings match “%s”', q)));
    mark(); spy();
  }
  // spy marks the sidebar's link of the section at the top of the view; the last at the bottom, a jumped-to one
  // while it shows (a short one near the end never reaches the top).
  let jumped = null;
  function spy() {
    const box = el.getBoundingClientRect(), top = box.top + 48;
    const ids = [...list.querySelectorAll('[id^="st-"]')];
    let cur = ids[0] && ids[0].id;
    for (const t of ids) { if (t.getBoundingClientRect().top <= top) cur = t.id; else break; }
    if (ids.length && el.scrollTop + el.clientHeight >= el.scrollHeight - 2) cur = ids[ids.length - 1].id;
    const j = jumped && list.querySelector('#' + jumped);
    if (j && j.getBoundingClientRect().top < box.bottom && j.getBoundingClientRect().top >= box.top - 2) cur = jumped; else jumped = null;
    for (const a of nav.children) if (a.dataset.sec) a.classList.toggle('on', a.dataset.sec === cur);
  }
  el.addEventListener('scroll', spy, { passive: true });
  // mark highlights the selected row and scrolls to it; a row re-rendered in place keeps the scroll (a drag never
  // selects its row, so scrolling would jump to the old selection).
  function mark(scroll = true) {
    for (const o of options) if (o.el) o.el.classList.toggle('sel', rows[sel] === o);
    const o = rows[sel]; if (scroll && o && o.el && o.el.scrollIntoView) o.el.scrollIntoView({ block: 'nearest' });
  }
  function refresh() { // re-render the rows in place: a value changed
    for (const o of options) if (o.el && o.el.isConnected) { const n = rowFor(o); o.el.replaceWith(n); o.el = n; }
    mark(false);
  }
  // setAdv shows or hides the advanced options, the cursor staying on its option while that shows.
  function setAdv(on) {
    const cur = rows[sel];
    adv = on;
    try { localStorage.setItem('lw:settings.advanced', on ? '1' : '0'); } catch (e) { /* ignore */ }
    advBtn.setAttribute('aria-pressed', String(on));
    draw();
    const i = rows.indexOf(cur);
    if (i >= 0) { sel = i; mark(); }
  }
  const change = d => { const o = rows[sel]; if (o && o.change) o.change(d); };
  const activate = () => { const o = rows[sel]; if (o) (o.activate || o.change || (() => {}))(1); };
  const go = d => { if (rows.length) { sel = (sel + d + rows.length) % rows.length; mark(); } };

  scope.bind(['j', 'ArrowDown'], () => go(1), T('next option'), { group: T('Settings') });
  scope.bind(['k', 'ArrowUp'], () => go(-1), T('previous option'), { group: T('Settings') });
  scope.bind(['Enter', 'Space'], activate, T('change option'), { group: T('Settings'), bar: T('change') });
  scope.bind(['ArrowRight', 'l'], () => change(1), T('next value'), { group: T('Settings') });
  scope.bind(['ArrowLeft', 'h'], () => change(-1), T('previous value'), { group: T('Settings') });
  scope.bind(['Delete', 'Backspace'], () => { const o = rows[sel]; if (o && o.reset) o.reset(); }, T('reset to the default'), { group: T('Settings'), bar: T('reset') });
  scope.bind('a', () => setAdv(!adv), T('show or hide the advanced options'), { group: T('Settings') });
  scope.bind('/', () => { filter.focus(); filter.select(); }, T('filter settings'), { group: T('Settings'), bar: T('filter') });
  scope.bind('Escape', () => { if (q) { filter.value = ''; q = ''; draw(); } else app.back('/board'); }, T('leave settings'), { group: T('Settings'), bar: T('leave') });
  scope.bind('Escape', () => { if (editing) editing.cancel(); else { filter.blur(); el.focus(); } }, '', { input: true, hidden: true });
  scope.bind('Enter', () => { if (editing) editing.commit(); else { filter.blur(); el.focus(); } }, '', { input: true, hidden: true, when: () => !(editing && editing.multi) });
  scope.bind('ctrl+Enter', () => { if (editing) editing.commit(); }, '', { input: true, hidden: true });
  scope.bind('ArrowDown', () => { filter.blur(); el.focus(); go(1); }, '', { input: true, hidden: true, when: () => !editing });

  draw();
  el.focus();
  // ?row= starts on that option (the account menu's API tokens and apps: row 'tokens').
  if (query.row) { const i = rows.findIndex(o => o.row === query.row); if (i >= 0) { sel = i; mark(); } }
  let dead = false, offCards = null, offJQL = null, offLanes = null;
  keyRows = keyOptions(app, host); options.push(...keyRows); draw();
  loadConfig(app, host).then(c => {
    if (dead) return;
    cfg = c;
    offCards = designCards(app, host, c.options);
    offJQL = designJQL(app, host, c.options);
    offLanes = designLanes(app, host, c.options);
    options.push(...c.options);
    if (c.path) options.push({ ...info(T('Config file'), c.path, 'Server', c.editable ? T('ui: options are written here, comments kept') : T('read-only')), advanced: true });
    for (const w of c.warnings) options.push(info(T('Config warning'), w, 'Server'));
    setFoot(); draw();
  }).catch(e => app.ui.errToast(e));
  // You on the server (settings_account.js): which rows depends on it.
  api.get('/me', { fresh: true }).then(m => {
    if (dead) return;
    options.push(...accountOptions(app, m, () => refresh()));
    draw();
  }).catch(e => console.warn('account', e));
  // Each GitLab instance (the gitlab: config's, then glab's logins), signed in to.
  api.get('/gitlab', { fresh: true }).then(list => {
    if (dead || !list.length) return;
    options.push(...list.map(g => ({ ...info(g.Host, g.OK ? g.User + ' (' + g.From + ')' : g.From ? T('fails') : T('no token'), 'GitLab', g.Summary), advanced: true })));
    draw();
  }).catch(() => {});
  // Density or font size changed elsewhere (palette, phone breakpoint): the shown size follows.
  const offMetrics = onMetrics(() => { if (fontSize.el && fontSize.el.isConnected) { const n = rowFor(fontSize); fontSize.el.replaceWith(n); fontSize.el = n; mark(false); } });
  return () => { dead = true; el.removeEventListener('scroll', spy); offMetrics(); if (offCards) offCards(); if (offJQL) offJQL(); if (offLanes) offLanes(); editor = null; editing = null; };
}
