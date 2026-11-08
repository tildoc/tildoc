// Search term handling shared by all viewers.

// terms parses websearch-style input ("a b", "exact phrase", -excluded, or)
// into lower-case terms to highlight.
export function terms(q) {
  if (!q) return [];
  const out = [];
  const re = /(-?)"([^"]+)"|(\S+)/g;
  let m;
  while ((m = re.exec(q))) {
    if (m[1] === '-') continue;
    let t = m[2] ?? m[3];
    if (!m[2]) {
      if (t.startsWith('-') || t.toLowerCase() === 'or') continue;
      t = t.replace(/^[^\p{L}\p{N}]+|[^\p{L}\p{N}]+$/gu, '');
    }
    t = t.trim().toLowerCase();
    if (t.length >= 2 && !out.includes(t)) out.push(t);
  }
  return out;
}

// findAll returns sorted, non-overlapping [start, end) ranges of all terms in text.
export function findAll(text, ts) {
  if (!ts.length || !text) return [];
  const lower = text.toLowerCase();
  const ranges = [];
  for (const t of ts) {
    let i = 0;
    while ((i = lower.indexOf(t, i)) !== -1) {
      ranges.push([i, i + t.length]);
      i += t.length;
    }
  }
  ranges.sort((a, b) => a[0] - b[0] || b[1] - a[1]);
  const out = [];
  for (const r of ranges) if (!out.length || r[0] >= out[out.length - 1][1]) out.push(r);
  return out;
}

// segments splits text into [{text, hit, index}] for rendering marks.
export function segments(text, ranges) {
  const out = [];
  let pos = 0;
  ranges.forEach(([a, b], index) => {
    if (a > pos) out.push({ text: text.slice(pos, a), hit: false });
    out.push({ text: text.slice(a, b), hit: true, index });
    pos = b;
  });
  if (pos < text.length) out.push({ text: text.slice(pos), hit: false });
  return out;
}
