// Settings > Jira site > API tokens and apps: on laneway-server the account's own API tokens (label, scopes, expiry,
// last use; revoke one or all, make one whose secret shows once) and the apps it signed in with (revoke; the laneway
// app's own grant marked). An app's sign-in can't make tokens: the server's own page can. On Jira: Atlassian's page.
import { h } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { ago, shortDate, isZero } from '../lib/fmt.js';
import { T } from '../lib/i18n.js';

export function tokenOptions(app, refresh) {
  let info = null, err = '', made = null, page = '';
  const load = () => app.api.get('/site/tokens', { fresh: true }).then(r => { info = r; err = ''; }, e => { err = e.message; }).finally(refresh);
  load();
  const scopes = l => ((l || []).length ? l.map(s => h('span.chip', s)) : h('span.chip', T('everything')));
  const when = x => [isZero(x.Expires) ? null : h('span.faint', T('expires %s', shortDate(x.Expires))),
    h('span.faint', isZero(x.LastUsed) ? T('never used') : T('used %s', ago(x.LastUsed)))];
  const link = (href, text) => h('a', { href, target: '_blank', rel: 'noopener' }, text);

  async function revoke(t) {
    if (!await app.ui.confirm({ title: T('Revoke %s', t.Label), text: T('Whatever signs in with it stops working.'), ok: T('Revoke'), danger: true })) return;
    try { await app.api.del('/site/tokens/' + encodeURIComponent(t.ID)); } catch (e) { return app.ui.errToast(e); }
    app.ui.toast(T('Revoked %s', t.Label), { kind: 'ok' });
    load();
  }
  async function revokeAll() {
    if (!await app.ui.confirm({ title: T('Revoke all tokens'), text: T('Whatever signs in with one stops working, laneway too when it does.'), ok: T('Revoke all'), danger: true })) return;
    try { await app.api.del('/site/tokens'); } catch (e) { return app.ui.errToast(e); }
    made = null;
    app.ui.toast(T('Revoked every token'), { kind: 'ok' });
    load();
  }
  async function signOut(g) {
    if (!await app.ui.confirm({ title: T('Revoke %s', g.App), text: g.Current ? T('This app signs out; it asks to be let in again.') : T('It has to be let in again.'), ok: T('Revoke'), danger: true })) return;
    try { await app.api.del('/site/apps/' + encodeURIComponent(g.ID)); } catch (e) { return app.ui.errToast(e); }
    app.ui.toast(T('Revoked %s', g.App), { kind: 'ok' });
    load();
  }
  function make() {
    css('setup');
    const errs = { label: h('div.form-err', { role: 'alert' }), days: h('div.form-err', { role: 'alert' }) };
    const label = h('input.input', { placeholder: T('what it is for'), spellcheck: false, 'aria-label': T('Label') });
    const days = h('input.input', { type: 'number', min: 1, placeholder: T('empty: the site’s default'), 'aria-label': T('Days it lives') });
    const ok = h('button.btn.primary', { type: 'submit' }, T('Make'));
    const m = app.ui.modal(h('form.su-form', { novalidate: true, onsubmit: e => { e.preventDefault(); send(); } },
      h('div.su-field', h('label.su-label', T('Label')), label, errs.label),
      h('div.su-field', h('label.su-label', T('Days it lives')), days, errs.days),
      h('div.su-actions', ok)), { title: T('Make a token') });
    label.focus();
    async function send() {
      ok.disabled = true;
      errs.label.textContent = errs.days.textContent = '';
      try {
        const r = await app.api.post('/site/tokens', { Label: label.value, Days: +days.value || 0 });
        m.close();
        if (r.Page) { page = r.Page; made = null; } else { made = r; page = ''; }
        load();
      } catch (e) {
        ok.disabled = false;
        const k = e.fields && Object.keys(e.fields)[0];
        if (k && errs[k]) errs[k].textContent = e.fields[k]; else app.ui.errToast(e);
      }
    }
  }
  async function copy() {
    try { await navigator.clipboard.writeText(made.Secret); app.ui.toast(T('Copied')); } catch (e) { app.ui.toast(T('Could not copy'), { kind: 'err' }); }
  }

  function render() {
    if (err) return h('div.st-tok', h('span.st-err', err));
    if (!info) return h('div.st-tok', h('span.faint', T('Loading…')));
    if (!info.Laneway) return h('span.st-val', T('Jira keeps them on '), link(info.TokenURL, T('Atlassian\'s API token page')));
    const toks = info.Tokens || [], apps = info.Apps || [];
    return h('div.st-tok',
      h('div.st-tok-h', T('API tokens')),
      toks.length ? toks.map(t => h('div.st-tok-row', { role: 'group', 'aria-label': t.Label },
        h('span.st-tok-name', t.Label || t.ID), scopes(t.Scopes), ...when(t), h('span.spacer'),
        h('button.btn.ghost', { onclick: () => revoke(t) }, T('Revoke')))) : h('div.faint', T('none')),
      made && h('div.st-tok-new', { role: 'status' },
        h('div', T('%s: copy it now, it shows only once.', made.Token.Label)),
        h('div.row', h('code.st-tok-secret', made.Secret), h('button.btn', { onclick: copy }, T('Copy')))),
      page && h('div.st-tok-new', { role: 'status' }, T('This sign-in can’t make tokens. Make one on '), link(page, T('the server’s own page')), '.'),
      h('div.row', h('button.btn', { onclick: make }, T('Make a token')), toks.length > 0 && h('button.btn.ghost', { onclick: revokeAll }, T('Revoke all'))),
      h('div.st-tok-h', T('Signed-in apps')),
      apps.length ? apps.map(g => h('div.st-tok-row', { role: 'group', 'aria-label': g.App },
        h('span.st-tok-name', g.App), g.Current && h('span.chip.st-tok-self', T('this app')), scopes(g.Scopes), ...when(g), h('span.spacer'),
        h('button.btn.ghost', { onclick: () => signOut(g) }, T('Revoke')))) : h('div.faint', T('none')));
  }
  return [{ name: T('API tokens and apps'), section: 'Jira site', wide: true,
    desc: T('your tokens for scripts and the apps you signed in with; revoke what you no longer use'),
    render, change: () => { if (info && info.Laneway) make(); else if (info) window.open(info.TokenURL, '_blank', 'noopener'); } }];
}
