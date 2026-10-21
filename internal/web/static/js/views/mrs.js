// Merge requests waiting on you, across GitLab instances and projects, Jira key or not (TUI: alt+m): review
// asked, assigned, and yours with comments since you last opened them (GET /api/gitlab/inbox, ui.MRInbox).
// Enter (or a click) opens one's page (views/mr.js), d on its changes; i shows the Jira issue it names beside,
// o opens it in GitLab, r reads them again.
import { h, clear, delegate, openURL } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { ago, isZero } from '../lib/fmt.js';
import { glyph, mrHref, diffHref, mrButtons, issueKeys } from '../lib/mr.js';
import { T, Tn } from '../lib/i18n.js';

export default function mount(el, { app, scope }) {
  css('agents'); css('dev');
  const { api, ui } = app;
  let rows = [], sel = 0, dead = false;
  const list = h('div.review-list'), note = h('div.empty');
  el.append(h('div.review', note, list));
  const keysOf = m => issueKeys(m, app.session && app.session.projects);

  function paint() {
    const kids = [];
    rows.forEach((r, i) => {
      const m = r.MR;
      if (!i || r.Group !== rows[i - 1].Group) kids.push(h('div.mr-group', r.Group));
      kids.push(h('div.rv-row.mr-row' + (i === sel ? '.sel' : ''), { dataset: { i } },
        h('div.mr-line', m.Checks ? glyph(m.Checks.Status) : h('span.dv-check'), h('span.mono', m.Repo + '!' + m.Number), h('span.sum', m.Title),
          keysOf(m).slice(0, 2).map(k => h('button.mr-key', { title: T('Show %s beside (i)', k), dataset: { key: k } }, k)),
          h('span.dim', [m.Draft && T('draft'), m.Author, m.Notes && Tn(m.Notes, '%d comment', '%d comments', m.Notes), !isZero(m.UpdatedAt) && ago(m.UpdatedAt)].filter(Boolean).join(' · ')),
          mrButtons(m.WebURL))));
    });
    list.replaceChildren(...kids);
    const s = list.querySelector('.sel'); if (s) s.scrollIntoView({ block: 'nearest' });
  }
  async function load() {
    note.hidden = false; note.textContent = T('Reading the merge requests waiting on you…'); clear(list);
    try {
      const r = await api.get('/gitlab/inbox', { fresh: true });
      if (dead) return;
      rows = r.Rows || []; sel = Math.min(sel, Math.max(rows.length - 1, 0));
      note.hidden = rows.length > 0 && !(r.Errs || []).length;
      note.textContent = !r.Configured ? T('No GitLab token: sign in with glab auth login --hostname <host>, or add a personal access token with scope api under gitlab: in the config.')
        : [rows.length ? '' : T('Nothing waits on you.'), ...(r.Errs || [])].filter(Boolean).join('\n');
      paint();
    } catch (e) { if (!dead) { note.hidden = false; note.textContent = e.message; } }
  }
  const cur = () => rows[sel] && rows[sel].MR;
  const move = d => { if (!rows.length) return; sel = Math.max(0, Math.min(rows.length - 1, sel + d)); paint(); };
  const G = T('Merge requests');
  scope.bind(['j', 'ArrowDown'], () => move(1), T('next'), { group: G });
  scope.bind(['k', 'ArrowUp'], () => move(-1), T('previous'), { group: G });
  scope.bind('Home', () => move(-rows.length), T('first'), { group: G });
  scope.bind('End', () => move(rows.length), T('last'), { group: G });
  scope.bind('PageDown', () => move(10), T('page down'), { group: G });
  scope.bind('PageUp', () => move(-10), T('page up'), { group: G });
  scope.bind('Enter', () => { if (cur()) app.go(mrHref(cur().WebURL)); }, T('open it: overview, pipeline, discussions'), { group: G, bar: T('open') });
  scope.bind('d', () => { if (cur()) app.go(diffHref(cur().WebURL)); }, T('its changes'), { group: G, bar: T('changes') });
  scope.bind('i', () => { const k = cur() && keysOf(cur())[0]; if (k) app.panel.open(k); else ui.toast(T('It names no Jira issue')); }, T('the Jira issue it names, beside'), { group: G, bar: T('issue') });
  scope.bind('o', () => cur() && openURL(cur().WebURL), T('open in GitLab'), { group: G, bar: 'GitLab', writes: true });
  scope.bind('r', load, T('refresh'), { group: G });
  delegate(list, 'click', '.mr-key', (e, t) => { e.stopPropagation(); app.panel.open(t.dataset.key); });
  delegate(list, 'click', '.mr-row', (e, t) => { if (e.target.closest('a,button')) return; sel = +t.dataset.i; app.go(mrHref(rows[sel].MR.WebURL)); });
  load();
  return () => { dead = true; };
}
