// node --test: which node of the history is shown (lib/format.js)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defaultVariant, shownVariant, hasVariant } from './format.js';

const doc = {
  version: 2,
  ocr_preference: 'existing',
  kind: 'pdf-digital',
  variants: [],
  versions: [
    { version: 2, kind: 'pdf-digital', variants: [] },
    { version: 1, kind: 'pdf-ocred', variants: [{ kind: 'image_only_pdf' }, { kind: 'searchable_pdf' }] }
  ]
};

test('defaults depend on the shown version', () => {
  assert.equal(defaultVariant(doc), 'original'); // v2 has no OCR variant
  assert.equal(defaultVariant(doc, 1), 'original'); // foreign OCR, preference "existing"
  assert.equal(defaultVariant({ ...doc, ocr_preference: 'tildoc' }, 1), 'searchable');
  const scan = { ...doc, versions: [{ version: 2, kind: 'pdf-scanned', variants: [{ kind: 'searchable_pdf' }] }, doc.versions[1]] };
  assert.equal(defaultVariant(scan), 'searchable'); // scans show tildoc's OCR
});

test('a chosen variant is kept only where it exists', () => {
  assert.equal(hasVariant(doc, 1, 'image_only'), true);
  assert.equal(hasVariant(doc, 0, 'image_only'), false);
  assert.equal(shownVariant(doc, 1, 'image_only'), 'image_only');
  assert.equal(shownVariant(doc, 0, 'image_only'), 'original'); // falls back on the current version
  assert.equal(shownVariant(doc, 1, ''), 'original');
  assert.equal(shownVariant(doc, 1, 'original'), 'original');
});
