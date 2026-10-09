// Settings > Jira site: this site's projects (jira.projects, or sites.<name>.projects: first in the project picker,
// the first one opened at start), picked from the site's projects, and your API tokens and apps (settings_tokens.js);
// Server: restarting laneway web.
import { h } from '../lib/dom.js';
import { restart } from '../lib/sites.js';
import { tokenOptions } from './settings_tokens.js';
import { T } from '../lib/i18n.js';

export function siteOptions(app, refresh) {
  const s = app.session || {};
  const mine = () => s.projects || [];
  async function save(keys, quiet) {
    const before = mine();
    try { s.projects = (await app.api.put('/site/projects', { Projects: keys })).projects; } catch (e) { app.ui.errToast(e); return; }
    if (!quiet) app.ui.toast(T('Saved your projects'), { kind: 'ok', action: { label: T('Undo'), run: () => save(before, true) } });
    refresh();
  }
  async function edit() {
    let all;
    try { all = await app.api.get('/projects'); } catch (e) { return app.ui.errToast(e); }
    const byKey = new Map(all.map(p => [p.Key, p]));
    for (const k of mine()) if (!byKey.has(k)) byKey.set(k, { Key: k, Name: '' }); // gone or hidden: still untickable
    const items = [...byKey.values()];
    const r = await app.ui.pick({ title: T('Your projects'), items, multi: true, selected: mine().map(k => byKey.get(k)), label: p => p.Key + ' ' + p.Name, placeholder: T('Projects…') });
    if (r) save(r.map(p => p.Key));
  }
  const drop = k => save(mine().filter(x => x !== k));
  return [
    { name: 'Projects', section: 'Jira site',
      desc: T('your favourites: first in the project picker, the first one opens at start; jira.projects of this site'),
      render: () => h('span.st-val',
        mine().length ? mine().map(k => h('span.chip.st-proj', k, h('button.st-proj-x', { tabindex: -1, title: T('Remove %s', k), 'aria-label': T('Remove %s', k), onclick: e => { e.stopPropagation(); drop(k); } }, '×'))) : h('span.faint', T('none')),
        h('button.btn.ghost', { tabindex: -1, onclick: edit }, T('Edit'))),
      change: edit, reset: () => { if (mine().length) save([]); } },
    ...tokenOptions(app, refresh),
    { name: 'Restart', section: 'Server', advanced: true,
      desc: T('starts laneway web again: the config read anew, for options marked restart needed'),
      render: () => h('span.st-val', h('button.btn', { tabindex: -1, onclick: () => restart(app) }, T('Restart'))),
      change: () => restart(app) },
  ];
}
