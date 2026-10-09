// Installable shell and phone behaviour: service worker, theme-color,
// card menu on right-click / long-press (the TUI's card menu).
import { h } from './dom.js';
import { T } from './i18n.js';

const KEY = /^[A-Z][A-Z0-9_]*-\d+$/;

export function install(app) {
  worker(app);
  themeColor();
  cardMenu(app);
}

function worker(app) {
  if (!('serviceWorker' in navigator)) return;
  const sw = navigator.serviceWorker;
  let told = false;
  const ready = w => {
    if (told || !w) return; told = true;
    app.ui.toast(T('Update ready'), { ms: 3600000, action: { label: T('Reload'), run: () => w.postMessage('skip') } });
  };
  let reloading = false;
  sw.addEventListener('controllerchange', () => { if (!reloading && told) { reloading = true; location.reload(); } });
  sw.register('/sw.js').then(reg => { // needs localhost or https
    ready(reg.waiting);
    reg.addEventListener('updatefound', () => {
      const w = reg.installing;
      if (w) w.addEventListener('statechange', () => { if (w.state === 'installed' && sw.controller) ready(w); });
    });
    document.addEventListener('visibilitychange', () => { if (!document.hidden) reg.update().catch(() => {}); });
  }).catch(() => {});
}

// The browser chrome follows the theme: the header's colour, re-read when the theme or the OS scheme changes.
function themeColor() {
  const root = document.documentElement;
  let meta = document.querySelector('meta[name=theme-color]');
  if (!meta) { meta = h('meta', { name: 'theme-color' }); document.head.append(meta); }
  let raf = 0;
  const sync = () => {
    cancelAnimationFrame(raf);
    raf = requestAnimationFrame(() => { const c = getComputedStyle(root).getPropertyValue('--bg-2').trim(); if (c) meta.content = c; });
  };
  new MutationObserver(sync).observe(root, { attributes: true, attributeFilter: ['data-theme', 'style'] });
  matchMedia('(prefers-color-scheme: dark)').addEventListener('change', sync);
  sync();
}

// The issue under el: its row's data-key, else the key text of its card (without an agent chip in it).
const keyAt = el => {
  if (!el.closest) return '';
  let k = (el.closest('[data-key]') || { dataset: {} }).dataset.key || '';
  if (!k) {
    const row = el.closest('.card, .lrow, .pl-row'), ke = row && row.querySelector('.ckey, .l-key');
    k = ke ? [...ke.childNodes].filter(n => n.nodeType === Node.TEXT_NODE).map(n => n.nodeValue).join('') : '';
  }
  return KEY.test(k.trim()) ? k.trim() : '';
};

