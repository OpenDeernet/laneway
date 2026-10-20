// Time helpers and the log-work dialog, shared by the timer, My work and the issue panel.
import { h } from './dom.js';
import { duration } from './fmt.js';
import { T } from './i18n.js';
import { keyHint } from './keys.js';

const UNIT = { d: 8 * 3600, h: 3600, m: 60 };

// "1h 30m", "1h30m", "1.5h", "45m", "2d" (a day is 8h) → seconds; 0 when it isn't a time.
export function parseDuration(s) {
  const toks = String(s).trim().toLowerCase().split(/\s+/);
  let total = 0;
  for (const t of toks) {
    const re = /(\d+(?:\.\d+)?)([dhm])/g;
    let m, used = 0;
    while ((m = re.exec(t))) { total += parseFloat(m[1]) * UNIT[m[2]]; used += m[0].length; }
    if (!used || used !== t.length) return 0;
  }
  return Math.round(total);
}

const p2 = n => String(n).padStart(2, '0');
export const ymd = d => d.getFullYear() + '-' + p2(d.getMonth() + 1) + '-' + p2(d.getDate());
export const parseYmd = s => { const [y, m, d] = s.split('-').map(Number); return new Date(y, m - 1, d); };
export const addDays = (d, n) => { const x = new Date(d.getFullYear(), d.getMonth(), d.getDate() + n); return x; };
// Monday of d's week.
export const weekStart = d => addDays(d, -((d.getDay() + 6) % 7));
export const hm = d => p2(d.getHours()) + ':' + p2(d.getMinutes());
// A local RFC 3339 time with its offset, what Jira files the work under.
export function isoLocal(d) {
  const o = -d.getTimezoneOffset(), a = Math.abs(o);
  return d.getFullYear() + '-' + p2(d.getMonth() + 1) + '-' + p2(d.getDate()) + 'T' + p2(d.getHours()) + ':' + p2(d.getMinutes()) + ':' + p2(d.getSeconds()) + (o < 0 ? '-' : '+') + p2(Math.floor(a / 60)) + ':' + p2(a % 60);
}
// When work logged on another day starts: ui.workday_start ("09:00").
export function dayStart(app, day) {
  const [hh, mm] = String((app.session && app.session.ui && app.session.ui.WorkdayStart) || '09:00').split(':').map(Number);
  return new Date(day.getFullYear(), day.getMonth(), day.getDate(), hh || 0, mm || 0);
}
export function targetSeconds(app) { return Math.round(Number(app.prefs.get('day_target', 8)) * 3600) || 8 * 3600; }
const DAYS = { sun: 0, mon: 1, tue: 2, wed: 3, thu: 4, fri: 5, sat: 6 };
export function workdays(app) {
  const l = (app.session && app.session.ui && app.session.ui.Workdays) || [];
  const out = l.map(d => DAYS[String(d).slice(0, 3).toLowerCase()]).filter(d => d != null);
  return out.length ? out : [1, 2, 3, 4, 5];
}

// logDialog(app, {key, summary?, seconds?, started?: Date, comment?, edit?: worklog id, discard?: bool, move?: key, note?})
// → Promise<'logged' | 'discard' | 'move' | null>. Posts (or puts, when editing) before it resolves.
export function logDialog(app, o) {
  const { ui, api, bus } = app;
  return new Promise(resolve => {
    let result = null;
    const time = h('input.input', { type: 'text', value: o.seconds ? duration(o.seconds) : '', placeholder: '1h 30m · 1.5h · 45m · 2d', autofocus: true, spellcheck: false });
    const base = o.started || new Date();
    const date = h('input.input', { type: 'date', value: ymd(base) });
    const comment = h('textarea.input', { rows: 3, placeholder: T('What you did'), value: o.comment || '' });
    const left = h('select.input', {}, h('option', { value: '' }, T('Reduce automatically')), h('option', { value: 'keep' }, T('Leave unchanged')), h('option', { value: 'set' }, T('Set to…')));
    const leftVal = h('input.input', { type: 'text', placeholder: '2h', hidden: true });
    left.addEventListener('change', () => { leftVal.hidden = left.value !== 'set'; if (!leftVal.hidden) leftVal.focus(); });
    const err = h('div.wl-err', { hidden: true });
    const row = (label, ...nodes) => h('label.wl-row', h('span.wl-label', label), h('span.wl-field', nodes));
    const submit = async () => {
      const secs = parseDuration(time.value);
      if (secs < 60) { err.hidden = false; err.textContent = T('Start with a time of at least a minute: 1h 30m, 1.5h, 45m'); time.focus(); return; }
      const changedDay = date.value !== ymd(base);
      let started = '';
      if (changedDay) {
        const d = parseYmd(date.value);
        started = isoLocal(ymd(d) === ymd(new Date()) ? new Date(Date.now() - secs * 1000) : dayStart(app, d));
      } else if (!o.edit) started = isoLocal(o.started || new Date(Date.now() - secs * 1000));
      const body = { Seconds: secs, Started: started };
      if (o.edit) { if (comment.value !== (o.comment || '')) body.Comment = comment.value; }
      else {
        body.Comment = comment.value;
        body.Left = left.value === 'set' ? leftVal.value.trim() : left.value;
        if (left.value === 'set' && !parseDuration(body.Left)) { err.hidden = false; err.textContent = T('Remaining estimate is not a time: 2h'); leftVal.focus(); return; }
      }
      save.disabled = true;
      try {
        if (o.edit) await api.put('/worklog/' + o.key + '/' + o.edit, body); else await api.post('/worklog/' + o.key, body);
      } catch (e) { save.disabled = false; err.hidden = false; err.textContent = e.message; return; }
      result = 'logged';
      ui.toast(o.edit ? T('Updated %s on %s', duration(secs), o.key) : T('Logged %s on %s', duration(secs), o.key), { kind: 'ok' });
      bus.emit('issue:changed', { key: o.key });
      m.close();
    };
    const save = h('button.btn.primary', { type: 'submit' }, o.edit ? T('Save') : T('Log work'), keyHint('mod+Enter'));
    const form = h('form.wl-form', { onsubmit: e => { e.preventDefault(); submit(); } },
      o.note && h('div.dim.wl-note', o.note),
      row(T('Time'), time), row(T('Date'), date), row(T('Comment'), comment),
      !o.edit && row(T('Remaining'), left, leftVal), err,
      h('div.row.end', o.discard && h('button.btn.ghost', { type: 'button', onclick: () => { result = 'discard'; m.close(); } }, T('Discard timer')),
        o.move && h('button.btn.ghost', { type: 'button', title: T('Its time goes along; nothing is logged'), onclick: () => { result = 'move'; m.close(); } }, T('Move timer to %s', o.move)), h('span.spacer'),
        h('button.btn', { type: 'button', onclick: () => m.close() }, T('Cancel'), keyHint('Escape')), save));
    const title = (o.edit ? T('Edit work on %s', o.key) : T('Log work on %s', o.key)) + (o.summary ? ' · ' + o.summary : '');
    const m = ui.modal(form, { title, onClose: () => resolve(result) });
    m.scope.bind('mod+Enter', submit, 'save', { input: true, hidden: true });
    time.select();
  });
}
