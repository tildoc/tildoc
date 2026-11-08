<script>
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { get, post, patch, del } from '#lib/api.js';
  import { toast, errorToast, bumpTree, session } from '#lib/state.svelte.js';
  import { collectDrop, collectInput, uploadAll } from '#lib/upload.js';
  import { moveKeys, splitKeys } from '#lib/actions.js';
  import FileTable from '#lib/components/FileTable.svelte';
  import DocumentPanel from '#lib/components/DocumentPanel.svelte';
  import Icon from '#lib/components/Icon.svelte';
  import Prompt from '#lib/components/Prompt.svelte';
  import Confirm from '#lib/components/Confirm.svelte';
  import FolderPicker from '#lib/components/FolderPicker.svelte';

  let data = $state(null);
  let error = $state('');
  let selected = $state([]);
  let dialog = $state(null); // {kind, ...}
  let dragging = $state(false);
  let fileInput, dirInput;

  let folderId = $derived(page.params.id);

  async function load(keepSelection = false) {
    try {
      const d = await get(`/folders/${folderId}`);
      data = d;
      error = '';
      const want = page.url.searchParams.get('sel');
      if (want && !keepSelection) selected = [`l:${want}`];
      else if (!keepSelection) selected = [];
      else selected = selected.filter((k) => rows.some((r) => r.key === k));
    } catch (e) {
      error = e.message;
      data = null;
    }
  }

  $effect(() => {
    folderId;
    load();
  });

  $effect(() => {
    const h = () => load(true);
    window.addEventListener('tildoc:changed', h);
    return () => window.removeEventListener('tildoc:changed', h);
  });

  let rows = $derived(
    data
      ? [
          ...data.folders.map((f) => ({ ...f, key: `f:${f.id}`, kind: 'folder' })),
          ...data.files.map((f) => ({ ...f, key: `l:${f.link_id}`, kind: 'file', modified_at: f.modified_at }))
        ]
      : []
  );
  let canWrite = $derived(data?.space?.perm >= 2);
  let canDelete = $derived(data?.space?.perm >= 3);
  let selRows = $derived(rows.filter((r) => selected.includes(r.key)));
  let single = $derived(selRows.length === 1 ? selRows[0] : null);
  let panelFile = $derived(single?.kind === 'file' ? single : null);

  function open(row) {
    if (row.kind === 'folder') goto(`/f/${row.id}`);
    else selected = [row.key];
  }

  async function mkdir(name) {
    dialog = null;
    try {
      await post('/folders', { parent_id: folderId, name });
      bumpTree();
      load(true);
    } catch (e) {
      errorToast(e);
    }
  }

  async function rename(name) {
    const row = single;
    dialog = null;
    try {
      if (row.kind === 'folder') await patch(`/folders/${row.id}`, { name });
      else await patch(`/files/${row.link_id}`, { name });
      bumpTree();
      load(true);
    } catch (e) {
      errorToast(e);
    }
  }

  async function remove() {
    dialog = null;
    try {
      const res = await post('/bulk/delete', splitKeys(selected));
      if (res.errors.length) toast(`${res.errors.length} failed: ${res.errors[0].error}`, 'error');
      else toast(`Moved ${res.done} item${res.done === 1 ? '' : 's'} to trash`, 'success');
      bumpTree();
      load();
    } catch (e) {
      errorToast(e);
    }
  }

  async function pickTarget(target, mode) {
    dialog = null;
    try {
      if (mode === 'move') await moveKeys(selected, target);
      else if (mode === 'copy') {
        for (const r of selRows) {
          if (r.kind === 'folder') await post(`/folders/${r.id}/copy`, { parent_id: target });
          else await post(`/files/${r.link_id}/copy`, { folder_id: target });
        }
        toast(`Copied ${selRows.length} item(s)`, 'success');
        bumpTree();
      } else if (mode === 'link') {
        await post('/links', { document_id: single.document_id, folder_id: target, name: single.name });
        toast('Link created – the document now appears in both places', 'success');
        load(true);
      }
    } catch (e) {
      errorToast(e);
    }
  }

  async function restoreOriginal() {
    try {
      const ids = splitKeys(selected).link_ids;
      await post('/bulk/restore-original', { link_ids: ids });
      toast('Moved back to the original import location', 'success');
      bumpTree();
      load();
    } catch (e) {
      errorToast(e);
    }
  }

  async function dropFiles(e) {
    e.preventDefault();
    dragging = false;
    if (!canWrite || !e.dataTransfer.files.length) return;
    const items = await collectDrop(e.dataTransfer);
    uploadAll(data.space.id, folderId, items, () => load(true));
  }

  function pickFiles(e) {
    const items = collectInput(e.target.files);
    e.target.value = '';
    uploadAll(data.space.id, folderId, items, () => load(true));
  }

  function keydown(e) {
    if (dialog || ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName)) return;
    if (e.key === 'Delete' && selected.length && canDelete) dialog = { kind: 'delete' };
    if (e.key === 'F2' && single && canWrite) dialog = { kind: 'rename' };
    if (e.key === 'Escape') selected = [];
    if (e.key === 'Backspace' && data?.folder.parent_id) goto(`/f/${data.folder.parent_id}`);
  }
</script>

<svelte:window onkeydown={keydown} />

