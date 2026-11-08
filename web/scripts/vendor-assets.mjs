// Copies runtime assets from pinned npm packages into static/ (gitignored):
// pdf.js resources (cmaps, standard fonts, wasm decoders, ICC profiles) and
// the self-hosted UI fonts. Runs before `vite dev` and `vite build`.
import { cpSync, mkdirSync, rmSync, readdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const nm = join(root, 'node_modules');
const out = join(root, 'static', 'vendor');

rmSync(out, { recursive: true, force: true });
mkdirSync(out, { recursive: true });

const pdfjs = join(nm, 'pdfjs-dist');
for (const d of ['cmaps', 'standard_fonts', 'iccs']) cpSync(join(pdfjs, d), join(out, 'pdfjs', d), { recursive: true });
// wasm image decoders + color management; the QuickJS scripting engine is not
// shipped because PDF scripting is disabled.
mkdirSync(join(out, 'pdfjs', 'wasm'), { recursive: true });
for (const f of readdirSync(join(pdfjs, 'wasm'))) {
  if (!f.startsWith('quickjs')) cpSync(join(pdfjs, 'wasm', f), join(out, 'pdfjs', 'wasm', f));
}
cpSync(join(pdfjs, 'LICENSE'), join(out, 'pdfjs', 'LICENSE'));

const fonts = {
  'atkinson-hyperlegible-next': ['latin-wght-normal', 'latin-ext-wght-normal', 'latin-wght-italic'],
  literata: ['latin-wght-normal', 'latin-ext-wght-normal', 'latin-wght-italic']
};
for (const [fam, files] of Object.entries(fonts)) {
  const src = join(nm, '@fontsource-variable', fam);
  mkdirSync(join(out, 'fonts'), { recursive: true });
  for (const f of files) cpSync(join(src, 'files', `${fam}-${f}.woff2`), join(out, 'fonts', `${fam}-${f}.woff2`));
  cpSync(join(src, 'LICENSE'), join(out, 'fonts', `${fam}.LICENSE`));
}
