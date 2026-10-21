// Your ui.actions from the config: in the palette and on their key. They run on the
// server with the issue on stdin and LANEWAY_KEY in the environment, as in the TUI.
import { h } from './dom.js';
import { css } from './css.js';
import { target } from './timer.js';
import { T } from './i18n.js';

export async function install(app) {
  css('ask');
  let list;
  try { list = await app.api.get('/actions'); } catch (e) { return; }
  if (!list.length) return;
  const scope = app.keys.scope('actions');
  const taken = new Set(app.keys.registry().flatMap(r => r.specs));
  // Where the keys are: the panel or the view. A modal (the palette) keeps the last one.
  const panel = () => !!(app.panel.key && app.panel.focused());
  const applies = a => a.Where === '' || a.Where === 'both' || (a.Where === 'panel') === panel();
  const keysOf = () => {
    const marked = app.marked ? app.marked() : [];
    if (marked.length && !panel()) return marked;
    const k = panel() ? app.panel.key : target(app);
    return k ? [k] : [];
  };
  async function run(a) {
    const ks = keysOf();
    if (!ks.length) return app.ui.toast(T('%s: no issue selected', a.Name));
    const done = app.ui.toast(T('Running %s on %s…', a.Name, ks.join(', ')), { ms: 60000 });
    let r;
    try { r = await app.api.post('/actions/' + a.ID + '/run', { Keys: ks }); } catch (e) { done(); return app.ui.errToast(e); }
    done();
    const last = (r.Output.split('\n').pop() || '').trim();
    if (r.Error) app.ui.toast(T('%s: %s %s', a.Name, r.Error, last), { kind: 'err' });
    else if (a.Show === 'pager' && r.Output) app.ui.modal(h('pre.action-out', r.Output), { title: a.Name, wide: true });
    else app.ui.toast(last ? T('%s: %s', a.Name, last) : T('%s done', a.Name));
    if (r.Refresh) for (const k of ks) app.bus.emit('issue:changed', { key: k });
  }
  for (const a of list) {
    app.commands.register({ id: 'action:' + a.ID, title: a.Name, group: 'Actions', run: () => run(a), when: () => applies(a) });
    if (!a.Key) continue;
    if (taken.has(a.Key)) { console.warn('ui.actions.' + a.Name + ': ' + a.Key + ' is taken here; the action stays in the palette'); continue; }
    taken.add(a.Key);
    scope.bind(a.Key, () => { if (applies(a)) run(a); }, a.Name, { group: 'Actions', writes: true });
  }
}
