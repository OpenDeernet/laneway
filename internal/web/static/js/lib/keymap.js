// Which TUI action (ui.keys name, internal/ui/keys.go) each web binding is, by scope and default key.
// A `ui.keys` entry for the action replaces the key here; bindings without an action stay as they are.
const COMMON = { j: 'down', ArrowDown: 'down', k: 'up', ArrowUp: 'up', y: 'copy_key' };
const TABLE = {
  global: { ':': 'palette', '/': 'search', 'g g': 'goto', '?': 'help', n: 'create', 'alt+p': 'project', Q: 'jql', ',': 'settings', 'ctrl+e': 'refine',
    W: 'timesheet', I: 'inbox', U: 'standup', O: 'my_work', 'ctrl+g': 'agents', 'g r': 'charts', 'g p': 'plan', 'g m': 'roadmap', 'g M': 'merge_requests', 'g h': 'home' },
  timer: { T: 'timer', w: 'log_work' },
  undo: { u: 'undo' },
  sites: { '@': 'site' },
  ask: { 'ctrl+a': 'ask' },
  board: { ...COMMON, Home: 'top', End: 'bottom', PageDown: 'page_down', PageUp: 'page_up', r: 'refresh', o: 'browser', h: 'left', ArrowLeft: 'left', l: 'right', ArrowRight: 'right', Enter: 'open', s: 'status', e: 'summary', a: 'assign', p: 'priority', P: 'points',
    H: 'move_left', L: 'move_right', J: 'rank_down', K: 'rank_up', 'alt+k': 'rank_top', 'alt+j': 'rank_bottom', x: 'mark', 'ctrl+a': 'mark_all', X: 'bulk', t: 'toggle_mode', O: 'sort',
    B: 'board', '[': 'prev_view', ']': 'next_view', m: 'mine', A: 'assignee_filter', 0: 'clear_filters', F: 'filter_builder', Y: 'copy_url', 'ctrl+y': 'copy_branch',
    '*': 'pin', z: 'fold', Z: 'unfold_all', '}': 'next_band', '{': 'prev_band', c: 'compact', 'alt+e': 'empty_lanes', 'alt+l': 'lane_layout', '.': 'repeat', 'alt+t': 'time_machine', 'alt+o': 'closed_sprint', M: 'move_sprint', E: 'quick_edit', V: 'releases' },
  planning: { ...COMMON, h: 'left', ArrowLeft: 'left', l: 'right', ArrowRight: 'right', Home: 'top', End: 'bottom', PageDown: 'page_down', PageUp: 'page_up', o: 'browser', J: 'rank_down', K: 'rank_up', m: 'move_sprint', x: 'mark', N: 'plan_new', Z: 'plan_start', C: 'plan_complete', E: 'plan_rename', P: 'points', B: 'board', 'alt+p': 'project', Enter: 'open', R: 'refresh', e: 'quick_edit', X: 'bulk', O: 'sort', F: 'filter_builder' },
  reports: { ...COMMON, R: 'refresh', Enter: 'open', '[': 'prev_view', ']': 'next_view', d: 'chart_done', c: 'chart_compare' },
  roadmap: { ...COMMON, Home: 'top', End: 'bottom', o: 'browser', h: 'left', ArrowLeft: 'left', l: 'right', ArrowRight: 'right', '+': 'zoom_in', '-': 'zoom_out', '.': 'today', Space: 'roadmap_fold', R: 'refresh', Enter: 'open',
    H: 'move_left', L: 'move_right', '<': 'end_earlier', '>': 'end_later', e: 'roadmap_grip', f: 'roadmap_issues', E: 'roadmap_edit', Y: 'copy_url' },
  work: { ...COMMON, r: 'refresh', o: 'browser', h: 'prev_view', l: 'next_view', ArrowLeft: 'left', ArrowRight: 'right', Enter: 'open', e: 'edit_entry', d: 'delete_entry', p: 'propose_work', x: 'mark', X: 'mark_all', m: 'move_entry' },
  inbox: { ...COMMON, Home: 'top', End: 'bottom', PageDown: 'page_down', PageUp: 'page_up', r: 'refresh', o: 'browser', Enter: 'open', e: 'inbox_done', E: 'inbox_done_all', s: 'inbox_snooze', u: 'inbox_unread', c: 'comment', R: 'reply' },
  standup: { ...COMMON, r: 'refresh', z: 'fold', '[': 'prev_view', ']': 'next_view', Enter: 'open', h: 'left', ArrowLeft: 'left', l: 'right', ArrowRight: 'right', Space: 'standup_pause', s: 'standup_shuffle', P: 'standup_park', A: 'assignee_filter' },
  agents: { ...COMMON, Home: 'top', End: 'bottom', r: 'refresh', o: 'browser', Enter: 'open', v: 'toggle_panel', p: 'agent_prompt', d: 'agent_stop', S: 'start_work', 'ctrl+\\': 'agent_back' },
  mrs: { j: 'down', ArrowDown: 'down', k: 'up', ArrowUp: 'up', Home: 'top', End: 'bottom', PageDown: 'page_down', PageUp: 'page_up', r: 'refresh', o: 'browser', Enter: 'open' },
  terminal: { 'ctrl+\\': 'agent_back' },
  'agents-global': { S: 'start_work', 'ctrl+y': 'copy_branch', 'ctrl+\\': 'agent_back' },
  issue: { j: 'next_comment', ArrowDown: 'next_comment', k: 'prev_comment', ArrowUp: 'prev_comment', 'ctrl+d': 'page_down', 'ctrl+u': 'page_up', c: 'comment', R: 'reply', e: 'summary', E: 'description', a: 'assign', p: 'priority', P: 'points',
    l: 'labels', 3: 'history', r: 'refresh', d: 'delete_comment', G: 'linked_issue', A: 'issue_actions', Backspace: 'back', i: 'image', y: 'copy_key', Y: 'copy_url', o: 'browser', s: 'status', '*': 'pin', '<': 'panel_wider', '>': 'panel_narrower', 'ctrl+\\': 'agent_back' },
  'issue-dev': { D: 'development' },
  'issue-notes': { N: 'notes' },
};
export const actionFor = (scope, spec) => (TABLE[scope] && TABLE[scope][spec]) || '';

