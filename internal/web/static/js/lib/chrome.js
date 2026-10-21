// App chrome. Two bars:
//   #top      the app bar, the same everywhere: brand + site, the views (folding into More when narrow),
//             the tray (sync, agents, timer: each shows only when it has something to say), search.
//   #viewbar  the view's own bar: ctx.context (where: project / board / sprint) and ctx.toolbar
//             (how: filters, modes). It hides itself while both are empty.
// Plus the chord hint: after `g` (or any chord prefix) a small card lists what can follow.
import { h, clear, $ } from './dom.js';
import { icon } from './icons.js';
import { kbd } from './keys.js';
import { routeTitle } from './nav.js';
import { T, Tn } from './i18n.js';

// Most used first: a narrow bar folds the tail into More. The open view always stays.
const PRIORITY = ['board', 'work', 'inbox', 'planning', 'reports', 'standup', 'roadmap', 'review', 'mrs', 'agents'];
const GROUPS = ['Project', 'You', 'Tools'];
const GROUP = { board: 'Project', planning: 'Project', reports: 'Project', roadmap: 'Project', work: 'You', inbox: 'You', standup: 'You', mrs: 'You' };
const groupName = g => ({ Project: T('Project'), You: T('You'), Tools: T('Tools') }[g] || g);
export const groupOf = r => r.group || GROUP[r.name] || 'Tools';
const rank = n => { const i = PRIORITY.indexOf(n); return i < 0 ? PRIORITY.length : i; };

