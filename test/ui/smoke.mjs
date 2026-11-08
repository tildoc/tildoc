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

function pdf(text) {
  const content = `BT /F1 18 Tf 72 720 Td (${text}) Tj ET`;
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>',
    `<< /Length ${content.length} >>\nstream\n${content}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'
  ];
  let out = '%PDF-1.4\n';
  const offs = [];
  objs.forEach((o, i) => { offs.push(out.length); out += `${i + 1} 0 obj\n${o}\nendobj\n`; });
  const x = out.length;
  out += `xref\n0 ${objs.length + 1}\n0000000000 65535 f \n` + offs.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('');
  out += `trailer\n<< /Size ${objs.length + 1} /Root 1 0 R >>\nstartxref\n${x}\n%%EOF\n`;
  return out;
}
writeFileSync(join(FIX, 'Quartalsbericht.pdf'), pdf('Quartalsbericht Umsatz Hamburg 2026'));
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
  await page.getByLabel('Password (min. 8 characters)').fill('supersecret');
  await page.getByLabel('Repeat password').fill('supersecret');
  await page.getByRole('button', { name: 'Create administrator' }).click();
  await page.waitForURL('**/f/**');
  await page.locator('.crumbs').waitFor();
});

await step('new folder', async () => {
  await page.getByRole('button', { name: 'New folder' }).click();
  await page.getByRole('dialog').getByRole('textbox').fill('Rechnungen');
  await page.getByRole('button', { name: 'Create' }).click();
  await row('Rechnungen').waitFor();
});

await step('upload files', async () => {
  await page.locator('input[type=file][multiple]').setInputFiles([join(FIX, 'Quartalsbericht.pdf'), join(FIX, 'notes.txt')]);
  await toast('2 uploaded');
  await row('Quartalsbericht.pdf').waitFor();
});

await step('dedup upload', async () => {
  await page.locator('input[type=file][multiple]').setInputFiles([join(FIX, 'notes-copy.txt')]);
  await toast('deduplicated');
  await row('notes-copy.txt').locator('.badge').waitFor();
});

await step('document panel', async () => {
  await row('Quartalsbericht.pdf').click();
  await page.locator('aside.panel').waitFor();
  await page.locator('aside.panel iframe').waitFor();
  await page.locator('aside.panel .tabs').getByRole('button', { name: 'Info' }).click();
  await page.locator('aside.panel .status.done').waitFor({ timeout: 20000 });
  await page.locator('aside.panel .tabs').getByRole('button', { name: 'Text' }).click();
  await page.locator('aside.panel pre', { hasText: 'Hamburg' }).waitFor();
});

await step('links tab', async () => {
  await row('notes.txt').click();
  await page.locator('aside.panel .tabs').getByRole('button', { name: /Links/ }).click();
  await page.locator('aside.panel .list li').nth(1).waitFor();
});

await step('full text search', async () => {
  await page.getByLabel('Search').fill('Umsatz');
  await page.getByLabel('Search').press('Enter');
  await page.waitForURL('**/search?**');
  await row('Quartalsbericht.pdf').waitFor();
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

await step('original location', async () => {
  await row('Q3-Bericht.pdf').click();
  await page.getByRole('button', { name: 'Original location' }).click();
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
  await page.getByRole('link', { name: 'Settings & access' }).click();
  await page.getByPlaceholder('Label, e.g. laptop-dolphin').fill('dolphin');
  await page.getByRole('button', { name: 'Create key' }).click();
  await page.getByText('shown only once').waitFor();
  await page.getByRole('dialog').getByRole('button', { name: 'Done' }).click();
  await row('dolphin').waitFor();
});

await step('teams and team space', async () => {
  await page.getByRole('link', { name: 'Teams' }).click();
  await page.getByPlaceholder('New team name').fill('Buchhaltung');
  await page.getByRole('button', { name: 'Create', exact: true }).click();
  await page.getByRole('heading', { name: 'Buchhaltung' }).waitFor();
  await page.getByRole('link', { name: 'Spaces' }).click();
  await page.getByLabel('Name').fill('Belege');
  await page.locator('select').selectOption({ label: 'Team Buchhaltung' });
  await page.getByRole('button', { name: 'Create space' }).click();
  await toast('created');
  await page.locator('nav.side', { hasText: 'Belege' }).waitFor();
});

await step('admin', async () => {
  await page.getByRole('link', { name: 'Administration' }).click();
  await page.locator('.tile').first().waitFor();
  await page.getByLabel('Username').fill('bob');
  await page.getByLabel('Initial password').fill('bobpass12');
  await page.getByRole('button', { name: 'Create user' }).click();
  await toast('User bob created');
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
  await page.locator('nav.side').getByText('Trash').waitFor();
});

await step('dark mode', async () => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: 'dark' });
  await page.goto(BASE + '/');
  await row('notes.txt').click();
  await page.locator('aside.panel').waitFor();
});

await browser.close();
if (errors.length) {
  console.error('\nBrowser errors:\n' + errors.join('\n'));
  process.exit(1);
}
console.log('\nUI SMOKE PASSED');
