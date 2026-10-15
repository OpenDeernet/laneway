// The text editor of comments, descriptions and rich-text fields, in two modes, switched in its toolbar
// (kept per browser): Visual (lib/rte.js: Jira's document as it shows, edited in place) and Markdown,
// the markdown drawn as rich text, its markers shown only on the caret's line (lib/mdarea.js,
// lib/mdhl.js; Source shows them all): enter continues a list, quote or table, tab nests a list item or
// steps through a table's cells, a task box clicks, a marker typed over a selection wraps it, pasted
// HTML comes in as markdown (lib/html2md.js), alt+↑↓ move lines, ctrl+click follows a link; a
// formatting toolbar, a `/` menu for Jira formatting, `@` mentions, `:` emoji, ctrl+b/i/k, ctrl+p
// preview. Both: full screen, pasted or dropped files, drafts.
//
//   const e = mdEdit(app, {doc | value, kept, editable, reason, rows, placeholder, mono, issueKey, project, label, save, cancel,
//                          allowEmpty, noCancel, wiki (a Confluence page or comment: kept nodes say so), people() → Map(name → accountId), mdOpts() → render options, onFiles(files), hint,
//                          draft: an id ("comment:KEY", "desc:KEY", "desc:KEY:comment:ID") kept in the state file as the TUI's drafts,
//                          base: the Base of the document it edits, kept with the draft and handed to save})
//   With doc (ADF; null for none) it edits a document: save(doc, base); kept, editable and reason are the
//   server's markdown of it (editable), for the Markdown mode. With value (markdown) it saves markdown:
//   save(text, mentions, base). onFiles uploads files and answers each one's attachment ({ID, Filename, MediaID} or null).
//   e.el (with ._save ._cancel ._escape for the Escape/ctrl+Enter bindings), e.focus(), e.isEmpty(), e.clear(), e.onInput(fn),
//   e.value() → Promise of the doc, or {text, mentions}; e.current() → {doc} or {text, mentions, kept} as it is now (no conversion;
//   kept: the nodes its placeholders name, to send along),
//   e.doc() (Visual, else null), e.text() (Markdown, else ''), e.mode(), e.ready() → Promise of the Visual mode mounted,
//   e.set(doc | markdown), e.setMarkdown(text), e.markdown() → Promise of it as markdown to read, e.mention(user, prepend),
//   e.unmention(name), e.snapshot(), e.restore(s), e.contains(el), e.preview(), e.dispose(), e.dropDraft(), e.rebase(base)
//   save answering false keeps the editor and its draft (a save Jira refused).
import { h, clear, debounce, onLeave } from './dom.js';
import { icon } from './icons.js';
import { css } from './css.js';
import { render as md, glyph } from './md.js';
import { jiraKey } from './issuepill.js';
import { mdArea } from './mdarea.js';
import { lines, enter, indent, toggleTask, pasteLink, tableTab, tableArrow, moveLines, wrapWith, backspace, inFence as fenced } from './mdhl.js';
import { htmlToMd, rich } from './html2md.js';
import { T } from './i18n.js';
import { mentionsIn } from './comment.js';

css('mdedit');

const today = () => new Date().toISOString().slice(0, 10);
const SOURCE = 'laneway.editor.source'; // Markdown mode shows the markdown as typed, not rendered
const MODE = 'laneway.editor.mode'; // 'markdown', or Visual
const pref = (k, d) => { try { return localStorage.getItem(k) || d; } catch (e) { return d; } };
const setPref = (k, v) => { try { localStorage.setItem(k, v); } catch (e) { /* this time only */ } };
const source = () => pref(SOURCE, '0') === '1';
const PANELS = ['info', 'note', 'success', 'warning', 'error'];
// The visual editor (ProseMirror, ~170 KB) loads with the first editor that shows it.
let rteModule = null;
const loadRte = () => (rteModule = rteModule || import('./rte.js'));
const EMPTY = { type: 'doc', version: 1, content: [] };

// Slash commands: apply(ctl) runs with the typed "/query" already removed.
const SLASH = [
  { name: 'h1', label: T('Heading 1'), run: c => c.line('# ') },
  { name: 'h2', label: T('Heading 2'), run: c => c.line('## ') },
  { name: 'h3', label: T('Heading 3'), run: c => c.line('### ') },
  { name: 'bullet', label: T('Bulleted list'), run: c => c.line('- ') },
  { name: 'numbered', label: T('Numbered list'), run: c => c.line('1. ') },
  { name: 'task', label: T('Task list'), run: c => c.line('- [ ] ') },
  { name: 'code', label: T('Code block'), run: c => c.block('```\n', '\n```', '') },
  { name: 'quote', label: T('Quote'), run: c => c.line('> ') },
  { name: 'rule', label: T('Horizontal rule'), run: c => c.insert('---\n\n') },
  { name: 'table', label: T('Table'), run: c => c.insert('| Header | Header |\n| --- | --- |\n| Cell | Cell |\n', 2, 8) },
  ...PANELS.map(p => ({ name: p, label: T('Panel: %s', p), run: c => c.block('<!-- panel:' + p + ' -->\n\n', '\n\n<!-- /panel -->', 'Text') })),
  { name: 'expand', label: T('Expand (collapsible)'), run: c => c.block('<!-- expand: Title -->\n\n', '\n\n<!-- /expand -->', 'Text') },
  { name: 'mention', label: T('Mention someone'), run: c => c.insert('@') },
  { name: 'issue', label: T('Issue reference'), run: c => c.issueRef() },
  { name: 'emoji', label: T('Emoji'), run: c => c.insert(':') },
  { name: 'link', label: T('Link'), run: c => c.link() },
  { name: 'date', label: T('Today’s date'), run: c => c.insert('<date>' + today() + '</date> ') },
  { name: 'decision', label: T('Decision'), run: c => c.line('<> ') },
  ...[['grey', 'neutral'], ['purple', 'purple'], ['blue', 'blue'], ['red', 'red'], ['yellow', 'yellow'], ['green', 'green']]
    .map(([label, color]) => ({ name: 'status' + label, label: T('Status: %s', label), run: c => c.wrap('<status color="' + color + '">', '</status>', 'DONE') })),
  { name: 'underline', label: T('Underline'), run: c => c.wrap('<u>', '</u>', 'text') },
  ...[['blue', '#0747a6'], ['teal', '#008da6'], ['green', '#006644'], ['orange', '#ff991f'], ['red', '#bf2600'], ['purple', '#403294'], ['grey', '#97a0af']]
    .map(([label, hex]) => ({ name: 'colour' + label, label: T('Text colour: %s', label), run: c => c.wrap('<span style="color:' + hex + '">', '</span>', 'text') })),
];

