// laneway web shell: session, hash router, panel host, global keys, command registry.
// Feature views live in js/views/*.js and talk to the shell only through `app`.
import { h, clear, $ } from './lib/dom.js';
import api from './lib/api.js';
import bus from './lib/bus.js';
import { keys } from './lib/keys.js';
import theme from './lib/theme.js';
import { onChange as onMetrics } from './lib/metrics.js';
import * as ui from './lib/ui.js';
import { routes } from './views/index.js';
import * as chromeBars from './lib/chrome.js';
import * as store from './lib/store.js';
import { emojiTable } from './lib/md.js';
import * as nav from './lib/nav.js';
import { T } from './lib/i18n.js';


// ---- commands: the palette lists these. register({id, title, keys?, group?, run, when?}) → unregister
const cmds = new Map();
const commands = {
  register(c) { cmds.set(c.id, c); return () => cmds.delete(c.id); },
  list: () => [...cmds.values()].filter(c => !c.when || c.when()),
  run(id) { const c = cmds.get(id); if (c) return c.run(); },
};

// Look-and-feel prefs are mirrored unscoped (boot.js and fonts.js read them before the site is known); the rest per site.
const GLOBAL_PREF = /^(font|terminal|theme)\./;

// Where the focus or a press last landed, in the panel or not (app.panel.focused); a modal over them, or the key bar, changes neither.
// body.panel-focus shows it: the panel's edge lit, the view's cursor muted.
let panelFocus = false;
const paintFocus = () => document.body.classList.toggle('panel-focus', app.panel.focused());
const focusAt = e => {
  const t = e.target;
  if (!t || !t.closest || t.closest('.overlay, #keybar')) return;
  panelFocus = !!t.closest('#panel');
  setTimeout(paintFocus); // after the press has moved the focus
};
document.addEventListener('focusin', focusAt, true);
document.addEventListener('pointerdown', focusAt, true);

// ---- history. Each entry laneway makes holds {lw: {d}}: d, the laneway entries behind it, so app.back never leaves the app.
const lw = () => (history.state && history.state.lw) || null;
const here = () => location.pathname + location.search;
let shown = '';        // the URL on screen; an event for it again changes nothing
let depth = 0;         // d of the entry shown
function record(url, { push = false } = {}) {
  if (push) { depth = ((lw() || { d: depth }).d) + 1; history.pushState({ lw: { d: depth } }, '', url); }
  else history.replaceState({ ...(history.state || {}), lw: lw() || { d: depth } }, '', url);
  shown = here();
}
// The browser moved: back, forward, a typed URL or a plain link. One it made itself is stamped as the next step.
function onURL() {
  if (location.hash.startsWith('#/')) history.replaceState(history.state, '', location.hash.slice(1)); // an old #/ link
  if (here() === shown) return;
  const s = lw();
  if (s) depth = s.d;
  else { depth += 1; history.replaceState({ ...(history.state || {}), lw: { d: depth } }, ''); }
  navigate();
}
const nameOf = path => { const r = table.find(r => r.re.test(path)); return r ? r.name : ''; };

