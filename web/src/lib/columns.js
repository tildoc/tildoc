import { size, date, shortType, statusLabel } from './format.js';

// Column definitions for the details list. value() renders, sort() orders.
export const COLUMNS = {
  name: { label: 'Name', always: true, sort: (r) => r.name.toLowerCase() },
  path: { label: 'Path', value: (r) => r.path, sort: (r) => r.path?.toLowerCase() ?? '' },
  type: { label: 'Type', value: (r) => (r.kind === 'folder' ? 'Folder' : shortType(r.mime, r.name)), sort: (r) => (r.kind === 'folder' ? '' : r.mime) },
  size: { label: 'Size', align: 'right', value: (r) => (r.kind === 'folder' ? '' : size(r.size)), sort: (r) => r.size ?? -1 },
  modified: { label: 'Modified', value: (r) => date(r.modified_at ?? r.created_at), sort: (r) => r.modified_at ?? r.created_at },
  created: { label: 'Added', value: (r) => date(r.created_at), sort: (r) => r.created_at },
  version: { label: 'Version', align: 'right', value: (r) => (r.kind === 'folder' ? '' : `v${r.version}`), sort: (r) => r.version ?? 0 },
  status: { label: 'Status', value: (r) => (r.kind === 'folder' ? '' : statusLabel[r.status] ?? r.status), sort: (r) => r.status ?? '' },
  links: { label: 'Links', align: 'right', value: (r) => (r.kind === 'folder' || !r.link_count ? '' : r.link_count), sort: (r) => r.link_count ?? 0 },
  original: { label: 'Original path', value: (r) => r.original_path ?? '', sort: (r) => r.original_path ?? '' },
  space: { label: 'Space', value: (r) => r.space_slug ?? '', sort: (r) => r.space_slug ?? '' },
  sha: { label: 'SHA-256', value: (r) => (r.sha256 ? r.sha256.slice(0, 12) + '…' : ''), sort: (r) => r.sha256 ?? '' }
};

export const DEFAULT_COLUMNS = {
  folder: ['name', 'type', 'size', 'modified', 'status', 'links'],
  search: ['name', 'space', 'path', 'type', 'size', 'modified', 'status'],
  collection: ['name', 'space', 'path', 'type', 'size', 'modified']
};

function storageKey(view) {
  return `tildoc.columns.${view}`;
}

export function loadView(view) {
  try {
    const v = JSON.parse(localStorage.getItem(storageKey(view)));
    if (v && Array.isArray(v.columns)) {
      v.columns = v.columns.filter((c) => COLUMNS[c]);
      if (!v.columns.includes('name')) v.columns.unshift('name');
      return v;
    }
  } catch {}
  return { columns: [...(DEFAULT_COLUMNS[view] ?? DEFAULT_COLUMNS.folder)], sort: 'name', desc: false };
}

export function saveView(view, v) {
  try {
    localStorage.setItem(storageKey(view), JSON.stringify(v));
  } catch {}
}
