// Development in the issue's Details tab (TUI: D): pull requests, deployments, builds, branches and
// commits from Jira's dev-status (GET /api/issues/{key}/dev, jira.DevItem). Hidden when there is none.
// mountDev(key, {app, el, full, card, details}) → {el, refresh, dispose}; `card()` is the issue's card
// (its PR/Deploy say work exists before the list arrives), `details()` shows the Details tab.
// A GitLab merge request's row unfolds it (enter, a click): state, pipeline by stage, approvals and
// description from GET /api/gitlab/mr (forge.Change), as the TUI's panel shows it; o opens any row's link.
import { h, clear, safe, openURL } from '../lib/dom.js';
import { mrBody, diffHref, mrButtons, signInHelp } from '../lib/mr.js';
import { icon } from '../lib/icons.js';
import { css } from '../lib/css.js';
import { ago, dateTime, isZero, duration } from '../lib/fmt.js';
import { T, Tn } from '../lib/i18n.js';

const COMMITS = 3;             // commits per repository while folded
const PR = { OPEN: ['open', T('Open'), 'git-pull-request'], MERGED: ['merged', T('Merged'), 'git-merge'], DECLINED: ['declined', T('Declined'), 'git-pull-request-closed'], DRAFT: ['draft', T('Draft'), 'git-pull-request-draft'] };
const PRWORD = { OPEN: T('open'), MERGED: T('merged'), DECLINED: T('declined'), DRAFT: T('draft') };
// Build and deployment states → a tone and a word.
const RUN = {
  SUCCESSFUL: ['ok', T('passed')], FAILED: ['err', T('failed')], IN_PROGRESS: ['run', T('running')], PENDING: ['run', T('pending')],
  CANCELLED: ['warn', T('cancelled')], ROLLED_BACK: ['warn', T('rolled back')], UNKNOWN: ['none', T('unknown')],
};
const ENV_RANK = { production: 0, staging: 1, testing: 2, development: 3 };
const run = s => RUN[s] || RUN.UNKNOWN;
const when = t => (isZero(t) ? null : h('time.dv-when', { datetime: t, title: dateTime(t) }, ago(t)));
const prNo = u => { const m = /\/(?:pull|pull-requests|merge_requests)\/(\d+)/.exec(u || ''); return m ? (/merge_requests/.test(u) ? '!' : '#') + m[1] : ''; };
const isMR = u => /\/-\/merge_requests\/\d+/.test(u || '');
const newest = (a, b) => (isZero(b.Updated) ? -1 : isZero(a.Updated) ? 1 : new Date(b.Updated) - new Date(a.Updated));

// The header's one-liner, as the board card's chips: "1 PR merged · 2 branches · 5 commits · deployed to production".
export function summary(items) {
  const by = k => items.filter(d => d.Kind === k), parts = [];
  const prs = by('pr');
  if (prs.length) {
    const n = s => prs.filter(p => p.Status === s).length;
    const st = ['OPEN', 'MERGED', 'DECLINED', 'DRAFT'].filter(n);
    parts.push([(st.length === 1 ? Tn(prs.length, '%d PR %s', '%d PRs %s', prs.length, PRWORD[st[0]]) : Tn(prs.length, '%d PR: %s', '%d PRs: %s', prs.length, st.map(s => n(s) + ' ' + PRWORD[s]).join(', '))), 'pr-' + (st[0] || '').toLowerCase()]);
  }
  const br = by('branch').length;
  if (br) parts.push([Tn(br, '%d branch', '%d branches', br), '']);
  const cm = by('commit').length;
  if (cm) parts.push([Tn(cm, '%d commit', '%d commits', cm), '']);
  const builds = by('build');
  if (builds.length) {
    const has = s => builds.some(b => b.Status === s);
    parts.push(has('FAILED') ? [T('build failing'), 'dvt-err'] : has('IN_PROGRESS') || has('PENDING') ? [T('build running'), 'dvt-run'] : has('SUCCESSFUL') ? [T('build passing'), 'dvt-ok'] : [T('build %s', run(builds[0].Status)[1]), '']);
  }
  const envs = topEnvs(by('deploy'));
  const live = envs.find(d => d.Status === 'SUCCESSFUL');
  if (live) parts.push([T('deployed to %s', live.Branch), 'dvt-ok']);
  else if (envs.length) parts.push([T('deploy %s to %s', run(envs[0].Status)[1], envs[0].Branch), 'dvt-' + run(envs[0].Status)[0]]);
  return parts;
}

