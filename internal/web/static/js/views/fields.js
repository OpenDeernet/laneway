// Editing an issue's fields: editField(app, key, field) opens the right picker or
// prompt, writes optimistically, toasts with Undo and pushes onto the session undo stack.
// Also the form widgets (fieldInput, formDialog) that create, bulk and the transition form share.
import { h } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { keys } from '../lib/keys.js';
import { T } from '../lib/i18n.js';
import { mdEdit } from '../lib/mdedit.js';

css('forms');

// ---- undo: a session stack of {what, run}; `u` takes the latest back.
const stack = [];
let scope = null;

export function installUndo(app) {
  if (scope) return;
  scope = keys.scope('undo');
  scope.bind('u', () => undo(app), T('undo last edit'), { group: T('Edit'), when: () => stack.length > 0 });
}

// pushUndo(app, what, run): run() is an async function that takes the change back.
// Anything that writes (a board drag, say) can push here so `u` covers it.
export function pushUndo(app, what, run) {
  installUndo(app);
  const step = { what, run };
  stack.push(step);
  if (stack.length > 50) stack.shift();
  return step;
}

// dropUndo takes a step off the stack unrun: what it would take back never happened.
export function dropUndo(step) {
  const i = stack.indexOf(step);
  if (i >= 0) stack.splice(i, 1);
}

export function undo(app, step) {
  step = step || stack[stack.length - 1];
  if (!step) { app.ui.toast(T('Nothing to undo')); return Promise.resolve(false); }
  const i = stack.indexOf(step);
  if (i >= 0) stack.splice(i, 1);
  return step.run().then(() => { app.ui.toast(T('Undid %s', step.what)); return true; }, e => { app.ui.errToast(e); return false; });
}

const undoAction = (app, step) => ({ label: T('Undo'), run: () => undo(app, step) });

// write: PUT one field. Views listening for 'issue:patch' {key, field, value} can show the
// change at once; everything refetches on 'issue:changed'. Resolves true on success.
async function write(app, key, field, body, what, patch) {
  app.bus.emit('issue:patch', { key, field, value: patch });
  try {
    const r = await app.api.put(`/issues/${key}/field/${field}`, body);
    app.bus.emit('issue:changed', { key, what });
    const u = r && r.Undo;
    const step = u && pushUndo(app, what, async () => {
      await app.api.put(`/issues/${key}/field/${u.Field}`, u);
      app.bus.emit('issue:changed', { key });
    });
    app.ui.toast(what, { kind: 'ok', action: step && undoAction(app, step) });
    return true;
  } catch (e) {
    app.ui.errToast(e);
    app.bus.emit('issue:changed', { key });
    return false;
  }
}

// bulkUndo pushes one step that reverts a bulk result's per-issue edits.
export function bulkUndo(app, what, res) {
  if (!res.Undo || !Object.keys(res.Undo).length) return null;
  return pushUndo(app, what, async () => {
    const r = await app.api.post('/bulk', { Each: res.Undo });
    for (const k of r.Done || []) {
      const u = res.Undo[k];
      if (u && u.Field === 'sprint') app.bus.emit('issue:patch', { key: k, field: 'sprint', id: u.Sprint });
      app.bus.emit('issue:changed', { key: k });
    }
    const bad = Object.keys(r.Failed || {});
    if (bad.length) throw new Error(T('%d not undone: %s', bad.length, bad.join(', ')));
  });
}

const projectOf = key => key.split('-')[0];
const issueOf = (app, key) => app.api.get('/issues/' + key);

// mustFill reports whether editmeta requires a field the panel edits with its own row (LW-357): the
// field configuration's, or a laneway field rule's; a read-only one isn't the panel's to refuse.
async function mustFill(app, key, id) {
  let m;
  try { m = await app.api.get(`/issues/${key}/editmeta`); } catch (e) { return false; } // unknown: Jira still judges the write
  if (id === 'points') id = m.PointsField;
  return !!id && (m.OwnRequired || []).includes(id);
}
const refuseEmpty = (app, name) => app.ui.toast(T('%s is required', name), { kind: 'err' });

// ---- entry point
const editors = { status, priority, assignee, reporter, points, labels, summary, duedate, issuetype, flag, sprint, delete: remove };