export function install(app) {
  const { routes, keys } = app;
  const top = $('#top'), nav = $('#nav'), more = $('#more'), tray = $('#tray');
  const views = routes.filter(r => r.name !== 'issue');
  const inBar = views.filter(r => r.nav !== false).sort((a, b) => GROUPS.indexOf(groupOf(a)) - GROUPS.indexOf(groupOf(b)));
  for (const r of inBar) {
    nav.insertBefore(h('a', { href: '/' + r.name, dataset: { name: r.name, group: groupOf(r) }, title: routeTitle(r) + (r.key ? '  (g ' + r.key + ')' : '') }, routeTitle(r)), more);
  }
  const links = [...nav.querySelectorAll('a')];
  let current = '';

  // ---- fold: measure once per resize (one read pass, one write pass)
  let raf = 0;
  const refit = () => { cancelAnimationFrame(raf); raf = requestAnimationFrame(fit); };
  const say = (el, t) => { if (el.textContent !== t) el.textContent = t; }; // no-op writes would re-trigger the observer
  function fit() {
    for (const a of links) a.classList.remove('fold');
    say(more, T('More')); more.classList.remove('solo');
    const room = nav.clientWidth - more.offsetWidth;
    const w = new Map(links.map(a => [a, a.hidden ? 0 : a.offsetWidth + 2]));
    const cur = links.find(a => a.dataset.name === current);
    const keep = new Set(cur ? [cur] : []);
    let used = cur ? w.get(cur) : 0;
    if (used > room) {
      // Not even the open view fits beside More: one button, named after the view, opens the list.
      const r = routes.find(x => x.name === current);
      keep.clear(); say(more, r ? routeTitle(r) : T('Views')); more.classList.add('solo');
    } else for (const a of [...links].sort((x, y) => rank(x.dataset.name) - rank(y.dataset.name))) {
      if (a === cur || a.hidden) continue;
      if (used + w.get(a) + 12 > room) break;
      used += w.get(a); keep.add(a);
    }
    let prev = '';
    for (const a of links) {
      const on = keep.has(a) && !a.hidden;
      a.classList.toggle('fold', !on);
      a.classList.toggle('gap', on && !!prev && prev !== a.dataset.group);
      if (on) prev = a.dataset.group;
    }
    more.classList.toggle('dot', links.some(a => !keep.has(a) && a.querySelector('.nav-badge')));
  }
  new ResizeObserver(refit).observe(top);
  // Badges (inbox) and links an owner hides (agents without herdr) change what fits.
  new MutationObserver(refit).observe(nav, { childList: true, subtree: true, attributes: true, attributeFilter: ['hidden'] });

  function mark(name) {
    current = name;
    for (const a of links) {
      const on = a.dataset.name === name;
      a.classList.toggle('on', on);
      on ? a.setAttribute('aria-current', 'page') : a.removeAttribute('aria-current');
    }
    const r = routes.find(x => x.name === name);
    $('#vtitle').textContent = r ? routeTitle(r) : '';
    $('#viewbar').setAttribute('aria-label', r ? T('%s controls', routeTitle(r)) : T('View controls'));
    refit();
  }

  // ---- More: every view, grouped, with its key
  async function menu() {
    const shown = views.filter(r => { const a = links.find(l => l.dataset.name === r.name); return !a || !a.hidden; })
      .sort((a, b) => GROUPS.indexOf(groupOf(a)) - GROUPS.indexOf(groupOf(b)));
    const first = new Set(GROUPS.map(g => shown.find(r => groupOf(r) === g)));
    const badge = r => { const a = links.find(l => l.dataset.name === r.name); const b = a && a.querySelector('.nav-badge'); return b ? b.textContent : ''; };
    const r = await app.ui.pick({
      title: T('Go to'), items: shown, placeholder: T('View…'), label: r => routeTitle(r), detail: r => groupName(groupOf(r)),
      render: r => [h('span.pick-label', routeTitle(r), r.name === current && h('span.faint', T('  · here'))),
        badge(r) && h('span.nav-badge', badge(r)), h('span.pick-detail', first.has(r) ? groupName(groupOf(r)) : ''),
        r.key && h('span.menu-keys', kbd('g ' + r.key).map(k => h('kbd', k)))],
    });
    if (r) app.go('/' + r.name);
  }
  more.addEventListener('click', menu);

  // ---- tray: add(el, order) keeps items sorted; lower order sits further left
  function add(el, order = 50) {
    el.dataset.order = order;
    el.classList.add('ind');
    const next = [...tray.children].find(c => Number(c.dataset.order) > order);
    tray.insertBefore(el, next || null);
    return el;
  }

  // Offline: the browser says the network is gone. Writes queue on the server meanwhile (lib/offline.js).
  const off = add(h('span.warn', { hidden: true, role: 'status', title: T('Offline. You see what was loaded; writes wait until the site is back.') }, T('Offline')), 5);
  const net = () => { off.hidden = navigator.onLine; document.body.classList.toggle('is-offline', !navigator.onLine); };
  addEventListener('online', net); addEventListener('offline', net); net();

  // Agents on an issue that wait on you (herdr). Working ones only as a quiet count.
  const ag = add(h('a', { href: '/agents', hidden: true }), 20);
  app.bus.on('agents', s => {
    const as = ((s && s.Available && s.Agents) || []).filter(a => a.Key);
    const blocked = as.filter(a => a.Status === 'blocked').length, working = as.filter(a => a.Status === 'working').length;
    ag.hidden = !blocked && !working;
    ag.className = 'ind' + (blocked ? ' warn' : ' quiet');
    if (blocked) ag.replaceChildren(icon('hand'), ' ' + T('%d waiting', blocked)); else ag.textContent = Tn(working, '%d agent', '%d agents', working);
    ag.title = blocked ? Tn(blocked, '%d agent waits on you', '%d agents wait on you', blocked) + (working ? T(', %d working', working) : '') : Tn(working, '%d agent working', '%d agents working', working);
    ag.setAttribute('aria-label', ag.title);
  });

  // ---- the chord hint
  const wk = h('div#whichkey', { hidden: true, 'aria-hidden': 'true' });
  document.body.append(wk);
  let wkT = 0, wkFor = '';
  keys.onChange(() => {
    const p = keys.pending();
    clearTimeout(wkT);
    if (!p) { wk.hidden = true; wkFor = ''; return; }
    if (p !== wkFor) wkT = setTimeout(() => paintWK(p), wk.hidden ? 350 : 0);
  });
  function paintWK(p) {
    const pre = p + ' ', rows = new Map(), goTo = T('go to %s', '');
    for (const b of keys.active().sort((x, y) => (y.gid === 'Go') - (x.gid === 'Go'))) {
      if (b.rank === 1) continue;
      for (const s of b.specs || [b.spec]) if (s.startsWith(pre) && !rows.has(s.slice(pre.length))) rows.set(s.slice(pre.length), b.desc.startsWith(goTo) ? b.desc.slice(goTo.length) : b.desc);
    }
    if (!rows.size) return;
    wkFor = p;
    clear(wk).append(h('div.wk-head', kbd(p).map(k => h('kbd', k)), ' …'),
      h('dl' + (rows.size > 8 ? '.two' : ''), [...rows].map(([k, d]) => [h('dt', kbd(k).map(x => h('kbd', x))), h('dd', d)])));
    wk.hidden = false;
  }

  // ---- keys
  keys.onTyping(() => app.ui.toast(T('Typing outside a field: shortcuts paused')));
  const g = keys.scope('chrome');
  g.bind('M', menu, T('all views (More)'), { group: 'Go' });
  g.bind('g c', () => {
    const bar = $('#viewbar');
    const f = bar.querySelector('button:not([hidden]):not([disabled]), input, select, a[href]');
    if (f && bar.offsetParent) f.focus(); else app.ui.toast(T('This view has no controls'));
  }, T('focus the view bar (context, filters)'), { group: 'Go' });
  app.commands.register({ id: 'nav:menu', title: T('All views'), group: 'Go', run: menu });

  // ---- context switchers for ctx.context: crumb('Board (b)', pick) → button; label(btn, 'DEMO', 'Kanban') → "DEMO / Kanban"
  const crumb = (title, onclick) => h('button.crumb', { type: 'button', title, onclick });
  const label = (btn, ...parts) => {
    const out = [];
    for (const p of parts.filter(Boolean)) out.push(out.length ? h('span.sep', '/') : '', p);
    btn.replaceChildren(...out.filter(x => x !== ''));
    return btn;
  };

  app.chrome = { add, mark, menu, refit, crumb, label };
  return app.chrome;
}
