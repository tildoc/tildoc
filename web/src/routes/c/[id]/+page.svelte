<script>
  import { tx } from '#lib/i18n/index.js';
  import { openView, syncSelection } from '#lib/nav.js';
  import { untrack } from 'svelte';
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
    if (!keep && want && d.items?.some((i) => i.link_id === want)) peekOpen = true; // a link to a file shows it (not stored)
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
    untrack(() => load()); // only the collection id triggers loading, not URL changes
  });

  // the address bar names the selected file (?sel=), ready to be shared
  $effect(() => {
    const sel = single?.kind === 'file' ? single.link_id : null;
    if (data) untrack(() => syncSelection(sel));
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
    if (opts.office) q.set('office', '1');
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
      toast(tx('c_removed_from_collection', { count: removable.length }), 'success');
      if (removable.length < selRows.length) toast(tx('c_documents_included_through_the_tag_stay'));
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
      toast(tx('c_deleted_the_collection_its_documents_are'), 'success');
      goto('/');
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="browser" class:withpanel={panelFile}>
  <section class="main" aria-label={tx('c_collection_contents')}>
    {#if error}
      <div class="empty-state"><strong>{tx('c_this_collection_can_t_be_opened')}</strong>{error}</div>
    {:else if c}
      <header class="head">
        <p class="crumbs">{c.team_name ? tx('c_collection_of_team', { team: c.team_name }) : tx('c_collection_personal')}</p>
        <div class="titlerow">
          <h1>{c.name}</h1>
          {#if c.tag_name}<span class="pill"><Icon name="tag" size={13} /> {tx('c_includes_everything_tagged_x', { tag_name: c.tag_name })}</span>{/if}
        </div>
      </header>

      <div class="toolbar" role="toolbar" aria-label={tx('c_collection_actions')}>
        {#if single}
          <button class="ghost" onclick={() => open(single)}><Icon name="open" size={16} /> {tx('common_open')}</button>
          {#if session.spaces.find((x) => x.id === single.space_id)?.caps.includes('download')}<a class="btn ghost" href={`/api/v1/files/${single.link_id}/content`} download><Icon name="download" size={16} /> {tx('common_download')}</a>{/if}
          <button class="ghost" onclick={() => goto(`/f/${single.folder_id}?sel=${single.link_id}`)}><Icon name="folder" size={16} /> {tx('c_show_in_folder')}</button>
        {/if}
        {#if c.can_edit && removable.length}
          <button class="ghost" onclick={removeItems}><Icon name="close" size={16} /> {tx('c_remove_from_collection')}</button>
        {/if}
        <span class="spacer"></span>
        <a class="btn ghost" href={`/api/v1/collections/${id}/export?format=zip`} download title={tx('c_the_documents_you_can_see_as')}><Icon name="download" size={16} /> {tx('c_zip')}</a>
        <a class="btn ghost" href={`/api/v1/collections/${id}/export?format=bundle`} download title={tx('c_for_importing_into_a_tildoc_server')}><Icon name="download" size={16} /> {tx('c_export')}</a>
        {#if c.can_manage}
          <button class="ghost" onclick={() => (dialog = { kind: 'edit' })}><Icon name="edit" size={16} /> {tx('c_edit')}</button>
          <button class="ghost" onclick={() => (dialog = { kind: 'delete' })}><Icon name="trash" size={16} /> {tx('c_delete_collection')}</button>
        {/if}
      </div>

      <FileTable {rows} view="collection" bind:selected onopen={open} onpeek={peek} onrowclick={showPeek}>
        {#snippet empty()}
          <div class="empty-state">
            <strong>{tx('c_this_collection_is_empty')}</strong>
            {c.can_manage ? tx('c_select_documents_or_tag') : tx('c_select_documents')}
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
  <Confirm title={tx('c_delete_collection')} danger confirm={tx('c_delete_collection')}
    message={tx('c_delete_the_collection_x_the_documents', { collection: c.name })}
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
