// UI smoke test: drives the real web UI in Chromium against a running
// tildoc (fresh database). Usage: BASE=http://localhost:8080 node smoke.mjs
import { chromium } from 'playwright-core';
import { mkdirSync, writeFileSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { join } from 'node:path';

const BASE = process.env.BASE || 'http://localhost:8080';
const CHROME = process.env.CHROME || '/usr/bin/chromium';
const OUT = join(import.meta.dirname, 'screenshots');
const FIX = join(import.meta.dirname, 'screenshots', 'fixtures');
mkdirSync(FIX, { recursive: true });

// pdf builds a minimal PDF with one text line per page.
function pdf(...pages) {
  const n = pages.length;
  const objs = ['<< /Type /Catalog /Pages 2 0 R >>', `<< /Type /Pages /Kids [${pages.map((_, i) => `${4 + i * 2} 0 R`).join(' ')}] /Count ${n} >>`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'];
  pages.forEach((text, i) => {
    const content = `BT /F1 18 Tf 72 720 Td (${text}) Tj ET`;
    objs.push(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents ${5 + i * 2} 0 R /Resources << /Font << /F1 3 0 R >> >> >>`);
    objs.push(`<< /Length ${content.length} >>\nstream\n${content}\nendstream`);
  });
  let out = '%PDF-1.4\n';
  const offs = [];
  objs.forEach((o, i) => { offs.push(out.length); out += `${i + 1} 0 obj\n${o}\nendobj\n`; });
  const x = out.length;
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` + offs.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('');
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${x}\n%%EOF\n`;
  return out;
}
writeFileSync(join(FIX, 'Quartalsbericht.pdf'), pdf('Quartalsbericht Umsatz Hamburg 2026'));
writeFileSync(join(FIX, 'Handbuch.pdf'), pdf('Handbuch Einleitung', 'Kapitel zwei Grundlagen', 'Kapitel drei Wartung', 'Anhang Zebra Tabelle', 'Index und Register'));
writeFileSync(join(FIX, 'scan.pdf'), pdf('Scan Original Seite'));
writeFileSync(join(FIX, 'scan-ocr.pdf'), pdf('Erkannter OCR Text'));
writeFileSync(join(FIX, 'scan-image.pdf'), pdf('Nur Bild Seite'));
writeFileSync(join(FIX, 'notes.txt'), 'Protokoll der Besprechung: Zuständigkeiten und Termine\n');
writeFileSync(join(FIX, 'notes-copy.txt'), 'Protokoll der Besprechung: Zuständigkeiten und Termine\n');

const errors = [];
let stepNo = 0;
const browser = await chromium.launch({ executablePath: CHROME, headless: true });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, acceptDownloads: true });
const page = await ctx.newPage();
page.on('pageerror', (e) => errors.push(`pageerror (step ${stepNo}): ${e.message}\n${e.stack ?? ''}`));
page.on('console', (m) => m.type() === 'error' && !m.text().includes('401') && errors.push(`console: ${m.text()}`));
page.setDefaultTimeout(10000);
const requests = [];
page.on('request', (r) => requests.push(r.url()));

async function step(name, fn) {
  stepNo++;
  process.stdout.write(`${String(stepNo).padStart(2)} ${name} … `);
  try {
    await fn();
    await page.screenshot({ path: join(OUT, `${String(stepNo).padStart(2, '0')}-${name.replace(/\W+/g, '_')}.png`) });
    console.log('ok');
  } catch (e) {
    await page.screenshot({ path: join(OUT, `FAIL-${stepNo}.png`) }).catch(() => {});
    console.log('FAILED');
    console.error(e);
    await browser.close();
    process.exit(1);
  }
}
// the History tab in the viewer's properties drawer (opened with "i")
async function viewerHistory() {
  if (!(await page.locator('.viewer aside.drawer').isVisible())) await page.keyboard.press('i');
  await page.locator('.viewer aside.drawer').getByRole('tab', { name: /^History/ }).click();
  return page.locator('.viewer aside.drawer .hist');
}
const row = (name) => page.locator('tbody tr', { hasText: name }).first();
const toast = (text) => page.locator('.toast', { hasText: text }).first().waitFor();

await step('setup admin', async () => {
  await page.goto(BASE + '/');
  await page.waitForURL('**/setup');
  await page.getByLabel('Username').fill('admin');
  await page.getByLabel('Display name').fill('Ada Admin');
  await page.getByLabel(/^Password/).fill('supersecret');
  await page.getByLabel('Repeat password').fill('supersecret');
  await page.getByRole('button', { name: 'Create administrator' }).click();
  await page.waitForURL('**/f/**');
  await page.locator('.titlerow h1').waitFor();
});

await step('new folder', async () => {
  await page.getByRole('button', { name: 'New folder' }).click();
  // typed key by key (fill() would hide focus/selection bugs)
  const name = page.getByRole('dialog').getByRole('textbox');
  await name.pressSequentially('Rechnungen');
  if ((await name.inputValue()) !== 'Rechnungen') throw new Error(`typed name became "${await name.inputValue()}"`);
  await page.getByRole('button', { name: 'Create' }).click();
  await row('Rechnungen').waitFor();
});

await step('upload files', async () => {
  await page.locator('input[type=file][multiple]').setInputFiles([join(FIX, 'Quartalsbericht.pdf'), join(FIX, 'notes.txt'), join(FIX, 'Handbuch.pdf')]);
  await toast('3 uploaded');
  await row('Quartalsbericht.pdf').waitFor();
  // the list follows background processing without a reload (live events)
  for (const n of ['Quartalsbericht.pdf', 'notes.txt', 'Handbuch.pdf'])
    await row(n).locator('.pill.status.done').waitFor({ timeout: 30000 });
  if (!requests.some((u) => u.includes('/api/events'))) throw new Error('no live event stream');
});

await step('dedup upload', async () => {
  await page.locator('input[type=file][multiple]').setInputFiles([join(FIX, 'notes-copy.txt')]);
  await toast('deduplicated');
  await row('notes-copy.txt').locator('.badge').waitFor();
});

await step('peek panel renders pdf', async () => {
  await row('Quartalsbericht.pdf').click();
  await page.locator('aside.peek').waitFor();
  await page.locator('aside.peek .pg canvas').first().waitFor();
  await page.locator('aside.peek .pg .textLayer span', { hasText: 'Hamburg' }).first().waitFor();
  await page.locator('aside.peek .status.done').waitFor({ timeout: 20000 });
  await page.locator('aside.peek .tabs').getByRole('tab', { name: 'Text' }).click();
  await page.locator('aside.peek pre', { hasText: 'Hamburg' }).waitFor();
});

await step('places tab', async () => {
  await row('notes.txt').click();
  await page.locator('aside.peek .sheet-text', { hasText: 'Protokoll' }).waitFor();
  await page.locator('aside.peek .tabs').getByRole('tab', { name: /Places \(2\)/ }).click();
  await page.locator('aside.peek .list li').nth(1).waitFor();
});

await step('peek toggles with space', async () => {
  await row('notes.txt').locator('td').nth(2).click();
  await page.keyboard.press('Space');
  await page.locator('aside.peek').waitFor({ state: 'detached' });
  await page.keyboard.press('Space');
  await page.locator('aside.peek').waitFor();
});

await step('row click previews, name opens', async () => {
  await page.locator('aside.peek').getByRole('button', { name: 'Close preview' }).click();
  await page.locator('aside.peek').waitFor({ state: 'detached' });
  await row('Quartalsbericht.pdf').locator('td').nth(2).click();
  await page.locator('aside.peek', { hasText: 'Quartalsbericht.pdf' }).waitFor();
  await row('notes.txt').getByRole('button', { name: 'notes.txt' }).click();
  await page.waitForURL('**/view/**');
  // text files open in the editor; Escape stays in it, the close button leaves
  await page.locator('.viewer .cm-editor', { hasText: 'Protokoll' }).waitFor();
  await page.locator('.editor .bar .state', { hasText: 'Saved' }).waitFor(); // editable and focused
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);
  if (!page.url().includes('/view/')) throw new Error('Escape in the editor closed the viewer');
  await page.locator('.viewer .bar').getByRole('button', { name: /^Close/ }).click();
  await page.waitForURL('**/f/**');
  await page.locator('tr.sel', { hasText: 'notes.txt' }).waitFor();
});

await step('tag a document', async () => {
  await row('Handbuch.pdf').locator('td').nth(2).click();
  await page.locator('aside.peek').getByRole('tab', { name: 'Details' }).click();
  const input = page.locator('aside.peek').getByLabel('Add tag');
  await input.pressSequentially('Steuer 2025');
  await input.press('Enter');
  // the tag is a chip inside the text box
  await page.locator('aside.peek .tageditor.field .chip', { hasText: 'Steuer 2025' }).waitFor();
  // comma adds, Backspace in the empty box removes the last tag
  await input.pressSequentially('Zettel,');
  await page.locator('aside.peek .tageditor .chip', { hasText: 'Zettel' }).waitFor();
  await input.press('Backspace');
  await page.locator('aside.peek .tageditor .chip', { hasText: 'Zettel' }).waitFor({ state: 'detached' });
  await page.locator('aside.peek .tageditor .chip', { hasText: 'Steuer 2025' }).waitFor();
  await row('Handbuch.pdf').locator('.tags .chip', { hasText: 'Steuer 2025' }).waitFor();
});

await step('collection across documents', async () => {
  await row('Quartalsbericht.pdf').locator('td').nth(2).click();
  await row('Handbuch.pdf').locator('td').nth(2).click({ modifiers: ['Control'] });
  await page.getByRole('button', { name: 'Add to collection' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'New collection' }).click();
  await page.getByRole('dialog').getByLabel('Name').fill('Steuer 2025');
  await page.getByRole('button', { name: 'Create collection' }).click();
  await toast('Added 2 documents to Steuer 2025');
  await page.locator('nav.rail a.coll', { hasText: 'Steuer 2025' }).locator('.cnt', { hasText: '2' }).waitFor();
  await page.locator('nav.rail a.coll', { hasText: 'Steuer 2025' }).click();
  await page.waitForURL('**/c/**');
  await row('Handbuch.pdf').waitFor();
  await row('Quartalsbericht.pdf').waitFor();
});

await step('viewer inside a collection', async () => {
  await row('Handbuch.pdf').getByRole('button', { name: 'Handbuch.pdf' }).click();
  await page.waitForURL('**/view/**c=**');
  await page.locator('.viewer .where', { hasText: 'In collection Steuer 2025' }).waitFor();
  await page.keyboard.press(']');
  await page.locator('.viewer h1', { hasText: 'Quartalsbericht.pdf' }).waitFor();
  await page.keyboard.press('Escape');
  await page.waitForURL('**/c/**');
  await page.locator('tr.sel', { hasText: 'Quartalsbericht.pdf' }).waitFor();
});

await step('version next to the program name', async () => {
  const v = (await page.locator('nav.rail .brand .ver').textContent()).trim();
  if (!/-g[0-9a-f]+|^[0-9a-f]{7,}/.test(v)) throw new Error(`version shows "${v}"`);
});

await step('search by tag', async () => {
  await page.goto(BASE + '/search');
  await page.getByLabel('Tag').selectOption({ label: 'Steuer 2025 (1)' });
  await page.locator('.total', { hasText: '1 document' }).waitFor();
  await page.locator('.results').getByRole('button', { name: 'Handbuch.pdf' }).waitFor();
  if ((await page.locator('.results li').count()) !== 1) throw new Error('tag filter returned more than one document');
  await page.goto(BASE + '/');
  await row('Handbuch.pdf').waitFor();
});

await step('viewer opens whole pdf', async () => {
  await row('Handbuch.pdf').click();
  await page.locator('aside.peek .pg canvas').first().waitFor();
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  await page.waitForURL('**/view/**');
  await page.locator('.viewer .stage .pg').nth(4).waitFor();
  await page.locator('.pager', { hasText: 'of 5' }).waitFor();
  await page.locator('.viewer .stage .pg canvas').first().waitFor();
  if ((await page.locator('.thumbs .thumb').count()) !== 5) throw new Error('expected 5 thumbnails');
});

await step('viewer page jump and zoom', async () => {
  const pg = page.getByLabel('Page', { exact: true });
  await pg.fill('3');
  await pg.press('Enter');
  await page.waitForFunction(() => document.querySelector('.pager input').value === '3');
  await page.locator('.thumb.active', { hasText: '3' }).waitFor();
  await page.getByRole('button', { name: 'Zoom in' }).click();
  await page.waitForFunction(() => document.querySelector('.zoom select').value === 'custom');
  await page.keyboard.press('0');
  await page.waitForFunction(() => document.querySelector('.zoom select').value === 'auto');
});

await step('viewer find highlights', async () => {
  await page.keyboard.press('Control+f');
  const before = requests.length;
  await page.getByPlaceholder('Find in document').fill('zebra');
  await page.locator('.find .count', { hasText: '1 of 1' }).waitFor();
  // pages were pre-selected with the server's text
  if (!requests.slice(before).some((u) => /\/api\/documents\/[^/]+\/text/.test(u))) throw new Error('find did not use the server text');
  await page.locator('.textLayer .hl.current', { hasText: /zebra/i }).waitFor();
  await page.locator('.thumb .hitmark').first().waitFor();
  await page.waitForFunction(() => document.querySelector('.pager input').value === '4');
  // the highlight must not cover the text: the glyphs underneath stay dark
  const shot = (await page.locator('.textLayer .hl.current').screenshot()).toString('base64');
  const dark = await page.evaluate(async (b64) => {
    const img = new Image();
    img.src = 'data:image/png;base64,' + b64;
    await img.decode();
    const c = document.createElement('canvas');
    c.width = img.width; c.height = img.height;
    const g = c.getContext('2d');
    g.drawImage(img, 0, 0);
    const d = g.getImageData(0, 0, c.width, c.height).data;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) if (d[i] + d[i + 1] + d[i + 2] < 200) n++;
    return n;
  }, shot);
  if (dark < 10) throw new Error(`search highlight hides the text (${dark} dark pixels)`);
  await page.screenshot({ path: join(OUT, 'viewer_find_highlight_active.png') });
  await page.getByPlaceholder('Find in document').press('Escape');
  await page.locator('.find').waitFor({ state: 'detached' });
});

await step('viewer details and next document', async () => {
  await page.keyboard.press('i');
  await page.locator('.drawer .status.done').waitFor({ timeout: 20000 });
  const before = page.url();
  await page.keyboard.press(']');
  await page.waitForURL((u) => u.href !== before);
  await page.locator('.viewer h1', { hasText: 'notes-copy.txt' }).waitFor();
  await page.locator('.viewer .cm-editor', { hasText: 'Protokoll' }).waitFor();
  await page.locator('.editor .bar .state', { hasText: 'Saved' }).waitFor();
  // the editor has the focus now: "i" would be typed, so use the button
  await page.getByRole('button', { name: 'Document details' }).click();
});

await step('viewer closes to folder', async () => {
  // a text file is open in the editor, which keeps Escape: close via the button
  await page.locator('.viewer .bar').getByRole('button', { name: 'Close viewer' }).click();
  await page.waitForURL('**/f/**');
  await page.locator('tr.sel', { hasText: 'notes-copy.txt' }).waitFor();
});

await step('quick search opens hit page', async () => {
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox', { name: 'Search names and contents' }).fill('Zebra');
  await page.getByRole('option', { name: /Handbuch\.pdf/ }).locator('.pg', { hasText: 'page 4' }).waitFor({ timeout: 20000 });
  await page.getByRole('combobox', { name: 'Search names and contents' }).press('Enter');
  await page.waitForURL('**/view/**');
  await page.locator('.textLayer .hl.current').waitFor();
  await page.waitForFunction(() => document.querySelector('.pager input').value === '4');
  await page.keyboard.press('Escape');
  await page.waitForURL('**/f/**');
});

await step('search page', async () => {
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox', { name: 'Search names and contents' }).fill('Umsatz');
  await page.getByRole('option', { name: /Show/ }).waitFor();
  await page.getByRole('option', { name: /Show/ }).click();
  await page.waitForURL('**/search?**');
  await page.locator('.results mark', { hasText: 'Umsatz' }).first().waitFor();
  await page.locator('.results').getByRole('button', { name: 'Quartalsbericht.pdf' }).click();
  await page.waitForURL('**/view/**');
  await page.locator('.textLayer .hl').first().waitFor();
  await page.keyboard.press('Escape');
  await page.waitForURL('**/search?**');
});

await step('move via dialog', async () => {
  await page.goto(BASE + '/');
  await row('Quartalsbericht.pdf').click();
  await page.getByRole('button', { name: 'Move', exact: true }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Rechnungen' }).click();
  await page.getByRole('button', { name: 'Move here' }).click();
  await toast('Moved 1 item');
  await row('Rechnungen').dblclick();
  await row('Quartalsbericht.pdf').waitFor();
});

await step('rename', async () => {
  await row('Quartalsbericht.pdf').click();
  await page.getByRole('button', { name: 'Rename' }).click();
  // the name is preselected without its extension: typing replaces only the name
  const name = page.getByRole('dialog').getByRole('textbox');
  await name.pressSequentially('Q3-Bericht');
  if ((await name.inputValue()) !== 'Q3-Bericht.pdf') throw new Error(`rename field became "${await name.inputValue()}"`);
  await page.getByRole('dialog').getByRole('button', { name: 'Rename' }).click();
  await row('Q3-Bericht.pdf').waitFor();
});

await step('original place', async () => {
  await row('Q3-Bericht.pdf').click();
  await page.getByRole('button', { name: 'Move to original place' }).click();
  await toast('original import location');
});

await step('delete and restore', async () => {
  await page.goto(BASE + '/');
  await row('Quartalsbericht.pdf').click();
  await page.getByRole('button', { name: 'Delete' }).click();
  await page.getByRole('button', { name: 'Move to trash' }).click();
  await toast('to trash');
  await page.getByRole('link', { name: 'Trash' }).click();
  await row('Quartalsbericht.pdf').waitFor();
  await row('Quartalsbericht.pdf').getByRole('button', { name: 'Restore' }).click();
  await toast('Restored');
});

await step('access key', async () => {
  await page.getByRole('link', { name: 'Settings and access' }).click();
  await page.getByPlaceholder('Label, for example laptop-dolphin').fill('dolphin');
  await page.getByRole('button', { name: 'Create key' }).click();
  await page.getByText('shown only this once').waitFor();
  await page.getByRole('dialog').getByRole('button', { name: 'Done' }).click();
  await row('dolphin').waitFor();
});

await step('prefer browser pdf viewer', async () => {
  await page.getByRole('radio', { name: /Browser’s built-in viewer/ }).check();
  await toast('browser’s own viewer');
  await page.goto(BASE + '/');
  await row('Handbuch.pdf').click();
  const frame = page.locator('aside.peek iframe.native');
  await frame.waitFor();
  const src = await frame.getAttribute('src');
  if (!src.includes('viewer=native')) throw new Error('native viewer url: ' + src);
  const res = await page.request.get(BASE + src);
  if (res.headers()['content-security-policy']) throw new Error('native PDF still sandboxed');
  // the same choice applies when the document is opened full-window
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  await page.waitForURL('**/view/**');
  const full = page.locator('.viewer iframe.native');
  await full.waitFor();
  if (!(await full.getAttribute('src')).includes('viewer=native')) throw new Error('full viewer ignores the preference');
  if (await page.locator('.viewer .pg canvas').count()) throw new Error('pdf.js used despite the preference');
  await page.goBack();
  await page.getByRole('link', { name: 'Settings and access' }).click();
  await page.getByRole('radio', { name: /tildoc viewer/ }).check();
  await toast('tildoc viewer');
  const res2 = await page.request.get(BASE + src);
  if (!res2.headers()['content-security-policy']?.includes('sandbox')) throw new Error('PDF must be sandboxed again');
});

await step('teams and team space', async () => {
  await page.getByRole('link', { name: 'Teams' }).click();
  await page.getByPlaceholder('New team name').fill('Buchhaltung');
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  await page.getByRole('heading', { name: 'Buchhaltung' }).waitFor();
  await page.getByRole('link', { name: 'Spaces' }).click();
  await page.getByLabel('Name', { exact: true }).fill('Belege');
  await page.getByLabel('Belongs to').selectOption({ label: 'Team Buchhaltung' });
  await page.getByRole('button', { name: 'Create space' }).click();
  await toast('Created space');
  await page.locator('nav.rail', { hasText: 'Belege' }).waitFor();
});

await step('admin', async () => {
  await page.getByRole('link', { name: 'Administration' }).click();
  await page.locator('.facts').waitFor();
  await page.locator('.tagname', { hasText: 'Steuer 2025' }).waitFor();
  await page.getByLabel('Username').fill('bob');
  await page.getByLabel(/^First password/).fill('bobpass12');
  await page.getByRole('button', { name: 'Add person' }).click();
  await toast('Added bob');
});

await step('share a space', async () => {
  await page.getByRole('link', { name: 'Spaces' }).click();
  await row('Ada Admin').getByRole('button', { name: 'Share' }).click();
  const dlg = page.getByRole('dialog');
  await dlg.getByLabel('Username').fill('bob');
  await dlg.getByLabel('Role').selectOption('viewer');
  await dlg.getByRole('button', { name: 'Share', exact: true }).click();
  await dlg.locator('.grants li', { hasText: 'bob' }).locator('.pill', { hasText: 'Viewer' }).waitFor();
  await dlg.getByRole('button', { name: 'Done' }).click();
});

await step('export space as zip and bundle', async () => {
  const href = await row('Ada Admin').getByRole('link', { name: 'ZIP' }).getAttribute('href');
  const zip = await page.request.get(BASE + href);
  const zbody = await zip.body();
  if (zip.headers()['content-type'] !== 'application/zip' || zbody.subarray(0, 2).toString() !== 'PK') throw new Error('zip export');
  const bhref = await row('Ada Admin').getByRole('link', { name: 'Export' }).getAttribute('href');
  const bundle = await page.request.get(BASE + bhref);
  if (!(bundle.headers()['content-disposition'] || '').includes('.tildoc')) throw new Error('bundle name');
  writeFileSync(join(FIX, 'ada.tildoc'), await bundle.body());
});

await step('import a space', async () => {
  await page.getByRole('button', { name: 'Import a space' }).click();
  const dlg = page.getByRole('dialog');
  await dlg.locator('input[type=file]').setInputFiles(join(FIX, 'ada.tildoc'));
  await dlg.getByLabel('Name of the new space').fill('Archiv');
  await dlg.getByRole('button', { name: 'Import' }).click();
  await dlg.getByText('Imported.').waitFor({ timeout: 30000 });
  await dlg.getByText(/files were already stored and not stored again/).waitFor();
  await dlg.getByRole('button', { name: 'Open' }).click();
  await page.waitForURL('**/f/**');
  await page.locator('.titlerow h1', { hasText: 'Archiv' }).waitFor();
  await row('Handbuch.pdf').locator('.tags .chip', { hasText: 'Steuer 2025' }).waitFor();
});

await step('server backup', async () => {
  await page.getByRole('link', { name: 'Administration' }).click();
  await page.getByRole('button', { name: 'Create full backup' }).click();
  await page.locator('table tr', { hasText: 'Full' }).locator('.pill', { hasText: 'done' }).waitFor({ timeout: 30000 });
  await page.getByRole('button', { name: 'Create differential backup' }).click();
  await page.locator('table tr', { hasText: 'Differential of' }).locator('.pill', { hasText: 'done' }).waitFor({ timeout: 30000 });
  await page.getByRole('radio', { name: 'tildoc OCR' }).waitFor();
});

await step('columns customization', async () => {
  await page.goto(BASE + '/');
  await page.getByRole('button', { name: 'Columns' }).click();
  await page.locator('.menu label', { hasText: 'SHA-256' }).locator('input').check();
  await page.getByRole('button', { name: 'Done' }).click();
  await page.locator('thead th', { hasText: 'SHA-256' }).waitFor();
  await page.reload();
  await page.locator('thead th', { hasText: 'SHA-256' }).waitFor();
});

await step('mobile layout', async () => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(BASE + '/');
  await row('notes.txt').waitFor();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
  if (overflow) throw new Error('horizontal overflow on mobile');
  await page.getByRole('button', { name: 'Menu' }).click();
  await page.locator('nav.rail').getByText('Trash').waitFor();
  await page.keyboard.press('Escape');
});

await step('mobile viewer', async () => {
  await page.goto(BASE + '/');
  await row('Handbuch.pdf').locator('button.link').click();
  await page.waitForURL('**/view/**');
  await page.locator('.viewer .stage .pg canvas').first().waitFor();
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth + 1);
  if (overflow) throw new Error('horizontal overflow in mobile viewer');
});

await step('dark mode', async () => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto(BASE + '/');
  await row('Handbuch.pdf').click();
  await page.locator('aside.peek .pg canvas').first().waitFor();
});

await step('dark viewer', async () => {
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  await page.waitForURL('**/view/**');
  await page.locator('.viewer .stage .pg canvas').first().waitFor();
});

// ---------------------------------------------------------------- live and large
const api = async (method, path, data) => {
  const r = await page.request.fetch(BASE + '/api' + path, {
    method, data, headers: { 'X-Tildoc-CSRF': '1', 'Content-Type': typeof data === 'string' ? 'application/octet-stream' : 'application/json' }
  });
  if (!r.ok()) throw new Error(`${method} ${path}: ${r.status()} ${await r.text()}`);
  return r.status() === 204 ? null : r.json();
};
const home = async () => {
  const spaces = await api('GET', '/spaces');
  return spaces.find((x) => x.slug === 'admin') ?? spaces[0];
};

await step('live update from another client', async () => {
  const sp = await home();
  const f = await api('POST', '/folders', { parent_id: sp.root_folder_id, name: 'Live' });
  await page.goto(`${BASE}/f/${f.id}`);
  await page.locator('.empty-state', { hasText: 'This folder is empty' }).waitFor();
  // e.g. WebDAV, S3 or a colleague: the open folder shows it without a reload
  await api('PUT', `/folders/${f.id}/files?path=arrived.txt`, 'arrived from elsewhere');
  await row('arrived.txt').waitFor();
  await row('arrived.txt').locator('.pill.status.done').waitFor({ timeout: 30000 });
  // folders created elsewhere appear in the tree
  await api('POST', '/folders', { parent_id: f.id, name: 'Sub from DAV' });
  await row('Sub from DAV').waitFor();
  await page.locator('nav.rail .row-node', { hasText: 'Live' }).locator('button.tw').click();
  await page.locator('nav.rail .lbl', { hasText: 'Sub from DAV' }).waitFor();
});

await step('large folder renders only visible rows', async () => {
  const sp = await home();
  const f = await api('POST', '/folders', { parent_id: sp.root_folder_id, name: 'Many' });
  for (let i = 0; i < 300; i++) await api('PUT', `/folders/${f.id}/files?path=many-${String(i).padStart(3, '0')}.txt`, `file ${i}`);
  await page.goto(`${BASE}/f/${f.id}`);
  await page.locator('.tools .count', { hasText: '300 items' }).waitFor();
  const rendered = await page.locator('tbody tr[data-key]').count();
  if (rendered >= 150) throw new Error(`rendered ${rendered} rows for 300 files`);
  // keyboard End reveals the last row, which was not rendered before
  await page.locator('tbody tr[data-key]').first().click();
  await page.keyboard.press('End');
  await page.locator('tbody tr.sel').waitFor();
  const last = await page.locator('tbody tr.sel .link').textContent();
  if (!/many-(000|299)\.txt/.test(last)) throw new Error(`End selected ${last}`);
  if (!(await page.locator('tbody tr.sel').isVisible())) throw new Error('last row not visible');
  // the scroll height stays that of all rows
  const h = await page.evaluate(() => document.querySelector('.scroll').scrollHeight);
  if (h < 300 * 30) throw new Error(`scroll height ${h}`);
});

await step('revisit renders from cache', async () => {
  await page.goto(BASE + '/');
  await row('Handbuch.pdf').waitFor();
  const folderId = new URL(page.url()).pathname.split('/')[2];
  await row('Handbuch.pdf').locator('button.link').click();
  await page.waitForURL('**/view/**');
  // file and document came in one request when the viewer opened
  if (!requests.some((u) => u.includes('include=document'))) throw new Error('viewer did not use include=document');
  // back: the folder shows from the cache even while the API is slow, and
  // the revalidation is answered with 304 Not Modified
  const statuses = [];
  const onResp = (r) => r.url().includes(`/api/folders/${folderId}`) && statuses.push(r.status());
  page.on('response', onResp);
  await page.route(`**/api/folders/${folderId}`, async (route) => {
    await new Promise((r) => setTimeout(r, 3000));
    await route.continue().catch(() => {}); // may already be unrouted
  });
  await page.goBack();
  await row('Handbuch.pdf').waitFor({ timeout: 1500 });
  await page.unroute(`**/api/folders/${folderId}`);
  for (let i = 0; i < 50 && !statuses.length; i++) await page.waitForTimeout(100);
  page.off('response', onResp);
  if (!statuses.includes(304)) throw new Error(`revalidation statuses ${statuses}`);
});

await step('history graph', async () => {
  const sp = await home();
  const f = await api('POST', '/folders', { parent_id: sp.root_folder_id, name: 'History' });
  const up = await api('PUT', `/folders/${f.id}/files?path=contract.txt`, 'first draft');
  const doc = up.file.document_id;
  await api('POST', `/documents/${doc}/versions`, 'second draft');
  await api('POST', `/documents/${doc}/versions/1/restore`);
  await page.goto(`${BASE}/f/${f.id}`);
  await row('contract.txt').click();
  await page.locator('aside.peek').getByRole('tab', { name: 'History (3)' }).click();
  const hist = page.locator('aside.peek .hist');
  await hist.getByText('Version 3').waitFor();
  // oldest first, the restore joins back from version 1 (dashed side lane)
  const titles = await hist.locator('li strong').allTextContents();
  if (titles.join(',') !== 'Version 1,Version 2,Version 3') throw new Error(`order ${titles}`);
  await hist.getByText('restored from version 1').waitFor();
  // metadata order: date | size | source
  const metas = await hist.locator('li .meta').allTextContents();
  if (!/^.+ \| 11 B \| Web upload$/.test(metas[0]) || !/^.+ \| 11 B \| Restore$/.test(metas[2])) throw new Error(`history metadata: ${metas}`);
  const box = await page.locator('aside.peek').boundingBox();
  if (box.x + box.width > 1440 + 1) throw new Error(`peek panel overflows the window (${box.x + box.width}px)`);
  if ((await hist.locator('path.edge.restore').count()) !== 1 || (await hist.locator('path.edge.main').count()) !== 2) throw new Error('edges');
  // only the shown node is marked; no "current" label
  if ((await hist.locator('li.shown').count()) !== 1 || !(await hist.locator('li.shown').textContent()).includes('Version 3')) throw new Error('shown node');
  if (await hist.getByText('current', { exact: true }).count()) throw new Error('current label still shown');
  // selecting a node shows it in the preview ...
  await hist.getByRole('button', { name: 'Show version 2' }).click();
  await hist.locator('li.shown', { hasText: 'Version 2' }).waitFor();
  await page.locator('aside.peek .preview', { hasText: 'second draft' }).waitFor();
  await page.locator('aside.peek .bar .pill', { hasText: 'Viewing version 2' }).waitFor();
  // ... and Open opens exactly that
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  await page.waitForURL(/\/view\/.*[?&]v=2/);
  await page.locator('.viewer', { hasText: 'second draft' }).waitFor();
  // in the viewer the history lives in the properties drawer; its nodes switch the view
  if (await page.locator('.viewer .strip').count()) throw new Error('history strip still in the toolbar');
  let vh = await viewerHistory();
  await vh.locator('li.shown', { hasText: 'Version 2' }).waitFor();
  await vh.getByRole('button', { name: 'Show version 3' }).click();
  await page.locator('.viewer', { hasText: 'first draft' }).waitFor();
  await vh.locator('li.shown', { hasText: 'Version 3' }).waitFor();
  // selecting the file again starts at its latest version, not at the one viewed before
  await page.goto(`${BASE}/f/${f.id}`);
  await row('contract.txt').click();
  await page.locator('aside.peek .preview', { hasText: 'first draft' }).waitFor();
  await page.locator('aside.peek').getByRole('tab', { name: 'History (3)' }).click();
  await page.locator('aside.peek .hist li.shown', { hasText: 'Version 3' }).waitFor();
  if (await page.locator('aside.peek .bar .pill', { hasText: 'Viewing' }).count()) throw new Error('an older selection was kept');
});

await step('history: open the selected variant', async () => {
  // a scan whose tildoc OCR variant exists (the UI test has no OCR tools:
  // the variant is attached in the database, as the worker would do)
  const sp = await home();
  const f = await api('POST', '/folders', { parent_id: sp.root_folder_id, name: 'Variants' });
  const { readFileSync } = await import('node:fs');
  const scan = await api('PUT', `/folders/${f.id}/files?path=scan.pdf`, readFileSync(join(FIX, 'scan.pdf')).toString('latin1'));
  const ocr = await api('PUT', `/folders/${f.id}/files?path=scan-ocr.pdf`, readFileSync(join(FIX, 'scan-ocr.pdf')).toString('latin1'));
  const img = await api('PUT', `/folders/${f.id}/files?path=scan-image.pdf`, readFileSync(join(FIX, 'scan-image.pdf')).toString('latin1'));
  // the chain the worker builds for scans with foreign OCR: original -> image only -> tildoc OCR
  const hex = (x) => `decode('${x.file.sha256}', 'hex')`;
  const sql = `INSERT INTO blob_derivatives(source_sha256, kind, derived_sha256, input_sha256, engine, meta, created_at) VALUES
    (${hex(scan)}, 'image_only_pdf', ${hex(img)}, ${hex(scan)}, 'test', '{}', now()),
    (${hex(scan)}, 'searchable_pdf', ${hex(ocr)}, ${hex(img)}, 'test', '{}', now() + interval '1 second')`;
  execFileSync('docker', ['exec', 'tildoc-dev-pg', 'psql', '-q', '-U', 'postgres', '-d', 'tildoc', '-c', sql]);
  await page.goto(`${BASE}/f/${f.id}`);
  await row('scan.pdf').click();
  // the preview shows tildoc's OCR (default for scans) and the graph marks that node
  await page.locator('aside.peek .pg .textLayer span', { hasText: 'Erkannter' }).first().waitFor();
  await page.locator('aside.peek').getByRole('tab', { name: 'History' }).click();
  const hist = page.locator('aside.peek .hist');
  await hist.locator('li.shown', { hasText: 'tildoc OCR' }).waitFor();
  if ((await hist.locator('li.shown').count()) !== 1) throw new Error('more than one node marked');
  // select the original: preview and viewer follow
  await hist.getByRole('button', { name: 'Show version 1' }).click();
  await hist.locator('li.shown', { hasText: 'Version 1' }).waitFor();
  await page.locator('aside.peek .pg .textLayer span', { hasText: 'Scan Original' }).first().waitFor();
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  await page.waitForURL(/variant=original/);
  await page.locator('.viewer .pg .textLayer span', { hasText: 'Scan Original' }).first().waitFor();
  // back, select the OCR variant again, open: the viewer shows the variant
  await page.goto(`${BASE}/f/${f.id}`);
  await row('scan.pdf').click();
  await page.locator('aside.peek').getByRole('tab', { name: 'History' }).click();
  await hist.getByRole('button', { name: 'Show tildoc OCR of version 1' }).click();
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  await page.waitForURL(/variant=searchable/);
  await page.locator('.viewer .pg .textLayer span', { hasText: 'Erkannter' }).first().waitFor();
  // switch variants in the viewer's properties (History): original and back
  const vh = await viewerHistory();
  await vh.locator('li.shown', { hasText: 'tildoc OCR' }).waitFor();
  if ((await vh.locator('li').count()) !== 3) throw new Error('chain original -> image only -> OCR');
  await vh.getByRole('button', { name: 'Show version 1' }).click();
  await page.locator('.viewer .pg .textLayer span', { hasText: 'Scan Original' }).first().waitFor();
  await vh.getByRole('button', { name: 'Show tildoc OCR of version 1' }).click();
  await page.locator('.viewer .pg .textLayer span', { hasText: 'Erkannter' }).first().waitFor();
});

await step('places across spaces, only readable ones', async () => {
  // the same file in the personal space and in the team space: two
  // documents, one content; Places shows both
  const spaces = await api('GET', '/spaces');
  const mine = spaces.find((x) => x.slug === 'admin') ?? spaces[0];
  const team = spaces.find((x) => x.name === 'Belege');
  const f = await api('POST', '/folders', { parent_id: mine.root_folder_id, name: 'Same' });
  await api('PUT', `/folders/${f.id}/files?path=quittung.txt`, 'Quittung 42 identical');
  await api('PUT', `/folders/${team.root_folder_id}/files?path=quittung-team.txt`, 'Quittung 42 identical');
  await page.goto(`${BASE}/f/${f.id}`);
  await row('quittung.txt').click();
  await page.locator('aside.peek .tabs').getByRole('tab', { name: 'Places (2)' }).click();
  await page.locator('aside.peek .list li', { hasText: `${team.slug}/quittung-team.txt` }).waitFor();
  await page.locator('aside.peek .list li', { hasText: 'same content' }).waitFor();
  // the place in the other space opens there
  await page.locator('aside.peek .list li', { hasText: 'quittung-team.txt' }).getByRole('button').click();
  await page.waitForURL(`**/f/${team.root_folder_id}?sel=*`);
  await page.locator('tbody tr.sel', { hasText: 'quittung-team.txt' }).waitFor();
  // no "Also show in…" any more
  if (await page.getByRole('button', { name: /Also show in/ }).count()) throw new Error('"Also show in…" still offered');
});

await step('selection in the URL, shareable', async () => {
  const spaces = await api('GET', '/spaces');
  const team = spaces.find((x) => x.name === 'Belege');
  await page.goto(`${BASE}/f/${team.root_folder_id}`);
  await page.evaluate(() => (window.__noReload = true));
  await row('quittung-team.txt').click();
  await page.waitForURL(/[?&]sel=/);
  const first = new URL(page.url()).searchParams.get('sel');
  await page.getByRole('button', { name: 'New folder' }).waitFor();
  // selecting another file updates the URL, without reloading the page
  await api('PUT', `/folders/${team.root_folder_id}/files?path=beleg-2.txt`, 'Beleg zwei');
  await row('beleg-2.txt').click();
  await page.waitForURL((u) => u.searchParams.get('sel') && u.searchParams.get('sel') !== first);
  if (!(await page.evaluate(() => window.__noReload))) throw new Error('the page reloaded');
  // several files selected: no single file in the URL
  await row('quittung-team.txt').click({ modifiers: ['Control'] });
  await page.waitForURL((u) => !u.searchParams.has('sel'));
  await row('beleg-2.txt').click();
  await page.waitForURL(/[?&]sel=/);
  const shared = page.url();

  // a colleague with access opens the link: same folder, same file, preview shown
  const mia = await api('POST', '/users', { username: 'mia', password: 'miapass12' });
  const grant = await api('POST', `/spaces/${team.id}/grants`, { user: 'mia', role: 'viewer' });
  const ctx2 = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const p2 = await ctx2.newPage();
  await p2.request.post(BASE + '/api/auth/login', { data: { username: 'mia', password: 'miapass12' } });
  await p2.goto(BASE + '/');
  await p2.evaluate(() => localStorage.setItem('tildoc.peek', 'false')); // she closed the preview before
  await p2.goto(shared);
  await p2.locator('tbody tr.sel', { hasText: 'beleg-2.txt' }).waitFor();
  await p2.locator('aside.peek', { hasText: 'Beleg zwei' }).waitFor();
  // without access the link opens nothing
  await api('DELETE', `/spaces/${team.id}/grants/${grant.id}`);
  await p2.goto(shared);
  await p2.locator('.empty-state', { hasText: "can't be opened" }).waitFor();
  if (await p2.getByText('Beleg zwei').count()) throw new Error('content shown without access');
  await ctx2.close();
  void mia;
});

await step('multi-select survives a shaky Ctrl-click', async () => {
  const sp = await home();
  const tree = await api('GET', `/spaces/${sp.id}/tree`);
  const many = tree.find((x) => x.name === 'Many');
  await page.goto(`${BASE}/f/${many.id}`);
  const rows = page.locator('tbody tr[data-key]');
  await rows.nth(0).locator('td').nth(2).click();
  for (const i of [1, 2, 3]) await rows.nth(i).locator('td').nth(2).click({ modifiers: ['Control'] });
  // Ctrl-click with the mouse moving a few pixels: the browser starts a drag
  const box = await rows.nth(4).locator('td').nth(2).boundingBox();
  await page.keyboard.down('Control');
  await page.mouse.move(box.x + 10, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x + 22, box.y + box.height / 2 + 3, { steps: 4 });
  await page.mouse.up();
  await page.keyboard.up('Control');
  for (const i of [5, 6, 7, 8, 9]) await rows.nth(i).locator('td').nth(2).click({ modifiers: ['Control'] });
  const n = await page.locator('tbody tr.sel').count();
  if (n !== 10) throw new Error(`${n} rows selected, want 10`);
  await page.locator('.tools .count', { hasText: '10 selected' }).waitFor();
});

await step('office document: Collabora, one version per session', async () => {
  const spaces = await api('GET', '/spaces');
  const mine = spaces.find((x) => x.slug === 'admin') ?? spaces[0];
  const f = await api('POST', '/folders', { parent_id: mine.root_folder_id, name: 'Office' });
  const up = await api('PUT', `/folders/${f.id}/files?path=bericht.docx`, 'PK\x03\x04 first');
  const doc = `/documents/${up.file.document_id}`;
  await page.goto(`${BASE}/f/${f.id}`);
  await row('bericht.docx').click();
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  const frame = page.frameLocator('iframe.office');
  await frame.getByRole('heading', { name: 'bericht.docx' }).waitFor();
  await frame.getByText('editing as').waitFor();
  // saves during the session are no versions
  await frame.getByRole('button', { name: 'Autosave' }).click();
  await frame.getByText('done: save').waitFor();
  let d = await api('GET', doc);
  if (d.version !== 1 || !d.office_session?.saved_at) throw new Error(`during the session: v${d.version}`);
  await page.getByText('become a new version when you close').waitFor();
  // closing makes exactly one version, and the iframe stayed in place
  await frame.getByRole('button', { name: 'Close document' }).click();
  await frame.getByText('done: exit').waitFor();
  d = await api('GET', doc);
  if (d.version !== 2 || d.versions[0].source !== 'office' || d.office_session) throw new Error(`after the session: ${JSON.stringify(d.versions)}`);
  await frame.getByText('done: exit').waitFor();
});

await step('office: a version arriving after the viewer closed shows up by itself', async () => {
  const spaces = await api('GET', '/spaces');
  const mine = spaces.find((x) => x.slug === 'admin') ?? spaces[0];
  const f = await api('POST', '/folders', { parent_id: mine.root_folder_id, name: 'Office live' });
  const up = await api('PUT', `/folders/${f.id}/files?path=spaet.docx`, 'PK\x03\x04 later');
  await page.goto(`${BASE}/f/${f.id}`);
  await row('spaet.docx').click();
  await page.locator('aside.peek').getByRole('button', { name: 'Open' }).click();
  const frame = page.frameLocator('iframe.office');
  await frame.getByRole('button', { name: 'Close later' }).click();
  await frame.getByText('done: exit-later').waitFor();
  await page.getByRole('button', { name: 'Close viewer' }).click();
  await page.waitForURL(`**/f/${f.id}*`);
  // while the save is still on its way, the row says so; then it clears itself
  await row('spaet.docx').locator('.badge', { hasText: 'In Office' }).waitFor();
  await row('spaet.docx').click();
  await page.locator('aside.peek').getByRole('tab', { name: 'History' }).click();
  await page.locator('aside.peek .hist li', { hasText: 'Office' }).waitFor({ timeout: 10000 });
  await row('spaet.docx').locator('.badge', { hasText: 'In Office' }).waitFor({ state: 'detached' });
});

await step('new file: text with any ending, office formats, PDF edit', async () => {
  const { readFileSync } = await import('node:fs');
  const spaces = await api('GET', '/spaces');
  const mine = spaces.find((x) => x.slug === 'admin') ?? spaces[0];
  const f = await api('POST', '/folders', { parent_id: mine.root_folder_id, name: 'Neu' });
  await page.goto(`${BASE}/f/${f.id}`);
  const menu = () => page.getByRole('button', { name: 'New file' }).click();
  // a text file with an unusual ending goes to the text editor
  await menu();
  await page.getByRole('menuitem', { name: 'Text file' }).click();
  await page.getByLabel('Name').fill('notizen.foo');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.waitForURL(/\/view\//);
  await page.locator('.cm-content').waitFor();
  // an office format, opened from its template
  await page.goto(`${BASE}/f/${f.id}`);
  await menu();
  if ((await page.getByRole('menuitem').allTextContents()).join().indexOf('Word document') < 0) throw new Error('no office formats');
  await page.getByRole('menuitem', { name: 'Word document' }).click();
  await page.getByLabel('Name').fill('brief');
  await page.getByRole('button', { name: 'Create' }).click();
  await page.frameLocator('iframe.office').getByRole('heading', { name: 'brief.docx' }).waitFor();
  // a PDF opens in the viewer and can be sent to the office server
  const pdf = await api('PUT', `/folders/${f.id}/files?path=edit-me.pdf`, readFileSync(join(FIX, 'scan.pdf')).toString('latin1'));
  await page.goto(`${BASE}/view/${pdf.file.link_id}`);
  await page.locator('.pg').first().waitFor();
  if (await page.locator('iframe.office').count()) throw new Error('PDF opened in the office server by default');
  await page.getByRole('button', { name: 'Edit in Office' }).click();
  await page.frameLocator('iframe.office').getByRole('heading', { name: /\.pdf$/ }).waitFor(); // same content as an earlier upload: named after its first place
  await page.getByRole('button', { name: 'Back to PDF viewer' }).click();
  await page.locator('.pg').first().waitFor();
  // office files explain the missing preview
  await api('PUT', `/folders/${f.id}/files?path=tabelle.odt`, 'PK\x03\x04 sheet');
  await page.goto(`${BASE}/f/${f.id}`);
  await row('tabelle.odt').click();
  await page.locator('aside.peek').getByText('No preview for this format').waitFor();
});

await step('text editor: edit, keys, review and save', async () => {
  const sp = await home();
  const f = await api('POST', '/folders', { parent_id: sp.root_folder_id, name: 'Editor' });
  await api('PUT', `/folders/${f.id}/files?path=notes.md`, '# Title\nline one\nline two\n');
  await page.goto(`${BASE}/f/${f.id}`);
  // the preview stays read-only
  await row('notes.md').click();
  await page.locator('aside.peek', { hasText: 'line one' }).waitFor();
  if (await page.locator('aside.peek .cm-editor').count()) throw new Error('editor in the preview');
  // opening goes straight into the editor
  await row('notes.md').locator('button.link').click();
  await page.waitForURL('**/view/**');
  await page.locator('.viewer .cm-editor').waitFor();
  await page.locator('.editor .bar .state', { hasText: 'Saved' }).waitFor();
  await page.locator('.cm-line', { hasText: 'line two' }).click();
  await page.keyboard.press('End');
  await page.keyboard.type(' and more');
  await page.keyboard.press('Alt+ArrowUp'); // move the line up
  await page.locator('.editor .bar .state', { hasText: 'Unsaved changes' }).waitFor();
  const lines = await page.locator('.cm-content .cm-line').allTextContents();
  if (lines[1] !== 'line two and more' || lines[2] !== 'line one') throw new Error(`lines ${lines}`);
  // search and replace panel
  await page.keyboard.press('Control+f');
  await page.locator('.cm-panel.cm-search').waitFor();
  await page.keyboard.press('Escape');
  await page.locator('.cm-panel.cm-search').waitFor({ state: 'detached' });
  if (!page.url().includes('/view/')) throw new Error('Escape in the editor closed the viewer');
  // Ctrl+S: review with the diff, Enter saves
  await page.locator('.cm-content').click();
  await page.keyboard.press('Control+s');
  const dlg = page.getByRole('dialog', { name: 'Save changes' });
  await dlg.locator('.cm-changedLine').first().waitFor();
  await dlg.getByRole('button', { name: 'Save as version 2' }).waitFor();
  await page.screenshot({ path: join(OUT, 'editor_review.png') });
  await page.keyboard.press('Enter');
  await toast('Saved as version 2');
  await page.locator('.editor .bar .state', { hasText: 'Saved' }).waitFor();
  const vh = await viewerHistory();
  await vh.locator('li', { hasText: 'Version 2' }).locator('.meta', { hasText: 'Editor' }).waitFor();
});

await step('text editor: one editor at a time, live hand-over', async () => {
  const spaces = await api('GET', '/spaces');
  const team = spaces.find((x) => x.name === 'Belege');
  const up = await api('PUT', `/folders/${team.root_folder_id}/files?path=protokoll.txt`, 'Punkt 1\nPunkt 2\n');
  await api('POST', `/spaces/${team.id}/grants`, { user: 'mia', role: 'member' });
  // admin edits
  await page.goto(`${BASE}/view/${up.file.link_id}`);
  await page.locator('.editor .bar .state', { hasText: 'Saved' }).waitFor();
  // mia sees it read-only, live
  const ctx2 = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const p2 = await ctx2.newPage();
  p2.on('pageerror', (e) => errors.push(`pageerror (mia): ${e.message}`));
  await p2.request.post(BASE + '/api/auth/login', { data: { username: 'mia', password: 'miapass12' } });
  await p2.goto(`${BASE}/view/${up.file.link_id}`);
  await p2.locator('.editor .note', { hasText: 'Ada Admin is editing' }).waitFor();
  // admin leaves: the lease is released, mia can edit at once
  await page.goto(BASE + '/');
  await p2.locator('.editor .bar .state', { hasText: 'Saved' }).waitFor();
  await p2.locator('.toast', { hasText: 'You can edit this document now' }).first().waitFor();
  // a version arrives meanwhile from another channel (e.g. WebDAV): warned, last save wins
  await p2.locator('.cm-line', { hasText: 'Punkt 2' }).click();
  await p2.keyboard.press('End');
  await p2.keyboard.type(' (Mia)');
  await api('PUT', `/folders/${team.root_folder_id}/files?path=protokoll.txt`, 'Punkt 1\nPunkt 2\nPunkt 3 extern\n');
  // her unsaved text stays, she is warned
  await p2.locator('.editor .bar .warn', { hasText: 'arrived meanwhile' }).waitFor();
  if (!(await p2.locator('.cm-content').innerText()).includes('Punkt 2 (Mia)')) throw new Error('unsaved text lost when a version arrived');
  await p2.keyboard.press('Control+s');
  const dlg = p2.getByRole('dialog', { name: 'Save changes' });
  await dlg.locator('p.warn', { hasText: 'was saved meanwhile' }).waitFor();
  await dlg.getByRole('button', { name: 'Save as version 3' }).click();
  await p2.locator('.toast', { hasText: 'Saved as version 3' }).first().waitFor();
  const doc = await api('GET', `/documents/${up.file.document_id}`);
  if (doc.version !== 3 || doc.versions[0].source !== 'editor' || doc.versions[1].source !== 'web') throw new Error('versions');
  await ctx2.close();
});

await browser.close();
if (errors.length) {
  console.error('\nBrowser errors:\n' + errors.join('\n'));
  process.exit(1);
}
console.log('\nUI SMOKE PASSED');
