// Server-sent event streams shared by all tabs. A browser keeps 6 HTTP/1.1 connections per host; with a few
// streams per tab, two or three tabs left none for the API and every request hung. A shared worker
// (streamhub.js) holds one EventSource per stream; without one (Chrome on Android) each tab opens its own.
// stream(url) → { on(type, fn), close() }; fn gets { data, lastEventId }, 'error' and 'open' included.
let port = null;
const subs = new Map(); // path → Map<type, Set<fn>>

function hub() {
  if (port || typeof SharedWorker === 'undefined') return port;
  try {
    const w = new SharedWorker('/js/streamhub.js', { name: 'lw-streams' });
    port = w.port;
    port.onmessage = m => {
      const fns = subs.get(m.data.url) && subs.get(m.data.url).get(m.data.type);
      if (fns) for (const fn of fns) fn({ data: m.data.data, lastEventId: m.data.id });
    };
    port.start();
  } catch (e) { port = null; }
  return port;
}

export function stream(url) {
  const p = hub();
  if (!p) {
    const es = new EventSource(url);
    return { on: (type, fn) => es.addEventListener(type, fn), close: () => es.close() };
  }
  const path = url.split('?')[0];
  if (!subs.has(path)) subs.set(path, new Map());
  const types = subs.get(path);
  return {
    on(type, fn) {
      if (!types.has(type)) types.set(type, new Set());
      types.get(type).add(fn);
      p.postMessage({ op: 'on', url, type });
    },
    close() { subs.delete(path); p.postMessage({ op: 'off', url }); },
  };
}
