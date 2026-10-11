// pageDiff(from, to): two versions of a page's markdown as the blocks of the newer one, each the same, added,
// removed (the older one's, in its place) or changed: a changed one is its markdown with the words that went and
// came between markers (INS/DEL … END), for the view to render and mark. churn(from, to) counts the words.
export const INS = '', DEL = '', END_INS = '', END_DEL = '';
export const MARKS = /[-]/g;

// The top-level blocks of markdown: runs of lines between blank ones, a code fence whole.
export function splitBlocks(md) {
  const out = [];
  let cur = [], fence = '';
  const flush = () => { if (cur.length) out.push(cur.join('\n')); cur = []; };
  for (const line of String(md || '').replace(/\r\n?/g, '\n').split('\n')) {
    const f = line.match(/^\s*(`{3,}|~{3,})/);
    if (fence) { cur.push(line); if (f && f[1][0] === fence[0] && f[1].length >= fence.length) fence = ''; continue; }
    if (f) { fence = f[1]; cur.push(line); continue; }
    if (!line.trim()) flush(); else cur.push(line);
  }
  flush();
  return out;
}

// seqDiff(a, b): the steps from a to b, ['=', i, j], ['-', i] or ['+', j], in order: their longest common
// subsequence apart, the common head and tail first. Past cap cells, the middle is all removed, then all added.
export function seqDiff(a, b, cap = 4e6) {
  let s = 0, ea = a.length, eb = b.length;
  while (s < ea && s < eb && a[s] === b[s]) s++;
  while (ea > s && eb > s && a[ea - 1] === b[eb - 1]) { ea--; eb--; }
  const out = [];
  for (let k = 0; k < s; k++) out.push(['=', k, k]);
  const n = ea - s, m = eb - s;
  if (n * m > cap) {
    for (let i = s; i < ea; i++) out.push(['-', i]);
    for (let j = s; j < eb; j++) out.push(['+', j]);
  } else {
    // lcs[i][j] is the common run of a[s+i:ea] and b[s+j:eb].
    const lcs = Array.from({ length: n + 1 }, () => new Int32Array(m + 1));
    for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) lcs[i][j] = a[s + i] === b[s + j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    let i = 0, j = 0;
    while (i < n || j < m) {
      if (i < n && j < m && a[s + i] === b[s + j]) { out.push(['=', s + i, s + j]); i++; j++; }
      else if (i < n && (j === m || lcs[i + 1][j] >= lcs[i][j + 1])) out.push(['-', s + i++]);
      else out.push(['+', s + j++]);
    }
  }
  for (let k = 0; k < a.length - ea; k++) out.push(['=', ea + k, eb + k]);
  return out;
}

// Words, the spaces between them and single other characters.
export const tokens = s => s.match(/\s+|[\p{L}\p{N}_'’-]+|[^\s\p{L}\p{N}_'’-]/gu) || [];
const isWord = t => /[\p{L}\p{N}]/u.test(t);
const wordCount = s => tokens(s).filter(isWord).length;

// How alike two blocks are: the share of their words in common.
function likeness(a, b) {
  const ta = tokens(a), tb = tokens(b);
  if (!ta.length || !tb.length) return 0;
  let same = 0;
  for (const st of seqDiff(ta, tb, 1e5)) if (st[0] === '=' && isWord(ta[st[1]])) same++;
  return same * 2 / (ta.filter(isWord).length + tb.filter(isWord).length || 1);
}
const ALIKE = 0.4;

// Markup a word-level change can't be marked inside: a block where these went or came is shown removed and added.
const SYNTAX = /[`*_[\]()|~<>#!]/;
// A line's own markup (list item, heading, quote, table row) stays before the marks, or it stops being one.
const LEAD = /^([-]+)((?:\s*(?:[-*+]\s+(?:\[[ xX]\]\s+)?|\d+[.)]\s+|#{1,6}\s+|>\s?|\|\s?))*)/gm;

// wordDiff(a, b): b's markdown with what changed from a marked, and the words that went and came; null when the
// change touches markup.
export function wordDiff(a, b) {
  const ta = tokens(a), tb = tokens(b);
  const steps = seqDiff(ta, tb);
  // One space alike between two changes joins them: "old words" → "new text" reads as one change, not four.
  for (let k = 1; k < steps.length - 1; k++) {
    if (steps[k][0] === '=' && !ta[steps[k][1]].trim() && steps[k - 1][0] !== '=' && steps[k + 1][0] !== '=') steps.splice(k, 1, ['-', steps[k][1]], ['+', steps[k][2]]);
  }
  let out = '', add = 0, del = 0;
  for (let k = 0; k < steps.length;) {
    if (steps[k][0] === '=') { out += tb[steps[k++][2]]; continue; }
    let went = '', came = '';
    for (; k < steps.length && steps[k][0] !== '='; k++) {
      const t = steps[k][0] === '-' ? ta[steps[k][1]] : tb[steps[k][1]];
      if (SYNTAX.test(t)) return null;
      if (steps[k][0] === '-') { went += t; if (isWord(t)) del++; } else { came += t; if (isWord(t)) add++; }
    }
    // Only the spacing changed: the new spacing, unmarked.
    if (!went.trim() && !came.trim()) { out += came; continue; }
    if (went.trim()) out += DEL + went + END_DEL;
    if (came.trim()) out += INS + came + END_INS; else out += came;
  }
  return { md: out.replace(LEAD, '$2$1'), add, del };
}

// A table: every line a row.
const isTable = s => s.split('\n').every(l => /^\s*\|/.test(l));
const cells = line => line.split('|').length;
// Every cell of a row marked m … e.
const markRow = (line, m, e) => line.replace(/\|([^|]+)(?=\|)/g, (x, c) => (c.trim() ? '| ' + m + c.trim() + e + ' ' : x));

// tableDiff(a, b): table b with its rows that went (back in their place), came or changed marked; null when its
// columns changed.
function tableDiff(a, b) {
  const ra = a.split('\n'), rb = b.split('\n');
  if (ra.length < 2 || rb.length < 2 || cells(ra[0]) !== cells(rb[0]) || ra[1] !== rb[1]) return null;
  const out = [];
  let add = 0, del = 0;
  const steps = seqDiff(ra, rb);
  for (let k = 0; k < steps.length;) {
    if (steps[k][0] === '=') { out.push(rb[steps[k++][2]]); continue; }
    const D = [], A = [];
    for (; k < steps.length && steps[k][0] !== '='; k++) (steps[k][0] === '-' ? D : A).push(steps[k][0] === '-' ? ra[steps[k][1]] : rb[steps[k][1]]);
    for (let i = 0; i < Math.max(D.length, A.length); i++) {
      const w = i < D.length && i < A.length && cells(D[i]) === cells(A[i]) ? wordDiff(D[i], A[i]) : null;
      if (w) { out.push(w.md); add += w.add; del += w.del; continue; }
      if (i < D.length) { out.push(markRow(D[i], DEL, END_DEL)); del += wordCount(D[i]); }
      if (i < A.length) { out.push(markRow(A[i], INS, END_INS)); add += wordCount(A[i]); }
    }
  }
  return { md: out.join('\n'), add, del };
}

// pageDiff(from, to) → [{kind: 'same'|'add'|'del'|'chg', md}], the newer page's blocks with the older one's removed
// ones in their place, and the words added and removed.
export function pageDiff(from, to) {
  const a = splitBlocks(from), b = splitBlocks(to);
  const out = [];
  let add = 0, del = 0;
  const added = md => { out.push({ kind: 'add', md }); add += wordCount(md); };
  const removed = md => { out.push({ kind: 'del', md }); del += wordCount(md); };
  const steps = seqDiff(a, b);
  for (let k = 0; k < steps.length;) {
    if (steps[k][0] === '=') { out.push({ kind: 'same', md: b[steps[k][2]] }); k++; continue; }
    // A run of changes: a removed block like an added one is that block changed.
    const D = [], A = [];
    for (; k < steps.length && steps[k][0] !== '='; k++) (steps[k][0] === '-' ? D : A).push(steps[k][0] === '-' ? a[steps[k][1]] : b[steps[k][1]]);
    let i = 0, j = 0;
    while (i < D.length && j < A.length) {
      if (likeness(D[i], A[j]) >= ALIKE) {
        const w = isTable(D[i]) && isTable(A[j]) ? tableDiff(D[i], A[j]) : wordDiff(D[i], A[j]);
        if (w) { out.push({ kind: 'chg', md: w.md }); add += w.add; del += w.del; } else { removed(D[i]); added(A[j]); }
        i++; j++;
      } else if (j + 1 < A.length && likeness(D[i], A[j + 1]) >= ALIKE) added(A[j++]);
      else removed(D[i++]);
    }
    while (i < D.length) removed(D[i++]);
    while (j < A.length) added(A[j++]);
  }
  return { blocks: out, add, del };
}

// churn(from, to): the words added and removed, for the history's bars.
export function churn(from, to) {
  const { add, del } = pageDiff(from, to);
  return { add, del };
}

// The title's change, marked the same way; null when it stayed.
export function titleDiff(from, to) {
  if (from === to) return null;
  const ta = tokens(from), tb = tokens(to);
  return seqDiff(ta, tb).map(st => (st[0] === '+' ? ['+', tb[st[1]]] : st[0] === '-' ? ['-', ta[st[1]]] : ['=', tb[st[2]]]));
}
