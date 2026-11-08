<script>
  // Ctrl+K palette: type, pick a document, open it in the viewer.
  import { openView } from '../nav.js';
  import { goto } from '$app/navigation';
  import Icon from './Icon.svelte';
  import { get } from '../api.js';
  import { session } from '../state.svelte.js';
  import { iconFor } from '../format.js';

  let { onclose } = $props();
  let q = $state('');
  let hits = $state([]);
  let total = $state(0);
  let sel = $state(0);
  let input = $state();
  let timer, gen = 0;

  $effect(() => input?.focus());

  function changed() {
    clearTimeout(timer);
    const v = q.trim();
    if (!v) {
      hits = [];
      total = 0;
      return;
    }
    timer = setTimeout(async () => {
      const my = ++gen;
      try {
        const r = await get(`/search?${new URLSearchParams({ q: v, limit: 8 })}`);
        if (my !== gen) return;
        hits = r.hits;
        total = r.total;
        sel = 0;
      } catch {}
    }, 180);
  }

  function open(h) {
    onclose();
    const p = new URLSearchParams({ q: q.trim() });
    if (h.page) p.set('page', h.page);
    openView(`/view/${h.link_id}?${p}`);
  }
  function all() {
    onclose();
    goto(`/search?${new URLSearchParams({ q: q.trim() })}`);
  }

  function key(e) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onclose();
    } else if (e.key === 'ArrowDown') {
      e.preventDefault();
      sel = Math.min(hits.length, sel + 1);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      sel = Math.max(0, sel - 1);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      if (sel < hits.length) open(hits[sel]);
      else if (q.trim()) all();
    }
  }

  const slug = (id) => session.spaces.find((s) => s.id === id)?.slug ?? '';
  // ts_headline marks hits with << >>; escape everything else
  function snippet(s) {
    const esc = s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    return esc.replace(/&lt;&lt;/g, '<mark>').replace(/&gt;&gt;/g, '</mark>');
  }
</script>

<div class="backdrop" role="presentation" onclick={(e) => e.target === e.currentTarget && onclose()}>
  <div class="palette" role="dialog" aria-modal="true" aria-label="Search documents">
    <div class="field">
      <Icon name="search" />
      <input bind:this={input} bind:value={q} oninput={changed} onkeydown={key} placeholder="Search names and contents"
        aria-label="Search names and contents" role="combobox" aria-expanded={hits.length > 0} aria-controls="qs-list" aria-activedescendant={hits.length ? `qs-${sel}` : undefined} />
      <kbd>Esc</kbd>
    </div>
    {#if q.trim()}
      <ul id="qs-list" role="listbox">
        {#each hits as h, i (h.link_id)}
          <li id="qs-{i}" role="option" aria-selected={sel === i}>
            <button class:sel={sel === i} onmousemove={() => (sel = i)} onclick={() => open(h)}>
              <span class="ic {iconFor({ kind: 'file', mime: h.mime })}"><Icon name={iconFor({ kind: 'file', mime: h.mime })} /></span>
              <span class="txt">
                <span class="nm">{h.name}{#if h.page}<span class="pg">page {h.page}</span>{/if}</span>
                {#if h.snippet}<span class="snip">{@html snippet(h.snippet)}</span>{:else}<span class="path">{slug(h.space_id)}{h.path}</span>{/if}
              </span>
            </button>
          </li>
        {/each}
        <li id="qs-{hits.length}" role="option" aria-selected={sel === hits.length}>
          <button class="all" class:sel={sel === hits.length} onmousemove={() => (sel = hits.length)} onclick={all}>
            {total > hits.length ? `Show all ${total} results` : hits.length ? 'Show results with filters' : 'No documents match. Search with filters'}
          </button>
        </li>
      </ul>
    {:else}
      <p class="tip">Finds words inside documents, including scanned pages after OCR. Use quotes for an exact phrase and a minus sign to exclude a word.</p>
    {/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: var(--scrim); z-index: 60; display: flex; justify-content: center; align-items: flex-start; padding: 12vh 16px 16px; }
  .palette { width: min(640px, 100%); background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); box-shadow: var(--pop-shadow); overflow: hidden; }
  .field { display: flex; align-items: center; gap: 10px; padding: 8px 14px; color: var(--ink-2); border-bottom: 1px solid var(--line); }
  .field input { border: 0; background: none; font-size: var(--t-md); padding: 6px 0; outline: none; }
  ul { list-style: none; margin: 0; padding: 6px; max-height: 60vh; overflow: auto; }
  li button { width: 100%; justify-content: flex-start; align-items: flex-start; text-align: left; gap: 10px; border: 0; background: none; padding: 8px 10px; white-space: normal; }
  li button.sel { background: var(--accent-soft); }
  .ic { display: inline-flex; color: var(--ink-2); padding-top: 2px; }
  .ic.pdf { color: var(--err); }
  .txt { display: flex; flex-direction: column; min-width: 0; gap: 2px; }
  .nm { font-weight: 600; display: flex; gap: 8px; align-items: baseline; }
  .pg { font-weight: 400; font-size: var(--t-sm); color: var(--ink-2); }
  .snip, .path { font-size: var(--t-sm); color: var(--ink-2); overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .snip :global(mark) { color: var(--ink); }
  .all { color: var(--accent); font-weight: 600; }
  .tip { margin: 0; padding: 14px 16px; color: var(--ink-2); font-size: var(--t-sm); }
</style>
