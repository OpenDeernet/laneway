// Bulk edit: openBulk(app, keys) asks what to change, then applies it to every key a few
// at a time with progress, and lists the ones that failed.
import { h } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { pickUser, pickLabels, bulkUndo, undo } from './fields.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

css('forms');

const ACTIONS = [
  { id: 'status', label: T('Status') },
  { id: 'assignee', label: T('Assignee') },
  { id: 'priority', label: T('Priority') },
  { id: 'labels+', label: T('Add labels') },
  { id: 'labels-', label: T('Remove labels') },
  { id: 'sprint', label: T('Sprint / backlog') },
  { id: 'flag+', label: T('Flag') },
  { id: 'flag-', label: T('Remove flag') },
  { id: 'points', label: T('Story points') },
];

// What to change: {edit, what}, or null when cancelled.
async function ask(app, id, keys, project) {
  const n = keys.length;
  switch (id) {
    case 'status': {
      const ts = await app.api.get(`/projects/${project}/statuses`);
      const names = [...new Set(ts.flatMap(t => (t.Statuses || []).map(s => s.Name)))];
      const s = await app.ui.pick({ title: T('Move %d issues to', n), items: names, label: x => x });
      return s && { edit: { Field: 'status', To: s }, what: T('%d moved to %s', n, s) };
    }
    case 'assignee': {
      const u = await pickUser(app, { project, title: T('Assign %d issues', n), specials: true });
      return u && { edit: { Field: 'assignee', ID: u.AccountID }, what: u.none ? T('%d unassigned', n) : T('%d assigned to %s', n, u.DisplayName) };
    }
    case 'priority': {
      const list = await app.api.get('/priorities');
      const p = await app.ui.pick({ title: T('Priority of %d issues', n), items: list, label: x => x.Name });
      return p && { edit: { Field: 'priority', ID: p.ID }, what: T('%d priority → %s', n, p.Name) };
    }
    case 'labels+': {
      const ls = await pickLabels(app, project, [], T('Add labels to %d issues', n));
      return ls && ls.length ? { edit: { Field: 'labels', Add: ls }, what: T('%d %s', n, '+' + ls.join(' +')) } : null;
    }
    case 'labels-': {
      const issues = await Promise.all(keys.slice(0, 100).map(k => app.api.get('/issues/' + k).catch(() => null)));
      const have = [...new Set(issues.flatMap(i => (i && i.Labels) || []))];
      if (!have.length) { app.ui.toast(T('None of them has labels')); return null; }
      const ls = await app.ui.pick({ title: T('Remove labels from %d issues', n), items: have, multi: true, label: x => x });
      return ls && ls.length ? { edit: { Field: 'labels', Remove: ls }, what: T('%d %s', n, '-' + ls.join(' -')) } : null;
    }
    case 'sprint': {
      const s = await app.api.get(`/projects/${project}/sprints`);
      const o = await app.ui.pick({ title: T('Move %d issues to', n), items: [{ ID: 0, Name: T('Backlog') }, ...s.Sprints], label: x => x.Name, detail: x => x.State || '' });
      if (!o) return null;
      const q = { edit: { Field: 'sprint', Sprint: o.ID }, what: T('%d → %s', n, o.Name), patch: { field: 'sprint', value: o.Name, id: o.ID } };
      if (o.ID) return q;
      // Jira's backlog leaves done issues out: they would show nowhere in planning (as planning's m asks).
      const listed = new Map(((app.listed && app.listed()) || []).map(c => [c.Key, c.Done]));
      const unknown = keys.filter(k => !listed.has(k)).slice(0, 100);
      const got = await Promise.all(unknown.map(k => app.api.get('/issues/' + k).catch(() => null)));
      got.forEach((iss, i) => listed.set(unknown[i], !!iss && iss.StatusCategory === 'done'));
      const done = keys.filter(k => listed.get(k));
      if (!done.length) return q;
      const ok = await app.ui.confirm({
        title: T('Move to the backlog?'), ok: T('Move'),
        text: Tn(done.length, '%s is done. Jira leaves done issues out of the backlog: it shows nowhere in planning after.', '%s are done. Jira leaves done issues out of the backlog: they show nowhere in planning after.', done.join(', ')),
      });
      return ok ? { ...q, asked: true } : null;
    }
    case 'flag+': return { edit: { Field: 'flag', On: true }, what: T('%d flagged', n) };
    case 'flag-': return { edit: { Field: 'flag', On: false }, what: T('%d unflagged', n) };
    case 'points': {
      const v = await app.ui.prompt({ title: T('Story points for %d issues (empty clears)', n) });
      if (v === null) return null;
      if (v.trim() && isNaN(Number(v))) { app.ui.toast(T('Story points must be a number'), { kind: 'err' }); return null; }
      return { edit: { Field: 'points', Text: v.trim() }, what: T('%d points → %s', n, v.trim() || T('none')) };
    }
  }
  return null;
}

