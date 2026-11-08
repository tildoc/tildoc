<script>
  // Full-window document viewer: /view/<linkId>?page=&q=&v=
  import { tick, untrack } from 'svelte';
  import { page as route } from '$app/state';
  import { goto, afterNavigate } from '$app/navigation';
  import { get } from '#lib/api.js';
  import { errorToast, session, stored, store } from '#lib/state.svelte.js';
  import { COLUMNS, loadView } from '#lib/columns.js';
  import { defaultVariant } from '#lib/format.js';
  import Icon from '#lib/components/Icon.svelte';
  import DocView from '#lib/components/viewer/DocView.svelte';
  import DocDetails from '#lib/components/DocDetails.svelte';
  import { watchBusy, isBusy } from '#lib/poll.svelte.js';

  let linkId = $derived(route.params.id);
  let file = $state(null);
  let doc = $state(null);
  let error = $state('');
  let siblings = $state([]);
  let backTo = null;
  let collectionName = $state('');

  let page = $state(1), numPages = $state(0);
  let zoom = $state('auto'), scale = $state(1);
  let matchCount = $state(0), current = $state(-1), searching = $state(false);
  let findOpen = $state(false), findInput = $state(), findText = $state(''), query = $state('');
  let thumbs = $state(stored('viewerThumbs', true));
  let info = $state(stored('viewerInfo', false));
  let variant = $state('original');
  let viewVersion = $state(0);
  let initialPage = $state(1);
  let view = $state();
  let pageInput = $state('1');

  afterNavigate(({ from }) => {
    if (from && !from.url.pathname.startsWith('/view/')) backTo = from.url.pathname + from.url.search;
  });

  $effect(() => {
    const id = linkId;
    untrack(() => load(id));
  });

  async function load(id) {
    error = '';
    const sp = route.url.searchParams;
    initialPage = Math.max(1, +sp.get('page') || 1);
    page = initialPage;
    viewVersion = +sp.get('v') || 0;
    const q = sp.get('q') ?? '';
    findText = query = q;
    findOpen = !!q;
    try {
      const f = await get(`/files/${id}`);
      const d = await get(`/documents/${f.document_id}`);
      if (id !== linkId) return;
      file = f;
      doc = d;
      variant = ['original', 'searchable', 'image_only'].includes(sp.get('variant')) ? sp.get('variant') : defaultVariant(d);
      loadSiblings(f);
    } catch (e) {
      error = e.message;
    }
  }

  async function reloadDoc(quiet = false) {
    const id = file.document_id;
    try {
      const d = await get(`/documents/${id}`);
      if (id === file?.document_id) doc = d;
    } catch (e) {
      if (!quiet) errorToast(e);
    }
  }

  watchBusy(() => isBusy(doc?.status), () => reloadDoc(true));

  async function loadSiblings(f) {
    if (siblings.some((s) => s.link_id === f.link_id)) return;
    const coll = route.url.searchParams.get('c');
    if (coll) {
      try {
        const d = await get(`/collections/${coll}`);
        siblings = d.items;
        collectionName = d.collection.name;
        return;
      } catch {}
    }
    try {
      const d = await get(`/folders/${f.folder_id}`);
      const cfg = loadView('folder');
      const col = COLUMNS[cfg.sort] ?? COLUMNS.name;
      const dir = cfg.desc ? -1 : 1;
      siblings = [...d.files].sort((a, b) => {
        const x = col.sort(a), y = col.sort(b);
        return x < y ? -dir : x > y ? dir : a.name.localeCompare(b.name);
      });
    } catch {
      siblings = [];
    }
  }

  let idx = $derived(siblings.findIndex((s) => s.link_id === linkId));
  let prevDoc = $derived(idx > 0 ? siblings[idx - 1] : null);
  let nextDoc = $derived(idx >= 0 && idx < siblings.length - 1 ? siblings[idx + 1] : null);

  function openSibling(s) {
    if (!s) return;
    const q = new URLSearchParams();
    if (query) q.set('q', query);
    const coll = route.url.searchParams.get('c');
    if (coll) q.set('c', coll);
    goto(`/view/${s.link_id}${q.size ? '?' + q : ''}`, { replaceState: true, keepFocus: true });
  }

  function close() {
    const coll = route.url.searchParams.get('c');
    if (coll) goto(`/c/${coll}?sel=${linkId}`, { replaceState: true });
    else if (backTo?.startsWith('/search')) history.back();
    else if (file) goto(`/f/${file.folder_id}?sel=${linkId}`, { replaceState: true });
    else history.back();
  }

  $effect(() => {
    pageInput = String(page);
  });

  function submitPage(e) {
    e.preventDefault();
    view?.goTo(+pageInput);
    e.currentTarget.querySelector('input').blur();
  }

  const STEPS = [0.25, 0.33, 0.5, 0.67, 0.75, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3, 4];
  function zoomBy(dir) {
    const s = scale;
    const next = dir > 0 ? STEPS.find((x) => x > s + 0.001) : [...STEPS].reverse().find((x) => x < s - 0.001);
    if (next) zoom = next;
  }
  let zoomSelect = $derived(typeof zoom === 'string' ? zoom : 'custom');

  async function openFind() {
    findOpen = true;
    await tick();
    findInput?.focus();
    findInput?.select();
  }
  function closeFind() {
    findOpen = false;
    findText = query = '';
  }
  let findTimer;
  function onFindInput() {
    clearTimeout(findTimer);
    findTimer = setTimeout(() => (query = findText), 250);
  }
  function findKey(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (query !== findText) query = findText;
      else if (e.shiftKey) view?.findPrev();
      else view?.findNext();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      closeFind();
    }
  }

  function toggleThumbs() {
    thumbs = !thumbs;
    store('viewerThumbs', thumbs);
  }
  function toggleInfo() {
    info = !info;
    store('viewerInfo', info);
  }

  let caps = $derived(doc && view ? view.capabilities() : { find: false, zoom: false, pages: false });

  function keydown(e) {
    if (e.defaultPrevented) return;
    const inField = ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement?.tagName);
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f' && caps.find) {
      e.preventDefault();
      openFind();
      return;
    }
    if (e.key === 'Escape' && !inField && !document.querySelector('[role="dialog"]')) {
      e.preventDefault();
      close();
      return;
    }
    if (inField || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === '[') openSibling(prevDoc);
    else if (e.key === ']') openSibling(nextDoc);
    else if ((e.key === '+' || e.key === '=') && caps.zoom) zoomBy(1);
    else if (e.key === '-' && caps.zoom) zoomBy(-1);
    else if (e.key === '0' && caps.zoom) zoom = 'auto';
    else if (e.key === 't' && caps.pages) toggleThumbs();
    else if (e.key === 'i') toggleInfo();
    else return;
    e.preventDefault();
  }

  const spaceSlug = $derived(session.spaces.find((s) => s.id === file?.space_id)?.slug ?? '');
