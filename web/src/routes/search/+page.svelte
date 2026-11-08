<script>
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { get } from '#lib/api.js';
  import { session, errorToast } from '#lib/state.svelte.js';
  import { size, date, shortType, statusLabel, iconFor } from '#lib/format.js';
  import Icon from '#lib/components/Icon.svelte';

  let result = $state(null);
  let space = $state(''), mime = $state(''), status = $state('');
  let offset = $state(0);
  let input = $state(page.url.searchParams.get('q') ?? '');
  const limit = 50;

  let q = $derived(page.url.searchParams.get('q') ?? '');
  let folder = $derived(page.url.searchParams.get('folder') ?? '');

  $effect(() => {
    input = q;
  });

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

  function submit(e) {
    e.preventDefault();
    offset = 0;
    const p = new URLSearchParams({ q: input.trim() });
    if (folder) p.set('folder', folder);
    goto(`/search?${p}`, { keepFocus: true });
  }

  function open(h) {
    const p = new URLSearchParams();
    if (q) p.set('q', q);
    if (h.page) p.set('page', h.page);
    goto(`/view/${h.link_id}${p.size ? '?' + p : ''}`);
  }

  const slug = (id) => session.spaces.find((s) => s.id === id)?.slug ?? '';
  function snippet(s) {
    const esc = s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return esc.replace(/&lt;&lt;/g, '<mark>').replace(/&gt;&gt;/g, '</mark>');
  }
  const dir = (h) => h.path.slice(0, h.path.length - h.name.length - 1) || '/';
</script>

<div class="page">
  <header>
    <h1>{q ? 'Search' : 'All documents'}</h1>
  </header>

  <form class="searchbar" onsubmit={submit} role="search">
    <Icon name="search" />
    <input bind:value={input} placeholder="Words in names and contents" aria-label="Search names and contents" />
    <button class="primary">Search</button>
  </form>

  <div class="filters row">
    <select bind:value={space} aria-label="Where" onchange={() => (offset = 0)}>
      <option value="">{folder ? 'This folder and its subfolders' : 'All spaces'}</option>
      {#each session.spaces as s}<option value={s.id}>{s.name}</option>{/each}
    </select>
    <select bind:value={mime} aria-label="Type" onchange={() => (offset = 0)}>
      <option value="">All types</option>
      <option value="application/pdf">PDF</option>
      <option value="text/">Text</option>
      <option value="image/">Images</option>
    </select>
    <select bind:value={status} aria-label="Text status" onchange={() => (offset = 0)}>
      <option value="">Any text status</option>
      <option value="done">Indexed</option>
      <option value="ocr_pending">Waiting for OCR</option>
      <option value="pending">Queued</option>
      <option value="unsupported">Stored only</option>
      <option value="failed">Failed</option>
    </select>
    {#if result}<span class="muted total" aria-live="polite">{result.total} {result.total === 1 ? 'document' : 'documents'}</span>{/if}
  </div>

  {#if result}
    {#if result.hits.length}
      <ol class="results">
        {#each result.hits as h (h.link_id)}
          <li>
            <span class="ic {iconFor({ kind: 'file', mime: h.mime })}"><Icon name={iconFor({ kind: 'file', mime: h.mime })} size={20} /></span>
            <div class="body">
              <button class="link name" onclick={() => open(h)}>{h.name}</button>
              {#if h.snippet}<p class="snip">{@html snippet(h.snippet)}</p>{/if}
              <div class="meta">
                {#if h.page}<span class="pill">page {h.page}</span>{/if}
                <span>{shortType(h.mime, h.name)}, {size(h.size)}</span>
                <span>changed {date(h.modified_at)}</span>
                {#if h.status !== 'done'}<span class="pill status {h.status}">{statusLabel[h.status] ?? h.status}</span>{/if}
                <button class="link where" onclick={() => goto(`/f/${h.folder_id}?sel=${h.link_id}`)} title="Show in folder">{slug(h.space_id)}{dir(h)}</button>
              </div>
            </div>
          </li>
        {/each}
      </ol>
      {#if result.total > limit}
        <div class="row pager">
          <button disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - limit))}>Previous</button>
          <span class="muted">{offset + 1} to {Math.min(offset + limit, result.total)} of {result.total}</span>
          <button disabled={offset + limit >= result.total} onclick={() => (offset += limit)}>Next</button>
        </div>
      {/if}
    {:else}
      <div class="empty-state">
        <strong>No documents match</strong>
        Try fewer or different words. Scanned PDFs become searchable once OCR has run.
      </div>
    {/if}
  {/if}
</div>

<style>
  .searchbar { display: flex; align-items: center; gap: 10px; max-width: 760px; padding: 4px 4px 4px 14px; border: 1px solid var(--line); border-radius: var(--r-panel); background: var(--surface); color: var(--ink-2); }
  .searchbar input { border: 0; background: none; font-size: var(--t-md); outline: none; padding: 6px 0; }
  .searchbar:focus-within { border-color: var(--accent); }
  .filters { margin: 14px 0 6px; }
  .filters select { width: auto; }
  .total { margin-left: 4px; }
  .results { list-style: none; margin: 8px 0 0; padding: 0; max-width: 860px; }
  .results li { display: flex; gap: 14px; padding: 16px 0; border-bottom: 1px solid var(--line); }
  .ic { color: var(--ink-2); padding-top: 3px; flex: none; }
  .ic.pdf { color: var(--err); }
  .body { min-width: 0; flex: 1; }
  .name { font-family: var(--font-read); font-size: var(--t-md); font-weight: 560; color: var(--ink); overflow-wrap: anywhere; }
  .name:hover { color: var(--accent); }
  .snip { margin: 4px 0 6px; font-family: var(--font-read); line-height: 1.6; color: var(--ink); }
  .meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 14px; font-size: var(--t-sm); color: var(--ink-2); }
  .where { color: var(--ink-2); overflow-wrap: anywhere; }
  .pager { margin-top: 18px; }
</style>
