<script>
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { get, post, patch, del } from '#lib/api.js';
  import { toast, errorToast, bumpTree, session, stored, store, refreshCollections } from '#lib/state.svelte.js';
  import { collectDrop, collectInput, uploadAll } from '#lib/upload.js';
  import { moveKeys, splitKeys } from '#lib/actions.js';
  import FileTable from '#lib/components/FileTable.svelte';
  import PeekPanel from '#lib/components/PeekPanel.svelte';
  import Icon from '#lib/components/Icon.svelte';
  import Prompt from '#lib/components/Prompt.svelte';
  import Confirm from '#lib/components/Confirm.svelte';
  import FolderPicker from '#lib/components/FolderPicker.svelte';
  import CollectionPicker from '#lib/components/CollectionPicker.svelte';
  import ImportDialog from '#lib/components/ImportDialog.svelte';
  import { watchBusy, isBusy } from '#lib/poll.svelte.js';

  let data = $state(null);
  let error = $state('');
  let selected = $state([]);
  let dialog = $state(null); // {kind, ...}
  let dragging = $state(false);
  let peekOpen = $state(stored('peek', true));
  let fileInput, dirInput;

  let folderId = $derived(page.params.id);

  // quiet: background refresh (processing status); errors keep the current view
  async function load(keepSelection = false, quiet = false) {
    const id = folderId;
    try {
      const d = await get(`/folders/${id}`);
      if (id !== folderId) return;
      data = d;
      error = '';
      const want = page.url.searchParams.get('sel');
      if (want && !keepSelection) selected = [`l:${want}`];
      else if (!keepSelection) selected = [];
      else selected = selected.filter((k) => rows.some((r) => r.key === k));
    } catch (e) {
      if (quiet) return;
      error = e.message;
      data = null;
    }
  }

  watchBusy(() => data?.files.some((f) => isBusy(f.status)), () => load(true, true));

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
  let panelFile = $derived(peekOpen && single?.kind === 'file' ? single : null);

  function open(row, opts = {}) {
    if (row.kind === 'folder') {
      goto(`/f/${row.id}`);
      return;
    }
    selected = [row.key];
    const q = new URLSearchParams();
    if (opts.page > 1) q.set('page', opts.page);
    if (opts.version) q.set('v', opts.version);
    goto(`/view/${row.link_id}${q.size ? '?' + q : ''}`);
  }

  function peek(row) {
    if (row.kind !== 'file') return;
    if (peekOpen && single?.key === row.key) peekOpen = false;
    else {
      selected = [row.key];
      peekOpen = true;
    }
    store('peek', peekOpen);
  }

  // a plain click on a file row always shows its preview; the name opens the viewer
  function showPeek(row) {
    if (row.kind !== 'file' || peekOpen) return;
    peekOpen = true;
    store('peek', true);
  }

  async function addToCollection(c) {
    const docs = [...new Set(selRows.filter((r) => r.kind === 'file').map((r) => r.document_id))];
    dialog = null;
    try {
      await post(`/collections/${c.id}/items`, { document_ids: docs });
      await refreshCollections();
      toast(`Added ${docs.length} ${docs.length === 1 ? 'document' : 'documents'} to ${c.name}`, 'success');
    } catch (e) {
      errorToast(e);
    }
  }

  function closePeek() {
    peekOpen = false;
    store('peek', false);
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
    if (e.defaultPrevented || dialog || ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName)) return;
    if (e.key === 'Delete' && selected.length && canDelete) dialog = { kind: 'delete' };
    if (e.key === 'F2' && single && canWrite) dialog = { kind: 'rename' };
    if (e.key === 'Escape') selected = [];
    if (e.key === 'Backspace' && data?.folder.parent_id) goto(`/f/${data.folder.parent_id}`);
    if (e.key.toLowerCase() === 'u' && !e.ctrlKey && !e.metaKey && canWrite) fileInput.click();
  }
</script>

<svelte:window onkeydown={keydown} />

