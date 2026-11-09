// node --test: layout of the history graph (lib/history.js)
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { layoutHistory, edgePath } from './history.js';

const sha = (n) => n.toString(16).padStart(64, '0');
const lanesOf = (rows) => Object.fromEntries(rows.map((r) => [r.id, [r.row, r.lane]]));
const edge = (edges, kind) => edges.filter((e) => e.kind === kind).map((e) => `${e.from.lane},${e.from.row}>${e.to.lane},${e.to.row}${e.via !== undefined ? '/' + e.via : ''}`);

test('plain versions stay on one line', () => {
  const { rows, edges, lanes } = layoutHistory([{ version: 2, sha256: sha(2) }, { version: 1, sha256: sha(1) }]);
  assert.deepEqual(lanesOf(rows), { v1: [0, 0], v2: [1, 0] });
  assert.deepEqual(edge(edges, 'main'), ['0,0>0,1']);
  assert.equal(lanes, 1);
});

test('variants branch off once, then continue on their lane (like a git branch)', () => {
  const { rows, edges } = layoutHistory([
    { version: 1, sha256: sha(1), variants: [
      { kind: 'searchable_pdf', sha256: sha(12), input: sha(11) },
      { kind: 'image_only_pdf', sha256: sha(11), input: sha(1) }
    ] }
  ]);
  assert.deepEqual(lanesOf(rows), { v1: [0, 0], 'v1-image_only_pdf': [1, 1], 'v1-searchable_pdf': [2, 1] });
  assert.deepEqual(edge(edges, 'branch'), ['0,0>1,1', '1,1>1,2']); // diagonal once, then straight
});

test('continuation from a variant and a restore join back into the main line', () => {
  const { rows, edges, lanes } = layoutHistory([
    { version: 1, sha256: sha(1), variants: [
      { kind: 'image_only_pdf', sha256: sha(11), input: sha(1) },
      { kind: 'searchable_pdf', sha256: sha(12), input: sha(11) }
    ] },
    { version: 2, sha256: sha(2) },
    { version: 3, sha256: sha(12), continued_from: { version: 1, variant: 'searchable_pdf' } },
    { version: 4, sha256: sha(2), restored_from: 2 }
  ]);
  assert.deepEqual(lanesOf(rows), { v1: [0, 0], 'v1-image_only_pdf': [1, 1], 'v1-searchable_pdf': [2, 1], v2: [3, 0], v3: [4, 0], v4: [5, 0] });
  assert.deepEqual(edge(edges, 'merge'), ['1,2>0,4']);
  // the restore uses lane 1 again once the variant branch has joined back
  assert.deepEqual(edge(edges, 'restore'), ['0,3>0,5/1']);
  assert.equal(lanes, 2);
});

test('two variants of the same node get two lanes', () => {
  const { rows } = layoutHistory([
    { version: 1, sha256: sha(1), variants: [
      { kind: 'image_only_pdf', sha256: sha(11), input: sha(1) },
      { kind: 'searchable_pdf', sha256: sha(12), input: sha(1) }
    ] }
  ]);
  assert.deepEqual(lanesOf(rows), { v1: [0, 0], 'v1-image_only_pdf': [1, 1], 'v1-searchable_pdf': [2, 2] });
});

test('a lane held by a later continuation is not reused meanwhile', () => {
  const { rows } = layoutHistory([
    { version: 1, sha256: sha(1), variants: [{ kind: 'searchable_pdf', sha256: sha(12), input: sha(1) }] },
    { version: 2, sha256: sha(2), variants: [{ kind: 'searchable_pdf', sha256: sha(22), input: sha(2) }] },
    { version: 3, sha256: sha(12), continued_from: { version: 1, variant: 'searchable_pdf' } }
  ]);
  const l = lanesOf(rows);
  assert.equal(l['v1-searchable_pdf'][1], 1);
  assert.equal(l['v2-searchable_pdf'][1], 2); // lane 1 is busy until version 3
});

test('edge paths are valid SVG path data', () => {
  const { edges } = layoutHistory([
    { version: 1, sha256: sha(1), variants: [{ kind: 'searchable_pdf', sha256: sha(12), input: sha(1) }] },
    { version: 2, sha256: sha(2) },
    { version: 3, sha256: sha(12), continued_from: { version: 1, variant: 'searchable_pdf' } }
  ]);
  for (const e of edges) assert.match(edgePath(e, (l) => 10 + l * 16, (r) => 20 + r * 48), /^M [\d.]+ [\d.]+( (L|C) [\d., ]+)+$/);
});
