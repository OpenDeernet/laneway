// A wiki page's history (h on a page), the board's time machine for a page: a strip of its versions, a bar each as
// tall as its change (words added up, removed down, a minor edit a dot), and the page as a version read, what changed
// since the one before marked on it. Click or drag the strip, ← → step; shift-click a second version compares from
// it. d shows the clean page, n N go to the next change (the marks beside the scrollbar show them all), esc back to
// now. It only looks; Restore saves the version shown as the newest.
import { h, clear, frame } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { render as md } from '../lib/md.js';
import { dateTime, shortDate } from '../lib/fmt.js';
import { T, Tn } from '../lib/i18n.js';
import { pageDiff, titleDiff, MARKS, INS, DEL, END_INS, END_DEL } from '../lib/pagediff.js';

const CONTEXT = 1;   // unchanged blocks shown each side of a change
const FOLD = 4;      // a longer unchanged run folds
const MARKED = /[\uE000-\uE003]/;

// openHistory(app, {main, page, rail, id, version, head, pageImage, close, restored}) → {step, diff, jump, at, dispose}.
// main is the page's scrolling pane, page the sheet in it, rail the strip of change marks beside it; head(title) the crumbs to show above.
export function openHistory(app, o) {
  const { api, ui } = app;
  let dead = false, vs = null, cur = -1, base = -1, clean = app.prefs.get('wikiHistClean', '') === '1';
  let churns = [], changes = [], at = -1;
  const diffs = new Map(); // "base:cur" → pageDiff
  const ro = new ResizeObserver(frame(() => paintRail()));

  const titleEl = h('h1');
  const restoreLabel = h('span');
  const restoreBtn = h('button.btn', { onclick: () => restore() }, icon('undo-2'), ' ', restoreLabel);
  const head = h('div.wk-head', titleEl, h('span.wk-acts', restoreBtn,
    h('button.btn.ghost', { onclick: () => o.close(), title: T('Back to now (esc)') }, icon('x'), ' ', T('Close history'))));
  const strip = h('div.wkh-strip', { role: 'slider', tabindex: 0, 'aria-label': T('Versions') });
  const info = h('div.wkh-info');
  const bar = h('div.wkh-bar', strip, info);
  const body = h('div.md.wk-body.wkh-body');
  clear(o.page).append(o.head(), head, bar, body);
  titleEl.textContent = T('History');
  info.append(icon('rotate-ccw-clock'), ' ', T('reading the page’s history…  ·  esc cancels'));
  restoreBtn.hidden = true;
  ro.observe(body);

  load();
  async function load() {
    try { vs = await api.get('/wiki/pages/' + o.id + '/versions', { fresh: true }); } catch (e) { if (!dead) { ui.errToast(e); o.close(); } return; }
    if (dead) return;
    if (!vs.length) { ui.toast(T('This page has no history')); o.close(); return; }
    const want = vs.findIndex(v => v.Number === o.version);
    cur = want >= 0 ? want : vs.length - 1;
    churns = vs.map(() => null);
    drawStrip(); show();
    // The bars grow in as their changes are counted, newest first: a long history doesn't hold the page.
    let last = performance.now();
    for (let i = vs.length - 1; i >= 0 && !dead; i--) {
      const d = diffOf(i - 1, i);
      churns[i] = { add: d.add, del: d.del };
      if (performance.now() - last > 24) { drawStrip(); await new Promise(r => setTimeout(r)); last = performance.now(); }
    }
    if (!dead) drawStrip();
  }
  const mdOf = i => (i < 0 ? '' : vs[i].Markdown || '');
  function diffOf(b, c) {
    const k = b + ':' + c;
    if (!diffs.has(k)) diffs.set(k, pageDiff(mdOf(b), mdOf(c)));
    return diffs.get(k);
  }

  // ---- the strip: a bar a version, words added up from the middle, removed down
  function drawStrip() {
    const max = Math.max(1, ...churns.map(c => (c ? Math.max(c.add, c.del) : 0)));
    // A comparison picked with shift: the versions it spans.
    const lo = base >= 0 ? base : cur, hi = cur;
    const height = n => (n ? Math.max(8, Math.round(Math.sqrt(n / max) * 100)) + '%' : '0');
    clear(strip).append(...vs.map((v, i) => {
      const c = churns[i];
      const small = c && !c.add && !c.del;
      return h('div.wkh-v' + (i === cur ? '.on' : '') + (i > lo && i < hi ? '.in' : '') + (base >= 0 && i === lo ? '.base' : '') + (v.Minor || small ? '.minor' : '') + (c ? '' : '.wait'),
        { dataset: { i }, title: tip(v, c) },
        h('div.wkh-up', h('div.wkh-a', { style: { height: c ? height(c.add) : '0' } })),
        h('div.wkh-dn', h('div.wkh-d', { style: { height: c ? height(c.del) : '0' } })));
    }));
    strip.setAttribute('aria-valuemin', vs[0].Number);
    strip.setAttribute('aria-valuemax', vs[vs.length - 1].Number);
    strip.setAttribute('aria-valuenow', vs[cur].Number);
    strip.setAttribute('aria-valuetext', T('version %d of %d', vs[cur].Number, vs[vs.length - 1].Number));
  }
  const tip = (v, c) => [T('v%d', v.Number), v.Author, dateTime(v.Created), v.Message && '“' + v.Message + '”', c && (c.add || c.del) ? '+' + c.add + ' −' + c.del : ''].filter(Boolean).join('  ·  ');

  // Click a version, drag along the strip; shift-click compares from the one clicked.
  const under = ev => {
    const kids = strip.children;
    if (!kids.length) return -1;
    const r0 = kids[0].getBoundingClientRect(), r1 = kids[kids.length - 1].getBoundingClientRect();
    const w = (r1.right - r0.left) / kids.length;
    return Math.max(0, Math.min(kids.length - 1, Math.floor((ev.clientX - r0.left) / w)));
  };
  // Dragging: the version under the pointer at the next frame.
  let want = -1, queued = 0;
  const scrub = i => { want = i; if (!queued) queued = requestAnimationFrame(() => { queued = 0; if (!dead && want >= 0 && want !== cur) go(want, { scroll: false }); }); };
  strip.addEventListener('pointerdown', ev => {
    if (!vs || ev.button !== 0) return;
    const i = under(ev);
    if (ev.shiftKey) { ev.preventDefault(); if (i !== cur) { base = i; if (base > cur) [base, cur] = [cur, base]; show(); } return; }
    strip.setPointerCapture(ev.pointerId);
    strip.classList.add('drag');
    go(i, { scroll: false });
    const move = e => scrub(under(e));
    const up = () => { strip.classList.remove('drag'); strip.removeEventListener('pointermove', move); strip.removeEventListener('pointerup', up); strip.removeEventListener('pointercancel', up); toFirstChange(); };
    strip.addEventListener('pointermove', move);
    strip.addEventListener('pointerup', up);
    strip.addEventListener('pointercancel', up);
  });

  function go(i, { scroll = true } = {}) {
    if (!vs) return;
    i = Math.max(0, Math.min(vs.length - 1, i));
    if (i === cur && base === -1) return;
    cur = i; base = -1;
    show();
    if (scroll) toFirstChange();
  }

  // ---- the version shown
  function show() {
    const v = vs[cur], b = base >= 0 ? base : cur - 1;
    const latest = cur === vs.length - 1;
    o.setVersion(latest ? null : v.Number);
    drawStrip();
    restoreBtn.hidden = latest;
    restoreLabel.textContent = T('Restore v%d', v.Number);
    restoreBtn.title = T('Save version %d as the newest', v.Number);
    // The title, a rename marked.
    const td = !clean && b >= 0 ? titleDiff(vs[b].Title, v.Title) : null;
    clear(titleEl).append(...(td ? td.map(([k, t]) => (k === '=' ? t : h(k === '+' ? 'ins' : 'del', t))) : [v.Title]));
    const d = b >= 0 ? diffOf(b, cur) : null;
    const from = b >= 0 ? vs[b] : null;
    clear(info).append(...[
      icon('rotate-ccw-clock'),
      ui.avatar(v.Author || '?', v.AvatarURL, 18),
      h('span.wkh-who', v.Author || v.AuthorID || ''),
      h('b', T('v%d of %d', v.Number, vs[vs.length - 1].Number)),
      h('span.dim', { title: dateTime(v.Created) }, shortDate(v.Created)),
      v.Message ? h('span.wkh-msg', '“' + v.Message + '”') : null,
      v.Minor ? h('span.chip', T('minor')) : null,
      base >= 0 ? h('button.chip.wkh-from', { title: T('Compare with the version before'), onclick: () => { base = -1; show(); } }, T('since v%d', from.Number), ' ', icon('x')) : null,
      h('span.spacer'),
      !clean && d && (d.add || d.del) ? h('span.wkh-count', h('span.wkh-plus', '+' + d.add), ' ', h('span.wkh-minus', '−' + d.del)) : null,
      h('span.wkh-seg', { role: 'group', 'aria-label': T('Show') },
        h('button.btn.sm' + (clean ? '' : '.on'), { onclick: () => setClean(false), 'aria-pressed': String(!clean), title: T('The changes marked (d)') }, T('Changes')),
        h('button.btn.sm' + (clean ? '.on' : ''), { onclick: () => setClean(true), 'aria-pressed': String(clean), title: T('The page as it read (d)') }, T('Page'))),
      clean ? null : h('span.wkh-nav',
        h('button.btn.ghost.sm', { onclick: () => jump(-1), title: T('Previous change (N)'), 'aria-label': T('Previous change') }, icon('chevron-up')),
        h('span.wkh-pos.dim', ''),
        h('button.btn.ghost.sm', { onclick: () => jump(1), title: T('Next change (n)'), 'aria-label': T('Next change') }, icon('chevron-down'))),
      h('span.dim.wkh-keys', T('← older  ·  → newer  ·  esc now'))].filter(Boolean));
    drawBody(d);
  }

  const image = { pageImage: o.pageImage };
  function drawBody(d) {
    clear(body);
    changes = []; at = -1;
    if (clean || !d) {
      if (!clean && cur === 0) body.append(h('p.faint.wkh-none', T('The first version: nothing before it to compare.')));
      body.append(mdOf(cur).trim() ? md(mdOf(cur), image) : h('p.faint', T('This version is empty.')));
    } else {
      const bs = d.blocks;
      const keep = bs.map(() => false);
      bs.forEach((x, i) => { if (x.kind !== 'same') for (let k = Math.max(0, i - CONTEXT); k <= Math.min(bs.length - 1, i + CONTEXT); k++) keep[k] = true; });
      for (let i = 0; i < bs.length;) {
        // A long unchanged run folds to a line that opens it.
        if (!keep[i]) {
          let j = i;
          while (j < bs.length && !keep[j]) j++;
          if (j - i >= FOLD) {
            const run = bs.slice(i, j);
            const fold = h('button.wkh-fold', { type: 'button', onclick: () => { fold.replaceWith(...run.map(x => md(x.md, image))); paintRail(); } },
              icon('chevron-down'), ' ', T('%d unchanged blocks', run.length));
            body.append(fold);
            i = j;
            continue;
          }
        }
        body.append(block(bs[i]));
        i++;
      }
      if (!bs.some(x => x.kind !== 'same')) body.prepend(h('p.faint.wkh-none', T('Nothing changed in the text.')));
    }
    changes = [...body.querySelectorAll('.wkh-add, .wkh-del, .wkh-chg')];
    for (const c of changes) c.classList.add('wkh-flash');
    paintPos();
    requestAnimationFrame(paintRail);
  }
  function block(x) {
    if (x.kind === 'same') return md(x.md, image);
    const el = h('div.wkh-b.wkh-' + x.kind, md(x.md, image));
    if (x.kind === 'chg') marks(el);
    return el;
  }
  // The pagediff marks in el's text as <ins> and <del>: a change may span elements (the words of a list).
  function marks(el) {
    for (const n of el.querySelectorAll('*')) for (const a of [...n.attributes]) if (MARKED.test(a.value)) n.setAttribute(a.name, a.value.replace(MARKS, ''));
    const walk = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    const texts = [];
    while (walk.nextNode()) texts.push(walk.currentNode);
    let open = '';
    for (const t of texts) {
      const s = t.nodeValue;
      if (!open && !MARKED.test(s)) continue;
      const parts = [];
      let buf = '';
      const flush = () => { if (buf) parts.push(open ? h(open, buf) : document.createTextNode(buf)); buf = ''; };
      for (const ch of s) {
        if (ch === INS || ch === DEL) { flush(); open = ch === INS ? 'ins' : 'del'; } else if (ch === END_INS || ch === END_DEL) { flush(); open = ''; } else buf += ch;
      }
      flush();
      t.replaceWith(...parts);
    }
  }

  // ---- changes: n N, and their marks beside the scrollbar
  function jump(dir) {
    if (!changes.length) { ui.toast(clean ? T('d shows the changes') : T('No changes here')); return; }
    at = at < 0 ? (dir > 0 ? 0 : changes.length - 1) : (at + dir + changes.length) % changes.length;
    const c = changes[at];
    c.scrollIntoView({ block: 'center', behavior: matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
    for (const x of changes) x.classList.remove('wkh-at');
    c.classList.add('wkh-at');
    paintPos();
  }
  function paintPos() {
    const p = info.querySelector('.wkh-pos');
    if (p) p.textContent = at >= 0 ? (at + 1) + '/' + changes.length : Tn(changes.length, '%d change', '%d changes', changes.length);
  }
  // The first change in view, unless one is already.
  function toFirstChange() {
    if (!changes.length) return;
    const box = o.main.getBoundingClientRect();
    if (changes.some(c => { const r = c.getBoundingClientRect(); return r.bottom > box.top && r.top < box.bottom; })) return;
    jump(1);
  }
  function paintRail() {
    if (dead) return;
    const total = o.main.scrollHeight || 1;
    const top0 = o.main.getBoundingClientRect().top - o.main.scrollTop;
    o.rail.hidden = !changes.length;
    clear(o.rail).append(...changes.map((c, i) => {
      const r = c.getBoundingClientRect();
      const kind = c.classList.contains('wkh-add') ? 'add' : c.classList.contains('wkh-del') ? 'del' : 'chg';
      return h('button.wkh-tick.' + kind, { type: 'button', tabindex: -1, 'aria-hidden': 'true', style: { top: ((r.top - top0) / total * 100) + '%', height: Math.max(0.4, r.height / total * 100) + '%' },
        onclick: () => { at = i - 1; jump(1); } });
    }));
  }

  function setClean(v) {
    clean = v;
    app.prefs.set('wikiHistClean', v ? '1' : '');
    show();
  }

  async function restore() {
    const v = vs[cur];
    if (!await ui.confirm({ title: T('Restore version %d?', v.Number), text: T('It becomes the newest version, v%d. The versions in between stay in the history.', vs[vs.length - 1].Number + 1), ok: T('Restore') })) return;
    try { await api.post('/wiki/pages/' + o.id + '/restore', { Version: v.Number }); } catch (e) { return ui.errToast(e); }
    ui.toast(T('Restored version %d', v.Number));
    o.restored();
  }

  return {
    step: d => { if (vs) go(cur + d); },
    diff: () => { if (vs) setClean(!clean); },
    jump: d => { if (vs) jump(d); },
    dispose() { dead = true; ro.disconnect(); clear(o.rail); o.rail.hidden = true; },
  };
}
