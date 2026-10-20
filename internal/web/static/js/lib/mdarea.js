// A text field that draws its text as rich lines: lines(text) → [{c, h, s, g}]
// (lib/mdhl.js: classes, HTML that wraps the line's text without changing it,
// style, group start), one <div class="ln"> each. A plaintext contenteditable
// carrying a textarea's API (value, selectionStart/End, setSelectionRange,
// setRangeText, readOnly), so callers use it as one. Only the lines that changed
// are redrawn. The lines the caret is on (a table or code block: all of it) are
// .act, so css can show their markdown and draw the others rendered (.live).
// Re-rendering clears the browser's undo, so it keeps its own (ctrl+z,
// ctrl+shift+z, ctrl+y); copy and cut take the markdown, hidden markers too.
// While the field is .live, an .atom (a line or a span: a kept block, a
// mention) is one thing: the caret selects all of it, arrows step past it,
// backspace and delete take it in two presses, enter opens a line after it.
//
//   const ta = mdArea('div.input.ed-ta', {lines, enter(v, a, b) → edit | null, type(v, a, b, ch) → edit | null,
//                                         paste(v, a, b, text, html) → edit | null, placeholder, rows})
//   ta.edit({from, to, text, a, b})   replace [from, to) with text, select [a, b), one undo step
import { h } from './dom.js';
import { isMod } from './keymap.js';

const BLOCK = /^(DIV|P)$/;

// The text of el: a block element (a line) starts a line, a <br> is a newline
// unless it is the last thing in its line (it only holds an empty line open).
// visit('t' | 'l', node, at) sees each text node and line with where it starts;
// point [container, index]: answers that DOM point's offset as .at (-1: not met).
function scan(el, visit, point) {
  let s = '', started = false, brk = false, found = -1;
  const nl = () => { s += '\n'; brk = false; };
  const go = n => {
    let i = 0;
    for (let c = n.firstChild; c; c = c.nextSibling, i++) {
      if (point && found < 0 && point[0] === n && point[1] === i) found = s.length + (started && (brk || BLOCK.test(c.nodeName)) ? 1 : 0);
      if (c.nodeType === 3) {
        if (!c.data) continue;
        if (brk) nl();
        started = true;
        if (point && found < 0 && point[0] === c) found = s.length + Math.min(point[1], c.data.length);
        if (visit) visit('t', c, s.length);
        s += c.data;
      } else if (c.nodeName === 'BR') {
        if (c.nextSibling) { if (brk) nl(); started = true; s += '\n'; }
      } else if (c.nodeType === 1) {
        if (BLOCK.test(c.nodeName)) {
          if (started) nl();
          started = true;
          if (visit) visit('l', c, s.length);
          go(c);
          brk = true;
        } else go(c);
      }
    }
    if (point && found < 0 && point[0] === n && point[1] >= i) found = s.length;
  };
  go(el);
  return { text: s, at: found };
}

