// One project + board context for every project-scoped view (board, planning,
// reports, roadmap, standup, create). Order: route params, else the last
// project, else the first configured; board: the project's remembered board,
// else its first. Prefs 'project' and 'board.last.<project>' (server side,
// mirrored per site in localStorage by app.prefs) persist it; nothing ever asks for a
// board when one is remembered.
import { h } from '../lib/dom.js';
import * as store from '../lib/store.js';
import { T } from '../lib/i18n.js';

function pref(app, k) {
  const v = app.prefs.get(k, '');
  if (v !== '' && v != null) return String(v);
  return store.get('p:' + k, '');
}

export function lastProject(app) {
  const legacy = pref(app, 'board.last').split('/')[0];
  return pref(app, 'project') || legacy || '';
}

// The remembered board id of a project, 0 when none.
export function lastBoard(app, project) {
  if (!project) return 0;
  const n = Number(pref(app, 'board.last.' + project) || pref(app, 'board:' + project));
  if (n) return n;
  const [p, b] = pref(app, 'board.last').split('/');
  return p === project ? Number(b) || 0 : 0;
}

export function projectOf(app, params) {
  const s = app.session;
  return (params && params.project) || lastProject(app) || (s.projects && s.projects[0]) || '';
}

// setCtx remembers project and (when given) board for all views.
export function setCtx(app, project, board) {
  if (!project) return;
  if (pref(app, 'project') !== project) app.prefs.set('project', project);
  const id = board && (board.ID || board);
  if (id && lastBoard(app, project) !== Number(id)) app.prefs.set('board.last.' + project, Number(id));
}

export async function boardsOf(app, project) {
  return (await app.api.get('/projects/' + encodeURIComponent(project) + '/boards')) || [];
}

const clearPref = (app, k) => { if (k in app.prefs.data || pref(app, k)) { delete app.prefs.data[k]; app.prefs.set(k, ''); } };

// recover forgets a remembered project (and its board) that this site does not know; true when
// something was dropped, so the caller can retry with the defaults.
export function recover(app, project) {
  const had = pref(app, 'project') === project || !!pref(app, 'board.last.' + project) || pref(app, 'board.last').split('/')[0] === project;
  if (pref(app, 'project') === project) clearPref(app, 'project');
  clearPref(app, 'board.last.' + project); clearPref(app, 'board:' + project);
  if (pref(app, 'board.last').split('/')[0] === project) clearPref(app, 'board.last');
  return had;
}

// sanitize runs once at boot: a remembered project or board the current site lacks (left over from
// another site) is dropped, so no view asks for it. Quiet; network trouble keeps everything.
export async function sanitize(app) {
  const cfg = app.session.projects || [];
  let p = lastProject(app);
  if (p && !cfg.includes(p)) {
    let list;
    try { list = await app.api.get('/projects'); } catch (e) { return; }
    if (!(list || []).some(x => x.Key === p)) { recover(app, p); p = lastProject(app); }
  }
  p = p || cfg[0];
  const id = p && lastBoard(app, p);
  if (!id) return;
  try {
    const bs = await boardsOf(app, p);
    if (!bs.some(b => b.ID === id)) clearPref(app, 'board.last.' + p);
  } catch (e) { if (e.status === 404) recover(app, p); }
}

// resolve → {project, board, boards}; board is null when the project has no
// board of the wanted type (scrum unless any).
export async function resolve(app, params, { scrum = true } = {}) {
  const project = projectOf(app, params);
  if (!project) return { project: '', board: null, boards: [] };
  let boards;
  try { boards = await boardsOf(app, project); } catch (e) {
    if (e.status === 404 && (recover(app, project) || (params && params.project))) return resolve(app, { ...params, project: '', board: '' }, { scrum });
    throw e;
  }
  const ok = b => !scrum || b.Type === 'scrum';
  const explicit = params && params.board ? boards.find(b => String(b.ID) === String(params.board)) : null;
  const last = lastBoard(app, project);
  const board = explicit || boards.find(b => b.ID === last && ok(b)) || boards.find(ok) || null;
  // A fallback never overwrites a remembered board (a kanban one, in a scrum-only view).
  setCtx(app, project, explicit || (last ? null : board));
  return { project, board, boards };
}

export const remember = (app, project, board) => setCtx(app, project, board);

