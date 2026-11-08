// Lazy pdf.js loader. The library and its worker are only fetched when a PDF
// is shown. PDF scripting and XFA stay disabled; the UI CSP forbids eval.
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';

let lib;

export async function pdfjs() {
  if (!lib) {
    lib = await import('pdfjs-dist');
    lib.GlobalWorkerOptions.workerSrc = workerUrl;
  }
  return lib;
}

export async function openPDF(url) {
  const { getDocument } = await pdfjs();
  return getDocument({
    url,
    withCredentials: true,
    enableXfa: false,
    disableAutoFetch: true,
    rangeChunkSize: 1 << 20,
    cMapUrl: '/vendor/pdfjs/cmaps/',
    cMapPacked: true,
    standardFontDataUrl: '/vendor/pdfjs/standard_fonts/',
    wasmUrl: '/vendor/pdfjs/wasm/',
    iccUrl: '/vendor/pdfjs/iccs/'
  });
}

// resolveDest turns a link destination into a 1-based page number.
export async function resolveDest(pdf, dest) {
  try {
    const d = typeof dest === 'string' ? await pdf.getDestination(dest) : dest;
    if (!Array.isArray(d)) return null;
    const ref = d[0];
    const idx = typeof ref === 'number' ? ref : await pdf.getPageIndex(ref);
    return idx + 1;
  } catch {
    return null;
  }
}
