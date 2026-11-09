// node --test: which node of the history is shown (lib/format.js)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { defaultVariant, shownVariant, hasVariant, opensInOffice, newFileTypes } from './format.js';

const doc = {
  version: 2,
  ocr_preference: 'existing',
  kind: 'pdf-digital',
  variants: [],
  versions: [
    { version: 2, kind: 'pdf-digital', variants: [] },
    { version: 1, kind: 'pdf-ocred', variants: [
      { kind: 'image_only_pdf', created_at: '2026-11-08T10:00:00Z' }, { kind: 'searchable_pdf', created_at: '2026-11-08T10:01:00Z' }] }
  ]
};

test('the default is the latest node of the shown version', () => {
  assert.equal(defaultVariant(doc), 'original'); // v2 has no variants
  // v1: original -> image only -> tildoc OCR: the OCR is the latest node,
  // whatever OCR the server prefers for search
  assert.equal(defaultVariant(doc, 1), 'searchable');
  assert.equal(defaultVariant({ ...doc, ocr_preference: 'tildoc' }, 1), 'searchable');
  const imageOnly = { ...doc, versions: [{ version: 2, kind: 'pdf-ocred', variants: [{ kind: 'image_only_pdf', created_at: '2026-11-09T10:00:00Z' }] }] };
  assert.equal(defaultVariant(imageOnly), 'image_only'); // OCR not (yet) there
  const newer = { ...doc, versions: [{ version: 2, variants: [
    { kind: 'searchable_pdf', created_at: '2026-11-09T12:00:00Z' }, { kind: 'image_only_pdf', created_at: '2026-11-09T11:00:00Z' }] }] };
  assert.equal(defaultVariant(newer), 'searchable'); // by time, not by list order
});

test('a chosen variant is kept only where it exists', () => {
  assert.equal(hasVariant(doc, 1, 'image_only'), true);
  assert.equal(hasVariant(doc, 0, 'image_only'), false);
  assert.equal(shownVariant(doc, 1, 'image_only'), 'image_only');
  assert.equal(shownVariant(doc, 0, 'image_only'), 'original'); // falls back on the current version
  assert.equal(shownVariant(doc, 1, ''), 'searchable');
  assert.equal(shownVariant(doc, 1, 'original'), 'original');
});

test('office: PDFs stay in the viewer, empty files only from a template', () => {
  const office = { extensions: ['docx', 'pdf', 'txt'], create: ['docx', 'pdf'] };
  assert.equal(opensInOffice('a.docx', 'application/zip', 9, office), true);
  assert.equal(opensInOffice('a.pdf', 'application/pdf', 9, office), false);
  assert.equal(opensInOffice('a.txt', 'text/plain', 9, office), false);
  assert.equal(opensInOffice('new.pdf', 'application/pdf', 0, office), true);
  assert.equal(opensInOffice('notes.txt', 'text/plain', 0, office), false);
  assert.equal(opensInOffice('notes.foo', 'application/octet-stream', 0, office), false);
});

test('new file menu: main formats first, unknown ones last', () => {
  const t = newFileTypes(['potx', 'zzz', 'pdf', 'docx']);
  assert.deepEqual(t.map((x) => x.ext), ['docx', 'pdf', 'potx', 'zzz']);
  assert.equal(t[3].label, 'ZZZ file');
});
