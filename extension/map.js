// Jira Cloud URL → laneway web route. Pure: no browser APIs, so node tests it.
// null: laneway has no view for it, the page stays in Jira.

const KEY = /^[A-Za-z][A-Za-z0-9_]*-\d+$/;
const PROJ = /^[A-Za-z][A-Za-z0-9_]*$/;

// Jira's report slugs (and RapidBoard chart= names), letters only, lower case → laneway report kind.
const REPORTS = {
  burndownchart: 'burndown', burndown: 'burndown', sprintburndown: 'burndown',
  burnupchart: 'burnup', burnup: 'burnup',
  cumulative: 'cfd', cumulativeflowdiagram: 'cfd', cfd: 'cfd',
  velocitychart: 'velocity', velocity: 'velocity',
  controlchart: 'cycle', cycletime: 'cycle',
  sprintretrospective: 'retro', sprintreport: 'retro',
  releaseburndown: 'releases', versionreport: 'releases', releases: 'releases',
};

// Jira's system filters: ?filter=-1 in the issue navigator, ?filter=myopenissues in a project's issues.
const SYSTEM = {
  '-1': ['My open issues', 'assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC'],
  '-2': ['Reported by me', 'reporter = currentUser() ORDER BY created DESC'],
  '-3': ['Viewed recently', 'issuekey in issueHistory() ORDER BY lastViewed DESC'],
  '-4': ['All issues', 'ORDER BY created DESC'],
  '-5': ['Open issues', 'resolution = Unresolved ORDER BY priority DESC, updated DESC'],
  '-6': ['Created recently', 'created >= -1w ORDER BY created DESC'],
  '-7': ['Resolved recently', 'resolutiondate >= -1w ORDER BY updated DESC'],
  '-8': ['Updated recently', 'updated >= -1w ORDER BY updated DESC'],
  '-9': ['Done issues', 'statusCategory = Done ORDER BY updated DESC'],
};
const NAMED = {
  myopenissues: '-1', reportedbyme: '-2', viewedrecently: '-3', allissues: '-4', allopenissues: '-5',
  createdrecently: '-6', resolvedrecently: '-7', updatedrecently: '-8', doneissues: '-9',
};

// http parses an http(s) URL, or null: the hash go.html reads comes from whoever opened it.
const http = href => { try { const u = new URL(href); return /^https?:$/.test(u.protocol) ? u : null; } catch { return null; } };

const report = s => REPORTS[String(s || '').toLowerCase().replace(/[^a-z]/g, '')] || 'burndown';
const withQuery = (path, q) => { const s = new URLSearchParams(Object.entries(q).filter(([, v]) => v)).toString(); return s ? path + '?' + s : path; };

// The query a navigator URL asks for: ?jql= wins (a filter edited in Jira carries both), then ?filter=.
// project scopes it to one project, as Jira's project issue list does. null: no query, or one Jira
// Cloud refuses unbounded (all issues of every project).
function query(q, project) {
  let jql = q.get('jql'), name = 'JQL';
  const f = q.get('filter');
  if (!jql && f) {
    const sys = SYSTEM[NAMED[f.toLowerCase()] || f];
    if (sys) [name, jql] = sys;
    else if (/^\d+$/.test(f)) [name, jql] = ['Filter ' + f, 'filter = ' + f];
  }
  if (!jql) return project ? { name: project, jql: 'project = ' + project + ' ORDER BY created DESC' } : null;
  if (project) {
    const i = jql.toUpperCase().lastIndexOf('ORDER BY'), where = (i >= 0 ? jql.slice(0, i) : jql).trim(), order = i >= 0 ? ' ' + jql.slice(i) : '';
    jql = 'project = ' + project + (where ? ' AND (' + where + ')' : '') + order;
  } else if (/^\s*ORDER BY/i.test(jql)) return null;
  return { name, jql };
}

// A query as laneway's board view: ?sprint=jql:… lists it as a view of the board (the remembered one
// without a project), issue opens in the panel when it is in the list.
const view = (project, v, issue) => withQuery(project ? '/board/' + project : '/board', { sprint: 'jql:' + v.jql, vname: v.name, issue });

