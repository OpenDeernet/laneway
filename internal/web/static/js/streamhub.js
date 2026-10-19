// Shared worker behind lib/streams.js: one EventSource per stream for every tab of this origin.
// Messages from a tab: { op: 'on', url, type } and { op: 'off', url }; to a tab: { url, type, data, id }.
// A stream is keyed by its path, so a later tab's ?since= joins the open one.
const streams = new Map(); // path → { es, types: Set, ports: Map<port, Set<type>> }

const key = url => url.split('?')[0];

function on(port, url, type) {
  const k = key(url);
  let s = streams.get(k);
  if (!s) { s = { es: new EventSource(url), types: new Set(), ports: new Map() }; streams.set(k, s); }
  if (!s.types.has(type)) {
    s.types.add(type);
    s.es.addEventListener(type, ev => {
      for (const [p, ts] of s.ports) if (ts.has(type)) p.postMessage({ url: k, type, data: ev.data, id: ev.lastEventId });
    });
  }
  if (!s.ports.has(port)) s.ports.set(port, new Set());
  s.ports.get(port).add(type);
}

function off(port, url) {
  const k = key(url), s = streams.get(k);
  if (!s) return;
  s.ports.delete(port);
  if (!s.ports.size) { s.es.close(); streams.delete(k); }
}

onconnect = e => {
  const port = e.ports[0];
  port.onmessage = m => (m.data.op === 'on' ? on : off)(port, m.data.url, m.data.type);
};