// The newest deployment per environment, production first.
function topEnvs(deploys) {
  const m = new Map();
  for (const d of deploys.slice().sort(newest)) if (!m.has(d.Branch)) m.set(d.Branch, d);
  const rank = d => ENV_RANK[(d.EnvType || '').toLowerCase()] ?? 4;
  return [...m.values()].sort((a, b) => rank(a) - rank(b));
}

export function mountDev(key, { app, el, full, card, details }) {
  css('dev');
  const { api, ui } = app;
  const PREF = 'issue.devFold';
  let items = null, dead = false, err = null, all = false, folded = !!app.prefs.get(PREF, false);
  // Unfolded merge requests by link, and each one's read: {data} | {err} | {} while loading.
  const openMR = new Set(), mrs = new Map();
  const id = 'dv-' + key.replace(/\W/g, '');
  const root = h('section.dv', { hidden: true, 'aria-labelledby': id + '-h' });
  const scope = app.keys.scope('issue-dev', { layer: 2 });
  const inPanel = () => full || app.panel.focused();
  const onRow = () => inPanel() && !!rowOf(document.activeElement);
  const rowOf = n => n && n.closest && root.contains(n) ? n.closest('[data-row]') : null;
  const hinted = () => { const c = card && card(); return !!(c && (c.PR || c.Deploy)); };
  const avatar = (name, url, size = 18) => ui.avatar(name, url, size);

  async function copy(text, what) {
    try { await navigator.clipboard.writeText(text); ui.toast(T('%s copied', what)); } catch (e) { ui.toast(T('Could not copy: %s', text), { kind: 'err' }); }
  }

  // ---- rows: every one is focusable (j/k), opens its link (enter, click) and copies (y)
  const row = (tag, d, copyText, copyWhat, ...kids) => h(tag + '.dv-row', {
    tabindex: -1, dataset: { row: '', url: safe(d.URL), copy: copyText || '', what: copyWhat || '' },
    title: safe(d.URL) ? (copyText ? T('Enter opens, y copies the %s', copyWhat.toLowerCase()) : T('Enter opens')) : '',
  }, ...kids);
  const title = (d, text) => (safe(d.URL) ? h('a.dv-title.clip', { href: safe(d.URL), target: '_blank', rel: 'noopener noreferrer', tabindex: -1 }, text) : h('span.dv-title.clip', text));
  const dot = (status, label) => { const [tone, word] = run(status); return h('span.dv-dot.dvt-' + tone, { role: 'img', 'aria-label': (label || T('build')) + ' ' + word, title: (label || T('Build')) + ' ' + word }); };
  const mono = (t, cls = '') => h('code.dv-mono' + cls, t);
  const sep = () => h('span.dv-sep', { 'aria-hidden': 'true' }, '·');
  const group = (label, n, ...kids) => h('div.dv-group', { role: 'group', 'aria-label': label },
    h('div.dv-gh', h('span', label), n > 1 ? h('span.dv-n', String(n)) : null), ...kids);

  function prRow(p, builds) {
    const [cls, text, ic] = PR[p.Status] || [String(p.Status || '').toLowerCase(), p.Status || '?'];
    const ci = builds.filter(b => b.Branch && b.Branch === p.Source).sort(newest)[0];
    const revs = p.Reviewers || [];
    const ok = revs.filter(r => r.Approved).length;
    // A laneway web for many people reads no GitLab: a merge request is a link there.
    const mr = isMR(p.URL) && !(app.session && app.session.hosted), open = mr && openMR.has(p.URL);
    const li = row('li', p, p.Source, T('Branch'),
      h('div.dv-line',
        mr ? h('span.dv-caret', { 'aria-hidden': 'true' }, icon('chevron-right')) : null,
        h('span.dv-badge.pr-' + cls, ic && icon(ic), ic ? ' ' + text : text),
        mr ? h('span.dv-title.clip', p.Name) : title(p, p.Name),
        ci ? dot(ci.Status, 'CI') : null,
        when(p.Updated),
        mr ? mrButtons(p.URL) : null),
      h('div.dv-meta',
        p.Repo && h('span', p.Repo), prNo(p.URL) && h('span.dv-mono', prNo(p.URL)),
        p.Source && h('span.dv-ref', mono(p.Source), h('span.dv-arrow', { 'aria-label': T('into') }, '→'), mono(p.Target)),
        p.Author && h('span.dv-who', avatar(p.Author, p.AuthorAvatar, 16), p.Author),
        revs.length ? h('span.dv-revs', { title: revs.map(r => r.Approved ? T('%s approved', r.Name) : T('%s not yet approved', r.Name)).join('\n'), 'aria-label': T('%d of %d reviewers approved', ok, revs.length) },
          revs.map(r => h('span.dv-rev' + (r.Approved ? '.ok' : ''), avatar(r.Name, r.Avatar, 16), r.Approved ? h('i.dv-tick', icon('check')) : null)),
          h('span.dv-dim', ok + '/' + revs.length)) : null,
        p.Comments ? h('span.dv-dim', { title: Tn(p.Comments, '%d comment', '%d comments', p.Comments) }, icon('message-square'), ' ' + p.Comments) : null),
      open ? mrDetail(p.URL) : null);
    if (mr) { li.dataset.mr = ''; li.setAttribute('aria-expanded', String(open)); li.title = T('Enter unfolds it, o opens it in GitLab'); }
    return li;
  }
  function mrDetail(u) {
    const st = mrs.get(u) || {};
    if (st.err && st.err.signin) return h('div.dv-mr', signInHelp(st.err));
    if (st.err) return h('div.dv-mr.dv-err', st.err.message || String(st.err), ' ', h('a', { href: safe(u), target: '_blank', rel: 'noopener noreferrer' }, T('Open in GitLab')));
    if (!st.data) return h('div.dv-mr.dv-dim', T('Loading the merge request…'));
    return mrBody(st.data, ui, u => app.go(u));
  }
  function toggleMR(u, fresh) {
    if (openMR.has(u) && !fresh) { openMR.delete(u); paint(); return; }
    openMR.add(u);
    if (fresh || !mrs.has(u)) {
      mrs.set(u, {});
      api.get('/gitlab/mr?url=' + encodeURIComponent(u) + (fresh ? '&fresh=1' : ''), { fresh: true })
        .then(data => { mrs.set(u, { data }); if (!dead) repaintKeep(); }, e => { mrs.set(u, { err: e }); if (!dead) repaintKeep(); });
    }
    repaintKeep();
  }
  // paint, keeping the focused row focused.
  function repaintKeep() {
    const u = (rowOf(document.activeElement) || {}).dataset?.url;
    paint();
    if (u) focusRow(rows().find(r => r.dataset.url === u));
  }
  function envChip(d) {
    const [tone, word] = run(d.Status);
    return h('a.dv-env.dvt-' + tone, {
      href: safe(d.URL) || null, target: '_blank', rel: 'noopener noreferrer', tabindex: -1, dataset: { row: '', url: safe(d.URL), copy: d.Branch, what: T('Environment') },
      title: [d.Name, word, d.Duration ? duration(d.Duration) : '', isZero(d.Updated) ? '' : dateTime(d.Updated)].filter(Boolean).join(' · '),
    }, h('span.dv-dot.dvt-' + tone, { 'aria-hidden': 'true' }), h('b', d.Branch || '?'), h('span.dv-dim', d.Status === 'SUCCESSFUL' ? '' : word), when(d.Updated));
  }
  function buildRow(b) {
    const [, word] = run(b.Status), t = b.Tests;
    return row('li', b, b.Branch, T('Branch'),
      h('div.dv-line', dot(b.Status), h('span.dv-state.dvt-' + run(b.Status)[0], word), title(b, b.Name), when(b.Updated)),
      (b.Branch || (t && t.Total)) ? h('div.dv-meta',
        b.Branch && mono(b.Branch),
        t && t.Total ? h('span.dv-tests', T('%d passed', t.Passed), t.Failed ? h('span.dvt-err', ' · ' + T('%d failed', t.Failed)) : null, t.Skipped ? ' · ' + T('%d skipped', t.Skipped) : null) : null) : null);
  }
  function branchRow(b, prs) {
    const pr = prs.find(p => p.Source === b.Name);
    const [cls, text, ic] = pr ? (PR[pr.Status] || ['', pr.Status]) : [];
    return row('li', b, b.Name, T('Branch'),
      h('div.dv-line', h('span.dv-glyph', icon('git-branch')), safe(b.URL) ? h('a.dv-title.clip.dv-mono', { href: safe(b.URL), target: '_blank', rel: 'noopener noreferrer', tabindex: -1 }, b.Name) : mono(b.Name, '.dv-title.clip'),
        pr ? h('span.dv-badge.sm.pr-' + cls, { title: pr.Name }, ic && icon(ic), (ic ? ' ' : '') + text + (prNo(pr.URL) ? ' ' + prNo(pr.URL) : ''))
          : safe(b.CreatePR) ? h('a.dv-create', { href: safe(b.CreatePR), target: '_blank', rel: 'noopener noreferrer', tabindex: -1 }, T('Create PR') + ' ', icon('external-link')) : null,
        when(b.Updated)),
      h('div.dv-meta', b.Repo && h('span', b.Repo),
        b.ShortHash && h('span.dv-last', mono(b.ShortHash), h('span.clip', b.Message || ''), b.Author ? h('span.dv-dim', b.Author) : null)));
  }
  function commitRow(c) {
    return row('li', c, c.Hash || c.ShortHash, T('Hash'),
      h('div.dv-line.dv-commit', mono((c.ShortHash || c.Hash || '').slice(0, 8), '.dv-hash'), title(c, c.Message || c.Name),
        c.Author && avatar(c.Author, c.AuthorAvatar, 16), c.Author && h('span.dv-dim.dv-author', c.Author), when(c.Updated)));
  }

  function paint() {
    const list = items || [];
    if (!list.length && !(items == null && hinted()) && !(err && hinted())) { root.hidden = true; clear(root); return; }
    root.hidden = false;
    const parts = items ? summary(list) : [];
    const toggle = h('button.dv-toggle', { 'aria-expanded': String(!folded), 'aria-controls': id + '-b', title: folded ? T('Unfold development') : T('Fold development'), onclick: () => fold(!folded) },
      h('span.dv-caret', icon('chevron-right')), h('h3', { id: id + '-h' }, T('Development')));
    const sum = h('span.dv-sum.clip', parts.map(([t, c], i) => [i ? sep() : null, h('span' + (c ? '.' + c : ''), t)]));
    clear(root).append(h('div.sec-head.dv-head', toggle, sum, h('span.spacer'), h('kbd', { title: T('Focus development (D)') }, 'D')));
    const body = h('div.dv-body', { id: id + '-b', hidden: folded });
    root.append(body);
    if (folded) return;
    if (items == null && err) {
      body.append(h('div.dv-err', { role: 'alert' }, T('Could not load development info: %s', err.message || err), ' ', h('button.btn.ghost.sm', { onclick: () => load(true) }, T('Retry'))));
      return;
    }
    if (items == null) { body.append(skeleton()); return; }
    const by = k => list.filter(d => d.Kind === k);
    const prs = by('pr'), builds = by('build'), envs = topEnvs(by('deploy')), branches = by('branch'), commits = by('commit');
    if (envs.length) body.append(h('div.dv-envs', { role: 'group', 'aria-label': T('Deployments') }, envs.map(envChip)));
    if (prs.length) body.append(group(T('Pull requests'), prs.length, h('ul.dv-list', prs.map(p => prRow(p, builds)))));
    const shownBuilds = all ? builds : latestPerRef(builds);
    if (builds.length) body.append(group(T('Builds'), builds.length, h('ul.dv-list', shownBuilds.map(buildRow)), more(builds.length - shownBuilds.length, 'build')));
    if (branches.length) body.append(group(T('Branches'), branches.length, h('ul.dv-list', branches.map(b => branchRow(b, prs)))));
    if (commits.length) {
      const repos = new Map();
      for (const c of commits) { if (!repos.has(c.Repo)) repos.set(c.Repo, []); repos.get(c.Repo).push(c); }
      let hidden = 0;
      const lists = [...repos].map(([repo, cs]) => {
        cs = cs.slice().sort(newest);
        const shown = all ? cs : cs.slice(0, COMMITS); hidden += cs.length - shown.length;
        return [repos.size > 1 ? h('div.dv-repo', repo || T('Repository'), h('span.dv-n', String(cs.length))) : null, h('ul.dv-list', shown.map(commitRow))];
      });
      body.append(group(T('Commits'), commits.length, lists, more(hidden, 'commit')));
    }
  }
  const more = (n, what) => (n > 0 ? h('button.btn.ghost.sm.dv-more', { onclick: () => { all = true; paint(); } }, (what === 'build' ? Tn(n, 'Show %d more build', 'Show %d more builds', n) : Tn(n, 'Show %d more commit', 'Show %d more commits', n))) : null);
  const latestPerRef = builds => { const m = new Map(); for (const b of builds.slice().sort(newest)) if (!m.has(b.Branch + '|' + b.Tool)) m.set(b.Branch + '|' + b.Tool, b); return [...m.values()]; };
  const skeleton = () => h('div.dv-skel', { 'aria-busy': 'true', 'aria-label': T('Loading development info') },
    [70, 55, 62].map(w => h('div.dv-skel-row', h('i.b'), h('i', { style: { width: w + '%' } }))));

  function fold(v) {
    folded = v; app.prefs.set(PREF, v ? 1 : '');
    paint();
  }

  let gen = 0;
  function load(fresh) {
    const g = ++gen, path = '/issues/' + key + '/dev';
    err = null;
    if (fresh) { items = null; paint(); }
    const got = d => { if (dead || g !== gen) return; items = d || []; paint(); };
    (fresh ? api.get(path, { fresh: true }).then(got) : api.swr(path, got))
      .catch(e => { if (dead || g !== gen || items) return; err = e; paint(); });
  }

  // ---- keys
  const rows = () => [...root.querySelectorAll('[data-row]')];
  const focusRow = n => { if (n) { n.focus({ preventScroll: true }); n.scrollIntoView({ block: 'nearest' }); } };
  const step = d => { const rs = rows(), i = rs.indexOf(rowOf(document.activeElement)); focusRow(rs[Math.max(0, Math.min(rs.length - 1, i + d))]); };
  function focusDev() {
    if (details) details();
    if (!items || !items.length) {
      if (items == null && !err) { load(); return ui.toast(T('Loading development info…')); }
      return ui.toast(err ? T('Could not load development info') : T('No development work linked to %s', key));
    }
    folded = false; all = true; paint();
    root.scrollIntoView({ block: 'start', behavior: 'smooth' });
    focusRow(rows()[0]);
  }
  const G = T('Development');
  scope.bind('D', focusDev, T('development: pull requests, builds, deploys, branches, commits'), { group: T('Issue'), when: inPanel });
  scope.bind(['j', 'ArrowDown'], () => step(1), T('next row'), { group: G, when: onRow });
  scope.bind(['k', 'ArrowUp'], () => step(-1), T('previous row'), { group: G, when: onRow });
  const openLink = r => { if (r && r.dataset.url) openURL(r.dataset.url); else ui.toast(T('No link')); };
  scope.bind('Enter', () => { const r = rowOf(document.activeElement); if (r && 'mr' in r.dataset) toggleMR(r.dataset.url); else openLink(r); }, T('open in the browser; a GitLab merge request unfolds'), { group: G, when: onRow });
  scope.bind('o', () => openLink(rowOf(document.activeElement)), T('open in the browser'), { group: G, when: onRow });
  scope.bind('d', () => { const r = rowOf(document.activeElement); if (r && 'mr' in r.dataset) app.go(diffHref(r.dataset.url)); else ui.toast(T('Not a GitLab merge request')); }, T('a merge request\'s diff'), { group: G, when: onRow });
  scope.bind('r', () => { const r = rowOf(document.activeElement); if (r && 'mr' in r.dataset && openMR.has(r.dataset.url)) toggleMR(r.dataset.url, true); else load(true); }, T('reload'), { group: G, when: onRow });
  scope.bind('y', () => { const r = rowOf(document.activeElement); if (r && r.dataset.copy) copy(r.dataset.copy, r.dataset.what); else ui.toast(T('Nothing to copy')); }, T('copy branch or hash'), { group: G, when: onRow });
  scope.bind('Escape', () => { const s = el.querySelector('.iss-scroll'); if (s) s.focus({ preventScroll: true }); else document.activeElement.blur(); }, T('leave development'), { group: G, when: onRow });

  // Mouse: a click on a row (not on its own link) does what enter does.
  root.addEventListener('click', e => {
    const r = e.target.closest('[data-row]');
    if (!r || e.target.closest('a,button') || !r.dataset.url || (window.getSelection && String(window.getSelection()))) return;
    if ('mr' in r.dataset) { r.focus({ preventScroll: true }); toggleMR(r.dataset.url); } else openURL(r.dataset.url);
  });

  load();
  return {
    el: root,
    refresh: () => load(true),
    hint: () => { if (items == null) paint(); },
    dispose: () => { dead = true; scope.dispose(); },
  };
}
