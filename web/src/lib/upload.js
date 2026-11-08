// Drag & drop / file input upload preserving the dropped folder structure.
import { post, uploadFile } from './api.js';
import { ui, toast, bumpTree } from './state.svelte.js';

function readEntries(reader) {
  return new Promise((res, rej) => reader.readEntries(res, rej));
}

async function walk(entry, prefix, out) {
  if (entry.isFile) {
    const file = await new Promise((res, rej) => entry.file(res, rej));
    out.push({ file, path: prefix + file.name });
  } else if (entry.isDirectory) {
    const reader = entry.createReader();
    let batch;
    out.push({ dir: prefix + entry.name });
    do {
      batch = await readEntries(reader);
      for (const e of batch) await walk(e, prefix + entry.name + '/', out);
    } while (batch.length > 0);
  }
}

// collectDrop converts a DataTransfer into [{file, path}] (+ {dir} entries).
export async function collectDrop(dt) {
  const out = [];
  const entries = [];
  for (const item of dt.items) {
    const e = item.webkitGetAsEntry?.();
    if (e) entries.push(e);
  }
  if (entries.length) {
    for (const e of entries) await walk(e, '', out);
  } else {
    for (const f of dt.files) out.push({ file: f, path: f.name });
  }
  return out;
}

export function collectInput(fileList) {
  return [...fileList].map((f) => ({ file: f, path: f.webkitRelativePath || f.name }));
}

// uploadAll uploads items into folder (space spaceId) with limited concurrency.
export async function uploadAll(spaceId, folderId, items, onDone) {
  const files = items.filter((i) => i.file);
  if (!files.length) return;
  const roots = [...new Set(files.map((f) => f.path.split('/')[0]))];
  let importId;
  try {
    const im = await post('/imports', { space_id: spaceId, client_root: roots.length === 1 ? roots[0] : '' });
    importId = im.id;
  } catch (e) {
    toast(e.message, 'error');
    return;
  }
  const jobs = files.map((f) => {
    const u = { id: crypto.randomUUID(), name: f.path, progress: 0, state: 'queued', note: '' };
    ui.uploads.push(u);
    return { ...f, u };
  });
  let stats = { ok: 0, dedup: 0, versions: 0, failed: 0 };
  const run = async (job) => {
    const u = ui.uploads.find((x) => x.id === job.u.id);
    u.state = 'uploading';
    try {
      const r = await uploadFile(folderId, job.path, job.file, importId, (p) => (u.progress = p));
      u.progress = 1;
      u.state = 'done';
      if (r.deduplicated) {
        u.note = 'already stored – linked';
        stats.dedup++;
      } else if (r.new_version) {
        u.note = 'new version';
        stats.versions++;
      }
      stats.ok++;
    } catch (e) {
      u.state = 'error';
      u.note = e.message;
      stats.failed++;
    }
  };
  const queue = [...jobs];
  const workers = Array.from({ length: Math.min(3, queue.length) }, async () => {
    while (queue.length) await run(queue.shift());
  });
  await Promise.all(workers);
  bumpTree();
  const parts = [`${stats.ok} uploaded`];
  if (stats.dedup) parts.push(`${stats.dedup} deduplicated`);
  if (stats.versions) parts.push(`${stats.versions} new versions`);
  if (stats.failed) parts.push(`${stats.failed} failed`);
  toast(parts.join(', '), stats.failed ? 'error' : 'success');
  setTimeout(() => {
    for (const j of jobs) {
      const i = ui.uploads.findIndex((x) => x.id === j.u.id && x.state === 'done');
      if (i >= 0) ui.uploads.splice(i, 1);
    }
  }, 4000);
  onDone?.();
}
