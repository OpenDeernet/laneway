// Visual editor: Jira's document (ADF) edited as it shows, on ProseMirror
// (vendor/prosemirror; the schema and the ADF in and out: lib/adf.js). A
// toolbar that follows the caret, markdown typed as markdown makes (# , - ,
// 1. , [] , > , ``` , ---, **bold**, *italic*, `code`, ~~strike~~), `/` to
// insert, `@` mentions, `:` emoji, task lists, tables (tab walks the cells;
// a bar over the table adds, removes, merges, colours), pictures that resize
// by their edges and sit left, centred, right or wrapped, panels, expands,
// code blocks with their language, status and date chips, pasted or dropped
// files uploaded where they land, pasted markdown made rich. What it can't
// edit (a macro) is kept as it was.
//
//   const r = rte({doc, placeholder, rows, issueKey, site, isKey(s), onKey(key), people() → Map(name → id),
//                  api, ui, upload(files) → [{ID, Filename, MimeType, MediaID} | null], onFiles(files), convert(markdown) → ADF,
//                  onChange(), keys (app.keys)})
//   r.el, r.toolbar, r.view, r.getDoc() → ADF, r.setDoc(adf), r.isEmpty(), r.focus(end), r.busy() → uploads running, r.destroy()
import { EditorState, Plugin, PluginKey, TextSelection, NodeSelection, Selection } from '../../vendor/prosemirror/prosemirror-state.js';
import { EditorView, Decoration, DecorationSet } from '../../vendor/prosemirror/prosemirror-view.js';
import { keymap } from '../../vendor/prosemirror/prosemirror-keymap.js';
import { history, undo, redo } from '../../vendor/prosemirror/prosemirror-history.js';
import { baseKeymap, toggleMark, setBlockType, wrapIn, lift, chainCommands, newlineInCode, exitCode, selectNodeBackward } from '../../vendor/prosemirror/prosemirror-commands.js';
import { inputRules, wrappingInputRule, textblockTypeInputRule, InputRule, undoInputRule } from '../../vendor/prosemirror/prosemirror-inputrules.js';
import { splitListItem, liftListItem, sinkListItem, wrapInList } from '../../vendor/prosemirror/prosemirror-schema-list.js';
import { dropCursor } from '../../vendor/prosemirror/prosemirror-dropcursor.js';
import { gapCursor } from '../../vendor/prosemirror/prosemirror-gapcursor.js';
import { tableEditing, columnResizing, goToNextCell, addRowAfter, addRowBefore, addColumnAfter, addColumnBefore, deleteRow, deleteColumn, deleteTable,
  mergeCells, splitCell, toggleHeaderRow, toggleHeaderColumn, setCellAttr, isInTable, CellSelection } from '../../vendor/prosemirror/prosemirror-tables.js';
import { Fragment, Slice } from '../../vendor/prosemirror/prosemirror-model.js';
import { schema, fromADF, toADF, isEmpty, localId, keptLabel } from './adf.js';
import { h, clear, debounce } from './dom.js';
import { icon } from './icons.js';
import { T } from './i18n.js';
import { kbd, modText } from './keys.js';
import { jiraKey, issuePill } from './issuepill.js';
import { glyph } from './md.js';
import { rich } from './html2md.js';
import { spans, cached as cachedSpans } from './codehl.js';

const N = schema.nodes, M = schema.marks;
const CSS = ['/vendor/prosemirror/prosemirror.css', '/vendor/prosemirror/tables.css', '/vendor/prosemirror/gapcursor.css'];
for (const href of CSS) if (!document.querySelector('link[href="' + href + '"]')) document.head.append(h('link', { rel: 'stylesheet', href }));

const PANELS = [['info', T('Info')], ['note', T('Note')], ['success', T('Success')], ['warning', T('Warning')], ['error', T('Error')]];
const STATUS = [['neutral', T('Grey')], ['purple', T('Purple')], ['blue', T('Blue')], ['red', T('Red')], ['yellow', T('Yellow')], ['green', T('Green')]];
const COLOURS = [['#172b4d', T('Default colour')], ['#97a0af', T('Grey')], ['#0747a6', T('Blue')], ['#008da6', T('Teal')], ['#006644', T('Green')], ['#ff991f', T('Orange')], ['#bf2600', T('Red')], ['#403294', T('Purple')]];
const CELLS = [[null, T('None')], ['#deebff', T('Blue')], ['#e3fcef', T('Green')], ['#fffae6', T('Yellow')], ['#ffebe6', T('Red')], ['#eae6ff', T('Purple')], ['#f4f5f7', T('Grey')]];
const LANGS = ['', 'bash', 'c', 'cpp', 'csharp', 'css', 'diff', 'go', 'graphql', 'html', 'java', 'javascript', 'json', 'kotlin', 'markdown', 'php', 'python', 'ruby', 'rust', 'sql', 'swift', 'typescript', 'xml', 'yaml'];
const LAYOUTS = [['align-start', 'align-left', T('Align left')], ['center', 'align-center', T('Centre')], ['align-end', 'align-right', T('Align right')],
  ['wrap-left', 'text-align-start', T('Wrap left: text flows on its right')], ['wrap-right', 'text-align-end', T('Wrap right: text flows on its left')]];

// ---- commands
const markActive = (state, type, attrs) => {
  const { from, $from, to, empty } = state.selection;
  const has = m => m.type === type && (!attrs || Object.keys(attrs).every(k => m.attrs[k] === attrs[k]));
  if (empty) return (state.storedMarks || $from.marks()).some(has);
  let found = false;
  state.doc.nodesBetween(from, to, n => { if (n.isInline && n.marks.some(has)) found = true; });
  return found;
};
// Toggling a mark with attributes (sub, sup) swaps the other one out.
const toggleWith = (type, attrs) => (state, dispatch) => {
  if (!attrs || markActive(state, type, attrs) || !markActive(state, type)) return toggleMark(type, attrs)(state, dispatch);
  if (dispatch) {
    const { from, to } = state.selection;
    dispatch(state.tr.removeMark(from, to, type).addMark(from, to, type.create(attrs)));
  }
  return true;
};
const setColour = (type, color) => (state, dispatch) => {
  const { from, to, empty } = state.selection;
  if (empty) return false;
  if (dispatch) { const tr = state.tr.removeMark(from, to, type); dispatch(color ? tr.addMark(from, to, type.create({ color })) : tr); }
  return true;
};
const clearMarks = (state, dispatch) => {
  const { from, to, empty } = state.selection;
  if (empty) { if (dispatch) dispatch(state.tr.setStoredMarks([])); return true; }
  if (dispatch) { let tr = state.tr; for (const t of Object.values(M)) if (t !== M.link) tr = tr.removeMark(from, to, t); dispatch(tr); }
  return true;
};

// The nearest ancestor of the selection that is one of types: {node, depth, pos} or null.
function ancestor(state, ...types) {
  const { $from } = state.selection;
  for (let d = $from.depth; d > 0; d--) if (types.includes($from.node(d).type)) return { node: $from.node(d), depth: d, pos: $from.before(d) };
  return null;
}
const blockActive = (state, type, attrs) => {
  const { $from, to } = state.selection;
  if (to > $from.end()) return false;
  const p = $from.parent;
  return p.type === type && (!attrs || Object.keys(attrs).every(k => p.attrs[k] === attrs[k]));
};

// A task list's or a list's items, made the other kind.
const itemToTask = li => {
  const out = [];
  li.forEach(c => {
    if (c.type === N.paragraph) out.push(N.taskItem.create({ localId: localId() }, c.content));
    else if (c.type === N.bulletList || c.type === N.orderedList || c.type === N.taskList) out.push(listAs(c, N.taskList));
    else out.push(N.taskItem.create({ localId: localId() }, c.textContent ? schema.text(c.textContent) : null));
  });
  return out;
};
function listAs(list, type) {
  if (list.type === type) return list;
  if (type === N.taskList) {
    const kids = [];
    list.forEach(li => kids.push(...(li.type === N.listItem ? itemToTask(li) : [li])));
    return N.taskList.create({ localId: localId() }, kids);
  }
  if (list.type !== N.taskList) return type.create(null, list.content);
  const items = [];
  list.forEach(c => {
    if (c.type === N.taskItem) items.push(N.listItem.create(null, N.paragraph.create(null, c.content)));
    else if (items.length) { const last = items.pop(); items.push(last.copy(last.content.append(Fragment.from(listAs(c, type))))); }
    else items.push(N.listItem.create(null, [N.paragraph.create(), listAs(c, type)]));
  });
  return type.create(null, items);
}
// sameCaret puts the caret in made (now at pos, in place of was) in the textblock that took the place of
// the one it was in: the same one in reading order, at the same offset.
function sameCaret(state, tr, pos, was, made) {
  const { $from } = state.selection;
  let n = -1, i = 0;
  was.descendants((c, p) => { if (c.isTextblock) { if (pos + 1 + p < $from.pos) n = i; i++; } return !c.isTextblock; });
  i = 0;
  let at = -1;
  made.descendants((c, p) => { if (c.isTextblock) { if (i === n) at = pos + 1 + p + 1 + Math.min($from.parentOffset, c.content.size); i++; } return !c.isTextblock; });
  return at >= 0 ? tr.setSelection(TextSelection.create(tr.doc, at)) : tr;
}
// toggleList: out of a list of type, into one, or a list of another kind made this one.
const toggleList = type => (state, dispatch) => {
  let at = ancestor(state, N.bulletList, N.orderedList, N.taskList);
  if (at && at.node.type === type) return type === N.taskList ? liftTask(state, dispatch) : liftListItem(N.listItem)(state, dispatch);
  // A nested task list is part of the one around it: that one changes kind.
  while (at && at.node.type === N.taskList && at.depth > 1 && state.selection.$from.node(at.depth - 1).type === N.taskList) {
    const d = at.depth - 1;
    at = { node: state.selection.$from.node(d), depth: d, pos: state.selection.$from.before(d) };
  }
  // A list item holding more than text and lists (a picture, a code block, a macro) has no task to become.
  if (at && type === N.taskList) {
    let fits = true;
    at.node.descendants(n => { if (n.type === N.listItem) n.forEach(c => { if (!/^(paragraph|bulletList|orderedList|taskList)$/.test(c.type.name)) fits = false; }); return fits; });
    if (!fits) return false;
  }
  if (at) {
    if (dispatch) {
      const made = listAs(at.node, type), tr = state.tr.replaceWith(at.pos, at.pos + at.node.nodeSize, made);
      dispatch(sameCaret(state, tr, at.pos, at.node, made).scrollIntoView());
    }
    return true;
  }
  if (type !== N.taskList) return wrapInList(type)(state, dispatch);
  // Paragraphs into a task list: each one an item.
  const { $from, $to } = state.selection, range = $from.blockRange($to);
  if (!range) return false;
  const items = [];
  let ok = true;
  state.doc.nodesBetween(range.start, range.end, (n, pos, parent) => {
    if (parent !== range.parent) return false;
    if (n.isTextblock) items.push(N.taskItem.create({ localId: localId() }, n.content));
    else ok = false;
    return false;
  });
  if (!ok || !items.length || !range.parent.canReplaceWith(range.startIndex, range.endIndex, N.taskList)) return false;
  if (dispatch) {
    const tr = state.tr.replaceWith(range.start, range.end, N.taskList.create({ localId: localId() }, items));
    dispatch(tr.setSelection(TextSelection.near(tr.doc.resolve(range.start + 2))).scrollIntoView());
  }
  return true;
};
const toggleWrap = type => (state, dispatch) => (ancestor(state, type) ? lift(state, dispatch) : wrapIn(type)(state, dispatch));
const toggleBlock = (type, attrs) => (state, dispatch) => (blockActive(state, type, attrs) ? setBlockType(N.paragraph)(state, dispatch) : setBlockType(type, attrs)(state, dispatch));

