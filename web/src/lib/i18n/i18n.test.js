import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { tx, txParts, setMessages, matchLocale, labels } from './index.js';
import en from './locales/en-us.json' with { type: 'json' };

test('English by default, placeholders filled', () => {
  setMessages('en-us', en);
  assert.equal(tx('history_version', { version: 3 }), 'Version 3');
});

test('plurals follow the language', () => {
  setMessages('en-us', en);
  assert.equal(tx('search_total', { count: 1 }), '1 document');
  assert.equal(tx('search_total', { count: 2 }), '2 documents');
  setMessages('pl-pl', { search_total_one: '{{count}} dokument', search_total_few: '{{count}} dokumenty', search_total_many: '{{count}} dokumentów', search_total_other: '{{count}} dokumentu' });
  assert.equal(tx('search_total', { count: 3 }), '3 dokumenty');
  assert.equal(tx('search_total', { count: 5 }), '5 dokumentów');
  setMessages('en-us', en);
});

test('a missing text falls back to English', () => {
  setMessages('de-de', { history_version: 'Version {{version}} (de)', variant_searchable: '' });
  assert.equal(tx('variant_searchable'), 'tildoc OCR');
  assert.equal(tx('history_version', { version: 1 }), 'Version 1 (de)');
  assert.equal(tx('variant_original'), 'Original');
  setMessages('en-us', en);
});

test('labels are looked up when read', () => {
  const l = labels({ a: 'variant_original' });
  setMessages('de-de', { variant_original: 'Original (de)' });
  assert.equal(l.a, 'Original (de)');
  assert.equal(l.b, undefined);
  setMessages('en-us', en);
});

test('inline emphasis is parsed before values are filled in', () => {
  setMessages('en-us', en);
  assert.deepEqual(txParts('admin_merge_into', { tag: '<strong>x</strong>' }), [{ text: 'Merge ' }, { tag: 'strong', text: '<strong>x</strong>' }, { text: ' into' }]);
});

test('browser languages match the available files', () => {
  assert.equal(matchLocale(['de-AT', 'en'], ['de-de']), 'de-de');
  assert.equal(matchLocale(['fr'], ['de-de']), 'en-us');
});

test('the translation files are consistent', () => {
  execFileSync('node', ['scripts/i18n-check.mjs'], { cwd: new URL('../../..', import.meta.url), stdio: 'pipe' });
});