<div class="browser" class:withpanel={panelFile}>
  <section class="main" aria-label="Folder contents"
    ondragover={(e) => { if (e.dataTransfer.types.includes('Files')) { e.preventDefault(); dragging = true; } }}
    ondragleave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) dragging = false; }}
    ondrop={dropFiles}>
    {#if error}
      <p class="err">{error}</p>
    {:else if data}
      <div class="crumbs">
        {#each data.breadcrumbs as c, i (c.id)}
          {#if i > 0}<span class="sep">/</span>{/if}
          <button class="link" class:cur={i === data.breadcrumbs.length - 1} onclick={() => goto(`/f/${c.id}`)}>
            {i === 0 ? data.space.name : c.name}
          </button>
        {/each}
        {#if !canWrite}<span class="ro">read-only</span>{/if}
      </div>

      <div class="toolbar">
        {#if canWrite}
          <button class="primary" onclick={() => fileInput.click()}><Icon name="upload" size={16} /> Upload</button>
          <button onclick={() => dirInput.click()}><Icon name="folder" size={16} /> Upload folder</button>
          <button onclick={() => (dialog = { kind: 'mkdir' })}><Icon name="plus" size={16} /> New folder</button>
        {/if}
        {#if selected.length}
          <span class="divider"></span>
          {#if single?.kind === 'file'}<a class="btn" href={`/api/files/${single.link_id}/content`} download><Icon name="download" size={16} /> Download</a>{/if}
          {#if canWrite && single}<button onclick={() => (dialog = { kind: 'rename' })}><Icon name="edit" size={16} /> Rename</button>{/if}
          {#if canWrite}<button onclick={() => (dialog = { kind: 'pick', mode: 'move' })}><Icon name="move" size={16} /> Move</button>{/if}
          <button onclick={() => (dialog = { kind: 'pick', mode: 'copy' })}><Icon name="copy" size={16} /> Copy</button>
          {#if canWrite && single?.kind === 'file'}<button onclick={() => (dialog = { kind: 'pick', mode: 'link' })} title="Make the document additionally appear in another folder"><Icon name="link" size={16} /> Link to…</button>{/if}
          {#if canWrite && selRows.some((r) => r.kind === 'file' && r.original_path !== r.path)}
            <button onclick={restoreOriginal} title="Move back to where it was imported"><Icon name="restore" size={16} /> Original location</button>
          {/if}
          {#if canDelete}<button onclick={() => (dialog = { kind: 'delete' })}><Icon name="trash" size={16} /> Delete</button>{/if}
        {/if}
      </div>

      <FileTable {rows} view="folder" bind:selected onopen={open}
        ondropon={(keys, row) => canWrite && moveKeys(keys, row.id).catch(errorToast)}
        emptyText={canWrite ? 'This folder is empty – drop files or folders here to upload them.' : 'This folder is empty.'} />

      {#if dragging}
        <div class="dropzone"><Icon name="upload" size={40} /><p>Drop files or folders to upload into <strong>{data.folder.path === '/' ? data.space.name : data.folder.name}</strong></p><small>Folder structure is preserved. Files already stored are only linked.</small></div>
      {/if}
    {/if}
    <input type="file" multiple hidden bind:this={fileInput} onchange={pickFiles} />
    <input type="file" hidden webkitdirectory bind:this={dirInput} onchange={pickFiles} />
  </section>

  {#if panelFile}
    <DocumentPanel file={panelFile} onclose={() => (selected = [])} onchanged={() => load(true)} />
  {/if}
</div>

{#if dialog?.kind === 'mkdir'}
  <Prompt title="New folder" confirm="Create" onsubmit={mkdir} onclose={() => (dialog = null)} />
{:else if dialog?.kind === 'rename'}
  <Prompt title="Rename" value={single.name} confirm="Rename" onsubmit={rename} onclose={() => (dialog = null)} />
{:else if dialog?.kind === 'delete'}
  <Confirm title="Delete" danger confirm="Move to trash"
    message={`Move ${selected.length} item(s) to the trash? Documents that also appear elsewhere (links) stay available there.`}
    onconfirm={remove} onclose={() => (dialog = null)} />
{:else if dialog?.kind === 'pick'}
  <FolderPicker title={{ move: 'Move to…', copy: 'Copy to…', link: 'Add link in…' }[dialog.mode]}
    confirm={{ move: 'Move here', copy: 'Copy here', link: 'Link here' }[dialog.mode]}
    spaceId={data.space.id} sameSpace={dialog.mode === 'link'} exclude={selRows.filter((r) => r.kind === 'folder').map((r) => r.id)}
    onpick={(t) => pickTarget(t, dialog.mode)} onclose={() => (dialog = null)} />
{/if}

<style>
  .browser { display: grid; grid-template-columns: 1fr; height: 100%; min-height: 0; }
  .browser.withpanel { grid-template-columns: 1fr auto; }
  .main { position: relative; display: flex; flex-direction: column; padding: 14px 18px; min-width: 0; min-height: 0; }
  .crumbs { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; font-size: 1.05rem; margin-bottom: 10px; }
  .crumbs .cur { font-weight: 600; }
  .sep { color: var(--muted); }
  .ro { font-size: 0.75rem; background: var(--surface-2); padding: 1px 8px; border-radius: 99px; color: var(--muted); }
  .toolbar { display: flex; gap: 6px; flex-wrap: wrap; margin-bottom: 8px; }
  .divider { width: 1px; background: var(--border); margin: 0 4px; }
  .dropzone { position: absolute; inset: 10px; border: 3px dashed var(--accent); border-radius: 14px; background: color-mix(in srgb, var(--surface) 88%, transparent);
    display: flex; flex-direction: column; align-items: center; justify-content: center; color: var(--accent-text); pointer-events: none; z-index: 10; }
  .err { color: var(--err); }
  @media (max-width: 760px) { .main { padding: 10px 12px; } .toolbar button, .toolbar .btn { padding: 5px 9px; } }
</style>