const NAMED = { enter: 'Enter', esc: 'Escape', space: 'Space', ' ': 'Space', up: 'ArrowUp', down: 'ArrowDown', left: 'ArrowLeft', right: 'ArrowRight',
  tab: 'Tab', backspace: 'Backspace', delete: 'Delete', home: 'Home', end: 'End', pgup: 'PageUp', pgdown: 'PageDown' };
// A key as the config spells it (bubbletea: "ctrl+y", "enter", "H") in the web's spelling; '' when the browser can't.
export function fromTUI(k) {
  const m = /^((?:ctrl\+|alt\+)*)(.+)$/.exec(k);
  if (!m || /shift|super|hyper|meta/.test(m[2]) && m[2].length > 1 && !NAMED[m[2]]) return '';
  const name = NAMED[m[2].toLowerCase()] || m[2];
  return name.length > 1 && !/^[A-Z][a-z0-9]+([A-Z][a-z]+)*$/.test(name) ? '' : m[1] + name;
}
const TUI_NAME = {};
for (const [t, w] of Object.entries(NAMED)) TUI_NAME[w] = TUI_NAME[w] || t;
// A web key in the config's spelling; '' when fromTUI could not read it back.
export function toTUI(k) {
  const m = /^((?:ctrl\+|alt\+)*)(.+)$/.exec(k);
  const t = m ? m[1] + (TUI_NAME[m[2]] || m[2]) : '';
  return t && fromTUI(t) === k ? t : '';
}
// Both ways for a whole spec, "g p" a key at a time; '' when a key does not convert.
const each = fn => spec => { const l = spec.split(' ').map(fn); return l.every(Boolean) ? l.join(' ') : ''; };
export const specFromTUI = each(fromTUI);
export const specToTUI = each(toTUI);
