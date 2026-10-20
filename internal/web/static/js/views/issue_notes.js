// Private notes on an issue (TUI: N). They live on the machine running laneway, beside the
// state file, where the TUI keeps them, so both see the same. mountNotes(parent, key, {app, el, full}) → cleanup.
import { h, clear } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { render } from '../lib/md.js';
import { postComment } from '../lib/comment.js';
import { T } from '../lib/i18n.js';
import { keyHint, modText } from '../lib/keys.js';

export function mountNotes(parent, key, { app, el, full }) {
  css('ask');
  const { api, ui } = app;
  const box = h('section.notes');
  parent.append(box);
  let text = '', dead = false, editing = false, open = false;
  const scope = app.keys.scope('issue-notes');
  const inPanel = () => full || app.panel.focused();
  scope.bind('N', () => edit(), T('edit your private notes'), { group: T('Issue'), when: () => inPanel() && !el.querySelector('.iss-find:not([hidden])') });

  async function save(v) {
    try { await api.put('/issues/' + key + '/notes', { Text: v }); } catch (e) { return ui.errToast(e); }
    text = v.trim(); editing = false; paint();
    app.bus.emit('notes:changed', { key, has: !!text });
    ui.toast(text ? T('Notes saved, on this machine only') : T('Notes removed'));
  }
  function edit() {
    if (editing) return;
    editing = true; paint();
    const ta = box.querySelector('textarea'); ta && ta.focus();
  }
  function paint() {
    clear(box);
    box.hidden = false;
    if (!text && !editing) { box.append(h('button.btn.ghost.sm', { title: T('Private notes, on this machine (N)'), onclick: edit }, T('+ Private notes'), keyHint('N'))); return; }
    if (editing) {
      const ta = h('textarea.input', { value: text, placeholder: T('Private notes in markdown. Only on this machine.'), spellcheck: true });
      const done = () => save(ta.value), cancel = () => { editing = false; paint(); };
      const ed = h('div.ed', ta);
      ed._save = done; ed._cancel = cancel; // the panel's esc and ctrl+enter find these
      box.append(h('div.notes-head', h('b', T('Notes (local)')), h('span.dim.key-dup', modText(T('ctrl+enter saves, esc cancels')))), ed,
        h('div.row.end', h('button.btn', { onclick: cancel }, T('Cancel'), keyHint('Escape')), h('button.btn.primary', { onclick: done }, T('Save'), keyHint('mod+Enter'))));
      return;
    }
    const long = text.split('\n').length > 4;
    const body = h('div.notes-body.md' + (long && !open ? '.folded' : ''), render(text, { site: app.session.baseURL, onKey: k => app.panel.open(k) }));
    box.append(h('div.notes-head', h('b', T('Notes (local)')), h('span.spacer'),
      long && h('button.btn.ghost.sm', { onclick: () => { open = !open; paint(); } }, open ? T('Fold') : T('Unfold')),
      h('button.btn.ghost.sm', { title: T('Post as a comment'), onclick: post }, T('Post as comment')),
      h('button.btn.ghost.sm', { title: T('Edit (N)'), onclick: edit }, T('Edit'), keyHint('N'))), body);
  }
  async function post() {
    if (!await ui.confirm({ title: T('Post notes'), text: T('Post your private notes on %s as a comment?', key), ok: T('Post') })) return;
    try { const found = await postComment(api, key, { Markdown: text }); app.bus.emit('issue:changed', { key }); ui.toast(found ? T('Your comment was on %s already: not posted again', key) : T('Comment posted')); } catch (e) { ui.errToast(e); }
  }
  api.get('/issues/' + key + '/notes', { fresh: true }).then(r => { if (!dead) { text = (r.Text || '').trim(); paint(); } }).catch(() => {});
  return () => { dead = true; scope.dispose(); box.remove(); };
}
