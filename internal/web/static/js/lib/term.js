// The browser terminal: an agent pane's terminal over /api/agents/{pane}/terminal (api_terminal.go runs
// `herdr agent attach` on a pty for it). xterm.js (vendor/xterm) loads on first use only.
//
//   const t = await terminal(host, {app, onState, onLeave});   t.attach(pane, {takeover}) · t.focus() · t.fit() · t.dispose()
//   onState({state, text, typing})   state: connecting | open | retry | exited | taken | idle | closed | gone
//
// While it has focus a `terminal` key scope (modal) sits on top of everything: every key goes to the terminal
// except LEAVE (ctrl+\, the TUI's agent_back), which calls onLeave. ctrl+shift+c copies the selection;
// shift+enter sends ESC CR (alt+enter), a newline in Claude Code.
import fonts from './fonts.js';
import { T } from './i18n.js';

export const LEAVE = 'ctrl+\\';
const V = '/vendor/xterm/';
const CHUNK = 32 << 10; // the server takes frames up to 64 KiB
let lib = null;

function load() {
  if (!lib) {
    const link = document.createElement('link');
    link.rel = 'stylesheet'; link.href = V + 'xterm.css';
    document.head.append(link);
    lib = Promise.all([import(V + 'xterm.js'), import(V + 'addon-fit.js'), import(V + 'addon-unicode11.js'), import(V + 'addon-web-links.js')])
      .then(([x, f, u, l]) => ({ Terminal: x.Terminal, FitAddon: f.FitAddon, Unicode11Addon: u.Unicode11Addon, WebLinksAddon: l.WebLinksAddon }))
      .catch(e => { lib = null; link.remove(); throw e; });
  }
  return lib;
}

// ---- colours: the theme's tokens, resolved through a canvas so color-mix() and friends arrive as rgb
const px = document.createElement('canvas').getContext('2d', { willReadFrequently: true });
function rgb(css) {
  px.clearRect(0, 0, 1, 1); px.fillStyle = '#000'; px.fillStyle = css; px.fillRect(0, 0, 1, 1);
  const [r, g, b] = px.getImageData(0, 0, 1, 1).data; return [r, g, b];
}
const hex = c => '#' + c.map(v => Math.round(v).toString(16).padStart(2, '0')).join('');
const mix = (a, b, t) => a.map((v, i) => v + (b[i] - v) * t);
const lum = ([r, g, b]) => (0.2126 * r + 0.7152 * g + 0.0722 * b) / 255;

export function palette() {
  const cs = getComputedStyle(document.documentElement), tok = n => rgb(cs.getPropertyValue('--' + n).trim() || '#888');
  const bg = tok('bg-2'), fg = tok('fg'), dark = lum(bg) < 0.5;
  const [red, green, yellow, blue, accent] = ['err', 'ok', 'warn', 'info', 'accent'].map(tok);
  const magenta = rgb(dark ? '#c678dd' : '#a626a4'), cyan = rgb(dark ? '#56b6c2' : '#0e7f8c');
  const bright = c => hex(mix(c, dark ? [255, 255, 255] : [0, 0, 0], 0.22));
  const black = dark ? tok('bg-4') : fg, white = dark ? tok('fg-2') : tok('fg-3');
  return {
    dark,
    theme: {
      background: hex(bg), foreground: hex(fg), cursor: hex(fg), cursorAccent: hex(bg),
      selectionBackground: hex(mix(bg, accent, dark ? 0.45 : 0.3)), selectionInactiveBackground: hex(mix(bg, accent, 0.18)),
      scrollbarSliderBackground: hex(mix(bg, fg, 0.18)), scrollbarSliderHoverBackground: hex(mix(bg, fg, 0.3)), scrollbarSliderActiveBackground: hex(mix(bg, fg, 0.4)),
      black: hex(black), red: hex(red), green: hex(green), yellow: hex(yellow), blue: hex(blue), magenta: hex(magenta), cyan: hex(cyan), white: hex(white),
      brightBlack: hex(dark ? tok('fg-3') : tok('fg-2')), brightRed: bright(red), brightGreen: bright(green), brightYellow: bright(yellow),
      brightBlue: bright(blue), brightMagenta: bright(magenta), brightCyan: bright(cyan), brightWhite: hex(dark ? fg : tok('bg-4')),
    },
  };
}

