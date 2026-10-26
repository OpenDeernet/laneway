// A lane card: its fields where the layout puts them (lib/cardstyle.js), restyled by its look. The board's lanes and
// the settings designer draw it.
//   const w = buildCard(layout)       the .bcw wrapper; w._r holds the parts
//   fillCard(w, card, o)              o: {look, sel, marked, pinned(key), hl(key), tmark(key), ribbon, fdate, stamp}
import { h } from './dom.js';
import { icon, setIcon, TYPE_ICON } from './icons.js';
import { isZero, date, shortDate, ago, localDate } from './fmt.js';
import { prioOrd } from './cardsort.js';
import { PRIO_ICON, ageText, setAvatar } from './cardlist.js';
import { extraOf } from './cardquery.js';
import { check, setCheck } from './selbar.js';
import { colour } from './cardstyle.js';
import { T } from './i18n.js';

const brokenTypeIcons = new Set();
const PR_ICON = { OPEN: ['git-pull-request', 'open'], MERGED: ['git-merge', 'merged'], DECLINED: ['git-pull-request-closed', 'declined'] };
export const catClass = c => (c.Done ? 'done' : c.InProgress ? 'prog' : 'todo');
const startOfToday = () => { const d = new Date(); d.setHours(0, 0, 0, 0); return d.getTime(); };

const PART = {
  type: () => h('span.ctype'), flagged: () => h('span.cflag', { title: T('Flagged') }, icon('flag', true)), priority: () => h('span.cprio'),
  status: () => h('span.cstatus'), points: () => h('span.cpts'), parent: () => h('span.cparent'), subtasks: () => h('span.csub'),
  due: () => h('span.cdue'), pr: () => h('span.cpr'), deploy: () => h('span.cdep'), labels: () => h('span.clabels'), age: () => h('span.cage'),
  avatar: () => h('span.cav'), assignee: () => h('span.cav.cwho'),
};

// typeBadge is c's type icon on its own (the issue input's suggestions).
export function typeBadge(c) { const el = h('span'); fillType(el, c); return el; }

// fillType draws c's type: our icon for Jira's stock ones (TypeKind), else the type's own icon from Jira, else by name.
function fillType(el, c) {
  const own = c.TypeAvatar && !brokenTypeIcons.has(c.TypeAvatar);
  const name = (c.Type || '').toLowerCase();
  const t = TYPE_ICON[c.TypeKind] || (!own && TYPE_ICON[name === 'sub-task' ? 'subtask' : name]);
  el.className = 'ctype t-' + (t ? t[1] : own ? 'img' : 'other');
  el.title = c.Type;
  if (t) setIcon(el, t[0]);
  else if (own) {
    if (el._ico === c.TypeAvatar) return;
    el._ico = c.TypeAvatar;
    el.replaceChildren(h('img', { src: c.TypeAvatar, alt: '', onerror: () => { brokenTypeIcons.add(c.TypeAvatar); if (el._ico === c.TypeAvatar) fillType(el, c); } }));
  } else setIcon(el, '', (c.Type || '?')[0].toUpperCase());
}

export function buildCard(layout) {
  const r = { f: {} };
  const part = f => {
    if (f === 'key') { // the key brings the marks along
      r.key = h('span.ckey');
      return [r.key, r.pin = h('span.cpin', { title: T('Pinned') }, icon('pin')),
        r.hl = h('span.chl', { title: T('A rule highlighted it; opening it clears the mark') }, icon('circle', true)), r.timer = h('span.ctimer', { title: T('Timer running · T stops it') })];
    }
    return (r.f[f] = (PART[f] || (() => h('span.cextra', { dataset: { field: f } })))());
  };
  const line = (cls, left, right, lead) => h('div.' + cls, lead, left.flatMap(part), h('span.sp'), right.flatMap(part));
  const w = h('div.bcw', { role: 'listitem' }, h('div.card', { draggable: true },
    line('c1', layout.top, layout.top_right, r.chk = check()), r.sum = h('div.csum'), line('c3', layout.bottom, layout.bottom_right)));
  w._r = r;
  return w;
}

