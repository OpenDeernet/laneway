// Inbox state shared by the inbox view and the nav badge.
import { $, h, openURL } from './dom.js';
import { css } from './css.js';
import { notify, enabled } from './notify.js';
import { T, Tn } from './i18n.js';
import { stream } from './streams.js';

export const latest = t => Date.parse(t.Entries[t.Entries.length - 1].When);

// Where a thread stands given its marks (Unix ms): unread, done until news, snoozed until then.
export function stateOf(t, mark, floor, now = Date.now()) {
  const mk = mark || {}, at = latest(t);
  return { at, unread: at > (mk.Read || floor), done: !!mk.Done && at <= mk.Done, snoozed: (mk.Snooze || 0) > now, till: mk.Snooze || 0 };
}
export function unreadCount(data, now = Date.now()) {
  let n = 0;
  for (const t of data.threads) { const s = stateOf(t, data.marks[t.ID], data.floor, now); if (s.unread && !s.done && !s.snoozed) n++; }
  return n;
}

export function setBadge(n) {
  if (navigator.setAppBadge) (n ? navigator.setAppBadge(n) : navigator.clearAppBadge()).catch(() => {}); // installed app, desktop app's dock
  const a = $('#nav a[data-name=inbox]'); if (!a) return;
  let b = a.querySelector('.nav-badge');
  if (!n) { if (b) b.remove(); return; }
  if (!b) { b = h('span.nav-badge'); a.append(b); }
  b.textContent = n > 99 ? '99+' : n;
  b.title = T('%d unread', n);
}

const openThread = (app, data, t) => (t.Site !== data.site ? openURL(t.URL) : app.panel.open(t.Key));

// As the TUI: one notification per mention newer than the last notified; those from before the page loaded stay quiet.
let mentionsSeen = Date.now();
function pingMentions(app, data) {
  const pinged = new Set();
  let newest = mentionsSeen;
  for (const t of data.threads) for (const e of t.Entries) {
    const at = Date.parse(e.When);
    if (!e.Mention || at <= mentionsSeen) continue;
    newest = Math.max(newest, at);
    pinged.add(t.ID);
    notify(T('%s mentioned you on %s', e.Who, t.Key), t.Summary, () => openThread(app, data, t), 'mention:' + t.ID + ':' + at);
  }
  mentionsSeen = newest;
  return pinged;
}

// Ping for the other threads that turned unread since the last look, while the tab is hidden or the window is behind another.
let seen = null;
function pingNew(app, data, skip, now = Date.now()) {
  const cur = new Map();
  for (const t of data.threads) { const s = stateOf(t, data.marks[t.ID], data.floor, now); if (s.unread && !s.done && !s.snoozed) cur.set(t.ID, s.at); }
  const fresh = seen && (document.hidden || !document.hasFocus()) ? [...cur].filter(([id, at]) => !skip.has(id) && (!seen.has(id) || seen.get(id) < at)) : [];
  seen = cur;
  if (!fresh.length || !enabled()) return;
  const t = data.threads.find(x => x.ID === fresh[0][0]), e = t.Entries[t.Entries.length - 1];
  const title = fresh.length === 1 ? t.Key + ' ' + t.Summary : Tn(fresh.length, '%d issue has news', '%d issues have news', fresh.length);
  notify(title, fresh.length === 1 ? (e.Who ? e.Who + ': ' : '') + (e.Body || e.What || T('updated')) : fresh.map(([id]) => id.slice(id.indexOf('/') + 1)).slice(0, 5).join(', '),
    () => (fresh.length === 1 ? openThread(app, data, t) : app.go('/inbox')), 'inbox');
}

// everyMs is ui.inbox_every ("5m", "off": 0), the TUI's five minutes when unset or unreadable.
export function everyMs(s) {
  s = String(s || '').trim().toLowerCase();
  if (s === 'off' || s === '0') return 0;
  const m = s.match(/^(\d+(?:\.\d+)?)(s|m|h)$/);
  return m ? Math.max(Number(m[1]) * { s: 1e3, m: 6e4, h: 36e5 }[m[2]], 5e3) : 3e5;
}

// Count unread threads now and every ui.inbox_every; while the tab is hidden only with notifications on.
export function installBadge(app) {
  css('inbox');
  let timer = 0;
  const poll = () => app.api.get('/inbox', { fresh: true }).then(d => { setBadge(unreadCount(d)); pingNew(app, d, pingMentions(app, d)); }).catch(() => {});
  const arm = () => {
    clearInterval(timer); timer = 0;
    const every = everyMs(app.session && app.session.ui && app.session.ui.InboxEvery);
    if (every && (!document.hidden || enabled())) timer = setInterval(poll, every);
  };
  document.addEventListener('visibilitychange', () => { arm(); if (!document.hidden) poll(); });
  arm(); poll();
  followSite(app, poll);
}

// followSite listens to the site's change stream (laneway-server; Jira has
// none and says so once): each change goes out on the bus as
// 'site:change' for the views, and a burst of them syncs the inbox once.
function followSite(app, poll) {
  if (!window.EventSource) return;
  let pending = 0;
  const es = stream('/api/site/events');
  es.on('none', () => es.close());
  es.on('change', e => {
    let ch;
    try { ch = JSON.parse(e.data); } catch { return; }
    app.bus.emit('site:change', ch);
    if ((ch.kind === 'issue' || ch.kind === 'comment') && !pending) pending = setTimeout(() => { pending = 0; poll(); }, 15000);
  });
}