</script>

<svelte:head><title>{file ? `${file.name} – tildoc` : 'tildoc'}</title></svelte:head>
<svelte:window onkeydown={keydown} />

<div class="viewer" class:withinfo={info && doc}>
  <header class="bar">
    <div class="left">
      <button class="icon" onclick={close} aria-label="Close viewer" title="Close (Esc)"><Icon name="close" /></button>
      {#if file}
        <div class="title">
          <h1 title={file.name}>{file.name}</h1>
          <span class="where" title="{spaceSlug}{file.path}">{collectionName ? `In collection ${collectionName}, from ` : ''}{spaceSlug}{file.path.slice(0, file.path.length - file.name.length - 1) || '/'}</span>
        </div>
      {/if}
    </div>

    {#if doc}
      <div class="mid">
        {#if caps.pages}
          <button class="icon hide-sm" class:on={thumbs} onclick={toggleThumbs} aria-pressed={thumbs} aria-label="Page thumbnails" title="Page thumbnails (T)"><Icon name="pages" /></button>
          <form class="pager" onsubmit={submitPage}>
            <button type="button" class="icon" onclick={() => view?.goTo(page - 1)} disabled={page <= 1} aria-label="Previous page"><Icon name="up" size={16} /></button>
            <input aria-label="Page" inputmode="numeric" bind:value={pageInput} onfocus={(e) => e.currentTarget.select()} />
            <span>of {numPages || '…'}</span>
            <button type="button" class="icon" onclick={() => view?.goTo(page + 1)} disabled={page >= numPages} aria-label="Next page"><Icon name="down" size={16} /></button>
          </form>
        {/if}
        {#if caps.zoom}
          <div class="zoom">
            <button class="icon hide-sm" onclick={() => zoomBy(-1)} aria-label="Zoom out" title="Zoom out (−)"><Icon name="zoom-out" /></button>
            <select aria-label="Zoom" value={zoomSelect} onchange={(e) => (zoom = e.currentTarget.value === 'custom' ? scale : e.currentTarget.value)}>
              <option value="auto">Automatic</option>
              <option value="width">Fit width</option>
              <option value="page">Fit page</option>
              <option value="custom">{Math.round(scale * 100)} %</option>
            </select>
            <button class="icon hide-sm" onclick={() => zoomBy(1)} aria-label="Zoom in" title="Zoom in (+)"><Icon name="zoom-in" /></button>
            {#if caps.pages}<button class="icon hide-sm" onclick={() => view?.rotate()} aria-label="Rotate view" title="Rotate view"><Icon name="rotate" /></button>{/if}
          </div>
        {/if}
      </div>

      <div class="right">
        {#if doc.has_searchable}
          <div class="seg hide-sm" role="group" aria-label="Version of the PDF">
            {#if doc.foreign_ocr}
              <button class:active={variant === 'original'} onclick={() => (variant = 'original')} title="The file as uploaded, with the OCR it came with">Existing OCR</button>
              <button class:active={variant === 'searchable'} onclick={() => (variant = 'searchable')} title="tildoc's own OCR of the plain page images">tildoc OCR</button>
            {:else}
              <button class:active={variant === 'searchable'} onclick={() => (variant = 'searchable')} title="The original with recognised text added (OCR)">With OCR text</button>
              <button class:active={variant === 'original'} onclick={() => (variant = 'original')} title="The file exactly as uploaded">Original</button>
            {/if}
          </div>
        {/if}
        {#if caps.find}<button class="icon" class:on={findOpen} onclick={() => (findOpen ? closeFind() : openFind())} aria-label="Find in document" title="Find in document (Ctrl+F)"><Icon name="search" /></button>{/if}
        {#if siblings.length > 1}
          <button class="icon" onclick={() => openSibling(prevDoc)} disabled={!prevDoc} aria-label="Previous document" title={collectionName ? 'Previous document in collection ([)' : 'Previous document in folder ([)'}><Icon name="left" /></button>
          <button class="icon" onclick={() => openSibling(nextDoc)} disabled={!nextDoc} aria-label="Next document" title={collectionName ? 'Next document in collection (])' : 'Next document in folder (])'}><Icon name="right" /></button>
        {/if}
        <button class="icon" class:on={info} onclick={toggleInfo} aria-pressed={info} aria-label="Document details" title="Details (I)"><Icon name="panel" /></button>
        <a class="btn icon" href={`/api/files/${linkId}/content`} download aria-label="Download original" title="Download the original file"><Icon name="download" /></a>
      </div>
    {/if}
  </header>

  {#if findOpen && caps.find}
    <div class="find" role="search">
      <Icon name="search" size={16} />
      <input bind:this={findInput} bind:value={findText} oninput={onFindInput} onkeydown={findKey} placeholder="Find in document" aria-label="Find in document" />
      <span class="count" aria-live="polite">
        {#if !query}&nbsp;{:else if searching && !matchCount}Searching…{:else if matchCount}{current + 1} of {matchCount}{:else}No matches{/if}
      </span>
      <button class="icon" onclick={() => view?.findPrev()} disabled={!matchCount} aria-label="Previous match" title="Previous match (Shift+Enter)"><Icon name="up" size={16} /></button>
      <button class="icon" onclick={() => view?.findNext()} disabled={!matchCount} aria-label="Next match" title="Next match (Enter)"><Icon name="down" size={16} /></button>
      <button class="icon" onclick={closeFind} aria-label="Close find"><Icon name="close" size={16} /></button>
      {#if query && !matchCount && !searching && doc?.has_searchable && variant === 'original'}
        <span class="hint-ocr">Scanned pages are only searchable in the version with OCR text. <button class="link" onclick={() => (variant = 'searchable')}>Switch</button></span>
      {/if}
    </div>
  {/if}

  <main class="stage">
    {#if error}
      <div class="msg"><strong>This document can't be opened</strong><span>{error}</span><button onclick={close}>Back</button></div>
    {:else if doc && file}
      {#key `${file.document_id}:${doc.version}`}
        <DocView bind:this={view} file={{ ...file, mime: doc.mime, size: doc.size }} {variant} version={viewVersion} mode="full"
          {query} {thumbs} {initialPage} transitionName="doc-page"
          bind:page bind:numPages bind:zoom bind:scale bind:matchCount bind:current bind:searching />
      {/key}
    {:else}
      <div class="msg">Opening document…</div>
    {/if}
  </main>

  {#if info && doc}
    <aside class="drawer" aria-label="Document details">
      <header><h2>Details</h2><button class="icon" onclick={toggleInfo} aria-label="Close details"><Icon name="close" size={16} /></button></header>
      {#if viewVersion}<p class="viewing">You are viewing version {viewVersion}. <button class="link" onclick={() => (viewVersion = 0)}>Show current version</button></p>{/if}
      <DocDetails {doc} {file} bind:viewVersion onchanged={reloadDoc} />
    </aside>
  {/if}
</div>

<style>
  .viewer {
    position: fixed; inset: 0; display: grid; grid-template-rows: auto auto 1fr; grid-template-columns: 1fr;
    grid-template-areas: 'bar' 'find' 'stage'; background: var(--stage); color: var(--stage-ink); z-index: 20;
  }
  .viewer.withinfo { grid-template-columns: 1fr 360px; grid-template-areas: 'bar bar' 'find drawer' 'stage drawer'; }
  .bar { grid-area: bar; display: flex; align-items: center; gap: 12px; padding: 6px 10px; background: var(--stage-2); border-bottom: 1px solid rgb(255 255 255 / 0.06); min-height: 52px; }
  .bar :global(button.icon), .bar :global(.btn.icon) { color: var(--stage-ink-2); }
  .bar :global(button.icon:hover:not(:disabled)), .bar :global(.btn.icon:hover) { background: rgb(255 255 255 / 0.08); color: var(--stage-ink); }
  .bar :global(button.icon.on) { color: var(--stage-ink); background: rgb(255 255 255 / 0.12); }
  .left { display: flex; align-items: center; gap: 8px; min-width: 0; flex: 1 1 0; }
  .title { display: flex; flex-direction: column; min-width: 0; line-height: 1.25; }
  h1 { font-size: var(--t-md); font-weight: 650; color: var(--stage-ink); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .where { font-size: var(--t-sm); color: var(--stage-ink-2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .mid { display: flex; align-items: center; gap: 14px; flex: none; }
  .right { display: flex; align-items: center; gap: 4px; flex: 1 1 0; justify-content: flex-end; }
  .pager, .zoom { display: flex; align-items: center; gap: 2px; }
  .pager input { width: 3.6em; min-height: 30px; text-align: center; padding: 2px 4px; background: var(--stage); border-color: rgb(255 255 255 / 0.15); color: var(--stage-ink); }
  .pager span { color: var(--stage-ink-2); font-size: var(--t-sm); margin: 0 4px; white-space: nowrap; }
  .zoom select { width: auto; min-height: 30px; padding: 2px 6px; background: var(--stage); border-color: rgb(255 255 255 / 0.15); color: var(--stage-ink); font-size: var(--t-sm); }
  .seg { border-color: rgb(255 255 255 / 0.15); margin-right: 6px; }
  .seg button { background: var(--stage); color: var(--stage-ink-2); }
  .seg button + button { border-left-color: rgb(255 255 255 / 0.15); }
  .seg button:hover:not(:disabled) { background: rgb(255 255 255 / 0.08); }
  .seg button.active { background: var(--stage-ink); color: var(--stage); }

  .find {
    grid-area: find; display: flex; align-items: center; gap: 6px; flex-wrap: wrap; padding: 6px 12px; background: var(--stage-2);
    border-bottom: 1px solid rgb(255 255 255 / 0.06); color: var(--stage-ink-2);
  }
  .find input { width: min(320px, 50vw); min-height: 30px; background: var(--stage); border-color: rgb(255 255 255 / 0.15); color: var(--stage-ink); }
  .find .count { min-width: 7.5em; font-size: var(--t-sm); }
  .find :global(button.icon) { color: var(--stage-ink-2); }
  .hint-ocr { font-size: var(--t-sm); color: var(--stage-ink); }
  .hint-ocr .link { color: var(--highlight); text-decoration: underline; }

  .stage { grid-area: stage; min-height: 0; min-width: 0; position: relative; }
  .msg { height: 100%; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 8px; color: var(--stage-ink-2); }
  .msg strong { color: var(--stage-ink); }

  .drawer { grid-area: drawer; display: flex; flex-direction: column; min-height: 0; background: var(--surface); color: var(--ink); border-left: 1px solid var(--line); }
  .drawer > header { display: flex; align-items: center; justify-content: space-between; padding: 8px 8px 4px 16px; }
  .drawer h2 { font-size: var(--t-base); }
  .viewing { margin: 0 16px 8px; font-size: var(--t-sm); background: var(--warn-soft); color: var(--warn); padding: 6px 10px; border-radius: var(--r-control); }
  .drawer :global(.details) { flex: 1; min-height: 0; }

  @media (max-width: 1000px) {
    .viewer.withinfo { grid-template-columns: 1fr; grid-template-areas: 'bar' 'find' 'stage'; }
    .drawer { position: fixed; left: 0; right: 0; bottom: 0; height: 62vh; z-index: 3; border-left: 0; border-top: 1px solid var(--line); border-radius: var(--r-panel) var(--r-panel) 0 0; box-shadow: var(--pop-shadow); }
  }
  @media (max-width: 760px) {
    .bar { flex-wrap: wrap; gap: 6px; padding: 4px 6px; }
    .left { flex-basis: 100%; }
    .mid { flex: 1; gap: 6px; }
    .right { flex: none; }
    .hide-sm { display: none; }
    .zoom select { display: none; }
  }
</style>