export function mdArea(sel, o) {
  const el = h(sel, { role: 'textbox', 'aria-multiline': 'true', spellcheck: 'true', 'data-placeholder': o.placeholder || '' });
  el.contentEditable = 'plaintext-only';
  if (el.contentEditable !== 'plaintext-only') el.contentEditable = 'true';
  const editable = el.contentEditable;
  if (o.rows) el.style.minHeight = 'calc(' + o.rows + ' * 1.45em + .714rem + 2px)';

  let text = '', composing = false, info = [];
  // clean: the DOM is one line div per line as last drawn (not since touched by
  // the browser), so a point maps through its line alone.
  let clean = false, starts = null;
  const lineStarts = () => {
    if (!starts) { starts = [0]; for (let j = text.indexOf('\n'); j >= 0; j = text.indexOf('\n', j + 1)) starts.push(j + 1); }
    return starts;
  };
  const lineOf = i => { const st = lineStarts(); let lo = 0, hi = st.length - 1; while (lo < hi) { const m = (lo + hi + 1) >> 1; if (st[m] <= i) lo = m; else hi = m - 1; } return lo; };
  const top = n => { while (n && n.parentNode !== el) n = n.parentNode; return n; };

  // ---- selection as offsets into the text
  const offset = (node, off) => {
    if (clean && el.children.length === lineStarts().length) {
      const st = lineStarts();
      if (node === el) return off < st.length ? st[off] : text.length;
      const d = top(node), i = d ? Array.prototype.indexOf.call(el.children, d) : -1;
      if (i >= 0) { const r = scan(d, null, [node, off]).at; if (r >= 0) return st[i] + r; }
    }
    const r = scan(el, null, [node, off]).at;
    return r < 0 ? text.length : r;
  };
  const getSel = () => {
    const s = window.getSelection();
    if (!s.rangeCount || !el.contains(s.anchorNode)) return [text.length, text.length];
    const r = s.getRangeAt(0);
    return [offset(r.startContainer, r.startOffset), offset(r.endContainer, r.endOffset)];
  };
  // The node and offset for i: a text node holding it (at a node boundary the
  // later one), else the empty line starting there.
  const point = i => {
    let hit = null, end = null, line = null, last = null, root = el, base = 0;
    if (clean && text && el.children.length === lineStarts().length) {
      const x = lineOf(i);
      root = el.children[x]; base = lineStarts()[x];
      if (!root.textContent) return [root, 0];
    }
    scan(root, (k, n, at) => {
      at += base;
      if (hit) return;
      if (k === 'l') { if (at === i && !line) line = n; return; }
      last = n;
      const e = at + n.data.length;
      if (i >= at && i < e) hit = [n, i - at];
      else if (i === e && !end) end = [n, n.data.length];
    });
    if (hit) return hit;
    if (end && !(line && line.textContent === '')) return end;
    if (line) return [line, 0];
    return last ? [last, last.data.length] : [el, el.childNodes.length];
  };
  // Only a focused field takes a selection; focus() places the caret.
  const setSel = (a, b) => {
    if (document.activeElement !== el) return;
    const r = document.createRange();
    r.setStart(...point(a)); r.setEnd(...point(b));
    const s = window.getSelection();
    s.removeAllRanges(); s.addRange(r);
  };

  // ---- atoms: [s, e) of each, line: it is a whole line
  const atoms = () => [...el.querySelectorAll(':scope.live .atom')].map(n => { const s = offset(n, 0); return { n, s, e: s + n.textContent.length, line: n.parentNode === el }; });
  // snap: a selection reaching into an atom takes all of it; a caret in one
  // (on an atom line: anywhere) selects it.
  const snap = (a, b) => {
    for (const t of atoms()) {
      if (a === b) { if (t.line ? a >= t.s && a <= t.e : a > t.s && a < t.e) return [t.s, t.e]; continue; }
      if (a > t.s && a < t.e) a = t.s;
      if (b > t.s && b < t.e) b = t.e;
    }
    return [a, b];
  };
  const atomAt = (a, b) => atoms().find(t => t.s === a && t.e === b);
  // gone: replacing [a, b) with t, a selected atom line goes with its newline.
  const gone = (a, b, t) => {
    const x = atomAt(a, b);
    if (!t && x && x.line) return b < text.length ? { from: a, to: b + 1, text: '', a, b: a } : a > 0 ? { from: a - 1, to: b, text: '', a: a - 1, b: a - 1 } : { from: a, to: b, text: '', a, b: a };
    return { from: a, to: b, text: t, a: a + t.length, b: a + t.length };
  };

  // ---- the lines the caret is on are .act, the atoms selected .on
  let act = [], on = [], held = false;
  const lineIndex = i => lineOf(i);
  const activate = (a, b) => {
    for (const d of act) d.classList.remove('act');
    for (const d of on) d.classList.remove('on');
    act = []; on = [];
    if (a == null || !info.length) return;
    if (a !== b) for (const t of atoms()) if (!t.line && t.s >= a && t.e <= b) { t.n.classList.add('on'); on.push(t.n); }
    let x = lineIndex(a), y = b === a ? x : lineIndex(b);
    if (info[x]) x = info[x].g;
    while (y + 1 < info.length && info[y] && info[y + 1].g === info[y].g) y++;
    for (let i = x; i <= y && i < el.children.length; i++) { const d = el.children[i]; d.classList.add('act'); act.push(d); }
  };
  const sync = () => {
    if (held || composing) return;
    if (document.activeElement !== el) return activate(null);
    const [a, b] = getSel(), [x, y] = snap(a, b);
    if (x !== a || y !== b) setSel(x, y);
    activate(x, y);
  };
  const onSel = () => sync();
  el.addEventListener('focus', () => { document.addEventListener('selectionchange', onSel); sync(); });
  el.addEventListener('blur', () => { document.removeEventListener('selectionchange', onSel); activate(null); });
  el.addEventListener('keyup', sync);
  // A drag selects over the text as it is drawn; the markdown shows when it ends.
  el.addEventListener('mousedown', e => {
    if (e.button) return;
    const n = e.target.closest && e.target.closest('.atom'), t = n && !e.shiftKey && atoms().find(t => t.n === n);
    if (t) { e.preventDefault(); el.focus({ preventScroll: true }); setSel(t.s, t.e); activate(t.s, t.e); return; }
    held = true;
    addEventListener('mouseup', () => { held = false; sync(); }, { once: true });
  });

  // ---- drawing: the lines whose HTML changed, or that the browser touched, are replaced
  const tpl = document.createElement('div');
  const render = (a, b) => {
    starts = null;
    info = text ? o.lines(text) : [];
    const raw = text ? text.split('\n') : [];
    const kids = [...el.childNodes], n = info.length;
    const ok = (d, i) => { const l = info[i]; return d.nodeType === 1 && d._h === l.h && d._c === l.c && d._s === l.s && d.textContent === raw[i] && (raw[i] || d.firstChild); };
    let p = 0, q = 0;
    while (p < n && p < kids.length && ok(kids[p], p)) p++;
    while (q < n - p && q < kids.length - p && ok(kids[kids.length - 1 - q], n - 1 - q)) q++;
    const after = q ? kids[kids.length - q] : null;
    for (let i = p; i < kids.length - q; i++) kids[i].remove();
    if (p < n - q) {
      tpl.innerHTML = info.slice(p, n - q).map(l => '<div class="ln' + (l.c ? ' ' + l.c : '') + '"' + (l.s ? ' style="' + l.s + '"' : '') + '>' + (l.h || '<br>') + '</div>').join('');
      const f = document.createDocumentFragment();
      [...tpl.childNodes].forEach((d, i) => { const l = info[p + i]; d._h = l.h; d._c = l.c; d._s = l.s; f.append(d); });
      el.insertBefore(f, after);
    }
    clean = true;
    if (a != null && document.activeElement === el) activate(a, b);
  };

  // ---- undo: one entry per change, typing within a second makes one step
  const hist = [{ text: '', a: 0, b: 0 }];
  let at = 0, last = 0;
  const record = (typing, a, b) => {
    const now = Date.now();
    if (typing && last && now - last < 1000 && at === hist.length - 1 && at > 0) hist[at] = { text, a, b };
    else { hist.splice(at + 1); hist.push({ text, a, b }); at = hist.length - 1; if (hist.length > 200) { hist.shift(); at--; } }
    last = typing ? now : 0;
  };
  const restore = i => {
    if (i < 0 || i >= hist.length) return;
    at = i; last = 0;
    const st = hist[i];
    text = st.text; render(st.a, st.b); setSel(st.a, st.b);
    el.dispatchEvent(new Event('input'));
  };

  const edit = e => {
    if (!e) return false;
    text = text.slice(0, e.from) + e.text + text.slice(e.to);
    render(e.a, e.b);
    setSel(e.a, e.b);
    record(false, e.a, e.b);
    el.dispatchEvent(new Event('input'));
    return true;
  };

  // The browser typed, deleted or dropped: read it back and redraw.
  el.addEventListener('input', e => {
    if (!e.isTrusted) return;
    clean = false;
    if (composing) return;
    const [a, b] = getSel();
    text = scan(el).text;
    render(a, b); setSel(a, b);
    record(e.inputType === 'insertText' || e.inputType === 'deleteContentBackward' || e.inputType === 'deleteContentForward', a, b);
  });
  el.addEventListener('compositionstart', () => { composing = true; clean = false; });
  el.addEventListener('compositionend', () => {
    composing = false;
    const [a, b] = getSel();
    text = scan(el).text;
    render(a, b); setSel(a, b); record(true, a, b);
    el.dispatchEvent(new Event('input'));
  });
  // A newline is ours, so no <div> or <br> appears; a key handler that takes
  // enter first (a popup) prevents it before it gets here. shift+enter is a plain one.
  let shift = false;
  el.addEventListener('beforeinput', e => {
    const t = e.inputType;
    if (t === 'historyUndo' || t === 'historyRedo') { e.preventDefault(); restore(at + (t === 'historyUndo' ? -1 : 1)); return; }
    // Over an atom the browser would keep its hidden text: the edit is ours.
    if (!composing && (t === 'insertText' || t.startsWith('delete'))) {
      const [a, b] = getSel();
      if (a !== b && atoms().some(x => x.s < b && x.e > a)) { e.preventDefault(); edit(gone(a, b, t === 'insertText' ? e.data || '' : '')); return; }
    }
    if (t === 'insertText' && o.type && !composing && e.data && e.data.length === 1) {
      const [a, b] = getSel(), ed = a !== b && o.type(text, a, b, e.data);
      if (ed) { e.preventDefault(); edit(ed); }
      return;
    }
    if (t !== 'insertParagraph' && t !== 'insertLineBreak') return;
    e.preventDefault();
    let [a, b] = getSel();
    if (atomAt(a, b)) a = b;
    edit((!shift && o.enter && o.enter(text, a, b)) || { from: a, to: b, text: '\n', a: a + 1, b: a + 1 });
  });
  // A key on or next to an atom: arrows step off a selected one, backspace
  // and delete select the one they would eat into. → whether it took the key.
  const atomKey = k => {
    if (!/^(Arrow(Left|Right|Up|Down)|Backspace|Delete)$/.test(k)) return false;
    const [a, b] = getSel(), t = atomAt(a, b);
    // Up and down onto an atom line (the browser skips a line with no text drawn).
    if (!t && a === b && (k === 'ArrowUp' || k === 'ArrowDown')) {
      const s = lineStarts()[lineOf(a)], e = text.indexOf('\n', a);
      const x = atoms().find(x => x.line && (k === 'ArrowUp' ? x.e + 1 === s : e >= 0 && x.s === e + 1));
      if (!x) return false;
      setSel(x.s, x.e); activate(x.s, x.e);
      return true;
    }
    if (t) {
      if (k === 'Backspace' || k === 'Delete' || (!t.line && (k === 'ArrowUp' || k === 'ArrowDown'))) return false;
      const back = k === 'ArrowLeft' || k === 'ArrowUp';
      const i = back ? (t.line ? Math.max(0, t.s - 1) : t.s) : (t.line ? Math.min(text.length, t.e + 1) : t.e);
      setSel(i, i); sync();
      return true;
    }
    if (a !== b || (k !== 'Backspace' && k !== 'Delete')) return false;
    // An empty line next to an atom line goes as usual, the caret then on the atom.
    const empty = (a === 0 || text[a - 1] === '\n') && (a === text.length || text[a] === '\n');
    const hit = atoms().find(t => k === 'Backspace' ? (t.line ? !empty && t.e + 1 === a : t.e === a) : (t.line ? !empty && t.s - 1 === a : t.s === a));
    if (!hit) return false;
    setSel(hit.s, hit.e); activate(hit.s, hit.e);
    return true;
  };
  let plain = false;
  el.addEventListener('keydown', e => {
    shift = e.shiftKey;
    plain = isMod(e) && e.shiftKey && e.key.toLowerCase() === 'v';
    if (!(e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) && !composing && atomKey(e.key)) { e.preventDefault(); return; }
    if (!isMod(e) || e.altKey) return;
    const k = e.key.toLowerCase();
    if (k === 'z' || k === 'y') { e.preventDefault(); restore(at + (k === 'z' && !e.shiftKey ? -1 : 1)); }
  });
  // Pasted text goes in as text (o.paste may make markdown of its HTML; ctrl+shift+v
  // keeps it plain); files are left to the field's owner.
  el.addEventListener('paste', e => {
    const d = e.clipboardData;
    if (!d || d.files.length) return;
    e.preventDefault();
    const t = d.getData('text/plain').replace(/\r\n?/g, '\n'), html = plain ? '' : d.getData('text/html');
    plain = false;
    if (!t && !html) return;
    const [a, b] = getSel();
    edit((o.paste && o.paste(text, a, b, t, html)) || { from: a, to: b, text: t, a: a + t.length, b: a + t.length });
  });
  const copy = (e, cut) => {
    const [a, b] = getSel();
    if (a === b || !e.clipboardData) return;
    e.preventDefault();
    e.clipboardData.setData('text/plain', text.slice(a, b));
    if (cut && !el.readOnly) edit({ from: a, to: b, text: '', a, b: a });
  };
  el.addEventListener('copy', e => copy(e, false));
  el.addEventListener('cut', e => copy(e, true));

  // ---- a textarea's API
  Object.defineProperties(el, {
    value: {
      get: () => text,
      set: v => { v = String(v == null ? '' : v); if (v === text) return; text = v; render(text.length, text.length); setSel(text.length, text.length); record(false, text.length, text.length); },
    },
    selectionStart: { get: () => getSel()[0] },
    selectionEnd: { get: () => getSel()[1] },
    readOnly: { get: () => el.contentEditable === 'false', set: v => { el.contentEditable = v ? 'false' : editable; } },
  });
  el.setSelectionRange = (a, b = a) => setSel(a, b);
  el.setRangeText = (t, a, b, mode) => {
    const [s0, s1] = getSel(), d = t.length - (b - a);
    const map = i => (i <= a ? i : i >= b ? i + d : a + t.length);
    const [n0, n1] = mode === 'end' ? [a + t.length, a + t.length] : mode === 'select' ? [a, a + t.length] : [map(s0), map(s1)];
    text = text.slice(0, a) + t + text.slice(b);
    render(n0, n1); setSel(n0, n1); record(false, n0, n1);
  };
  el.edit = edit;
  el.rerender = () => { const [a, b] = getSel(); render(a, b); if (document.activeElement === el) setSel(a, b); };
  el.offsetOf = node => offset(node, 0);
  // The caret's box on screen (a popup opens there).
  el.caretRect = () => {
    const s = window.getSelection();
    if (!s.rangeCount || !el.contains(s.anchorNode)) return el.getBoundingClientRect();
    const r = s.getRangeAt(0).cloneRange();
    r.collapse(false);
    const box = r.getClientRects()[0] || r.getBoundingClientRect();
    if (box && (box.width || box.height)) return box;
    const n = r.startContainer.nodeType === 1 ? r.startContainer : r.startContainer.parentElement;
    return n.getBoundingClientRect();
  };

  el.value = o.value || '';
  hist.length = 0; hist.push({ text, a: text.length, b: text.length }); at = 0;
  return el;
}
