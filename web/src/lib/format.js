export function size(n) {
  if (n == null) return '';
  if (n < 1024) return `${n} B`;
  const u = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do {
    n /= 1024;
    i++;
  } while (n >= 1024 && i < u.length - 1);
  return `${n.toFixed(n < 10 ? 1 : 0)} ${u[i]}`;
}

const dtf = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' });
export function date(s) {
  if (!s) return '';
  return dtf.format(new Date(s));
}

export function shortType(mime, name = '') {
  if (!mime) return '';
  const base = mime.split(';')[0];
  const map = {
    'application/pdf': 'PDF',
    'text/plain': 'Text',
    'text/markdown': 'Markdown',
    'text/csv': 'CSV',
    'text/html': 'HTML',
    'application/json': 'JSON',
    'application/zip': 'ZIP',
    'image/jpeg': 'JPEG',
    'image/png': 'PNG',
    'application/octet-stream': 'Binary'
  };
  if (map[base]) return map[base];
  const ext = name.includes('.') ? name.split('.').pop().toUpperCase() : '';
  return ext || base.split('/').pop();
}

export const statusLabel = {
  pending: 'Queued',
  processing: 'Processing',
  done: 'Indexed',
  ocr_pending: 'OCR pending',
  unsupported: 'Stored',
  failed: 'Failed'
};

export function iconFor(row) {
  if (row.kind === 'folder') return 'folder';
  const m = row.mime || '';
  if (m.startsWith('application/pdf')) return 'pdf';
  if (m.startsWith('image/')) return 'image';
  if (m.startsWith('text/') || m.includes('json')) return 'text';
  return 'file';
}

// textVariant names the PDF whose text the server uses for search: tildoc's
// OCR when it is the search text, otherwise the original.
export function textVariant(doc) {
  return doc?.variants?.some((v) => v.kind === 'searchable_pdf' && v.search_text) ? 'searchable' : 'original';
}

// What is shown of a document is one node of its history: a version
// (0 = current) and a variant of it: 'original' | 'searchable' | 'image_only'.
export const VARIANT_KIND = { searchable: 'searchable_pdf', image_only: 'image_only_pdf' };
export const VARIANT_NAME = { original: 'Original', searchable: 'tildoc OCR', image_only: 'Image only' };

// shownVersion returns the version entry for a version number (0 = current).
export function shownVersion(doc, version = 0) {
  return doc?.versions?.find((v) => v.version === (version || doc.version));
}

// hasVariant: does that version have the variant?
export function hasVariant(doc, version, variant) {
  if (variant === 'original') return true;
  const vs = shownVersion(doc, version)?.variants ?? (version ? [] : doc?.variants ?? []);
  return vs.some((x) => x.kind === VARIANT_KIND[variant]);
}

// foreignOCR: did that version arrive with someone else's OCR layer?
export function foreignOCR(doc, version = 0) {
  return (shownVersion(doc, version)?.kind ?? doc?.kind) === 'pdf-ocred';
}

// defaultVariant picks which PDF to show: tildoc's OCR for scans, the
// original for files that came with their own OCR unless the server prefers
// tildoc's OCR.
export function defaultVariant(doc, version = 0) {
  if (!hasVariant(doc, version, 'searchable')) return 'original';
  if (foreignOCR(doc, version)) return doc.ocr_preference === 'tildoc' ? 'searchable' : 'original';
  return 'searchable';
}

// shownVariant: the chosen variant if that version has it, else the default.
export function shownVariant(doc, version, chosen) {
  return chosen && hasVariant(doc, version, chosen) ? chosen : defaultVariant(doc, version);
}
