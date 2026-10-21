// Themes are CSS variable sets (css/themes.css) picked with data-theme on <html>.
// `auto` follows prefers-color-scheme. The accent and density are overrides.
export const presets = [
  { id: 'auto', group: T('Basic'), name: T('System') },
  { id: 'light', group: T('Basic'), name: T('Light') },
  { id: 'dark', group: T('Basic'), name: T('Dark') },
  { id: 'mono', group: T('Basic'), name: T('Mono') },
  { id: 'nord', group: T('More dark'), name: 'Nord' },
  { id: 'dracula', group: T('More dark'), name: 'Dracula' },
  { id: 'monokai', group: T('More dark'), name: 'Monokai' },
  { id: 'gruvbox', group: 'Gruvbox', name: 'Gruvbox' },
  { id: 'gruvbox-light', group: 'Gruvbox', name: 'Gruvbox Light' },
  { id: 'solarized-dark', group: 'Solarized', name: 'Solarized Dark' },
  { id: 'solarized-light', group: 'Solarized', name: 'Solarized Light' },
  { id: 'tokyonight', group: 'Tokyo Night', name: 'Tokyo Night' },
  { id: 'tokyonight-storm', group: 'Tokyo Night', name: 'Tokyo Night Storm' },
  { id: 'tokyonight-day', group: 'Tokyo Night', name: 'Tokyo Night Day' },
  { id: 'catppuccin', group: 'Catppuccin', name: 'Catppuccin Mocha' },
  { id: 'catppuccin-macchiato', group: 'Catppuccin', name: 'Catppuccin Macchiato' },
  { id: 'catppuccin-frappe', group: 'Catppuccin', name: 'Catppuccin Frappé' },
  { id: 'catppuccin-latte', group: 'Catppuccin', name: 'Catppuccin Latte' },
  { id: 'rosepine', group: 'Rosé Pine', name: 'Rosé Pine' },
  { id: 'rosepine-moon', group: 'Rosé Pine', name: 'Rosé Pine Moon' },
  { id: 'rosepine-dawn', group: 'Rosé Pine', name: 'Rosé Pine Dawn' },
  { id: 'kanagawa', group: 'Kanagawa', name: 'Kanagawa Wave' },
  { id: 'kanagawa-dragon', group: 'Kanagawa', name: 'Kanagawa Dragon' },
  { id: 'kanagawa-lotus', group: 'Kanagawa', name: 'Kanagawa Lotus' },
  { id: 'onedark', group: 'One', name: 'One Dark' },
  { id: 'onelight', group: 'One', name: 'One Light' },
  { id: 'emico', group: 'Emico', name: 'Emico' },
];
export const accents = ['#5b8def', '#e5484d', '#f76b15', '#e0a100', '#30a46c', '#12a594', '#8e4ec6', '#d6409f'];
import { changed } from './metrics.js';
import { T } from './i18n.js';
const root = document.documentElement;
const get = (k, d) => { try { return localStorage.getItem('lw:' + k) || d; } catch (e) { return d; } };
const local = (k, v) => { try { v == null ? localStorage.removeItem('lw:' + k) : localStorage.setItem('lw:' + k, v); } catch (e) { /* ignore */ } };
// The picks below follow the user across browsers: app.prefs (server store) as theme.<key>, localStorage for boot.js's first paint.
const SYNCED = ['theme', 'accent', 'density', 'fs', 'motion', 'custom'];
let prefs = null; // app.prefs once attached
const set = (k, v) => { local(k, v); if (prefs && SYNCED.includes(k)) prefs.set('theme.' + k, v == null ? '' : v); };

// ui.theme (preset and accent) is the default until this browser picks its own; kept for boot.js's first paint.
const dflt = () => get('theme-default', 'auto');
const apply = id => { if (id === 'auto') delete root.dataset.theme; else root.dataset.theme = id; };
const paintAccent = c => { if (c) root.style.setProperty('--accent', c); else root.style.removeProperty('--accent'); };

