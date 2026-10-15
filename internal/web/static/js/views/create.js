// Create dialog: openCreate(app, {project, parent, subtask, type, summary, sprint}); a clone adds
// {description, cloneOf, note} and starts from that issue's copy.
// Several lines in the summary make several issues. ctrl+Enter creates.
import { h, debounce, onLeave } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { fieldInput, formRow } from './fields.js';
import { mdEdit } from '../lib/mdedit.js';
import { projectOf } from './plan_ctx.js';
import { T } from '../lib/i18n.js';

css('forms');

const SKIP = new Set(['summary', 'description', 'project', 'issuetype', 'reporter', 'attachment', 'issuelinks', 'comment']);
const COMMON = new Set(['priority', 'assignee', 'labels', 'parent']);
const bullet = /^(?:[-*+•]\s+)?(?:\[[ xX]\]\s+)?(?:\d+[.)]\s+)?/;

// Summaries in the text: one per line, list markers dropped when there are several.
export function summaries(text) {
  const lines = text.split('\n').map(l => l.trim()).filter(Boolean);
  return lines.length > 1 ? lines.map(l => l.replace(bullet, '').trim()).filter(Boolean) : lines;
}

// An unsent form comes back next time, after a reload too: the draft "create" (as lib/mdedit.js keeps the
// editor's), written a moment after each change, on close and when the page goes; draft is its last copy.
let draft = null;
const DRAFT = '/drafts/create';
async function lastDraft(api) {
  if (draft) return draft;
  try { const d = await api.get(DRAFT, { fresh: true }); if (d && d.Text) draft = JSON.parse(d.Text); } catch (e) { /* none */ }
  return draft;
}

