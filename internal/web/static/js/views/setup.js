// First start: no Jira site yet. Asks for the site, email and API token the
// way `laneway setup` does, with the steps to make a token; the server signs
// in, writes the config and comes back as the app, so the page reloads.
import { h, $, clear } from '../lib/dom.js';
import * as api from '../lib/api.js';
import { T } from '../lib/i18n.js';

export function mountSetup(el, st) {
  document.title = T('Connect laneway to Jira');
  $('#app').classList.add('setup-mode');
  const again = !!st.site;
  const errs = {};
  const field = (name, label, input, ...hint) => {
    errs[name] = h('div.form-err', { id: 'su-err-' + name, role: 'alert' });
    input.setAttribute('aria-describedby', 'su-err-' + name);
    return h('div.su-field', h('label.su-label', { for: input.id }, label), ...hint, input, errs[name]);
  };
  const site = h('input.input#su-site', { name: 'site', value: st.site || '', placeholder: 'acme.atlassian.net', autocomplete: 'url', spellcheck: false, autocapitalize: 'off' });
  const email = h('input.input#su-email', { name: 'email', type: 'email', value: st.email || '', placeholder: 'you@example.com', autocomplete: 'email' });
  const token = h('input.input.mono#su-token', { name: 'token', type: 'password', autocomplete: 'off', spellcheck: false, 'data-1p-ignore': true, 'data-bwignore': true, 'data-lpignore': 'true', // laneway keeps it, not the password manager
    placeholder: st.envToken ? T('empty: uses JIRA_API_TOKEN') : T('paste it here') });
  const show = h('button.btn.ghost.su-show', { type: 'button', onclick: () => { const on = token.type === 'password'; token.type = on ? 'text' : 'password'; show.textContent = on ? T('Hide') : T('Show'); } }, T('Show'));
  const keyring = st.keyring ? h('input#su-keyring', { type: 'checkbox', checked: true }) : null;
  const atLogin = st.autostart ? h('input#su-login', { type: 'checkbox', checked: st.autostart.enabled }) : null;
  const connect = h('button.btn.primary.su-go', { type: 'submit' }, T('Connect'));
  const status = h('div.su-status', { role: 'status', 'aria-live': 'polite' });
  const tokenLink = h('a', { href: st.tokenURL, target: '_blank', rel: 'noopener' }, T("Atlassian's API token page"));

  const form = h('form.su-form', { novalidate: true, onsubmit: e => { e.preventDefault(); send(false); } },
    field('site', T('Your Jira address'), site,
      h('p.su-hint', T('The address you open Jira at in the browser, like https://acme.atlassian.net. Just acme works too.'))),
    field('email', T('Your email'), email,
      h('p.su-hint', T('The email address you sign in to Jira with.'))),
    field('token', T('An API token'), h('div.su-token', token, show),
      h('p.su-hint', T('A password for laneway only. You can delete it at any time; your own password stays as it is.')),
      h('ol.su-steps',
        h('li', T('Open '), tokenLink, T(' (it opens in a new tab) and sign in if it asks.')),
        h('li', T('Click '), h('b', T('Create API token')), T('. Name it '), h('b', 'laneway'), T(', pick how long it lasts, and click '), h('b', T('Create')), '.'),
        h('li', T('Click '), h('b', T('Copy')), T(', come back to this tab and paste it below.'))),
      st.envToken ? h('p.su-hint', T('JIRA_API_TOKEN is set: leave this empty to use it.')) : null),
    keyring ? h('label.check.su-keyring', keyring, T("Keep the token in this computer's keyring instead of in the config file")) : null,
    atLogin ? h('label.check.su-keyring', atLogin, T('Start laneway when I log in, so this page is always there (Settings turns it off)')) : null,
    h('div.su-actions', connect, status));

  // A laneway-server signs in through itself: no email or token here.
  let laneway = false;
  const tokenParts = [email.closest('.su-field'), token.closest('.su-field'), keyring && keyring.parentElement].filter(Boolean);
  const lanewayBox = h('div.su-laneway', { hidden: true });
  form.insertBefore(lanewayBox, form.querySelector('.su-actions'));
  const setMode = on => {
    laneway = on;
    tokenParts.forEach(x => { x.hidden = on; });
    lanewayBox.hidden = !on;
    lanewayBox.replaceChildren(h('p.su-hint', T('This is a laneway-server: you sign in on its own page, as in the browser, and laneway keeps that sign-in. No token needed.')));
    connect.textContent = on ? T('Sign in with laneway') : T('Connect');
  };
  let probed = '';
  const probe = async () => {
    const v = site.value.trim();
    if (!v || v === probed) return;
    probed = v;
    try { setMode(!!(await api.post('/setup/probe', { Site: v })).laneway); } catch (e) { setMode(false); }
  };
  site.addEventListener('change', probe);
  if (site.value) probe();

  clear(el).append(h('div.su-wrap', h('div.su-card',
    h('h2', again ? T('Sign in to Jira again') : T('Connect laneway to Jira')),
    h('p.su-intro.dim', again
      ? T('The config has this site, but not everything it needs to sign in. Fill in what is missing.')
      : T('laneway shows your Jira boards and works on them as you. Tell it where your Jira is and give it a token to sign in with. Works with Jira Cloud.')),
    form,
    again ? null : h('p.su-demo.dim', T('Just looking? '), h('button.btn.link', { type: 'button', onclick: () => send(true) }, T('Try the demo')), T(' on a made-up board first; nothing to set up.')),
    h('p.su-foot.faint', T('Saved on this computer in '), h('code', st.configPath || T('the config file')), T('. In a terminal, '), h('code', 'laneway setup'), T(' does the same.')))));
  (site.value ? email.value ? token : email : site).focus();

  let busy = false;
  async function send(demo) {
    if (busy) return;
    if (!demo && laneway) return signInLaneway();
    busy = true; connect.disabled = true;
    for (const k in errs) errs[k].textContent = '';
    status.className = 'su-status dim';
    status.textContent = demo ? T('Starting the demo…') : T('Signing in to Jira…');
    try {
      const r = await api.post('/setup', demo ? { Demo: true } : { Site: site.value, Email: email.value, Token: token.value, Keyring: !!(keyring && keyring.checked), Autostart: !!(atLogin && atLogin.checked) });
      status.className = 'su-status ok';
      status.textContent = demo ? T('Opening the demo…') : T('Signed in as %s. Opening your boards…', r.who);
      if (r.autostartError) { status.textContent += T(' Starting at login did not work: %s', r.autostartError); await new Promise(res => setTimeout(res, 4000)); }
      await waitForApp();
    } catch (e) {
      busy = false; connect.disabled = false;
      status.textContent = '';
      const f = e.fields && Object.keys(e.fields)[0];
      if (f && errs[f]) { errs[f].textContent = e.fields[f]; ({ site, email, token })[f].focus(); } else { status.className = 'su-status err'; status.textContent = e.message; }
    }
  }

  // The sign-in happens on the laneway-server's own page: a code to check
  // there, then this page waits until it was allowed.
  async function signInLaneway() {
    busy = true; connect.disabled = true;
    errs.site.textContent = '';
    status.className = 'su-status dim';
    status.textContent = T('Asking laneway for a sign-in code…');
    try {
      const c = await api.post('/setup/laneway', { Site: site.value });
      lanewayBox.replaceChildren(
        h('p', T('Open '), h('a', { href: c.complete || c.uri, target: '_blank', rel: 'noopener' }, T("laneway's sign-in page")), T(' (a new tab), sign in, and allow laneway there.')),
        h('p', T('Check it shows this code: '), h('b.mono.su-code', c.code)));
      status.textContent = T('Waiting for you to allow it…');
      for (;;) {
        await new Promise(r => setTimeout(r, 2000));
        const r = await api.post('/setup', { Site: site.value, Laneway: true, Autostart: !!(atLogin && atLogin.checked) });
        if (r.pending) continue;
        status.className = 'su-status ok';
        status.textContent = T('Signed in as %s. Opening your boards…', r.who);
        if (r.autostartError) { status.textContent += T(' Starting at login did not work: %s', r.autostartError); await new Promise(res => setTimeout(res, 4000)); }
        await waitForApp();
        return;
      }
    } catch (e) {
      busy = false; connect.disabled = false;
      status.className = 'su-status err';
      status.textContent = (e.fields && Object.values(e.fields)[0]) || e.message;
    }
  }
}

// The server swaps itself for the app; reload once it answers as that.
async function waitForApp() {
  for (let i = 0; i < 100; i++) {
    await new Promise(r => setTimeout(r, 300));
    try {
      const res = await fetch('/api/session');
      if (res.ok && !(await res.json()).setup) { location.reload(); return; }
    } catch (e) { /* restarting */ }
  }
  throw new Error(T('laneway did not come back; start it again.'));
}
