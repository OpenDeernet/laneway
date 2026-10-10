// Route table. Each view module default-exports mount(el, ctx) → cleanup?
// bar: false marks a view that never fills the view bar (it hides at once instead of holding its place).
// ctx: {app, params, query, scope (a keys scope disposed on leave), context + toolbar (the view bar: where, then how)}.
// Routes: /board/PROJECT/BOARDID?issue=KEY … (':x' are params); the server answers any such path with the shell.
// A missing module shows a "not built yet" page; nothing else breaks.
import { T } from '../lib/i18n.js';
export const routes = [
  { path: '/home', name: 'home', title: T('Home'), key: 'h', bar: false, load: () => import('./home.js') },
  { path: '/board/:project?/:board?', name: 'board', title: T('Board'), key: 'b', load: () => import('./board.js') },
  { path: '/issue/:key', name: 'issue', title: T('Issue'), nav: false, bar: false, load: () => import('./issue_page.js') },
  { path: '/planning/:project?/:board?', name: 'planning', title: T('Planning'), key: 'p', load: () => import('./planning.js') },
  { path: '/reports/:kind?/:project?/:board?', name: 'reports', title: T('Reports'), key: 'r', load: () => import('./reports.js') },
  { path: '/roadmap/:project?', name: 'roadmap', title: T('Roadmap'), key: 'm', load: () => import('./roadmap.js') },
  { path: '/work', name: 'work', title: T('My work'), key: 'w', load: () => import('./work.js') },
  { path: '/inbox', name: 'inbox', title: T('Inbox'), key: 'i', load: () => import('./inbox.js') },
  { path: '/dashboards/:id?', name: 'dashboards', title: T('Dashboards'), bar: false, load: () => import('./dashboards.js') },
  { path: '/wiki/:space?/:page?', name: 'wiki', title: T('Wiki'), key: 'k', bar: false, load: () => import('./wiki.js') },
  { path: '/standup', name: 'standup', title: T('Standup'), key: 's', load: () => import('./standup.js') },
  { path: '/agents', name: 'agents', title: T('Agents'), key: 'a', bar: false, load: () => import('./agents.js') },
  { path: '/mr', name: 'mr', title: T('Merge request'), nav: false, bar: false, load: () => import('./mr.js') },
  { path: '/mrs', name: 'mrs', title: T('Merge requests'), key: 'M', bar: false, load: () => import('./mrs.js') },
  { path: '/rules', name: 'rules', title: T('Rules'), key: 'l', nav: false, load: () => import('./rules.js') },
  { path: '/settings', name: 'settings', title: T('Settings'), key: ',', nav: false, load: () => import('./settings.js') },
];
