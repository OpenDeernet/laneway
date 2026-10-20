import { test } from 'node:test';
import assert from 'node:assert/strict';
import { plat, fromTUI, toTUI, MAC } from '../static/js/lib/keymap.js';

test('mod+ is ctrl+ off a mac', () => {
  assert.equal(MAC, false);
  assert.equal(plat('mod+Enter'), 'ctrl+Enter');
  assert.equal(plat('g mod+y'), 'g ctrl+y');
});

test('the config\'s super is the web\'s meta, modifiers in keyOf\'s order', () => {
  assert.equal(fromTUI('super+enter'), 'meta+Enter');
  assert.equal(fromTUI('super+ctrl+y'), 'ctrl+meta+y');
  assert.equal(toTUI('meta+Enter'), 'super+enter');
  assert.equal(toTUI('ctrl+alt+meta+k'), 'ctrl+alt+super+k');
  assert.equal(fromTUI('ctrl+y'), 'ctrl+y');
});
