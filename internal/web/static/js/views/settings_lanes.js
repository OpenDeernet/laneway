// The lane layout editor on the settings page: ui.lane_layouts drawn over a board's real columns. Drag a column onto
// a lane to stack it there, between lanes for a lane of its own, onto Hidden to take it off the board; split a column
// to place its statuses one by one; drag a lane's head to reorder; type in its name to rename. The server arranges a
// layout over the board (internal/lanes, as the board does); statuses of columns on other boards stay with their
// lane. Each change writes the config file.
// Keys: enter on the row starts editing; h/l pick a column, H/L stack it on the lane before or after, n gives it a
// lane of its own, x hides or shows it, s splits it into statuses or gathers them, < > move its lane, r renames its
// lane, esc leaves. The server turns the edited view (lanes.Draft) into the layout written, as the TUI's arrange mode
// does.
import { h, debounce } from '../lib/dom.js';
import { icon } from '../lib/icons.js';
import { saver } from './settings_config.js';
import { projectOf, lastBoard, boardsOf } from './plan_ctx.js';
import { T, Tn } from '../lib/i18n.js';

const DRAG_COL = 'text/x-laneway-col', DRAG_LANE = 'text/x-laneway-lane';
// A piece is what moves: a column whole ({Col}) or a status of a split one ({Col, Status}), keyed "3" or "3:10020".
const keyOf = p => (p.Status ? p.Col + ':' + p.Status : String(p.Col));
const pieceOf = k => { const [c, st] = String(k).split(':'); return st ? { Col: Number(c), Status: st } : { Col: Number(c) }; };

