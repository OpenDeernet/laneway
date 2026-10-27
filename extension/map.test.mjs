// node --test extension/*.test.mjs
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { route, toLaneway, parseHosts, bypass, PATHS } from './map.js';

const J = 'https://acme.atlassian.net';
const jqlView = (path, jql, vname, issue) => path + '?' + new URLSearchParams(Object.entries({ sprint: 'jql:' + jql, vname, issue }).filter(([, v]) => v));

test('route', () => {
  const cases = [
    ['/browse/ABC-12', '/issue/ABC-12'],
    ['/browse/ABC-12?focusedCommentId=1', '/issue/ABC-12?comment=1'],
    ['/browse/ABC-12?focusedCommentId=1&page=com.atlassian.jira.plugin.system.issuetabpanels:comment-tabpanel#comment-1', '/issue/ABC-12?comment=1'],
    ['/browse/ABC-12?focusedCommentId=x', '/issue/ABC-12'],
    ['/browse/ABC?focusedCommentId=1', '/board/ABC'],
    ['/browse/ABC', '/board/ABC'],
    ['/jira/browse/ABC-12', '/issue/ABC-12'],
    ['/projects/ABC/issues/ABC-3', '/issue/ABC-3'],
    ['/jira/software/projects/ABC/boards/12', '/board/ABC/12'],
    ['/jira/software/c/projects/ABC/boards/12?selectedIssue=ABC-4', '/board/ABC/12?issue=ABC-4'],
    ['/jira/software/projects/ABC/boards/12/', '/board/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/backlog', '/planning/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/backlog?selectedIssue=ABC-4', '/issue/ABC-4'],
    ['/jira/software/projects/ABC/boards/12/reports/velocity-chart', '/reports/velocity/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/reports/burndown-chart?sprint=7', '/reports/burndown/ABC/12?sprint=7'],
    ['/jira/software/projects/ABC/boards/12/reports/cumulative', '/reports/cfd/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/reports/control-chart', '/reports/cycle/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/reports/sprint-retrospective', '/reports/retro/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/reports', '/reports/burndown/ABC/12'],
    ['/jira/software/projects/ABC/boards/12/timeline', '/roadmap/ABC'],
    ['/jira/software/projects/ABC/boards/12/code', '/board/ABC/12'],
    ['/jira/software/projects/ABC', '/board/ABC'],
    ['/jira/software/projects/ABC/summary', '/board/ABC'],
    ['/jira/core/projects/ABC/board', '/board/ABC'],
    ['/jira/software/projects/ABC/backlog', '/planning/ABC'],
    ['/jira/software/projects/ABC/timeline', '/roadmap/ABC'],
    ['/jira/software/projects/ABC/releases', '/reports/releases/ABC'],
    ['/jira/software/projects/ABC/issues/ABC-9', '/issue/ABC-9'],
    ['/jira/software/projects/ABC/pages', null],
    ['/jira/servicedesk/projects/ABC/queues/custom/1', null],
    ['/jira/your-work', '/work'],
    ['/jira/for-you', '/work'],
    ['/jira/dashboards/10000', null],
    ['/wiki/spaces/X', null],
    ['/secure/RapidBoard.jspa?rapidView=12&projectKey=ABC', '/board/ABC/12'],
    ['/secure/RapidBoard.jspa?rapidView=12&projectKey=ABC&view=planning.nodetail', '/planning/ABC/12'],
    ['/secure/RapidBoard.jspa?rapidView=12&projectKey=ABC&view=reporting&chart=velocityChart', '/reports/velocity/ABC/12'],
    ['/secure/RapidBoard.jspa?rapidView=12&selectedIssue=ABC-1', '/issue/ABC-1'],
    ['/secure/RapidBoard.jspa?rapidView=12', '/board'],
    // queries
    ['/issues/?jql=' + encodeURIComponent('assignee = currentUser()'), jqlView('/board', 'assignee = currentUser()', 'JQL')],
    ['/issues?jql=status%3DDone', jqlView('/board', 'status=Done', 'JQL')],
    ['/issues/ABC-5?jql=status%3DDone', jqlView('/board', 'status=Done', 'JQL', 'ABC-5')],
    ['/issues/?jql=status%3DDone&selectedIssue=ABC-5', jqlView('/board', 'status=Done', 'JQL', 'ABC-5')],
    ['/issues/?filter=10042', jqlView('/board', 'filter = 10042', 'Filter 10042')],
    ['/issues/?filter=10042&jql=status%3DDone', jqlView('/board', 'status=Done', 'JQL')],
    ['/issues/?filter=-1', jqlView('/board', 'assignee = currentUser() AND resolution = Unresolved ORDER BY updated DESC', 'My open issues')],
    ['/issues/?filter=-4', null],
    ['/issues/ABC-5', '/issue/ABC-5'],
    ['/issues/', null],
    ['/jira/software/projects/ABC/issues?jql=' + encodeURIComponent('status = Done ORDER BY rank'), jqlView('/board/ABC', 'project = ABC AND (status = Done) ORDER BY rank', 'JQL')],
    ['/jira/software/projects/ABC/issues/?filter=allissues', jqlView('/board/ABC', 'project = ABC ORDER BY created DESC', 'All issues')],
    ['/jira/software/projects/ABC/issues', jqlView('/board/ABC', 'project = ABC ORDER BY created DESC', 'ABC')],
    ['/jira/software/projects/ABC/issues/ABC-2?jql=status%3DDone', jqlView('/board/ABC', 'project = ABC AND (status=Done)', 'JQL', 'ABC-2')],
  ];
  for (const [path, want] of cases) assert.equal(route(J + path), want, path);
});

test('toLaneway', () => {
  assert.equal(toLaneway(J + '/browse/ABC-1', 'http://127.0.0.1:8484/'), 'http://127.0.0.1:8484/#/issue/ABC-1');
  assert.equal(toLaneway(J + '/wiki', 'http://127.0.0.1:8484'), null);
  assert.equal(toLaneway('not a url', 'http://x'), null);
});

test('parseHosts', () => {
  assert.deepEqual(parseHosts('a.atlassian.net = jira\n# note\n B.atlassian.net=team # mine\nc.atlassian.net\n'), [
    { host: 'a.atlassian.net', site: '' }, { host: 'b.atlassian.net', site: 'team' }, { host: 'c.atlassian.net', site: null },
  ]);
});

test('bypass', () => {
  assert.equal(bypass(J + '/browse/ABC-1'), J + '/browse/ABC-1?laneway=jira');
  assert.equal(bypass(J + '/issues/?jql=a'), J + '/issues/?jql=a&laneway=jira');
  for (const s of ['javascript:fetch(1)', 'data:text/html,x', 'file:///etc/passwd', '//evil.com/x', '']) {
    assert.equal(bypass(s), null, s);
    assert.equal(route(s), null, s);
  }
  assert.equal(route('javascript:/browse/ABC-1'), null);
});

test('PATHS lets other pages through', () => {
  const re = new RegExp('^https?://[^/]+/(' + PATHS.join('|') + ')');
  for (const p of ['/browse/A-1', '/jira/browse/A-1', '/issues/?jql=x', '/issues?jql=x', '/jira/software/c/projects/A/boards/1', '/jira/your-work', '/projects/A/issues/A-1', '/secure/RapidBoard.jspa?rapidView=1']) assert.ok(re.test(J + p), p);
  for (const p of ['/wiki/spaces/A', '/jira/dashboards', '/jira/people/x', '/']) assert.ok(!re.test(J + p), p);
});