export const app = {
  api, bus, keys, ui, theme, commands, routes,
  session: null,
  route: null,
  // Navigate: app.go('/board/ABC/12?issue=ABC-1'), a step back can undo; {replace} takes this entry's place.
  // The route shown again, bare (g b on the board), is no step; the open panel comes along.
  go(to, { replace = false } = {}) {
    const url = nav.target(here(), to, { panel: app.panel.key, nameOf });
    if (!url) return;
    record(url, { push: !replace });
    navigate();
  },
  // back: the previous entry when laneway made it, else fallback in this one's place (esc on a page opened by a link).
  back(fallback = '/board') {
    const s = lw();
    if (s && s.d > 0) history.back(); else app.go(fallback, { replace: true });
  },
  // setURL: the view says where it is now without remounting: a canonical path (replace), or a place of its
  // own a step back returns to ({push}: another project on the roadmap).
  setURL(url, { push = false } = {}) {
    if (url === here()) return;
    record(url, { push });
    const { path, query } = nav.split(url);
    if (current) current.path = path;
    if (app.route) app.route.query = query;
  },
  // Query params of the current hash route, updatable without remounting: app.setQuery({tab:'day'}).
  query() { return nav.split(here()).query; },
  setQuery(patch, { replace = true } = {}) { app.setURL(nav.withQuery(here(), patch), { push: !replace }); },
  // The issue panel. Board/list/etc. call app.panel.open(key[, {card}]); the issue module renders it,
  // its head from `card` (the view's copy) until its own data arrives. It is ?issue= in the URL, and every
  // change a step back undoes (opening, closing, a link followed) but another issue of the list beside
  // (the cursor, [ ]), which takes the entry's place; {push} makes that a step too (a jump), {replace} any.
  panel: {
    key: null, cleanup: null,
    async open(key, opts = {}) {
      const el = $('#panel'); if (!key) return app.panel.close();
      if (app.panel.key === key && !el.hidden && !opts.force) return;
      if (opts.url !== false && current) app.setQuery({ issue: key }, { replace: !!opts.replace || (!el.hidden && !opts.push) });
      panelFocus = app.panel.focused(); // another issue from inside the panel keeps it
      setTimeout(paintFocus);
      app.panel.key = key; el.hidden = false; el.dataset.key = key;
      document.body.classList.add('has-panel');
      app.panel.cleanup && app.panel.cleanup(); app.panel.cleanup = null;
      clear(el).append(h('div.loading', T('Loading %s…', key)));
      try {
        const m = await import('./views/issue.js');
        if (app.panel.key !== key) return;
        clear(el);
        app.panel.cleanup = m.mountIssue(el, key, { app, full: false, card: opts.card }) || null;
      } catch (e) { if (reloadOnce(e)) return; clear(el).append(h('div.empty', T('Issue panel not available: %s', e.message))); }
      bus.emit('panel', { key });
    },
    close(opts = {}) {
      if (opts.url !== false && current) app.setQuery({ issue: null }, { replace: !!opts.replace });
      const el = $('#panel'); app.panel.cleanup && app.panel.cleanup(); app.panel.cleanup = null;
      app.panel.key = null; el.hidden = true; clear(el); document.body.classList.remove('has-panel');
      panelFocus = false; paintFocus();
      if (viewTitle) document.title = viewTitle;
      bus.emit('panel', { key: null });
    },
    // Whether the panel has the focus. Kept while the focus falls to nowhere (the focused node re-rendered or
    // hidden away, an input blurred), so its keys don't drop through to the view; lost to a focus or press elsewhere.
    focused() {
      const el = $('#panel'), a = document.activeElement;
      if (el.hidden) return false;
      return !a || a === document.body || a.closest('.overlay') ? panelFocus : el.contains(a);
    },
  },
  // Lazy entry points owned by the editing module (views/fields.js, views/create.js).
  actions: {
    edit: (key, field, anchor) => import('./views/fields.js').then(m => m.editField(app, key, field, anchor)),       // field: status|priority|assignee|points|labels|summary|…
    transition: key => import('./views/fields.js').then(m => m.editField(app, key, 'status')),
    create: opts => import('./views/create.js').then(m => m.openCreate(app, opts || {})),                          // {project, parent, type, summary, description, cloneOf}
    palette: (mode) => import('./views/palette.js').then(m => m.openPalette(app, mode)),                            // mode: '' | ':' commands | '/' search | 'g' goto
    jump: () => import('./views/palette.js').then(m => m.openPalette(app, 'g')),
    bulk: keysArr => import('./views/bulk.js').then(m => m.openBulk(app, keysArr)),
  },
  // Prefs are small per-site settings kept on the server store (and mirrored locally for instant reads).
  prefs: {
    data: {},
    get: (k, d) => (k in app.prefs.data ? app.prefs.data[k] : d),
    set(k, v) { app.prefs.data[k] = v; api.put('/prefs/' + encodeURIComponent(k), { Value: String(v) }).catch(() => {}); if (GLOBAL_PREF.test(k)) { try { localStorage.setItem('lw:p:' + k, String(v)); } catch (e) { /* ignore */ } } else store.set('p:' + k, v); },
  },
};
window.laneway = app;