// ---- task and decision items (ADF nests a task list beside its item, not in it)
const itemAt = (state, type) => {
  const { $from } = state.selection;
  return $from.parent.type === type ? { item: $from.parent, list: $from.node(-1), index: $from.index(-1), listPos: $from.before(-1), depth: $from.depth } : null;
};
// A task list's children as ADF has them: an item first (a list that would lead is spliced in a level up).
const taskKids = kids => { const out = [...kids]; while (out.length && out[0].type === N.taskList) out.splice(0, 1, ...out[0].content.content); return out; };
// Put the caret back in item (the same node) inside the node at pos, at offset off.
const caretIn = (tr, pos, item, off) => {
  let at = -1;
  tr.doc.nodeAt(pos).descendants((n, p) => { if (at < 0 && n === item) at = pos + 1 + p + 1; return at < 0; });
  if (at >= 0) tr.setSelection(TextSelection.create(tr.doc, at + Math.min(off, item.content.size)));
  return tr;
};
function sinkTask(state, dispatch) {
  const t = itemAt(state, N.taskItem);
  if (!t || t.index === 0) return false;
  if (dispatch) {
    const kids = [], prev = t.list.child(t.index - 1), next = t.index + 1 < t.list.childCount ? t.list.child(t.index + 1) : null;
    for (let i = 0; i < t.index - 1; i++) kids.push(t.list.child(i));
    // Its own sub-list goes along, a level deeper.
    const moved = [t.item, ...(next && next.type === N.taskList ? [next] : [])];
    if (prev.type === N.taskList) kids.push(prev.copy(prev.content.append(Fragment.from(moved))));
    else kids.push(prev, N.taskList.create({ localId: localId() }, moved));
    for (let i = t.index + (moved.length > 1 ? 2 : 1); i < t.list.childCount; i++) kids.push(t.list.child(i));
    const tr = state.tr.replaceWith(t.listPos, t.listPos + t.list.nodeSize, t.list.copy(Fragment.from(kids)));
    dispatch(caretIn(tr, t.listPos, t.item, state.selection.$from.parentOffset).scrollIntoView());
  }
  return true;
}
// itemOut: the item the caret is in leaves its list as a paragraph, the list split around it.
const itemOut = type => (state, dispatch) => {
  const t = itemAt(state, type);
  if (!t) return false;
  const outer = state.doc.resolve(t.listPos).parent;
  if (outer !== state.doc && !outer.type.contentMatch.matchType(N.paragraph) && !outer.type.spec.content.includes('paragraph')) return false;
  if (dispatch) {
    const before = [], after = [];
    t.list.forEach((c, _, i) => { if (i < t.index) before.push(c); else if (i > t.index) after.push(c); });
    const head = before.length ? t.list.copy(Fragment.from(before)) : null;
    const p = N.paragraph.create(null, t.item.content);
    const rest = t.list.type === N.taskList ? taskKids(after) : after; // its sub-list, without it, moves up a level
    const tr = state.tr.replaceWith(t.listPos, t.listPos + t.list.nodeSize, [head, p, rest.length ? t.list.copy(Fragment.from(rest)) : null].filter(Boolean));
    const at = t.listPos + (head ? head.nodeSize : 0) + 1;
    dispatch(tr.setSelection(TextSelection.create(tr.doc, at + Math.min(state.selection.$from.parentOffset, p.content.size))).scrollIntoView());
  }
  return true;
};
// liftTask: an item of a nested task list moves out a level; a top one leaves the list.
function liftTask(state, dispatch) {
  const t = itemAt(state, N.taskItem);
  if (!t) return false;
  const $l = state.doc.resolve(t.listPos);
  if ($l.parent.type !== N.taskList) return itemOut(N.taskItem)(state, dispatch);
  if (dispatch) {
    const before = [], after = [];
    t.list.forEach((c, _, i) => { if (i < t.index) before.push(c); else if (i > t.index) after.push(c); });
    const rest = taskKids(after);
    const out = [...(before.length ? [t.list.copy(Fragment.from(before))] : []), t.item, ...(rest.length ? [N.taskList.create({ localId: localId() }, rest)] : [])];
    const tr = state.tr.replaceWith(t.listPos, t.listPos + t.list.nodeSize, out);
    dispatch(caretIn(tr, $l.before(), t.item, state.selection.$from.parentOffset).scrollIntoView());
  }
  return true;
}
// Enter on an empty item leaves the list (a nested task one a level); else a new item.
const itemEnter = type => (state, dispatch) => {
  const t = itemAt(state, type);
  if (!t || !state.selection.empty) return false;
  if (!t.item.content.size) return type === N.taskItem ? liftTask(state, dispatch) : itemOut(type)(state, dispatch);
  if (dispatch) dispatch(state.tr.split(state.selection.from, 1, [{ type, attrs: { localId: localId(), state: type === N.taskItem ? 'TODO' : 'DECIDED' } }]).scrollIntoView());
  return true;
};
// Backspace at an item's start: out of the list (a nested task a level).
const itemBack = type => (state, dispatch) => {
  const t = itemAt(state, type), { $from, empty } = state.selection;
  if (!t || !empty || $from.parentOffset !== 0) return false;
  return type === N.taskItem ? liftTask(state, dispatch) : itemOut(type)(state, dispatch);
};
const headingBack = (state, dispatch) => {
  const { $from, empty } = state.selection;
  if (!empty || $from.parentOffset !== 0 || $from.parent.type !== N.heading) return false;
  return setBlockType(N.paragraph)(state, dispatch);
};
// Enter after ```lang on a line of its own: a code block.
const fenceEnter = (state, dispatch) => {
  const { $from, empty } = state.selection;
  if (!empty || $from.parent.type !== N.paragraph) return false;
  const m = /^```([\w+#-]*)$/.exec($from.parent.textContent);
  if (!m || $from.parentOffset !== $from.parent.content.size) return false;
  if (dispatch) {
    const tr = state.tr.replaceWith($from.before(), $from.after(), N.codeBlock.create({ language: m[1] || null }));
    dispatch(tr.setSelection(TextSelection.create(tr.doc, $from.before() + 1)).scrollIntoView());
  }
  return true;
};
// Enter at the end of a code block that ends in two empty lines leaves it.
const codeExit = (state, dispatch) => {
  const { $from, empty } = state.selection;
  if (!empty || $from.parent.type !== N.codeBlock || $from.parentOffset !== $from.parent.content.size || !$from.parent.textContent.endsWith('\n\n')) return false;
  const above = $from.node(-1), i = $from.indexAfter(-1);
  if (!above.canReplaceWith(i, i, N.paragraph)) return false;
  if (dispatch) {
    const tr = state.tr.delete($from.pos - 2, $from.pos), at = tr.mapping.map($from.after());
    tr.insert(at, N.paragraph.create());
    dispatch(tr.setSelection(TextSelection.create(tr.doc, at + 1)).scrollIntoView());
  }
  return true;
};
const hardBreak = (state, dispatch) => {
  if (state.selection.$from.parent.type.spec.code) return newlineInCode(state, dispatch);
  if (dispatch) dispatch(state.tr.replaceSelectionWith(N.hardBreak.create()).scrollIntoView());
  return true;
};
// Alt+↑/↓ moves the block the caret is in (a list item within its list).
const moveBlock = dir => (state, dispatch) => {
  const { $from } = state.selection;
  let d = $from.depth;
  while (d > 1 && !/^(listItem|taskItem|decisionItem)$/.test($from.node(d).type.name)) d--;
  if (d < 1) return false;
  const parent = $from.node(d - 1), i = $from.index(d - 1), j = i + dir;
  if (parent.type === N.taskList) return moveTask(state, dispatch, d, dir);
  if (j < 0 || j >= parent.childCount) return false;
  if (dispatch) {
    const node = $from.node(d), pos = $from.before(d), other = parent.child(j);
    const tr = state.tr;
    if (dir < 0) { const at = pos - other.nodeSize; tr.delete(pos, pos + node.nodeSize).insert(at, node); tr.setSelection(Selection.near(tr.doc.resolve(at + ($from.pos - pos)))); }
    else { tr.delete(pos, pos + node.nodeSize); const at = pos + other.nodeSize; tr.insert(at, node); tr.setSelection(Selection.near(tr.doc.resolve(at + ($from.pos - pos)))); }
    dispatch(tr.scrollIntoView());
  }
  return true;
};
// A task moves with its sub-list (the task list after it), past the next task and its sub-list.
function moveTask(state, dispatch, d, dir) {
  const { $from } = state.selection, list = $from.node(d - 1), listPos = $from.before(d - 1), item = $from.node(d);
  const units = [];
  list.forEach(c => { if (c.type === N.taskList && units.length) units[units.length - 1].push(c); else units.push([c]); });
  const u = units.findIndex(x => x[0] === item), v = u + dir;
  if (u < 0 || v < 0 || v >= units.length) return false;
  if (dispatch) {
    [units[u], units[v]] = [units[v], units[u]];
    const tr = state.tr.replaceWith(listPos, listPos + list.nodeSize, list.copy(Fragment.from(units.flat())));
    dispatch(caretIn(tr, listPos, item, $from.parentOffset).scrollIntoView());
  }
  return true;
}
// Tab in a table's last cell adds a row, as Jira's.
const tableTab = (state, dispatch) => {
  if (!isInTable(state)) return false;
  if (goToNextCell(1)(state, dispatch)) return true;
  if (!dispatch) return true;
  addRowAfter(state, dispatch);
  return true;
};
const insertNode = (node, select) => (state, dispatch) => {
  if (dispatch) {
    const tr = state.tr.replaceSelectionWith(node);
    if (select) { const pos = tr.selection.from; tr.setSelection(TextSelection.near(tr.doc.resolve(Math.max(0, pos - node.nodeSize + 1)))); }
    dispatch(tr.scrollIntoView());
  }
  return true;
};
const insertBlock = node => (state, dispatch) => {
  const sel = state.selection, { $from } = sel;
  // An empty paragraph is replaced where its parent can hold the block; else the block goes after the caret's.
  const p = $from.parent, i = $from.depth ? $from.index(-1) : 0;
  const empty = !(sel instanceof NodeSelection) && $from.depth > 0 && p.type === N.paragraph && !p.content.size && $from.node(-1).canReplaceWith(i, i + 1, node.type);
  const at = empty ? $from.before() : insertPoint(state.doc, $from.depth && !(sel instanceof NodeSelection) ? $from.after() : sel.to, node);
  if (at == null) return false;
  if (dispatch) {
    const tr = empty ? state.tr.replaceWith(at, $from.after(), node) : state.tr.insert(at, node);
    tr.setSelection(Selection.near(tr.doc.resolve(Math.min(tr.doc.content.size, at + 1))));
    dispatch(tr.scrollIntoView());
  }
  return true;
};
// insertPoint: where node can go at pos or, climbing, after the block holding pos; null for nowhere.
const insertPoint = (doc, pos, node) => { const $p = doc.resolve(pos); for (let d = $p.depth; d >= 0; d--) { const i = $p.index(d); if ($p.node(d).canReplaceWith(i, i, node.type)) return d === $p.depth ? pos : $p.after(d + 1); } return null; };
const table = (rows = 3, cols = 3) => N.table.create({ isNumberColumnEnabled: false, layout: 'default', localId: localId() },
  Array.from({ length: rows }, (_, r) => N.tableRow.create(null, Array.from({ length: cols }, () => (r ? N.tableCell : N.tableHeader).create(null, N.paragraph.create())))));
const today = () => { const d = new Date(); return String(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate())); };

// ---- input rules: markdown as it is typed
const markRule = (re, type) => new InputRule(re, (state, m, start, end) => {
  const text = m[2], lead = m[1] || '';
  if (!text || state.doc.resolve(start).parent.type.spec.code) return null;
  const from = start + lead.length, tr = state.tr.replaceWith(from, end, schema.text(text, type.create().addToSet(state.doc.resolve(start).marks())));
  return tr.removeStoredMark(type);
});
function rules(o) {
  return inputRules({ rules: [
    textblockTypeInputRule(/^(#{1,6})\s$/, N.heading, m => ({ level: m[1].length })),
    wrappingInputRule(/^\s*>\s$/, N.blockquote),
    wrappingInputRule(/^\s*([-+*])\s$/, N.bulletList),
    wrappingInputRule(/^(\d+)\.\s$/, N.orderedList, m => ({ order: +m[1] }), (m, n) => n.childCount + n.attrs.order === +m[1]),
    // [] or [x]: a task list (a list it is in becomes one).
    new InputRule(/^\[( |x)?\]\s$/, (state, m, start, end) => {
      let out = null;
      toggleList(N.taskList)(state.apply(state.tr.delete(start, end)), t => { out = t; });
      if (!out) return null;
      const tr = state.tr.delete(start, end);
      for (const st of out.steps) tr.step(st);
      tr.setSelection(TextSelection.create(tr.doc, out.selection.anchor, out.selection.head));
      const $p = tr.selection.$from;
      if (m[1] === 'x' && $p.parent.type === N.taskItem) tr.setNodeMarkup($p.before(), null, { ...$p.parent.attrs, state: 'DONE' });
      return tr;
    }),
    new InputRule(/^<>\s$/, (state, m, start, end) => {
      const $s = state.doc.resolve(start);
      if ($s.parent.type !== N.paragraph || !$s.node(-1).canReplaceWith($s.index(-1), $s.index(-1) + 1, N.decisionList)) return null;
      const tr = state.tr.delete(start, end), p = tr.doc.resolve(start).parent;
      const dl = N.decisionList.create({ localId: localId() }, N.decisionItem.create({ localId: localId(), state: 'DECIDED' }, p.content));
      tr.replaceWith($s.before(), $s.before() + p.nodeSize, dl);
      return tr.setSelection(TextSelection.create(tr.doc, $s.before() + 2));
    }),
    textblockTypeInputRule(/^```([\w+#-]*)\s$/, N.codeBlock, m => ({ language: m[1] || null })),
    new InputRule(/^(?:---|\*\*\*|___)$/, (state, m, start, end) => {
      const $s = state.doc.resolve(start);
      if ($s.parent.type !== N.paragraph || $s.parent.content.size !== m[0].length - 1 || !$s.node(-1).canReplace($s.index(-1), $s.index(-1) + 1, Fragment.from([N.rule.create(), N.paragraph.create()]))) return null;
      const tr = state.tr.replaceWith($s.before(), $s.after(), [N.rule.create(), N.paragraph.create()]);
      return tr.setSelection(TextSelection.create(tr.doc, $s.before() + 2));
    }),
    markRule(/(^|[^*\w])\*\*([^*\s](?:[^*]*[^*\s])?)\*\*$/, M.strong),
    markRule(/(^|[^_\w])__([^_\s](?:[^_]*[^_\s])?)__$/, M.strong),
    markRule(/(^|[^*\w])\*([^*\s](?:[^*]*[^*\s])?)\*$/, M.em),
    markRule(/(^|[^_\w])_([^_\s](?:[^_]*[^_\s])?)_$/, M.em),
    markRule(/(^|[^`])`([^`]+)`$/, M.code),
    markRule(/(^|[^~])~~([^~\s](?:[^~]*[^~\s])?)~~$/, M.strike),
    // A typed :name: is its emoji once the table knows it.
    new InputRule(/(^|\s):([a-z0-9_+-]{2,}):$/, (state, m, start, end) => {
      const g = glyph(m[2]);
      if (!g || state.doc.resolve(start).parent.type.spec.code) return null;
      return state.tr.replaceWith(start + m[1].length, end, emojiNode(m[2], g));
    }),
    // A typed URL, then a space: a link.
    new InputRule(/(^|\s)(https?:\/\/[^\s]+[^\s.,;:!?'")\]])\s$/, (state, m, start, end) => {
      const from = start + m[1].length, to = from + m[2].length;
      if (state.doc.resolve(from).parent.type.spec.code || state.doc.rangeHasMark(from, to, M.link)) return null;
      const key = o.site && jiraKey(m[2], o.site);
      if (key) return state.tr.replaceWith(from, to, N.inlineCard.create({ url: m[2] })).insertText(' ', from + 1);
      return state.tr.addMark(from, to, M.link.create({ href: m[2] })).insertText(' ', to);
    }),
  ] });
}
const emojiNode = (name, g) => N.emoji.create({ shortName: ':' + name + ':', id: [...g].map(c => c.codePointAt(0).toString(16)).filter(x => x !== 'fe0f').join('-'), text: g });

// ---- plugins
// Every task, decision and status carries an id of its own (ADF asks one; a split copies it).
const ids = new Plugin({
  appendTransaction(trs, _, state) {
    if (!trs.some(t => t.docChanged)) return null;
    const seen = new Set();
    let tr = null;
    state.doc.descendants((n, pos) => {
      if (!/^(taskList|taskItem|decisionList|decisionItem)$/.test(n.type.name)) return;
      const id = n.attrs.localId;
      if (!id || seen.has(id)) (tr = tr || state.tr).setNodeMarkup(pos, null, { ...n.attrs, localId: localId() });
      else seen.add(id);
    });
    return tr && tr.setMeta('addToHistory', false);
  },
});
const placeholder = text => new Plugin({
  props: {
    decorations(state) {
      if (!text || !isEmpty(state.doc)) return null;
      return DecorationSet.create(state.doc, [Decoration.node(0, state.doc.firstChild.nodeSize, { class: 'rte-empty', 'data-placeholder': text })]);
    },
  },
});
// Issue keys of known projects drawn as links (ctrl+click opens one).
const keyMarks = isKey => {
  const find = doc => {
    const out = [];
    doc.descendants((n, pos, parent) => {
      if (!n.isText || parent.type.spec.code || n.marks.some(m => m.type === M.link || m.type === M.code)) return;
      for (const m of n.text.matchAll(/(?<![\w-])[A-Z][A-Z0-9_]+-\d+(?![\w-])/g)) if (!isKey || isKey(m[0])) out.push(Decoration.inline(pos + m.index, pos + m.index + m[0].length, { class: 'rte-key', 'data-key': m[0] }));
    });
    return DecorationSet.create(doc, out);
  };
  return new Plugin({
    state: { init: (_, s) => find(s.doc), apply: (tr, set) => (tr.docChanged ? find(tr.doc) : set) },
    props: { decorations(s) { return this.getState(s); } },
  });
};

// Code blocks coloured by their language (lib/codehl.js); while one is typed in its colours move along
// and are asked again a moment after.
function codeColours() {
  const key = new PluginKey('codehl');
  const paint = doc => {
    const out = [];
    doc.descendants((n, pos) => {
      if (n.type !== N.codeBlock) return true;
      const sp = n.attrs.language && n.textContent && cachedSpans(n.attrs.language, n.textContent);
      if (sp) for (const [a, b, cls] of sp) out.push(Decoration.inline(pos + 1 + a, pos + 1 + b, { class: cls }));
      return false;
    });
    return DecorationSet.create(doc, out);
  };
  return new Plugin({
    key,
    state: { init: (_, st) => paint(st.doc), apply: (tr, set) => (tr.getMeta(key) ? paint(tr.doc) : tr.docChanged ? set.map(tr.mapping, tr.doc) : set) },
    props: { decorations(st) { return key.getState(st); } },
    view(v) {
      let dead = false, timer = 0;
      const ask = () => {
        const want = [];
        v.state.doc.descendants(n => { if (n.type === N.codeBlock) { if (n.attrs.language && n.textContent && !cachedSpans(n.attrs.language, n.textContent)) want.push(spans(n.attrs.language, n.textContent)); return false; } return true; });
        Promise.all(want).then(() => { if (!dead) v.dispatch(v.state.tr.setMeta(key, true).setMeta('addToHistory', false)); });
      };
      ask();
      return {
        update: (_, prev) => { if (prev.doc !== v.state.doc) { clearTimeout(timer); timer = setTimeout(ask, 250); } },
        destroy: () => { dead = true; clearTimeout(timer); },
      };
    },
  });
}

// ---- node views
function mediaSingleView(ctx) {
  return (node, view, getPos) => {
    const box = h('div.rte-img-box'), cap = h('div.rte-img-in');
    const grip = side => h('span.rte-grip.' + side, { contenteditable: 'false', onmousedown: e => resize(e, side) });
    box.append(cap, grip('l'), grip('r'));
    const dom = h('figure.rte-img', box);
    const paint = n => {
      node = n;
      dom.dataset.layout = n.attrs.layout || 'center';
      const w = +n.attrs.width;
      box.style.width = !w ? '' : n.attrs.widthType === 'pixel' ? 'min(100%, ' + w + 'px)' : Math.min(100, w) + '%';
    };
    paint(node);
    function resize(e, side) {
      e.preventDefault(); e.stopPropagation();
      const start = e.clientX, w0 = box.getBoundingClientRect().width, max = view.dom.clientWidth - 2;
      const move = ev => { const dx = (ev.clientX - start) * (side === 'l' ? -1 : 1) * (dom.dataset.layout === 'center' ? 2 : 1); box.style.width = Math.round(Math.max(48, Math.min(max, w0 + dx))) + 'px'; };
      const up = () => {
        removeEventListener('mousemove', move); removeEventListener('mouseup', up); dom.classList.remove('sizing');
        const w = Math.round(box.getBoundingClientRect().width), pos = getPos();
        if (pos == null) return;
        const full = w >= max - 2;
        const tr = view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, width: full ? null : w, widthType: full ? null : 'pixel' });
        view.dispatch(tr.setSelection(NodeSelection.create(tr.doc, pos)));
      };
      dom.classList.add('sizing');
      addEventListener('mousemove', move); addEventListener('mouseup', up);
    }
    return {
      dom, contentDOM: cap,
      update: n => { if (n.type !== node.type) return false; paint(n); return true; },
      ignoreMutation: m => !cap.contains(m.target) || m.type === 'attributes' && m.target === box,
      stopEvent: e => !!(e.target.closest && e.target.closest('.rte-grip')),
    };
  };
}
function mediaView(ctx) {
  return (node, view, getPos) => {
    const pos = getPos(), parent = pos != null ? view.state.doc.resolve(pos).parent : null;
    if (parent && parent.type === N.mediaSingle) {
      const dom = h('div.rte-media');
      const paint = n => {
        clear(dom);
        const src = ctx.src(n.attrs);
        if (src) {
          const img = h('img', { src, alt: n.attrs.alt || '', draggable: false, onload: () => ctx.sized(img, n, getPos), onerror: () => dom.classList.add('broken') });
          dom.append(img);
        } else dom.append(h('div.rte-media-ph', icon('image'), ' ', n.attrs.type === 'external' ? n.attrs.url || '' : n.attrs.alt || T('Picture')));
        dom.classList.toggle('busy', String(n.attrs.id || '').startsWith('pending-'));
      };
      paint(node);
      return { dom, update: n => { if (n.type !== node.type) return false; if (n.attrs.id !== node.attrs.id || n.attrs.url !== node.attrs.url) paint(n); node = n; return true; } };
    }
    // A file of a group: a chip that opens it.
    const dom = h('div.rte-file', { title: node.attrs.alt || '' });
    const paint = n => {
      clear(dom).append(icon('file'), ' ', n.attrs.alt || T('File'));
      dom.classList.toggle('busy', String(n.attrs.id || '').startsWith('pending-'));
    };
    paint(node);
    dom.addEventListener('dblclick', () => { const src = ctx.src(node.attrs); if (src) window.open(src + '?download=1&name=' + encodeURIComponent(node.attrs.alt || 'file'), '_blank', 'noopener'); });
    return { dom, update: n => { if (n.type !== node.type) return false; node = n; paint(n); return true; } };
  };
}
function taskView(node, view, getPos) {
  const box = h('input', { type: 'checkbox', checked: node.attrs.state === 'DONE', contenteditable: 'false', tabindex: -1,
    onmousedown: e => e.preventDefault(),
    onclick: e => {
      e.preventDefault();
      const pos = getPos(); if (pos == null) return;
      view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, state: node.attrs.state === 'DONE' ? 'TODO' : 'DONE' }));
    } });
  const body = h('div.task-body');
  const dom = h('li.task', { 'data-task': node.attrs.state }, h('span.rte-tbox', { contenteditable: 'false' }, box), body);
  const paint = n => { node = n; box.checked = n.attrs.state === 'DONE'; dom.classList.toggle('done', n.attrs.state === 'DONE'); };
  paint(node);
  return { dom, contentDOM: body, update: n => { if (n.type !== node.type) return false; paint(n); return true; }, stopEvent: e => e.target === box, ignoreMutation: m => m.target === box || m.target === dom };
}
function decisionView(node) {
  const body = h('div.rte-dec-body');
  return { dom: h('div.md-decision.rte-dec', h('span.rte-dec-i', { contenteditable: 'false' }, icon('split')), body), contentDOM: body };
}
function panelView(ctx) {
  return (node, view, getPos) => {
    const btn = h('button.rte-pi', { type: 'button', contenteditable: 'false', tabindex: -1, title: T('Panel type'), onmousedown: e => e.preventDefault(),
      onclick: () => ctx.menu(btn, [...PANELS.map(([t, label]) => ({ label, on: node.attrs.panelType === t, run: () => setAttrs({ panelType: t }) })),
        { label: T('Remove panel'), run: () => { const pos = getPos(); if (pos == null) return; view.dispatch(view.state.tr.replaceWith(pos, pos + node.nodeSize, node.content)); view.focus(); } }]) });
    const body = h('div.rte-pc'), dom = h('div.md-panel', btn, body);
    const setAttrs = a => { const pos = getPos(); if (pos != null) view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, ...a })); view.focus(); };
    const paint = n => { node = n; dom.className = 'md-panel p-' + n.attrs.panelType; clear(btn).append(icon({ info: 'info', note: 'pencil', success: 'circle-check', warning: 'triangle-alert', error: 'circle-alert' }[n.attrs.panelType] || 'info')); };
    paint(node);
    return { dom, contentDOM: body, update: n => { if (n.type !== node.type) return false; paint(n); return true; }, stopEvent: e => btn.contains(e.target), ignoreMutation: m => btn.contains(m.target) || m.target === dom };
  };
}
function expandView(node, view, getPos) {
  let open = true;
  const chev = h('button.rte-xc', { type: 'button', tabindex: -1, title: T('Fold'), onmousedown: e => e.preventDefault(), onclick: () => { open = !open; dom.classList.toggle('shut', !open); } }, icon('chevron-down'));
  const title = h('input.rte-xt', { value: node.attrs.title || '', placeholder: T('Title (optional)'),
    onkeydown: e => { if (e.key === 'Enter' || e.key === 'ArrowDown') { e.preventDefault(); const pos = getPos(); if (pos != null) { view.focus(); view.dispatch(view.state.tr.setSelection(Selection.near(view.state.doc.resolve(pos + 1)))); } } },
    oninput: debounce(() => { const pos = getPos(); if (pos != null && title.value !== node.attrs.title) view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, title: title.value }).setMeta('addToHistory', true)); }, 250) });
  const body = h('div.rte-xb'), dom = h('div.md-expand.rte-x', h('div.rte-xh', { contenteditable: 'false' }, chev, title), body);
  return { dom, contentDOM: body,
    update: n => { if (n.type !== node.type) return false; node = n; if (document.activeElement !== title) title.value = n.attrs.title || ''; return true; },
    stopEvent: e => e.target === title || chev.contains(e.target), ignoreMutation: m => !body.contains(m.target) };
}
function codeView(node, view, getPos) {
  const lang = h('select.rte-lang', { tabindex: -1, title: T('Language'), onchange: () => { const pos = getPos(); if (pos != null) view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, language: lang.value || null })); view.focus(); } },
    ...[...new Set([...LANGS, node.attrs.language || ''])].map(l => h('option', { value: l }, l || T('Plain text'))));
  const code = h('code'), dom = h('pre.rte-code', h('div.rte-code-head', { contenteditable: 'false' }, lang), code);
  const paint = n => { node = n; if (![...lang.options].some(x => x.value === (n.attrs.language || ''))) lang.append(h('option', { value: n.attrs.language }, n.attrs.language)); lang.value = n.attrs.language || ''; };
  paint(node);
  return { dom, contentDOM: code, update: n => { if (n.type !== node.type) return false; paint(n); return true; }, stopEvent: e => lang.contains(e.target), ignoreMutation: m => !code.contains(m.target) };
}
function statusView(ctx) {
  return (node, view, getPos) => {
    const dom = h('span.md-status', { title: T('Click to change') });
    const paint = n => { node = n; dom.className = 'md-status c-' + (n.attrs.color || 'neutral'); dom.textContent = (n.attrs.text || T('Status')).toUpperCase(); };
    paint(node);
    dom.addEventListener('mousedown', e => {
      e.preventDefault();
      const set = a => { const pos = getPos(); if (pos != null) view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, ...a })); };
      const input = h('input.input', { value: node.attrs.text || '', placeholder: T('Status'), oninput: () => set({ text: input.value }),
        onkeydown: ev => { if (ev.key === 'Enter' || ev.key === 'Escape') { ev.preventDefault(); ev.stopPropagation(); close(); view.focus(); } } });
      const close = ctx.pop(dom, h('div.rte-pop-col', input, h('div.rte-swatches', STATUS.map(([c, label]) => h('button.md-status.c-' + c, { type: 'button', title: label, onclick: () => { set({ color: c }); input.focus(); } }, label)))));
      input.focus(); input.select();
    });
    return { dom, update: n => { if (n.type !== node.type) return false; paint(n); return true; }, stopEvent: () => false, ignoreMutation: () => true };
  };
}
function dateView(ctx) {
  return (node, view, getPos) => {
    const dom = h('span.md-date', { title: T('Click to change') });
    const iso = ts => { const d = new Date(+ts); return isNaN(d) ? '' : d.toISOString().slice(0, 10); };
    const paint = n => { node = n; const d = new Date(+n.attrs.timestamp); dom.textContent = isNaN(d) ? String(n.attrs.timestamp || '') : d.toLocaleDateString(undefined, { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' }); };
    paint(node);
    dom.addEventListener('mousedown', e => {
      e.preventDefault();
      const input = h('input.input', { type: 'date', value: iso(node.attrs.timestamp),
        onchange: () => { const t = Date.parse(input.value + 'T00:00:00Z'); const pos = getPos(); if (!isNaN(t) && pos != null) view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...node.attrs, timestamp: String(t) })); },
        onkeydown: ev => { if (ev.key === 'Enter' || ev.key === 'Escape') { ev.preventDefault(); ev.stopPropagation(); close(); view.focus(); } } });
      const close = ctx.pop(dom, input);
      input.focus();
    });
    return { dom, update: n => { if (n.type !== node.type) return false; paint(n); return true; }, ignoreMutation: () => true };
  };
}
function cardView(ctx) {
  return node => {
    const url = node.attrs.url || '', key = ctx.site && jiraKey(url, ctx.site);
    const dom = key && ctx.onKey ? h('span.rte-icard', issuePill(key, ctx.onKey, url)) : h('span.rte-icard.rte-link', { title: url }, icon('link'), ' ', url.replace(/^https?:\/\//, ''));
    return { dom, ignoreMutation: () => true };
  };
}
const keptView = (ctx) => (node) => {
  const inline = node.type === N.unknownInline;
  const dom = h((inline ? 'span' : 'div') + '.rte-kept', { title: ctx.wiki ? T('Kept as it is: edit it in Confluence') : T('Kept as it is: edit it in Jira') }, icon('lock'), ' ', keptLabel(node.attrs.raw));
  return { dom, ignoreMutation: () => true };
};

// ---- the editor
export function rte(o) {
  const media = new Map(); // media id → a picture to show for it (a pasted file's while it uploads, an attachment's)
  const uploaded = new Set(); // media ids uploaded here: their pictures' sizes are written in
  let seq = 0;
  const ctx = {
    site: o.site, onKey: o.onKey, wiki: o.wiki,
    src: a => {
      if (!a) return '';
      if (a.type === 'external') return /^(data:image\/|blob:)/.test(a.url || '') ? a.url : '';
      if (media.has(a.id)) return media.get(a.id);
      return o.issueKey && /^[0-9a-f-]{36}$/i.test(a.id || '') ? '/api/issues/' + encodeURIComponent(o.issueKey) + '/media/' + a.id : '';
    },
    // A picture's own size goes in its media node (Jira lays it out by it) once it is known.
    sized: (img, n, getPos) => {
      if (!uploaded.has(n.attrs.id) || (n.attrs.width && n.attrs.height)) return;
      const pos = getPos(); if (pos == null || !img.naturalWidth) return;
      const cur = view.state.doc.nodeAt(pos);
      if (!cur || cur.type !== N.media || cur.attrs.width) return;
      view.dispatch(view.state.tr.setNodeMarkup(pos, null, { ...cur.attrs, width: img.naturalWidth, height: img.naturalHeight }).setMeta('addToHistory', false));
    },
    menu: (anchor, items) => menu(anchor, items),
    pop: (anchor, content) => pop(anchor, content),
  };

  // ---- floating bits: a popover at an element, a menu, the context bar
  let popEl = null;
  function closePop() { if (popEl) { popEl.remove(); popEl = null; removeEventListener('mousedown', outside, true); } }
  function outside(e) { if (popEl && !popEl.contains(e.target)) closePop(); }
  function pop(anchor, content) {
    closePop();
    popEl = h('div.rte-pop', content);
    document.body.append(popEl);
    const r = anchor.getBoundingClientRect(), w = popEl.offsetWidth, below = innerHeight - r.bottom > popEl.offsetHeight + 8;
    Object.assign(popEl.style, { left: Math.max(4, Math.min(r.left, innerWidth - w - 4)) + 'px', top: (below ? r.bottom + 4 : Math.max(4, r.top - popEl.offsetHeight - 4)) + 'px' });
    setTimeout(() => addEventListener('mousedown', outside, true));
    return closePop;
  }
  function menu(anchor, items) {
    const close = pop(anchor, h('div.rte-menu', { role: 'menu' }, items.map(it => it.sep ? h('i.rte-sep')
      : h('button.rte-mi' + (it.on ? '.on' : ''), { type: 'button', role: 'menuitem', onmousedown: e => e.preventDefault(), onclick: () => { close(); it.run(); } },
        it.icon ? icon(it.icon) : null, it.swatch ? h('span.rte-sw', { style: { '--c': it.swatch } }) : null, h('span', it.label), it.key ? h('span.dim.rte-mk', kbd(it.key).join(' ')) : null))));
    return close;
  }

  // ---- toolbar
  const run = c => () => { c(view.state, view.dispatch, view); view.focus(); };
  const tb = (name, title, c, active, opts = {}) => {
    const b = h('button.tb', { type: 'button', title, tabindex: -1, 'aria-pressed': 'false', onmousedown: e => e.preventDefault(), onclick: opts.click || run(c) }, opts.label || icon(name));
    b._active = active; b._cmd = c;
    return b;
  };
  const blockLabel = h('span.rte-bt-l', T('Normal text'));
  const blockBtn = h('button.tb.txt.rte-bt', { type: 'button', tabindex: -1, title: T('Text style'), onmousedown: e => e.preventDefault(),
    onclick: () => menu(blockBtn, [
      { label: T('Normal text'), key: 'mod+alt+0', on: blockActive(view.state, N.paragraph), run: run(setBlockType(N.paragraph)) },
      ...[1, 2, 3, 4, 5, 6].map(l => ({ label: T('Heading %d', l), key: 'mod+alt+' + l, on: blockActive(view.state, N.heading, { level: l }), run: run(setBlockType(N.heading, { level: l })) })),
    ]) }, blockLabel, icon('chevron-down'));
  const colourBtn = h('button.tb', { type: 'button', tabindex: -1, title: T('Text colour, sub/superscript, clear formatting'), onmousedown: e => e.preventDefault(),
    onclick: () => menu(colourBtn, [
      ...COLOURS.map(([c, label], i) => ({ label, swatch: c, run: run(setColour(M.textColor, i ? c : null)) })),
      { sep: true },
      { label: T('Subscript'), icon: 'subscript', on: markActive(view.state, M.subsup, { type: 'sub' }), run: run(toggleWith(M.subsup, { type: 'sub' })) },
      { label: T('Superscript'), icon: 'superscript', on: markActive(view.state, M.subsup, { type: 'sup' }), run: run(toggleWith(M.subsup, { type: 'sup' })) },
      { label: T('Clear formatting'), icon: 'remove-formatting', run: run(clearMarks) },
    ]) }, icon('baseline'));
  const insertBtn = h('button.tb', { type: 'button', tabindex: -1, title: T('Insert… (or type / in the text)'), onmousedown: e => e.preventDefault(),
    onclick: () => menu(insertBtn, slash().map(s => ({ label: s.label, icon: s.icon, run: () => { s.run(); view.focus(); } }))) }, icon('plus'));
  const buttons = [
    tb('bold', modText(T('Bold (ctrl+b)')), toggleMark(M.strong), s => markActive(s, M.strong)),
    tb('italic', modText(T('Italic (ctrl+i)')), toggleMark(M.em), s => markActive(s, M.em)),
    tb('underline', modText(T('Underline (ctrl+u)')), toggleMark(M.underline), s => markActive(s, M.underline)),
    tb('strikethrough', modText(T('Strikethrough (ctrl+shift+x)')), toggleMark(M.strike), s => markActive(s, M.strike)),
    tb('code', modText(T('Inline code (ctrl+e)')), toggleMark(M.code), s => markActive(s, M.code)),
    tb('link', modText(T('Link (ctrl+k)')), null, s => markActive(s, M.link), { click: () => editLink() }),
    h('i.sep'),
    tb('list', modText(T('Bulleted list (ctrl+shift+8)')), toggleList(N.bulletList), s => !!ancestor(s, N.bulletList, N.orderedList, N.taskList) && ancestor(s, N.bulletList, N.orderedList, N.taskList).node.type === N.bulletList),
    tb('list-ordered', modText(T('Numbered list (ctrl+shift+7)')), toggleList(N.orderedList), s => !!ancestor(s, N.bulletList, N.orderedList, N.taskList) && ancestor(s, N.bulletList, N.orderedList, N.taskList).node.type === N.orderedList),
    tb('list-todo', modText(T('Task list (ctrl+shift+9)')), toggleList(N.taskList), s => !!ancestor(s, N.taskList)),
    tb('quote', T('Quote'), toggleWrap(N.blockquote), s => !!ancestor(s, N.blockquote)),
    tb('square-code', T('Code block'), toggleBlock(N.codeBlock), s => blockActive(s, N.codeBlock)),
    tb('table', T('Table'), insertBlock(table()), null),
    h('i.sep'),
    tb('at-sign', T('Mention someone'), null, null, { click: () => { insertText('@'); } }),
    tb('smile', T('Emoji'), null, null, { click: () => { insertText(':'); } }),
    o.upload || o.onFiles ? tb('paperclip', T('Attach files (or paste, or drop them)'), null, null, { click: () => fileIn.click() }) : null,
  ].filter(Boolean);
  const fileIn = h('input', { type: 'file', multiple: true, hidden: true, onchange: () => { if (fileIn.files.length) files([...fileIn.files]); fileIn.value = ''; } });
  const toolbar = h('div.ed-tools.rte-tools', { role: 'toolbar' }, blockBtn, h('i.sep'), ...buttons.slice(0, 5), colourBtn, ...buttons.slice(5), insertBtn, fileIn);
  const paintTools = state => {
    for (const b of buttons) if (b._active) { const on = !!b._active(state); b.classList.toggle('on', on); b.setAttribute('aria-pressed', String(on)); }
    const p = state.selection.$from.parent;
    blockLabel.textContent = p.type === N.heading ? T('Heading %d', p.attrs.level) : p.type === N.codeBlock ? T('Code') : T('Normal text');
    colourBtn.classList.toggle('on', markActive(state, M.textColor) || markActive(state, M.subsup));
  };
  const insertText = t => { view.focus(); view.dispatch(view.state.tr.insertText(t)); };

  // ---- links
  function editLink() {
    const s = view.state, { from, to, empty, $from } = s.selection;
    let a = from, b = to, href = '';
    const lm = $from.marks().find(m => m.type === M.link) || (s.doc.nodeAt(from) && s.doc.nodeAt(from).marks.find(m => m.type === M.link));
    if (lm) {
      // The whole link the caret is in.
      const range = markRange(s.doc, from, lm);
      if (range) { a = range.from; b = range.to; }
      href = lm.attrs.href || '';
    }
    const text = s.doc.textBetween(a, b, ' ');
    const url = h('input.input', { value: href || (/^https?:\/\/\S+$/.test(text) ? text : ''), placeholder: 'https://…' });
    const label = h('input.input', { value: text, placeholder: T('Text to show') });
    const apply = () => {
      const u = url.value.trim();
      closePop(); view.focus();
      let tr = view.state.tr;
      if (!u) { if (lm) tr = tr.removeMark(a, b, M.link); return view.dispatch(tr); }
      const t = label.value || u;
      if (t !== text || a === b) tr = tr.insertText(t, a, b);
      tr.addMark(a, a + t.length, M.link.create({ href: u }));
      view.dispatch(tr.setSelection(TextSelection.create(tr.doc, a + t.length)));
    };
    const key = e => { if (e.key === 'Enter') { e.preventDefault(); apply(); } else if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); closePop(); view.focus(); } };
    url.addEventListener('keydown', key); label.addEventListener('keydown', key);
    const at = view.coordsAtPos(a);
    const anchor = { getBoundingClientRect: () => ({ left: at.left, right: at.right, top: at.top, bottom: at.bottom }) };
    pop(anchor, h('div.rte-pop-col.rte-linkform', url, label, h('div.row.end', lm && h('button.btn.ghost.sm', { type: 'button', onclick: () => { url.value = ''; apply(); } }, T('Remove link')), h('button.btn.primary.sm', { type: 'button', onclick: apply }, T('Apply')))));
    (href || !url.value ? url : label).focus();
  }
  const markRange = (doc, pos, mark) => {
    const $p = doc.resolve(pos), parent = $p.parent, start = $p.start();
    let from = -1, to = -1, at = start;
    for (let i = 0; i < parent.childCount; i++) {
      const c = parent.child(i), end = at + c.nodeSize, has = mark.isInSet(c.marks);
      if (has && from < 0) from = at;
      if (has) to = end;
      if (!has && from >= 0 && to < pos) { from = -1; to = -1; }
      if (!has && from >= 0 && to >= pos) break;
      at = end;
    }
    return from >= 0 && to >= pos ? { from, to } : null;
  };

  // ---- `/`, `@` and `:` at the caret: a list to pick from
  const list = h('div.mention-pop.rte-list', { hidden: true, role: 'listbox' });
  let items = [], pick = 0, trig = null; // trig: {kind, from, to, q}
  const closeList = () => { list.hidden = true; items = []; trig = null; };
  const paintList = () => {
    clear(list).append(...items.map((it, i) => h('div.mp' + (i === pick ? '.sel' : ''), { role: 'option', onmousedown: e => { e.preventDefault(); accept(i); } },
      trig.kind === 'mention' ? [o.ui ? o.ui.avatar(it.DisplayName, null, 18) : null, it.DisplayName]
        : trig.kind === 'emoji' ? [h('span.mp-g', it.Glyph), ':' + it.Name + ':']
          : [h('span.rte-li', icon(it.icon || 'plus')), h('b', it.label), it.hint ? h('span.dim', ' ' + it.hint) : null])));
    list.hidden = !items.length;
    if (list.hidden) return;
    const r = view.coordsAtPos(trig.from), below = innerHeight - r.bottom > 280 || r.top < 280;
    Object.assign(list.style, { position: 'fixed', left: Math.max(4, Math.min(r.left - 8, innerWidth - 290)) + 'px', right: 'auto',
      top: below ? r.bottom + 4 + 'px' : 'auto', bottom: below ? 'auto' : innerHeight - r.top + 4 + 'px' });
    const s = list.querySelector('.sel'); if (s) s.scrollIntoView({ block: 'nearest' });
  };
  function accept(i) {
    const it = items[i], t = trig;
    if (!it || !t) return;
    closeList();
    const tr = view.state.tr.delete(t.from, t.to);
    if (t.kind === 'mention') {
      tr.replaceSelectionWith(N.mention.create({ id: it.AccountID, text: '@' + it.DisplayName, accessLevel: '' }), false).insertText(' ');
      view.dispatch(tr);
    } else if (t.kind === 'emoji') {
      tr.replaceSelectionWith(emojiNode(it.Name, it.Glyph), false).insertText(' ');
      view.dispatch(tr);
      if (o.api) o.api.post('/emoji/used', { Name: it.Name }).catch(() => {});
    } else { view.dispatch(tr); it.run(); }
    view.focus();
  }
  const people = () => (o.people ? [...o.people()] : []);
  const users = debounce(async (q, at) => {
    const local = people().filter(([n]) => n.toLowerCase().includes(q.toLowerCase())).map(([n, id]) => ({ DisplayName: n, AccountID: id }));
    let remote = [];
    try {
      const r = o.api ? await o.api.get('/users?' + (o.issueKey ? 'issue=' + encodeURIComponent(o.issueKey) : 'project=' + encodeURIComponent((typeof o.project === 'function' ? o.project() : o.project) || '')) + '&q=' + encodeURIComponent(q)) : [];
      remote = (Array.isArray(r) ? r : []).map(u => ({ DisplayName: u.DisplayName, AccountID: u.AccountID }));
    } catch (e) { /* local names only */ }
    if (!trig || trig.kind !== 'mention' || trig.from !== at) return;
    const seen = new Set();
    items = [...remote, ...local].filter(u => u.DisplayName && u.AccountID && !seen.has(u.AccountID) && seen.add(u.AccountID)).slice(0, 8);
    pick = 0; paintList();
  }, 120);
  const emojis = debounce(async (q, at) => {
    let r = [];
    try { r = o.api ? await o.api.get('/emoji?q=' + encodeURIComponent(q)) : []; } catch (e) { /* none */ }
    if (!trig || trig.kind !== 'emoji' || trig.from !== at) return;
    items = r; pick = 0; paintList();
  }, 80);
  function slash() {
    const c = f => () => f(view.state, view.dispatch, view);
    return [
      { name: 'h1', label: T('Heading 1'), icon: 'heading-1', run: c(setBlockType(N.heading, { level: 1 })) },
      { name: 'h2', label: T('Heading 2'), icon: 'heading-2', run: c(setBlockType(N.heading, { level: 2 })) },
      { name: 'h3', label: T('Heading 3'), icon: 'heading-3', run: c(setBlockType(N.heading, { level: 3 })) },
      { name: 'bullet', label: T('Bulleted list'), icon: 'list', run: c(toggleList(N.bulletList)) },
      { name: 'numbered', label: T('Numbered list'), icon: 'list-ordered', run: c(toggleList(N.orderedList)) },
      { name: 'task', label: T('Task list'), icon: 'list-todo', hint: '[]', run: c(toggleList(N.taskList)) },
      { name: 'decision', label: T('Decision'), icon: 'split', hint: '<>', run: c(insertBlock(N.decisionList.create({ localId: localId() }, N.decisionItem.create({ localId: localId(), state: 'DECIDED' })))) },
      { name: 'table', label: T('Table'), icon: 'table', run: c(insertBlock(table())) },
      { name: 'code', label: T('Code block'), icon: 'square-code', hint: '```', run: c(setBlockType(N.codeBlock)) },
      { name: 'quote', label: T('Quote'), icon: 'quote', hint: '>', run: c(toggleWrap(N.blockquote)) },
      ...PANELS.map(([t, label]) => ({ name: t, label: T('Panel: %s', label), icon: { info: 'info', note: 'pencil', success: 'circle-check', warning: 'triangle-alert', error: 'circle-alert' }[t],
        run: c((state, dispatch) => wrapIn(N.panel, { panelType: t })(state, dispatch) || insertBlock(N.panel.create({ panelType: t }, N.paragraph.create()))(state, dispatch)) })),
      { name: 'expand', label: T('Expand (collapsible)'), icon: 'list-collapse', run: c(insertBlock(N.expand.create({ title: '' }, N.paragraph.create()))) },
      { name: 'rule', label: T('Divider'), icon: 'minus', hint: '---', run: c(insertBlock(N.rule.create())) },
      { name: 'date', label: T('Date'), icon: 'calendar', run: c(insertNode(N.date.create({ timestamp: today() }))) },
      { name: 'status', label: T('Status'), icon: 'tag', run: () => { c(insertNode(N.status.create({ text: 'TO DO', color: 'neutral', localId: localId() })))(); setTimeout(() => { const sel = view.state.selection.from - 1; const d = view.nodeDOM(sel); if (d) d.dispatchEvent(new MouseEvent('mousedown', { bubbles: true })); }); } },
      { name: 'mention', label: T('Mention someone'), icon: 'at-sign', hint: '@', run: () => insertText('@') },
      { name: 'emoji', label: T('Emoji'), icon: 'smile', hint: ':', run: () => insertText(':') },
      { name: 'link', label: T('Link'), icon: 'link', run: () => editLink() },
      ...(o.upload || o.onFiles ? [{ name: 'image', label: T('Picture or file'), icon: 'image', run: () => fileIn.click() }] : []),
    ];
  }
  function trigger(state) {
    const { $from, empty } = state.selection;
    if (!empty || $from.parent.type.spec.code || !view.hasFocus()) { if (trig) closeList(); return; }
    const before = $from.parent.textBetween(Math.max(0, $from.parentOffset - 40), $from.parentOffset, null, '￼');
    let m = /(?:^|[\s(])@([^\s@￼]{0,30})$/u.exec(before);
    if (m) { const from = $from.pos - m[1].length - 1; if (!trig || trig.kind !== 'mention' || trig.from !== from) items = []; trig = { kind: 'mention', from, to: $from.pos }; return users(m[1], from); }
    m = /(?:^|\s):([a-z0-9_+-]{2,})$/i.exec(before);
    if (m) { const from = $from.pos - m[1].length - 1; trig = { kind: 'emoji', from, to: $from.pos }; return emojis(m[1].toLowerCase(), from); }
    m = /(?:^|\s)\/([a-z0-9]{0,12})$/i.exec(before);
    if (m) {
      const q = m[1].toLowerCase();
      trig = { kind: 'slash', from: $from.pos - q.length - 1, to: $from.pos };
      items = slash().filter(s => s.name.startsWith(q) || (q.length > 1 && s.label.toLowerCase().includes(q))).slice(0, 10);
      pick = 0; return paintList();
    }
    if (trig) closeList();
  }
  const listKeys = new Plugin({
    props: {
      handleKeyDown(v, e) {
        if (list.hidden || !items.length) { if (e.key === 'Escape' && trig) { closeList(); return true; } return false; }
        const k = e.key;
        if (k === 'ArrowDown' || (k === 'Tab' && !e.shiftKey)) pick = (pick + 1) % items.length;
        else if (k === 'ArrowUp' || (k === 'Tab' && e.shiftKey)) pick = (pick - 1 + items.length) % items.length;
        else if (k === 'Enter') { accept(pick); return true; } else if (k === 'Escape') { closeList(); return true; } else return false;
        paintList();
        return true;
      },
    },
  });

  // ---- the context bar: over a picture, a link, a table
  const bar = h('div.rte-bar', { hidden: true, role: 'toolbar', onmousedown: e => { if (e.target.tagName !== 'INPUT') e.preventDefault(); } });
  let barFor = null;
  const bb = (name, title, fn, on) => h('button.tb' + (on ? '.on' : ''), { type: 'button', title, tabindex: -1, onclick: () => { fn(); } }, icon(name));
  function paintBar(state) {
    const sel = state.selection;
    let target = null, kind = '', content = [];
    const ms = sel instanceof NodeSelection && sel.node.type === N.mediaSingle ? { node: sel.node, pos: sel.from }
      : (() => { const a = ancestor(state, N.mediaSingle); return a ? { node: a.node, pos: a.pos } : null; })();
    if (ms) {
      kind = 'img'; target = view.nodeDOM(ms.pos);
      target = target && (target.querySelector('.rte-img-box') || target);
      const set = a => { const tr = view.state.tr.setNodeMarkup(ms.pos, null, { ...ms.node.attrs, ...a }); view.dispatch(tr.setSelection(NodeSelection.create(tr.doc, ms.pos))); };
      const m = ms.node.firstChild, src = m && ctx.src(m.attrs);
      const hasCap = ms.node.childCount > 1;
      content = [
        ...LAYOUTS.map(([l, ic, label]) => bb(ic, label, () => set({ layout: l }), (ms.node.attrs.layout || 'center') === l)),
        h('i.sep'),
        bb('shrink', T('Smaller'), () => { const w = target.getBoundingClientRect().width; set({ width: Math.max(48, Math.round(w * 0.75)), widthType: 'pixel' }); }),
        bb('expand', T('Larger'), () => { const w = target.getBoundingClientRect().width; const max = view.dom.clientWidth; const nw = Math.round(w / 0.75); set(nw >= max ? { width: null, widthType: null } : { width: nw, widthType: 'pixel' }); }),
        bb('image-upscale', T('Full width (drag an edge to size it)'), () => set({ width: null, widthType: null }), !ms.node.attrs.width),
        h('i.sep'),
        bb('type', T('Alternative text'), () => {
          const input = h('input.input', { value: (m && m.attrs.alt) || '', placeholder: T('Describe the picture') });
          const close = pop(bar, h('div.rte-pop-col', input));
          input.focus(); input.select();
          input.addEventListener('keydown', e => {
            if (e.key !== 'Enter' && e.key !== 'Escape') return;
            e.preventDefault(); e.stopPropagation();
            if (e.key === 'Enter' && m) view.dispatch(view.state.tr.setNodeMarkup(ms.pos + 1, null, { ...m.attrs, alt: input.value || null }));
            close(); view.focus();
          });
        }),
        bb('captions', hasCap ? T('Remove the caption') : T('Add a caption'), () => {
          let tr = view.state.tr;
          if (hasCap) { const c = ms.pos + 1 + ms.node.firstChild.nodeSize; tr = tr.delete(c, c + ms.node.child(1).nodeSize); view.dispatch(tr); return; }
          const at = ms.pos + 1 + ms.node.firstChild.nodeSize;
          tr = tr.insert(at, N.caption.create());
          view.dispatch(tr.setSelection(TextSelection.create(tr.doc, at + 1))); view.focus();
        }, hasCap),
        src && bb('download', T('Download'), () => window.open(src + (src.includes('?') ? '&' : '?') + 'download=1&name=' + encodeURIComponent((m && m.attrs.alt) || 'image'), '_blank', 'noopener')),
        bb('trash-2', T('Remove'), () => { view.dispatch(view.state.tr.delete(ms.pos, ms.pos + ms.node.nodeSize)); view.focus(); }),
      ];
    } else if (sel.empty && sel.$from.marks().some(m => m.type === M.link) || (!sel.empty && sel.$from.sameParent(sel.$to) && markActive(state, M.link) && sel.$from.marks().some(m => m.type === M.link))) {
      const lm = sel.$from.marks().find(m => m.type === M.link);
      kind = 'link'; target = { getBoundingClientRect: () => { const c = view.coordsAtPos(sel.from); return { left: c.left, right: c.right, top: c.top, bottom: c.bottom, width: 0 }; } };
      const href = lm.attrs.href || '';
      content = [
        h('a.rte-href', { href: /^(https?:|mailto:)/i.test(href) ? href : null, target: '_blank', rel: 'noopener noreferrer', title: href,
          onclick: e => { const k = o.site && jiraKey(href, o.site); if (k && o.onKey) { e.preventDefault(); o.onKey(k); } } }, href.replace(/^https?:\/\//, '').slice(0, 48) + (href.length > 56 ? '…' : '')),
        bb('pencil', modText(T('Edit link (ctrl+k)')), () => editLink()),
        bb('unlink', T('Remove link'), () => { const r = markRange(view.state.doc, sel.from, lm); if (r) view.dispatch(view.state.tr.removeMark(r.from, r.to, M.link)); view.focus(); }),
      ];
    } else if (isInTable(state)) {
      const t = ancestor(state, N.table);
      kind = 'table'; target = t && view.nodeDOM(t.pos);
      const c = f => () => { f(view.state, view.dispatch, view); view.focus(); };
      const cellBtn = h('button.tb', { type: 'button', tabindex: -1, title: T('Cell colour'), onclick: () => menu(cellBtn, CELLS.map(([col, label]) => ({ label, swatch: col || 'transparent', run: c(setCellAttr('background', col)) }))) }, icon('paint-bucket'));
      content = [
        bb('between-horizontal-start', T('Row above'), c(addRowBefore)), bb('between-horizontal-end', T('Row below'), c(addRowAfter)),
        bb('between-vertical-start', T('Column left'), c(addColumnBefore)), bb('between-vertical-end', T('Column right'), c(addColumnAfter)),
        h('i.sep'),
        bb('rows-3', T('Delete row'), c(deleteRow)), bb('columns-3', T('Delete column'), c(deleteColumn)),
        h('i.sep'),
        bb('heading', T('Header row'), c(toggleHeaderRow)), bb('panel-left-dashed', T('Header column'), c(toggleHeaderColumn)),
        bb('table-cells-merge', T('Merge cells'), c(mergeCells)), bb('table-cells-split', T('Split cell'), c(splitCell)), cellBtn,
        bb('list-ordered', T('Numbered rows'), () => { const tt = ancestor(view.state, N.table); if (tt) view.dispatch(view.state.tr.setNodeMarkup(tt.pos, null, { ...tt.node.attrs, isNumberColumnEnabled: !tt.node.attrs.isNumberColumnEnabled })); view.focus(); }, t && t.node.attrs.isNumberColumnEnabled),
        h('i.sep'),
        bb('trash-2', T('Delete table'), c(deleteTable)),
      ];
    }
    if (!target || !view.hasFocus() && !bar.contains(document.activeElement) && !popEl) { bar.hidden = true; barFor = null; return; }
    clear(bar).append(...content.filter(Boolean));
    barFor = kind;
    bar.hidden = false;
    bar._target = target;
    placeBar();
    requestAnimationFrame(placeBar); // again once the change is laid out
  }
  function placeBar() {
    if (bar.hidden || !bar._target) return;
    const r = bar._target.getBoundingClientRect(), box = scroller.getBoundingClientRect(), hgt = bar.offsetHeight || 32;
    const top = r.top - hgt - 6 >= Math.max(box.top, 0) ? r.top - hgt - 6 : Math.min(r.bottom + 6, innerHeight - hgt - 4);
    const visible = r.bottom > box.top && r.top < box.bottom;
    bar.style.visibility = visible ? '' : 'hidden';
    // Over the middle of a picture, at the start of a table or a link.
    const left = barFor === 'img' ? r.left + r.width / 2 - bar.offsetWidth / 2 : r.left;
    Object.assign(bar.style, { position: 'fixed', top: top + 'px', left: Math.max(4, Math.min(left, innerWidth - bar.offsetWidth - 4)) + 'px' });
  }

  // ---- files: uploaded where they land, a picture of them shown meanwhile
  let uploading = 0;
  async function files(list, at) {
    if (!list.length) return;
    if (!o.upload) { if (o.onFiles) o.onFiles(list); return; }
    const marks = list.map(f => {
      const id = 'pending-' + (++seq), img = f.type && f.type.startsWith('image/');
      if (img) media.set(id, URL.createObjectURL(f));
      const m = N.media.create({ id, type: 'file', collection: '', alt: f.name || T('image') });
      return { id, node: img ? N.mediaSingle.create({ layout: 'center' }, m) : N.mediaGroup.create(null, m) };
    });
    let tr = view.state.tr;
    if (at != null) { let pos = at; for (const m of marks) { const p = insertPoint(tr.doc, pos, m.node); if (p == null) continue; tr.insert(p, m.node); pos = p + m.node.nodeSize; } }
    else for (const m of marks) tr.replaceSelectionWith(m.node);
    // Text goes on after the pictures (beside one that wraps): a paragraph to type in.
    if (tr.doc.lastChild.type !== N.paragraph) { tr.insert(tr.doc.content.size, N.paragraph.create()); }
    const $end = tr.selection.$to;
    if (!$end.parent.isTextblock) tr.setSelection(Selection.near($end, 1));
    view.dispatch(tr.scrollIntoView());
    uploading++;
    let made = [];
    try { made = (await o.upload(list)) || []; } finally { uploading--; }
    marks.forEach((m, i) => {
      const a = made[i], found = findMedia(view.state.doc, m.id);
      if (!found) return;
      const { pos, node, parent, parentPos } = found;
      let tr = view.state.tr;
      if (a && a.MediaID) {
        if (media.has(m.id)) media.set(a.MediaID, media.get(m.id));
        uploaded.add(a.MediaID);
        tr.setNodeMarkup(pos, null, { ...node.attrs, id: a.MediaID, collection: '', alt: a.Filename || node.attrs.alt });
      } else if (a && a.ID) {
        // Jira named no media file: a link to the attachment instead.
        const href = String(o.site || '').replace(/\/+$/, '') + '/secure/attachment/' + a.ID + '/' + encodeURIComponent(a.Filename || 'file');
        tr.replaceWith(parentPos, parentPos + parent.nodeSize, N.paragraph.create(null, schema.text(a.Filename || T('File'), [M.link.create({ href })])));
      } else tr.delete(parentPos, parentPos + parent.nodeSize);
      view.dispatch(tr.setMeta('addToHistory', false));
    });
    if (o.onChange) o.onChange();
  }
  function findMedia(doc, id) {
    let out = null;
    doc.descendants((n, pos, parent) => {
      if (out) return false;
      if (n.type === N.media && n.attrs.id === id) { const $p = doc.resolve(pos); out = { pos, node: n, parent, parentPos: $p.before() }; }
      return true;
    });
    return out;
  }

  // ---- paste: files upload, a URL over text links it, markdown comes in rich
  let shiftHeld = false;
  const looksMarkdown = t => /^(#{1,6} |\s*[-*+] |\s*\d+\. |> |```|\|.*\|\s*$|- \[[ x]\] )/m.test(t) || /\*\*[^*\n]+\*\*|\[[^\]\n]+\]\(https?:[^)\s]+\)|`[^`\n]+`/.test(t);
  const handlePaste = (v, e) => {
    const d = e.clipboardData;
    if (!d) return false;
    const fs = [...d.files];
    if (fs.length && (o.upload || o.onFiles)) { e.preventDefault(); files(fs); return true; }
    const text = d.getData('text/plain'), html = d.getData('text/html');
    const { $from, empty, from, to } = v.state.selection;
    if ($from.parent.type.spec.code) return false;
    const url = text.trim();
    if (!empty && /^https?:\/\/\S+$/.test(url) && $from.sameParent(v.state.selection.$to)) { v.dispatch(v.state.tr.addMark(from, to, M.link.create({ href: url }))); return true; }
    if (/^https?:\/\/\S+$/.test(url) && (!html || !rich(html))) {
      const key = o.site && jiraKey(url, o.site);
      const tr = key ? v.state.tr.replaceSelectionWith(N.inlineCard.create({ url }), false) : v.state.tr.replaceSelectionWith(schema.text(url, [M.link.create({ href: url })]), false);
      v.dispatch(tr.insertText(' ').scrollIntoView());
      return true;
    }
    if (shiftHeld || !o.convert || (html && rich(html)) || !looksMarkdown(text)) return false;
    e.preventDefault();
    const pos = v.state.selection;
    o.convert(text).then(adf => {
      const doc = fromADF(adf);
      const tr = view.state.tr.setSelection(pos.map(view.state.doc, view.state.tr.mapping)).replaceSelection(new Slice(doc.content, 1, 1));
      view.dispatch(tr.scrollIntoView());
    }).catch(() => view.dispatch(view.state.tr.insertText(text)));
    return true;
  };

  // ---- keys
  const keys = {
    'Mod-z': undo, 'Mod-Shift-z': redo, 'Mod-y': redo, Backspace: chainCommands(undoInputRule, itemBack(N.taskItem), itemBack(N.decisionItem), headingBack),
    'Mod-b': toggleMark(M.strong), 'Mod-i': toggleMark(M.em), 'Mod-u': toggleMark(M.underline), 'Mod-Shift-x': toggleMark(M.strike), 'Mod-Shift-s': toggleMark(M.strike), 'Mod-e': toggleMark(M.code),
    'Mod-k': () => { editLink(); return true; },
    'Mod-Alt-0': setBlockType(N.paragraph), ...Object.fromEntries([1, 2, 3, 4, 5, 6].map(l => ['Mod-Alt-' + l, toggleBlock(N.heading, { level: l })])),
    'Mod-Shift-7': toggleList(N.orderedList), 'Mod-Shift-8': toggleList(N.bulletList), 'Mod-Shift-9': toggleList(N.taskList), 'Mod-Alt-c': toggleBlock(N.codeBlock),
    'Shift-Enter': hardBreak, Enter: chainCommands(itemEnter(N.taskItem), itemEnter(N.decisionItem), codeExit, fenceEnter, splitListItem(N.listItem)),
    Tab: chainCommands(tableTab, sinkListItem(N.listItem), sinkTask), 'Shift-Tab': chainCommands(goToNextCell(-1), liftListItem(N.listItem), liftTask),
    'Alt-ArrowUp': moveBlock(-1), 'Alt-ArrowDown': moveBlock(1),
  };

  // ---- the view
  const plugins = [listKeys, rules(o), keymap(keys), keymap(baseKeymap), history(), dropCursor({ class: 'rte-drop' }), gapCursor(),
    columnResizing({ cellMinWidth: 48 }), tableEditing(), ids, placeholder(o.placeholder), keyMarks(o.isKey), codeColours()];
  const scroller = h('div.input.ed-ta.rte-body.md');
  if (o.rows) scroller.style.minHeight = 'calc(' + o.rows + ' * 1.5em + .714rem + 2px)';
  const mk = adf => EditorState.create({ doc: fromADF(adf), plugins });
  const view = new EditorView(scroller, {
    state: mk(o.doc),
    nodeViews: { mediaSingle: mediaSingleView(ctx), media: mediaView(ctx), taskItem: taskView, decisionItem: decisionView, panel: panelView(ctx), expand: expandView, nestedExpand: expandView,
      codeBlock: codeView, status: statusView(ctx), date: dateView(ctx), inlineCard: cardView(ctx), unknownBlock: keptView(ctx), unknownInline: keptView(ctx) },
    attributes: { class: 'rte-pm', spellcheck: 'true', role: 'textbox', 'aria-multiline': 'true', 'aria-label': o.placeholder || T('Text') },
    handlePaste,
    handleDrop: (v, e, slice, moved) => {
      const fs = e.dataTransfer ? [...e.dataTransfer.files] : [];
      if (moved || !fs.length || !(o.upload || o.onFiles)) return false;
      e.preventDefault();
      const at = v.posAtCoords({ left: e.clientX, top: e.clientY });
      files(fs, at ? at.pos : null);
      return true;
    },
    // A click on a picture selects all of it (its sizes and place show).
    handleClickOn: (v, pos, node, nodePos, e, direct) => {
      if (direct && node.type === N.media) {
        const $p = v.state.doc.resolve(nodePos);
        if ($p.parent.type === N.mediaSingle) { v.dispatch(v.state.tr.setSelection(NodeSelection.create(v.state.doc, $p.before()))); return true; }
      }
      return false;
    },
    handleDOMEvents: {
      keydown: (v, e) => { shiftHeld = e.shiftKey; return false; },
      keyup: (v, e) => { shiftHeld = e.shiftKey; return false; },
      blur: () => { setTimeout(() => { if (!view.hasFocus() && !bar.contains(document.activeElement) && !popEl) { bar.hidden = true; closeList(); } }, 150); return false; },
      focus: () => { paintBar(view.state); return false; },
      mousedown: (v, e) => {
        if (!(e.ctrlKey || e.metaKey)) return false;
        const a = e.target.closest && e.target.closest('a[href], .rte-key');
        if (!a) return false;
        e.preventDefault();
        const href = a.getAttribute('href'), k = a.dataset.key || (href && o.site && jiraKey(href, o.site));
        if (k && o.onKey) o.onKey(k); else if (href && /^(https?:|mailto:)/i.test(href)) window.open(href, '_blank', 'noopener');
        return true;
      },
    },
    dispatchTransaction(tr) {
      // A command that would make a document Jira refuses is dropped, not applied.
      if (tr.docChanged) { try { tr.doc.check(); } catch (e) { console.warn('rte: invalid document refused', e); return; } }
      const state = view.state.apply(tr);
      view.updateState(state);
      paintTools(state);
      trigger(state);
      paintBar(state);
      if (tr.docChanged && o.onChange) o.onChange();
    },
  });
  paintTools(view.state);
  const onScroll = () => { placeBar(); if (!list.hidden && trig) paintList(); };
  addEventListener('scroll', onScroll, true);
  addEventListener('resize', onScroll);
  // The list and the bar float over the page (in the panel, contain would hold position: fixed to it).
  const el = h('div.rte', scroller);
  document.body.append(list, bar);

  // Shortcuts the app takes before the editor sees them (ctrl+k, ctrl+b…) are the editor's while it has focus.
  let scope = null;
  view.dom.addEventListener('focus', () => {
    if (scope || !o.keys) return;
    scope = o.keys.scope('mdedit');
    const b = (spec, c, desc) => scope.bind(spec, () => { c(view.state, view.dispatch, view); }, desc, { input: true, group: 'Editor' });
    b('mod+b', keys['Mod-b'], T('bold')); b('mod+i', keys['Mod-i'], T('italic')); b('mod+u', keys['Mod-u'], T('underline'));
    b('mod+k', keys['Mod-k'], T('link')); b('mod+e', keys['Mod-e'], T('inline code')); b('mod+X', keys['Mod-Shift-x'], T('strikethrough'));
    b('mod+z', undo, T('undo')); b(['mod+Z', 'mod+y'], redo, T('redo'));
    scope.bind('Escape', () => { closeList(); }, '', { input: true, hidden: true, when: () => !!trig && !list.hidden });
  });
  view.dom.addEventListener('blur', () => { if (scope) { scope.dispose(); scope = null; } });

  return {
    el, toolbar, view,
    getDoc: () => toADF(view.state.doc),
    setDoc: adf => { view.updateState(mk(adf)); paintTools(view.state); closeList(); paintBar(view.state); },
    isEmpty: () => isEmpty(view.state.doc),
    focus: end => { view.focus(); if (end) view.dispatch(view.state.tr.setSelection(Selection.atEnd(view.state.doc))); },
    busy: () => uploading > 0 || !!findPending(view.state.doc),
    files,
    destroy: () => { closePop(); closeList(); list.remove(); bar.remove(); if (scope) scope.dispose(); removeEventListener('scroll', onScroll, true); removeEventListener('resize', onScroll); for (const u of media.values()) if (u.startsWith('blob:')) URL.revokeObjectURL(u); view.destroy(); },
  };
}
const findPending = doc => { let hit = false; doc.descendants(n => { if (n.type === N.media && String(n.attrs.id || '').startsWith('pending-')) hit = true; return !hit; }); return hit; };
