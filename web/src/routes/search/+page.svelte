<script>
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { get } from '#lib/api.js';
  import { session, errorToast } from '#lib/state.svelte.js';
  import FileTable from '#lib/components/FileTable.svelte';
  import DocumentPanel from '#lib/components/DocumentPanel.svelte';

  let result = $state(null);
  let selected = $state([]);
  let space = $state(''), mime = $state(''), status = $state('');
  let offset = $state(0);
  const limit = 200;

  let q = $derived(page.url.searchParams.get('q') ?? '');
  let folder = $derived(page.url.searchParams.get('folder') ?? '');

  async function load() {
    const params = new URLSearchParams({ q, limit, offset });
    if (space) params.set('space', space);
    if (folder && !space) params.set('folder', folder);
    if (mime) params.set('mime', mime);
    if (status) params.set('status', status);
    try {
      result = await get(`/search?${params}`);
    } catch (e) {
      errorToast(e);
    }
  }

  $effect(() => {
    q; folder; space; mime; status; offset;
    load();
  });

  let rows = $derived(
    (result?.hits ?? []).map((h) => ({
      ...h, key: `l:${h.link_id}`, kind: 'file',
      space_slug: session.spaces.find((s) => s.id === h.space_id)?.slug
    }))
  );
  let sel = $derived(rows.find((r) => selected.length === 1 && r.key === selected[0]));
</script>

<div class="browser" class:withpanel={sel}>
  <section class="main">
    <h1>{q ? `Results for “${q}”` : 'All documents'}</h1>
    <div class="filters row">
      <select bind:value={space} aria-label="Space">
        <option value="">{folder ? 'Current folder (incl. subfolders)' : 'All spaces'}</option>
        {#each session.spaces as s}<option value={s.id}>{s.name}</option>{/each}
      </select>
      <select bind:value={mime} aria-label="Type">
        <option value="">All types</option>
        <option value="application/pdf">PDF</option>
        <option value="text/">Text</option>
        <option value="image/">Images</option>
      </select>
      <select bind:value={status} aria-label="Status">
        <option value="">Any status</option>
        <option value="done">Indexed</option>
        <option value="ocr_pending">OCR pending</option>
        <option value="pending">Queued</option>
        <option value="unsupported">Stored only</option>
        <option value="failed">Failed</option>
      </select>
      {#if result}<span class="muted">{result.total} match{result.total === 1 ? '' : 'es'}</span>{/if}
    </div>
    {#if result}
      <FileTable {rows} view="search" bind:selected sortable={true}
        onopen={(r) => goto(`/f/${r.folder_id}?sel=${r.link_id}`)} emptyText="Nothing found." />
      {#each result.hits.filter((h) => h.snippet && selected.includes(`l:${h.link_id}`)) as h}
        <p class="snippet">{@html h.snippet.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/&lt;&lt;/g, '<mark>').replace(/&gt;&gt;/g, '</mark>')}</p>
      {/each}
      {#if result.total > limit}
        <div class="row">
          <button disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - limit))}>Previous</button>
          <span class="muted">{offset + 1}–{Math.min(offset + limit, result.total)} of {result.total}</span>
          <button disabled={offset + limit >= result.total} onclick={() => (offset += limit)}>Next</button>
        </div>
      {/if}
    {/if}
  </section>
  {#if sel}<DocumentPanel file={sel} onclose={() => (selected = [])} />{/if}
</div>

<style>
  .browser { display: grid; grid-template-columns: 1fr; height: 100%; }
  .browser.withpanel { grid-template-columns: 1fr auto; }
  .main { display: flex; flex-direction: column; padding: 14px 18px; min-width: 0; min-height: 0; gap: 6px; }
  .filters select { width: auto; }
  .snippet { background: var(--surface); border: 1px solid var(--border); border-radius: 8px; padding: 8px 12px; margin: 6px 0 0; font-size: 0.88rem; }
  .snippet :global(mark) { background: var(--warn-soft); color: inherit; padding: 0 2px; border-radius: 3px; }
</style>