// Who a document mentions, as {AccountID, DisplayName}.
const mentionsOf = doc => {
  const out = [];
  const walk = n => {
    if (!n || typeof n !== 'object') return;
    if (n.type === 'mention' && n.attrs && n.attrs.id && !out.some(m => m.AccountID === n.attrs.id)) out.push({ AccountID: n.attrs.id, DisplayName: String(n.attrs.text || '').replace(/^@/, '') });
    (n.content || []).forEach(walk);
  };
  walk(doc);
  return out;
};
const hasContent = doc => !!(doc && Array.isArray(doc.content) && doc.content.length);

export function mdEdit(app, o) {
  const { api, ui } = app;
  const adf = 'doc' in o; // edits a document (ADF), not markdown
  let kept = (o.kept || []).slice(); // the Markdown mode's placeholders: the nodes markdown can't carry
  const editable = o.editable !== false;
  // Markdown when that is the pref and it can hold the document (one given only as a document starts Visual).
  let mode = pref(MODE, 'visual') === 'markdown' && (!adf || (editable && !(hasContent(o.doc) && !(o.value || '').trim()))) ? 'markdown' : 'visual';
  let keptMoved = false; // kept is a switch's, not the one the editor opened with: a Markdown draft goes as the document
  const listeners = [];
  const changed = () => { touched = true; for (const f of listeners) f(); draft.save(); };
  let touched = false;

  const mentions = [];
  const glyphs = new Map(); // emoji taken here → glyph, for the preview
  // Longest first, so "@Ann Lee" wins over "@Ann".
  const names = () => [...new Set([...mentions.map(m => m.DisplayName), ...(o.people ? [...o.people()] : []).map(([n]) => n)])].filter(Boolean).sort((a, b) => b.length - a.length);
  const cache = new Map(); // a line → its HTML, while the names and the emoji table stay the same
  let cacheSig = '';
  // An attachment's picture, as the panel draws it (the page loads no other images).
  const img = u => {
    const m = u.startsWith('attachment:') && o.mdOpts && o.mdOpts();
    return m && m.attachment ? m.attachment(u.slice(11)) : '';
  };
  // Pasted HTML (not a code editor's, not into a code block) comes in as markdown.
  const paste = (v, a, b, t, html) => {
    const link = t && pasteLink(v, a, b, t);
    if (link) return link;
    if (!html || !rich(html) || fenced(v, a)) return null;
    const m = htmlToMd(new DOMParser().parseFromString(html, 'text/html').body);
    if (!m || m.replace(/\s+/g, ' ').trim() === t.replace(/\s+/g, ' ').trim()) return null;
    return { from: a, to: b, text: m, a: a + m.length, b: a + m.length };
  };
  const ta = mdArea('div.input.ed-ta' + (o.mono ? '.mono' : '') + (source() ? '' : '.live'), { value: adf && mode === 'visual' ? '' : o.value, rows: o.rows || 4, placeholder: o.placeholder,
    lines: t => {
      const ns = names(), sig = ns.join('\u0001') + (glyph('smile') ? '+' : '');
      if (sig !== cacheSig) { cache.clear(); cacheSig = sig; }
      return lines(t, { names: ns, emoji: glyph, img, cache, wiki: o.wiki });
    }, enter, paste, type: wrapWith });
  const pop = h('div.mention-pop', { hidden: true, role: 'listbox' });
  const preview = h('div.md.ed-preview', { hidden: true });
  const fileIn = h('input', { type: 'file', multiple: true, hidden: true, onchange: () => { if (fileIn.files.length) files([...fileIn.files]); fileIn.value = ''; } });
  const go = h('button.btn.primary', { onclick: () => run() }, o.label || T('Save'));

  // ---- text operations: each one undo step
  const replace = (from, to, text, s0 = text.length, s1 = s0) => {
    ta.focus({ preventScroll: true });
    ta.edit({ from, to, text, a: from + s0, b: from + s1 });
  };
  const sel = () => [ta.selectionStart, ta.selectionEnd];
  const lineStart = i => ta.value.lastIndexOf('\n', i - 1) + 1;
  const ctl = {
    insert(text, s0, s1) { const [a, b] = sel(); replace(a, b, text, s0 != null ? s0 : text.length, s1); },
    // Prefix the current line(s); a line already carrying it loses it.
    line(prefix) {
      const [a, b] = sel(), v = ta.value, from = lineStart(a);
      let end = v.indexOf('\n', b > a ? b - 1 : b); if (end < 0) end = v.length;
      const lines = v.slice(from, end).split('\n');
      const all = lines.every(l => l.startsWith(prefix));
      const out = lines.map(l => (all ? l.slice(prefix.length) : prefix + l.replace(/^(#{1,6} |[-*] (\[[ x]\] )?|\d+\. |> )/, ''))).join('\n');
      replace(from, end, out, out.length);
    },
    wrap(pre, post, ph) {
      const [a, b] = sel(), t = ta.value.slice(a, b);
      if (t.startsWith(pre) && t.endsWith(post) && t.length >= pre.length + post.length) return replace(a, b, t.slice(pre.length, t.length - post.length), t.length - pre.length - post.length);
      const inner = t || ph;
      replace(a, b, pre + inner + post, pre.length, pre.length + inner.length);
    },
    block(pre, post, ph) {
      const [a, b] = sel(), t = ta.value.slice(a, b) || ph;
      const lead = a > 0 && ta.value[a - 1] !== '\n' ? '\n' : '';
      replace(a, b, lead + pre + t + post + '\n', lead.length + pre.length, lead.length + pre.length + t.length);
    },
    link() {
      const [a, b] = sel(), t = ta.value.slice(a, b);
      if (/^https?:\/\//.test(t)) return replace(a, b, '[text](' + t + ')', 1, 5);
      replace(a, b, '[' + (t || 'text') + '](https://)', t ? t.length + 3 : 7, t ? t.length + 11 : 7 + 8);
    },
    async issueRef() {
      const c = await ui.pick({ title: T('Issue'), items: [], label: x => x.Key + ' ' + x.Summary, placeholder: T('Find an issue…'), empty: T('Type to search'),
        search: async q => (q.length > 1 ? (await api.get('/find?q=' + encodeURIComponent(q))).cards : []) });
      ta.focus();
      if (c) ctl.insert(c.Key + ' ');
    },
  };

  // ---- popup: mention, emoji, slash
  let items = [], pick = 0, from = -1, kind = '';
  const closePop = () => { pop.hidden = true; from = -1; kind = ''; items = []; };
  const paint = () => {
    clear(pop).append(...items.map((it, i) => h('div.mp' + (i === pick ? '.sel' : ''), { role: 'option', onmousedown: e => { e.preventDefault(); accept(i); } },
      kind === 'mention' ? [ui.avatar(it.DisplayName, null, 18), it.DisplayName]
        : kind === 'emoji' ? [h('span.mp-g', it.Glyph), ':' + it.Name + ':']
          : [h('b', '/' + it.name), h('span.dim', ' ' + it.label)])));
    pop.hidden = !items.length;
    // At the caret: below it, above when there is no room.
    const r = ta.caretRect(), below = window.innerHeight - r.bottom > 270 || r.top < 270;
    Object.assign(pop.style, { position: 'fixed', left: Math.max(4, Math.min(r.left - 8, window.innerWidth - 270)) + 'px', right: 'auto',
      top: below ? r.bottom + 4 + 'px' : 'auto', bottom: below ? 'auto' : window.innerHeight - r.top + 4 + 'px' });
    const s = pop.querySelector('.sel'); if (s) s.scrollIntoView({ block: 'nearest' });
  };
  function accept(i) {
    const it = items[i]; if (!it) return;
    const end = ta.selectionStart, at = from, k = kind;
    closePop();
    if (k === 'mention') {
      const name = '@' + it.DisplayName + ' ';
      if (it.AccountID && !mentions.some(m => m.AccountID === it.AccountID)) mentions.push({ AccountID: it.AccountID, DisplayName: it.DisplayName });
      replace(at, end, name, name.length);
    } else if (k === 'emoji') {
      const code = ':' + it.Name + ': ';
      replace(at, end, code, code.length);
      glyphs.set(it.Name, it.Glyph);
      api.post('/emoji/used', { Name: it.Name }).catch(() => {});
    } else {
      replace(at, end, '', 0);
      it.run(ctl);
    }
  }
  const people = () => (o.people ? [...o.people()] : []);
  const users = debounce(async (q, at) => {
    const local = people().filter(([n]) => n.toLowerCase().includes(q.toLowerCase())).map(([n, id]) => ({ DisplayName: n, AccountID: id }));
    let remote = [];
    try {
      const r = await api.get('/users?' + (o.issueKey ? 'issue=' + encodeURIComponent(o.issueKey) : 'project=' + encodeURIComponent((typeof o.project === 'function' ? o.project() : o.project) || '')) + '&q=' + encodeURIComponent(q));
      remote = (Array.isArray(r) ? r : []).map(u => ({ DisplayName: u.DisplayName, AccountID: u.AccountID }));
    } catch (e) { /* local names only */ }
    if (from !== at || kind !== 'mention') return;
    const seen = new Set();
    items = [...remote, ...local].filter(u => u.DisplayName && u.AccountID && !seen.has(u.AccountID) && seen.add(u.AccountID)).slice(0, 8);
    pick = 0; paint();
  }, 120);
  const emojis = debounce(async (q, at) => {
    let r = [];
    try { r = await api.get('/emoji?q=' + encodeURIComponent(q)); } catch (e) { /* none */ }
    if (from !== at || kind !== 'emoji') return;
    items = r; pick = 0; paint();
  }, 80);
  function trigger() {
    const before = ta.value.slice(0, ta.selectionStart);
    let m = /(?:^|[\s(])@([^\s@]{0,30})$/u.exec(before);
    if (m) { from = before.length - m[1].length - 1; kind = 'mention'; return users(m[1], from); }
    m = /(?:^|\s):([a-z0-9_+-]{2,})$/i.exec(before);
    if (m) { from = before.length - m[1].length - 1; kind = 'emoji'; return emojis(m[1].toLowerCase(), from); }
    m = /(?:^|\n|\s)\/([a-z0-9]{0,10})$/i.exec(before);
    if (m && !inFence(before)) {
      const q = m[1].toLowerCase();
      from = before.length - q.length - 1; kind = 'slash';
      items = SLASH.filter(s => s.name.startsWith(q) || (q.length > 1 && s.label.toLowerCase().includes(q))).slice(0, 8);
      pick = 0; return paint();
    }
    if (kind) closePop();
  }
  const inFence = t => (t.match(/^```/gm) || []).length % 2 === 1;

  // ---- preview (Markdown mode)
  const renderPreview = () => {
    if (preview.hidden) return;
    let text = ta.value;
    for (const [n, g] of glyphs) text = text.split(':' + n + ':').join(g);
    clear(preview).append(text.trim() ? md(text, o.mdOpts ? o.mdOpts() : {}) : h('p.faint', T('Nothing to preview.')));
  };
  const livePreview = debounce(renderPreview, 120);
  const togglePreview = () => {
    if (mode !== 'markdown') return;
    preview.hidden = !preview.hidden; bPrev.classList.toggle('on', !preview.hidden); renderPreview(); ta.focus({ preventScroll: true });
  };

  // ---- files: onFiles uploads them and answers each one's attachment ({ID, Filename}, or null).
  // On an issue an image goes in the text where it was pasted: a line while it uploads, then ![name](attachment:ID).
  let uploads = 0;
  async function files(list) {
    if (mode === 'visual' && r) return r.files(list);
    if (!o.onFiles) return;
    const marks = list.map(f => o.issueKey && f.type.startsWith('image/') ? '![' + T('Uploading %s %d…', f.name || T('image'), ++uploads) + ']()' : '');
    const shown = marks.filter(Boolean);
    if (shown.length) {
      const [a] = sel();
      ctl.insert((a && ta.value[a - 1] !== '\n' ? '\n' : '') + shown.join('\n') + '\n');
    }
    const made = (await o.onFiles(list)) || [];
    marks.forEach((m, i) => {
      if (!m) return;
      const at = ta.value.indexOf(m);
      if (at < 0) return;
      const att = made[i];
      if (att && att.ID) ta.setRangeText('![' + att.Filename.replace(/[[\]\n]/g, ' ') + '](attachment:' + att.ID + ')', at, at + m.length, 'preserve');
      else ta.setRangeText('', at, at + m.length + (ta.value[at + m.length] === '\n' ? 1 : 0), 'preserve');
    });
    if (shown.length) ta.dispatchEvent(new Event('input'));
  }

  // ---- the Markdown mode's toolbar
  const btn = (label, title, fn, cls = '') => h('button.tb' + cls, { type: 'button', title, tabindex: -1, onmousedown: e => e.preventDefault(), onclick: fn }, label);
  const bPrev = btn(T('Preview'), T('Preview (ctrl+p)'), togglePreview, '.txt');
  const bSrc = btn(T('Source'), T('Show the markdown everywhere, not only on the caret’s line'), () => {
    const on = ta.classList.toggle('live');
    bSrc.classList.toggle('on', !on);
    setPref(SOURCE, on ? '0' : '1');
    ta.focus({ preventScroll: true });
  }, '.txt' + (source() ? '.on' : ''));
  const mdTools = h('div.ed-tools', { role: 'toolbar' },
    btn(icon('bold'), T('Bold (ctrl+b)'), () => ctl.wrap('**', '**', 'bold')), btn(icon('italic'), T('Italic (ctrl+i)'), () => ctl.wrap('*', '*', 'italic')),
    btn(icon('strikethrough'), T('Strikethrough'), () => ctl.wrap('~~', '~~', 'text')), btn(icon('code'), T('Inline code'), () => ctl.wrap('`', '`', 'code')),
    btn(icon('link'), T('Link (ctrl+k)'), () => ctl.link()), h('i.sep'),
    btn(icon('heading'), T('Heading (/h2)'), () => ctl.line('## ')), btn(icon('list'), T('Bulleted list'), () => ctl.line('- ')), btn(icon('list-ordered'), T('Numbered list'), () => ctl.line('1. ')),
    btn(icon('list-todo'), T('Task list'), () => ctl.line('- [ ] ')), btn(icon('quote'), T('Quote'), () => ctl.line('> ')), btn(icon('square-code'), T('Code block'), () => ctl.block('```\n', '\n```', '')), h('i.sep'),
    btn('/', T('Insert… (type / in the text)'), () => { ctl.insert('/'); trigger(); }), btn(icon('at-sign'), T('Mention'), () => { ctl.insert('@'); trigger(); }),
    o.onFiles && btn(icon('paperclip'), T('Attach files (or paste, or drop them)'), () => fileIn.click()),
    h('span.spacer'), bSrc, bPrev);
  if (!o.onFiles) fileIn.remove();

  // ---- what both modes share: the mode switch, full screen
  const full = () => {
    const on = node.classList.toggle('full');
    bFull.classList.toggle('on', on);
    focus();
  };
  const bFull = btn(icon('maximize-2'), T('Full screen (esc leaves it)'), full);
  const bVis = h('button', { type: 'button', tabindex: -1, title: T('Edit it as it shows'), onmousedown: e => e.preventDefault(), onclick: () => toMode('visual') }, T('Visual'));
  const bMd = h('button', { type: 'button', tabindex: -1, onmousedown: e => e.preventDefault(), onclick: () => toMode('markdown'),
    disabled: adf && !editable, title: adf && !editable ? T('Markdown can’t hold this one: %s', o.reason || T('it has what markdown can’t write')) : T('Edit the markdown') }, T('Markdown'));
  const modeSw = h('span.ed-mode', { role: 'group', 'aria-label': T('Editor mode') }, bVis, bMd);
  const right = h('span.ed-right', modeSw, bFull);

  // ---- shell
  const head = h('div.ed-head');
  const body = h('div.ed-body');
  document.body.append(pop); // floats over the page: in the panel, contain would hold position: fixed to it
  const node = h('div.ed', head, body, preview, fileIn,
    h('div.ed-foot', h('span.dim.hint', o.hint || (o.noCancel ? T('ctrl+⏎ %s · / formats', (o.label || T('Save')).toLowerCase()) : T('ctrl+⏎ %s · esc cancels · / formats', (o.label || T('Save')).toLowerCase()))), h('span.spacer'),
      !o.noCancel && o.cancel && h('button.btn.ghost', { onclick: () => node._cancel() }, T('Cancel')), o.save && go));

  // ---- the Visual mode: mounted once its module is in. Until then a plain box stands in and takes
  // what is typed (c, then typing at once), which goes into the document when it mounts.
  let r = null, pendingFocus = null, mounting = null;
  const early = h('div.input.ed-ta', { contentEditable: 'plaintext-only', role: 'textbox', 'aria-multiline': 'true', 'aria-label': o.placeholder || T('Text'),
    style: { minHeight: 'calc(' + (o.rows || 4) + ' * 1.5em + .714rem + 2px)' } });
  const carry = () => {
    const t = early.innerText.replace(/\n$/, '');
    if (!r || !t) return;
    early.textContent = '';
    if (pendingFocus === null) r.focus(true);
    r.view.dispatch(r.view.state.tr.insertText(t));
  };
  const mountVisual = async (doc) => {
    const m = await loadRte();
    if (disposed) return;
    if (r) { r.setDoc(doc); return; }
    r = m.rte({ doc, placeholder: o.placeholder, rows: o.rows || 4, issueKey: o.issueKey, project: o.project, site: app.session && app.session.baseURL, wiki: o.wiki,
      isKey: o.mdOpts && o.mdOpts().isKey, onKey: o.mdOpts && o.mdOpts().onKey, people: o.people, api, ui,
      upload: o.issueKey && o.onFiles ? o.onFiles : null, onFiles: !o.issueKey && o.onFiles ? o.onFiles : null,
      convert: async text => (await api.post('/adf/doc', { Markdown: text, Kept: [], People: mentionsIn(text, [], people()) })).Doc,
      onChange: changed, keys: app.keys });
  };
  const showMode = () => {
    bVis.classList.toggle('on', mode === 'visual'); bMd.classList.toggle('on', mode === 'markdown');
    bVis.setAttribute('aria-pressed', String(mode === 'visual')); bMd.setAttribute('aria-pressed', String(mode === 'markdown'));
    node.classList.toggle('visual', mode === 'visual');
    if (mode === 'visual') {
      preview.hidden = true;
      clear(head).append(r ? r.toolbar : h('div.ed-tools'), right);
      if (r) { if (!r.toolbar.querySelector(':scope > .spacer')) r.toolbar.append(h('span.spacer')); r.toolbar.append(right); }
      clear(body).append(r ? r.el : early);
    } else {
      mdTools.append(right);
      clear(head).append(mdTools);
      clear(body).append(ta);
    }
  };
  // Switching converts what is written: the document to markdown (refused when markdown can't hold it) or back.
  async function toMode(m, quiet) {
    if (m === mode || mounting) return;
    if (r && r.busy()) return ui.toast(T('A file is still uploading'));
    node.classList.add('ed-busy');
    try {
      if (m === 'markdown') {
        const doc = r ? r.getDoc() : EMPTY;
        if (hasContent(doc) || adf) {
          const c = await api.post('/adf/markdown', { Doc: doc });
          if (adf && !c.Editable) { ui.toast(T('Markdown can’t hold this one: %s', c.Reason || ''), { kind: 'err' }); return; }
          if (adf) { ta.value = c.Markdown; kept = c.Kept || []; keptMoved = true; } else { ta.value = c.Text; mentions.push(...mentionsOf(doc).filter(x => !mentions.some(y => y.AccountID === x.AccountID))); }
        }
      } else {
        mounting = mountVisual(await toDoc());
        await mounting;
      }
      mode = m;
      if (!quiet) setPref(MODE, m);
      showMode();
      focus();
    } catch (e) { ui.errToast(e); } finally { mounting = null; node.classList.remove('ed-busy'); }
  }
  // The Markdown mode's text as a document.
  const toDoc = async () => {
    if (!ta.value.trim()) return EMPTY;
    return (await api.post('/adf/doc', { Markdown: ta.value, Kept: kept, People: mentionsIn(ta.value, mentions, people()) })).Doc;
  };
  const isEmpty = () => (mode === 'visual' ? (r ? r.isEmpty() : !(adf ? hasContent(o.doc) : (o.value || '').trim())) : !ta.value.trim());
  const focus = end => {
    if (mode === 'visual') { if (r) r.focus(end); else { pendingFocus = end || false; early.focus({ preventScroll: true }); } return; }
    ta.focus({ preventScroll: true }); if (end !== false) ta.setSelectionRange(ta.value.length);
  };
  async function value() {
    if (mounting) await mounting;
    if (adf) return mode === 'visual' ? (r ? r.getDoc() : o.doc || EMPTY) : toDoc();
    if (mode === 'markdown') return { text: ta.value, mentions: mentionsIn(ta.value, mentions, people()) };
    const doc = r ? r.getDoc() : EMPTY;
    if (!hasContent(doc)) return { text: '', mentions: [] };
    const c = await api.post('/adf/markdown', { Doc: doc });
    return { text: c.Text, mentions: mentionsOf(doc) };
  }

  let busy = false;
  let base = o.base || '';
  async function run() {
    if (busy || !o.save) return;
    if (mode === 'visual' && !r && mounting) await mounting.catch(() => {}); // what was typed meanwhile goes in first
    if (mode === 'visual' && r && r.busy()) return ui.toast(T('A file is still uploading'));
    if (!o.allowEmpty && isEmpty()) return;
    busy = true; go.disabled = true; node.classList.add('ed-busy');
    try {
      const v = await value();
      const ok = adf ? await o.save(v, base) : await o.save(v.text, v.mentions, base);
      if (ok !== false) { draft.drop(); touched = false; }
    } catch (e) { ui.errToast(e); } finally { busy = false; go.disabled = false; node.classList.remove('ed-busy'); }
  }
  node._save = run;
  // Cancelling with changes asks first (TUI: esc in the description editor); it drops the draft too.
  node._cancel = async () => {
    closePop();
    if (o.noCancel || !o.cancel) return document.activeElement && document.activeElement.blur();
    if (touched && !await ui.confirm({ title: T('Discard your changes?'), text: T('What you typed here is lost.'), ok: T('Discard'), danger: true })) return focus(false);
    draft.drop(); o.cancel();
  };

  // ---- drafts: what is typed is kept a moment after each change (TUI drafts.go: "unix base\ntext" under jira_tab:draft:),
  // so a reload or the terminal brings it back; saving or discarding drops it. The Visual mode's goes as the document
  // (the server keeps its markdown for the TUI). Leaving the page sends the last moment's typing (keepalive: it outlives
  // the page). A restored draft saves against the document it was written on: one Jira changed since asks first.
  const draft = { save: () => {}, drop: () => {}, flush: () => {} };
  if (o.draft) {
    const path = '/drafts/' + encodeURIComponent(o.draft);
    let typed = false;
    const keep = async (keepalive = false) => {
      if (!touched || isEmpty()) return api.del(path, undefined, { keepalive }).catch(() => {});
      if (mode === 'visual' && r && r.busy()) return; // kept once its files are in
      try {
        const body = mode === 'visual' && r ? { Doc: r.getDoc(), Base: base } : keptMoved ? { Doc: await toDoc(), Base: base } : { Text: ta.value, Base: base };
        await api.put(path, body, { keepalive });
      } catch (e) { /* the next change tries again */ }
    };
    const later = debounce(() => { if (typed) keep(); }, 2000);
    draft.save = () => { typed = true; later(); };
    draft.drop = () => { typed = false; api.del(path).catch(() => {}); };
    draft.flush = keepalive => { if (typed) keep(keepalive); typed = false; };
    api.get(path, { fresh: true }).then(async d => {
      if (!d || (!d.Text && !d.Doc) || touched) return;
      if (mounting) await mounting;
      if (touched) return;
      const was = base, before = snapshot();
      if (d.Doc) await set(d.Doc, true); else if (!adf) await set(d.Text, true); else await setMarkdown(d.Text, true);
      touched = true;
      if (d.Base && base) base = d.Base;
      ui.toast(base !== was ? T('Draft restored; the site’s has changed since') : T('Draft restored'),
        { action: { label: T('Drop it'), run: () => { restore(before); touched = false; base = was; draft.drop(); } } });
    }).catch(() => {});
  }
  node._escape = () => {
    if (!pop.hidden) { closePop(); return true; }
    if (node.classList.contains('full')) { full(); return true; }
    return false;
  };

  // ---- setting what is written
  // set: a document (or, editing markdown, markdown) as the text, in whichever mode shows.
  async function set(v, quiet) {
    if (adf || (v && typeof v === 'object')) {
      const doc = v && typeof v === 'object' ? v : EMPTY;
      if (mode === 'visual') { if (r) r.setDoc(doc); else await (mounting = mountVisual(doc)).finally(() => { mounting = null; }); } else {
        const c = await api.post('/adf/markdown', { Doc: doc });
        if (c.Editable) { ta.value = c.Markdown; kept = c.Kept || []; keptMoved = true; } else {
          // Markdown can't hold it: it shows Visual.
          await (mounting = mountVisual(doc)).finally(() => { mounting = null; });
          mode = 'visual'; showMode();
        }
      }
    } else if (mode === 'markdown') ta.value = v || '';
    else await setMarkdown(v || '', quiet);
    if (!quiet) changed();
  }
  // setMarkdown: text as what is written; untouched: only while nothing was typed (a template).
  async function setMarkdown(text, quiet, untouched) {
    if (untouched && touched) return;
    if (mode === 'markdown') ta.value = text;
    else {
      const doc = text.trim() ? (await api.post('/adf/doc', { Markdown: text, Kept: kept, People: mentionsIn(text, mentions, people()) })).Doc : EMPTY;
      if (untouched && touched) return;
      if (r) r.setDoc(doc); else await (mounting = mountVisual(doc)).finally(() => { mounting = null; });
    }
    if (!quiet) changed();
  }
  const snapshot = () => ({ mode, doc: mode === 'visual' ? (r ? r.getDoc() : (adf && o.doc) || EMPTY) : null, text: ta.value, kept: kept.slice(), mentions: mentions.slice() });
  function restore(s) {
    if (!s) return;
    kept = s.kept || kept; mentions.splice(0, mentions.length, ...(s.mentions || []));
    if (s.mode === 'visual' && s.doc) return set(s.doc, true);
    if (s.mode === 'markdown') return mode === 'markdown' ? (ta.value = s.text) : setMarkdown(s.text, true);
  }
  // A mention at the start (a reply's @Author), or one taken out again.
  function mention(u, prepend) {
    if (u.AccountID && !mentions.some(m => m.AccountID === u.AccountID)) mentions.push({ AccountID: u.AccountID, DisplayName: u.DisplayName });
    const tag = '@' + u.DisplayName + ' ';
    if (mode === 'markdown') { if (!ta.value.startsWith(tag)) ta.value = (prepend ? tag + ta.value : ta.value + tag); return; }
    if (!r) return;
    const v = r.view, { schema } = v.state, n = schema.nodes.mention.create({ id: u.AccountID, text: '@' + u.DisplayName, accessLevel: '' });
    const first = v.state.doc.firstChild;
    if (prepend && first && first.firstChild && first.firstChild.type === n.type && first.firstChild.attrs.id === u.AccountID) return;
    // At the start of the first paragraph; before anything else (a code block, a picture) in one of its own.
    const tr = !prepend ? v.state.tr.replaceSelectionWith(n).insertText(' ')
      : first && first.type === schema.nodes.paragraph ? v.state.tr.insert(1, [n, schema.text(' ')]) : v.state.tr.insert(0, schema.nodes.paragraph.create(null, [n, schema.text(' ')]));
    v.dispatch(tr);
  }
  function unmention(name) {
    const tag = '@' + name + ' ';
    if (mode === 'markdown') { if (ta.value.startsWith(tag)) ta.value = ta.value.slice(tag.length); return; }
    if (!r) return;
    const v = r.view, first = v.state.doc.firstChild, m = first && first.firstChild;
    if (m && m.type.name === 'mention' && String(m.attrs.text).replace(/^@/, '') === name) {
      const next = first.childCount > 1 ? first.child(1) : null, extra = next && next.isText && next.text.startsWith(' ') ? 1 : 0;
      v.dispatch(v.state.tr.delete(1, 1 + m.nodeSize + extra));
    }
  }

  ta.addEventListener('input', () => { trigger(); livePreview(); changed(); });
  ta.addEventListener('keydown', e => {
    if (e.defaultPrevented) return;
    if (e.altKey && !e.ctrlKey && !e.metaKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown') && pop.hidden) {
      const ed = moveLines(ta.value, ta.selectionStart, ta.selectionEnd, e.key === 'ArrowUp' ? -1 : 1);
      e.preventDefault();
      if (ed) ta.edit(ed);
      return;
    }
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    // Up and down in a table row stay in its column (the browser loses its way in a grid).
    if (pop.hidden && !e.shiftKey && (e.key === 'ArrowUp' || e.key === 'ArrowDown') && ta.selectionStart === ta.selectionEnd) {
      const to = tableArrow(ta.value, ta.selectionStart, e.key === 'ArrowUp' ? -1 : 1);
      if (to != null) { e.preventDefault(); ta.setSelectionRange(to); }
      return;
    }
    if (pop.hidden && e.key === 'Backspace' && !e.shiftKey) {
      const ed = backspace(ta.value, ta.selectionStart, ta.selectionEnd);
      if (ed) { e.preventDefault(); ta.edit(ed); }
      return;
    }
    if (pop.hidden) {
      if (e.key !== 'Tab') return;
      const [a, b] = [ta.selectionStart, ta.selectionEnd];
      const ed = indent(ta.value, a, b, e.shiftKey) || tableTab(ta.value, a, e.shiftKey);
      if (ed) { e.preventDefault(); ta.edit(ed); }
      return;
    }
    const k = e.key;
    if (k === 'ArrowDown' || (k === 'Tab' && !e.shiftKey)) pick = (pick + 1) % items.length;
    else if (k === 'ArrowUp' || (k === 'Tab' && e.shiftKey)) pick = (pick - 1 + items.length) % items.length;
    else if (k === 'Enter') accept(pick);
    else return;
    e.preventDefault(); e.stopPropagation(); if (k !== 'Enter') paint();
  });
  ta.addEventListener('blur', () => setTimeout(closePop, 150));
  ta.addEventListener('click', e => {
    const t = e.target.closest ? e.target : e.target.parentElement;
    const box = t.closest('.hl-box');
    if (box && !ta.readOnly) return ta.edit(toggleTask(ta.value, ta.offsetOf(box)));
    if (!(e.ctrlKey || e.metaKey)) return;
    const a = t.closest('[data-href]'), k = t.closest('[data-key]');
    const m = o.mdOpts && o.mdOpts(), local = a && m && m.onKey && jiraKey(a.dataset.href, m.site); // an issue on this site opens here
    if (local) { e.preventDefault(); m.onKey(local); } else if (a) { e.preventDefault(); window.open(a.dataset.href, '_blank', 'noopener'); } else if (k && m && m.onKey) { e.preventDefault(); m.onKey(k.dataset.key); }
  });
  ta.addEventListener('paste', e => {
    const fs = [...(e.clipboardData ? e.clipboardData.files : [])];
    if (fs.length && o.onFiles) { e.preventDefault(); files(fs); }
  });
  // Files dropped on the Markdown mode; the Visual one takes its own where they land.
  for (const t of ['dragenter', 'dragover']) node.addEventListener(t, e => { if (o.onFiles && e.dataTransfer && [...e.dataTransfer.types].includes('Files')) { e.preventDefault(); node.classList.add('drop'); } });
  node.addEventListener('dragleave', e => { if (!node.contains(e.relatedTarget)) node.classList.remove('drop'); });
  node.addEventListener('drop', e => {
    node.classList.remove('drop');
    if (mode === 'visual') return;
    if (o.onFiles && e.dataTransfer && e.dataTransfer.files.length) { e.preventDefault(); files([...e.dataTransfer.files]); }
  });

  // Shortcuts live in a scope while the text has focus, so they beat the global ones (ctrl+k).
  let scope = null;
  ta.addEventListener('focus', () => {
    if (scope) return;
    scope = app.keys.scope('mdedit');
    const b = (spec, fn, desc) => scope.bind(spec, fn, desc, { input: true, group: 'Editor' });
    b('ctrl+b', () => ctl.wrap('**', '**', 'bold'), T('bold'));
    b('ctrl+i', () => ctl.wrap('*', '*', 'italic'), T('italic'));
    b('ctrl+k', () => ctl.link(), T('link'));
    b('ctrl+shift+x', () => ctl.wrap('~~', '~~', 'text'), T('strikethrough'));
    b('ctrl+e', () => ctl.wrap('`', '`', 'code'), T('inline code'));
    b('ctrl+p', togglePreview, T('toggle preview'));
    scope.bind('Escape', closePop, '', { input: true, hidden: true, when: () => !pop.hidden }); // the list, not the dialog the editor is in
  });
  const release = () => { draft.flush(); if (scope) { scope.dispose(); scope = null; } };
  ta.addEventListener('blur', release);
  node.addEventListener('focusout', e => { if (mode === 'visual' && !node.contains(e.relatedTarget)) draft.flush(); });
  const unLeave = o.draft ? onLeave(() => draft.flush(true)) : () => {};

  // ---- start: the Visual mode mounts on its document (a markdown value converted first), the Markdown one at once
  let disposed = false;
  showMode();
  if (mode === 'visual') {
    // A document starts as it is; markdown (or a document given only as markdown) is made one first.
    const md0 = (o.value || '').trim() && (!adf || !hasContent(o.doc));
    const first = md0 ? api.post('/adf/doc', { Markdown: o.value, Kept: kept, People: mentionsIn(o.value, [], people()) }).then(c => c.Doc) : Promise.resolve(o.doc || EMPTY);
    mounting = first.then(mountVisual).then(() => {
      showMode();
      if (pendingFocus !== null) r.focus(pendingFocus);
      carry(); pendingFocus = null;
    }).catch(e => {
      // No visual editor: the markdown one, as it was.
      console.warn('visual editor', e);
      if (adf && !editable) return ui.errToast(e);
      if (adf) ta.value = o.value || '';
      const t = early.innerText.trim();
      if (t) ta.value += (ta.value ? '\n' : '') + t;
      mode = 'markdown'; showMode();
    }).finally(() => { mounting = null; });
  }

  // current: what is written, now: {doc} (Visual) or {text, mentions} (Markdown).
  const current = () => (mode === 'visual' ? (r ? { doc: r.getDoc() } : adf && hasContent(o.doc) ? { doc: o.doc } : { text: o.value || '', mentions: [] })
    : { text: ta.value, mentions: mentionsIn(ta.value, mentions, people()), kept: kept.length ? kept.slice() : undefined });

  return { el: node, focus, isEmpty, value, current, mode: () => mode, doc: () => (mode === 'visual' && r ? r.getDoc() : null), text: () => (mode === 'markdown' ? ta.value : ''),
    clear: () => { touched = false; if (r) r.setDoc(EMPTY); ta.value = ''; mentions.length = 0; },
    onInput: fn => listeners.push(fn), set, setMarkdown, markdown: async () => (mode === 'markdown' ? ta.value : r && !r.isEmpty() ? (await api.post('/adf/markdown', { Doc: r.getDoc() })).Text : ''),
    mention, unmention, snapshot, restore, contains: el => node.contains(el), ready: () => mounting || Promise.resolve(),
    preview: togglePreview, dispose: () => { disposed = true; release(); unLeave(); pop.remove(); if (r) r.destroy(); }, dropDraft: () => draft.drop(), rebase: b => { base = b; } };
}
