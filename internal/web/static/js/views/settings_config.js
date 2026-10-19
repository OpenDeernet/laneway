// The config file's ui: options on the settings page. The server owns the table (/api/settings: type,
// default, value, description, choices) and writes the file the terminal app reads, comments kept.
import { h } from '../lib/dom.js';
import { restart } from '../lib/sites.js';
import { T, Tn } from '../lib/i18n.js';
import { keyHint } from '../lib/keys.js';

const cycle = (list, cur, d) => list[(Math.max(0, list.indexOf(cur)) + d + list.length) % list.length];
const show = v => (Array.isArray(v) ? v.join(', ') : String(v));

// host: {redraw(o) re-renders the row, begin(edit) / end() tell the page an inline editor is open}
export async function load(app, host) {
  const d = await app.api.get('/settings', { fresh: true });
  const options = d.settings.map(st => make(app, host, st, d.editable));
  return { options, path: d.path, editable: d.editable, warnings: d.warnings || [], groups: d.groups, keyActions: d.keyActions };
}

// put writes ui.<name> and refreshes the session's copy; the setting comes back as /api/settings lists it.
export async function put(app, name, payload) {
  const st = await app.api.put('/settings/' + name, payload);
  app.api.get('/session', { fresh: true }).then(s => { if (app.session) app.session.ui = s.ui; }).catch(() => {});
  app.bus.emit('prefs', { key: 'ui.' + name, value: '' });
  return st;
}

// saver → run(): writes value() as ui.<o.name>, one write at a time, the last change last; the outcome shows in
// the row's .cd-status.
export function saver(app, o, value) {
  let busy = false, again = false;
  const say = (t, bad) => { const s = o.el && o.el.querySelector('.cd-status'); if (s) { s.textContent = t; s.classList.toggle('bad', !!bad); } };
  async function run() {
    if (!o.editable) { say(T('No config file to write to'), true); return; }
    if (busy) { again = true; return; }
    busy = true; say(T('Saving…'));
    try {
      o.st = await put(app, o.name, { Value: value() });
      say(T('Saved to the config file'));
    } catch (e) { say(e.message, true); }
    busy = false;
    if (again) { again = false; run(); }
  }
  return run;
}

