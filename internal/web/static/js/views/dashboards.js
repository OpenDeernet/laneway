// Dashboards: the site's dashboards and their gadgets, in the dashboard's columns. On laneway-server
// every gadget draws from what the server computes as you see it; on Jira the ones that list a
// filter's issues, the rest link to the dashboard there.
import { h, clear } from '../lib/dom.js';
import { css } from '../lib/css.js';
import { ago } from '../lib/fmt.js';
import { chart, niceTicks, timeTicks } from '../lib/charts.js';
import { T } from '../lib/i18n.js';

export default async function mount(el, { app, params }) {
  css('home'); css('dashboards');
  const { api } = app;
  const box = h('div.dbs');
  el.append(box);
  let list;
  try { list = await api.get('/dashboards'); } catch (e) { box.append(h('div.empty', h('h2', T('No dashboards')), h('p', e.message))); return; }
  if (!list.length) { box.append(h('div.empty', h('h2', T('No dashboards')), h('p', T('Make one on the site; it shows here.')))); return; }
  const db = list.find(d => d.ID === params.id) || list[0];
  const pick = h('select.input', { 'aria-label': T('Dashboard'), onchange: e => app.go('/dashboards/' + e.target.value) },
    list.map(d => h('option', { value: d.ID, selected: d.ID === db.ID }, (d.Favourite ? '★ ' : '') + d.Name)));
  box.append(h('div.db-head', h('h2', db.Name), pick, db.View ? h('a.btn.ghost', { href: db.View, target: '_blank', rel: 'noopener' }, T('Open on the site')) : null));
  let gadgets;
  try { gadgets = (await api.get('/dashboards/' + encodeURIComponent(db.ID))).gadgets || []; } catch (e) { box.append(h('p.err', e.message)); return; }
  if (!gadgets.length) { box.append(h('p.dim', T('This dashboard has no gadgets yet.'))); return; }
  const n = Math.max(1, ...gadgets.map(g => g.Col + 1));
  const cols = Array.from({ length: n }, () => h('div.db-col'));
  const grid = h('div.db-cols', { style: `--cols:${n}` }, cols);
  box.append(grid);
  const charts = [];
  for (const g of gadgets) {
    const body = h('div', h('div.hnote.dim', T('Loading…')));
    cols[g.Col].append(h('section.hw.db-g', h('div.hhead', h('span.ht', g.Title || g.Kind)), body));
    api.get(`/dashboards/${encodeURIComponent(db.ID)}/gadgets/${g.ID}`).then(d => clear(body).append(...draw(g, d, db, charts)))
      .catch(e => clear(body).append(h('div.hnote.err', e.message)));
  }
  return () => charts.forEach(c => c && c.destroy && c.destroy());

  function draw(g, d, db, charts) {
    const elsewhere = msg => [h('div.hnote.dim', msg, ' ', db.View ? h('a', { href: db.View, target: '_blank', rel: 'noopener' }, T('Open the dashboard')) : null)];
    if (d.elsewhere || d.unsupported) return elsewhere(T('This gadget shows on the site.'));
    if (d.configure) return elsewhere(T('This gadget isn’t set up yet: set it up on the site.'));
    if (d.text !== undefined) return [h('div.hnote', d.text)];
    if (d.issues) {
      const rows = d.issues.map(r => h('div.hrow', { onclick: () => app.panel.open(r.key), title: r.summary },
        h('span.hk', r.key), h('span.hs', r.summary), h('span.hn', r.status || ''), r.assignee ? h('span.hn', r.assignee) : null));
      return [rows.length ? h('div', rows) : h('div.hnote.dim', T('No issues.')), h('div.db-foot', T('%s of %s', rows.length, d.total))];
    }
    if (d.buckets) return [h('div', d.buckets.map(b => h('div.db-bucket', h('span', b.label),
      h('div.hbar', h('div.done', { style: `right:${100 - b.percent}%` })), h('span.hn', `${b.count} · ${b.percent}%`)))), h('div.db-foot', T('%s issues', d.total))];
    if (d.rows && d.columns) return [h('table.db-grid', h('tr', h('th', ''), d.columns.map(c => h('th', c.label)), h('th', T('Total'))),
      d.rows.map(r => h('tr', h('td', r.label), r.counts.map(c => h('td', c || '')), h('td', r.total))))];
    if (d.points && !d.series) {
      const pts = d.points.map(p => [Date.parse(p.start), p.created, p.resolved]);
      const top = niceTicks(Math.max(1, ...pts.map(p => Math.max(p[1], p[2]))));
      const wrap = h('div.db-chart');
      charts.push(chart(wrap, { title: g.Title, height: 180, x: { min: pts[0][0], max: pts[pts.length - 1][0], ticks: timeTicks(pts[0][0], pts[pts.length - 1][0], 5) },
        y: { max: top.max, ticks: top.ticks }, layers: [
          { type: 'line', pts: pts.map(p => [p[0], p[1]]), color: 'var(--err)', width: 2 },
          { type: 'line', pts: pts.map(p => [p[0], p[2]]), color: 'var(--ok)', width: 2 }],
        legend: [{ color: 'var(--err)', name: T('Created') }, { color: 'var(--ok)', name: T('Resolved') }] }));
      return [wrap];
    }
    if (d.items) return [h('div', d.items.map(a => h('div.hrow', { onclick: () => app.panel.open(a.issue) },
      h('span.hk', a.issue), h('span.hs', `${a.actor} · ${a.kind === 'comment' ? T('commented') : a.kind === 'create' ? T('created') : T('changed')} · ${a.summary}`), h('span.hn', ago(Date.parse(a.at))))))];
    if (d.projects) return [h('div', d.projects.map(p => h('div.hrow', h('span.hk', p.key), h('span.hs', p.name), h('span.hn', p.lead || ''))))];
    if (d.spacer) return [];
    // Starred filters and quick links: each search opens as a board view.
    if (d.filters) return d.filters.length ? [h('div', d.filters.map(f => h('div.hrow', { onclick: () => app.go('/board?' + new URLSearchParams({ sprint: 'jql:' + f.jql, vname: f.name })), title: f.jql },
      h('span.hs', f.name), h('span.hn', String(f.count)))))] : [h('div.hnote.dim', T('No starred filters.'))];
    if (d.versions) return d.versions.length ? [h('div', d.versions.map(v => h('div.db-bucket', h('span', `${v.project} ${v.name}`),
      h('div.hbar', h('div.done', { style: `right:${100 - v.percent}%` })), h('span.hn', `${v.done}/${v.issues} · ${v.releaseDate || ''}`))))]
      : [h('div.hnote.dim', T('No versions due in the next %s days.', d.days))];
    // Time in status, sprint health: a value per row.
    if (d.values) return [h('div', d.values.map(v => h('div.hrow', h('span.hs', v.label), h('span.hn', `${v.value}${v.unit ? (v.unit === '%' ? '%' : ' ' + v.unit) : d.unit ? ' ' + d.unit : ''}`))))];
    if (d.points && d.series) {
      if (!d.points.length) return [h('div.hnote.dim', T('No data yet.'))];
      const [k, name] = d.series[0];
      const pts = d.points.map(p => [Date.parse(p.start.replace(' ', 'T')), p[k]]);
      const top = niceTicks(Math.max(1, ...pts.map(p => p[1])));
      const wrap = h('div.db-chart');
      charts.push(chart(wrap, { title: g.Title, height: 180, x: { min: pts[0][0], max: pts[pts.length - 1][0], ticks: timeTicks(pts[0][0], pts[pts.length - 1][0], 5) },
        y: { max: top.max, ticks: top.ticks }, layers: [{ type: 'line', pts, color: 'var(--accent)', width: 2 }], legend: [{ color: 'var(--accent)', name }] }));
      return [wrap];
    }
    return elsewhere(T('This gadget shows on the site.'));
  }
}