// route maps a Jira URL to a laneway hash route ('/board/ABC/12'), or null. An issue's comment link
// (?focusedCommentId=, Server's #comment-) keeps its comment: /issue/ABC-1?comment=10001.
export function route(href) {
  const u = http(href);
  if (!u) return null;
  const r = jiraRoute(u);
  const c = u.searchParams.get('focusedCommentId') || (u.hash.match(/^#comment-(\d+)$/) || [])[1] || '';
  return r && /^\/issue\/[^/?]+$/.test(r) && /^\d+$/.test(c) ? r + '?comment=' + c : r;
}

function jiraRoute(u) {
  const q = u.searchParams, seg = u.pathname.split('/').filter(Boolean).map(decodeURIComponent);
  const sel = KEY.test(q.get('selectedIssue') || '') ? q.get('selectedIssue') : '';

  // /browse/ABC-1, /browse/ABC, /jira/browse/ABC-1
  if (seg[0] === 'jira' && seg[1] === 'browse') seg.shift();
  if (seg[0] === 'browse' && seg[1]) return KEY.test(seg[1]) ? '/issue/' + seg[1] : PROJ.test(seg[1]) ? '/board/' + seg[1] : null;
  // /projects/ABC/issues/ABC-1 (old links)
  if (seg[0] === 'projects' && seg[2] === 'issues' && KEY.test(seg[3] || '')) return '/issue/' + seg[3];
  // /issues/?jql=…, /issues/ABC-1?jql=…, /issues/?filter=-1
  if (seg[0] === 'issues') {
    const key = KEY.test(seg[1] || '') ? seg[1] : sel, v = query(q);
    return v ? view('', v, key) : key ? '/issue/' + key : null;
  }
  if (u.pathname === '/secure/RapidBoard.jspa') return rapidBoard(q, sel);
  if (seg[0] !== 'jira') return null;
  if (seg[1] === 'your-work' || seg[1] === 'for-you') return '/work';
  // /jira/software/projects/ABC/…, /jira/software/c/projects/ABC/…, /jira/core/projects/ABC/…
  const s = seg.slice(seg[2] === 'c' ? 3 : 2);
  if (!['software', 'core'].includes(seg[1]) || s[0] !== 'projects' || !PROJ.test(s[1] || '')) return null;
  return project(s[1], s.slice(2), q, sel);
}

function project(p, rest, q, sel) {
  const [area, id, sub, kind] = rest;
  switch (area) {
    case undefined: case 'summary': case 'board': case 'list': case 'calendar':
      return sel ? '/issue/' + sel : '/board/' + p;
    case 'boards':
      if (!/^\d+$/.test(id || '')) return '/board/' + p;
      return board(p, id, sub, kind, q, sel);
    case 'backlog': return sel ? '/issue/' + sel : '/planning/' + p;
    case 'timeline': case 'roadmap': return '/roadmap/' + p;
    case 'releases': case 'versions': return '/reports/releases/' + p;
    case 'reports': return '/reports/' + report(id) + '/' + p;
    case 'issues': {
      const key = KEY.test(id || '') ? id : sel;
      if (key && !q.get('jql') && !q.get('filter')) return '/issue/' + key;
      return view(p, query(q, p), key);
    }
  }
  return null;
}

function board(p, id, sub, kind, q, sel) {
  const b = '/' + p + '/' + id;
  switch (sub) {
    case 'backlog': return sel ? '/issue/' + sel : '/planning' + b;
    case 'reports': return withQuery('/reports/' + report(kind) + b, { sprint: /^\d+$/.test(q.get('sprint') || '') ? q.get('sprint') : '' });
    case 'timeline': case 'roadmap': return '/roadmap/' + p;
  }
  return withQuery('/board' + b, { issue: sel });
}

// Server and old Cloud boards: /secure/RapidBoard.jspa?rapidView=12&projectKey=ABC&view=planning|reporting&chart=…
function rapidBoard(q, sel) {
  const id = /^\d+$/.test(q.get('rapidView') || '') ? q.get('rapidView') : '', p = PROJ.test(q.get('projectKey') || '') ? q.get('projectKey') : '';
  const v = q.get('view') || '';
  if (!p || !id) return sel ? '/issue/' + sel : p ? '/board/' + p : '/board';
  if (v.startsWith('planning')) return board(p, id, 'backlog', '', q, sel);
  if (v.startsWith('reporting')) return board(p, id, 'reports', q.get('chart'), q, sel);
  return board(p, id, '', '', q, sel);
}

// toLaneway is the laneway URL for a Jira URL, or null.
export function toLaneway(href, base) {
  const r = route(href);
  return r && base.replace(/\/+$/, '') + '/#' + r;
}

// parseHosts reads the options' host lines: "host" or "host = site" ("jira" is the jira: block).
// # starts a comment. Empty: every *.atlassian.net, laneway's current site.
export function parseHosts(text) {
  const out = [];
  for (const line of String(text || '').split('\n')) {
    const l = line.replace(/#.*/, '').trim();
    if (!l) continue;
    const [host, site] = l.split('=').map(s => s.trim());
    if (host) out.push({ host: host.toLowerCase(), site: site === undefined ? null : site === 'jira' ? '' : site });
  }
  return out;
}

// The paths worth sending to laneway, so dashboards, Confluence (/wiki) and the rest never take the
// detour. route() still decides; what it maps to null goes back to Jira. One rule each: Chrome caps a
// rule's compiled regex at 2 KB, and all of them in one alternation followed by .* exceeds it.
export const PATHS = ['browse', 'issues', 'jira/(browse|software|core|your-work|for-you)', 'secure/RapidBoard\\.jspa', 'projects/[^/]+/issues'];

// The marker that sends a request to Jira untouched (a priority allow rule matches it).
// bypass is href with it, or null when href is no http(s) URL.
export const BYPASS = 'laneway=jira';
export function bypass(href) {
  const u = http(href);
  if (!u) return null;
  u.searchParams.set(...BYPASS.split('='));
  return u.toString();
}