export async function editField(app, key, field, anchor) {
  installUndo(app);
  try {
    const f = editors[field === 'due' ? 'duedate' : field === 'type' ? 'issuetype' : field === 'estimate' ? 'points' : field];
    if (f) return await f(app, key, anchor);
    return await custom(app, key, field);
  } catch (e) { app.ui.errToast(e); }
}

async function priority(app, key) {
  const [iss, list] = await Promise.all([issueOf(app, key), app.api.get('/priorities')]);
  const p = await app.ui.pick({ title: T('%s priority', key), items: list, label: p => p.Name, detail: p => (p.ID === iss.PriorityID ? T('current') : ''), placeholder: T('Priority…') });
  if (!p || p.ID === iss.PriorityID) return;
  await write(app, key, 'priority', { ID: p.ID }, T('%s priority → %s', key, p.Name), p.Name);
}

async function assignee(app, key) {
  const u = await pickUser(app, { issue: key, title: T('%s assignee', key), specials: true });
  if (!u) return;
  if (u.none && await mustFill(app, key, 'assignee')) return refuseEmpty(app, T('Assignee'));
  await write(app, key, 'assignee', { ID: u.AccountID }, u.none ? T('%s unassigned', key) : T('%s → %s', key, u.DisplayName), u.none ? null : { AccountID: u.AccountID, DisplayName: u.DisplayName });
}

async function reporter(app, key) {
  const u = await pickUser(app, { issue: key, title: T('%s reporter', key) });
  if (!u) return;
  await write(app, key, 'reporter', { ID: u.AccountID }, T('%s reporter → %s', key, u.DisplayName), u.DisplayName);
}

async function points(app, key) {
  const iss = await issueOf(app, key);
  const v = await app.ui.prompt({ title: T('%s story points (empty clears)', key), value: iss.StoryPoints || '', placeholder: '5' });
  if (v === null || v.trim() === (iss.StoryPoints || '')) return;
  if (v.trim() && isNaN(Number(v))) return app.ui.toast(T('Story points must be a number'), { kind: 'err' });
  if (!v.trim() && await mustFill(app, key, 'points')) return refuseEmpty(app, T('Points'));
  await write(app, key, 'points', { Text: v.trim() }, T('%s points → %s', key, v.trim() || T('none')), v.trim());
}

async function summary(app, key) {
  const iss = await issueOf(app, key);
  const v = await app.ui.prompt({ title: T('%s summary', key), value: iss.Summary });
  if (v === null || !v.trim() || v.trim() === iss.Summary) return;
  await write(app, key, 'summary', { Text: v.trim() }, T('%s summary changed', key), v.trim());
}

async function duedate(app, key) {
  const m = await app.api.get(`/issues/${key}/editmeta`, { fresh: true });
  const v = await app.ui.prompt({ title: T('%s due date (2026-10-01, today, +3d, fri; empty clears)', key), value: m.Due || '' });
  if (v === null || v.trim() === (m.Due || '')) return;
  if (!v.trim() && (m.Fields || []).some(f => f.ID === 'duedate' && f.Required)) return app.ui.toast(T('%s is required', T('Due')), { kind: 'err' });
  await write(app, key, 'duedate', { Text: v.trim() }, T('%s due → %s', key, v.trim() || T('none')), v.trim());
}

async function labels(app, key) {
  const iss = await issueOf(app, key);
  const cur = iss.Labels || [];
  const next = await pickLabels(app, projectOf(key), cur, T('%s labels', key));
  if (!next) return;
  const Add = next.filter(l => !cur.includes(l)), Remove = cur.filter(l => !next.includes(l));
  if (!Add.length && !Remove.length) return;
  if (!next.length && await mustFill(app, key, 'labels')) return refuseEmpty(app, T('Labels'));
  await write(app, key, 'labels', { Add, Remove }, T('%s labels %s', key, [...Add.map(l => '+' + l), ...Remove.map(l => '-' + l)].join(' ')), next);
}

async function issuetype(app, key) {
  const [m, t] = await Promise.all([app.api.get(`/issues/${key}/editmeta`, { fresh: true }), app.api.get(`/projects/${projectOf(key)}/issuetypes`)]);
  const sub = (t.Subtasks || []).some(x => x.Name === m.Type);
  const list = (sub ? t.Subtasks : t.Types).filter(x => x.Name !== m.Type);
  if (!list.length) return app.ui.toast(T('No other issue type to change to'));
  const o = await app.ui.pick({ title: T('%s type (now %s)', key, m.Type), items: list, label: x => x.Name });
  if (o) await write(app, key, 'issuetype', { ID: o.ID }, T('%s type → %s', key, o.Name), o.Name);
}

