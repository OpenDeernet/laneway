// Settings > Account: you on laneway-server (GET /api/me): your avatar (upload or remove), how it mails you, your time
// zone and the language of its mail, the browsers signed in as you (sign out everywhere), and the server's page for
// password, two-step and passkeys. Mail and sessions an app's sign-in can't reach yet come as null: no row. On Jira only
// where Jira keeps your profile. A new avatar goes out on the bus as 'me' (the account button redraws).
import { h } from '../lib/dom.js';
import { avatar } from '../lib/badge.js';
import { ago } from '../lib/fmt.js';
import { T } from '../lib/i18n.js';

const MAIL = ['off', 'instant', 'hourly', 'daily'];
const LOCALES = ['en_US', 'nl_NL'];

export function accountOptions(app, m, refresh) {
  // Sessions past the first few behind a button: every sign-in makes one.
  const SESSIONS_SHOWN = 5;
  let allSessions = false;
  const link = (href, text) => h('a', { href, target: '_blank', rel: 'noopener' }, text);
  if (!m.Laneway) return [{ name: T('Profile'), section: 'Account', desc: T('your name, picture and time zone are kept by Jira'),
    render: () => h('span.st-val', link(m.ProfilePage, T('Open your Jira profile'))), change: () => window.open(m.ProfilePage, '_blank', 'noopener') }];

  const save = async (path, body, said) => {
    try { await app.api.put(path, body); } catch (e) { app.ui.errToast(e); return false; }
    app.ui.toast(said, { kind: 'ok' }); refresh(); return true;
  };

  // ---- avatar
  const file = h('input', { type: 'file', accept: 'image/png,image/jpeg,image/svg+xml', hidden: true, onchange: () => { if (file.files[0]) upload(file.files[0]); file.value = ''; } });
  const pick = () => file.click();
  const mine = me => { m.Me = me; app.session.me = { ...app.session.me, ...me }; app.bus.emit('me', app.session.me); refresh(); };
  async function upload(f) {
    const fd = new FormData(); fd.append('file', f, f.name || 'avatar');
    try {
      const res = await fetch('/api/me/avatar', { method: 'POST', body: fd });
      const out = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(out.error || res.statusText);
      m.Uploaded = true; mine(out);
      app.ui.toast(T('Your avatar changed'), { kind: 'ok' });
    } catch (e) { app.ui.errToast(e); }
  }
  async function remove() {
    let me;
    try { me = await app.api.del('/me/avatar'); } catch (e) { return app.ui.errToast(e); }
    m.Uploaded = false; mine(me);
    app.ui.toast(T('Your avatar is the generated one again'), { kind: 'ok' });
  }

  // ---- mail: off, or on at once / hourly / daily
  const n = m.Notifications;
  const mail = () => (n.Email ? n.Mode || 'instant' : 'off');
  const mailName = v => ({ off: T('off'), instant: T('at once'), hourly: T('hourly digest'), daily: T('daily digest') }[v]);
  async function setMail(v) {
    const was = { ...n };
    n.Email = v !== 'off'; if (v !== 'off') n.Mode = v;
    if (!await save('/me/notifications', { Email: n.Email, Mode: n.Mode || 'instant' }, T('Mail: %s', mailName(v)))) { Object.assign(n, was); refresh(); }
  }
  const step = (list, cur, d) => list[(Math.max(0, list.indexOf(cur)) + d + list.length) % list.length];

  // ---- time zone: any the browser knows, its own first
  const here = Intl.DateTimeFormat().resolvedOptions().timeZone;
  async function setZone(z) { if (z && z !== m.TimeZone && await save('/me/timezone', { Value: z }, T('Time zone: %s', z))) { m.TimeZone = z; refresh(); } }
  async function pickZone() {
    const all = (Intl.supportedValuesOf ? Intl.supportedValuesOf('timeZone') : [here]).filter(z => z !== here);
    setZone(await app.ui.pick({ title: T('Time zone'), items: [here, ...all], label: z => z, detail: z => (z === here ? T('this browser') : z === m.TimeZone ? T('current') : ''), placeholder: T('Time zone…') }));
  }

  // ---- mail language
  const locales = LOCALES.includes(m.Locale) ? LOCALES : [...LOCALES, m.Locale];
  const localeName = l => ({ en_US: 'English', nl_NL: 'Nederlands' }[l] || l);
  async function setLocale(l) { if (await save('/me/locale', { Value: l }, T('Mail language: %s', localeName(l)))) { m.Locale = l; refresh(); } }

  // ---- sessions: everywhere out, this laneway too when it signed in as an app (hosted)
  async function endAll() {
    if (!await app.ui.confirm({ title: T('Sign out everywhere'), text: T('Every browser and app signed in as you has to sign in again, maybe this one too.'), ok: T('Sign out everywhere'), danger: true })) return;
    try { await app.api.del('/me/sessions'); } catch (e) { return app.ui.errToast(e); }
    location.reload();
  }

  return [
    { name: T('Avatar'), section: 'Account', desc: T('PNG, JPEG or SVG up to 1 MB; without one the server draws your initials'),
      render: () => h('span.st-val', file, avatar(m.Me.DisplayName, m.Me.AvatarURL, 32),
        h('button.btn.ghost', { tabindex: -1, onclick: pick }, T('Upload…')),
        m.Uploaded && h('button.btn.ghost', { tabindex: -1, onclick: remove }, T('Remove'))),
      change: pick, reset: () => { if (m.Uploaded) remove(); } },
    n && { name: T('Email'), section: 'Account', desc: T('what the server mails you about issues: at once, or gathered hourly or daily'),
      render: () => h('span.st-val', h('button.btn.ghost', { tabindex: -1, onclick: () => setMail(step(MAIL, mail(), 1)) }, mailName(mail()))),
      change: d => setMail(step(MAIL, mail(), d)) },
    { name: T('Time zone'), section: 'Account', desc: T('digests go out by it'),
      render: () => h('span.st-val', h('span.mono', m.TimeZone || 'UTC'),
        h('button.btn.ghost', { tabindex: -1, onclick: pickZone }, T('Change')),
        here && here !== m.TimeZone && h('button.btn.ghost', { tabindex: -1, onclick: () => setZone(here) }, T('Use %s', here))),
      change: pickZone },
    { name: T('Mail language'), section: 'Account', desc: T('the server\'s mail; laneway\'s own language is ui.language'),
      render: () => h('span.st-val', h('button.btn.ghost', { tabindex: -1, onclick: () => setLocale(step(locales, m.Locale, 1)) }, localeName(m.Locale))),
      change: d => setLocale(step(locales, m.Locale, d)) },
    m.Sessions && { name: T('Sessions'), section: 'Account', wide: true, desc: T('browsers signed in to the server as you'),
      render: () => {
        const shown = allSessions ? m.Sessions : m.Sessions.slice(0, SESSIONS_SHOWN), more = m.Sessions.length - shown.length;
        return h('div.st-tok',
          m.Sessions.length ? shown.map(s => h('div.st-tok-row', { role: 'group', 'aria-label': s.Browser || s.IP || s.ID },
            h('span.st-tok-name', s.Browser || s.IP || T('unknown address')), s.Browser && s.IP && h('span.faint', s.IP), s.Method && h('span.chip', s.Method),
            h('span.faint', T('seen %s', ago(s.LastSeen))))) : h('div.faint', T('none')),
          more > 0 && h('button.btn.ghost', { onclick: () => { allSessions = true; refresh(); } }, T('+ %d more', more)),
          h('div.row', h('button.btn', { onclick: endAll }, T('Sign out everywhere'))));
      },
      change: endAll },
    { name: T('Password and two-step'), section: 'Account', desc: T('password, two-step verification and passkeys, on the server\'s own page'),
      render: () => h('span.st-val', link(m.SignInPage, T('Open my sign-in'))), change: () => window.open(m.SignInPage, '_blank', 'noopener') },
  ].filter(Boolean);
}