// ---- router
const compile = r => {
  const names = [];
  const re = new RegExp('^' + r.path.replace(/\/:(\w+)(\?)?/g, (_, n, opt) => { names.push(n); return opt ? '(?:/([^/?]+))?' : '/([^/?]+)'; }) + '/?$');
  return { ...r, re, names };
};
const table = routes.map(compile);
let current = null, viewToken = 0, viewTitle = '';
// A module that fails to load after a binary upgrade (old page, new files): reload once to get a matching set.
function reloadOnce(e) {
  try {
    const at = +sessionStorage.getItem('lw:reloaded') || 0;
    if (Date.now() - at < 30000) return false;
    sessionStorage.setItem('lw:reloaded', String(Date.now()));
  } catch (x) { return false; }
  console.warn('reloading after a failed import', e);
  location.reload(); return true;
}

// The panel as the URL has it: open on ?issue=, closed without.
function syncPanel(key) {
  if (key) { if (app.panel.key !== key) app.panel.open(key, { url: false }); } else if (app.panel.key) app.panel.close({ url: false });
}

// navigate mounts the view of the URL; one that differs from the shown only in ?issue just moves the panel.
// force remounts the same (reload).
async function navigate({ force = false } = {}) {
  const tidy = nav.tidy(here(), app.session.home && app.session.home.Start ? '/home' : '/board');
  if (tidy !== here()) record(tidy);
  shown = here();
  const { path, query } = nav.split(here());
  if (!force && current && current.path === path && nav.sameBut(app.route.query, query, 'issue')) {
    app.route.query = query;
    syncPanel(query.issue);
    return;
  }
  let hit = null, params = {};
  for (const r of table) { const m = path.match(r.re); if (m) { hit = r; r.names.forEach((n, i) => { if (m[i + 1]) params[n] = decodeURIComponent(m[i + 1]); }); break; } }
  const view = $('#view');
  const token = ++viewToken;
  if (current && current.cleanup) { try { current.cleanup(); } catch (e) { console.error(e); } }
  if (current && current.scope) current.scope.dispose();
  current = null; app.marked = app.listed = app.menuItems = null;
  $('#viewbar').classList.toggle('hold', !!hit && hit.bar !== false);
  clear($('#toolbar')); clear($('#context'));
  if (!hit) {
    app.route = { name: '', params: {}, query: {} };
    viewTitle = T('Not found · laneway'); document.title = viewTitle;
    app.chrome.mark('');
    clear(view).append(h('div.empty', h('h2', T('Nothing at %s', path)), h('p', h('a', { href: '/board' }, T('Back to the board')))));
    bus.emit('route', app.route);
    return;
  }
  current = { name: hit.name, path, scope: keys.scope(hit.name) };
  app.route = { name: hit.name, params, query };
  syncPanel(query.issue); // beside the view while it loads
  viewTitle = T('%s · laneway', nav.routeTitle(hit)); document.title = viewTitle;
  app.chrome.mark(hit.name);
  clear(view).append(h('div.loading', T('Loading…')));
  let mod;
  try { mod = await hit.load(); } catch (e) {
    if (token !== viewToken) return;
    if (reloadOnce(e)) return;
    clear(view).append(h('div.empty', h('h2', nav.routeTitle(hit)), h('p', T('This view is not built yet.')), h('pre.dim', String(e.message))));
    return;
  }
  if (token !== viewToken) return;
  clear(view);
  try {
    current.cleanup = await (mod.default || mod.mount)(view, { app, params, query: app.route.query, scope: current.scope, context: $('#context'), toolbar: $('#toolbar') });
  } catch (e) { console.error(e); clear(view).append(h('div.empty', h('h2', T('Something broke')), h('pre', e.stack || e.message))); }
  if (token === viewToken) $('#viewbar').classList.remove('hold');
  bus.emit('route', app.route);
}

