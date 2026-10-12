// Markdown as it is typed: lines(text) answers one {c, h, s, g} per source line,
// its block classes, its HTML (the line wrapped in spans, markers .mk, content by
// role, never a character changed), an inline style and the line its group (a
// table, a code block) starts at, so the editor draws it as rich text and shows
// the markdown only where the caret is. The editing helpers answer what enter,
// tab, a task box click, a pasted link or a moved line do to the text
// ({from, to, text, a, b}: replace [from, to) with text, then select [a, b)).
// Pure: no DOM, tested with node (internal/web/jstest).
import { T } from './i18n.js';

const esc = s => s.replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]);
const span = (cls, inner) => '<span class="' + cls + '">' + inner + '</span>';
const mk = s => span('mk', esc(s));
const word = c => c != null && /[\p{L}\p{N}_]/u.test(c);
const SAFE = /^(https?:|mailto:)/i;
const href = u => (SAFE.test(u) ? ' data-href="' + esc(u) + '" title="' + esc(u) + '"' : '');

const STATUS = new Set(['neutral', 'purple', 'blue', 'red', 'yellow', 'green']);

// An image: its source hides behind the picture once there is one to show
// (o.img(url) → src; none: the source as text).
function image(m, o) {
  const src = o.img ? o.img(m[4]) : '';
  const source = mk(m[1]) + span('hl-alt', esc(m[2])) + mk(m[3]) + span('mk hl-url', esc(m[4])) + mk(m[5]);
  if (!src) return source;
  return span('hl-imgsrc', source) + '<img class="hl-img" src="' + esc(src) + '" alt="" contenteditable="false" draggable="false" loading="lazy">';
}

