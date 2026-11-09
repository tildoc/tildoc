// where the source code is offered (AGPL section 13): shown on the program name
export const SOURCE_URL = 'https://github.com/tildoc/tildoc';

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

// defaultVariant: what opens when nothing is chosen is the latest node of
// the version's history, i.e. its newest derived variant (tildoc's OCR at
// the end of original -> image only -> OCR), or the version itself.
const PARAM = { searchable_pdf: 'searchable', image_only_pdf: 'image_only' };
export function defaultVariant(doc, version = 0) {
  const vs = shownVersion(doc, version)?.variants ?? (version ? [] : doc?.variants ?? []);
  const latest = vs.reduce((a, b) => (!a || (b.created_at ?? '') >= (a.created_at ?? '') ? b : a), null);
  return latest ? PARAM[latest.kind] ?? 'original' : 'original';
}

// shownVariant: the chosen variant if that version has it, else the default.
export function shownVariant(doc, version, chosen) {
  return chosen && hasVariant(doc, version, chosen) ? chosen : defaultVariant(doc, version);
}

export function extOf(name) {
  const i = (name ?? '').lastIndexOf('.');
  return i >= 0 ? name.slice(i + 1).toLowerCase() : '';
}

// opensInOffice: does this file open in the office server by default? Same
// types as the server (core.OfficeMIME), minus PDFs (their own viewer is the
// default, "Edit in Office" asks for it). An empty file only if the server
// can fill it from a template; other empty files go to the text editor.
export function opensInOffice(name, mime, size, office) {
  if (size === 0) return (office?.create ?? []).includes(extOf(name));
  const m = mime ?? '';
  if (m.startsWith('text/') || m.startsWith('application/pdf') || m.includes('json') || m.startsWith('image/')) return false;
  return (office?.extensions ?? []).includes(extOf(name));
}

// newFileTypes: the "New file" menu, the main formats first, then templates.
const NEW_FILE_LABEL = {
  docx: 'Word document', xlsx: 'Excel spreadsheet', pptx: 'PowerPoint presentation', pdf: 'PDF document',
  odt: 'OpenDocument text', ods: 'OpenDocument spreadsheet', odp: 'OpenDocument presentation', odg: 'OpenDocument drawing',
  dotx: 'Word template', xltx: 'Excel template', potx: 'PowerPoint template',
  ott: 'OpenDocument text template', ots: 'OpenDocument spreadsheet template', otp: 'OpenDocument presentation template'
};
const NEW_FILE_ORDER = Object.keys(NEW_FILE_LABEL);
export function newFileTypes(create) {
  const rank = (e) => (NEW_FILE_ORDER.includes(e) ? NEW_FILE_ORDER.indexOf(e) : NEW_FILE_ORDER.length);
  return [...(create ?? [])]
    .sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
    .map((ext) => ({ ext, label: NEW_FILE_LABEL[ext] ?? `${ext.toUpperCase()} file` }));
}