// ---- global keys
function globalKeys() {
  const g = keys.scope('global');
  g.bind(':', () => app.actions.palette(':'), T('command palette'), { group: 'Global', bar: T('commands') });
  g.bind('ctrl+k', () => app.actions.palette(':'), T('command palette'), { group: 'Global', input: true, hidden: true });
  g.bind('/', () => app.actions.palette('/'), T('search issues'), { group: 'Global', bar: T('search') });
  g.bind('g g', () => app.actions.jump(), T('jump to issue by key'), { group: 'Global' });
  g.bind('?', () => import('./views/help.js').then(m => m.openHelp(app)), T('show keys'), { group: 'Global', bar: T('keys') });
  g.bind('alt+p', () => import('./views/plan_ctx.js').then(m => m.openProject(app)), T('switch project (its last board)'), { group: 'Global' });
  g.bind('n', () => app.actions.create({ project: app.route && app.route.params.project }), T('new issue'), { group: 'Global' });
  g.bind('Q', () => app.actions.palette('#'), T('JQL search'), { group: 'Global' });
  g.bind('ctrl+e', () => import('./views/refine.js').then(m => m.startRefine(app)), T('refine: the view\'s issues one at a time'), { group: 'Global' });
  g.bind('g t', () => { const t = theme.next(); ui.toast(T('Theme: %s', t)); }, T('next theme'), { group: 'Global' });
  g.bind('Tab', () => { const p = $('#panel'); if (app.panel.focused()) { $('#view').focus(); } else { p.tabIndex = -1; p.focus(); } }, T('focus panel / view'), { group: 'Global', when: () => app.panel.key });
  g.bind('Escape', () => { if (app.panel.key) app.panel.close(); }, T('close panel'), { group: 'Global', hidden: true });
  for (const r of routes) if (r.key) g.bind('g ' + r.key, () => app.go('/' + r.name), T('go to %s', nav.routeTitle(r).toLowerCase()), { group: 'Go' });
  g.bind(['g ,', ','], () => app.go('/settings'), T('go to settings'), { group: 'Go' });
  // The TUI's own view keys; a view's key of the same name wins there (board O sorts, My work W switches day/week).
  g.bind('W', () => app.go('/work?tab=day'), T("today's worklogs"), { group: 'Go' });
  g.bind('O', () => app.go('/work?tab=issues'), T('my work, every project'), { group: 'Go' });
  g.bind('I', () => app.go('/inbox'), T('go to inbox'), { group: 'Go' });
  g.bind('U', () => app.go('/standup'), T('go to standup'), { group: 'Go' });
  if (routes.some(r => r.name === 'agents')) g.bind('ctrl+g', () => app.go('/agents'), T('go to agents'), { group: 'Go' });
  for (const r of routes) commands.register({ id: 'go:' + r.name, title: T('Go to %s', nav.routeTitle(r)), group: 'Go', run: () => app.go('/' + r.name) });
  commands.register({ id: 'theme:next', title: T('Theme: next'), group: 'Theme', run: () => ui.toast(T('Theme: %s', theme.next())) });
  for (const p of theme.presets) commands.register({ id: 'theme:' + p.id, title: T('Theme: %s', p.name), group: 'Theme', run: () => theme.set(p.id) });
  commands.register({ id: 'refine', title: T('Refine the view\'s issues one at a time'), group: 'Issue', run: () => import('./views/refine.js').then(m => m.startRefine(app)) });
  commands.register({ id: 'create', title: T('Create issue'), group: 'Issue', run: () => app.actions.create({}) });
  // A view with its own project switch (planning, reports…) lists that one instead.
  commands.register({ id: 'project', title: T('Switch project (its last board)'), group: 'Go', when: () => !keys.screen().some(b => b.id.endsWith(':alt+p')), run: () => import('./views/plan_ctx.js').then(m => m.openProject(app)) });
  commands.register({ id: 'reload', title: T('Reload data (drop caches)'), group: 'App', run: () => { api.forget(); navigate({ force: true }); ui.toast(T('Reloaded')); } });
  commands.register({ id: 'help', title: T('Keyboard help'), group: 'App', run: () => import('./views/help.js').then(m => m.openHelp(app)) });
  commands.register({ id: 'messages', group: 'App', get title() { return T('Messages: the last %d (enter copies one)', ui.messages.length); }, run: openMessages });
}