// Inline rules, tried in order where a character could start one. pre: what the
// character before must not be.
const INLINE = [
  { re: /\\[\\`*_{}[\]()#+\-.!~|<>@:]/y, out: m => mk('\\') + esc(m[0][1]) },
  { re: /(`+)([^`\n]+?)\1(?!`)/y, out: m => span('hl-code', mk(m[1]) + esc(m[2]) + mk(m[1])) },
  { re: /(!\[)([^\]\n]*)(\]\()([^)\s]*)((?: "[^"\n]*")?\))/y, out: image },
  { re: /(\[)([^\]\n]*)(\]\()([^)\s]*)((?: "[^"\n]*")?\))/y, out: (m, o) => mk(m[1]) + '<span class="hl-lt"' + href(m[4]) + '>' + inline(m[2], o) + '</span>' + mk(m[3]) + span('mk hl-url', esc(m[4])) + mk(m[5]) },
  { re: /\*\*\*(?=\S)([^\n]*?\S)\*\*\*/y, out: (m, o) => mk('***') + span('hl-b hl-i', inline(m[1], o)) + mk('***') },
  { re: /\*\*(?=\S)([^\n]*?\S)\*\*/y, out: (m, o) => mk('**') + span('hl-b', inline(m[1], o)) + mk('**') },
  { re: /__(?=\S)([^\n]*?\S)__(?![\p{L}\p{N}_])/uy, pre: word, out: (m, o) => mk('__') + span('hl-b', inline(m[1], o)) + mk('__') },
  { re: /\*(?=[^\s*])([^\n]*?[^\s*])\*(?!\*)/y, out: (m, o) => mk('*') + span('hl-i', inline(m[1], o)) + mk('*') },
  { re: /_(?=[^\s_])([^\n]*?[^\s_])_(?![\p{L}\p{N}_])/uy, pre: word, out: (m, o) => mk('_') + span('hl-i', inline(m[1], o)) + mk('_') },
  { re: /~~(?=\S)([^\n]*?\S)~~/y, out: (m, o) => mk('~~') + span('hl-s', inline(m[1], o)) + mk('~~') },
  { re: /<u>([^\n]*?)<\/u>/y, out: (m, o) => mk('<u>') + span('hl-u', inline(m[1], o)) + mk('</u>') },
  { re: /<(sub|sup)>([^\n]*?)<\/\1>/y, out: (m, o) => mk('<' + m[1] + '>') + span('hl-' + m[1], inline(m[2], o)) + mk('</' + m[1] + '>') },
  { re: /<status color="([a-z]+)">([^<\n]*)<\/status>/y, out: m => mk(m[0].slice(0, m[0].indexOf('>') + 1)) + span('hl-status' + (STATUS.has(m[1]) ? ' c-' + m[1] : ''), esc(m[2])) + mk('</status>') },
  { re: /<date>([^<\n]*)<\/date>/y, out: m => mk('<date>') + span('hl-date', esc(m[1])) + mk('</date>') },
  { re: /<span style="color:\s*(#[0-9a-fA-F]{3,8})">([^\n]*?)<\/span>/y, out: (m, o) => mk(m[0].slice(0, m[0].indexOf('>') + 1)) + '<span class="md-col" style="--c:' + m[1] + '">' + inline(m[2], o) + '</span>' + mk('</span>') },
  { re: /⟦(\d+ ?)([^⟦⟧\n]*)⟧/y, out: m => span('atom', mk('⟦' + m[1]) + span('hl-kept', esc(m[2])) + mk('⟧')) },
  { re: /<!-- bg:(#[0-9a-fA-F]{3,8}) -->/y, out: m => '<span class="hl-sw" style="--c:' + m[1] + '">' + mk(m[0]) + '</span>' },
  { re: /<!-- th -->/y, out: m => span('mk hl-thm', esc(m[0])) },
  { re: /<!--[^\n]*?-->/y, out: m => span(/^<!-- ?keep:/.test(m[0]) ? 'hl-keep' : 'hl-tag', esc(m[0])) },
  { re: /https?:\/\/[^\s<>()]*[^\s<>().,;:!?'"\]]/y, pre: c => word(c) || c === '/', out: m => '<span class="hl-url hl-bare"' + href(m[0]) + '>' + esc(m[0]) + '</span>' },
  { re: /:([a-z0-9_+-]{2,}):/y, pre: word, out: (m, o) => { const g = o.emoji && o.emoji(m[1]); return g ? '<span class="hl-emo" data-g="' + esc(g) + '">' + mk(m[0]) + '</span>' : span('hl-emo', esc(m[0])); } },
  { re: /[A-Z][A-Z0-9_]+-\d+(?![\p{L}\p{N}_-])/uy, pre: c => word(c) || c === '-', out: m => '<span class="hl-key" data-key="' + m[0] + '">' + m[0] + '</span>' },
];
const STARTS = /[\\`!*[_~<h:@A-Z⟦]/;

function inline(s, o) {
  let out = '', plain = 0;
  for (let i = 0; i < s.length;) {
    const c = s[i];
    let hit = null;
    if (STARTS.test(c)) {
      if (c === '@' && o.names) {
        const n = o.names.find(n => s.startsWith(n, i + 1) && !word(s[i + 1 + n.length]));
        if (n && !word(s[i - 1])) hit = { len: n.length + 1, html: span('hl-at', esc('@' + n)) };
      }
      for (const r of INLINE) {
        if (hit) break;
        if (r.pre && r.pre(s[i - 1])) continue;
        r.re.lastIndex = i;
        const m = r.re.exec(s);
        if (m) hit = { len: m[0].length, html: r.out(m, o) };
      }
    }
    if (!hit) { i++; continue; }
    out += esc(s.slice(plain, i)) + hit.html;
    i += hit.len; plain = i;
  }
  return out + esc(s.slice(plain));
}

const FENCE = /^ {0,3}(`{3,}|~{3,})/;
const LIST = /^(\s*)(?:([-*+])|(\d{1,9})([.)]))( +)(\[[ xX]\] )?/;
const QUOTE = /^\s*(?:> ?)+/;
const DELIM = /^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)*\|?\s*$/;
const PANEL = /^(\s*<!-- panel:)([a-z]+)( -->\s*)$/;
const EXPAND = /^(\s*<!-- expand(?::\s*)?)(.*?)( -->\s*)$/;
const CLOSE = /^(\s*<!-- )(\/(?:panel|expand|block))( -->\s*)$/;
// Placeholders desc.go writes for what markdown can't hold: a kept block, a
// container edited as its content, a kept table's layout, a link card.
const KEEP = /^(\s*<!-- ?keep:\d+ )(.*?)((?:: move or delete this line)? -->\s*)$/;
const BLOCK = /^(\s*<!-- block:\d+ )(.*?)( -->\s*)$/;
const SHELL = /^(\s*<!-- table:\d+ )(.*?)( -->\s*)$/;
const CARD = /^(\s*<!-- card: )(.*?)( -->\s*)$/;
// KEPT names a kept block's ADF type as a reader would.
const KEPT = { mediaSingle: T('image'), mediaGroup: T('attachments'), media: T('attachment'), table: T('table'), blockCard: T('link card'), embedCard: T('embed'), extension: T('macro'), bodiedExtension: T('macro'), codeBlock: T('code block'), taskList: T('checklist'), decisionList: T('decisions'), nestedExpand: T('expand'), layoutSection: T('columns'), rule: T('divider') };
const keptLabel = n => { const [t, ...more] = n.split(' with '); return (KEPT[t] || t) + (more.length ? T(' with %s', more.join(' with ')) : ''); };
const isRow = l => /^\s*\|/.test(l) && (l.match(/(?<!\\)\|/g) || []).length > 1;
const isTable = (l, next) => isRow(l) || (l.includes('|') && DELIM.test(next || '') && next.includes('|'));
const cols = s => [...s].reduce((n, c) => (c === '\t' ? n + 4 - (n % 4) : n + 1), 0);

// A table row as cells (each its bar and content), so the editor can lay the
// rows out as a grid; th: a header row (bold), dl: the delimiter row.
function row(l, inl, th, dl) {
  const parts = l.split(/(?<!\\)\|/);
  const cell = (p, bar, last) => {
    const inner = dl ? mk(p) : th && p.trim() ? span('hl-th', inl(p)) : inl(p);
    return span('td' + (last && !p.trim() ? ' tde' : ''), (bar ? mk('|') : '') + inner);
  };
  let out = '', lead = '';
  if (parts[0].trim()) out += cell(parts[0], false, parts.length === 1);
  else lead = parts[0];
  for (let i = 1; i < parts.length; i++) out += (i === 1 && lead ? span('td-lead', esc(lead)) : '') + cell(parts[i], true, i === parts.length - 1);
  return out;
}

// lines(text, {names, emoji(name) → glyph, img(url) → src, cache}) → [{c, h, s, g}]: per
// line its classes, HTML, style and the line its group starts at.
export function lines(text, o = {}) {
  const src = text.split('\n');
  // o.cache (a Map the caller keeps while names, emoji and images stay the same):
  // a line's inline HTML once worked out, so typing redoes only the changed line.
  const inl = s => {
    if (!o.cache) return inline(s, o);
    let r = o.cache.get(s);
    if (r === undefined) { r = inline(s, o); if (o.cache.size > 5000) o.cache.clear(); o.cache.set(s, r); }
    return r;
  };
  const out = [];
  let fence = '', group = -1;
  const frames = []; // open panels, expands and blocks: {kind, type}
  const frame = () => {
    const p = [...frames].reverse().find(f => f.kind === 'panel');
    return (p ? ' pn pn-' + p.type : '') + (frames.some(f => f.kind !== 'panel') ? ' ex' : '');
  };
  for (let i = 0; i < src.length; i++) {
    const l = src[i];
    const push = (c, h, s, g) => out.push({ c: c + frame(), h, s: s || '', g: g == null ? i : g });
    const f = FENCE.exec(l);
    if (fence) {
      if (f && f[1][0] === fence[0] && f[1].length >= fence.length && !l.slice(f[0].length).trim()) { fence = ''; push('fc fx', mk(l), '', group); } else push('pre', l ? esc(l) : '', '', group);
      continue;
    }
    if (f) { fence = f[1]; group = i; push('fc fo', mk(f[0]) + span('hl-lang', esc(l.slice(f[0].length)))); continue; }
    let m = /^(#{1,6})( +)(.*)$/.exec(l);
    if (m) { push('h h' + m[1].length, mk(m[1] + m[2]) + inl(m[3])); continue; }
    if (/^ {0,3}([-*_])(?: *\1){2,} *$/.test(l)) { push('hr', span('hl-hr', esc(l))); continue; }
    if ((m = PANEL.exec(l))) { push('tag pno', mk(m[1]) + span('hl-pn', esc(m[2])) + mk(m[3])); frames.push({ kind: 'panel', type: m[2] }); out[out.length - 1].c += ' pn pn-' + m[2]; continue; }
    if ((m = EXPAND.exec(l)) && !l.includes('<!-- /')) { push('tag exo', mk(m[1]) + span('hl-exp', esc(m[2] || T('Details'))) + mk(m[3])); frames.push({ kind: 'expand' }); continue; }
    if ((m = CLOSE.exec(l))) {
      const kind = m[2].slice(1), at = frames.map(f => f.kind).lastIndexOf(kind);
      const c = frame();
      if (at >= 0) frames.splice(at, 1);
      out.push({ c: 'tag pnc' + c, h: mk(m[1]) + span('hl-end', esc(m[2])) + mk(m[3]), s: '', g: i });
      continue;
    }
    if ((m = BLOCK.exec(l))) { push('tag blo', mk(m[1]) + span('hl-blk', esc(m[2])) + mk(m[3])); frames.push({ kind: 'block' }); continue; }
    if ((m = KEEP.exec(l))) { push('keep atom', mk(m[1]) + '<span class="hl-kn" data-l="' + esc(keptLabel(m[2])) + '" title="' + esc(o.wiki ? T('Kept as it is in Confluence: move or delete this line') : T('Kept as it is in Jira: move or delete this line')) + '">' + mk(m[2]) + '</span>' + mk(m[3])); continue; }
    if ((m = SHELL.exec(l))) { push('tag shell', mk(m[1]) + span('hl-shell', esc(m[2])) + mk(m[3])); continue; }
    if ((m = CARD.exec(l))) { push('card atom', mk(m[1]) + '<span class="hl-lt hl-card"' + href(m[2]) + '>' + esc(m[2]) + '</span>' + mk(m[3])); continue; }
    if (/^\s*<!--.*-->\s*$/.test(l)) { push(/^\s*<!-- ?keep:/.test(l) ? 'keep' : 'tag', span(/^\s*<!-- ?keep:/.test(l) ? 'hl-keep' : 'hl-tag', esc(l))); continue; }
    if (isTable(l, src[i + 1])) {
      const first = i === 0 || !isTable(src[i - 1], l);
      if (first) group = i;
      const dl = DELIM.test(l) && !first;
      push('tr' + (dl ? ' dl' : first ? ' th' : ''), row(l, inl, first, dl), '', group);
      continue;
    }
    m = QUOTE.exec(l);
    if (m && m[0].includes('>')) { push('q', mk(m[0]) + inl(l.slice(m[0].length))); continue; }
    m = LIST.exec(l);
    if (m) {
      const ind = cols(m[1]), mark = (m[2] || m[3] + m[4]) + m[5], depth = Math.min(2, Math.floor(ind / 2)) % 3;
      const box = m[6] ? '<span class="hl-box' + (m[6][1] === ' ' ? '' : ' on') + '">' + esc(m[6].slice(0, 3)) + '</span> ' : '';
      const body = inl(l.slice(m[0].length));
      const lead = ind ? '<span class="ind" style="width:' + ind + 'ch">' + esc(m[1]) + '</span>' : '';
      const marker = '<span class="lm' + (m[2] ? ' bul d' + depth : ' num') + '" style="min-width:' + (mark.length) + 'ch">' + span('hl-li', esc(mark.trimEnd())) + mark.slice(mark.trimEnd().length) + '</span>';
      push('li' + (m[6] ? ' task' + (m[6][1] === ' ' ? '' : ' done') : ''), lead + marker + box + (m[6] && m[6][1] !== ' ' ? span('hl-done', body) : body), '--h:' + (ind + mark.length + (m[6] ? 4 : 0)) + 'ch');
      continue;
    }
    if (l.startsWith('<> ')) { push('li dec', span('hl-li', esc('<>')) + ' ' + inl(l.slice(3))); continue; }
    if ((m = /^\s*(!\[[^\]\n]*\]\([^)\s]*\))\s*$/.exec(l))) { push('imgl', inl(l)); continue; }
    push('', inl(l));
  }
  return out;
}

// highlight(text, opts) → HTML: the lines joined as the text is.
export const highlight = (text, o) => lines(text, o).map(l => l.h).join('\n');

// ---- editing

// listItem(line) → {indent, bullet, num, delim, task, body, width} or null; body: where
// the item's text starts, width: the marker's columns (its children's indent).
export function listItem(line) {
  const m = LIST.exec(line);
  if (!m) return null;
  return { indent: m[1], bullet: m[2] || '', num: m[3] ? +m[3] : 0, delim: m[4] || '', task: !!m[6], body: m[0].length, width: m[0].length - m[1].length - (m[6] ? m[6].length : 0) };
}
const nextMarker = it => it.indent + (it.bullet || it.num + 1 + it.delim) + ' ' + (it.task ? '[ ] ' : '');

const lineAt = (v, i) => { const s = v.lastIndexOf('\n', i - 1) + 1; let e = v.indexOf('\n', i); if (e < 0) e = v.length; return [s, e]; };

// inFence: is position i inside a fenced code block?
export function inFence(v, i) {
  let fence = '';
  for (const l of v.slice(0, v.lastIndexOf('\n', i - 1) + 1).split('\n')) {
    const f = FENCE.exec(l);
    if (!f) continue;
    if (!fence) fence = f[1];
    else if (f[1][0] === fence[0] && f[1].length >= fence.length && !l.slice(f[0].length).trim()) fence = '';
  }
  return !!fence;
}

// renumber: the numbered items after line s (at its indent, in its list) counted on from n.
// → [end of the run, its new text]
function renumber(v, s, it, n) {
  let at = v.indexOf('\n', s);
  if (at < 0) return null;
  const from = at + 1, out = [];
  let to = from;
  for (const l of v.slice(from).split('\n')) {
    const x = listItem(l);
    if (!x) { if (/^\S/.test(l) || !l.trim()) break; out.push(l); to += l.length + 1; continue; }
    if (x.indent.length < it.indent.length) break;
    if (x.indent.length === it.indent.length) {
      if (!x.num || x.delim !== it.delim) break;
      out.push(x.indent + n++ + x.delim + l.slice(x.indent.length + String(x.num).length + 1));
    } else out.push(l);
    to += l.length + 1;
  }
  if (!out.length) return null;
  return [from, Math.min(to - 1, v.length), out.join('\n')];
}

// enter: a list item opens the next one (an empty one ends the list; the numbers
// after it count on), a quote line continues the quote, a table row opens a row
// of as many cells (an empty one ends the table; under a header, past its
// delimiter row, or with one added when it has none), other lines keep their indent.
export function enter(v, a, b) {
  const [s, e] = lineAt(v, a);
  const line = v.slice(s, e);
  const nl = t => ({ from: a, to: b, text: t, a: a + t.length, b: a + t.length });
  const indent = /^[ \t]*/.exec(line)[0];
  if (inFence(v, s)) return nl('\n' + indent.slice(0, a - s));
  const it = listItem(line);
  if (it && a - s >= it.body) {
    if (a === b && !line.slice(it.body).trim()) return { from: s, to: e, text: '', a: s, b: s };
    const mark = nextMarker(it), ins = '\n' + mark;
    const r = it.num && b === e ? renumber(v, s, it, it.num + 2) : null;
    if (r) return { from: a, to: r[1], text: ins + v.slice(b, r[0]) + r[2], a: a + ins.length, b: a + ins.length };
    return nl(ins);
  }
  const q = QUOTE.exec(line);
  if (q && q[0].includes('>') && a - s >= q[0].length) {
    if (a === b && !line.slice(q[0].length).trim()) return { from: s, to: e, text: '', a: s, b: s };
    return nl('\n' + q[0]);
  }
  if (isRow(line) && a === b && a === e) {
    const n = line.split(/(?<!\\)\|/).length - 2;
    if (!line.replace(/[|\s]/g, '')) return { from: s, to: e, text: '', a: s, b: s };
    const t = '\n' + indent + '|' + '  |'.repeat(Math.max(1, n));
    const prev = s > 0 ? v.slice(...lineAt(v, s - 1)) : '';
    if (!isRow(prev) && !isDelim(prev)) {
      const [ns, ne] = e < v.length ? lineAt(v, e + 1) : [e, e];
      if (isDelim(v.slice(ns, ne))) return { from: ne, to: ne, text: t, a: ne + indent.length + 3, b: ne + indent.length + 3 };
      const d = '\n' + indent + '|' + ' --- |'.repeat(Math.max(1, n));
      return { from: a, to: a, text: d + t, a: a + d.length + indent.length + 3, b: a + d.length + indent.length + 3 };
    }
    return { from: a, to: a, text: t, a: a + indent.length + 3, b: a + indent.length + 3 };
  }
  return nl('\n' + indent.slice(0, a - s));
}

// backspace: right after a list marker a nested item moves out a level and a
// top one drops its marker; after a quote's or heading's marker that goes.
// null: an ordinary backspace.
export function backspace(v, a, b) {
  if (a !== b) return null;
  const [s, e] = lineAt(v, a), line = v.slice(s, e);
  if (inFence(v, s)) return null;
  const it = listItem(line);
  if (it && a - s === it.body) {
    if (it.indent) return indent(v, a, a, true);
    return { from: s, to: s + it.body, text: '', a: s, b: s };
  }
  const m = /^(#{1,6} +|\s*(?:> ?)+)/.exec(line);
  if (m && a - s === m[0].length && (m[0].includes('>') || m[0].startsWith('#'))) return { from: s, to: a, text: '', a: s, b: s };
  return null;
}

// tableTab: in a table row the next cell's text selected (back: the previous),
// on over the rows; past the last cell a new row. null when not in a row.
const cellsOf = (v, s, e) => {
  const bars = [], out = [];
  for (let i = s; i < e; i++) if (v[i] === '|' && v[i - 1] !== '\\') bars.push(i);
  for (let i = 0; i + 1 < bars.length; i++) {
    let x = bars[i] + 1, y = bars[i + 1];
    while (x < y && v[x] === ' ') x++;
    while (y > x && v[y - 1] === ' ') y--;
    out.push([x, y, bars[i] + 1, bars[i + 1]]);
  }
  return out;
};
const isDelim = l => DELIM.test(l) && l.includes('|');
export function tableTab(v, a, back) {
  const [s, e] = lineAt(v, a);
  if (!isRow(v.slice(s, e)) || inFence(v, s)) return null;
  const cells = cellsOf(v, s, e);
  const cur = cells.findIndex(c => a >= c[2] && a <= c[3]);
  const sel = c => ({ from: a, to: a, text: '', a: c[0], b: c[1] });
  const stay = { from: a, to: a, text: '', a, b: a };
  if (!back) {
    if (cur + 1 < cells.length) return sel(cells[cur + 1]);
    for (let i = e + 1; e < v.length && i <= v.length;) {
      const [x, y] = lineAt(v, i), l = v.slice(x, y);
      if (isDelim(l) && y < v.length) { i = y + 1; continue; }
      if (isRow(l) && !isDelim(l)) { const c = cellsOf(v, x, y); if (c.length) return sel(c[0]); }
      break;
    }
    return v.slice(s, e).replace(/[|\s]/g, '') ? enter(v, e, e) : stay;
  }
  if (cur > 0) return sel(cells[cur - 1]);
  for (let i = s - 1; i >= 0;) {
    const [x, y] = lineAt(v, i), l = v.slice(x, y);
    if (isDelim(l) && x > 0) { i = x - 1; continue; }
    if (isRow(l) && !isDelim(l)) { const c = cellsOf(v, x, y); if (c.length) return sel(c[c.length - 1]); }
    break;
  }
  return stay;
}

// tableArrow: the caret's offset one line up (dir -1) or down (1) from a in a
// table, in the same cell; out of the table at the same column. null outside one.
export function tableArrow(v, a, dir) {
  const [s, e] = lineAt(v, a), l = v.slice(s, e);
  if (!isRow(l) && !isDelim(l)) return null;
  if (dir < 0 ? s === 0 : e >= v.length) return null;
  const [ts, te] = dir < 0 ? lineAt(v, s - 1) : lineAt(v, e + 1), t = v.slice(ts, te);
  if (!isRow(t) && !isDelim(t)) return ts + Math.min(a - s, te - ts);
  const from = cellsOf(v, s, e), to = cellsOf(v, ts, te);
  const i = Math.max(0, from.findIndex(c => a >= c[2] && a <= c[3]));
  const c = to[Math.min(i, to.length - 1)];
  if (!c) return ts;
  return Math.min(c[0] + Math.max(0, a - (from[i] ? from[i][0] : a)), c[1]);
}

// moveLines: the lines of [a, b) swapped with the one above (dir -1) or below (1).
export function moveLines(v, a, b, dir) {
  const [s] = lineAt(v, a), [, e] = lineAt(v, b > a && v[b - 1] === '\n' ? b - 1 : b);
  if (dir < 0 ? s === 0 : e >= v.length) return null;
  const block = v.slice(s, e);
  if (dir < 0) {
    const [ps] = lineAt(v, s - 1), prev = v.slice(ps, s - 1);
    const d = -(prev.length + 1);
    return { from: ps, to: e, text: block + '\n' + prev, a: a + d, b: b + d };
  }
  const [, ne] = lineAt(v, e + 1), next = v.slice(e + 1, ne);
  const d = next.length + 1;
  return { from: s, to: ne, text: next + '\n' + block, a: a + d, b: b + d };
}

// wrapWith: a marker typed over selected text (one line) wraps it, the text kept
// selected: * _ ` as they are, ~ as ~~, [ as a link.
export function wrapWith(v, a, b, ch) {
  if (a === b || v.slice(a, b).includes('\n')) return null;
  const t = v.slice(a, b);
  if (ch === '[') {
    const out = '[' + t + '](url)';
    return { from: a, to: b, text: out, a: a + t.length + 3, b: a + t.length + 6 };
  }
  const m = { '*': '*', _: '_', '`': '`', '~': '~~' }[ch];
  if (!m) return null;
  return { from: a, to: b, text: m + t + m, a: a + m.length, b: b + m.length };
}

// indent (out: false) or outdent (out: true) the list items in [a, b): one level
// under the item above, or back to its parent's. null when the caret is not on
// a list item (tab then leaves the field).
export function indent(v, a, b, out) {
  const [s] = lineAt(v, a), [, e] = lineAt(v, b > a && v[b - 1] === '\n' ? b - 1 : b);
  const lines = v.slice(s, e).split('\n');
  if (!listItem(lines[0])) return null;
  const before = v.slice(0, s).split('\n').slice(0, -1);
  let first = 0, total = 0;
  lines.forEach((l, i) => {
    const it = listItem(l);
    if (!it) return;
    let to = it.indent;
    for (let j = before.length + i - 1; j >= 0; j--) {
      const pl = j < before.length ? before[j] : lines[j - before.length];
      const p = listItem(pl);
      if (!p) {
        if (/^\S/.test(pl)) break; // a paragraph ends the list
        continue;
      }
      if (out ? p.indent.length < it.indent.length : p.indent.length <= it.indent.length) {
        to = out ? p.indent : p.indent.length === it.indent.length ? it.indent + ' '.repeat(p.width) : it.indent;
        break;
      }
    }
    if (out && to === it.indent) to = '';
    const d = to.length - it.indent.length;
    lines[i] = to + l.slice(it.indent.length);
    if (i === 0) first = d;
    total += d;
  });
  const text = lines.join('\n');
  if (text === v.slice(s, e)) return { from: s, to: e, text, a, b };
  return a === b ? { from: s, to: e, text, a: Math.max(s, a + first), b: Math.max(s, a + first) } : { from: s, to: e, text, a: Math.max(s, a + first), b: b + total };
}

// toggleTask: the "[ ]" at i ticked, or unticked.
export function toggleTask(v, i) {
  if (!/^\[[ xX]\]$/.test(v.slice(i, i + 3))) return null;
  const t = v[i + 1] === ' ' ? 'x' : ' ';
  return { from: i + 1, to: i + 2, text: t, a: i + 3, b: i + 3 };
}

// pasteLink: a URL pasted over selected text links that text.
export function pasteLink(v, a, b, text) {
  const url = text.trim(), sel = v.slice(a, b);
  if (a === b || !/^https?:\/\/\S+$/.test(url) || sel.includes('\n') || /^https?:\/\//.test(sel)) return null;
  const t = '[' + sel + '](' + url + ')';
  return { from: a, to: b, text: t, a: a + t.length, b: a + t.length };
}