// starProject stars or unstars a project: your projects (jira.projects) are first in the picker, the first opens at
// start. Saved at once; false when it couldn't be.
export async function starProject(app, key) {
  const mine = app.session.projects || [];
  const next = mine.includes(key) ? mine.filter(k => k !== key) : [...mine, key];
  try { app.session.projects = (await app.api.put('/site/projects', { Projects: next })).projects; } catch (e) { app.ui.errToast(e); return false; }
  return true;
}

// pickProject → the project key, or null. Starred ones first; ctrl+s or a row's star stars it.
export async function pickProject(app) {
  const mine = app.session.projects || [];
  let items = [];
  try { items = [...(await app.api.get('/projects'))].sort((a, b) => (mine.includes(b.Key) - mine.includes(a.Key))); } catch (e) { items = []; }
  if (!items.length) mine.forEach(k => items.push({ Key: k, Name: k }));
  if (!items.length) { app.ui.toast(T('No projects'), { kind: 'err' }); return null; }
  const starred = x => (app.session.projects || []).includes(x.Key);
  const p = await app.ui.pick({ title: T('Project'), items, label: x => x.Key + ' ' + x.Name, star: { on: starred, toggle: x => starProject(app, x.Key) } });
  return p ? p.Key : null;
}

// openProject asks for a project and opens its remembered board (any type). The global alt+p.
export async function openProject(app) {
  const p = await pickProject(app); if (!p) return;
  let b;
  try { b = await boardOf(app, p, { scrum: false }); } catch (e) { return app.ui.errToast(e); }
  if (b) app.go('/board/' + p + '/' + b.ID);
  else app.ui.toast(T('No boards in %s', p), { kind: 'err' });
}

// boardOf → the project's remembered board of the wanted type, else its first; null when none. Never asks.
export async function boardOf(app, project, { scrum = true } = {}) {
  const list = (await boardsOf(app, project)).filter(b => !scrum || b.Type === 'scrum');
  return list.find(b => b.ID === lastBoard(app, project)) || list[0] || null;
}

// pickBoard → a board of the project (asks, even with a remembered one), null when cancelled or none.
export async function pickBoard(app, project, { scrum = true } = {}) {
  let list;
  try { list = (await boardsOf(app, project)).filter(b => !scrum || b.Type === 'scrum'); } catch (e) { app.ui.errToast(e); return null; }
  if (!list.length) { app.ui.toast(T('No boards in %s', project), { kind: 'err' }); return null; }
  if (list.length === 1) return list[0];
  return app.ui.pick({ title: T('%s board', project), items: list, label: x => x.Name, detail: x => x.Type });
}

// switcher: separate project and board crumbs for a view's context slot. Board key B
// (project-only views: B too), project key alt+p. Picking a project moves to its remembered
// board without asking. onPick({project, board}) after the choice is remembered.
// Returns {btn, boardBtn, label(project, board)}.
export function switcher(app, { scope, context, project, board, scrum = true, boards = true, onPick, key = 'B', group }) {
  let cur = project;
  const done = (p, b) => { setCtx(app, p, b); cur = p; onPick({ project: p, board: b }); };
  const runProject = async () => {
    const p = await pickProject(app); if (!p) return;
    let b = null;
    if (boards) { try { b = await boardOf(app, p, { scrum }); } catch (e) { app.ui.errToast(e); return; } }
    done(p, b);
  };
  const runBoard = async () => {
    if (!boards) return runProject();
    const b = await pickBoard(app, cur, { scrum }); if (b) done(cur, b);
  };
  const btn = app.chrome.crumb(boards ? T('Project  (alt+p)') : T('Project  (alt+p, %s)', key), runProject);
  const boardBtn = boards ? app.chrome.crumb(T('Board  (%s)', key), runBoard) : null;
  const label = (p, b) => {
    cur = p; app.chrome.label(btn, p || '\u2014');
    if (boardBtn) app.chrome.label(boardBtn, b && b.Name || '\u2014');
  };
  label(project, board);
  context.append(btn);
  if (boardBtn) context.append(boardBtn);
  if (scope) {
    scope.bind('alt+p', runProject, T('switch project (its last board)'), { group });
    scope.bind(key, runBoard, boards ? T('switch board (same project)') : T('switch project'), { group });
  }
  return { btn, boardBtn, label, run: runBoard, runProject };
}

export const noBoard = (what, project) => h('div.empty', h('h2', what), h('p', project ? T('%s has no scrum board. %s need sprints.', project, what) : T('No project configured.')));
