<script>
  import { tx } from '#lib/i18n/index.js';
  import { openView } from '#lib/nav.js';
  import { preloadDoc, cancelPreload } from '#lib/preload.js';
  import { page } from '$app/state';
  import { goto } from '$app/navigation';
  import { get } from '#lib/api.js';
  import { session, errorToast, refreshTags } from '#lib/state.svelte.js';
  import { size, date, shortType, statusLabel, iconFor } from '#lib/format.js';
  import Icon from '#lib/components/Icon.svelte';
  import TagChips from '#lib/components/TagChips.svelte';

  let result = $state(null);
  let space = $state(''), mime = $state(''), status = $state('');
  let tag = $state(page.url.searchParams.get('tag') ?? '');
  let coll = $state(page.url.searchParams.get('collection') ?? '');

  $effect(() => {
    refreshTags().catch(() => {});
  });
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
    if (tag) params.set('tag', tag);
    if (coll) params.set('collection', coll);
    try {
      result = await get(`/search?${params}`);
    } catch (e) {
      errorToast(e);
    }
  }

  $effect(() => {
    q; folder; space; mime; status; tag; coll; offset;
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
    openView(`/view/${h.link_id}${p.size ? '?' + p : ''}`);
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
    <h1>{q || tag || coll ? tx('layout_search') : tx('layout_all_documents')}</h1>
  </header>

  <form class="searchbar" onsubmit={submit} role="search">
    <Icon name="search" />
    <input bind:value={input} placeholder={tx('search_words_in_names_and_contents')} aria-label={tx('quicksearch_search_names_and_contents')} />
    <button class="primary">{tx('layout_search')}</button>
  </form>

  <div class="filters row">
    <select bind:value={space} aria-label={tx('search_where')} onchange={() => (offset = 0)}>
      <option value="">{folder ? tx('search_this_folder_and_its_subfolders') : tx('search_all_spaces')}</option>
      {#each session.spaces as s}<option value={s.id}>{s.name}</option>{/each}
    </select>
    <select bind:value={mime} aria-label={tx('common_type')} onchange={() => (offset = 0)}>
      <option value="">{tx('search_all_types')}</option>
      <option value="application/pdf">{tx('search_pdf')}</option>
      <option value="text/">{tx('docdetails_text')}</option>
      <option value="image/">{tx('search_images')}</option>
    </select>
    <select bind:value={status} aria-label={tx('search_text_status')} onchange={() => (offset = 0)}>
      <option value="">{tx('search_any_text_status')}</option>
      <option value="done">{tx('search_indexed')}</option>
      <option value="ocr_pending">{tx('search_waiting_for_ocr')}</option>
      <option value="pending">{tx('search_queued')}</option>
      <option value="unsupported">{tx('search_stored_only')}</option>
      <option value="failed">{tx('search_failed')}</option>
    </select>
    <select bind:value={tag} aria-label={tx('admin_tag')} onchange={() => (offset = 0)}>
      <option value="">{tx('search_any_tag')}</option>
      {#each session.tags.filter((t) => t.count > 0 || t.id === tag) as t (t.id)}<option value={t.id}>{t.name} ({t.count})</option>{/each}
    </select>
    {#if session.collections.length}
      <select bind:value={coll} aria-label={tx('search_collection')} onchange={() => (offset = 0)}>
        <option value="">{tx('search_in_any_collection_or_none')}</option>
        {#each session.collections as c (c.id)}<option value={c.id}>{c.name}{c.team_name ? ` (${c.team_name})` : ''}</option>{/each}
      </select>
    {/if}
    {#if result}<span class="muted total" aria-live="polite">{tx('search_total', { count: result.total })}</span>{/if}
  </div>

  {#if result}
    {#if result.hits.length}
      <ol class="results">
        {#each result.hits as h (h.link_id)}
          <li>
            <span class="ic {iconFor({ kind: 'file', mime: h.mime })}"><Icon name={iconFor({ kind: 'file', mime: h.mime })} size={20} /></span>
            <div class="body">
              <button class="link name" onclick={() => open(h)} onpointerenter={() => preloadDoc(h)} onpointerleave={cancelPreload} onfocus={() => preloadDoc(h)}>{h.name}</button>
              {#if h.snippet}<p class="snip">{@html snippet(h.snippet)}</p>{/if}
              <div class="meta">
                {#if h.page}<span class="pill">{tx('quicksearch_page_x', { page: h.page })}</span>{/if}
                <span>{shortType(h.mime, h.name)}, {size(h.size)}</span>
                <span>{tx('search_changed_x', { modified_at: date(h.modified_at) })}</span>
                {#if h.status !== 'done'}<span class="pill status {h.status}">{statusLabel[h.status] ?? h.status}</span>{/if}
                <TagChips tags={h.tags} small onpick={(t) => { tag = t.id; offset = 0; }} />
                <button class="link where" onclick={() => goto(`/f/${h.folder_id}?sel=${h.link_id}`)} title={tx('c_show_in_folder')}>{slug(h.space_id)}{dir(h)}</button>
              </div>
            </div>
          </li>
        {/each}
      </ol>
      {#if result.total > limit}
        <div class="row pager">
          <button disabled={offset === 0} onclick={() => (offset = Math.max(0, offset - limit))}>{tx('search_previous')}</button>
          <span class="muted">{tx('search_x_to_x_of_x', { from: offset + 1, to: Math.min(offset + limit, result.total), total: result.total })}</span>
          <button disabled={offset + limit >= result.total} onclick={() => (offset += limit)}>{tx('search_next')}</button>
        </div>
      {/if}
    {:else}
      <div class="empty-state">
        <strong>{tx('search_no_documents_match')}</strong>
        {tx('search_try_fewer_or_different_words_scanned')}
      </div>
    {/if}
  {/if}
</div>

<style>
  .searchbar { display: flex; align-items: center; gap: 10px; padding: 4px 4px 4px 14px; border: 1px solid var(--line); border-radius: var(--r-panel); background: var(--surface); color: var(--ink-2); }
  .searchbar input { border: 0; background: none; font-size: var(--t-md); outline: none; padding: 6px 0; }
  .searchbar:focus-within { border-color: var(--accent); }
  .filters { margin: 14px 0 6px; }
  .filters select { width: auto; }
  .total { margin-left: 4px; }
  .results { list-style: none; margin: 8px 0 0; padding: 0; }
  .results li { display: flex; gap: 14px; padding: 16px 0; border-bottom: 1px solid var(--line); }
  .ic { color: var(--ink-2); padding-top: 3px; flex: none; }
  .ic.pdf { color: var(--err); }
  .body { min-width: 0; flex: 1; }
  .name { font-size: var(--t-md); font-weight: 650; color: var(--ink); overflow-wrap: anywhere; }
  .name:hover { color: var(--accent); }
  .snip { margin: 4px 0 6px; line-height: 1.6; color: var(--ink); }
  .meta { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 14px; font-size: var(--t-sm); color: var(--ink-2); }
  .where { color: var(--ink-2); overflow-wrap: anywhere; }
  .pager { margin-top: 18px; }
</style>