async function flag(app, key) {
  const m = await app.api.get(`/issues/${key}/editmeta`, { fresh: true });
  if (m.Flagged === undefined) return app.ui.toast(T('This Jira has no Flagged field'), { kind: 'err' });
  await write(app, key, 'flag', { On: !m.Flagged }, m.Flagged ? T('%s unflagged', key) : T('%s flagged', key), !m.Flagged);
}

async function sprint(app, key) {
  const [m, s] = await Promise.all([app.api.get(`/issues/${key}/editmeta`, { fresh: true }), app.api.get(`/projects/${projectOf(key)}/sprints`)]);
  const items = [{ ID: 0, Name: T('Backlog') }, ...s.Sprints];
  const cur = m.Sprint && m.Sprint.ID ? m.Sprint.ID : 0;
  const o = await app.ui.pick({ title: T('%s sprint', key), items, label: x => x.Name, detail: x => (x.ID === cur ? T('current') : x.State || '') });
  if (!o || o.ID === cur) return;
  await write(app, key, 'sprint', { Sprint: o.ID }, o.ID ? T('%s → %s', key, o.Name) : T('%s → backlog', key), o.Name);
}

async function remove(app, key) {
  const iss = await issueOf(app, key);
  if (!(await app.ui.confirm({ title: T('Delete %s?', key), text: T('%s. This cannot be undone.', iss.Summary), ok: T('Delete'), danger: true }))) return;
  let sub = false;
  for (;;) {
    try {
      await app.api.del(`/issues/${key}${sub ? '?subtasks=1' : ''}`);
      break;
    } catch (e) {
      if (sub || !/subtask/i.test(e.message)) throw e;
      if (!(await app.ui.confirm({ title: T('%s has subtasks', key), text: T('Delete them with it?'), ok: T('Delete all'), danger: true }))) return;
      sub = true;
    }
  }
  if (app.panel.key === key) app.panel.close({ replace: true }); // not back to a deleted issue
  app.bus.emit('issue:changed', { key, deleted: true });
  app.ui.toast(T('%s deleted', key), { kind: 'ok' });
}

// Any other field id: the edit screen's own field, written from its kind.
async function custom(app, key, id) {
  const m = await app.api.get(`/issues/${key}/editmeta`, { fresh: true });
  const fm = (m.Fields || []).find(f => f.ID === id || f.Name.toLowerCase() === id.toLowerCase());
  if (!fm) return app.ui.toast(T('%s has no editable “%s”', key, id), { kind: 'err' });
  const required = fm.Required && !fm.ReadOnly; // the field configuration's: the form won't send it empty
  const w = fieldInput(app, fm, { issue: key, project: projectOf(key), value: (m.Values || {})[fm.ID], required });
  formDialog(app, {
    title: T('%s %s', key, fm.Name), rows: [{ label: fm.Name, required, widget: w }],
    submit: async () => {
      const r = await app.api.put(`/issues/${key}/field/${fm.ID}`, { Kind: fm.Kind, Value: w.get() });
      app.bus.emit('issue:changed', { key, what: T('%s %s changed', key, fm.Name) });
      const u = r && r.Undo;
      const step = u && pushUndo(app, T('%s %s', key, fm.Name), async () => { await app.api.put(`/issues/${key}/field/${u.Field}`, u); app.bus.emit('issue:changed', { key }); });
      app.ui.toast(T('%s %s changed', key, fm.Name), { kind: 'ok', action: step && undoAction(app, step) });
    },
  });
}

// ---- status: a picker of the moves on offer; a move the workflow wants fields for opens a form
async function status(app, key) {
  const metaP = app.api.get(`/issues/${key}/transitionmeta`, { fresh: true });
  metaP.catch(() => {});
  // Open at once; the moves fill in when Jira answers.
  const list = app.api.get(`/issues/${key}/transitions`, { fresh: true });
  const t = await app.ui.pick({ title: T('Move %s', key), items: list, label: t => t.Name, placeholder: T('Move to…'), empty: T('No moves from here') });
  if (t) await takeMove(app, key, t, metaP);
}

