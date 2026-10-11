import { test } from 'node:test';
import assert from 'node:assert/strict';
import { splitBlocks, seqDiff, pageDiff, churn, titleDiff } from '../static/js/lib/pagediff.js';

// The marks as text: {+came+} [-went-].
const shown = r => r.blocks.map(b => [b.kind, b.md.replace(//g, '{+').replace(//g, '+}').replace(//g, '[-').replace(//g, '-]')]);

test('blocks are runs of lines between blank ones, a code fence whole', () => {
  assert.deepEqual(splitBlocks('# A\n\npara\nmore\n\n\n```\nx\n\ny\n```\nafter'), ['# A', 'para\nmore', '```\nx\n\ny\n```\nafter']);
});

test('seqDiff keeps the common head and tail and the longest run between', () => {
  assert.deepEqual(seqDiff(['a', 'b', 'c', 'd'], ['a', 'x', 'c', 'd']), [['=', 0, 0], ['-', 1], ['+', 1], ['=', 2, 2], ['=', 3, 3]]);
  assert.deepEqual(seqDiff([], ['a']), [['+', 0]]);
});

test('a changed paragraph marks the words, as one change for words in a row', () => {
  const r = pageDiff('# Deploy\n\nRun the health check now.', '# Deploy\n\nRun the readiness probe now.');
  assert.deepEqual(shown(r), [['same', '# Deploy'], ['chg', 'Run the [-health check-]{+readiness probe+} now.']]);
  assert.equal(r.add, 2);
  assert.equal(r.del, 2);
});

test('blocks unlike any other went or came, in their place', () => {
  assert.deepEqual(shown(pageDiff('a b c\n\nAsk ops to restart.', 'a b c\n\nNew para here.')), [['same', 'a b c'], ['del', 'Ask ops to restart.'], ['add', 'New para here.']]);
});

test('a list item that came is marked after its bullet', () => {
  assert.deepEqual(shown(pageDiff('- one\n- two', '- one\n- two\n- three')), [['chg', '- one\n- two{+\n- three+}']]);
});

test('a change to markup shows the block removed and added', () => {
  assert.deepEqual(shown(pageDiff('Some **bold** text', 'Some _bold_ text')), [['del', 'Some **bold** text'], ['add', 'Some _bold_ text']]);
});

test('a table diffs by row: cells changed, rows that came or went marked whole', () => {
  const r = pageDiff('| h | i |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |', '| h | i |\n|---|---|\n| 1 | 2x |');
  assert.deepEqual(shown(r), [['chg', '| h | i |\n|---|---|\n| 1 | [-2-]{+2x+} |\n| [-3-] | [-4-] |']]);
  // Columns changed: the table removed and added.
  assert.deepEqual(shown(pageDiff('| h |\n|---|\n| 1 |', '| h | i |\n|---|---|\n| 1 | 2 |')).map(b => b[0]), ['del', 'add']);
});

test('spacing alone is no change', () => {
  assert.deepEqual(churn('one two', 'one  two'), { add: 0, del: 0 });
});

test('churn counts the words that came and went', () => {
  assert.deepEqual(churn('', 'three new words'), { add: 3, del: 0 });
  assert.deepEqual(churn('gone now\n\nkept', 'kept'), { add: 0, del: 2 });
});

test('titleDiff marks a renamed title, null for the same', () => {
  assert.equal(titleDiff('Policy', 'Policy'), null);
  assert.deepEqual(titleDiff('Log policy', 'Retention policy'), [['-', 'Log'], ['+', 'Retention'], ['=', ' '], ['=', 'policy']]);
});
