import { tx } from './i18n/index.js';
import { size, date, shortType, statusLabel } from './format.js';

// Column definitions for the details list. value() renders, sort() orders.
export const COLUMNS = {
  name: { get label() { return tx('column_name'); }, always: true, sort: (r) => r.name.toLowerCase() },
  path: { get label() { return tx('column_path'); }, value: (r) => r.path, sort: (r) => r.path?.toLowerCase() ?? '' },
  type: { get label() { return tx('column_type'); }, value: (r) => (r.kind === 'folder' ? tx('column_folder') : shortType(r.mime, r.name)), sort: (r) => (r.kind === 'folder' ? '' : r.mime) },
  size: { get label() { return tx('column_size'); }, align: 'right', value: (r) => (r.kind === 'folder' ? '' : size(r.size)), sort: (r) => r.size ?? -1 },
  modified: { get label() { return tx('column_modified'); }, value: (r) => date(r.modified_at ?? r.created_at), sort: (r) => r.modified_at ?? r.created_at },
  created: { get label() { return tx('column_created'); }, value: (r) => date(r.created_at), sort: (r) => r.created_at },
  version: { get label() { return tx('column_version'); }, align: 'right', value: (r) => (r.kind === 'folder' ? '' : `v${r.version}`), sort: (r) => r.version ?? 0 },
  status: { get label() { return tx('column_status'); }, value: (r) => (r.kind === 'folder' ? '' : statusLabel[r.status] ?? r.status), sort: (r) => r.status ?? '' },
  links: { get label() { return tx('column_links'); }, align: 'right', value: (r) => (r.kind === 'folder' || !r.link_count ? '' : r.link_count), sort: (r) => r.link_count ?? 0 },
  original: { get label() { return tx('column_original'); }, value: (r) => r.original_path ?? '', sort: (r) => r.original_path ?? '' },
  space: { get label() { return tx('column_space'); }, value: (r) => r.space_slug ?? '', sort: (r) => r.space_slug ?? '' },
  sha: { get label() { return tx('column_sha'); }, value: (r) => (r.sha256 ? r.sha256.slice(0, 12) + '…' : ''), sort: (r) => r.sha256 ?? '' }
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
