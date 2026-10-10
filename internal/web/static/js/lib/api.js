// JSON API client. GETs are de-duplicated while in flight; `swr` answers from
// a persisted cache at once and again when fresh data arrives.
const inflight = new Map();
const mem = new Map();
let site = '';
export const setSite = s => { site = s; };

// fields: Jira's refusal reasons by field id, when it gave them; signin: how to sign in to GitLab, when it has no
// token for a merge request's host (gitlab.SignIn).
export class ApiError extends Error { constructor(msg, status, fields, signin) { super(msg); this.status = status; this.fields = fields || null; this.signin = signin || null; } }

// Go sends a nil slice as null; these fields are always lists. Keep in step
// with sliceKeys in internal/web/server.go.
const LISTS = new Set(['Links', 'Attachments', 'Comments', 'Labels', 'Watches', 'Watchers', 'Transitions', 'Worklogs',
  'Sprints', 'Issues', 'Columns', 'Statuses', 'Subtasks', 'Kids', 'Mentions', 'Threads', 'Histories',
  'FixVersions', 'Components', 'Warnings', 'Options', 'AllowedValues', 'Entries', 'Results', 'Changes',
  'Activity', 'Agents', 'Views', 'Groups', 'Filters', 'QuickFilters', 'Branches', 'Commits',
  'PullRequests', 'Builds', 'Deployments', 'BlockedBy', 'References', 'Choices', 'Moves', 'Items', 'Rules', 'Replies']);
export function lists(v) {
  if (Array.isArray(v)) { for (const x of v) if (x && typeof x === 'object') lists(x); }
  else if (v && typeof v === 'object') {
    for (const k in v) {
      const x = v[k];
      if (x === null) { if (LISTS.has(k)) v[k] = []; } else if (typeof x === 'object') lists(x);
    }
  }
  return v;
}

// keepalive lets a write outlive the page (a draft sent as it unloads; at most 64KB).
async function call(method, path, body, signal, keepalive = false) {
  const res = await fetch('/api' + path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
    keepalive,
  });
  let data = null;
  try { data = lists(await res.json()); } catch (e) { /* empty body */ }
  // A laneway web shared by many people: signed out (the page came from the cache), sign in and come back.
  if (res.status === 401 && data && data.signedOut === '/auth/login') location.assign('/auth/login?next=' + encodeURIComponent(location.pathname + location.search));
  if (!res.ok) throw new ApiError((data && data.error) || res.statusText, res.status, data && data.fields, data && data.signin);
  if (res.status === 202) window.dispatchEvent(new CustomEvent('lw:queued')); // a write kept for when Jira is back
  return data;
}

// `signal` (an AbortSignal) cancels the request; such a call is never shared.
export function get(path, { fresh = false, signal } = {}) {
  if (signal) return call('GET', path, undefined, signal);
  if (!fresh && inflight.has(path)) return inflight.get(path);
  const p = call('GET', path).then(d => { mem.set(path, d); return d; }).finally(() => inflight.delete(path));
  inflight.set(path, p);
  return p;
}
// put and del take { keepalive } (call's).
export const post = (path, body = {}) => write('POST', path, body);
export const put = (path, body = {}, { keepalive } = {}) => write('PUT', path, body, keepalive);
export const del = (path, body, { keepalive } = {}) => write('DELETE', path, body, keepalive);
function write(method, path, body, keepalive) {
  // Any write may change what cached reads say.
  for (const k of mem.keys()) mem.delete(k);
  return call(method, path, body, undefined, keepalive);
}

const lsKey = p => 'lw:c:' + site + ':' + p;
function readLS(path) { try { const v = localStorage.getItem(lsKey(path)); return v ? lists(JSON.parse(v)) : undefined; } catch (e) { return undefined; } }
function writeLS(path, data) {
  try { const s = JSON.stringify(data); if (s.length < 600000) localStorage.setItem(lsKey(path), s); } catch (e) { /* quota */ }
}

// swr(path, onData): onData(data, {cached}) runs with the stored copy first (if
// any), then with the fresh one. Resolves with the fresh data; rejects if the
// fetch fails (after a cached answer the caller may keep showing it).
export function swr(path, onData, { persist = true } = {}) {
  const hit = mem.has(path) ? mem.get(path) : persist ? readLS(path) : undefined;
  if (hit !== undefined) onData(hit, { cached: true });
  return get(path, { fresh: hit !== undefined }).then(d => { if (persist) writeLS(path, d); onData(d, { cached: false }); return d; });
}
// warm(path) loads path into the in-memory cache unless it is there or on its way, for a quick swr later.
export function warm(path) { if (!mem.has(path) && !inflight.has(path)) get(path).catch(() => {}); }
export function forget() { mem.clear(); try { for (const k of Object.keys(localStorage)) if (k.startsWith('lw:c:')) localStorage.removeItem(k); } catch (e) { /* ignore */ } }
export const api = { get, post, put, del, swr, warm, forget, setSite };
export default api;
