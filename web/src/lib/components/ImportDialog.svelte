<script>
  import { tx } from '#lib/i18n/index.js';
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

<Modal title={folder ? tx('importdialog_import_into_x', { folder: folder.name || tx('importdialog_this_folder') }) : tx('importdialog_import_a_space')} {onclose}>
  {#if phase === 'choose'}
    <form onsubmit={start}>
      <p class="muted">
        {folder ? tx('importdialog_hint_folder') : tx('importdialog_hint_space')}
      </p>
      <label>{tx('admin_file')}<input type="file" accept={folder ? '.tildoc,.zip' : '.tildoc'} onchange={pick} required /></label>
      {#if !folder}
        <label>{tx('importdialog_name_of_the_new_space')}<input bind:value={name} required /></label>
        <label>{tx('importdialog_short_name')}<input bind:value={slug} placeholder={tx('importdialog_made_from_the_name')} /><span class="hint">{tx('importdialog_optional_lowercase_letters_digits_and_dashes')}</span></label>
        <label>{tx('common_belongs_to')}
          <select bind:value={team}>
            <option value="">{tx('common_just_me')}</option>
            {#each teams as t (t.id)}<option value={t.id}>{tx('common_team_x', { name: t.name })}</option>{/each}
          </select>
        </label>
      {/if}
      <div class="row end">
        <button type="button" onclick={onclose}>{tx('common_cancel')}</button>
        <button class="primary" disabled={!file}>{tx('f_import')}</button>
      </div>
    </form>
  {:else if phase === 'upload' || phase === 'apply'}
    <p>{phase === 'upload' ? tx('importdialog_uploading') : tx('importdialog_importing')} {file.name}…</p>
    <div class="bar" role="progressbar" aria-valuenow={Math.round(progress * 100)} aria-valuemin="0" aria-valuemax="100">
      <span style:width="{Math.round(progress * 100)}%"></span>
    </div>
    <p class="muted small">{phase === 'apply' ? tx('importdialog_you_can_close_this_window_the') : ''}</p>
  {:else if phase === 'done'}
    <p><strong>{tx('importdialog_imported')}</strong></p>
    {#if isZip}
      <p class="muted">{tx('importdialog_x_files_x_of_them_already', { files: result.files, deduplicated: result.deduplicated, folders: result.folders })}</p>
    {:else}
      <p class="muted">{tx('importdialog_x_new_documents_x_joined_documents', { documents: result.documents, merged: result.merged, places: result.places, collections: result.collections, blobs_existing: result.blobs_existing })}</p>
    {/if}
    <div class="row end"><button onclick={onclose}>{tx('importdialog_close')}</button><button class="primary" onclick={openResult}>{tx('common_open')}</button></div>
  {:else}
    <p class="err"><strong>{tx('importdialog_the_import_failed')}</strong> {transfer?.error}</p>
    <p class="muted small">{isZip ? tx('importdialog_files_imported_before_the_problem_stay') : tx('importdialog_nothing_was_changed')}</p>
    <div class="row end"><button onclick={() => (phase = 'choose')}>{tx('importdialog_try_another_file')}</button><button onclick={onclose}>{tx('importdialog_close')}</button></div>
  {/if}
</Modal>

<style>
  .bar { height: 8px; background: var(--surface-2); border-radius: 99px; overflow: hidden; margin: 8px 0; }
  .bar span { display: block; height: 100%; background: var(--accent); transition: width 0.3s; }
  @media (prefers-reduced-motion: reduce) { .bar span { transition: none; } }
</style>