function chrome() {
  chromeBars.install(app);
  const sb = $('#search-btn');
  sb.addEventListener('click', () => app.actions.palette('/'));
  // index.html's own text, in ui.language.
  sb.setAttribute('aria-label', T('Search issues'));
  sb.title = T('Search (/) · commands (:) · jump to a key (g g)');
  sb.querySelector('.pal-text').textContent = T('Search');
  $('a.skip').textContent = T('Skip to content');
  $('#nav').setAttribute('aria-label', T('Views'));
  $('#viewbar').setAttribute('aria-label', T('View controls'));
  // An in-app link is an app.go: no step for the view shown, the panel along. Modified clicks stay the browser's.
  document.addEventListener('click', e => {
    if (e.defaultPrevented || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
    const a = e.target.closest && e.target.closest('a[href^="/"]'), href = a && a.getAttribute('href');
    if (!a || a.target || a.hasAttribute('download') || !nameOf(nav.split(href).path)) return; // /api files load as files
    e.preventDefault(); app.go(href);
  });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) bus.emit('focus'); });
}

// The toasts so far, newest first; enter copies one whole (TUI: the palette's messages row).
async function openMessages() {
  if (!ui.messages.length) return ui.toast(T('No messages yet'));
  const pad = n => String(n).padStart(2, '0');
  const at = d => pad(d.getHours()) + ':' + pad(d.getMinutes()) + ':' + pad(d.getSeconds());
  const m = await ui.pick({ title: T('Messages · enter copies one'), items: [...ui.messages].reverse(), label: e => (e.err ? '✗ ' : '') + e.text, detail: e => at(e.at), placeholder: T('Filter messages…') });
  if (m) navigator.clipboard.writeText(m.text).then(() => ui.toast(T('Copied the message')), () => ui.toast(T('Could not copy'), { kind: 'err' }));
}