export async function openBulk(app, keys) {
  keys = [...new Set(keys || [])];
  if (!keys.length) return app.ui.toast(T('Mark issues with x first'));
  const project = keys[0].split('-')[0];
  try {
    const a = await app.ui.pick({ title: Tn(keys.length, 'Edit %d issue: %s', 'Edit %d issues: %s', keys.length, keys.slice(0, 4).join(', ') + (keys.length > 4 ? '…' : '')), items: ACTIONS, label: x => x.label });
    if (!a) return;
    const q = await ask(app, a.id, keys, project);
    if (!q) return;
    const list = keys.slice(0, 12).join(', ') + (keys.length > 12 ? T(' and %d more', keys.length - 12) : '');
    if (keys.length > 1 && !q.asked && !await app.ui.confirm({ title: T('Edit %d issues', keys.length), text: T('%s: %s', q.what, list), ok: T('Apply') })) return;
    return await run(app, keys, q);
  } catch (e) { app.ui.errToast(e); }
}

const CHUNK = 4;

async function run(app, keys, { edit, what, patch }) {
  let stop = false;
  const bar = h('progress', { max: keys.length, value: 0 });
  const label = h('div.dim', `0 / ${keys.length}`);
  const list = h('ul.bulk-failed');
  const closeBtn = h('button.btn', { type: 'button', onclick: () => m.close() }, T('Stop'), keyHint('Escape'));
  const m = app.ui.modal(h('div.bulk', bar, label, list, h('div.row.end', closeBtn)), { title: what, onClose: () => { stop = true; } });
  const res = { Done: [], Undo: {}, Failed: {} };
  let n = 0;
  for (let i = 0; i < keys.length && !stop; i += CHUNK) {
    const chunk = keys.slice(i, i + CHUNK);
    try {
      const r = await app.api.post('/bulk', { Keys: chunk, Edit: edit });
      res.Done.push(...r.Done); Object.assign(res.Undo, r.Undo); Object.assign(res.Failed, r.Failed);
      for (const k of r.Done) {
        if (patch) app.bus.emit('issue:patch', { key: k, ...patch });
        app.bus.emit('issue:changed', { key: k, what: `${k}: ${what}` });
      }
    } catch (e) { for (const k of chunk) res.Failed[k] = e.message; }
    n += chunk.length;
    bar.value = n; label.textContent = `${n} / ${keys.length}`;
    list.replaceChildren(...Object.entries(res.Failed).map(([k, msg]) => h('li', h('b', k), ' ', msg)));
  }
  const failed = Object.keys(res.Failed);
  const step = bulkUndo(app, what, res);
  if (!failed.length) {
    m.close();
    app.ui.toast(`${what}`, { kind: 'ok', action: step && { label: T('Undo'), run: () => undo(app, step) } });
  } else if (!stop) {
    label.textContent = T('%d done, %d failed', res.Done.length, failed.length);
    closeBtn.replaceChildren(T('Close'), keyHint('Escape')); closeBtn.focus();
  }
  return res;
}
