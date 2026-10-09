// Who is signed in, at the app bar's right: your avatar; a click opens the account menu (lib/ctxmenu.js): settings,
// keys, the Jira site; on laneway-server your sign-in and passkeys, tokens and apps, and its admin when you
// administer the site or a project (GET /api/account); Sign out on a laneway web shared by many people.
import { h, $ } from './dom.js';
import { avatar } from './badge.js';
import { T } from './i18n.js';

export function install(app) {
  const me = app.session.me || {}, name = me.DisplayName || '';
  const btn = $('#me');
  if (!btn || !name) return;
  let acct = { Laneway: false };
  const ext = url => () => window.open(url, '_blank', 'noopener');
  const has = id => app.commands.list().some(c => c.id === id);

  const paint = m => btn.replaceChildren(avatar(name, m.AvatarURL, 24));
  paint(me);
  app.bus.on('me', paint); // a new avatar (Settings > Account)
  btn.title = T('%s · account', name);
  btn.setAttribute('aria-label', btn.title);
  app.api.swr('/account', a => {
    acct = a || acct;
    if (acct.SignInPage) app.commands.register({ id: 'account:signin', title: T('My sign-in and passkeys on laneway-server'), group: 'App', run: ext(acct.SignInPage) });
    if (acct.AdminPage) app.commands.register({ id: 'account:admin', title: T('laneway-server admin'), group: 'App', run: ext(acct.AdminPage) });
  });

  function items() {
    const out = [
      { label: T('Settings'), hint: ',', run: () => app.go('/settings') },
      { label: T('Keyboard help'), hint: '?', run: () => app.commands.run('help') },
    ];
    if (has('site')) out.push({ label: T('Switch Jira site'), hint: '@', run: () => app.commands.run('site') });
    if (acct.Laneway) out.push('-',
      { label: T('My sign-in and passkeys'), hint: '↗', run: ext(acct.SignInPage) },
      { label: T('API tokens and apps'), run: () => app.go('/settings?row=tokens') },
      acct.AdminPage && { label: T('Server admin'), hint: '↗', run: ext(acct.AdminPage) });
    if (app.session.hosted) out.push('-', { label: T('Sign out of laneway'), run: () => app.commands.run('signout') });
    return out.filter(Boolean);
  }
  const head = () => h('div.ctx-head', { role: 'none' }, avatar(name, app.session.me.AvatarURL, 32),
    h('div.ctx-who', h('div.ctx-name', name), me.Email && h('div.faint', me.Email), h('div.faint', host(app.session.baseURL))));

  // A press on the button while its menu is open only closes it (the menu's own outside press does).
  let wasOpen = false;
  addEventListener('pointerdown', e => { if (btn.contains(e.target)) wasOpen = !!document.querySelector('.ctx .ctx-head'); }, true);
  btn.addEventListener('click', () => {
    if (wasOpen) { wasOpen = false; return; }
    const r = btn.getBoundingClientRect();
    import('./ctxmenu.js').then(m => m.ctxMenu(items(), r.left, r.bottom + 4, { head: head() }));
  });
}

const host = u => { try { return new URL(u).host; } catch (e) { return u || ''; } };
