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

// defaultVariant picks which PDF to show: tildoc's OCR for scans, the
// original for files that came with their own OCR unless the server prefers
// tildoc's OCR.
// textVariant names the PDF whose text the server uses for search: tildoc's
// OCR when it is the search text, otherwise the original.
export function textVariant(doc) {
  return doc?.variants?.some((v) => v.kind === 'searchable_pdf' && v.search_text) ? 'searchable' : 'original';
}

export function defaultVariant(doc) {
  if (!doc?.has_searchable) return 'original';
  if (doc.foreign_ocr) return doc.ocr_preference === 'tildoc' ? 'searchable' : 'original';
  return 'searchable';
}
