import { test } from 'node:test';
import assert from 'node:assert/strict';

// keys.js listens on the document when loaded.
let onKey;
globalThis.document = { addEventListener(_, fn) { onKey = fn; } };
const { keys } = await import('../static/js/lib/keys.js');

let t = 0;
const press = (key, gap = 50) => {
  t += gap;
  const e = { key, timeStamp: t, target: {}, prevented: false, preventDefault() { this.prevented = true; }, stopPropagation() {} };
  onKey(e);
  return e;
};

test('a burst of distinct keys outside a field pauses the shortcuts until a quiet spell', () => {
  const fired = [], s = keys.scope('typing-test');
  for (const k of 'abcdj') s.bind(k, () => fired.push(k), k);
  let paused = 0; const off = keys.onTyping(() => paused++);

  press('j', 1000); press('j'); press('j');
  assert.deepEqual(fired, ['j', 'j', 'j'], 'repeating one key is navigation');

  press('a', 1000); press('b'); press('c'); press('d');
  assert.deepEqual(fired.slice(3), ['a', 'b'], 'the third key of the burst pauses');
  assert.equal(paused, 1);

  press('a', 300);
  assert.equal(fired.length, 5, 'still typing: still paused');
  press('a', 1200);
  assert.deepEqual(fired.slice(5), ['a'], 'a quiet spell resumes');

  press('b', 1000); press('c', 250); press('d', 250);
  assert.equal(fired.length, 9, 'deliberate presses never pause');
  off(); s.dispose();
});
