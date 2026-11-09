<script>
  // Import a tildoc bundle as a new space, or a bundle/ZIP into a folder.
  // Every file goes through the normal upload path (stored once).
  import { goto } from '$app/navigation';
  import Modal from './Modal.svelte';
  import { get } from '../api.js';
  import { uploadTransfer, waitTransfer } from '../upload.js';
  import { errorToast, refreshSpaces, refreshCollections, bumpTree } from '../state.svelte.js';

  // folder: import into this folder (bundle or ZIP); without it a new space is created
  let { folder = null, onclose, ondone } = $props();
  let file = $state(null);
  let name = $state('');
  let slug = $state('');
  let team = $state('');
  let teams = $state([]);
  let phase = $state('choose'); // choose | upload | apply | done | failed
  let progress = $state(0);
  let transfer = $state(null);

  $effect(() => {
    if (!folder) get('/teams').then((t) => (teams = t.filter((x) => x.role === 'manager'))).catch(() => {});
  });

  const isZip = $derived(file?.name?.toLowerCase().endsWith('.zip'));
  const result = $derived(transfer?.result ?? {});

  function pick(e) {
    file = e.currentTarget.files[0] ?? null;
    if (file && !folder && !name) name = file.name.replace(/\.tildoc$/i, '');
  }

  async function start(e) {
    e.preventDefault();
    if (!file) return;
    phase = 'upload';
    try {
      const t = await uploadTransfer(file, {
        kind: isZip ? 'import_zip' : 'import_bundle',
        folder_id: folder?.id, space_name: folder ? '' : name, space_slug: folder ? '' : slug, team_id: folder ? '' : team
      }, (p) => (progress = p));
      phase = 'apply';
      transfer = await waitTransfer(t.id, (x) => {
        transfer = x;
        progress = x.progress;
      });
      phase = transfer.status === 'done' ? 'done' : 'failed';
      if (phase === 'done') {
        await refreshSpaces();
        await refreshCollections();
        bumpTree();
        ondone?.(transfer);
      }
    } catch (err) {
      errorToast(err);
      phase = 'choose';
    }
  }

  function openResult() {
    onclose();
    const fid = result.folder_id || folder?.id;
    if (fid) goto(`/f/${fid}`);
  }
</script>

<Modal title={folder ? `Import into ${folder.name || 'this folder'}` : 'Import a space'} {onclose}>
  {#if phase === 'choose'}
    <form onsubmit={start}>
      <p class="muted">
        {folder ? 'A tildoc export (.tildoc) or a ZIP file. Folders are kept;' : 'A tildoc space export (.tildoc) from this or another server. It becomes a new space with its folders, versions, tags and collections;'}
        files that are already stored here are not stored again.
      </p>
      <label>File<input type="file" accept={folder ? '.tildoc,.zip' : '.tildoc'} onchange={pick} required /></label>
      {#if !folder}
        <label>Name of the new space<input bind:value={name} required /></label>
        <label>Short name<input bind:value={slug} placeholder="made from the name" /><span class="hint">Optional. Lowercase letters, digits and dashes.</span></label>
        <label>Belongs to
          <select bind:value={team}>
            <option value="">Just me</option>
            {#each teams as t (t.id)}<option value={t.id}>Team {t.name}</option>{/each}
          </select>
        </label>
      {/if}
      <div class="row end">
        <button type="button" onclick={onclose}>Cancel</button>
        <button class="primary" disabled={!file}>Import</button>
      </div>
    </form>
  {:else if phase === 'upload' || phase === 'apply'}
    <p>{phase === 'upload' ? 'Uploading' : 'Importing'} {file.name}…</p>
    <div class="bar" role="progressbar" aria-valuenow={Math.round(progress * 100)} aria-valuemin="0" aria-valuemax="100">
      <span style:width="{Math.round(progress * 100)}%"></span>
    </div>
    <p class="muted small">{phase === 'apply' ? 'You can close this window; the import continues on the server.' : ''}</p>
  {:else if phase === 'done'}
    <p><strong>Imported.</strong></p>
    {#if isZip}
      <p class="muted">{result.files} files, {result.deduplicated} of them already stored, {result.folders} folders.</p>
    {:else}
      <p class="muted">{result.documents} new documents, {result.merged} joined documents that were already here, {result.places} places, {result.collections} collections. {result.blobs_existing} files were already stored and not stored again.</p>
    {/if}
    <div class="row end"><button onclick={onclose}>Close</button><button class="primary" onclick={openResult}>Open</button></div>
  {:else}
    <p class="err"><strong>The import failed.</strong> {transfer?.error}</p>
    <p class="muted small">{isZip ? 'Files imported before the problem stay where they are.' : 'Nothing was changed.'}</p>
    <div class="row end"><button onclick={() => (phase = 'choose')}>Try another file</button><button onclick={onclose}>Close</button></div>
  {/if}
</Modal>

<style>
  .bar { height: 8px; background: var(--surface-2); border-radius: 99px; overflow: hidden; margin: 8px 0; }
  .bar span { display: block; height: 100%; background: var(--accent); transition: width 0.3s; }
  @media (prefers-reduced-motion: reduce) { .bar span { transition: none; } }
</style>