<div class="browser" class:withpanel={panelFile}>
  <section class="main" aria-label="Folder contents"
    ondragover={(e) => { if (e.dataTransfer.types.includes('Files') && canWrite) { e.preventDefault(); dragging = true; } }}
    ondragleave={(e) => { if (!e.currentTarget.contains(e.relatedTarget)) dragging = false; }}
    ondrop={dropFiles}>
    {#if error}
      <div class="empty-state"><strong>This folder can't be opened</strong>{error}</div>
    {:else if data}
      <header class="head">
        <nav class="crumbs" aria-label="Folder path">
          {#each data.breadcrumbs.slice(0, -1) as c, i (c.id)}
            <button class="link" onclick={() => goto(`/f/${c.id}`)}>{i === 0 ? data.space.name : c.name}</button><span class="sep" aria-hidden="true">/</span>
          {/each}
        </nav>
        <div class="titlerow">
          <h1>{data.breadcrumbs.length === 1 ? data.space.name : data.folder.name}</h1>
          {#if !canWrite}<span class="pill">Read only</span>{/if}
        </div>
      </header>

      <div class="toolbar" role="toolbar" aria-label="Folder actions">
        {#if canWrite}
          <button class="primary" onclick={() => fileInput.click()} title="Upload files (U)"><Icon name="upload" size={16} /> Upload</button>
          <button onclick={() => dirInput.click()}><Icon name="folder" size={16} /> Upload folder</button>
          <button class="ghost" onclick={() => (dialog = { kind: 'mkdir' })}><Icon name="plus" size={16} /> New folder</button>
        {/if}
        {#if !selected.length}
          <span class="divider" aria-hidden="true"></span>
          {#if data.space.caps?.includes('export')}<a class="btn ghost" href={`/api/folders/${folderId}/export`} download title="This folder with all subfolders as a ZIP archive"><Icon name="download" size={16} /> Export as ZIP</a>{/if}
          {#if canWrite}<button class="ghost" onclick={() => (dialog = { kind: 'import' })} title="Import a ZIP or a tildoc export into this folder"><Icon name="upload" size={16} /> Import</button>{/if}
        {/if}
        {#if selected.length}
          <span class="divider" aria-hidden="true"></span>
          {#if single?.kind === 'file'}
            <button class="ghost" onclick={() => open(single)}><Icon name="open" size={16} /> Open</button>
            <a class="btn ghost" href={`/api/files/${single.link_id}/content`} download><Icon name="download" size={16} /> Download</a>
          {/if}
          {#if canWrite && single}<button class="ghost" onclick={() => (dialog = { kind: 'rename' })}><Icon name="edit" size={16} /> Rename</button>{/if}
          {#if canWrite}<button class="ghost" onclick={() => (dialog = { kind: 'pick', mode: 'move' })}><Icon name="move" size={16} /> Move</button>{/if}
          <button class="ghost" onclick={() => (dialog = { kind: 'pick', mode: 'copy' })}><Icon name="copy" size={16} /> Copy</button>
          {#if canWrite && single?.kind === 'file'}<button class="ghost" onclick={() => (dialog = { kind: 'pick', mode: 'link' })} title="Show the same document in another folder too"><Icon name="link" size={16} /> Also show in…</button>{/if}
          {#if canWrite && selRows.some((r) => r.kind === 'file' && r.original_path !== r.path)}
            <button class="ghost" onclick={restoreOriginal} title="Move back to where it was imported"><Icon name="restore" size={16} /> Move to original place</button>
          {/if}
          {#if selRows.some((r) => r.kind === 'file')}<button class="ghost" onclick={() => (dialog = { kind: 'collect' })}><Icon name="collection" size={16} /> Add to collection</button>{/if}
          {#if canDelete}<button class="ghost" onclick={() => (dialog = { kind: 'delete' })}><Icon name="trash" size={16} /> Delete</button>{/if}
        {/if}
        {#if single?.kind === 'file' && !peekOpen}
          <button class="ghost peekbtn" onclick={() => peek(single)} title="Show preview (Space)"><Icon name="panel" size={16} /> Preview</button>
        {/if}
      </div>

      <FileTable {rows} view="folder" bind:selected onopen={open} onpeek={peek} onrowclick={showPeek}
        ondropon={(keys, row) => canWrite && moveKeys(keys, row.id).catch(errorToast)}>
        {#snippet empty()}
          <div class="empty-state">
            <strong>This folder is empty</strong>
            {#if canWrite}Drop files or whole folders here, or press <kbd>U</kbd> to upload. Folder structure is kept.{:else}Nothing has been added here yet.{/if}
          </div>
        {/snippet}
      </FileTable>

      {#if dragging}
        <div class="dropzone" aria-hidden="true">
          <Icon name="upload" size={36} />
          <p>Drop to upload into <strong>{data.breadcrumbs.length === 1 ? data.space.name : data.folder.name}</strong></p>
          <small>Folders keep their structure. Files that are already stored are only added as another place.</small>
        </div>
      {/if}
    {/if}
    <input type="file" multiple hidden bind:this={fileInput} onchange={pickFiles} />
    <input type="file" hidden webkitdirectory bind:this={dirInput} onchange={pickFiles} />
  </section>

  {#if panelFile}
    <PeekPanel file={panelFile} onclose={closePeek} onopen={(o) => open(panelFile, o)} onchanged={() => load(true)} />
  {/if}
</div>

{#if dialog?.kind === 'import'}
  <ImportDialog folder={{ id: folderId, name: data.breadcrumbs.length === 1 ? data.space.name : data.folder.name }}
    onclose={() => (dialog = null)} ondone={() => load(true)} />
{:else if dialog?.kind === 'collect'}
  <CollectionPicker count={selRows.filter((r) => r.kind === 'file').length} onpick={addToCollection} onclose={() => (dialog = null)} />
{:else if dialog?.kind === 'mkdir'}
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
  .browser { display: grid; grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr); height: 100%; min-height: 0; }
  .browser.withpanel { grid-template-columns: minmax(0, 1fr) auto; }
  .main { position: relative; display: flex; flex-direction: column; padding: 22px 28px 18px; min-width: 0; min-height: 0; }
  .head { margin-bottom: 14px; }
  .crumbs { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; font-size: var(--t-sm); color: var(--ink-2); min-height: 1.5em; }
  .crumbs .link:hover { color: var(--ink); }
  .sep { color: var(--line); }
  .titlerow { display: flex; align-items: center; gap: 12px; }
  .titlerow h1 { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .toolbar { display: flex; gap: 4px; flex-wrap: wrap; align-items: center; margin-bottom: 10px; }
  .toolbar > .primary { margin-right: 4px; }
  .peekbtn { margin-left: auto; }
  .divider { width: 1px; align-self: stretch; background: var(--line); margin: 4px 6px; }
  .dropzone { position: absolute; inset: 12px; border: 2px dashed var(--accent); border-radius: var(--r-panel); background: color-mix(in srgb, var(--surface) 92%, transparent);
    display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 4px; color: var(--accent-ink); pointer-events: none; z-index: 10; text-align: center; padding: 16px; }
  .dropzone p { margin: 8px 0 0; font-size: var(--t-md); }
  .dropzone small { color: var(--ink-2); }
  @media (max-width: 760px) {
    .main { padding: 14px 12px 10px; }
    .toolbar button, .toolbar .btn { padding: 4px 8px; }
  }
</style>