export function fillCard(w, c, o = {}) {
  const r = w._r, card = w.firstChild, F = r.f, lk = o.look || null;
  const off = f => !!(lk && lk.hidden.has(f));
  const show = (f, on) => { if (F[f]) F[f].hidden = !on || off(f); return !!F[f] && on && !off(f); };
  w.dataset.key = c.Key;
  const rib = o.ribbon || '';
  card.className = 'card ' + catClass(c) + (c.Flagged && F.flagged && !off('flagged') ? ' flagged' : '') + (o.sel ? ' sel' : '') + (o.marked ? ' mark' : '') + (rib ? ' ribbon' : '')
    + (lk && lk.edge ? ' edged' : '') + (lk && lk.tint ? ' tinted' : '') + (lk && lk.fade ? ' faded' : '') + (lk && lk.bold ? ' bold' : '');
  const prop = (k, v) => (v ? card.style.setProperty(k, v) : card.style.removeProperty(k));
  prop('--ribbon', rib); prop('--edge', lk && colour(lk.edge)); prop('--tint', lk && colour(lk.tint));

  if (show('type', true)) fillType(F.type, c);
  setCheck(r.chk, !!o.marked);
  r.key.textContent = c.Key; if (o.stamp) o.stamp(r.key, c.Key);
  r.pin.hidden = !(o.pinned && o.pinned(c.Key));
  const hl = o.hl ? o.hl(c.Key) : null;
  r.hl.hidden = hl === null; r.hl.style.color = hl && hl.startsWith('#') ? hl : '';
  const tm = o.tmark ? o.tmark(c.Key) : ''; r.timer.hidden = !tm; if (tm) setIcon(r.timer, 'timer', tm);
  show('flagged', !!c.Flagged);
  if (show('priority', !!c.Priority)) {
    const po = prioOrd(c);
    F.priority.className = 'cprio p' + po; if (po < 5) setIcon(F.priority, PRIO_ICON[po]); else setIcon(F.priority, '', (c.Priority || '').slice(0, 3)); F.priority.title = c.Priority;
  }
  if (show('status', !!c.Status)) { F.status.textContent = c.Status; F.status.className = 'cstatus cat-' + catClass(c); F.status.title = T('Status'); }
  if (show('points', c.Points !== '' && c.Points != null)) F.points.textContent = c.Points;
  r.sum.textContent = c.Summary; r.sum.title = c.Summary;
  if (show('parent', !!c.ParentKey)) {
    F.parent.textContent = c.ParentSummary || c.ParentKey; F.parent.dataset.open = c.ParentKey; F.parent.title = c.ParentKey + ' ' + c.ParentSummary;
  } else if (F.parent) F.parent.dataset.open = '';
  if (show('subtasks', !!c.Subtasks)) { F.subtasks.textContent = c.SubtasksDone + '/' + c.Subtasks; F.subtasks.style.setProperty('--p', Math.round(100 * c.SubtasksDone / c.Subtasks) + '%'); F.subtasks.title = T('Subtasks done'); }
  const due = date(c.Due);
  if (show('due', !!due)) { F.due.textContent = o.fdate ? o.fdate(c.Due, shortDate(c.Due)) : shortDate(c.Due); F.due.className = 'cdue' + (!c.Done && due.getTime() < startOfToday() ? ' overdue' : ''); F.due.title = T('Due %s', localDate(due)); }
  if (show('pr', !!c.PR)) { if (PR_ICON[c.PR]) setIcon(F.pr, ...PR_ICON[c.PR]); else setIcon(F.pr, '', c.PR || ''); F.pr.className = 'cpr pr-' + (c.PR || '').toLowerCase(); }
  if (show('deploy', !!c.Deploy)) setIcon(F.deploy, 'rocket', c.Deploy);
  const ls = c.Labels ? c.Labels.split(' ') : [];
  if (show('labels', ls.length > 0)) { F.labels.textContent = ls.slice(0, 2).map(l => '#' + l).join(' ') + (ls.length > 2 ? ' +' + (ls.length - 2) : ''); F.labels.title = c.Labels; }
  const a = F.age ? ageText(c) : '';
  if (show('age', !!a)) { F.age.textContent = a; F.age.title = T('In status since %s', ago(!isZero(c.Since) ? c.Since : c.Created)); }
  if (show('avatar', true)) setAvatar(F.avatar, c);
  if (show('assignee', true)) setAvatar(F.assignee, c, true);
  const ex = extraOf(c);
  for (const [f, el] of Object.entries(F)) {
    if (PART[f]) continue;
    const v = ex[f.toLowerCase()] || '';
    if (show(f, !!v)) { el.textContent = v; el.title = f + ': ' + v; }
  }
}
