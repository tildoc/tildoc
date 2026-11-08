<script>
  import { openView } from '#lib/nav.js';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { del, cachedData, revalidate } from '#lib/api.js';
  import { session, toast, errorToast, refreshCollections, stored, store } from '#lib/state.svelte.js';
  import FileTable from '#lib/components/FileTable.svelte';
  import PeekPanel from '#lib/components/PeekPanel.svelte';
  import Icon from '#lib/components/Icon.svelte';
  import Confirm from '#lib/components/Confirm.svelte';
  import CollectionForm from '#lib/components/CollectionForm.svelte';
  import { watchBusy, isBusy } from '#lib/poll.svelte.js';
  import { watchLive } from '#lib/events.svelte.js';

  let data = $state(null);
  let error = $state('');
  let selected = $state([]);
  let dialog = $state(null);
  let peekOpen = $state(stored('peek', true));

  let id = $derived(page.params.id);

  function show(d, keep) {
    data = d;
    error = '';
    const want = page.url.searchParams.get('sel');
    if (!keep) selected = want ? [`l:${want}`] : [];
  }

  async function load(keep = false, quiet = false) {
    const cid = id;
    const path = `/collections/${cid}`;
    const cached = !keep && cachedData(path);
    if (cached) show(cached, false);
    try {
      const { data: d, changed } = await revalidate(path);
      if (cid !== id) return;
      if (changed || !data) show(d, keep || !!cached);
    } catch (e) {
      if (quiet) return;
      error = e.message;
      data = null;
    }
  }

  watchBusy(() => data?.items?.some((i) => isBusy(i.status)), () => load(true, true));
  // items come from several spaces; a rule collection can gain documents anywhere
  watchLive((ev) => ev.type === 'space' && (!!data?.collection?.tag_id || data?.items?.some((i) => i.space_id === ev.space_id)), () => load(true, true));

  $effect(() => {
    id;
    load();
  });

  let c = $derived(data?.collection);
  let rows = $derived(
    (data?.items ?? []).map((i) => ({ ...i, key: `l:${i.link_id}`, kind: 'file', space_slug: session.spaces.find((s) => s.id === i.space_id)?.slug }))
  );
  let selRows = $derived(rows.filter((r) => selected.includes(r.key)));
  let single = $derived(selRows.length === 1 ? selRows[0] : null);
  let panelFile = $derived(peekOpen && single ? single : null);
  let removable = $derived(selRows.filter((r) => r.manual));

  function open(row, opts = {}) {
    selected = [row.key];
    const q = new URLSearchParams({ c: id });
    if (opts.page > 1) q.set('page', opts.page);
    if (opts.version) q.set('v', opts.version);
    if (opts.variant) q.set('variant', opts.variant);
    openView(`/view/${row.link_id}?${q}`);
  }

  function peek(row) {
    if (peekOpen && single?.key === row.key) peekOpen = false;
    else {
      selected = [row.key];
      peekOpen = true;
    }
    store('peek', peekOpen);
  }

  function showPeek() {
    if (!peekOpen) {
      peekOpen = true;
      store('peek', true);
    }
  }

  async function removeItems() {
    try {
      for (const r of removable) await del(`/collections/${id}/items/${r.document_id}`);
      toast(`Removed ${removable.length} ${removable.length === 1 ? 'document' : 'documents'} from the collection`, 'success');
      if (removable.length < selRows.length) toast('Documents included through the tag stay until the tag is removed from them.');
      await refreshCollections();
      load();
    } catch (e) {
      errorToast(e);
    }
  }

  async function remove() {
    dialog = null;
    try {
      await del(`/collections/${id}`);
      await refreshCollections();
      toast('Deleted the collection. Its documents are unchanged.', 'success');
      goto('/');
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="browser" class:withpanel={panelFile}>
  <section class="main" aria-label="Collection contents">
    {#if error}
      <div class="empty-state"><strong>This collection can't be opened</strong>{error}</div>
    {:else if c}
      <header class="head">
        <p class="crumbs">Collection{c.team_name ? ` of team ${c.team_name}` : ', personal'}</p>
        <div class="titlerow">
          <h1>{c.name}</h1>
          {#if c.tag_name}<span class="pill"><Icon name="tag" size={13} /> includes everything tagged {c.tag_name}</span>{/if}
        </div>
      </header>

      <div class="toolbar" role="toolbar" aria-label="Collection actions">
        {#if single}
          <button class="ghost" onclick={() => open(single)}><Icon name="open" size={16} /> Open</button>
          <a class="btn ghost" href={`/api/files/${single.link_id}/content`} download><Icon name="download" size={16} /> Download</a>
          <button class="ghost" onclick={() => goto(`/f/${single.folder_id}?sel=${single.link_id}`)}><Icon name="folder" size={16} /> Show in folder</button>
        {/if}
        {#if c.can_edit && removable.length}
          <button class="ghost" onclick={removeItems}><Icon name="close" size={16} /> Remove from collection</button>
        {/if}
        <span class="spacer"></span>
        <a class="btn ghost" href={`/api/collections/${id}/export?format=zip`} download title="The documents you can see, as a ZIP archive"><Icon name="download" size={16} /> ZIP</a>
        <a class="btn ghost" href={`/api/collections/${id}/export?format=bundle`} download title="For importing into a tildoc server, with versions and tags"><Icon name="download" size={16} /> Export</a>
        {#if c.can_manage}
          <button class="ghost" onclick={() => (dialog = { kind: 'edit' })}><Icon name="edit" size={16} /> Edit</button>
          <button class="ghost" onclick={() => (dialog = { kind: 'delete' })}><Icon name="trash" size={16} /> Delete collection</button>
        {/if}
      </div>

      <FileTable {rows} view="collection" bind:selected onopen={open} onpeek={peek} onrowclick={showPeek}>
        {#snippet empty()}
          <div class="empty-state">
            <strong>This collection is empty</strong>
            Select documents in any folder and choose “Add to collection”{c.can_manage ? ', or include a tag under Edit' : ''}.
          </div>
        {/snippet}
      </FileTable>
    {/if}
  </section>

  {#if panelFile}
    <PeekPanel file={panelFile} onclose={() => { peekOpen = false; store('peek', false); }} onopen={(o) => open(panelFile, o)} onchanged={() => load(true)} />
  {/if}
</div>

{#if dialog?.kind === 'edit'}
  <CollectionForm collection={c} onsaved={() => { dialog = null; load(true); }} onclose={() => (dialog = null)} />
{:else if dialog?.kind === 'delete'}
  <Confirm title="Delete collection" danger confirm="Delete collection"
    message={`Delete the collection “${c.name}”? The documents in it stay where they are.`}
    onconfirm={remove} onclose={() => (dialog = null)} />
{/if}

<style>
  .browser { display: grid; grid-template-columns: minmax(0, 1fr); grid-template-rows: minmax(0, 1fr); height: 100%; min-height: 0; }
  .browser.withpanel { grid-template-columns: minmax(0, 1fr) auto; }
  .main { position: relative; display: flex; flex-direction: column; padding: 22px 28px 18px; min-width: 0; min-height: 0; }
  .head { margin-bottom: 14px; }
  .crumbs { font-size: var(--t-sm); color: var(--ink-2); margin: 0; min-height: 1.5em; }
  .titlerow { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
  .toolbar { display: flex; gap: 4px; flex-wrap: wrap; align-items: center; margin-bottom: 10px; min-height: 32px; }
  @media (max-width: 760px) { .main { padding: 14px 12px 10px; } }
</style>
