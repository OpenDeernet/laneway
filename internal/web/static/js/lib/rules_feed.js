// What the server's rules engine does, live: one event stream for the whole app.
// notify actions become toasts, and browser notifications when the user opted in; highlight marks the card ●
// until it is opened (app.highlights: key → colour, bus 'highlights'), as in the TUI.
import { notify, enabled, supported, permission, setEnabled } from './notify.js';
import { T } from './i18n.js';
import { stream } from './streams.js';

const subs = new Set();
let last = 0, asked = false;

export const onRuleEvent = fn => { subs.add(fn); return () => subs.delete(fn); };

export async function install(app) {
  app.highlights = new Map();
  app.bus.on('panel', ({ key }) => { if (key && app.highlights.delete(key)) app.bus.emit('highlights'); });
  let info;
  try { info = await app.api.get('/rules'); } catch (e) { return; }
  last = info.Last || 0;
  if (!info.Rules || !info.Rules.length) return;
  connect(app);
}

function connect(app) {
  stream('/api/rules/stream?since=' + last).on('message', m => {
    let ev; try { ev = JSON.parse(m.data); } catch (e) { return; }
    last = Math.max(last, ev.ID);
    for (const fn of subs) fn(ev);
    if (ev.Action === 'highlight') { if (ev.Key && ev.Key !== app.panel.key) { app.highlights.set(ev.Key, ev.Color || ''); app.bus.emit('highlights'); } return; }
    if (ev.Err) return app.ui.toast(T('Rule %s: %s', ev.Rule || '', ev.Err), { kind: 'err' });
    if (ev.Action !== 'notify') return;
    const title = ev.Title || 'laneway';
    if (!document.hidden) app.ui.toast(title + ': ' + ev.Text, { ms: 6000 });
    if (!notify(title, ev.Text, () => ev.Key && app.panel.open(ev.Key), 'rule:' + ev.ID)) optIn(app);
  });
}

// Once per page: offer browser notifications for notify actions.
function optIn(app) {
  if (asked || enabled() || !supported() || permission() === 'denied') return;
  asked = true;
  app.ui.toast(T('Rules want to notify you. Allow browser notifications?'), { ms: 12000, action: { label: T('Enable'), run: () => setEnabled(true).then(on => app.ui.toast(on ? T('Notifications on') : T('Notifications not allowed'))) } });
}
