// Caches the app shell (this binary's files) so a reload is instant; never touches /api or /auth.
// The server fills in VERSION (a hash of the embedded files) and FILES: a new binary is a new cache.
const VERSION = '__VERSION__';
const FILES = __FILES__;
const CACHE = 'lw-' + VERSION;

self.addEventListener('install', e => {
  e.waitUntil(caches.open(CACHE).then(c => c.addAll(FILES.map(f => new Request(f, { cache: 'reload' })))));
});
// A new binary waits until the page asks ("Update ready"): one reload never mixes old and new modules.
self.addEventListener('message', e => { if (e.data === 'skip') self.skipWaiting(); });
self.addEventListener('activate', e => {
  e.waitUntil(caches.keys().then(ks => Promise.all(ks.filter(k => k.startsWith('lw-') && k !== CACHE).map(k => caches.delete(k)))).then(() => self.clients.claim()));
});
// Bundled fonts (/fonts/*.woff2) are cached the first time a page uses one; they never change under a name.
const FONTS = 'fonts-lw'; // not lw-*: activate prunes those
self.addEventListener('fetch', e => {
  const r = e.request, u = new URL(r.url);
  // /auth/ is laneway web -hosted's sign-in (and its way back): always the server's.
  if (r.method !== 'GET' || u.origin !== location.origin || u.pathname.startsWith('/api/') || u.pathname.startsWith('/auth/')) return;
  if (u.pathname.startsWith('/fonts/') && !u.pathname.startsWith('/fonts/custom/')) {
    e.respondWith(caches.open(FONTS).then(c => c.match(r).then(hit => hit || fetch(r).then(res => { if (res.ok) c.put(r, res.clone()); return res; }))));
    return;
  }
  // vendor/ (the terminal's xterm.js) is not precached: kept in this version's cache once a terminal opened.
  if (u.pathname.startsWith('/vendor/')) {
    e.respondWith(caches.open(CACHE).then(c => c.match(u.pathname).then(hit => hit || fetch(r).then(res => { if (res.ok) c.put(u.pathname, res.clone()); return res; }))));
    return;
  }
  const path = r.mode === 'navigate' || u.pathname === '/index.html' ? '/' : u.pathname;
  e.respondWith(caches.open(CACHE).then(c => c.match(path)).then(hit => hit || fetch(r)));
});
