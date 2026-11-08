// Layout of a document's history graph (versions + derived variants), in
// the style of `git log --graph`, oldest at the top:
// - lane 0 is the main line of versions;
// - every derived variant branches off diagonally from the node it was made
//   from into its own lane (original -> image only -> tildoc OCR);
// - a version that continues from a variant (or from an older version, or
//   restores one) joins back into lane 0 from that node.
// Pure function: no DOM, unit-testable (node --test).

export const VARIANT_LABEL = { image_only_pdf: 'Image only', searchable_pdf: 'tildoc OCR' };

// layoutHistory(versions) -> { rows, edges, lanes }
//   versions: API document.versions (any order)
//   rows:  [{ id, kind: 'version'|'variant', lane, row, version, variant?, ... }]
//   edges: [{ kind: 'main'|'branch'|'merge'|'restore', from: {lane,row}, to: {lane,row}, via?: lane }]
export function layoutHistory(versions) {
  const vs = [...(versions ?? [])].sort((a, b) => a.version - b.version);
  const rows = [];
  const bySha = new Map(); // `${version}:${sha}` -> row of that node
  const versionRow = new Map(); // version -> row

  // 1. rows: each version, then its variants depth-first along their inputs
  for (const v of vs) {
    const vr = { id: `v${v.version}`, kind: 'version', version: v.version, data: v, row: rows.length, parent: null };
    rows.push(vr);
    versionRow.set(v.version, vr);
    bySha.set(`${v.version}:${v.sha256}`, vr);
    const variants = v.variants ?? [];
    const walk = (parent, inputSha) => {
      for (const x of variants) {
        if (x.input !== inputSha || x.sha256 === inputSha) continue;
        const r = { id: `v${v.version}-${x.kind}`, kind: 'variant', version: v.version, variant: x.kind, data: x, row: rows.length, parent };
        rows.push(r);
        bySha.set(`${v.version}:${x.sha256}`, r);
        walk(r, x.sha256);
      }
    };
    walk(vr, v.sha256);
  }

  // 2. what each version continues from (a node of an earlier version)
  const joins = []; // {from: rowObj, to: rowObj, kind: 'merge'|'restore'}
  for (const v of vs) {
    const to = versionRow.get(v.version);
    if (v.restored_from) {
      const from = versionRow.get(v.restored_from);
      if (from) joins.push({ from, to, kind: 'restore' });
    } else if (v.continued_from) {
      const c = v.continued_from;
      const from = c.variant ? rows.find((r) => r.version === c.version && r.variant === c.variant) : versionRow.get(c.version);
      if (from) joins.push({ from, to, kind: from.kind === 'variant' ? 'merge' : 'restore' });
    }
  }

  // 3. lanes. Every variant and every version-to-version jump needs a lane
  // for an interval of rows; intervals are packed greedily (lowest free lane,
  // right of the lane they branch from).
  const intervals = [];
  for (const r of rows) {
    if (r.kind !== 'variant') continue;
    let end = r.row;
    for (const j of joins) if (j.from === r) end = Math.max(end, j.to.row - 1);
    intervals.push({ owner: r, start: r.row, end, after: () => r.parent.lane });
  }
  for (const j of joins) {
    if (j.from.kind === 'version') intervals.push({ owner: j, start: j.from.row + 1, end: j.to.row - 1, after: () => 0 });
  }
  intervals.sort((a, b) => a.start - b.start);
  const busyUntil = [Infinity]; // lane -> last occupied row (lane 0 always busy)
  for (const r of rows) if (r.kind === 'version') r.lane = 0;
  for (const iv of intervals) {
    let lane = iv.after() + 1;
    while (lane < busyUntil.length && busyUntil[lane] >= iv.start) lane++;
    busyUntil[lane] = iv.end;
    iv.owner.lane = lane;
  }

  // 4. edges
  const edges = [];
  const pt = (r) => ({ lane: r.lane, row: r.row });
  for (let i = 1; i < vs.length; i++) {
    edges.push({ kind: 'main', from: pt(versionRow.get(vs[i - 1].version)), to: pt(versionRow.get(vs[i].version)) });
  }
  for (const r of rows) if (r.kind === 'variant') edges.push({ kind: 'branch', from: pt(r.parent), to: pt(r) });
  for (const j of joins) {
    if (j.from.kind === 'variant') edges.push({ kind: 'merge', from: pt(j.from), to: pt(j.to) });
    else edges.push({ kind: 'restore', from: pt(j.from), to: pt(j.to), via: j.lane }); // lane assigned in step 3
  }

  const lanes = Math.max(1, ...rows.map((r) => r.lane + 1), ...edges.map((e) => (e.via ?? 0) + 1));
  return { rows, edges, lanes };
}

// Selecting a node: which variant it is ('original' for a version node) and
// the selection { viewVersion (0 = current), viewVariant } that shows it.
const PARAM = { image_only_pdf: 'image_only', searchable_pdf: 'searchable' };
export const nodeVariant = (r) => (r.kind === 'version' ? 'original' : PARAM[r.variant]);
export function nodeSelection(doc, r) {
  return { viewVersion: r.version === doc.version ? 0 : r.version, viewVariant: nodeVariant(r) };
}
export function nodeName(r) {
  return r.kind === 'version' ? `Version ${r.version}` : `${VARIANT_LABEL[r.variant] ?? r.variant} of version ${r.version}`;
}

// path returns the SVG path of an edge. x(lane) and y(row) give centres.
export function edgePath(e, x, y) {
  const { from: a, to: b } = e;
  const curve = (x1, y1, x2, y2) => {
    const m = (y1 + y2) / 2;
    return `C ${x1} ${m}, ${x2} ${m}, ${x2} ${y2}`;
  };
  switch (e.kind) {
    case 'main':
      return `M ${x(a.lane)} ${y(a.row)} L ${x(b.lane)} ${y(b.row)}`;
    case 'branch': {
      // leave the parent diagonally, then straight down in the own lane
      const y1 = y(a.row + 1);
      return `M ${x(a.lane)} ${y(a.row)} ${curve(x(a.lane), y(a.row), x(b.lane), y1)} L ${x(b.lane)} ${y(b.row)}`;
    }
    case 'merge': {
      // down in the variant's lane, then diagonally into the version
      const y1 = y(b.row - 1);
      return `M ${x(a.lane)} ${y(a.row)} L ${x(a.lane)} ${y1} ${curve(x(a.lane), y1, x(b.lane), y(b.row))}`;
    }
    case 'restore': {
      // out of lane 0 into a side lane, down, back into lane 0
      const v = e.via, y1 = y(a.row + 1), y2 = y(b.row - 1);
      return `M ${x(a.lane)} ${y(a.row)} ${curve(x(a.lane), y(a.row), x(v), y1)} L ${x(v)} ${y2} ${curve(x(v), y2, x(b.lane), y(b.row))}`;
    }
  }
  return '';
}
