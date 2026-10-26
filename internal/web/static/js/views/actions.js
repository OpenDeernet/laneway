// The issue actions menu (TUI: A). issueActions(app, st, hooks) opens a picker of what else can be done with
// the issue; st is the panel's state ({issue, card}), hooks {key, changed(what), open(key), upload(files), reloadExtras()}.
import { h } from '../lib/dom.js';
import { T, Tn } from '../lib/i18n.js';

const enc = encodeURIComponent;

export async function issueActions(app, st, hk, only) {
  const { api, ui } = app, key = hk.key, iss = st.issue;
  if (!iss) return;
  const project = key.slice(0, key.lastIndexOf('-'));
  const epic = /^epic$/i.test(iss.Type);
  const flagged = !!(st.card && st.card.Flagged);
  const demo = !!(app.session && app.session.demo);
  const repo = !demo && ((app.session && app.session.repos) || []).includes(project); // jira.repos has a checkout
  const herdr = !demo && !!(app.agents && app.agents.available);
  // As the TUI: type into, prompt and stop each of the issue's agents, and a new agent in each directory.
  const home = p => { const m = p && p.match(/^\/(?:home|Users)\/[^/]+/); return m ? '~' + p.slice(m[0].length) : p || ''; };
  const agents = herdr ? ((app.agents.stateFor(key) || {}).agents || []) : [];
  function agentItems() {
    const out = [], dirs = new Set();
    for (const a of agents) {
      out.push({ id: 'agent-type:' + a.PaneID, label: T('Type into agent %s (%s)', a.Name, a.Status) },
        { id: 'agent-prompt:' + a.PaneID, label: T('Send agent %s a prompt', a.Name) },
        { id: 'agent-stop:' + a.PaneID, label: T('Stop agent %s (closes its tab)', a.Name) });
      if (a.CWD && !dirs.has(a.CWD)) { dirs.add(a.CWD); out.push({ id: 'agent-new:' + a.PaneID, label: T('New agent in %s', home(a.CWD)) }); }
    }
    return out;
  }
  async function agentAction(what, pane) {
    const a = agents.find(x => x.PaneID === pane); if (!a) return;
    if (what === 'type') return app.go('/agents?agent=' + enc(key) + '&pane=' + enc(pane) + '&type=1');
    if (what === 'new') return app.agents.start(key, { another: true, path: a.CWD });
    if (what === 'prompt') {
      const text = await ui.prompt({ title: T('Prompt for %s', a.Name), multiline: true, placeholder: T('What should it do next?'), ok: T('Send') });
      if (text && text.trim()) { await api.post('/agents/' + pane + '/prompt', { Text: text }); ui.toast(T('Prompt sent')); }
      return;
    }
    if (!await ui.confirm({ title: T('Stop %s', a.Name), text: T('Stop the agent on %s? Its herdr tab closes.', key), ok: T('Stop'), danger: true })) return;
    await api.post('/agents/' + pane + '/stop'); ui.toast(T('Stopped %s', a.Name));
  }
  const items = [
    { id: epic ? 'child' : 'subtask', label: epic ? T('New issue in this epic') : T('New subtask') },
    { id: 'link', label: T('Link to another issue') },
    { id: 'weblink', label: T('Add a web link') },
    { id: 'clone', label: T('Clone') },
    { id: 'type', label: T('Change the issue type') },
    { id: 'move', label: T('Move to another project') },
    { id: 'estimate', label: T('Set the original estimate') },
    { id: 'deps', label: T('Dependency tree') },
    { id: 'delete', label: T('Delete the issue') },
    { id: 'watch', label: T('Watch / stop watching') },
    { id: 'watchers', label: T('Add or remove watchers') },
    { id: 'vote', label: T('Vote / take back the vote') },
    { id: 'flag', label: flagged ? T('Clear the flag') : T('Flag as an impediment') },
    { id: 'upload', label: T('Upload files') },
    { id: 'unlink', label: T('Remove a link'), skip: !(iss.Links || []).some(l => l.LinkID) },
    { id: 'delatt', label: T('Delete an attachment'), skip: !(iss.Attachments || []).length },
    { id: 'history', label: T('Time in each status / history') },
    ...agentItems(),
    { id: 'worktree-remove', label: T('Remove its worktree (merged, no uncommitted changes)'), skip: !(repo && herdr && iss.StatusCategory === 'done') },
    { id: 'pr', label: T('Open a pull request (draft)'), skip: !repo || demo },
  ].filter(x => !x.skip);
  const it = only ? { id: only } : await ui.pick({ title: T('Actions on %s', key), items, label: x => x.label, placeholder: T('Action…') });
  if (!it) return;
  try { await run(it.id); } catch (e) { ui.errToast(e); }

  async function run(id) {
    const [what, pane] = id.split(':');
    if (pane && what.startsWith('agent-')) return agentAction(what.slice(6), pane);
    switch (id) {
      case 'pr': return app.agents.draftPR(key);
      case 'worktree-remove': {
        const close = ui.toast(T('%s: removing its worktree…', key), { ms: 60000 });
        try { const r = await api.del('/issues/' + key + '/worktree'); close(); ui.toast(T('Removed %s (%s stays)', home(r.Path), r.Branch), { kind: 'ok' }); app.agents.refresh(); } catch (e) { close(); throw e; }
        return;
      }
      case 'subtask': return app.actions.create({ project, parent: key, subtask: true });
      case 'child': return app.actions.create({ project, parent: key });
      case 'link': return link();
      case 'weblink': {
        const url = await ui.prompt({ title: T('Link a web page'), placeholder: 'https://…', ok: T('Add') });
        if (!url || !url.trim()) return;
        await api.post('/issues/' + key + '/weblinks', { URL: url.trim(), Title: '' });
        return hk.reloadExtras();
      }
      case 'clone': {
        // The create form, filled from the copy (TUI openJiraClone); creating links it as a clone.
        const d = await api.get('/issues/' + key + '/clonedraft', { fresh: true });
        return app.actions.create({ project: d.Project, type: d.Type, summary: d.Summary, description: d.Description, descriptionDoc: d.DescriptionDoc, parent: d.Parent, cloneOf: key, note: d.Note });
      }
      case 'type': {
        const ts = await api.get('/issues/' + key + '/types?current=' + enc(iss.Type), { fresh: true });
        const t = await ui.pick({ title: T('%s is a %s: change to', key, iss.Type), items: ts, label: x => x.Name });
        if (!t) return;
        await api.post('/issues/' + key + '/type', { ID: t.ID });
        hk.changed(T('%s is now a %s', key, t.Name)); return ui.toast(T('%s is now a %s', key, t.Name), { kind: 'ok' });
      }
      case 'move': return move();
      case 'estimate': {
        const v = await ui.prompt({ title: T('Original estimate of %s', key), placeholder: '2d 4h', ok: T('Set') });
        if (!v || !v.trim()) return;
        const r = await api.put('/issues/' + key + '/field/estimate', { Text: v.trim() });
        const u = r && r.Undo;
        if (u) import('./fields.js').then(m => m.pushUndo(app, T('%s estimate', key), async () => { await api.put('/issues/' + key + '/field/estimate', u); app.bus.emit('issue:changed', { key }); }));
        hk.changed(T('%s estimate %s', key, v.trim())); return ui.toast(T('%s estimate %s', key, v.trim()), { kind: 'ok' });
      }
      case 'deps': return deps();
      case 'delete': {
        const n = (iss.Links || []).filter(l => l.Rel === 'subtask').length;
        const ok = await ui.confirm({ title: T('Delete %s', key), text: (n ? Tn(n, 'Delete %s %s and its subtask? This cannot be undone.', 'Delete %s %s and its %d subtasks? This cannot be undone.', key, iss.Summary, n) : T('Delete %s %s? This cannot be undone.', key, iss.Summary)), ok: T('Delete'), danger: true });
        if (!ok) return;
        await api.del('/issues/' + key + (n ? '?subtasks=1' : ''));
        app.bus.emit('issue:changed', { key, deleted: true });
        ui.toast(T('Deleted %s', key), { kind: 'ok' });
        return app.panel.close({ replace: true }); // not back to a deleted issue
      }
      case 'watch': {
        const r = await api.post('/issues/' + key + '/watch');
        return ui.toast(r.On ? T('Watching %s', key) : T('Stopped watching %s', key), { kind: 'ok' });
      }
      case 'watchers': return watchers();
      case 'vote': {
        const r = await api.post('/issues/' + key + '/vote');
        return ui.toast(r.On ? T('Voted for %s', key) : T('Vote taken back'), { kind: 'ok' });
      }
      case 'flag': await api.put('/issues/' + key + '/field/flag', { On: !flagged }); return hk.changed(flagged ? T('%s unflagged', key) : T('%s flagged', key));
      case 'upload': {
        const inp = h('input', { type: 'file', multiple: true, onchange: () => { if (inp.files.length) hk.upload([...inp.files]); } });
        return inp.click();
      }
      case 'unlink': {
        const l = await ui.pick({ title: T('Remove a link from %s', key), items: (iss.Links || []).filter(x => x.LinkID), label: x => x.Rel + ' ' + x.Key + ' ' + x.Summary });
        if (!l) return;
        await api.del('/issues/' + key + '/links/' + enc(l.LinkID)); return hk.changed();
      }
      case 'delatt': {
        const a = await ui.pick({ title: T('Delete an attachment'), items: iss.Attachments, label: x => x.Filename });
        if (!a || !await ui.confirm({ title: T('Delete attachment'), text: a.Filename, ok: T('Delete'), danger: true })) return;
        await api.del('/issues/' + key + '/attachments/' + enc(a.ID)); return hk.changed();
      }
      case 'history': return hk.tab('history');
    }
  }

  // What holds the issue up, through their own blockers, and what it holds up (TUI deps.go); a row opens its issue.
  async function deps() {
    const d = await api.get('/issues/' + key + '/deps', { fresh: true });
    const items = [{ n: d.Root, pre: '' }];
    let open = 0;
    const walk = (ns, indent) => ns.forEach((n, i) => {
      const last = i === ns.length - 1;
      if (!n.Done && !n.Seen) open++;
      items.push({ n, pre: indent + (last ? '└ ' : '├ ') });
      walk(n.Kids || [], indent + (last ? '  ' : '│ '));
    });
    const section = (title, ns) => { items.push({ head: ns.length ? title : T('%s: nothing', title) }); walk(ns, '  '); };
    section(T('held up by'), d.BlockedBy);
    const by = open;
    section(T('holds up'), d.Blocks);
    const label = x => x.head || x.pre + x.n.Key + ' ' + x.n.Summary + ' [' + x.n.Status + ']' + (x.n.Seen ? ' ↺' : x.n.Done ? ' ✓' : '');
    const r = await ui.pick({ title: T('Dependencies — %s', key) + (by ? ' · ' + Tn(by, 'held up by %d open issue', 'held up by %d open issues', by) : ''), items, label,
      render: x => (x.head ? h('span.faint', x.head) : h('span.pick-label', { style: 'white-space:pre' }, label(x))) });
    if (r && r.n && r.n.Key !== key) hk.open(r.n.Key);
  }

  async function link() {
    const types = await api.get('/linktypes');
    const opts = types.flatMap(t => [{ label: t.Outward, Type: t.Name, Outward: true }, ...(t.Inward !== t.Outward ? [{ label: t.Inward, Type: t.Name, Outward: false }] : [])]);
    const t = await ui.pick({ title: key + ' …', items: opts, label: o => o.label, placeholder: T('Link type…') });
    if (!t) return;
    const other = await ui.pick({ title: key + ' ' + t.label + ' …', items: [], label: c => c.Key + ' ' + c.Summary, placeholder: T('Find an issue or type its key…'), empty: T('Type to search'),
      create: q => ({ Key: q.trim().toUpperCase(), Summary: '' }),
      search: async q => (q.trim().length > 1 ? (await api.get('/find?q=' + enc(q.trim()) + '&project=' + enc(project))).cards.filter(c => c.Key !== key) : []) });
    if (!other) return;
    await api.post('/issues/' + key + '/links', { Type: t.Type, Other: other.Key, Outward: t.Outward });
    hk.changed();
  }

  async function move() {
    const ps = (await api.get('/projects/creatable')).filter(p => p.Key !== project);
    const p = await ui.pick({ title: T('Move %s to', key), items: ps, label: x => x.Key + '  ' + x.Name });
    if (!p) return;
    const ts = await api.get('/issues/' + key + '/movetypes?project=' + enc(p.Key) + '&current=' + enc(iss.Type), { fresh: true });
    const t = ts.find(x => x.Name.toLowerCase() === iss.Type.toLowerCase()) && ts.length === 1 ? ts[0] : await ui.pick({ title: T('Move %s to %s as', key, p.Key), items: ts, label: x => x.Name });
    if (!t) return;
    ui.toast(T('Moving %s…', key));
    const r = await api.post('/issues/' + key + '/move', { Project: p.Key, TypeID: t.ID });
    app.bus.emit('issue:changed', { key, moved: r.Key });
    ui.toast(T('%s is now %s', key, r.Key), { kind: 'ok', ms: 6000 });
    hk.open(r.Key);
  }

  async function watchers() {
    for (;;) {
      const ws = await api.get('/issues/' + key + '/watchers', { fresh: true });
      const on = new Set(ws.map(u => u.AccountID));
      const u = await ui.pick({ title: T('Watchers of %s (enter adds or removes)', key), items: ws, label: x => (on.has(x.AccountID) ? '✓ ' : '   ') + x.DisplayName,
        placeholder: T('Find a person…'), empty: T('No watchers'),
        search: async q => {
          const others = await api.get('/issues/' + key + '/viewusers?q=' + enc(q), { fresh: true });
          const names = q.toLowerCase();
          return [...ws.filter(x => x.DisplayName.toLowerCase().includes(names)), ...others.filter(x => !on.has(x.AccountID))];
        } });
      if (!u) return;
      await api.put('/issues/' + key + '/watchers/' + enc(u.AccountID), { Watch: !on.has(u.AccountID) });
      ui.toast(on.has(u.AccountID) ? T('Removed %s', u.DisplayName) : T('Added %s', u.DisplayName), { kind: 'ok' });
    }
  }
}
