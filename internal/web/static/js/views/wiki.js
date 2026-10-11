// Wiki: the site's Confluence (or laneway-server's wiki). Spaces, a space's page tree beside the page, the page as it
// reads, edited in the description's editor (visual or markdown), pages made under it, its comments and replies, its
// history (wiki_history.js, ?v= the version shown) and a search over the pages' text.
import { h, clear, debounce } from '../lib/dom.js';
import * as store from '../lib/store.js';
import { css } from '../lib/css.js';
import { icon } from '../lib/icons.js';
import { render as md } from '../lib/md.js';
import { mdEdit } from '../lib/mdedit.js';
import { ago } from '../lib/fmt.js';
import { T } from '../lib/i18n.js';
import { grip } from '../lib/grip.js';
import { openHistory } from './wiki_history.js';

const LAST = 'wiki.space'; // the space opened last, per site

export default async function mount(el, { app, params, query, scope }) {
  css('wiki'); css('issue');
  const { api, ui } = app;
  const side = h('aside.wk-side');
  const main = h('main.wk-main');
  const gripEl = h('div.wk-grip', { title: T('Drag to resize'), role: 'separator' });
  const rail = h('div.wkh-rail', { hidden: true }); // a history's changes, beside the scrollbar
  const wk = h('div.wk', side, gripEl, main, rail);
  el.append(wk);
  // The tree's share of the width, kept in prefs (20% by default).
  const showSide = p => wk.style.setProperty('--wk-side', p + '%');
  const savedSide = Number(app.prefs.get('wikiSide', ''));
  showSide(savedSide >= 10 && savedSide <= 50 ? savedSide : 20);
  grip(gripEl, { def: 20, min: 10, max: 50, show: showSide, keep: p => { showSide(p); app.prefs.set('wikiSide', p === 20 ? '' : String(p)); },
    pct: ev => { const b = wk.getBoundingClientRect(); return (ev.clientX - b.left) * 100 / b.width; } });
  let dead = false, editor = null, hist = null;
  const cleanup = () => { dead = true; if (editor) editor.dispose(); if (hist) hist.dispose(); };

  let spaces;
  try { spaces = await api.get('/wiki/spaces'); } catch (e) { main.append(h('div.empty', h('h2', T('No wiki')), h('p', e.message))); return cleanup; }
  if (dead) return cleanup;
  if (!spaces.length) { main.append(h('div.empty', h('h2', T('No wiki spaces')), h('p', T('Make a space on the site; it shows here.')))); return cleanup; }
  const space = spaces.find(s => s.ID === params.space) || spaces.find(s => s.ID === store.get(LAST)) || spaces[0];
  store.set(LAST, space.ID);

  // ---- the side: the space, a search, the tree
  const pick = h('select.input', { 'aria-label': T('Space'), onchange: e => app.go('/wiki/' + e.target.value) },
    spaces.map(s => h('option', { value: s.ID, selected: s.ID === space.ID }, s.Name)));
  const q = h('input.input', { type: 'search', placeholder: T('Search the wiki…'), 'aria-label': T('Search the wiki') });
  const hits = h('div.wk-hits', { hidden: true });
  const tree = h('nav.wk-tree', { 'aria-label': T('Pages') }, h('div.dim', T('Loading…')));
  side.append(pick, q, hits, tree,
    h('button.btn.ghost.sm', { onclick: () => newPage('') }, icon('plus'), ' ', T('New page')));
  let searchSeq = 0;
  q.addEventListener('input', debounce(async () => {
    const text = q.value.trim(), n = ++searchSeq;
    if (!text) { hits.hidden = true; tree.hidden = false; return; }
    let rs;
    try { rs = await api.get('/wiki/search?q=' + encodeURIComponent(text) + '&space=' + encodeURIComponent(space.Key)); } catch (e) { rs = null; ui.errToast(e); }
    if (n !== searchSeq || dead) return;
    tree.hidden = true; hits.hidden = false;
    clear(hits).append(...(rs && rs.length ? rs.map(r => h('a.wk-hit', { href: '/wiki/' + space.ID + '/' + r.ID, onclick: go(r.ID) },
      h('span.wk-hit-t', r.Title), r.SpaceKey ? h('span.dim', ' · ' + r.SpaceKey) : null, r.Excerpt ? h('div.dim.wk-hit-x', r.Excerpt) : null))
      : [h('div.dim', T('Nothing found.'))]));
  }, 250));

  let pages = [];
  const byParent = new Map();
  async function loadTree() {
    try { pages = await api.get('/wiki/spaces/' + space.ID + '/pages', { fresh: true }); } catch (e) { clear(tree).append(h('p.err', e.message)); return; }
    if (dead) return;
    byParent.clear();
    for (const p of pages) { const k = p.ParentID || ''; if (!byParent.has(k)) byParent.set(k, []); byParent.get(k).push(p); }
    for (const l of byParent.values()) l.sort((a, b) => (a.Position - b.Position) || a.Title.localeCompare(b.Title));
    drawTree();
  }
  const open = new Set(); // pages whose children show
  const go = id => e => { if (e) e.preventDefault(); app.go('/wiki/' + space.ID + '/' + id); };
  function drawTree() {
    const current = params.page || space.HomepageID;
    // The path to the page shown opens.
    for (let p = pages.find(x => x.ID === current); p && p.ParentID; p = pages.find(x => x.ID === p.ParentID)) open.add(p.ParentID);
    const branch = parent => {
      const kids = byParent.get(parent) || [];
      if (!kids.length) return null;
      return h('ul', kids.map(p => {
        const has = byParent.has(p.ID), on = open.has(p.ID);
        const tog = has ? h('button.wk-tog', { type: 'button', 'aria-label': on ? T('Collapse') : T('Expand'), 'aria-expanded': String(on),
          onclick: () => { on ? open.delete(p.ID) : open.add(p.ID); drawTree(); } }, icon(on ? 'chevron-down' : 'chevron-right')) : h('span.wk-tog');
        return h('li', h('div.wk-row' + (p.ID === current ? '.on' : ''), tog,
          h('a', { href: '/wiki/' + space.ID + '/' + p.ID, onclick: go(p.ID), 'aria-current': p.ID === current ? 'page' : null }, p.Title)),
          has && on ? branch(p.ID) : null);
      }));
    };
    clear(tree).append(branch('') || h('div.dim', T('No pages yet.')));
  }

  // ---- the page
  const pageImage = n => '/api/confluence/images/' + encodeURIComponent(n);
  const crumbs = id => {
    const out = [];
    for (let p = pages.find(x => x.ID === id); p && p.ParentID; p = pages.find(x => x.ID === p.ParentID)) {
      const parent = pages.find(x => x.ID === p.ParentID);
      if (parent) out.unshift(h('a', { href: '/wiki/' + space.ID + '/' + parent.ID, onclick: go(parent.ID) }, parent.Title));
    }
    return out.length ? h('div.wk-crumbs.dim', out.flatMap((a, i) => (i ? [' / ', a] : [a]))) : h('div.wk-crumbs');
  };

  async function showPage(id) {
    if (editor) { editor.dispose(); editor = null; }
    if (!id) { clear(main).append(h('div.empty', h('h2', space.Name), h('p', T('This space has no home page.')))); return; }
    clear(main).append(h('div.dim', T('Loading…')));
    let p;
    try { p = await api.get('/confluence/pages/' + id, { fresh: true }); } catch (e) { clear(main).append(h('p.err', e.message)); return; }
    if (dead) return;
    const body = h('div.md.wk-body');
    body.append(p.Markdown && p.Markdown.trim() ? md(p.Markdown, { pageImage }) : h('p.faint', T('This page is empty. Press e to write it.')));
    const comments = h('section.wk-comments', h('h3', T('Comments')));
    clear(main).append(
      crumbs(id),
      h('div.wk-head', h('h1', p.Title), h('span.wk-acts',
        h('button.btn', { onclick: () => edit(id), title: T('Edit (e)') }, icon('pencil'), ' ', T('Edit')),
        h('button.btn.ghost', { onclick: () => newPage(id), title: T('New page under this one (n)'), 'aria-label': T('New page') }, icon('plus')),
        h('button.btn.ghost', { onclick: () => showHistory(id), title: T('Its versions, and what each changed (h)'), 'aria-label': T('History') }, icon('rotate-ccw-clock')),
        p.URL ? h('a.btn.ghost', { href: p.URL, target: '_blank', rel: 'noopener noreferrer', title: T('Open on the site'), 'aria-label': T('Open on the site') }, icon('external-link')) : null,
        h('button.btn.ghost', { onclick: () => remove(id, p.Title), title: T('Move to the trash'), 'aria-label': T('Move to the trash') }, icon('trash-2')))),
      body, comments);
    body.ondblclick = e => { if (!e.target.closest('a,input,img,summary')) edit(id); };
    loadComments(id, comments);
  }

  async function loadComments(id, box) {
    let cs;
    try { cs = await api.get('/wiki/pages/' + id + '/comments', { fresh: true }); } catch (e) { box.append(h('p.err', e.message)); return; }
    if (dead) return;
    const one = (c, reply) => h('div.wk-c' + (reply ? '.wk-reply' : ''),
      h('div.dim.wk-c-who', c.Author || c.AuthorID, ' · ', c.Created ? ago(Date.parse(c.Created)) : ''),
      h('div.md', md(c.Markdown || '')),
      reply ? null : h('button.btn.ghost.sm', { onclick: e => compose(box, id, c.ID, e.target) }, T('Reply')),
      ...(c.Replies || []).map(r => one(r, true)));
    clear(box).append(h('h3', T('Comments')), ...(cs.length ? cs.map(c => one(c, false)) : [h('p.faint', T('No comments yet.'))]));
    compose(box, id, '', null);
  }
  function compose(box, id, parent, after) {
    const e = mdEdit(app, { doc: null, rows: 3, placeholder: parent ? T('Write a reply…') : T('Write a comment… (@ mentions, / inserts)'),
      label: parent ? T('Reply') : T('Comment'), noCancel: !parent, cancel: () => e.el.remove(), draft: 'wiki:' + id + ':comment:' + (parent || 'new'),
      save: async doc => { await api.post('/wiki/pages/' + id + '/comments', { Doc: doc, Parent: parent }); loadComments(id, box); } });
    if (after) { after.replaceWith(e.el); e.focus(); } else box.append(e.el);
  }

  // ---- the history: the page as it read at each version, the changes marked
  function showHistory(id, version) {
    if (editor || hist || !id) return;
    hist = openHistory(app, { main, rail, id, version, pageImage, head: () => crumbs(id),
      setVersion: n => app.setQuery({ v: n }),
      close: () => leaveHistory(id),
      restored: async () => { leaveHistory(id); await loadTree(); } });
  }
  function leaveHistory(id) {
    if (!hist) return;
    hist.dispose(); hist = null;
    app.setQuery({ v: null });
    main.scrollTop = 0;
    showPage(id);
  }
  const inHistory = () => !!hist;

  async function edit(id) {
    if (editor) return;
    if (hist) { ui.toast(T('The history only looks  ·  esc back to now')); return; }
    let d;
    try { d = await api.get('/wiki/pages/' + id + '/edit', { fresh: true }); } catch (e) { return ui.errToast(e); }
    if (dead) return;
    const title = h('input.input.wk-title', { value: d.Title, 'aria-label': T('Title') });
    editor = mdEdit(app, { doc: d.Doc || null, value: d.Markdown || '', kept: d.Kept || [], editable: !!d.Editable, reason: d.Reason,
      rows: 18, label: T('Save'), allowEmpty: true, draft: 'wiki:' + id, placeholder: T('Write the page… (/ inserts)'),
      cancel: () => { editor.dispose(); editor = null; showPage(id); },
      save: async doc => {
        try { await api.put('/wiki/pages/' + id, { Title: title.value, Doc: doc, Version: d.Version }); }
        catch (e) { if (e.status === 409) { ui.toast(T('Someone saved this page meanwhile: copy your text, then reload.'), { kind: 'err' }); return false; } throw e; }
        editor.dispose(); editor = null;
        await loadTree(); showPage(id); ui.toast(T('Page saved'));
      } });
    clear(main).append(crumbs(id), h('div.wk-edit', title, editor.el));
    editor.focus();
  }

  async function newPage(parent) {
    if (editor) return;
    if (hist) { hist.dispose(); hist = null; app.setQuery({ v: null }); }
    const title = h('input.input.wk-title', { placeholder: T('Title'), 'aria-label': T('Title') });
    editor = mdEdit(app, { doc: null, rows: 18, label: T('Create'), allowEmpty: true, draft: 'wiki:new:' + space.ID + ':' + (parent || 'top'),
      placeholder: T('Write the page… (/ inserts)'),
      cancel: () => { editor.dispose(); editor = null; showPage(params.page || space.HomepageID); },
      save: async doc => {
        if (!title.value.trim()) { ui.toast(T('A page needs a title'), { kind: 'err' }); title.focus(); return false; }
        const r = await api.post('/wiki/pages', { SpaceID: space.ID, ParentID: parent, Title: title.value, Doc: doc });
        editor.dispose(); editor = null;
        await loadTree(); app.go('/wiki/' + space.ID + '/' + r.ID);
      } });
    const under = parent && pages.find(p => p.ID === parent);
    clear(main).append(h('h2', under ? T('New page under %s', under.Title) : T('New page in %s', space.Name)), h('div.wk-edit', title, editor.el));
    title.focus();
  }

  async function remove(id, name) {
    if (!await ui.confirm({ title: T('Move “%s” to the trash?', name), text: T('Its pages below move up. The site can restore it from the space’s trash.'), ok: T('Move to the trash'), danger: true })) return;
    try { await api.del('/wiki/pages/' + id); } catch (e) { return ui.errToast(e); }
    const p = pages.find(x => x.ID === id);
    await loadTree();
    app.go('/wiki/' + space.ID + (p && p.ParentID ? '/' + p.ParentID : ''));
  }

  scope.bind('e', () => { if (!editor) edit(params.page || space.HomepageID); }, T('Edit the page'));
  scope.bind('n', () => { if (!editor) newPage(params.page || ''); }, T('New page under this one'), { when: () => !hist });
  scope.bind('h', () => (hist ? leaveHistory(params.page || space.HomepageID) : showHistory(params.page || space.HomepageID)), T('History: the page’s versions and their changes'));
  scope.bind('ArrowLeft', () => hist.step(-1), T('History: an older version'), { when: inHistory });
  scope.bind('ArrowRight', () => hist.step(1), T('History: a newer version'), { when: inHistory });
  scope.bind('d', () => hist.diff(), T('History: the changes or the clean page'), { when: inHistory });
  scope.bind('n', () => hist.jump(1), T('History: next change'), { when: inHistory });
  scope.bind('N', () => hist.jump(-1), T('History: previous change'), { when: inHistory });
  scope.bind('Escape', () => leaveHistory(params.page || space.HomepageID), T('History: back to now'), { when: inHistory });
  scope.bind('/', () => q.focus(), T('Search the wiki'));

  await loadTree();
  const v = Number(query && query.v);
  if (!dead && v > 0 && (params.page || space.HomepageID)) showHistory(params.page || space.HomepageID, v);
  else if (!dead) showPage(params.page || space.HomepageID);
  return cleanup;
}