// takeMove takes move t of key: at once, or through its form when the workflow wants fields.
export async function takeMove(app, key, t, metaP) {
  installUndo(app);
  let meta;
  try { meta = ((await (metaP || app.api.get(`/issues/${key}/transitionmeta`, { fresh: true }))).Transitions || []).find(x => x.ID === t.ID); } catch (e) { meta = null; }
  if (!meta || !meta.NeedsInput) return write(app, key, 'status', { ID: t.ID }, T('%s → %s', key, t.Name), t.Name);
  moveForm(app, key, meta);
}

// setField writes one field as the editors do: the card shows it at once, u undoes it.
export function setField(app, key, field, body, what, patch) {
  installUndo(app);
  return write(app, key, field, body, what, patch);
}

export function moveForm(app, key, t) {
  const rows = t.Fields.map(f => ({ label: f.Name, base: f.Required && f.Kind !== 'other', required: f.Required && f.Kind !== 'other',
    widget: fieldInput(app, f, { issue: key, project: projectOf(key), value: f.Value, required: f.Required, onChange: () => reask() }) }));
  // laneway-server's form rules for the move as its fields change (LW-423); none on Jira.
  let seq = 0, timer = 0;
  function reask() {
    clearTimeout(timer);
    const my = ++seq;
    timer = setTimeout(async () => {
      const Fields = [];
      rows.forEach((r, i) => { const f = t.Fields[i]; if (f.Kind !== 'other' && !r.widget.empty()) Fields.push({ ID: f.ID, Kind: f.Kind, Value: r.widget.get() }); });
      let fx;
      try { fx = (await app.api.ask('/form', { Issue: key, Form: 'transition', Fields })).Fields || {}; } catch (e) { return; }
      if (my !== seq) return;
      rows.forEach((r, i) => {
        const e = fx[t.Fields[i].ID] || {}, [label, cell] = r.dom || [];
        r.hidden = !!e.hidden; r.required = r.base || !!e.required;
        if (label) { label.hidden = cell.hidden = r.hidden; label.classList.toggle('req', r.required); label.title = e.message || ''; }
      });
    }, 300);
  }
  setTimeout(reask);
  formDialog(app, {
    title: T('Move %s → %s', key, t.ToName), intro: t.Message, rows, ok: T('Move'), wide: t.Fields.some(f => f.Kind === 'doc' || f.Kind === 'comment'),
    submit: async () => {
      const out = [];
      rows.forEach((r, i) => { const f = t.Fields[i]; if (f.Kind !== 'other' && !r.hidden && r.widget.touched()) out.push({ ID: f.ID, Kind: f.Kind, Value: r.widget.get() }); });
      app.bus.emit('issue:patch', { key, field: 'status', value: t.ToName });
      let r;
      try { r = await app.api.post(`/issues/${key}/transitionwith`, { ID: t.ID, Fields: out }); } catch (e) { app.bus.emit('issue:changed', { key }); throw e; }
      app.bus.emit('issue:changed', { key, what: T('%s → %s', key, t.ToName) });
      const u = r && r.Undo;
      const step = u && pushUndo(app, T('%s → %s', key, t.ToName), async () => { await app.api.put(`/issues/${key}/field/status`, u); app.bus.emit('issue:changed', { key }); });
      app.ui.toast(T('%s → %s', key, t.ToName), { kind: 'ok', action: step && undoAction(app, step) });
    },
  });
}

// ---- pickers

// pickUser → Promise<{AccountID, DisplayName, me?, none?} | null>. `specials` adds Me and Unassigned.
export function pickUser(app, { issue, project, title = T('Person'), specials = false } = {}) {
  const me = app.session && app.session.me;
  const scopeQ = issue ? 'issue=' + encodeURIComponent(issue) : 'project=' + encodeURIComponent(project);
  const head = () => {
    if (!specials) return [];
    const out = [];
    if (me && me.AccountID) out.push({ AccountID: me.AccountID, DisplayName: me.DisplayName, me: true });
    out.push({ AccountID: '', DisplayName: T('Unassigned'), none: true });
    return out;
  };
  const search = async q => {
    const us = await app.api.get(`/users?${scopeQ}&q=${encodeURIComponent(q)}`, { fresh: true });
    return [...head(), ...us.filter(u => !(specials && me && u.AccountID === me.AccountID))];
  };
  return app.ui.pick({
    title, items: head(), search, placeholder: T('Search people…'),
    label: u => (u.me ? T('Me · %s', u.DisplayName) : u.DisplayName),
    render: u => h('span.pick-user', u.none ? app.ui.avatar(null) : app.ui.avatar(u.DisplayName), h('span.pick-label', u.me ? T('Me · %s', u.DisplayName) : u.DisplayName)),
  });
}