function make(app, host, st0, editable) {
  const o = { name: st0.Name, section: st0.Group, cfg: true, st: st0, err: '', wide: st0.Type === 'yaml', editable, advanced: st0.Advanced };
  const st = () => o.st;
  const current = () => { const s = st(); return s.Value != null ? show(s.Value) : (s.Type === 'bool' || s.Type === 'enum') && s.Choices && s.Name !== 'theme' && s.Name !== 'code_theme' ? s.Choices[0] : ''; };
  o.desc = T(st0.Doc);
  Object.defineProperty(o, 'meta', { get: () => [st().Terminal && T('terminal only'), st().Restart && T('restart needed')].filter(Boolean).join(' · ') });

  async function save(payload, quiet) {
    if (!editable) { app.ui.toast(T('No config file to write to'), { kind: 'err' }); return false; }
    try {
      o.st = await put(app, o.name, payload);
      o.err = '';
      if (!quiet) app.ui.toast(st().Restart ? T('Saved ui.%s · takes effect on restart', o.name) : T('Saved ui.%s', o.name), { kind: 'ok', action: st().Restart ? { label: T('Restart'), run: () => restart(app) } : undefined });
      host.redraw(o);
      return true;
    } catch (e) { o.err = e.message; host.redraw(o); return false; }
  }
  o.reset = () => { if (st().Set) save({ Value: null }); };

  const tag = (cls, text, title) => h('span.st-tag' + cls, { title }, text);
  const valueText = () => {
    const s = st();
    if (s.Type === 'yaml') return s.Set ? Tn(s.YAML.split('\n').length, '%d line', '%d lines', s.YAML.split('\n').length) : '';
    return s.Set ? show(s.Value) : '';
  };
  const def = () => h('span.faint', T('default: %s', st().Default || '-'));

  o.edit = null;
  function begin(multi) {
    const s = st();
    const init = s.Type === 'yaml' ? s.YAML || '' : s.Value != null ? show(s.Value) : '';
    const input = multi ? h('textarea.input.st-json', { rows: Math.min(14, Math.max(5, init.split('\n').length + 1)), spellcheck: false, value: init, placeholder: s.Default })
      : h('input.input', { type: 'text', value: init, placeholder: s.Default, spellcheck: false, autocomplete: 'off' });
    o.edit = { input, multi };
    const finish = () => { o.edit = null; host.end(); host.redraw(o); };
    host.begin({
      o, multi,
      commit: async () => {
        const v = input.value;
        const ok = await save(s.Type === 'yaml' ? { YAML: v } : { Value: v }, false);
        if (ok) finish(); else { o.edit.input = input; host.redraw(o); input.focus(); }
      },
      cancel: () => { o.err = ''; finish(); },
    });
    host.redraw(o);
    setTimeout(() => { input.focus(); if (input.select && !multi) input.select(); }, 0);
  }

  const toggleBtn = (label, on, onclick) => h('button.btn.ghost.st-pick' + (on ? '.on' : ''), { tabindex: -1, onclick }, label);

  o.render = () => {
    const s = st();
    if (o.edit) {
      const ed = o.edit;
      return h('div.st-edit', ed.input, o.err && h('div.st-err', o.err),
        h('div.row', h('button.btn', { tabindex: -1, onclick: () => host.commit() }, T('Save'), keyHint(ed.multi ? 'ctrl+Enter' : 'Enter')), h('button.btn.ghost', { tabindex: -1, onclick: () => host.cancel() }, T('Cancel'), keyHint('Escape')),
          h('span.faint', ed.multi ? T('ctrl+enter saves · esc cancels · empty resets') : T('enter saves · esc cancels · empty resets'))));
    }
    const reset = s.Set && h('button.btn.ghost.st-x', { tabindex: -1, title: T('Back to the default (del)'), 'aria-label': T('Reset %s', o.name), onclick: e => { e.stopPropagation(); o.reset(); } }, '×');
    const err = o.err && h('div.st-err', o.err);
    switch (s.Type) {
      case 'bool': {
        const on = current() === 'on';
        return h('span.st-val', h('button.st-switch' + (on ? '.on' : '') + (s.Set ? '' : '.unset'), { role: 'switch', 'aria-checked': on, 'aria-label': o.name, tabindex: -1, onclick: () => o.change(1) }, h('i')),
          h('span.st-state', on ? T('on') : T('off')), reset, err);
      }
      case 'enum':
        return h('span.st-val', h('button.btn.ghost', { tabindex: -1, onclick: () => o.activate() }, s.Set ? show(s.Value) : h('span.faint', current() || T('default'))), reset, err);
      case 'yaml':
        return h('div.st-yaml', h('div.row', s.Set ? h('span.chip', valueText()) : def(), h('span.spacer'), h('button.btn.ghost', { tabindex: -1, onclick: () => o.activate() }, T('Edit')), reset),
          s.Set && h('pre.st-pre', s.YAML.split('\n').slice(0, 8).join('\n') + (s.YAML.split('\n').length > 8 ? '\n…' : '')), err);
      default:
        return h('span.st-val.mono', s.Set ? h('button.btn.ghost.st-text', { tabindex: -1, onclick: () => o.activate() }, valueText()) : h('button.btn.ghost.st-text', { tabindex: -1, onclick: () => o.activate() }, def()), reset, err);
    }
  };

  o.change = d => {
    const s = st();
    if (s.Type === 'bool') save({ Value: current() === 'on' ? 'off' : 'on' });
    else if (s.Type === 'enum' && s.Choices.length <= 8) save({ Value: cycle(s.Choices, current() || s.Choices[0], d) });
  };
  o.activate = async () => {
    const s = st();
    if (o.edit) return;
    if (s.Type === 'bool') return o.change(1);
    if (s.Type === 'enum') {
      const r = await app.ui.pick({ title: o.name, items: s.Choices, placeholder: T(s.Doc) || T('Filter…'), selected: [] });
      if (r) save({ Value: r });
      return;
    }
    begin(s.Type === 'yaml');
  };
  return o;
}