// designLanes gives the settings row of ui.lane_layouts its editor.
export function designLanes(app, host, options) {
  const O = options.find(o => o.name === 'lane_layouts');
  if (!O) return () => {};
  const ui = () => (app.session && app.session.ui) || {};
  // specs are ui.lane_layouts as written: lower-case keys, status ids as strings.
  const norm = l => ({ name: l.Name || '', site: l.Site || '', boards: (l.Boards || []).map(Number), hidden: (l.Hidden || []).map(String),
    lanes: (l.Lanes || []).map(x => ({ name: x.Name || '', statuses: (x.Statuses || []).map(String) })) });
  const specs = (ui().LaneLayouts || []).map(norm);
  let cur = 0, pick = null, seq = 0;
  // B is the board the layout shows over; view the layout arranged on it: lanes of piece keys, with the statuses of
  // columns elsewhere (foreign) kept, and the hidden pieces. split are the columns split though their statuses sit
  // together.
  const B = { project: projectOf(app, null), board: 0, boards: [], cols: [], names: {}, fits: true, err: '' };
  let view = null;
  const split = new Set();

  const out = l => {
    const o = { name: l.name, lanes: l.lanes.map(x => (x.name ? { name: x.name, statuses: x.statuses } : { statuses: x.statuses })) };
    if (l.site) o.site = l.site;
    if (l.boards.length) o.boards = l.boards;
    if (l.hidden.length) o.hidden = l.hidden;
    return o;
  };
  const save = debounce(saver(app, O, () => { const v = specs.filter(l => l.name.trim() && l.lanes.length).map(out); return v.length ? v : null; }), 400);
  const colName = ci => B.cols[ci].Name;
  const pieceName = k => { const p = pieceOf(k); return p.Status ? colName(p.Col) + ' › ' + (B.names[p.Status] || p.Status) : colName(p.Col); };
  // laneName is a lane's name, else its first whole column's, else its first status's (as the board names it).
  const laneName = l => {
    if (l.name || !l.pieces.length) return l.name || T('Lane');
    const ps = l.pieces.map(pieceOf).sort((a, b) => a.Col - b.Col), p = ps.find(x => !x.Status) || ps[0];
    return (p.Status && B.names[p.Status]) || colName(p.Col);
  };
  // splitIn shows each split column in ks status by status.
  const splitIn = ks => ks.flatMap(k => { const p = pieceOf(k); return !p.Status && split.has(p.Col) ? (B.cols[p.Col].StatusIDs || []).map(id => p.Col + ':' + id) : [k]; });

  // ---- the layout over the board
  // arrange has the server lay the layout over the board (internal/lanes): with a draft (the view edited) it
  // applies it first, and the layout to write comes back. quiet keeps the view and the page as they are (a name
  // being typed).
  async function arrange(draft, quiet) {
    const spec = specs[cur], n = quiet ? seq : ++seq;
    if (!spec || !B.board) { view = null; host.redraw(O); return; }
    try {
      const r = await app.api.post('/boards/' + B.board + '/arrange', { Layout: spec, Draft: draft });
      if (n !== seq && !quiet) return;
      specs[cur] = norm(r.Layout);
      if (draft) save();
      if (quiet) return;
      B.cols = r.Columns || []; B.names = r.StatusNames || {}; B.fits = r.Fits; B.err = '';
      const d = r.Draft;
      const ks = ps => splitIn((ps || []).map(keyOf));
      view = { lanes: (d.Lanes || []).map(l => ({ name: l.Name, pieces: ks(l.Pieces), foreign: l.Foreign || [] })), hidden: ks(d.Hidden), foreignHidden: d.ForeignHidden || [] };
      if (pick !== null && !order().includes(pick)) pick = order().find(k => pieceOf(k).Col === pieceOf(pick).Col) ?? null;
    } catch (e) {
      if (quiet) return app.ui.errToast(e);
      if (n === seq) { view = null; B.err = e.message; }
    }
    host.redraw(O);
  }
  const draft = () => ({ Lanes: view.lanes.map(l => ({ Name: l.name, Pieces: l.pieces.map(pieceOf), Foreign: l.foreign })), Hidden: view.hidden.map(pieceOf), ForeignHidden: view.foreignHidden });
  // commit writes the view into the layout and saves it; write does without redrawing.
  function commit() { host.redraw(O); return arrange(draft()); }
  const write = debounce(() => arrange(draft(), true), 400);
  const laneOf = k => view.lanes.find(l => l.pieces.includes(k));
  // take lifts piece k out of its lane or Hidden, a whole column's statuses with it.
  function take(k) {
    const p = pieceOf(k), gone = x => x === k || (!p.Status && pieceOf(x).Col === p.Col);
    view.lanes.forEach(l => { l.pieces = l.pieces.filter(x => !gone(x)); });
    view.hidden = view.hidden.filter(x => !gone(x));
  }
  // stack puts piece k in lane l; alone makes it a lane of its own before lane `before` (null: last).
  function stack(k, l) { take(k); l.pieces.push(k); pick = k; commit(); }
  function alone(k, before) {
    take(k);
    const at = before ? view.lanes.indexOf(before) : view.lanes.length;
    view.lanes.splice(at < 0 ? view.lanes.length : at, 0, { name: '', pieces: [k], foreign: [] });
    pick = k; commit();
  }
  function hide(k) {
    if (view.hidden.includes(k)) return alone(k, null);
    take(k); view.hidden.push(k); pick = k; commit();
  }
  // splitOrGather lists a column status by status, or gathers a split one's statuses where piece k is.
  function splitOrGather(k) {
    const p = pieceOf(k), col = B.cols[p.Col];
    if (!p.Status) {
      if ((col.StatusIDs || []).length < 2) return app.ui.toast(T('%s has one status', col.Name));
      split.add(p.Col);
      view.lanes.forEach(l => { l.pieces = splitIn(l.pieces); });
      view.hidden = splitIn(view.hidden);
      pick = p.Col + ':' + col.StatusIDs[0]; host.redraw(O); return;
    }
    split.delete(p.Col);
    const l = laneOf(k), whole = String(p.Col);
    take(whole);
    if (l) l.pieces.push(whole); else view.hidden.push(whole);
    pick = whole; commit();
  }
  function moveLane(l, before) {
    if (l === before) return;
    view.lanes.splice(view.lanes.indexOf(l), 1);
    const at = before ? view.lanes.indexOf(before) : view.lanes.length;
    view.lanes.splice(at, 0, l); commit();
  }

  // ---- layouts and boards
  // newLayout starts from the board's columns, a lane each (the server places none, so each keeps its own).
  async function newLayout() {
    if (!B.board) return app.ui.toast(T('Pick a board first'), { kind: 'err' });
    let n = specs.length + 1;
    while (specs.some(l => l.name === 'Layout ' + n)) n++;
    specs.push({ name: 'Layout ' + n, site: '', boards: [], hidden: [], lanes: [] });
    cur = specs.length - 1; pick = null; split.clear();
    await arrange();
    if (view) commit();
  }
  function dropLayout() {
    specs.splice(cur, 1);
    cur = Math.max(0, Math.min(cur, specs.length - 1));
    save(); arrange();
  }
  async function setProject(p) {
    B.project = p; B.boards = []; B.board = 0; split.clear();
    try { B.boards = await boardsOf(app, p); } catch (e) { B.err = e.message; }
    const last = lastBoard(app, p);
    B.board = (B.boards.find(b => b.ID === last) || B.boards[0] || {}).ID || 0;
    arrange();
  }
  // setAll clears the boards (every board it fits), or else keeps it to the board shown.
  function setAll(all) {
    specs[cur].boards = all || !B.board ? [] : [B.board];
    commit();
  }
  function toggleBoard(id) {
    const spec = specs[cur];
    spec.boards = spec.boards.includes(id) ? spec.boards.filter(x => x !== id) : [...spec.boards, id];
    commit();
  }

  // ---- keyboard: a scope over the settings page's own while editing (as the card designer's)
  let keys = null;
  const outside = e => { if (!e.target.closest || !e.target.closest('.ln, .modal, .pick')) { leave(); host.redraw(O); } };
  const leave = () => { if (keys) { keys.dispose(); keys = null; document.removeEventListener('mousedown', outside, true); host.end(); } };
  const order = () => (view ? [...view.lanes.flatMap(l => l.pieces), ...view.hidden] : []);
  const choose = d => { const o = order(); if (!o.length) return; pick = o[(Math.max(0, o.indexOf(pick)) + d + o.length) % o.length]; host.redraw(O); };
  function step(d) { // H/L: onto the lane before or after
    if (pick === null || !view) return;
    const l = laneOf(pick), i = l ? view.lanes.indexOf(l) : view.lanes.length;
    const to = view.lanes[i + d];
    if (to) stack(pick, to);
  }
  function shiftLane(d) { // < >: the column's lane one place along
    const l = pick !== null && view && laneOf(pick);
    if (!l) return;
    const i = view.lanes.indexOf(l), j = i + d;
    if (j < 0 || j >= view.lanes.length) return;
    view.lanes.splice(i, 1); view.lanes.splice(j, 0, l); commit();
  }
  function rename() {
    const l = pick !== null && view && laneOf(pick);
    const inp = l && O.el && O.el.querySelectorAll('.ln-name')[view.lanes.indexOf(l)];
    if (inp) { inp.focus(); inp.select(); }
  }
  O.wide = true;
  O.activate = O.change = () => {
    if (!view) return;
    pick = pick ?? order()[0] ?? null;
    host.begin({ o: O, commit: () => {}, cancel: () => { leave(); host.redraw(O); } });
    if (keys) keys.dispose(); else document.addEventListener('mousedown', outside, true);
    keys = app.keys.scope('lanes', { layer: 2, covers: () => true });
    const g = { group: T('Lane layouts') };
    keys.bind('Escape', () => { if (document.activeElement && document.activeElement.matches('input')) document.activeElement.blur(); else { leave(); host.redraw(O); } }, T('leave the editor'), { ...g, bar: T('leave'), input: true });
    keys.bind(['h', 'ArrowLeft'], () => choose(-1), T('previous column'), { ...g, bar: T('h l pick') });
    keys.bind(['l', 'ArrowRight'], () => choose(1), T('next column'), g);
    keys.bind('H', () => step(-1), T('stack it on the lane before'), { ...g, bar: T('H L stack'), writes: true });
    keys.bind('L', () => step(1), T('stack it on the lane after'), { ...g, writes: true });
    keys.bind('n', () => { if (pick !== null) { const l = laneOf(pick); alone(pick, l ? view.lanes[view.lanes.indexOf(l) + 1] || null : null); } }, T('a lane of its own'), { ...g, bar: T('n own lane'), writes: true });
    keys.bind(['x', 'Delete'], () => { if (pick !== null) hide(pick); }, T('hide it, or show it again'), { ...g, bar: T('x hide'), writes: true });
    keys.bind('s', () => { if (pick !== null) splitOrGather(pick); }, T('split the column into its statuses, or gather them'), { ...g, bar: T('s split'), writes: true });
    keys.bind('<', () => shiftLane(-1), T('move its lane left'), { ...g, bar: T('< > lane'), writes: true });
    keys.bind('>', () => shiftLane(1), T('move its lane right'), { ...g, writes: true });
    keys.bind('r', rename, T('rename its lane'), g);
    host.redraw(O);
  };
  O.reset = () => { specs.length = 0; cur = 0; view = null; save(); host.redraw(O); };

  // ---- drawing
  let dragLane = null;
  const over = (e, ok) => { if (!ok) return; e.preventDefault(); e.dataTransfer.dropEffect = 'move'; e.currentTarget.classList.add('over'); };
  const leaveZone = e => { if (!e.currentTarget.contains(e.relatedTarget)) e.currentTarget.classList.remove('over'); };
  const has = (e, t) => e.dataTransfer.types.includes(t);
  // chip is piece k, with a button splitting a column of several statuses or gathering a split one's.
  const chip = k => {
    const p = pieceOf(k), ids = B.cols[p.Col].StatusIDs || [];
    return h('span.ln-piece',
      h('button.cd-chip.ln-col' + (keys && k === pick ? '.on' : '') + (p.Status ? '.ln-status' : ''), {
        type: 'button', tabindex: -1, draggable: true, title: p.Status ? pieceName(k) : ids.map(id => B.names[id] || id).join(', '), 'aria-label': pieceName(k),
        ondragstart: e => { e.dataTransfer.setData(DRAG_COL, k); e.dataTransfer.effectAllowed = 'move'; e.currentTarget.classList.add('dragging'); },
        ondragend: e => e.currentTarget.classList.remove('dragging'),
        onclick: () => { pick = k; if (!keys) O.activate(); else host.redraw(O); },
      }, p.Status ? B.names[p.Status] || p.Status : colName(p.Col)),
      (p.Status || ids.length > 1) && h('button.cd-fx.ln-split', { type: 'button', tabindex: -1,
        title: p.Status ? T('Gather %s here', colName(p.Col)) : T('Split into statuses'), 'aria-label': p.Status ? T('Gather %s here', colName(p.Col)) : T('Split %s into statuses', colName(p.Col)),
        onclick: () => splitOrGather(k) }, icon(p.Status ? 'merge' : 'split')));
  };
  const dropped = e => { const k = e.dataTransfer.getData(DRAG_COL); return k && order().includes(k) ? k : null; };
  const gap = before => h('div.ln-gap', {
    title: T('Drop a column here for a lane of its own'),
    ondragover: e => over(e, has(e, DRAG_COL) || has(e, DRAG_LANE)), ondragleave: leaveZone,
    ondrop: e => {
      e.preventDefault(); e.currentTarget.classList.remove('over');
      if (has(e, DRAG_LANE) && dragLane) return moveLane(dragLane, before);
      const k = dropped(e);
      if (k) alone(k, before);
    },
  });
  const lane = l => h('div.ln-lane', {
    ondragover: e => over(e, has(e, DRAG_COL)), ondragleave: leaveZone,
    ondrop: e => { e.preventDefault(); e.currentTarget.classList.remove('over'); const k = dropped(e); if (k) stack(k, l); },
  },
  h('div.ln-head',
    h('span.cd-grip', { draggable: true, title: T('Drag to reorder'), ondragstart: e => { dragLane = l; e.dataTransfer.setData(DRAG_LANE, '1'); e.dataTransfer.effectAllowed = 'move'; }, ondragend: () => { dragLane = null; } }, icon('grip-vertical')),
    h('input.input.ln-name', { type: 'text', value: l.name, placeholder: laneName(l), spellcheck: false, autocomplete: 'off', 'aria-label': T('Lane name'),
      oninput: e => { l.name = e.target.value.trim(); write(); } })),
  h('div.ln-body', l.pieces.map(chip),
    l.foreign.length > 0 && h('span.faint.ln-foreign', { title: l.foreign.join(', ') }, (l.pieces.length ? '+ ' : '') + Tn(l.foreign.length, '%d status on other boards', '%d statuses on other boards', l.foreign.length))));

  O.render = () => {
    const spec = specs[cur], projects = (app.session && app.session.projects) || [];
    const top = h('div.row.ln-top',
      specs.length > 0 && h('select.input', { 'aria-label': T('Layout'), onchange: e => { cur = Number(e.target.value); pick = null; split.clear(); arrange(); } },
        specs.map((l, i) => h('option', { value: i, selected: i === cur }, l.name || T('Untitled')))),
      spec && h('input.input.ln-lname', { type: 'text', value: spec.name, placeholder: T('Name'), 'aria-label': T('Layout name'), spellcheck: false,
        oninput: debounce(e => { specs[cur].name = e.target.value.trim(); save(); }, 500), onchange: () => host.redraw(O) }),
      h('button.btn', { type: 'button', onclick: newLayout }, icon('plus'), T('New layout')),
      spec && h('button.btn.ghost', { type: 'button', onclick: dropLayout }, T('Delete layout')),
      h('span.spacer'), h('span.cd-status'));
    const board = h('div.row.ln-board',
      h('span.cd-label', T('Board')),
      h('select.input', { 'aria-label': T('Project'), onchange: e => setProject(e.target.value) }, projects.map(p => h('option', { value: p, selected: p === B.project }, p))),
      h('select.input', { 'aria-label': T('Board'), onchange: e => { B.board = Number(e.target.value); split.clear(); arrange(); } },
        B.boards.map(b => h('option', { value: b.ID, selected: b.ID === B.board }, b.Name))),
      spec && h('span.cd-label', T('On')),
      spec && h('span.ln-boards',
        h('label.ln-bchk', { title: T('Every board with two or more of its columns') }, h('input', { type: 'checkbox', checked: !spec.boards.length, onchange: e => setAll(e.target.checked) }), T('Every board it fits')),
        B.boards.map(b => h('label.ln-bchk', h('input', { type: 'checkbox', checked: spec.boards.includes(b.ID), onchange: () => toggleBoard(b.ID) }), b.Name)),
        spec.boards.filter(id => !B.boards.some(b => b.ID === id)).map(id => h('span.chip', T('board %s', id), h('button.cd-fx', { type: 'button', 'aria-label': T('Remove board %s', id), onclick: () => toggleBoard(id) }, '×')))));
    if (!spec) return h('div.cd.ln', top, board, h('div.faint', T("No lane layouts yet. A layout stacks, reorders, renames and hides a board's columns, for you; alt+l switches to it.")));
    if (!view) return h('div.cd.ln', top, board, h('div.faint', B.err || (B.board ? T('Loading the board…') : T('Pick a board'))));
    const lanes = h('div.ln-lanes', view.lanes.flatMap(l => [gap(l), lane(l)]), gap(null));
    const hidden = h('div.cd-trayrow', h('span.cd-label', T('Hidden')),
      h('div.cd-zone.ln-hidden', {
        ondragover: e => over(e, has(e, DRAG_COL)), ondragleave: leaveZone,
        ondrop: e => { e.preventDefault(); e.currentTarget.classList.remove('over'); const k = dropped(e); if (k && !view.hidden.includes(k)) hide(k); },
      }, view.hidden.map(chip), !view.hidden.length && h('span.faint', T('drop a column here to take it off the board'))));
    return h('div.cd.ln', top, board, lanes, hidden,
      h('div.row.cd-foot',
        h('span.faint', keys ? T('h l pick · H L stack · n own lane · x hide · s split · < > lane · r rename · esc done') : T('Drag columns onto lanes, between them, or onto Hidden; split one to place its statuses; enter for keys.')),
        h('span.spacer'),
        !B.fits && h('span.st-err', T('Not on this board: tick it, or place two of its columns'))));
  };
  setProject(B.project);
  return () => leave();
}
