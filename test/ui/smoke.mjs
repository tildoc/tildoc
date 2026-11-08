// UI smoke test: drives the real web UI in Chromium against a running
// tildoc (fresh database). Usage: BASE=http://localhost:8080 node smoke.mjs
import { chromium } from 'playwright-core';
import { mkdirSync, writeFileSync } from 'node:fs';
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
writeFileSync(join(FIX, 'notes.txt'), 'Protokoll der Besprechung: Zuständigkeiten und Termine\n');
writeFileSync(join(FIX, 'notes-copy.txt'), 'Protokoll der Besprechung: Zuständigkeiten und Termine\n');

const errors = [];
let stepNo = 0;
const browser = await chromium.launch({ executablePath: CHROME, headless: true });
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, acceptDownloads: true });
const page = await ctx.newPage();
page.on('pageerror', (e) => errors.push(`pageerror: ${e.message}`));
page.on('console', (m) => m.type() === 'error' && !m.text().includes('401') && errors.push(`console: ${m.text()}`));
page.setDefaultTimeout(10000);

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
  await page.getByRole('dialog').getByRole('textbox').fill('Rechnungen');
  await page.getByRole('button', { name: 'Create' }).click();
  await row('Rechnungen').waitFor();
});

await step('upload files', async () => {
  await page.locator('input[type=file][multiple]').setInputFiles([join(FIX, 'Quartalsbericht.pdf'), join(FIX, 'notes.txt'), join(FIX, 'Handbuch.pdf')]);
  await toast('3 uploaded');
  await row('Quartalsbericht.pdf').waitFor();
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
  await page.locator('.viewer .sheet-text', { hasText: 'Protokoll' }).waitFor();
  await page.keyboard.press('Escape');
  await page.waitForURL('**/f/**');
  await page.locator('tr.sel', { hasText: 'notes.txt' }).waitFor();
});

await step('tag a document', async () => {
  await row('Handbuch.pdf').locator('td').nth(2).click();
  await page.locator('aside.peek').getByRole('tab', { name: 'Details' }).click();
  const input = page.locator('aside.peek').getByLabel('Add tag');
  await input.fill('Steuer 2025');
  await input.press('Enter');
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
  await page.getByPlaceholder('Find in document').fill('zebra');
  await page.locator('.find .count', { hasText: '1 of 1' }).waitFor();
  await page.locator('.textLayer .hl.current', { hasText: /zebra/i }).waitFor();
  await page.locator('.thumb .hitmark').first().waitFor();
  await page.waitForFunction(() => document.querySelector('.pager input').value === '4');
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
  await page.locator('.viewer .sheet-text', { hasText: 'Protokoll' }).waitFor();
  await page.keyboard.press('i');
});

await step('viewer closes to folder', async () => {
  await page.keyboard.press('Escape');
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
  await page.getByRole('dialog').getByRole('textbox').fill('Q3-Bericht.pdf');
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

await browser.close();
if (errors.length) {
  console.error('\nBrowser errors:\n' + errors.join('\n'));
  process.exit(1);
}
console.log('\nUI SMOKE PASSED');