// pickLabels → Promise<string[] | null>: toggle suggestions, type to create a new one.
// pickLabels: the issue's and the project's recent labels, and Jira's as you type (TUI label_suggest.go);
// clause is a custom labels field's JQL name (cf[10050]).
export async function pickLabels(app, project, current = [], title = T('Labels'), clause = 'labels') {
  let recent = [];
  try { recent = (await app.api.get(`/projects/${project}/labels`)) || []; } catch (e) { /* suggestions are optional */ }
  const items = [...new Set([...current, ...recent])];
  const search = async q => {
    if (!q.trim()) return items;
    const found = await app.api.get('/labels?q=' + encodeURIComponent(q.trim()) + '&field=' + encodeURIComponent(clause)).catch(() => []);
    return [...new Set([...items, ...found])];
  };
  return app.ui.pick({ title, items, search, multi: true, selected: current, create: q => q.trim().replace(/\s+/g, '-'), placeholder: T('Filter or type a new label…'), empty: T('No labels yet: type one') });
}

// ---- form widgets

export function formRow(label, el, required) {
  return [h('label.form-label' + (required ? '.req' : ''), label), h('div.form-field', el)];
}

// formDialog({title, intro, rows:[{label, required, widget}], ok, submit}): a modal form. submit()
// may throw; its message shows in the form and the dialog stays. ctrl+Enter submits.
export function formDialog(app, { title, intro, rows, ok = T('Save'), submit, wide = false }) {
  const err = h('div.form-err', { role: 'alert' });
  const okBtn = h('button.btn.primary', { type: 'submit' }, ok);
  const form = h('form.form-dialog', { onsubmit: e => { e.preventDefault(); go(); } },
    intro && h('p.dim', intro),
    h('div.form', rows.map(r => (r.dom = formRow(r.label, r.widget.el, r.required)))), err,
    h('div.row.end', h('span.faint.form-hint', T('ctrl+⏎ saves')), h('button.btn', { type: 'button', onclick: () => m.close() }, T('Cancel')), okBtn));
  const m = app.ui.modal(form, { title, wide, onClose: () => rows.forEach(r => r.widget.dispose && r.widget.dispose()) });
  m.scope.bind('ctrl+Enter', () => go(), T('submit'), { input: true, hidden: true });
  const firstEmpty = rows.find(r => r.widget.empty());
  const target = firstEmpty && firstEmpty.widget.el.querySelector ? firstEmpty.widget.el : null;
  const focus = target && (firstEmpty.widget.focusEl || (target.matches('input,textarea,select,button') ? target : target.querySelector('input,textarea,select,button')));
  if (focus) focus.focus();
  async function go() {
    const missing = rows.filter(r => r.required && !r.hidden && r.widget.empty()).map(r => r.label);
    if (missing.length) { err.textContent = T('Fill in %s', missing.join(', ')); return; }
    okBtn.disabled = true; err.textContent = '';
    try { await submit(); m.close(); } catch (e) { err.textContent = e.message; okBtn.disabled = false; }
  }
  return m;
}

const HINT = { number: '5', date: '2026-10-01, today, +3d, fri', time: 'fri 14:00', issue: 'ABC-123' };