function cardMenu(app) {
  let opened = 0, press = 0, sx = 0, sy = 0, swallow = 0;
  const open = (key, at) => {
    if (Date.now() - opened < 800) return;
    run(key, at);
  };
  // E and the like: at the card when it shows, else the centred picker.
  app.actions.menu = key => {
    const el = document.querySelector('#view [data-key="' + CSS.escape(key) + '"]');
    const r = el && el.getBoundingClientRect();
    run(key, r && r.width ? { x: r.left + 12, y: Math.min(r.bottom - 4, window.innerHeight - 8) } : null);
  };
  const fields = () => import('../views/fields.js');
  function items(key) {
    const url = () => app.session.baseURL.replace(/\/$/, '') + '/browse/' + key;
    // The card's current values, when the view lists it (TUI quick edit).
    const c = (app.listed && app.listed().find(x => x.Key === key)) || {};
    const list = v => (Array.isArray(v) ? v.join(' ') : v || '');
    const extras = app.menuItems ? app.menuItems(key) : [];
    const sprintSub = async () => {
      const [s, m] = await Promise.all([app.api.get('/projects/' + encodeURIComponent(key.split('-')[0]) + '/sprints'), app.api.get('/issues/' + key + '/editmeta', { fresh: true })]);
      const cur = m.Sprint && m.Sprint.ID ? m.Sprint.ID : 0;
      return [...(s.Sprints || []), { ID: 0, Name: T('Backlog') }].map(sp => ({ label: sp.Name, hint: sp.State || '', current: sp.ID === cur,
        run: () => sp.ID !== cur && fields().then(f => f.setField(app, key, 'sprint', { Sprint: sp.ID }, sp.ID ? T('%s → %s', key, sp.Name) : T('%s → backlog', key), sp.Name)) }));
    };
    return [
      { label: T('Open'), run: () => app.panel.open(key) },
      { label: T('Open in new tab'), run: () => window.open('/issue/' + key, '_blank', 'noopener') },
      '-',
      { label: T('Status'), hint: c.Status, sub: async () => (await app.api.get('/issues/' + key + '/transitions', { fresh: true })).map(t => ({
        label: t.Name, current: c.StatusID != null && String(t.StatusID) === String(c.StatusID), run: () => fields().then(f => f.takeMove(app, key, t)) })) },
      { label: T('Assignee'), hint: c.Assignee, sub: async () => {
        const us = await app.api.get('/users?issue=' + key + '&q=');
        const me = (app.session.me && app.session.me.AccountID) || '';
        const set = u => fields().then(f => f.setField(app, key, 'assignee', { ID: u ? u.AccountID : '' }, u ? T('%s → %s', key, u.DisplayName) : T('%s unassigned', key), u ? { AccountID: u.AccountID, DisplayName: u.DisplayName } : null));
        const people = us.slice().sort((a, b) => (b.AccountID === me) - (a.AccountID === me)).slice(0, 15);
        return [{ label: T('Unassigned'), current: !!c.Key && !c.AssigneeID, run: () => set(null) }, ...people.map(u => ({ label: u.AccountID === me ? T('%s (me)', u.DisplayName) : u.DisplayName, current: u.AccountID === c.AssigneeID, run: () => set(u) })),
          '-', { label: T('Someone else…'), run: () => app.actions.edit(key, 'assignee') }];
      } },
      { label: T('Priority'), hint: c.Priority, sub: async () => (await app.api.get('/priorities')).map(p => ({
        label: p.Name, current: p.Name === c.Priority, run: () => fields().then(f => f.setField(app, key, 'priority', { ID: p.ID }, T('%s priority → %s', key, p.Name), p.Name)) })) },
      extras.find(x => x.id === 'sprint') || { id: 'sprint', label: T('Sprint'), hint: c.Sprint || '', sub: sprintSub },
      { label: T('Story points…'), hint: c.Points, run: () => app.actions.edit(key, 'points') },
      { label: T('Labels…'), hint: list(c.Labels), run: () => app.actions.edit(key, 'labels') },
      { label: T('Summary…'), run: () => app.actions.edit(key, 'summary') },
      ...extras.filter(x => x.id !== 'sprint'),
      '-',
      { label: T('Copy key'), run: () => navigator.clipboard && navigator.clipboard.writeText(key).then(() => app.ui.toast(T('Copied %s', key))) },
      { label: T('Copy link'), run: () => navigator.clipboard && navigator.clipboard.writeText(url()).then(() => app.ui.toast(T('Copied link'))) },
      { label: T('Open in Jira'), run: () => window.open(url(), '_blank', 'noopener') },
    ];
  }
  // at: where the menu goes; none (a long press on a phone) keeps the picker, a submenu a second one.
  function run(key, at) {
    opened = Date.now();
    const its = items(key);
    if (at) return import('./ctxmenu.js').then(m => m.ctxMenu(its, at.x, at.y));
    const flat = its.filter(it => it !== '-');
    app.ui.pick({ title: key, items: flat, label: i => i.label + (i.hint ? '  · ' + i.hint : '') + (i.sub ? '  ▸' : ''), placeholder: T('Action…') }).then(async r => {
      if (!r) return;
      if (!r.sub) return r.run && r.run();
      const sub = (await r.sub()).filter(it => it !== '-');
      const s = await app.ui.pick({ title: key + ' ' + r.label.toLowerCase(), items: sub, label: i => (i.current ? '✓ ' : '') + i.label + (i.hint ? '  · ' + i.hint : '') });
      if (s && s.run) s.run();
    }).catch(e => app.ui.errToast(e));
  }
  document.addEventListener('contextmenu', e => {
    if (e.defaultPrevented || e.target.closest('input, textarea, a[href]')) return;
    const key = keyAt(e.target); if (!key) return;
    e.preventDefault(); open(key, { x: e.clientX, y: e.clientY });
  });
  // iOS fires no contextmenu on a long press.
  document.addEventListener('touchstart', e => {
    if (e.touches.length !== 1) return;
    const key = keyAt(e.target); if (!key) return;
    sx = e.touches[0].clientX; sy = e.touches[0].clientY;
    clearTimeout(press);
    press = setTimeout(() => { swallow = Date.now(); if (navigator.vibrate) navigator.vibrate(10); open(key); }, 450);
  }, { passive: true });
  const cancel = () => clearTimeout(press);
  document.addEventListener('touchend', cancel, { passive: true });
  document.addEventListener('touchcancel', cancel, { passive: true });
  document.addEventListener('touchmove', e => { const t = e.touches[0]; if (Math.abs(t.clientX - sx) + Math.abs(t.clientY - sy) > 10) cancel(); }, { passive: true });
  document.addEventListener('click', e => { if (Date.now() - swallow < 700) { e.stopPropagation(); e.preventDefault(); } }, true);
}