async function boot() {
  // A view without the bar lets it go now, not once the session is in: the page would jump up.
  const first = table.find(r => r.re.test(nav.split(location.hash.startsWith('#/') ? location.hash.slice(1) : here()).path));
  if (first && first.bar === false) $('#viewbar').classList.remove('hold');
  let session;
  try { session = await api.get('/session'); } catch (e) { session = e; }
  if (session && session.setup) { // no Jira site yet: only the setup screen
    $('#top').classList.remove('boot');
    import('./views/setup.js').then(m => m.mountSetup($('#view'), session.setup));
    return;
  }
  // A laneway web shared by many people runs no agents, rules or GitLab on its machine: no nav, keys or commands for them.
  if (session.hosted) for (let i = routes.length; i--;) if (['agents', 'mr', 'mrs', 'rules'].includes(routes[i].name)) routes.splice(i, 1);
  chrome(); globalKeys();
  if (!session.hosted) import('./lib/agents.js').then(m => m.install(app)).catch(e => console.error('agents', e));
  import('./views/plan_cmds.js').then(m => m.register(app));
  import('./lib/pwa.js').then(m => m.install(app)).catch(e => console.error('pwa', e));
  try {
    if (session instanceof Error) throw session;
    app.session = session;
    theme.setDefault(session.ui && session.ui.Theme);
    api.setSite(app.session.site); store.setSite(app.session.site);
    api.swr('/emoji/table', emojiTable);
    const p = await api.get('/prefs'); app.prefs.data = p || {};
    onMetrics(kind => { bus.emit(kind); bus.emit('metrics', kind); });
    theme.attach(app.prefs); theme.fonts.attach(app.prefs); theme.fonts.refreshFiles(api).catch(() => {});
    keys.configure({ conf: session.ui.Keys || {}, web: session.ui.WebKeys || {} });
  } catch (e) { $('#top').classList.remove('boot'); $('#viewbar').classList.remove('hold'); clear($('#view')).append(h('div.empty', h('h2', T('Cannot reach Jira')), h('pre', e.message))); return; }
  import('./lib/keybar.js').then(m => m.install(app)).catch(e => console.error('keybar', e));
  import('./lib/timer.js').then(m => m.install(app)).catch(e => console.error('timer', e));
  import('./lib/tools.js').then(m => m.install(app)).catch(e => console.error('tools', e));
  import('./lib/account.js').then(m => m.install(app)).catch(e => console.error('account', e));
  import('./lib/sites.js').then(m => m.install(app)).catch(e => console.error('sites', e)).finally(() => $('#top').classList.remove('boot'));
  api.get('/update').then(u => { // as the TUI's ↑ v1.2: a newer release, copy its command or open its page
    if (!u.Tag) return;
    const desk = window.__lanewayDesktop; // the desktop app updates itself
    const up = () => desk ? desk.update() : (u.Command ? navigator.clipboard.writeText(u.Command).then(() => ui.toast(T('Copied %s', u.Command)), () => ui.toast(u.Command)) : window.open(u.Page, '_blank', 'noopener'));
    const what = desk ? T('update and restart') : u.Command || T('open the release page');
    $('#site').append(h('button.site-chip', { type: 'button', title: T('laneway %s is out · %s', u.Tag, desk ? T('click updates the app') : u.Command ? T('click copies %s', u.Command) : T('click opens the release page')), onclick: up }, '↑ ' + u.Tag));
    commands.register({ id: 'update', title: T('Update laneway to %s: %s', u.Tag, what), group: 'App', run: up });
  }).catch(() => {});
  if (app.session.hosted) commands.register({ id: 'signout', title: T('Sign out of laneway'), group: 'App', run: async () => { // the gateway's, which ends the sign-in on laneway-server too
    await fetch('/auth/logout', { method: 'POST' }).catch(() => {});
    location.reload();
  } });
  api.get('/branch').then(b => { if (b.Key) commands.register({ id: 'branch', title: '⎇ ' + b.Key + '  ' + (b.Summary || ''), group: 'Branch', run: () => app.panel.open(b.Key) }); }).catch(() => {}); // the cwd's, as the TUI: first in the palette
  $('.brand').title = 'laneway · ' + app.session.baseURL + (app.session.demo ? ' (demo)' : '');
  if (app.session.demo) $('#site').append(h('span.demo-badge', { title: T('Demo data, no Jira behind it') }, T('demo')));
  await import('./views/plan_ctx.js').then(m => m.sanitize(app)).catch(e => console.warn('ctx', e));
  if (lw()) depth = lw().d; else history.replaceState({ ...(history.state || {}), lw: { d: 0 } }, '');
  if (location.hash.startsWith('#/')) history.replaceState(history.state, '', location.hash.slice(1)); // an old #/ link or bookmark
  window.addEventListener('popstate', onURL);
  window.addEventListener('hashchange', onURL); // a #/ link typed or pasted
  await navigate();
  (window.requestIdleCallback || setTimeout)(() => import('./views/palette.js')); // ready before the first ':'
}
boot();
