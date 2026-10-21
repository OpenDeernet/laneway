import { test } from 'node:test';
import assert from 'node:assert/strict';

// keys.js listens on the document when loaded.
const on = {};
globalThis.document = { activeElement: null, addEventListener(type, fn) { on[type] = fn; } };
globalThis.KeyboardEvent = class { constructor(type, o) { Object.assign(this, { type }, o); } };
const { keys } = await import('../static/js/lib/keys.js');

let t = 0;
const press = (key, gap = 50) => {
  t += gap;
  const e = { key, timeStamp: t, target: {}, preventDefault() {}, stopPropagation() {} };
  on.keydown(e);
  return e;
};
const wait = ms => new Promise(r => setTimeout(r, ms));

test('a burst of distinct keys outside a field takes back its first keys and pauses until a quiet spell', () => {
  const fired = [], s = keys.scope('typing-test');
  let state = 0, modals = 0;
  s.bind('j', () => { fired.push('j'); state++; }, 'j');
  s.bind('a', () => { fired.push('a'); state++; }, 'a');
  s.bind('b', () => { fired.push('b'); const m = keys.scope('modal', { modal: true }); modals++; m.bind('Escape', () => { modals--; m.dispose(); }, ''); }, 'b');
  for (const k of 'cd') s.bind(k, () => fired.push(k), k);
  const offKeep = keys.keep({ save: () => state, restore: v => { state = v; } });
  const bursts = []; const off = keys.onTyping(b => bursts.push(b));

  press('j', 1000); press('j'); press('j');
  assert.deepEqual(fired, ['j', 'j', 'j'], 'repeating one key is navigation');
  assert.equal(bursts.length, 0);

  state = 0;
  press('a', 1000); press('b'); press('c'); press('d');
  assert.deepEqual(fired.slice(3), ['a', 'b'], 'the third key of the burst pauses');
  assert.equal(state, 0, 'what the first keys did is taken back');
  assert.equal(modals, 0, 'the modal one opened is closed');
  assert.equal(bursts.length, 1);
  assert.equal(bursts[0].put, null, 'no field used yet: nothing to type into');

  press('a', 300);
  assert.equal(fired.length, 5, 'still typing: still paused');
  press('a', 1200);
  assert.deepEqual(fired.slice(5), ['a'], 'a quiet spell resumes');

  press('c', 1000); press('d', 250); press('c', 250);
  assert.equal(bursts.length, 1, 'deliberate presses never pause');
  off(); offKeep(); s.dispose();
});

test('keys typed into a picker the run opened count: "later" on the board', () => {
  const fired = [], s = keys.scope('typing-test');
  let picker = null;
  const input = { tagName: 'INPUT' };
  s.bind('l', () => fired.push('l'), 'l');
  s.bind('a', () => { fired.push('a'); picker = keys.scope('modal', { modal: true }); picker.bind('Escape', () => { picker.dispose(); picker = null; }, ''); }, 'a');
  const bursts = []; const off = keys.onTyping(b => bursts.push(b));
  press('l', 2000); press('a');
  on.focusin({ target: input, timeStamp: t + 5 });
  t += 50;
  const e = { key: 't', timeStamp: t, target: input, prevented: false, preventDefault() { this.prevented = true; }, stopPropagation() {} };
  on.keydown(e);
  assert.equal(bursts.length, 1, 'l a t is typing');
  assert.ok(e.prevented, 'the t never reaches the picker');
  assert.equal(picker, null, 'the picker is closed');
  off(); s.dispose();
});

test('a quick hand is no typing: j j l, x x X, g b g p', () => {
  const fired = [], s = keys.scope('typing-test');
  for (const k of 'jlxXbp') s.bind(k, () => fired.push(k), k);
  s.bind('g b', () => fired.push('gb'), 'gb'); s.bind('g p', () => fired.push('gp'), 'gp');
  const bursts = []; const off = keys.onTyping(b => bursts.push(b));
  press('j', 2000); press('j'); press('l');
  press('x', 2000); press('x'); press('X');
  press('g', 2000); press('b'); press('g'); press('p');
  assert.equal(bursts.length, 0);
  assert.deepEqual(fired, ['j', 'j', 'l', 'x', 'x', 'X', 'gb', 'gp']);
  off(); s.dispose();
});

test('"laa": two keys, then one into the picker they opened', () => {
  const s = keys.scope('typing-test');
  s.bind('l', () => {}, 'l'); s.bind('a', () => {}, 'a');
  const bursts = []; const off = keys.onTyping(b => bursts.push(b));
  const input = { tagName: 'INPUT' };
  press('l', 2000); press('a');
  on.focusin({ target: input, timeStamp: t + 5 });
  t += 50;
  on.keydown({ key: 'a', timeStamp: t, target: input, preventDefault() {}, stopPropagation() {} });
  assert.equal(bursts.length, 1);
  off(); s.dispose();
});

test('typing in a field you chose never pauses', () => {
  const s = keys.scope('typing-test');
  const bursts = []; const off = keys.onTyping(b => bursts.push(b));
  const input = { tagName: 'INPUT' };
  on.focusin({ target: input, timeStamp: t + 5000 });
  t += 6000;
  for (const key of 'hello') { t += 50; on.keydown({ key, timeStamp: t, target: input, preventDefault() {}, stopPropagation() {} }); }
  assert.equal(bursts.length, 0);
  off(); s.dispose();
});

test('the burst can go to the field last used', () => {
  const s = keys.scope('typing-test');
  for (const k of 'hi') s.bind(k, () => {}, k);
  let got = '';
  const field = { tagName: 'TEXTAREA', isConnected: true, getClientRects: () => [1], lwType: x => { got = x; }, lwName: 'the terminal', focus() {} };
  on.focusin({ target: field });
  const bursts = []; const off = keys.onTyping(b => bursts.push(b));
  press('h', 2000); press('i'); press('Space'); press('y'); press('o');
  assert.equal(bursts[0].field, 'the terminal');
  bursts[0].put();
  assert.equal(got, 'hi yo');
  off(); s.dispose();
});

test('a write waits a moment and a different key drops it', async () => {
  const fired = [], s = keys.scope('typing-test');
  s.bind('H', () => fired.push('H'), 'move', { writes: true });
  s.bind('x', () => fired.push('x'), 'x');
  press('H', 2000); press('x');
  await wait(250);
  assert.deepEqual(fired, ['x'], 'typed: the write never happens');
  press('H', 2000);
  await wait(250);
  assert.deepEqual(fired, ['x', 'H'], 'pressed alone it runs');
  press('H', 2000); press('H');
  await wait(250);
  assert.deepEqual(fired, ['x', 'H', 'H', 'H'], 'the same key again runs both');
  s.dispose();
});

test('single-key shortcuts off leaves characters alone, named and modifier keys work', () => {
  const fired = [], s = keys.scope('typing-test');
  s.bind('j', () => fired.push('j'), 'j');
  s.bind('Enter', () => fired.push('Enter'), 'open');
  keys.configure({ single: false });
  press('j', 2000); press('Enter', 2000);
  assert.deepEqual(fired, ['Enter']);
  assert.ok(!keys.active().some(b => b.spec === 'j'), 'not offered in the key bar either');
  keys.configure({ single: true });
  press('j', 2000);
  assert.deepEqual(fired, ['Enter', 'j']);
  s.dispose();
});