// fieldInput(app, fieldMeta, {project, issue, value, required}) → {el, get() → Value, empty(), touched()}.
// The Value has the shape the API encodes for fm.Kind (see jira.EncodeValue).
export function fieldInput(app, fm, { project, issue, value, required, onChange } = {}) {
  const v = value || {};
  let dirty = false;
  const touch = () => { dirty = true; if (onChange) onChange(); };
  const w = { el: null, touch, touched: () => dirty, get: () => ({}), empty: () => true };
  // A button-like control redrawn from state.
  const control = (render, choose) => {
    const box = h('div.field-btn');
    const draw = () => { box.replaceChildren(...[render()].flat().filter(Boolean), h('button.btn', { type: 'button', onclick: async () => { await choose(); touch(); draw(); } }, T('Choose…'))); };
    draw();
    return box;
  };
  switch (fm.Kind) {
    case 'text': case 'number': case 'date': case 'time': case 'issue': {
      const i = h('input.input', { type: 'text', value: v.Text || '', placeholder: HINT[fm.Kind] || '', oninput: touch });
      if (fm.Kind === 'number') i.setAttribute('inputmode', 'decimal');
      w.el = i; w.get = () => ({ Text: i.value }); w.empty = () => !i.value.trim();
      break;
    }
    case 'doc': case 'comment': {
      // As the comment composer: the document (Visual), or markdown with the people it mentions (sent as Users).
      const e = mdEdit(app, { doc: v.Doc || null, value: v.Text || '', rows: 4, issueKey: issue, project, noCancel: true, hint: T('@ mentions · / formats') });
      e.onInput(touch);
      w.el = e.el; w.focusEl = { focus: () => e.focus() }; w.dispose = e.dispose;
      w.get = () => { const c = e.current(); return c.doc ? { Doc: c.doc } : { Text: c.text, Users: c.mentions, Kept: c.kept }; }; w.empty = () => e.isEmpty();
      break;
    }
    case 'strings':
      if (fm.Clause) {
        let ls = (v.Text || '').split(/\s+/).filter(Boolean);
        w.el = control(() => [ls.length ? h('span.chips', ls.map(l => app.ui.chip(l))) : h('span.faint', T('none'))], async () => {
          const next = await pickLabels(app, project || projectOf(issue || ''), ls, fm.Name, fm.Clause);
          if (next) ls = next;
        });
        w.get = () => ({ Text: ls.join(' ') }); w.empty = () => !ls.length;
      } else {
        const i = h('input.input', { type: 'text', value: v.Text || '', placeholder: T('words, separated by spaces'), oninput: touch });
        w.el = i; w.get = () => ({ Text: i.value }); w.empty = () => !i.value.trim();
      }
      break;
    case 'user': {
      let u = (v.Users || [])[0] || null;
      w.el = control(() => [u ? h('span.pick-user', app.ui.avatar(u.DisplayName), u.DisplayName) : h('span.faint', T('none'))], async () => {
        const p = await pickUser(app, { issue, project, title: fm.Name, specials: true });
        if (p) u = p.none ? null : p;
      });
      w.get = () => ({ Users: u ? [{ AccountID: u.AccountID, DisplayName: u.DisplayName }] : [] }); w.empty = () => !u;
      break;
    }
    case 'users': {
      let us = [...(v.Users || [])];
      w.el = control(() => [us.length ? h('span.chips', us.map(u => app.ui.chip(u.DisplayName))) : h('span.faint', T('none'))], async () => {
        const p = await pickUser(app, { issue, project, title: fm.Name });
        if (p && !us.some(x => x.AccountID === p.AccountID)) us = [...us, p];
      });
      w.get = () => ({ Users: us.map(u => ({ AccountID: u.AccountID, DisplayName: u.DisplayName })) }); w.empty = () => !us.length;
      break;
    }
    case 'option': case 'sprint': {
      const s = h('select.input', { onchange: touch }, h('option', { value: '' }, '—'));
      const fill = opts => { for (const o of opts) s.append(h('option', { value: o.ID, selected: (v.Options || [])[0] && v.Options[0].ID === String(o.ID) }, o.Name)); };
      if (fm.Kind === 'sprint') {
        const p = project || projectOf(issue || '');
        app.api.get(`/projects/${p}/sprints`).then(r => fill(r.Sprints.map(x => ({ ID: String(x.ID), Name: x.Name })))).catch(() => {});
      } else fill(fm.Options || []);
      w.el = s; w.get = () => ({ Options: s.value ? [{ ID: s.value, Name: s.selectedOptions[0].text }] : [] }); w.empty = () => !s.value;
      break;
    }
    case 'options': {
      const sel = new Set((v.Options || []).map(o => o.ID));
      const boxes = (fm.Options || []).map(o => h('label.check', h('input', { type: 'checkbox', checked: sel.has(o.ID), onchange: touch }), ' ' + o.Name));
      w.el = h('div.checks', boxes);
      w.get = () => ({ Options: (fm.Options || []).filter((o, i) => boxes[i].firstChild.checked).map(o => ({ ID: o.ID, Name: o.Name })) });
      w.empty = () => !w.get().Options.length;
      break;
    }
    default:
      w.el = h('span.faint', required ? T('Not editable here: set it in Jira') : T('Not editable here'));
  }
  return w;
}
