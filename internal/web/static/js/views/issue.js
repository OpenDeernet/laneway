// The issue panel (right-hand aside) and full page. mountIssue(el, key, {app, full}) → cleanup.
// One scroll, three tabs: Details (fields, development, description, children, links, files), Comments, History;
// a fourth, Terminal, while a herdr agent works on the issue (views/issue_term.js).
// Renders from cache first, refetches, and patches sections rather than rebuilding them.
import { h, clear, delegate } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { render as md } from '../lib/md.js';
import { mdEdit } from '../lib/mdedit.js';
import { postComment } from '../lib/comment.js';
import { issueActions } from './actions.js';
import { mountDev } from './issue_dev.js';
import { mountTerm } from './issue_term.js';
import { ICON, LABEL } from '../lib/agents.js';
import * as pins from '../lib/pins.js';
import { lineDiff } from '../lib/linediff.js';
import { ago, dateTime as localDateTime, shortDate, isZero, duration } from '../lib/fmt.js';
import { goDate } from '../lib/godate.js';
import { grip } from '../lib/grip.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint, modText } from '../lib/keys.js';
import { player } from '../lib/player.js';

const RECENT = 40;           // comments drawn at first; the rest on demand
const trail = [];            // issues left by following a link, oldest first: {key, summary, status, cat}
const meta = new Map();      // key -> {summary, status, cat}, what the trail shows
let expect = null;
let here = null;             // the mounted issue: {key, entry()}
let trailOpen = false;       // the whole trail, not the last TRAIL_SHOWN
const TRAIL_SHOWN = 4;
const catOf = c => (c.Done ? 'done' : c.InProgress ? 'indeterminate' : 'new');
// follow: the mounted issue joins the trail before k opens (palette, jump), so Backspace returns to it.
export function follow(k) {
  if (here && here.key !== k) { trail.push(here.entry()); expect = k; }
}
// showTerminal: type into k's agent in its Terminal tab (ctrl+\ anywhere); the panel opens on k when needed.
let termWant = '';
export function showTerminal(app, k) {
  if (here && here.key === k) return here.term();
  termWant = k;
  app.panel.open(k);
}
// composeWith: text in k's comment composer to edit before posting (an LLM answer, as the TUI's ctrl+a).
let composeWant = null;
export function composeWith(app, k, text) {
  if (here && here.key === k && here.compose) return here.compose(text);
  composeWant = { key: k, text };
  app.panel.open(k);
}
const drafts = new Map();    // unsent comment by issue key (its editor's snapshot)
const fill = (el, ...kids) => { clear(el); for (const k of kids.flat(Infinity)) if (k) el.append(k); return el; };
const dash = () => h('span.faint', '—');