export async function openCreate(app, opts = {}) {
  const prefs = app.prefs;
  const last = opts.summary ? null : await lastDraft(app.api);
  const restore = last && (!opts.project || opts.project === last.project) ? last : null;
  let project = opts.project || (restore && restore.project) || projectOf(app, {});
  let type = opts.type || (restore && restore.type) || '';
  let widgets = new Map(), fields = [], pseq = 0, fseq = 0, busy = false, submitted = false;
  let effects = {}, rseq = 0, rtimer = 0; // laneway-server's form rules (reask)
  const rows = new Map();
  const kept = new Map(); // values typed, kept across a type change

  const projectSel = h('select.input', { onchange: () => { project = projectSel.value; type = ''; loadProject(); changed(); } }, h('option', { value: project }, project || '…'));
  const typeSel = h('select.input', { onchange: () => { type = typeSel.value; applyTemplate(); loadFields(); changed(); } });
  const summary = h('textarea.input', { rows: 2, placeholder: T('Summary. One per line makes several issues.'), value: opts.summary || (restore && restore.summary) || '', oninput: () => { count(); changed(); } });
  const files = [];
  const fileBar = h('div.ed-files');
  const paintFiles = () => fileBar.replaceChildren(...files.map((f, i) => h('span.chip', f.name || T('image'), ' ', h('button.btn.ghost.sm', { type: 'button', title: T('Remove'), onclick: () => { files.splice(i, 1); paintFiles(); } }, icon('x')))));
  const ed = mdEdit(app, { doc: opts.descriptionDoc || (restore && restore.descriptionDoc) || null, value: opts.description || (restore ? restore.description || '' : ''), rows: 5,
    placeholder: T('Description. / formats, @ mentions, drop files to attach'), noCancel: true,
    hint: T('files attach after creating'), project: () => project, onFiles: fs => { files.push(...fs); paintFiles(); } });
  // typed: the description is the user's (typed, restored, a clone's), not the type's template.
  let typed = !!(opts.description || opts.descriptionDoc || (restore && (restore.description || restore.descriptionDoc)));
  ed.onInput(() => { typed = !ed.isEmpty(); changed(); });
  const sprintSel = h('select.input', h('option', { value: '' }, T('None (backlog)')));
  const sprintRow = h('div.form-sprint', { hidden: true });
  const count$ = h('div.faint.form-hint');
  const extra = h('div.form');
  const more = h('details.form-more', { hidden: true, open: prefs.get('create.more', '') === '1', ontoggle: () => prefs.set('create.more', more.open ? '1' : '') }, h('summary', T('More fields')), h('div.form'));
  const err = h('div.form-err', { role: 'alert' });
  const another = h('input', { type: 'checkbox', checked: prefs.get('create.another', '') === '1', onchange: () => prefs.set('create.another', another.checked ? '1' : '0') });
  const okBtn = h('button.btn.primary', { type: 'submit' }, T('Create'));

  const form = h('form.form-dialog', { onsubmit: e => { e.preventDefault(); submit(); } },
    h('div.form', formRow(T('Project'), projectSel), formRow(T('Type'), typeSel), formRow(T('Summary'), [summary, count$], true), formRow(T('Description'), [ed.el, fileBar, opts.note ? h('div.faint.form-hint', opts.note) : ''], true)),
    extra, sprintRow, more, err,
    h('div.row.end', h('label.check', another, ' ' + T('Create another')), h('span.spacer'), h('span.faint.form-hint', T('ctrl+⏎ creates')),
      h('button.btn', { type: 'button', onclick: () => m.close() }, T('Cancel')), okBtn));
  const m = app.ui.modal(form, { title: opts.cloneOf ? T('Clone of %s', opts.cloneOf) : T('Create issue'), wide: true, onClose: () => { ed.dispose(); unLeave(); keep(); } });
  m.scope.bind('ctrl+Enter', () => submit(), T('create'), { input: true, hidden: true });
  summary.focus();
  count();

  // The draft: what is typed, unless it is only the type's template; none drops it.
  let dirty = false;
  const keep = (keepalive = false) => {
    if (!dirty || submitted) return;
    dirty = false;
    const v = ed.current();
    draft = summary.value.trim() || (typed && !ed.isEmpty()) ? { project, type, summary: summary.value, description: v.text || '', descriptionDoc: v.doc || null } : null;
    (draft ? app.api.put(DRAFT, { Text: JSON.stringify(draft) }, { keepalive }) : app.api.del(DRAFT, undefined, { keepalive })).catch(() => {});
  };
  const later = debounce(() => keep(), 2000);
  const changed = () => { dirty = true; later(); };
  const unLeave = onLeave(() => keep(true));

  function count() {
    const n = summaries(summary.value).length;
    count$.textContent = n > 1 ? T('%d issues will be created', n) : '';
  }

  // The first project key is shown at once; the creatable ones fill in.
  app.api.get('/projects/creatable').then(ps => {
    if (!ps.length) return;
    const was = project;
    if (!ps.some(p => p.Key === project)) project = ps[0].Key;
    projectSel.replaceChildren(...ps.map(p => h('option', { value: p.Key, selected: p.Key === project }, `${p.Key} ${p.Name}`)));
    if (project !== was) loadProject();
  }).catch(() => {});
  if (project) loadProject();

  async function loadProject() {
    const my = ++pseq;
    if (!project) return;
    try {
      const t = await app.api.get(`/projects/${project}/issuetypes`);
      if (my !== pseq) return;
      err.textContent = '';
      // A subtask offers only the subtask types, as the TUI's.
      const all = opts.subtask && (t.Subtasks || []).length ? t.Subtasks : [...t.Types, ...(opts.parent ? t.Subtasks || [] : [])];
      if (!all.some(x => x.Name === type)) type = prefs.get('create.type.' + project, '');
      if (!all.some(x => x.Name === type)) type = (all.find(x => /^(task|story)$/i.test(x.Name)) || all[0] || {}).Name || '';
      typeSel.replaceChildren(...all.map(x => h('option', { value: x.Name, selected: x.Name === type }, x.Name)));
      applyTemplate();
      loadFields();
      loadSprints();
    } catch (e) { err.textContent = e.message; }
  }

  // ui.templates: the description a type starts with; an untouched one follows a type change.
  let template = '';
  function applyTemplate() {
    const ts = (app.session.ui && app.session.ui.Templates) || {};
    const k = Object.keys(ts).find(t => t.toLowerCase() === String(type).toLowerCase());
    const next = k ? ts[k] : '';
    if (opts.cloneOf || typed || next === template) return;
    template = next;
    ed.ready().then(() => ed.setMarkdown(next, true, true)).catch(() => {});
  }

  async function loadSprints() {
    const p = project;
    sprintRow.hidden = true;
    try {
      const r = await app.api.get(`/projects/${p}/sprints`);
      if (p !== project || !r.Sprints.length) return;
      const want = opts.sprint ? String(opts.sprint) : '';
      sprintSel.replaceChildren(h('option', { value: '' }, T('None (backlog)')), ...r.Sprints.map(s => h('option', { value: s.ID, selected: String(s.ID) === want }, `${s.Name} (${s.State})`)));
      sprintRow.replaceChildren(h('div.form', formRow(T('Sprint'), sprintSel)));
      sprintRow.hidden = false;
    } catch (e) { /* no board: no sprint */ }
  }

  async function loadFields() {
    const my = ++fseq;
    for (const [id, w] of widgets) if (w.touched()) kept.set(id, w.get());
    try {
      const fs = await app.api.get(`/projects/${project}/createfields?type=${encodeURIComponent(type)}`);
      if (my !== fseq) return;
      err.textContent = '';
      fields = fs.filter(f => !SKIP.has(f.ID) && f.Kind !== 'other' && f.Kind !== 'comment' && f.Kind !== 'sprint');
      widgets = new Map();
      const req = [], common = [], rest = [];
      for (const f of fields) {
        const value = kept.get(f.ID) || (f.ID === 'parent' && opts.parent ? { Text: opts.parent } : undefined);
        const w = fieldInput(app, f, { project, value, required: f.Required, onChange: () => reask() });
        if (kept.has(f.ID)) w.touch();
        widgets.set(f.ID, w);
        const row = formRow(f.Name, w.el, f.Required);
        rows.set(f.ID, row);
        (f.Required ? req : COMMON.has(f.ID) ? common : rest).push(row);
      }
      extra.replaceChildren(...[...req.flat(), ...common.flat()].filter(Boolean));
      more.lastChild.replaceChildren(...rest.flat().filter(Boolean));
      more.firstChild.textContent = T('More fields (%d)', rest.length);
      more.hidden = !rest.length;
      effects = {};
      reask();
    } catch (e) { err.textContent = e.message; }
  }

  // laneway-server's form rules as the form changes (LW-423): rows hide, become required or take a default.
  // Jira has none (an empty answer); the server stays the judge at create.
  function reask() {
    clearTimeout(rtimer);
    rtimer = setTimeout(async () => {
      const my = ++rseq;
      const Fields = [];
      for (const f of fields) { const w = widgets.get(f.ID); if (w && !w.empty()) Fields.push({ ID: f.ID, Kind: f.Kind, Value: w.get() }); }
      try {
        const r = await app.api.ask('/form', { Project: project, Type: type, Form: 'create', Fields });
        if (my !== rseq) return;
        effects = r.Fields || {};
        for (const f of fields) {
          const [label, cell] = rows.get(f.ID) || [], e = effects[f.ID] || {}; // hidden, required, default, message
          if (!label) continue;
          label.hidden = cell.hidden = !!e.hidden;
          label.classList.toggle('req', !!(f.Required || e.required));
          if (e.required && !e.hidden && more.contains(label) && widgets.get(f.ID).empty()) more.open = true; // in view, not folded away
          label.title = e.message || '';
          const w = widgets.get(f.ID);
          if (e.default && w && !w.touched() && w.empty() && w.el.matches && w.el.matches('input')) w.el.value = e.default;
        }
      } catch (e) { /* the rules can't be read now: the create still checks them */ }
    }, 300);
  }

  async function attach(key) {
    for (const f of files.splice(0)) {
      const fd = new FormData(); fd.append('file', f, f.name || 'pasted-' + Date.now() + '.png');
      try { const res = await fetch('/api/issues/' + key + '/attachments', { method: 'POST', body: fd }); if (!res.ok) throw new Error(res.statusText); }
      catch (e) { app.ui.toast(T('Could not attach %s to %s', f.name || T('file'), key), { kind: 'err' }); }
    }
    paintFiles();
  }

  // Jira's reasons go under the fields they are about (TUI formErrors); the rest, and the message, above.
  function fieldErrors(e) {
    for (const x of form.querySelectorAll('.field-err')) x.remove();
    if (!e.fields) return e.message;
    const at = { summary: summary, description: ed.el, project: projectSel, issuetype: typeSel };
    const rest = [];
    for (const [id, msg] of Object.entries(e.fields)) {
      const el = at[id] || (widgets.get(id) && widgets.get(id).el);
      if (!el) { rest.push(id + ': ' + msg); continue; }
      if (more.contains(el)) more.open = true;
      el.after(h('div.form-err.field-err', msg));
    }
    return rest.length ? rest.join('; ') : T('Jira refused it: see the fields marked');
  }

  async function submit() {
    if (busy) return;
    for (const x of form.querySelectorAll('.field-err')) x.remove();
    const sums = summaries(summary.value);
    if (!sums.length) { err.textContent = T('Summary is required'); summary.focus(); return; }
    const missing = fields.filter(f => (f.Required || (effects[f.ID] || {}).required) && !(effects[f.ID] || {}).hidden && widgets.get(f.ID).empty()).map(f => f.Name);
    if (missing.length) { err.textContent = T('Fill in %s', missing.join(', ')); return; }
    const Fields = [];
    for (const f of fields) {
      const w = widgets.get(f.ID);
      if (w.touched() && !w.empty()) Fields.push({ ID: f.ID, Kind: f.Kind, Value: w.get() });
      else if (f.ID === 'parent' && !w.empty()) Fields.push({ ID: f.ID, Kind: f.Kind, Value: w.get() });
    }
    const parent = opts.parent && !fields.some(f => f.ID === 'parent') ? opts.parent : '';
    const cur = ed.current(), desc = cur.doc ? { DescriptionDoc: cur.doc } : { Description: cur.text, DescriptionKept: cur.kept };
    busy = true; okBtn.disabled = true; err.textContent = '';
    const made = [], warn = [];
    let failed = null;
    for (const [i, s] of sums.entries()) {
      okBtn.textContent = sums.length > 1 ? T('Creating %d/%d…', i + 1, sums.length) : T('Creating…');
      try {
        const r = await app.api.post('/issues', { Project: project, Type: type, Summary: s, ...desc, Parent: parent, CloneOf: opts.cloneOf || '', Sprint: Number(sprintSel.value) || 0, Fields });
        made.push(r.Key);
        if (files.length) await attach(r.Key);
        if (r.Warning) warn.push(r.Warning);
        app.bus.emit('issue:changed', { key: r.Key, created: true });
      } catch (e) { failed = e; summary.value = sums.slice(i).join('\n'); break; }
    }
    busy = false; okBtn.disabled = false; okBtn.textContent = T('Create');
    if (made.length) {
      prefs.set('create.project', project); prefs.set('create.type.' + project, type);
      const first = made[0];
      app.ui.toast(made.length === 1 ? T('Created %s', first) : T('Created %d issues: %s', made.length, made.join(', ')), { kind: 'ok', ms: 6000, action: { label: T('Open'), run: () => app.panel.open(first) } });
      for (const w of warn) app.ui.toast(w, { kind: 'err' });
    }
    if (failed) {
      err.textContent = (made.length ? T('%d created, then: ', made.length) : '') + fieldErrors(failed);
      return;
    }
    submitted = true; draft = null; dirty = false;
    app.api.del(DRAFT).catch(() => {});
    if (another.checked) {
      summary.value = ''; ed.clear(); typed = false; template = ''; applyTemplate(); count(); summary.focus(); submitted = false;
      return;
    }
    m.close();
    if (made.length === 1) app.panel.open(made[0]);
  }
}
