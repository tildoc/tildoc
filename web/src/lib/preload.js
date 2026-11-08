// Preloading: when the pointer rests on (or focus reaches) a file name, load
// the viewer's code and, for PDFs, start pdf.js and its worker, so opening
// feels instant.
import { preloadCode } from '$app/navigation';
import { warmup } from './viewer/pdfjs.js';
import { prefs } from './state.svelte.js';

let viewerCode = null;
let timer = null;

export function preloadDoc(row) {
  if (!row || row.kind === 'folder' || !row.link_id) return;
  clearTimeout(timer);
  // a short delay: sweeping over a list does not load anything
  timer = setTimeout(() => {
    viewerCode ??= preloadCode(`/view/${row.link_id}`).catch(() => (viewerCode = null));
    if ((row.mime ?? '').startsWith('application/pdf') && prefs().pdf_viewer !== 'native') warmup().catch(() => {});
  }, 80);
}

export function cancelPreload() {
  clearTimeout(timer);
}