export function mountIssue(el, key, { app, full, card }) {
  css('issue');
  const { api, bus, ui } = app;
  // ui.date_format (a Go layout) for the panel's dates, as the TUI's; else the browser's own.
  const DF = (app.session.ui && app.session.ui.DateFormat) || '';
  const dateTime = t => (DF ? goDate(t, DF) : localDateTime(t));
  const me = () => (app.session && app.session.me) || {};
  const st = { issue: null, card: card && card.Key === key ? card : null, tab: 'details', children: null, weblinks: null, req: null, hist: null, tis: null,
    focusId: null, reply: null, all: false, pending: [], descSig: null, editingDesc: false, editingComment: null };
  if (expect !== key) trail.length = 0;
  expect = null;
  const startTerm = termWant === key;
  termWant = '';
  const startText = composeWant && composeWant.key === key ? composeWant.text : null;
  composeWant = null;
  const me_ = here = { key, entry: () => ({ key, ...(meta.get(key) || {}) }), term: () => openTerm(true) };
  let dead = false;
  const editors = new Set();
  const scope = app.keys.scope('issue', { covers: full ? null : app.panel.focused });
  // Beside a board the panel's keys apply only while it has focus (Tab / click), so the board keeps j/k/c/e/s.
  if (!full) {
    const bind = scope.bind;
    scope.bind = (spec, fn, desc, opts = {}) => bind(spec, fn, desc, { ...opts, help: true, when: opts.when || app.panel.focused });
  }

  // ---- skeleton
  const scroll = h('div.iss-scroll', { tabindex: -1 });
  const head = h('header.iss-head');
  const tabs = h('nav.iss-tabs', { role: 'tablist' });
  const panes = {
    details: h('section.pane.details'),
    comments: h('section.pane.comments'),
    history: h('section.pane.history'),
  };
  const box = {
    fields: h('div.fields'), request: h('div.block.request'), desc: h('div.desc'), docs: h('div.docs'), children: h('div.block.children'), links: h('div.block.links'), files: h('div.block.files'),
    list: h('div.cm-list'), more: h('div.cm-more'), composer: h('div.composer'),
  };
  const dev = mountDev(key, { app, el, full, card: () => st.card, details: () => setTab('details') });
  // .waiting shows only the fields (loading) till the issue is in: dev and notes answer sooner, and the
  // fields filling in above them pushed them down.
  panes.details.classList.add('waiting');
  panes.details.append(box.fields, box.request, dev.el, box.desc, box.docs, box.children, box.links, box.files);
  panes.comments.append(box.more, box.list, box.composer);
  let offNotes = null;
  import('./issue_notes.js').then(m => { if (!dead) offNotes = m.mountNotes(panes.details, key, { app, el, full }); });
  scroll.append(panes.details, panes.comments, panes.history);
  const term = mountTerm(key, { app, back: () => { setTab('details'); scroll.focus({ preventScroll: true }); }, repaint: () => { if (!dead) renderTabs(); } });
  const root = h('div.iss' + (full ? '.full' : ''), !full && h('div.iss-grip', { title: T('Drag to resize'), role: 'separator' }), head, tabs, scroll, term.el);
  el.append(root);

  // ---- helpers
  const copy = async (text, what) => {
    try { await navigator.clipboard.writeText(text); } catch (e) {
      const t = h('textarea', { value: text, style: { position: 'fixed', opacity: 0 } }); document.body.append(t); t.select();
      try { document.execCommand('copy'); } catch (e2) { /* nothing more to try */ }
      t.remove();
    }
    ui.toast(T('%s copied', what));
  };
  const browseURL = () => (st.issue && st.issue.URL) || (app.session.baseURL + '/browse/' + key);
  // The site's own web is this one when it is laneway-server's (hosted): nothing to open elsewhere.
  const elsewhere = !app.session || app.session.siteElsewhere !== false;
  const go = (k, push = true) => { expect = k; return full ? app.go('/issue/' + k, { replace: !push }) : app.panel.open(k, { force: true, push }); };
  const open = k => { if (k !== key) { trail.push(me_.entry()); go(k); } };
  const back = () => { if (!trail.length) return ui.toast(T('No previous issue')); go(trail.pop().key); };
  const jumpTo = i => { const e = trail[i]; if (!e) return; trail.length = i; go(e.key); };
  const learn = (k, c) => { if (c && c.Summary) meta.set(k, { summary: c.Summary, status: c.Status, cat: c.StatusCategory || catOf(c) }); };
  function trailEl() {
    if (!trail.length) return null;
    const from = trailOpen ? 0 : Math.max(trail.length - TRAIL_SHOWN, 0);
    for (const e of trail) if (!e.summary && !e.asked) {
      e.asked = true;
      api.get('/issues/' + e.key + '/card').then(c => { learn(e.key, c); Object.assign(e, meta.get(e.key)); if (!dead) renderHead(); }).catch(() => {});
    }
    return h('div.iss-trail' + (trailOpen ? '.open' : ''), { role: 'navigation', 'aria-label': T('Came from') },
      h('div.tr-head', h('span', (app.session && app.session.site) || 'Jira'),
        ...(trail.length > TRAIL_SHOWN ? [h('button.tr-more', { title: T('Show or hide older'), onclick: () => { trailOpen = !trailOpen; renderHead(); } },
          trailOpen ? T('less') : '+' + (trail.length - TRAIL_SHOWN))] : []), h('span.spacer'), h('kbd', 'Backspace')),
      h('div.tr-list', trail.slice(from).map((e, j) => {
        const d = meta.get(e.key) || e;
        return h('button.tr-row', { title: e.key + ' ' + (d.summary || ''), onclick: () => jumpTo(from + j) },
          h('span.tr-mark', icon('corner-left-up')), h('span.tr-key', e.key), d.status ? ui.statusPill(d.status, d.cat) : null, h('span.tr-sum.clip', d.summary || ''));
      })));
  }
  const viewKeys = () => [...new Set([...document.querySelectorAll('#view [data-key]')].map(e => e.dataset.key).filter(k => /^[A-Z][A-Z0-9]*-\d+$/.test(k)))];
  const step = d => {
    const ks = viewKeys(), i = ks.indexOf(key);
    if (i < 0 || !ks[i + d]) return ui.toast(i < 0 ? T('Not in the list beside') : T('End of the list'));
    trail.length = 0; go(ks[i + d], false);
  };
  const prefixes = () => new Set([...(app.session.projects || []), key.split('-')[0], ...(st.issue ? (st.issue.Links || []).map(l => l.Key.split('-')[0]) : [])]);
  const people = () => {
    const i = st.issue, m = new Map();
    const add = (id, name) => { if (name && !m.has(name)) m.set(name, id || ''); };
    if (i) { add(i.AssigneeAccountID, i.Assignee); add(i.ReporterAccountID, i.Reporter); for (const c of i.Comments) add(c.AuthorID, c.Author); for (const u of i.Mentioned || []) add(u.AccountID, u.DisplayName); }
    return m;
  };
  const mdOpts = () => {
    const pre = prefixes();
    const att = id => ((st.issue && st.issue.Attachments) || []).find(a => a.ID === id);
    return { attachment: id => (att(id) ? mediaURL(att(id)) : '/api/attachments/' + encodeURIComponent(id)), video: id => (att(id) && isVideo(att(id)) ? player(mediaURL(att(id)), { poster: poster(att(id)), focusKeys: true }).el : null), names: [...people().keys()].sort((a, b) => b.length - a.length),
      isKey: s => pre.has(s.slice(0, s.lastIndexOf('-'))), onKey: open, site: app.session.baseURL };
  };
  const fail = e => ui.errToast(e);
  const changed = what => bus.emit('issue:changed', { key, what });
  const edit = (field, anchor) => app.actions.edit(key, field, anchor);

  // ---- header + tabs
  function renderHead() {
    const i = st.issue, c = st.card;
    const summary = (i && i.Summary) || (c && c.Summary) || '';
    const type = (i && i.Type) || (c && c.Type) || '';
    clear(head).append(
      h('div.iss-top',
        ...(type ? [h('span.chip', type)] : []),
        h('button.iss-key.btn.link', { title: T('Copy key (y)'), onclick: () => copy(key, key) }, key),
        h('button.btn.ghost.sm', { title: T('Copy link (Y)'), onclick: () => copy(browseURL(), T('Link')) }, T('Copy link'), keyHint('Y')),
        h('button.btn.ghost.sm', { title: T('Subtask, clone, move, watchers… (A)'), onclick: () => actions() }, T('Actions'), keyHint('A')),
        app.agents && app.agents.available && !(app.session && app.session.demo) &&
          h('button.btn.ghost.sm', { title: T('Start work: worktree and agent (S) · another agent (alt+s)'), onclick: () => app.agents.start(key) }, (app.agents.stateFor(key) || {}).count ? T('Agent') : T('Start work'), keyHint('S')),
        h('span.spacer'),
        elsewhere && h('a.btn.ghost.sm', { href: browseURL(), target: '_blank', rel: 'noopener noreferrer', title: T('Open in Jira (o)') }, 'Jira', icon('external-link')),
        h('button.btn.ghost.sm', { title: full ? T('Back (esc)') : T('Close (esc)'), onclick: goBack }, full ? [icon('arrow-left'), T('Back')] : icon('x'), keyHint('Escape'))),
      h('h1.iss-title', { title: T('Edit summary'), onclick: e => edit('summary', e.currentTarget) }, summary || '…'),
      ...(i || c ? [h('div.iss-sub', h('button.pill-btn', { title: T('Change status'), onclick: () => app.actions.transition(key) }, i ? ui.statusPill(i.Status, i.StatusCategory) : ui.statusPill(c.Status, catOf(c))),
        ...(c && c.Flagged ? [h('span.chip.flag', T('Flagged'))] : []),
        ...(app.timer && app.timer.mark(key) ? [h('button.chip.timer-mark', { title: T('Timer running · T stops it'), onclick: () => app.timer.toggle(key) }, icon('timer'), ' ' + app.timer.mark(key))] : []),
        ...(app.agents ? [app.agents.chip(key)] : []),
        h('span.dim', T('updated %s', ago((i || c).Updated))))]
        // its height held, or the tabs below jump down as it comes
        : [h('div.iss-sub', { 'aria-hidden': 'true', style: { visibility: 'hidden' } }, h('span.pill-btn', ui.statusPill('…', 'new')))]));
    learn(key, c); if (i) learn(key, { Summary: i.Summary, Status: i.Status, StatusCategory: i.StatusCategory });
    const tr = trailEl(); if (tr) head.prepend(tr);
    document.title = key + (summary ? ' ' + summary : '') + ' · laneway';
  }
  function renderTabs() {
    const n = st.issue ? (st.issue.CommentTotal || (st.issue.Comments || []).length) : 0;
    const ts = [['details', T('Details'), '1'], ['comments', n ? T('Comments %d', n) : T('Comments'), '2'], ['history', T('History'), '3']];
    if (term.has() || st.tab === 'terminal') ts.push(['terminal', T('Terminal'), '4']);
    clear(tabs).append(...ts.map(([id, label, k]) => {
      const s = id === 'terminal' && term.status();
      // The key in the tooltip: a number beside the label reads as a count.
      return h('button.tab' + (st.tab === id ? '.on' : ''), { role: 'tab', 'aria-selected': st.tab === id, title: label + ' (' + k + ')',
        onclick: () => (id !== 'terminal' ? setTab(id) : st.tab !== 'terminal' && openTerm(false)) },
        s ? h('span.it-st.st-' + s, { title: T('agent %s', LABEL[s] || s) }, icon(ICON[s] || ICON.unknown)) : null, label);
    }));
  }
  function setTab(t) {
    const was = st.tab;
    st.tab = t;
    for (const [id, p] of Object.entries(panes)) p.hidden = id !== t;
    const on = t === 'terminal';
    scroll.hidden = on;
    root.classList.toggle('term-on', on);
    el.classList.toggle('term-on', on);
    if (on) term.show(); else term.hide();
    renderTabs();
    if (t === 'history') loadHistory();
    if (t === 'comments' && st.focusId == null) focusComment(null);
    if (t === 'comments' && was !== 'comments' && !newest()) { // oldest first: the latest is at the end
      // Again each frame while content-visibility sizes the comments it scrolls past.
      let h = -1, n = 0;
      const end = () => { if (st.tab !== 'comments' || (scroll.scrollHeight === h && n > 1) || ++n > 20) return; h = scroll.scrollHeight; scroll.scrollTop = h; requestAnimationFrame(end); };
      end();
    }
  }
  // The Terminal tab: shown (4, a click) or typing (ctrl+\, enter); 4 on it steps through several agents.
  function openTerm(typing) {
    if (!term.has() && st.tab !== 'terminal') return ui.toast(T('%s has no agent: S starts one', key));
    if (st.tab === 'terminal' && !typing) return term.next();
    if (st.tab !== 'terminal') setTab('terminal');
    if (typing) term.type(); else term.el.focus({ preventScroll: true });
  }
  function goBack() {
    if (!full) return app.panel.close();
    app.back('/board');
  }

  // ---- fields
  // The panel's own fields, then the rest of the edit screen (editmeta: components, custom fields), as the TUI's
  // panel_fields.go; a click edits one. Pinned fields show (the pin on hover; the site's every project, shared
  // with the TUI; own ones pinned till unpinned, the others once pinned), the others fold under More (the pref
  // fields.more remembers it open); ui.empty_fields: hide folds their empty ones too. A filled rich-text field
  // reads as a section under the description (renderDocs). Read-only: the screen Jira last offered for this
  // project and type, on an issue it lets no one edit now. One the field configuration requires is marked *.
  function renderFields() {
    const i = st.issue; if (!i) return clear(box.fields);
    const c = st.card || {};
    const cell = (field, label, value) => {
      const inner = [h('span.k' + (field && required(field) ? '.req' : ''), label), h('span.v', value == null || value === '' ? dash() : value)];
      return field ? h('button.fld', { dataset: { field }, title: T('Edit %s', label.toLowerCase()), onclick: e => edit(field, e.currentTarget) }, inner)
        : h('div.fld.ro', inner);
    };
    const parent = (c.ParentKey || parentLink());
    const pk = c.ParentKey || (parent && parent.Key), ps = c.ParentSummary || (parent && parent.Summary);
    const date = t => (isZero(t) ? null : shortDate(t));
    // [pin id, field to edit, label, value]
    const own = [
      ['status', 'status', T('Status'), i.Status && ui.statusPill(i.Status, i.StatusCategory)],
      ['priority', 'priority', T('Priority'), i.Priority],
      // Nobody assigned reads as a word, not a person's avatar.
      ['assignee', 'assignee', T('Assignee'), i.Assignee && (c.Key && !c.AssigneeID ? h('span.dim', T('Unassigned')) : h('span.who', ui.avatar(i.Assignee, c.AvatarURL, 18), i.Assignee))],
      ['reporter', 'reporter', T('Reporter'), i.Reporter],
      ['points', 'points', T('Points'), i.StoryPoints],
      ['labels', 'labels', T('Labels'), i.Labels && i.Labels.length ? h('span.chips', i.Labels.map(l => h('span.chip', l))) : null],
      ['parent', 'parent', T('Parent'), pk && h('span.parent', h('a.issue-ref', { href: '/issue/' + pk, title: ps, onclick: e => { e.preventDefault(); e.stopPropagation(); open(pk); } }, pk), ps && h('span.dim.clip', ' ' + ps))],
      ['sprint', 'sprint', T('Sprint'), c.Sprint],
      ['duedate', 'due', T('Due'), date(c.Due)],
      ['updated', null, T('Updated'), ago(i.Updated)],
      ['created', null, T('Created'), !isZero(c.Created) ? dateTime(c.Created) : null],
    ].map(([id, field, label, value]) => ({ id, name: label, def: true, filled: value != null && value !== '', el: cell(field, label, value) }));
    clear(box.fields).append(...fieldRows([...own, ...extraRows(cell)]));
  }
  const SHOWN = new Set(['parent', 'duedate']);
  const valueText = v => (!v ? '' : v.Text ? (v.Text.length > 80 ? v.Text.slice(0, 80) + '…' : v.Text) : [...(v.Users || []).map(u => u.DisplayName), ...(v.Options || []).map(o => o.Name)].join(', '));
  // editmeta once it answered; till then the screen last seen for the issue's project and type, which came
  // with the issue (jira.Issue.Screen): the fields draw at once, no jump when editmeta lands.
  const fieldMeta = () => st.meta || (st.issue && st.issue.Screen ? { Fields: st.issue.Screen, Values: st.issue.ScreenValues || {}, early: true } : null);
  const fieldPins = () => (st.meta && st.meta.Pins) || app.session.pins || {};
  const isDoc = f => f.Kind === 'doc' && !!valueText(fieldMeta().Values[f.ID]);
  // required: editmeta's field id (or the panel's due) may not be emptied.
  const required = id => {
    const m = fieldMeta(), want = id === 'due' ? 'duedate' : id === 'points' ? m && m.PointsField : id;
    if (m && want && (m.OwnRequired || []).includes(want)) return true; // the panel's own rows (LW-357)
    const f = m && want && (m.Fields || []).find(x => x.ID === want);
    return !!(f && f.Required && !f.ReadOnly);
  };
  function extraRows(cell) {
    const m = fieldMeta(); if (!m) return [];
    return (m.Fields || []).filter(f => !SHOWN.has(f.ID) && f.Kind !== 'sprint' && !isDoc(f))
      .map(f => ({ id: f.ID, name: f.Name, def: false, filled: !!valueText(m.Values[f.ID]), el: fieldCell(cell, f) }));
  }
  // fieldRows are the rows shown: the pinned, then More over the others.
  function fieldRows(rows) {
    const pins = fieldPins();
    const pinned = r => (r.id in pins ? pins[r.id] : r.def);
    const top = rows.filter(pinned), rest = rows.filter(r => !pinned(r));
    const out = top.map(r => pinCell(r, true));
    if (!rest.length) return out;
    const open = app.prefs.get('fields.more', '') === 'open';
    out.push(h('button.fld.more', { 'aria-expanded': String(open), onclick: () => { app.prefs.set('fields.more', open ? '' : 'open'); renderFields(); } },
      h('span.k', top.length ? T('More fields') : T('Fields')), h('span.v.dim', icon(open ? 'chevron-down' : 'chevron-right'), ' ' + rest.length, open ? '' : [' · ', icon('pin'), ' ' + T('to pin/unpin')])));
    if (!open) return out;
    const hide = String((app.session.ui && app.session.ui.EmptyFields) || '').toLowerCase() === 'hide' && !st.showEmpty;
    const shown = hide ? rest.filter(r => r.filled) : rest;
    out.push(...shown.map(r => pinCell(r, false)));
    if (rest.length > shown.length) out.push(h('button.fld.more', { onclick: () => { st.showEmpty = true; renderFields(); } }, h('span.k', T('Empty fields')), h('span.v.dim', T('+ %d more', rest.length - shown.length))));
    return out;
  }
  function fieldCell(cell, f) {
    const v = valueText(fieldMeta().Values[f.ID]);
    let c;
    if (f.ReadOnly) { c = cell(null, f.Name, v); c.title = f.ByRule ? T('A field rule lets you only read it') : T('The site lets no one edit this issue now'); }
    else if (f.Kind === 'doc') c = h('button.fld', { title: T('Write %s', f.Name.toLowerCase()), onclick: () => editDoc(f) }, h('span.k' + (required(f.ID) ? '.req' : ''), f.Name), h('span.v', dash()));
    else c = cell(f.ID, f.Name, v);
    return c;
  }
  function pinCell(r, on) {
    return h('div.fld-pin' + (on ? '.on' : ''), r.el, h('button.pin', { title: on ? T('Unpin: fold it under More') : T('Pin: show it on every issue'), 'aria-label': on ? T('Unpin %s', r.name) : T('Pin %s', r.name), 'aria-pressed': String(on), onclick: () => pinField(r.id, !on) }, icon('pin', on)));
  }
  async function pinField(id, on) {
    try { app.session.pins = await api.put('/fields/pinned/' + encodeURIComponent(id), { On: on }); } catch (e) { return fail(e); }
    if (st.meta) st.meta.Pins = app.session.pins;
    renderFields();
  }

  // ---- rich-text fields: a filled one is a section like the description, edited the same way
  function renderDocs() {
    const m = fieldMeta();
    if (!m || st.editingDoc) return; // a reload keeps the last ones until editmeta answers: no jump
    const fs = (m.Fields || []).filter(isDoc);
    clear(box.docs).append(...fs.map(f => h('div.desc.doc', { dataset: { field: f.ID }, ondblclick: e => { if (!f.ReadOnly && !e.target.closest('a,input,img,summary')) editDoc(f); } },
      h('div.sec-head', h('h3', f.Name), !f.ReadOnly && h('button.btn.ghost.sm', { title: T('Edit %s', f.Name), onclick: () => editDoc(f) }, T('Edit'))),
      h('div.md', md(m.Values[f.ID].Text, mdOpts())))));
  }
  async function editDoc(f) {
    if (st.editingDoc) return;
    setTab('details');
    let ed;
    try { ed = await api.get('/issues/' + key + '/doc/' + encodeURIComponent(f.ID), { fresh: true }); } catch (e) { return fail(e); }
    if (dead) return;
    st.editingDoc = f.ID;
    const sec = box.docs.querySelector('[data-field="' + CSS.escape(f.ID) + '"]') || box.docs.appendChild(h('div.desc.doc', { dataset: { field: f.ID } }));
    const done = () => { st.editingDoc = null; renderDocs(); };
    const e = editor({ ...docOf(ed), rows: 8, placeholder: T('%s…', f.Name), allowEmpty: true, label: T('Save'), draft: 'desc:' + key + ':field:' + f.ID, base: ed.Base,
      save: async (doc, base) => {
        if (required(f.ID) && e.isEmpty()) { ui.toast(T('%s is required', f.Name), { kind: 'err' }); return false; }
        const r = await saveDoc('/issues/' + key + '/doc/' + encodeURIComponent(f.ID), f.Name, e, { Doc: doc }, base);
        if (r === 'theirs') { e.dropDraft(); done(); changed(); }
        if (r !== true) return false;
        st.editingDoc = null; changed(T('%s %s updated', key, f.Name)); ui.toast(T('%s saved', f.Name), { kind: 'ok' });
      },
      cancel: done });
    clear(sec).append(h('div.sec-head', h('h3', f.Name)), e.el);
    e.focus();
    sec.scrollIntoView({ block: 'nearest' });
  }
  const parentLink = () => st.issue && (st.issue.Links || []).find(l => l.Rel === 'parent');

  // ---- description
  function renderDesc(force) {
    const i = st.issue; if (!i || st.editingDesc) return;
    const sig = i.Description;
    if (!force && st.descSig === sig) return;
    st.descSig = sig;
    const body = h('div.md');
    if (sig && sig.trim()) body.append(md(sig, { ...mdOpts(), onTask: toggleTask }));
    else body.append(h('p.faint', T('No description. Press e to write one.')));
    clear(box.desc).append(h('div.sec-head', h('h3', T('Description')), h('button.btn.ghost.sm', { title: T('Edit (E)'), onclick: editDesc }, T('Edit'), keyHint('E'))), body);
    box.desc.ondblclick = e => { if (!e.target.closest('a,input,img,summary')) editDesc(); };
  }
  let taskChain = Promise.resolve();
  function toggleTask(n, total, done, input) {
    taskChain = taskChain.then(() => api.post('/issues/' + key + '/description/task', { N: n, Total: total, Done: done }))
      .then(() => { st.issue.Description = flipTask(st.issue.Description, n, done); st.descSig = st.issue.Description; box.desc.querySelector('[data-task="' + n + '"]')?.closest('li')?.classList.toggle('done', done); })
      .catch(e => { input.checked = !done; fail(e); reload(); });
  }
  async function editDesc() {
    if (st.editingDesc || !st.issue) return;
    setTab('details');
    let ed;
    try { ed = await api.get('/issues/' + key + '/description', { fresh: true }); } catch (e) { return fail(e); }
    if (dead) return;
    st.editingDesc = true;
    const e = editor({ ...docOf(ed), rows: 10, placeholder: T('Description… (/ inserts, @ mentions)'), allowEmpty: true, label: T('Save'), draft: 'desc:' + key, base: ed.Base,
      save: async (doc, base) => {
        const r = await saveDoc('/issues/' + key + '/description', T('Description'), e, { Doc: doc }, base);
        if (r === 'theirs') { e.dropDraft(); st.editingDesc = false; st.descSig = null; changed(); }
        if (r !== true) return false;
        st.editingDesc = false; st.issue.Description = null; st.descSig = null; changed(T('%s description updated', key)); ui.toast(T('Description saved'), { kind: 'ok' });
      },
      cancel: () => { st.editingDesc = false; renderDesc(true); } });
    clear(box.desc).append(h('div.sec-head', h('h3', T('Description'))), e.el);
    e.focus();
  }
  // saveDoc PUTs body to a document's path when Jira still has the one the editor opened on (base). When someone
  // changed it meanwhile (409) it shows what saving yours would do to theirs and asks: true when saved, 'theirs' to
  // drop yours, false to keep editing (the editor then saves against theirs).
  async function saveDoc(path, name, e, body, base) {
    try { await api.put(path, { ...body, Base: base }); return true; } catch (err) { if (err.status !== 409) throw err; }
    const now = await api.get(path, { fresh: true });
    e.rebase(now.Base);
    // What saving would change, read as markdown.
    const read = doc => (doc ? api.post('/adf/markdown', { Doc: doc }).then(c => c.Text, () => '') : '');
    const [theirs, mine] = await Promise.all([read(now.Doc), read(body.Doc)]);
    const pick = await new Promise(resolve => {
      let v = false;
      const b = (label, val, cls = '') => h('button.btn' + cls, { onclick: () => { v = val; m.close(); } }, label);
      const diff = lineDiff(theirs, mine, 40);
      const m = ui.modal(h('div',
        h('p', T('Someone changed it on the site since you opened it. Nothing was saved.')),
        diff.length > 0 && [h('p.dim', T('Saving yours over theirs:')), h('div.chg-diff', diff.map(l => h('div.' + (l[0] === '-' ? 'del' : l[0] === '+' ? 'add' : 'more'), l)))],
        h('div.row.end', b(T('Keep editing'), false, '.primary'), b(T('Take theirs'), 'theirs'), b(T('Save yours over it'), 'mine', '.danger'))),
      { title: T('%s changed on the site', name), wide: true, onClose: () => resolve(v) });
    });
    if (pick !== 'mine') return pick;
    await api.put(path, { ...body, Base: now.Base });
    return true;
  }

  // ---- a service desk's request: its type, customer and SLAs' time left, red once breached (TUI renderRequest)
  function renderRequest() {
    const r = st.req && st.req.Request;
    // An issue of a desk's project that came in another way (no request type, no SLA) isn't a request to show.
    if (!r || !r.Type && !(r.SLAs || []).length) return clear(box.request);
    const sla = s => (s.Done ? (s.Breached ? T('breached') : T('met')) : s.Breached ? T('%s · breached', s.Remaining) : s.Paused ? T('%s left · paused', s.Remaining) : T('%s left', s.Remaining));
    const cell = (k, v, cls = '') => h('div.fld' + cls, h('span.k', k), h('span.v', v));
    fill(box.request, h('div.sec-head', h('h3', T('Request'))),
      h('div.fields', cell(T('Type'), r.Type || '—'), cell(T('Customer'), [r.Customer || '—', r.CustomerEmail && h('span.dim', ' ' + r.CustomerEmail)]),
        (r.SLAs || []).map(s => cell(s.Name, sla(s), s.Breached ? '.breached' : ''))));
  }

  // ---- children, links, files
  function renderChildren() {
    const kids = st.children;
    if (!kids || !kids.length) return clear(box.children);
    const done = kids.filter(k => k.Done).length;
    clear(box.children).append(
      h('div.sec-head', h('h3', T('Child issues')), h('span.dim', done + '/' + kids.length), h('div.bar', h('i', { style: { width: Math.round(100 * done / kids.length) + '%' } }))),
      h('div.rows', kids.map(k => h('a.row-link' + (k.Done ? '.done' : ''), { href: '/issue/' + k.Key, onclick: e => { e.preventDefault(); open(k.Key); } },
        h('span.mono.k', k.Key), h('span.clip', k.Summary), ui.statusPill(k.Status, k.Done ? 'done' : 'indeterminate'), k.Assignee && ui.avatar(k.Assignee, null, 18)))));
  }
  function renderLinks() {
    const i = st.issue; if (!i) return clear(box.links);
    const links = (i.Links || []).filter(l => l.Rel !== 'parent' && l.Rel !== 'subtask');
    const web = st.weblinks || [];
    const groups = new Map();
    for (const l of links) (groups.get(l.Rel) || groups.set(l.Rel, []).get(l.Rel)).push(l);
    fill(box.links,
      h('div.sec-head', h('h3', T('Links')), h('button.btn.ghost.sm', { title: T('Link an issue (L)'), onclick: addLink }, T('+ Issue'), keyHint('L')), h('button.btn.ghost.sm', { onclick: addWebLink }, T('+ Web'))),
      !links.length && !web.length && h('p.faint', T('No links.')),
      [...groups].map(([rel, ls]) => h('div.lgroup', h('div.rel', rel), ls.map(l => h('div.row-link', { dataset: { key: l.Key }, onclick: () => open(l.Key) },
        h('span.mono.k', l.Key), h('span.clip', l.Summary), l.Status && h('span.chip', l.Status),
        l.LinkID && h('button.btn.ghost.sm.x', { title: T('Remove link'), onclick: e => { e.stopPropagation(); removeLink(l); } }, icon('x')))))),
      web.length > 0 && h('div.lgroup', h('div.rel', T('Web')), web.map(w => h('a.row-link', { href: safeHref(w.URL), target: '_blank', rel: 'noopener noreferrer',
        onclick: w.Page ? e => { if (!e.ctrlKey && !e.metaKey) { e.preventDefault(); readPage(w); } } : null },
        h('span.clip', w.Title), w.App && h('span.chip', w.App)))));
  }
  // A Confluence page of this site, read in place (ctrl+click: in Confluence).
  async function readPage(w) {
    let p;
    try { p = await api.get('/confluence/pages/' + w.Page); } catch (e) { return ui.errToast(e); }
    ui.modal(h('div.page', h('div.md', md(p.Markdown, { ...mdOpts(), pageImage: id => '/api/confluence/images/' + encodeURIComponent(id) })), h('div.modal-foot', h('a.btn', { href: safeHref(p.URL), target: '_blank', rel: 'noopener noreferrer' }, T('Open in Confluence'), icon('external-link')))),
      { title: p.Title, wide: true });
  }
  const addLink = () => actions('link');
  const actions = only => issueActions(app, st, { key, changed, open, upload: attachFiles, reloadExtras: () => { st.weblinks = null; return loadExtras(); }, tab: setTab }, only);
  async function addWebLink() {
    const url = await ui.prompt({ title: T('Link a web page'), placeholder: 'https://…', ok: T('Add') });
    if (!url || !url.trim()) return;
    try { await api.post('/issues/' + key + '/weblinks', { URL: url.trim(), Title: '' }); st.weblinks = null; loadExtras(); } catch (e) { fail(e); }
  }
  async function removeLink(l) {
    if (!await ui.confirm({ title: T('Remove link'), text: T('%s %s?', l.Rel, l.Key), ok: T('Remove'), danger: true })) return;
    try { await api.del('/issues/' + key + '/links/' + encodeURIComponent(l.LinkID)); changed(); } catch (e) { fail(e); }
  }
  const safeHref = u => (/^https?:\/\//i.test(u) ? u : '#');

  // Images show in place; videos the server serves to a player (mp4, mov, webm, ogg) play in place.
  const isImage = a => a.MimeType.startsWith('image/') && !/svg/.test(a.MimeType);
  const isVideo = a => /^video\/(mp4|quicktime|webm|ogg)$/.test(a.MimeType);
  const mediaURL = a => '/api/attachments/' + encodeURIComponent(a.ID) + (isVideo(a) ? '?type=' + encodeURIComponent(a.MimeType) : '');
  const poster = a => '/api/attachments/' + encodeURIComponent(a.ID) + '/thumbnail';
  const size = n => (n > 1 << 20 ? (n / (1 << 20)).toFixed(1) + ' MB' : Math.max(1, Math.round(n / 1024)) + ' KB');
  function renderFiles() {
    const fs = (st.issue && st.issue.Attachments) || [];
    if (!fs.length) return clear(box.files);
    const shown = fs.filter(a => isImage(a) || isVideo(a)), rest = fs.filter(a => !shown.includes(a));
    fill(box.files,h('div.sec-head', h('h3', T('Attachments')), h('span.dim', String(fs.length))),
      shown.length > 0 && h('div.thumbs', shown.map(a => h('a.thumb', { href: mediaURL(a), title: a.Filename + ' · ' + size(a.Size), onclick: e => { e.preventDefault(); gallery(0, mediaURL(a)); } },
        isVideo(a) ? h('span.thumb-video', h('img', { src: poster(a), alt: a.Filename, loading: 'lazy', decoding: 'async' }), icon('play'))
          : h('img', { src: mediaURL(a), alt: a.Filename, loading: 'lazy', decoding: 'async' }), h('span.clip', a.Filename)))),
      rest.map(a => h('a.row-link', { href: '/api/attachments/' + encodeURIComponent(a.ID) + '?download=1&name=' + encodeURIComponent(a.Filename), download: a.Filename }, h('span.clip', a.Filename), h('span.dim', size(a.Size)))));
  }
  // In-page viewer: the attachments' images and videos, then the images drawn in the text. ← → step, esc closes.
  function galleryList() {
    const seen = new Set(), out = [];
    const add = (src, name, video, still) => { if (src && !seen.has(src)) { seen.add(src); out.push({ src, name, video, still }); } };
    for (const a of (st.issue && st.issue.Attachments) || []) if (isImage(a) || isVideo(a)) add(mediaURL(a), a.Filename, isVideo(a), isVideo(a) && poster(a));
    for (const im of root.querySelectorAll('img.md-img')) add(im.getAttribute('src'), im.alt);
    return out;
  }
  function gallery(at, src) {
    const list = galleryList();
    if (!list.length) return ui.toast(T('No images'));
    let i = src ? Math.max(0, list.findIndex(x => x.src === src)) : at;
    let shown = h('img.lightbox'), vid = null;
    const stop = () => { if (vid) vid.dispose(); vid = null; };
    const name = h('span'), dl = h('a.btn.ghost.sm', { title: T('Download (d)'), 'aria-label': T('Download') }, icon('download'));
    const show = d => {
      i = (i + d + list.length) % list.length; const x = list[i];
      stop();
      if (x.video) { vid = player(x.src, { autoplay: true, poster: x.still, onError: () => ui.toast(T("This video doesn't play in the browser; download it (d).")) }); vid.keys(); }
      const el = vid ? vid.el : h('img.lightbox', { src: x.src, alt: x.name });
      el.classList.add('lightbox'); shown.replaceWith(el); shown = el;
      name.textContent = x.name + (list.length > 1 ? '  ' + (i + 1) + '/' + list.length : '');
      dl.href = x.src + (x.src.includes('?') ? '&' : '?') + 'download=1&name=' + encodeURIComponent(x.name); dl.download = x.name;
    };
    const m = ui.modal(h('div.lb', list.length > 1 && h('button.btn.ghost.lb-prev', { onclick: () => show(-1), title: T('Previous (←)'), 'aria-label': T('Previous') }, icon('chevron-left')), shown, list.length > 1 && h('button.btn.ghost.lb-next', { onclick: () => show(1), title: T('Next (→)'), 'aria-label': T('Next') }, icon('chevron-right')), h('div.lb-cap', name, dl)), { wide: true, onClose: stop });
    m.scope.bind(['ArrowRight', 'n'], () => show(1), '', { hidden: true });
    m.scope.bind(['ArrowLeft', 'N'], () => show(-1), '', { hidden: true });
    m.scope.bind('d', () => dl.click(), T('download'));
    show(0);
  }
  root.addEventListener('click', e => { const im = e.target.closest('img.md-img'); if (im) gallery(0, im.getAttribute('src')); });

  // Linked issue jump: parent, links, children, web links.
  async function linked() {
    const i = st.issue; if (!i) return;
    const items = [...(i.Links || []).map(l => ({ k: l.Key, label: l.Key + '  ' + (l.Rel || '') + '  ' + (l.Summary || '') })),
      ...(st.children || []).filter(c => !(i.Links || []).some(l => l.Key === c.Key)).map(c => ({ k: c.Key, label: c.Key + '  ' + T('child') + '  ' + c.Summary })),
      ...(st.weblinks || []).map(w => ({ url: w.URL, w, label: (w.Page ? '▤ ' : '↗ ') + (w.Title || w.URL) }))];
    if (!items.length) return ui.toast(T('No links'));
    const it = await ui.pick({ title: T('Go to'), items, label: x => x.label, placeholder: T('Linked issue…') });
    if (!it) return;
    if (it.k) open(it.k); else if (it.w.Page) readPage(it.w); else window.open(safeHref(it.url), '_blank', 'noopener');
  }

  // Find in the issue: marks matches in all tabs; n / N (or enter) step, esc clears.
  const find = { on: false, marks: [], at: -1 };
  const findBar = h('div.iss-find', { hidden: true });
  const findIn = h('input.input', { type: 'text', placeholder: T('Find in this issue…'), spellcheck: false, oninput: () => findRun(findIn.value) });
  const findCount = h('span.dim');
  findBar.append(findIn, findCount, h('button.btn.ghost.sm', { onclick: () => findStep(-1), title: T('Previous (N)'), 'aria-label': T('Previous') }, icon('arrow-up'), keyHint('N')), h('button.btn.ghost.sm', { onclick: () => findStep(1), title: T('Next (n)'), 'aria-label': T('Next') }, icon('arrow-down'), keyHint('n')), h('button.btn.ghost.sm', { onclick: () => findClose(), title: T('Close (esc)'), 'aria-label': T('Close') }, icon('x'), keyHint('Escape')));
  findIn.addEventListener('keydown', e => {
    if (e.key === 'Enter') { e.preventDefault(); e.stopPropagation(); findStep(e.shiftKey ? -1 : 1); findIn.blur(); }
    else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); findClose(); }
  });
  head.after(findBar);
  function findClear() {
    for (const m of find.marks) { const t = document.createTextNode(m.textContent); m.replaceWith(t); t.parentNode && t.parentNode.normalize(); }
    find.marks = []; find.at = -1;
  }
  function findOpen() { find.on = true; findBar.hidden = false; findIn.focus(); findIn.select(); }
  function findClose() { findClear(); find.on = false; findBar.hidden = true; findIn.value = ''; findCount.textContent = ''; scroll.focus({ preventScroll: true }); }
  function findRun(q) {
    findClear();
    q = q.trim().toLowerCase();
    if (q.length < 2) { findCount.textContent = ''; return; }
    const hits = [], w = document.createTreeWalker(scroll, NodeFilter.SHOW_TEXT);
    for (let n = w.nextNode(); n && hits.length < 500; n = w.nextNode()) {
      if (n.parentElement.closest('textarea,script,style,.ed')) continue;
      const t = n.nodeValue.toLowerCase();
      for (let i = t.indexOf(q); i >= 0; i = t.indexOf(q, i + q.length)) hits.push([n, i]);
    }
    for (let k = hits.length - 1; k >= 0; k--) {
      const [n, i] = hits[k];
      const r = document.createRange(); r.setStart(n, i); r.setEnd(n, i + q.length);
      const mk = h('mark.find'); r.surroundContents(mk); find.marks.unshift(mk);
    }
    findCount.textContent = find.marks.length ? T('%d found', find.marks.length) : T('none');
    if (find.marks.length) findStep(1, true);
  }
  function findStep(d, first) {
    if (!find.marks.length) return;
    find.marks[find.at]?.classList.remove('cur');
    find.at = first ? 0 : (find.at + d + find.marks.length) % find.marks.length;
    const m = find.marks[find.at]; m.classList.add('cur');
    const pane = Object.entries(panes).find(([, p]) => p.contains(m));
    if (pane && pane[0] !== st.tab) setTab(pane[0]);
    for (let d = m.closest('details'); d; d = d.parentElement.closest('details')) d.open = true; // a folded expand opens to show it
    m.scrollIntoView({ block: 'center' });
    findCount.textContent = find.at + 1 + '/' + find.marks.length;
  }

  // ---- comments
  // Who a comment is for, as the composer and a comment's lock name it.
  const visWho = v => (v.Internal ? T('internal note') : v.Role || v.Group || '');
  const visLimited = v => !!(v.Internal || v.Role || v.Group);
  const visLabel = v => (v.Internal ? T('internal note') : v.Role ? T('only %s', v.Role) : v.Group ? T('only %s', v.Group) : T('everyone'));
  // ui.comment_layout flat: by date, a reply under a line quoting its parent; ui.comment_order newest: latest thread first.
  const uiOpt = k => String((app.session.ui || {})[k] || '').trim().toLowerCase();
  const flat = () => uiOpt('CommentLayout') === 'flat';
  const newest = () => uiOpt('CommentOrder') === 'newest';
  const order = () => {
    const cs = st.issue ? st.issue.Comments : [];
    const byId = new Map(cs.map(c => [c.ID, c]));
    let groups;
    if (flat()) groups = cs.map(c => [{ c, reply: false }]);
    else {
      const kids = new Map(), roots = [];
      for (const c of cs) {
        let p = c.ParentID && byId.get(c.ParentID);
        while (p && p.ParentID && byId.get(p.ParentID)) p = byId.get(p.ParentID);
        if (p) (kids.get(p.ID) || kids.set(p.ID, []).get(p.ID)).push(c); else roots.push(c);
      }
      groups = roots.map(r => [{ c: r, reply: false }, ...(kids.get(r.ID) || []).map(k => ({ c: k, reply: true }))]);
    }
    groups.push(...st.pending.map(p => [{ c: p, reply: false }]));
    if (newest()) groups.reverse();
    return groups.flat();
  };
  // parentRef is a flat reply's parent, quoted: who and the start of what they said.
  const parentRef = c => {
    const p = flat() && c.ParentID && (st.issue.Comments || []).find(x => x.ID === c.ParentID);
    return p ? { id: p.ID, who: p.Author || T('Unknown'), text: String(p.Body || '').replace(/\s+/g, ' ').trim().slice(0, 200) } : null;
  };
  const cmEls = new Map();
  function commentEl(c, reply) {
    const mine = !c.pending && c.AuthorID && c.AuthorID === me().AccountID;
    const lock = c.Visibility && visLimited(c.Visibility) ? visLabel(c.Visibility) : '';
    const lockWho = lock ? visWho(c.Visibility) : '';
    const ref = parentRef(c);
    const sig = [c.Body, c.Author, reply, mine, c.pending, lock, ref && ref.who + ref.text].join('\u0001');
    const hit = cmEls.get(c.ID);
    if (hit && hit.sig === sig) return hit.el;
    const body = h('div.md.cbody');
    body.append(md(c.Body, mdOpts()));
    const act = (a, label, title) => h('button.btn.ghost.sm', { dataset: { act: a }, title }, label);
    const node = h('article.cm' + (reply ? '.reply' : '') + (mine ? '.mine' : '') + (c.pending ? '.pending' : ''), { dataset: { id: c.ID }, tabindex: -1 },
      ref && h('div.cm-ref', { dataset: { parent: ref.id }, title: T('Go to the comment it replies to') }, '↪ ', h('b', ref.who), ': ' + ref.text),
      h('header', ui.avatar(c.Author, null, 22), h('b', c.Author), h('time', { title: dateTime(c.Created) }, c.pending ? T('sending…') : ago(c.Created)),
        lock && h('span.cm-lock', { title: T('Visible to %s', lockWho) }, icon('lock'), ' ' + lockWho),
        h('span.spacer'), !c.pending && h('span.acts', act('reply', T('Reply'), T('Reply (R)')), mine && act('edit', T('Edit'), T('Edit (e)')), mine && act('del', T('Delete'), T('Delete (d)')))),
      body);
    cmEls.set(c.ID, { el: node, sig });
    return node;
  }
  function renderComments() {
    if (!st.issue) return;
    const all = order();
    const total = st.issue.CommentTotal || (st.issue.Comments || []).length;
    const shown = st.all ? all : newest() ? all.slice(0, RECENT) : all.slice(-RECENT);
    if (newest() !== (box.list.nextSibling === box.more)) newest() ? box.list.after(box.more) : box.list.before(box.more); // the older ones' end
    const want = shown.map(({ c, reply }) => commentEl(c, reply));
    const live = new Set(shown.map(s => s.c.ID));
    if (st.editingComment && !live.has(st.editingComment.id)) st.editingComment = null;
    for (const [id, v] of cmEls) if (!live.has(id)) { cmEls.delete(id); v.el.remove(); }
    let at = box.list.firstChild;
    for (const n of want) {
      if (n === at) { at = at.nextSibling; continue; }
      box.list.insertBefore(n, at);
    }
    while (at) { const nx = at.nextSibling; at.remove(); at = nx; }
    for (const n of box.list.children) n.classList.toggle('focus', n.dataset.id === st.focusId);
    clear(box.more);
    if (all.length > shown.length) box.more.append(h('button.btn', { onclick: () => { st.all = true; renderComments(); } }, Tn(all.length - shown.length, 'Show %d earlier comment', 'Show %d earlier comments', all.length - shown.length)));
    else if (total > all.length && !st.pending.length) box.more.append(h('p.faint', Tn(total - all.length, '%d older comment not loaded. Open in Jira to read it.', '%d older comments not loaded. Open in Jira to read them.', total - all.length)));
    if (!all.length) box.list.dataset.empty = T('No comments yet.'); else delete box.list.dataset.empty;
  }
  const commentEls = () => [...box.list.children].filter(n => n.dataset.id && !n.dataset.id.startsWith('tmp-'));
  function focusComment(id, scrollTo = true) {
    st.focusId = id;
    for (const n of box.list.children) n.classList.toggle('focus', n.dataset.id === id);
    const n = id && box.list.querySelector('[data-id="' + CSS.escape(id) + '"]');
    if (n && scrollTo) n.scrollIntoView({ block: 'nearest' });
  }
  function moveComment(d) {
    const els = commentEls(); if (!els.length) return;
    let i = els.findIndex(n => n.dataset.id === st.focusId);
    i = i < 0 ? (d > 0 ? 0 : els.length - 1) : Math.max(0, Math.min(els.length - 1, i + d));
    focusComment(els[i].dataset.id);
  }
  const focused = () => st.issue && (st.issue.Comments || []).find(c => c.ID === st.focusId);
  const isMine = c => c && c.AuthorID && c.AuthorID === me().AccountID;

  delegate(box.list, 'click', 'article.cm', (e, art) => {
    const ref = e.target.closest('.cm-ref'); if (ref) return focusComment(ref.dataset.parent);
    if (!e.target.closest('a,input,img,textarea')) focusComment(art.dataset.id, false);
    const b = e.target.closest('button[data-act]'); if (!b) return;
    const c = (st.issue.Comments || []).find(x => x.ID === art.dataset.id);
    if (!c) return;
    ({ reply: () => replyTo(c), edit: () => editComment(c), del: () => deleteComment(c) })[b.dataset.act]();
  });

  async function editComment(c) {
    if (st.editingComment) return;
    const art = box.list.querySelector('[data-id="' + CSS.escape(c.ID) + '"]'); if (!art) return;
    let ed;
    try { ed = await api.get('/issues/' + key + '/comments/' + encodeURIComponent(c.ID) + '/edit', { fresh: true }); } catch (e) { return fail(e); }
    st.editingComment = { id: c.ID };
    const body = art.querySelector('.cbody');
    const e = editor({ ...docOf(ed), rows: 4, label: T('Save'), draft: 'desc:' + key + ':comment:' + c.ID,
      save: async doc => {
        await api.put('/issues/' + key + '/comments/' + encodeURIComponent(c.ID), { Doc: doc });
        st.editingComment = null; cmEls.delete(c.ID); art.remove(); changed(T('%s comment edited', key));
      },
      cancel: () => { st.editingComment = null; body.hidden = false; e.el.remove(); } });
    body.hidden = true; body.after(e.el); e.focus();
  }
  async function deleteComment(c) {
    if (!await ui.confirm({ title: T('Delete comment'), text: T('Delete this comment by %s?', c.Author), ok: T('Delete'), danger: true })) return;
    try {
      const r = await api.del('/issues/' + key + '/comments/' + encodeURIComponent(c.ID));
      st.issue.Comments = (st.issue.Comments || []).filter(x => x.ID !== c.ID); renderComments(); changed();
      // u posts it again, as you (TUI undoDeleteComment)
      if (r && r.Raw) import('./fields.js').then(m => m.pushUndo(app, T('the deleted comment on %s', key), async () => { await api.post('/issues/' + key + '/comments', { Raw: r.Raw, Parent: c.ParentID || '' }); bus.emit('issue:changed', { key }); }));
    } catch (e) { fail(e); }
  }

  // composer
  const comp = editor({ doc: null, rows: 3, placeholder: T('Write a comment… (@ mentions, / inserts)'), label: T('Comment'), noCancel: true, draft: 'comment:' + key,
    save: async doc => {
      const snap = comp.snapshot(), text = await comp.markdown().catch(() => '');
      const tmp = { ID: 'tmp-' + Date.now(), Author: me().DisplayName || T('You'), AuthorID: me().AccountID, Body: text, Created: new Date(), pending: true };
      const parent = st.reply && threaded() && !desk() ? threadRoot(st.reply) : ''; // a real reply in Jira's thread; on a request a new comment
      st.pending.push(tmp); renderComments(); (newest() ? box.list.firstElementChild : box.list.lastElementChild)?.scrollIntoView({ block: 'nearest' });
      comp.clear(); drafts.delete(key); setReply(null);
      try {
        const found = await postComment(api, key, { Doc: doc, Visibility: parent ? everyone() : st.vis, Parent: parent });
        st.pending = st.pending.filter(p => p !== tmp);
        st.vis = visDefault(); paintVis(); // the next one is for everyone again (an internal note on a request)
        await reload(true); changed(T('%s comment added', key));
        if (found) ui.toast(T('Your comment was on %s already: not posted again', key));
      } catch (e) {
        st.pending = st.pending.filter(p => p !== tmp); renderComments();
        if (parent && !st.reply) setReply(st.issue.Comments.find(c => c.ID === parent) || null);
        comp.restore(snap); drafts.set(key, snap); throw e;
      }
    } });
  if (drafts.get(key)) comp.ready().then(() => comp.restore(drafts.get(key)));
  comp.onInput(() => drafts.set(key, comp.snapshot()));
  const replyChip = h('div.reply-chip', { hidden: true });
  // Jira's threads are one level deep: a reply to a reply goes under that one's parent (jira.ThreadRoot).
  const threadRoot = c => {
    const by = new Map((st.issue.Comments || []).map(x => [x.ID, x])), seen = new Set();
    let id = c.ID;
    while (!seen.has(id)) { seen.add(id); const p = by.get(id) && by.get(id).ParentID; if (!p) break; id = p; }
    return id;
  };
  // ui.threaded_replies (on by default): a reply goes under its comment in Jira's thread.
  const threaded = () => String((app.session.ui || {}).ThreadedReplies || 'on').trim().toLowerCase() !== 'off';
  // Who the comment is for: everyone, an internal note (Service Desk), a role or group the user is in; ctrl+o steps (TUI cycleCommentVis).
  // On a service desk's request: an internal note (the default, so nothing reaches a customer by accident), a reply to the customer, the roles and groups.
  const everyone = () => ({ Internal: false, Customer: false, Role: '', Group: '', GroupID: '' });
  const desk = () => !!(st.req && st.req.Desk);
  const visDefault = () => (desk() ? { ...everyone(), Internal: true } : everyone());
  st.vis = everyone();
  const visBtn = h('button.btn.ghost.sm.vis-chip', { title: modText(T('Who sees it  (ctrl+o)')), onclick: () => cycleVis() });
  const paintVis = () => {
    visBtn.replaceChildren(icon('eye'), ' ' + (st.vis.Customer ? T('reply to the customer: they read this') : desk() && st.vis.Internal ? T('internal note: only agents see it') : visLabel(st.vis)), keyHint('mod+o'));
    visBtn.classList.toggle('on', visLimited(st.vis)); visBtn.classList.toggle('customer', !!st.vis.Customer);
  };
  paintVis();
  let visOpts = null;
  async function cycleVis() {
    if (st.reply && threaded() && !desk()) return ui.toast(T('A reply is for whoever its comment is for'));
    if (!visOpts) {
      try { visOpts = await api.get('/projects/' + encodeURIComponent(key.slice(0, key.lastIndexOf('-'))) + '/commentvis'); } catch (e) { return fail(e); }
      if (!visOpts.length && !desk()) return ui.toast(T("Comments here are for everyone: you're in no role or group to limit them to"));
    }
    const all = desk() ? [{ Internal: true }, { Customer: true }, ...visOpts.filter(v => !v.Internal)] : [everyone(), ...visOpts];
    const visKey = v => [!!v.Internal, !!v.Customer, v.Role || '', v.Group || ''].join('\n');
    const i = all.findIndex(v => visKey(v) === visKey(st.vis));
    st.vis = { ...everyone(), ...all[(i + 1) % all.length] };
    paintVis();
  }
  box.composer.append(replyChip, h('div.vis-row', visBtn), comp.el);
  function setReply(c) {
    st.reply = c;
    replyChip.hidden = !c;
    clear(replyChip);
    const locked = !!c && threaded() && !desk();
    visBtn.disabled = locked; visBtn.title = locked ? T('A reply is for whoever its comment is for') : modText(T('Who sees it  (ctrl+o)'));
    if (c) replyChip.append(h('span', T('Replying to'), ' ', h('b', c.Author)), h('button.btn.ghost.sm', { title: T('Not a reply'), onclick: () => { comp.unmention(c.Author); setReply(null); } }, icon('x')));
  }
  function replyTo(c) {
    setTab('comments');
    setReply(c);
    comp.ready().then(() => { comp.mention({ AccountID: c.AuthorID, DisplayName: c.Author }, true); comp.focus(true); });
  }
  function composeComment() {
    setTab('comments');
    comp.focus();
    comp.el.scrollIntoView({ block: 'nearest' });
  }
  me_.compose = text => {
    setReply(null);
    comp.ready().then(() => comp.setMarkdown(text)).then(() => { drafts.set(key, comp.snapshot()); composeComment(); });
  };
  if (startText != null) setTimeout(() => me_.compose(startText), 0);

  // ---- history tab: time in status, then Changes · Work log · All (comments, changes and work
  // interleaved), newest first, as the TUI's activity tabs; 3 again steps the view, the box filters.
  const HIST = [['changes', T('Changes')], ['worklog', T('Work log')], ['all', T('All')]];
  st.histView = HIST.some(v => v[0] === app.prefs.get('issue.hist', '')) ? app.prefs.get('issue.hist', '') : 'changes';
  st.histQ = '';
  async function loadHistory() {
    if (st.hist) return;
    st.hist = 'loading';
    clear(panes.history).append(h('div.loading', T('Loading history…')));
    const [log, tis, logs] = await Promise.all([
      api.get('/issues/' + key + '/history').catch(e => e), api.get('/issues/' + key + '/timeinstatus').catch(() => null),
      api.get('/issues/' + key + '/worklogs').catch(() => [])]);
    if (dead) return;
    st.hist = log instanceof Error ? null : log; st.tis = tis; st.logs = logs || [];
    renderHistory(log instanceof Error ? log : null);
  }
  function stepHist() {
    const i = HIST.findIndex(v => v[0] === st.histView);
    setHist(HIST[(i + 1) % HIST.length][0]);
  }
  function setHist(v) { st.histView = v; app.prefs.set('issue.hist', v === 'changes' ? '' : v); if (st.hist && st.hist !== 'loading') renderHistory(); }
  function renderHistory(err) {
    const p = clear(panes.history);
    if (err) return p.append(h('div.empty', T('Could not load history: %s', err.message), h('div', h('button.btn', { onclick: () => { st.hist = null; loadHistory(); } }, T('Retry')))));
    const tis = st.tis || [];
    if (tis.length && st.histView === 'changes') {
      const max = Math.max(...tis.map(t => t.Time), 1);
      p.append(h('div.sec-head', h('h3', T('Time in status'))), h('div.tis', tis.map(t => h('div.tis-row' + (t.Now ? '.now' : ''),
        h('span.name', t.Status), h('span.track', h('i', { style: { width: Math.max(2, Math.round(100 * t.Time / max)) + '%' } })),
        h('span.dim', duration(t.Time / 1e9) + (t.Visits > 1 ? ' · ' + t.Visits + '×' : ''))))));
    }
    const counts = { changes: (st.hist || []).length, worklog: st.logs.length, all: (st.hist || []).length + st.logs.length + ((st.issue && st.issue.Comments) || []).length };
    const filter = h('input.input.hist-q', { type: 'search', placeholder: T('Filter: who, field, words'), value: st.histQ, oninput: () => { st.histQ = filter.value; paintList(); } });
    p.append(h('div.hist-bar', h('span.seg', HIST.map(([id, label]) => h('button.btn.sm' + (st.histView === id ? '.on' : ''), { onclick: () => setHist(id), title: T('3 steps through them') }, label, h('span.dim', ' ' + counts[id])))), filter));
    const list = h('div.hist-list');
    p.append(list);
    paintList();
    function paintList() {
      const words = st.histQ.toLowerCase().split(/\s+/).filter(Boolean);
      const items = histItems().filter(it => words.every(w => it.text.includes(w))).sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
      const frag = document.createDocumentFragment();
      for (const it of items) frag.append(it.el());
      if (!items.length) frag.append(h('p.faint', words.length ? T('Nothing matches.') : st.histView === 'worklog' ? T('No work logged.') : T('Nothing recorded.')));
      if (st.histView === 'worklog' && items.length) frag.append(h('p.dim', T('%s in all', duration(items.reduce((n, it) => n + it.secs, 0)))));
      clear(list).append(frag);
    }
  }
  function histItems() {
    const v = st.histView, out = [];
    const low = (...xs) => xs.filter(Boolean).join(' ').toLowerCase();
    if (v !== 'worklog') for (const e of st.hist || []) out.push({ at: e.When, text: low(e.Who, e.What, ...(e.Changes || []).flatMap(c => [c.Field, c.From, c.To])), el: () => changeEl(e) });
    if (v !== 'changes') for (const w of st.logs) out.push({ at: w.Started, secs: w.Seconds, text: low(w.Author, 'logged', w.Comment), el: () => worklogEl(w) });
    if (v === 'all') for (const c of (st.issue && st.issue.Comments) || []) out.push({ at: c.Created, text: low(c.Author, 'commented', c.Body), el: () => h('div.chg', chgHead(c.Author, c.Created, T('commented')), h('div.md.cbody.chg-body', md(c.Body, mdOpts()))) });
    return out;
  }
  const chgHead = (who, at, what) => h('div.chg-head', ui.avatar(who, null, 18), h('b', who || 'Jira'), what && h('span.dim', what), h('time.dim', { title: dateTime(at) }, ago(at)));
  const changeEl = e => h('div.chg', chgHead(e.Who, e.When),
    (e.Changes && e.Changes.length ? e.Changes : [{ Field: '', From: '', To: e.What }]).map(c => (c.From.includes('\n') || c.To.includes('\n')
      ? [h('div.chg-line', h('span.fname', c.Field), h('span.dim', T('changed'))), h('div.chg-diff', lineDiff(c.From, c.To).map(l => h('div.' + (l[0] === '-' ? 'del' : l[0] === '+' ? 'add' : 'more'), l)))]
      : h('div.chg-line', c.Field && h('span.fname', c.Field), c.From || c.To ? changeText(c) : null))));
  const worklogEl = w => h('div.chg', chgHead(w.Author, w.Started, T('logged %s', duration(w.Seconds))), w.Comment && h('div.md.cbody.chg-body', md(w.Comment, mdOpts())));
  const clip = (s, n = 160) => (s.length > n ? s.slice(0, n) + '…' : s);
  const changeText = c => (!c.Field ? h('span', c.To) : [
    c.From ? h('span.from', clip(c.From)) : h('span.faint', T('none')), h('span.arrow', '→'), c.To ? h('span.to', clip(c.To)) : h('span.faint', T('none'))]);

  // ---- editor (description, comment edit, composer): lib/mdedit.js
  function editor(o) { const e = mdEdit(app, { ...o, issueKey: key, people, mdOpts, onFiles: attachFiles }); editors.add(e); return e; }
  async function attachFiles(list) {
    const made = [];
    for (const f of list) {
      try {
        const fd = new FormData(); fd.append('file', f, f.name || 'pasted-' + Date.now() + '.png');
        const res = await fetch('/api/issues/' + key + '/attachments', { method: 'POST', body: fd });
        if (!res.ok) throw new Error(((await res.json().catch(() => ({}))).error) || res.statusText);
        made.push(await res.json().catch(() => null));
        ui.toast(T('Attached %s', f.name || T('image')), { kind: 'ok' });
      } catch (e) { made.push(null); fail(e); }
    }
    changed();
    return made;
  }
  // The editor's start from the server's editable: the document, and its markdown for the Markdown mode.
  const docOf = ed => ({ doc: ed.Doc || null, value: ed.Markdown || '', kept: ed.Kept || [], editable: !!ed.Editable, reason: ed.Reason });

  // ---- data
  let gen = 0;
  async function reload(quiet) {
    const g = ++gen;
    try {
      const [iss, card] = await Promise.all([api.get('/issues/' + key + '?fresh=1', { fresh: true }), api.get('/issues/' + key + '/card', { fresh: true }).catch(() => st.card)]);
      if (dead || g !== gen) return;
      st.issue = iss; if (!iss.Comments) iss.Comments = []; st.card = card; st.children = null; st.hist = null; st.weblinks = null; st.tis = null; st.meta = null;
      paint(); loadExtras(true);
      if (st.tab === 'history') loadHistory();
    } catch (e) { if (!quiet) fail(e); }
  }
  // again: the request too, whose SLAs move (whether it is a desk's stays, and with it who the comment is for).
  async function loadExtras(again) {
    const [kids, web, meta, req] = await Promise.all([st.children ? null : api.get('/issues/' + key + '/children').catch(() => []), st.weblinks ? null : api.get('/issues/' + key + '/weblinks').catch(() => []),
      st.meta ? null : api.get('/issues/' + key + '/editmeta', { fresh: true }).catch(() => null), st.req && !again ? null : api.get('/issues/' + key + '/request', { fresh: true }).catch(() => null)]);
    if (dead) return;
    if (meta) { st.meta = meta; renderFields(); renderDocs(); }
    if (req) { const was = desk(); st.req = req; renderRequest(); if (desk() !== was) { st.vis = visDefault(); paintVis(); } }
    if (kids) { st.children = kids; renderChildren(); }
    if (web) { st.weblinks = web; renderLinks(); }
  }
  function paint() {
    panes.details.classList.remove('waiting');
    renderHead(); renderTabs(); renderFields(); renderRequest(); renderDesc(); renderDocs(); renderChildren(); renderLinks(); renderFiles(); renderComments();
  }

  function loadFailed(e) {
    const gone = e && (e.status === 404 || /does not exist|not found|404/i.test(e.message || ''));
    clear(box.fields).append(h('div.empty',
      h('h2', gone ? T('%s not found', key) : T('Could not load %s', key)),
      h('p.dim', gone ? T('It does not exist, or you cannot see it.') : (e && e.message) || ''),
      h('div', h('button.btn', { onclick: () => { clear(box.fields).append(h('div.loading', T('Loading %s…', key))); reload(true).then(() => { if (!st.issue && !dead) loadFailed(e); }); } }, T('Retry')),
        ' ', h('button.btn.ghost', { onclick: goBack }, full ? T('← Back') : T('Close'), keyHint('Escape')))));
  }

  renderHead(); renderTabs(); setTab('details');
  clear(panes.history);
  box.fields.append(h('div.loading', T('Loading %s…', key)));
  api.swr('/issues/' + key, iss => { if (!dead) { st.issue = iss; if (!iss.Comments) iss.Comments = []; paint(); } }).then(() => { if (!dead) loadExtras(); })
    .catch(e => { if (!dead && !st.issue) loadFailed(e); });
  api.swr('/issues/' + key + '/card', c => { if (!dead) { st.card = c; renderFields(); renderHead(); dev.hint(); } }).catch(() => {});

  let mine = 0, siteTimer = 0;
  const offChanged = bus.on('issue:changed', e => { if (e && e.key === key) { mine = Date.now(); reload(true); } });
  // laneway-server's change stream: someone else's change to this issue (a field, a comment, a worklog,
  // a file, a link) loads it again, once for a burst; the echo of a change made here is skipped.
  const offSite = bus.on('site:change', ch => {
    if (siteTimer || !ch || ch.key !== key || Date.now() - mine < 2000) return;
    siteTimer = setTimeout(() => { siteTimer = 0; if (!dead) reload(true); }, 500);
  });
  const offFocus = bus.on('focus', () => reload(true));
  const offTimer = [bus.on('timer', () => { if (!dead) renderHead(); }), bus.on('timer:tick', () => { if (!dead && app.timer.mark(key)) renderHead(); })];

  // ---- keys
  const G = T('Issue');
  scope.bind('Escape', e => {
    if (find.on && !(e.target.closest && e.target.closest('.ed'))) return findClose();
    const ed = e.target.closest && e.target.closest('.ed');
    if (ed) { if (ed._escape && ed._escape()) return; if (ed._cancel) return ed._cancel(); }
    if (document.activeElement && document.activeElement.matches && document.activeElement.matches('input,textarea,select')) return document.activeElement.blur();
    goBack();
  }, full ? T('back') : T('close panel'), { group: G, input: true });
  scope.bind('mod+o', e => { if (comp.contains(e.target)) cycleVis(); }, T('who sees the comment: everyone, an internal note, a role or group'), { group: G, input: true });
  scope.bind('mod+Enter', e => { const ed = e.target.closest && e.target.closest('.ed'); if (ed && ed._save) ed._save(); }, T('save / send'), { group: G, input: true });
  scope.bind(['j', 'ArrowDown'], () => (st.tab === 'comments' ? moveComment(1) : st.tab === 'terminal' ? term.scroll(3) : scroll.scrollBy({ top: 80 })), T('next comment / scroll down'), { group: G });
  scope.bind(['k', 'ArrowUp'], () => (st.tab === 'comments' ? moveComment(-1) : st.tab === 'terminal' ? term.scroll(-3) : scroll.scrollBy({ top: -80 })), T('previous comment / scroll up'), { group: G });
  scope.bind('ctrl+d', () => scroll.scrollBy({ top: scroll.clientHeight / 2 }), T('half page down'), { group: G, hidden: true });
  scope.bind('ctrl+u', () => scroll.scrollBy({ top: -scroll.clientHeight / 2 }), T('half page up'), { group: G, hidden: true });
  scope.bind('c', composeComment, T('write a comment'), { group: G, bar: T('comment') });
  scope.bind('e', () => { const c = st.tab === 'comments' && focused(); if (c && isMine(c)) editComment(c); else edit('summary'); }, T('edit summary (or own focused comment)'), { group: G });
  scope.bind('E', editDesc, T('edit description'), { group: G, bar: T('description') });
  scope.bind('a', () => edit('assignee'), T('change assignee'), { group: G, bar: T('assign') });
  scope.bind('p', () => edit('priority'), T('change priority'), { group: G });
  scope.bind('P', () => edit('points'), T('set story points'), { group: G });
  scope.bind('l', () => edit('labels'), T('edit labels'), { group: G });
  scope.bind('r', () => { reload(true); dev.refresh(); }, T('refresh'), { group: G });
  scope.bind('R', () => { const c = focused() || (st.issue && st.issue.Comments[(st.issue.Comments || []).length - 1]); if (c) replyTo(c); }, T('reply to comment'), { group: G });
  scope.bind('d', () => { const c = st.tab === 'comments' && focused(); if (c && isMine(c)) deleteComment(c); }, T('delete own comment'), { group: G });
  scope.bind('L', addLink, T('link an issue'), { group: G });
  scope.bind('A', () => actions(), T('issue actions: subtask, clone, move, watchers…'), { group: G, bar: T('actions') });
  scope.bind('[', () => step(-1), T('previous issue in the list'), { group: G, bar: T('issue') });
  scope.bind(']', () => step(1), T('next issue in the list'), { group: G, bar: T('issue') });
  scope.bind('Backspace', back, T('back along the trail of followed issues'), { group: G });
  scope.bind('G', linked, T('go to a linked issue, child or web link'), { group: G });
  scope.bind('i', () => gallery(0), T('view images'), { group: G });
  scope.bind('/', () => findOpen(), T('find in the issue (n / N next, previous)'), { group: G });
  scope.bind('n', () => findStep(1), T('next match'), { group: G, hidden: true, when: () => find.on && (full || app.panel.focused()) });
  scope.bind('N', () => findStep(-1), T('previous match'), { group: G, hidden: true, when: () => find.on && (full || app.panel.focused()) });
  scope.bind('1', () => setTab('details'), T('details tab'), { group: G, bar: T('tabs') });
  scope.bind('2', () => setTab('comments'), T('comments tab'), { group: G, bar: T('tabs') });
  scope.bind('3', () => (st.tab === 'history' ? stepHist() : setTab('history')), T('history tab (again: work log, all)'), { group: G, bar: T('tabs') });
  scope.bind('4', () => openTerm(false), T('terminal tab: the issue\'s agent (again: the next agent)'), { group: G, when: () => (term.has() || st.tab === 'terminal') && (full || app.panel.focused()) });
  scope.bind('ctrl+\\', () => openTerm(true), T('type into the issue\'s agent (its terminal here; ctrl+\\ there back to the issue)'), { group: G });
  scope.bind('Enter', () => term.type(), T('type into the terminal'), { group: G, when: () => st.tab === 'terminal' && (full || app.panel.focused()) });
  scope.bind('t', () => term.type({ takeover: true }), T('take over the agent\'s input from another herdr attach'), { group: G, when: () => st.tab === 'terminal' && (full || app.panel.focused()), writes: true });
  scope.bind('y', () => copy(key, key), T('copy key'), { group: G, writes: true });
  scope.bind('*', () => pins.toggle(app, key, st.issue && st.issue.Summary), T('pin / unpin issue (first in the palette)'), { group: G, writes: true });
  scope.bind('Y', () => copy(browseURL(), 'Link'), T('copy link'), { group: G, writes: true });
  if (elsewhere) scope.bind('o', () => window.open(browseURL(), '_blank', 'noopener'), T('open in Jira'), { group: G, bar: 'Jira', writes: true });
  scope.bind('s', () => edit('status'), T('change status'), { group: G, bar: T('status') });

  // ---- resize (panel only): ui.panel_width (50%) until a drag or < > picks another, 20–80%; near
  // ui.panel_width a drag snaps to it and < > stop there on the way past (TUI panel_resize.go).
  if (!full) {
    const pw = Number(app.session.ui && app.session.ui.PanelWidth);
    const def = pw >= 20 && pw <= 80 ? pw : 50;
    const show = p => el.style.setProperty('--panel-w', p + '%'); // on <html> it would restyle every element
    const keep = p => { pct = p; show(p); app.prefs.set('panelPct', p === def ? '' : String(p)); };
    const clamp = grip(root.querySelector('.iss-grip'), { def, show, keep, pct: ev => (el.getBoundingClientRect().right - ev.clientX) * 100 / window.innerWidth });
    const px = Number(app.prefs.get('panelW', '')); // the width in pixels, before percentages
    if (px) { app.prefs.set('panelW', ''); app.prefs.set('panelPct', String(clamp(px * 100 / window.innerWidth))); }
    const saved = Number(app.prefs.get('panelPct', ''));
    let pct = saved >= 20 && saved <= 80 ? saved : def;
    show(pct);
    const stepWidth = d => {
      let next = clamp(pct + d * 5);
      if ((pct < def) !== (next < def) && pct !== def && next !== def) next = def;
      keep(next);
      ui.toast(next === def ? T('Panel %d%% (ui.panel_width)', next) : T('Panel %d%%', next));
    };
    scope.bind('<', () => stepWidth(1), T('widen the panel'), { group: G, writes: true });
    scope.bind('>', () => stepWidth(-1), T('narrow the panel'), { group: G, writes: true });
  }
  // A dialog opened while the panel loaded keeps the focus.
  if (!document.querySelector('[aria-modal="true"]')) scroll.focus({ preventScroll: true });
  if (startTerm) openTerm(true);

  return () => {
    dead = true; scope.dispose(); offChanged(); offSite(); clearTimeout(siteTimer); offFocus(); offTimer.forEach(f => f());
    term.dispose(); el.classList.remove('term-on');
    for (const e of editors) e.dispose();
    if (offNotes) offNotes();
    dev.dispose();
    drafts.set(key, comp.snapshot());
    if (here === me_) here = null;
    if (!full) document.title = 'laneway';
  };
}

// Sets the nth task checkbox of markdown (outside code fences) done or not.
function flipTask(md, n, done) {
  if (!md) return md;
  let i = 0, fence = false;
  return md.split('\n').map(l => {
    if (/^\s*```/.test(l)) fence = !fence;
    if (fence) return l;
    return l.replace(/^(\s*[-*+]\s+)\[([ xX])\](\s)/, (m, pre, _box, sp) => (++i === n ? pre + (done ? '[x]' : '[ ]') + sp : m));
  }).join('\n');
}