const size = () => fonts.terminalSize() || Math.round((parseFloat(getComputedStyle(document.documentElement).fontSize) || 14) * 0.93);

// Wait for the terminal's text face (cells are measured once), and fetch the icon faces in the background.
function faces() {
  const fam = fonts.terminalStack(), s = size();
  const main = document.fonts.load(s + 'px ' + fam, 'Mw').catch(() => {});
  document.fonts.load(s + 'px ' + fonts.SYMBOLS, '').catch(() => {}); // powerline, devicons…; the Material Design half loads when drawn
  return Promise.race([main, new Promise(r => setTimeout(r, 1500))]);
}

export async function terminal(host, { app, onState = () => {}, onLeave = () => {} }) {
  const X = await load();
  await faces();
  const pal = palette();
  const openLink = (e, uri) => { if (/^https?:/i.test(uri)) window.open(uri, '_blank', 'noopener'); };
  const term = new X.Terminal({
    linkHandler: { activate: openLink }, // OSC 8 links: xterm's own asks with confirm() first
    fontFamily: fonts.terminalStack(), fontSize: size(), lineHeight: 1, fontWeightBold: 700,
    theme: pal.theme, minimumContrastRatio: pal.dark ? 1 : 3,
    scrollback: 5000, cursorBlink: true, allowProposedApi: true, macOptionIsMeta: true, rescaleOverlappingGlyphs: true,
  });
  const fit = new X.FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new X.Unicode11Addon());
  term.unicode.activeVersion = '11';
  term.loadAddon(new X.WebLinksAddon(openLink));
  term.open(host);
  let gl = null, dead = false;
  import(V + 'addon-webgl.js').then(m => {
    if (dead) return;
    try { gl = new m.WebglAddon(); gl.onContextLoss(() => { gl.dispose(); gl = null; }); term.loadAddon(gl); } catch (e) { gl = null; } // the DOM renderer stays
  }).catch(() => {});

  // ---- keys: the terminal scope exists while it has focus
  let scope = null, typing = false;
  const ta = term.textarea;
  ta.lwType = s => term.input(s); ta.lwName = T('the terminal'); // typing that missed it lands here (lib/keys.js)
  ta.addEventListener('focus', () => {
    typing = true;
    if (!scope) {
      scope = app.keys.scope('terminal', { modal: true });
      scope.bind(LEAVE, () => onLeave(), T('back from the terminal'), { input: true, group: 'Terminal' });
    }
    report();
  });
  ta.addEventListener('blur', () => { typing = false; if (scope) { scope.dispose(); scope = null; } report(); });
  term.attachCustomKeyEventHandler(e => {
    if (e.type === 'keydown' && e.ctrlKey && e.shiftKey && !e.altKey && e.code === 'KeyC') {
      const sel = term.getSelection();
      if (sel && navigator.clipboard) navigator.clipboard.writeText(sel).then(() => app.ui.toast(T('Copied')), () => {});
      e.preventDefault(); return false;
    }
    if (e.ctrlKey && e.shiftKey && e.code === 'KeyV') return false; // the browser pastes (bracketed when the program asked)
    if (e.key === 'Enter' && e.shiftKey && !e.ctrlKey && !e.altKey && !e.metaKey) { // a newline in Claude Code and co: alt+enter
      if (e.type === 'keydown') { term.input('\x1b\r'); e.preventDefault(); }
      return false;
    }
    return true;
  });

  // ---- the connection
  const enc = new TextEncoder();
  let ws = null, pane = '', takeover = false, tries = 0, retryTimer = 0, state = 'closed', text = '';
  function report(s = state, t = text) { state = s; text = t; onState({ state, text, typing, pane }); }
  const send = b => { if (ws && ws.readyState === 1) for (let i = 0; i < b.length; i += CHUNK) ws.send(b.subarray(i, i + CHUNK)); };
  term.onData(d => send(enc.encode(d)));
  term.onBinary(d => send(Uint8Array.from(d, c => c.charCodeAt(0) & 255)));
  term.onResize(({ cols, rows }) => { if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: 'resize', cols, rows })); });

  function connect() {
    clearTimeout(retryTimer);
    if (dead || !pane) return;
    const my = pane;
    try { fit.fit(); } catch (e) { /* hidden */ }
    const q = new URLSearchParams({ cols: term.cols, rows: term.rows });
    if (takeover) q.set('takeover', '1');
    takeover = false;
    const sock = new WebSocket((location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/api/agents/' + encodeURIComponent(my) + '/terminal?' + q);
    sock.binaryType = 'arraybuffer';
    ws = sock;
    report(tries ? 'retry' : 'connecting', tries ? T('reconnecting…') : T('connecting…'));
    let opened = false;
    sock.onopen = () => { opened = true; tries = 0; report('open', ''); };
    sock.onmessage = e => {
      if (typeof e.data !== 'string') { term.write(new Uint8Array(e.data)); return; }
      try { const m = JSON.parse(e.data); if (m.type === 'exit') text = m.status; } catch (err) { /* ignore */ }
    };
    sock.onclose = e => {
      if (ws !== sock) return; // replaced
      ws = null;
      if (dead) return;
      if (e.code === 4001) return report('taken', T('opened in another window'));
      if (e.code === 4002) return report('exited', text || T('the attach ended'));
      if (e.code === 4003) return report('idle', e.reason || T('closed after a while without activity'));
      const gone = app.agents && app.agents.available && !app.agents.snapshot.Agents.some(a => a.PaneID === my);
      if (gone) return report('gone', T('the agent is gone'));
      if (e.code === 4004) { tries = 0; return connect(); }
      if (tries >= 8) return report('closed', opened ? T('the connection dropped') : T('could not attach'));
      const wait = Math.min(30000, 500 * 2 ** tries++);
      report('retry', T('reconnecting in %ds', Math.ceil(wait / 1000)));
      retryTimer = setTimeout(connect, wait);
    };
  }
  function close() { clearTimeout(retryTimer); const s = ws; ws = null; if (s) try { s.close(1000); } catch (e) { /* ignore */ } }

  // ---- size, theme and font follow the page
  let fitTimer = 0;
  const refit = () => { clearTimeout(fitTimer); fitTimer = setTimeout(() => { if (!dead && host.clientWidth > 0 && host.clientHeight > 0) try { fit.fit(); } catch (e) { /* ignore */ } }, 60); };
  const ro = new ResizeObserver(refit); ro.observe(host);
  let themeRaf = 0;
  const retheme = () => { cancelAnimationFrame(themeRaf); themeRaf = requestAnimationFrame(() => { if (dead) return; const p = palette(); term.options.theme = p.theme; term.options.minimumContrastRatio = p.dark ? 1 : 3; }); };
  const mo = new MutationObserver(retheme); mo.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme', 'style', 'class'] });
  const mq = matchMedia('(prefers-color-scheme: dark)'); mq.addEventListener('change', retheme);
  const offFonts = fonts.on(async () => {
    const fam = fonts.terminalStack(), s = size();
    if (fam === term.options.fontFamily && s === term.options.fontSize) return;
    await faces();
    if (dead) return;
    term.options.fontFamily = fam; term.options.fontSize = s; refit();
  });
  // A face that arrived after its glyphs were drawn (an icon font): draw them again.
  const onFaces = () => { if (!dead) { term.clearTextureAtlas(); term.refresh(0, term.rows - 1); } };
  document.fonts.addEventListener('loadingdone', onFaces);

  return {
    term,
    get state() { return state; },
    get pane() { return pane; },
    get typing() { return typing; },
    // Show pane's terminal: a new pane starts on a clean screen.
    attach(p, o = {}) {
      if (p === pane && ws && !o.takeover && !o.again) return;
      close();
      if (p !== pane) { term.reset(); text = ''; }
      pane = p; tries = 0; takeover = !!o.takeover;
      if (p) connect(); else report('closed', '');
    },
    detach() { close(); pane = ''; report('closed', ''); },
    focus() { term.focus(); },
    blur() { term.blur(); },
    fit() { requestAnimationFrame(() => { if (!dead && host.clientWidth > 0) try { fit.fit(); } catch (e) { /* ignore */ } }); },
    dispose() {
      dead = true; close(); ro.disconnect(); mo.disconnect(); mq.removeEventListener('change', retheme); offFonts();
      document.fonts.removeEventListener('loadingdone', onFaces);
      if (scope) scope.dispose();
      term.dispose();
    },
  };
}
