// Writes that never reached Jira wait on the server (TUI: ⇡3). A header chip counts them;
// click or the palette lists them: send now, or drop one. The server retries on its own.
import { h, clear } from './dom.js';
import { icon } from './icons.js';
import { css } from './css.js';
import { ago } from './fmt.js';
import { T, Tn } from './i18n.js';

export function install(app) {
  css('ask');
  const { api, ui, bus } = app;
  let list = [], timer = 0, seenFail = -1;
  const chip = app.chrome.add(h('button.queue-chip.warn', { hidden: true, onclick: () => open() }), 10);

  function paint() {
    chip.hidden = !list.length;
    chip.replaceChildren(icon('cloud-upload'), ' ' + list.length);
    chip.title = Tn(list.length, '%d write waiting to reach Jira', '%d writes waiting to reach Jira', list.length);
  }
  async function poll() {
    let next;
    let q;
    try { q = await api.get('/queue', { fresh: true }); } catch (e) { return; }
    next = q.Items || [];
    const fails = q.Failed || [];
    if (seenFail < 0) seenFail = Math.max(0, ...fails.map(f => f.ID));
    for (const f of fails) if (f.ID > seenFail) { seenFail = f.ID; ui.toast(T('Queued write refused, dropped: %s: %s', f.What, f.Error), { kind: 'err' }); }
    const gone = list.filter(w => !next.some(n => n.ID === w.ID));
    list = next; paint();
    if (gone.length) for (const k of new Set(gone.map(w => w.Key).filter(Boolean))) bus.emit('issue:changed', { key: k });
    if (gone.length && !next.length) ui.toast(T('Back online: queued writes sent'));
  }
  const arm = () => { clearInterval(timer); timer = document.hidden ? 0 : setInterval(poll, 20000); };
  document.addEventListener('visibilitychange', () => { arm(); if (!document.hidden) poll(); });
  window.addEventListener('online', poll);
  window.addEventListener('lw:queued', () => { ui.toast(T('Offline: kept for later, sent when the site is back')); poll(); });
  poll(); arm();

  async function open() {
    await poll();
    if (!list.length) return ui.toast(T('Nothing waiting to reach the site'));
    const body = h('div.queue');
    const m = ui.modal(body, { title: T('Offline writes'), wide: true });
    const draw = () => {
      clear(body);
      if (!list.length) return m.close();
      body.append(
        h('ul.queue-list', list.map(w => h('li', h('b', w.What), ' ', h('span.dim', w.Method + ' ' + w.Key), h('span.spacer'), h('span.dim', ago(w.At)),
          h('button.btn.ghost.sm', { title: T('Drop this write'), onclick: async () => {
            if (!await ui.confirm({ title: T('Drop write'), text: T('%s on %s will never be sent.', w.What, w.Key), ok: T('Drop'), danger: true })) return;
            try { await api.del('/queue/' + w.ID); } catch (e) { return ui.errToast(e); }
            await poll(); draw();
          } }, T('Drop'))))),
        h('div.row.end',
          h('button.btn', { onclick: () => send(false) }, T('Retry')),
          h('button.btn.primary', { title: T('Also over changes made on the site since'), onclick: () => send(true) }, T('Send now'))));
    };
    async function send(force) {
      let r;
      try { r = await api.post('/queue/send', { Force: force }); } catch (e) { return ui.errToast(e); }
      for (const f of r.Failed || []) ui.toast(T('Refused, dropped: %s', f), { kind: 'err' });
      if (r.Conflict) ui.toast(T('%s changed on the site since you queued a write. Send now goes over it.', r.Conflict), { kind: 'err' });
      else if (r.Error) ui.toast(r.Offline ? T('Still offline') : r.Error, { kind: 'err' });
      else if (r.Sent) ui.toast(Tn(r.Sent, 'Sent %d queued write', 'Sent %d queued writes', r.Sent));
      await poll(); draw();
    }
    draw();
  }
  app.commands.register({ id: 'queue', title: T('Offline writes: send or drop'), group: 'App', run: open, when: () => list.length > 0 });
}