export const theme = {
  presets, accents,
  // solarized was the light one before the dark variant came.
  get current() { const t = get('theme', dflt()); return t === 'solarized' ? 'solarized-light' : t; },
  get accent() { return get('accent', ''); },
  get density() { return get('density', 'normal'); },
  set(id) { apply(id); set('theme', id === dflt() ? null : id); },
  setAccent(c) { paintAccent(c || get('accent-default', '')); set('accent', c || null); },
  // ui.theme: a preset name, or {preset, accent}; colours by name stay the TUI's.
  setDefault(t) {
    const p = t && presets.some(x => x.id === t.preset) ? t.preset : '', a = t && /^#[0-9a-f]{3,8}$/i.test(t.accent || '') ? t.accent : '';
    local('theme-default', p || null); local('accent-default', a || null);
    apply(theme.current);
    if (!theme.accent) paintAccent(a);
  },
  setDensity(d) { root.dataset.density = d; set('density', d === 'normal' ? null : d); changed('density'); },
  // Cycle to the next preset (command palette / key).
  next() { const i = presets.findIndex(p => p.id === theme.current); theme.set(presets[(i + 1) % presets.length].id); return theme.current; },
};

// ---- display extras: font size, reduced motion, custom token overrides
const TOKENS = ['bg', 'bg-2', 'bg-3', 'fg', 'fg-2', 'border', 'accent', 'ok', 'warn', 'err'];
let customKeys = [];
const motionCss = document.createElement('style');
motionCss.textContent = ':root[data-motion="reduce"] *, :root[data-motion="reduce"] *::before, :root[data-motion="reduce"] *::after { animation: none !important; transition: none !important; }';
document.head.append(motionCss);
// defineProperties, not assign: assign would freeze the getters' values at load.
Object.defineProperties(theme, Object.getOwnPropertyDescriptors({
  get fontSize() { return Number(get('fs', '')) || 0; },
  setFontSize(px) { if (px) root.style.setProperty('--fs', px + 'px'); else root.style.removeProperty('--fs'); set('fs', px ? String(px) : null); changed('fontsize'); },
  // The size in use right now (px), whatever set it: the setting, the density or the phone breakpoint.
  get effectiveFontSize() { return parseFloat(getComputedStyle(root).fontSize) || 14; },
  get motion() { return get('motion', 'auto'); },
  setMotion(m) { if (m === 'reduce') root.dataset.motion = 'reduce'; else delete root.dataset.motion; set('motion', m === 'reduce' ? 'reduce' : null); },
  // Custom overrides: {'--bg': '#000', …}; anything not starting with "--" is ignored.
  get custom() { try { return JSON.parse(get('custom', '{}')) || {}; } catch (e) { return {}; } },
  setCustom(obj) {
    for (const k of customKeys) root.style.removeProperty(k);
    customKeys = [];
    const clean = {};
    for (const [k, v] of Object.entries(obj || {})) if (/^--[\w-]+$/.test(k) && typeof v === 'string') { root.style.setProperty(k, v); customKeys.push(k); clean[k] = v; }
    set('custom', customKeys.length ? JSON.stringify(clean) : null);
  },
  // The colours of a preset, for swatches: {bg, fg, accent, …}. Read with the local overrides lifted.
  tokens(id) {
    const keep = root.dataset.theme, inline = ['--accent', ...customKeys].map(k => [k, root.style.getPropertyValue(k)]);
    for (const [k] of inline) root.style.removeProperty(k);
    if (id === 'auto') delete root.dataset.theme; else root.dataset.theme = id;
    const cs = getComputedStyle(root), out = {};
    for (const t of TOKENS) out[t] = cs.getPropertyValue('--' + t).trim();
    if (keep) root.dataset.theme = keep; else delete root.dataset.theme;
    for (const [k, v] of inline) if (v) root.style.setProperty(k, v);
    return out;
  },
}));
if (theme.fontSize) theme.setFontSize(theme.fontSize);
if (theme.motion === 'reduce') theme.setMotion('reduce');
theme.setCustom(theme.custom);

// Another browser may have picked since: adopt the server store's values (app.prefs.data after load).
theme.attach = pf => {
  for (const k of SYNCED) if (pf.data && 'theme.' + k in pf.data) local(k, String(pf.data['theme.' + k]) || null);
  apply(theme.current); paintAccent(theme.accent || get('accent-default', ''));
  root.dataset.density = theme.density; theme.setFontSize(theme.fontSize); theme.setMotion(theme.motion); theme.setCustom(theme.custom);
  prefs = pf; // after: re-applying must not write the values back
};

import fonts from './fonts.js'; // applies --font-ui / --font-mono at load
theme.fonts = fonts;
fonts.on(() => changed('font'));

export default theme;
