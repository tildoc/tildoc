<script>
  // Side preview of the selected file: the whole document scrolls here, details
  // below. Resizable; "Open" morphs it into the full viewer.
  import { untrack } from 'svelte';
  import Icon from './Icon.svelte';
  import DocView from './viewer/DocView.svelte';
  import DocDetails from './DocDetails.svelte';
  import { get } from '../api.js';
  import { errorToast, stored, store } from '../state.svelte.js';
  import { defaultVariant } from '../format.js';
  import { watchBusy, isBusy } from '../poll.svelte.js';

  let { file, onclose, onopen, onchanged, query = '' } = $props();
  let doc = $state(null);
  let viewVersion = $state(0);
  let page = $state(1), numPages = $state(0), zoom = $state('width');
  let width = $state(stored('peekWidth', 460));
  let opening = $state(false);

  async function load(quiet = false) {
    const id = file.document_id;
    try {
      const d = await get(`/documents/${id}`);
      if (id === file.document_id) doc = d;
    } catch (e) {
      if (!quiet) errorToast(e);
    }
  }

  // reload only when the document or its version changes, not when the
  // parent list refreshes the same row
  let docKey = $derived(`${file.document_id}:${file.version}`);
  $effect(() => {
    docKey;
    untrack(() => {
      doc = null;
      viewVersion = 0;
      page = 1;
      load();
    });
  });

  watchBusy(() => isBusy(doc?.status), () => load(true));

  const maxW = () => Math.round(window.innerWidth * 0.6);
  const clampW = (w) => Math.max(320, Math.min(maxW(), w));

  function resize(e) {
    const startX = e.clientX, startW = width;
    const move = (ev) => (width = clampW(startW + (startX - ev.clientX)));
    const up = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', up);
      store('peekWidth', width);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', up);
  }
  function resizeKey(e) {
    if (e.key === 'ArrowLeft') width = clampW(width + 24);
    else if (e.key === 'ArrowRight') width = clampW(width - 24);
    else return;
    e.preventDefault();
    store('peekWidth', width);
  }

  function open() {
    opening = true;
    onopen?.({ page, version: viewVersion });
  }

  const variant = $derived(defaultVariant(doc));
</script>

<aside class="peek" style:--w="{width}px" aria-label="Preview of {file.name}">
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
  <div class="handle" role="separator" aria-orientation="vertical" aria-label="Resize preview" tabindex="0"
    aria-valuenow={width} aria-valuemin={320} aria-valuemax={1600} onpointerdown={resize} onkeydown={resizeKey}></div>
  <header>
    <h2 title={file.name}>{file.name}</h2>
    <button class="icon" onclick={onclose} aria-label="Close preview" title="Close preview (Space)"><Icon name="close" /></button>
  </header>

  <div class="preview">
    {#if doc}
      <DocView file={{ ...file, mime: doc.mime, size: doc.size }} {variant} version={viewVersion} mode="peek" {query}
        transitionName={opening ? 'doc-page' : ''} bind:page bind:numPages bind:zoom />
      {#if numPages > 1}<span class="pageno">Page {page} of {numPages}</span>{/if}
    {/if}
  </div>

  <div class="bar">
    <button class="primary" onclick={open}><Icon name="open" size={16} /> Open</button>
    <a class="btn" href={`/api/files/${file.link_id}/content`} download><Icon name="download" size={16} /> Download</a>
    {#if viewVersion}<span class="pill">Viewing version {viewVersion}</span>{/if}
  </div>

  <div class="info">
    {#if doc}<DocDetails {doc} {file} compact bind:viewVersion onchanged={() => { load(); onchanged?.(); }} />{/if}
  </div>
</aside>

<style>
  .peek {
    position: relative; display: grid; grid-template-rows: auto minmax(220px, 1.25fr) auto minmax(150px, 1fr);
    width: var(--w); min-width: 0; height: 100%; background: var(--surface); border-left: 1px solid var(--line);
  }
  .handle { position: absolute; left: -4px; top: 0; bottom: 0; width: 8px; cursor: col-resize; z-index: 5; }
  .handle:hover, .handle:focus-visible { background: color-mix(in srgb, var(--accent) 35%, transparent); outline: none; }
  header { display: flex; align-items: center; gap: 8px; padding: 10px 10px 10px 16px; }
  h2 { flex: 1; min-width: 0; font-weight: 650; font-size: var(--t-md); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .preview { position: relative; min-height: 0; border-block: 1px solid var(--line); }
  .pageno { position: absolute; right: 12px; bottom: 10px; font-size: var(--t-sm); background: var(--surface); color: var(--ink-2); padding: 1px 8px; border-radius: 99px; box-shadow: 0 1px 3px rgb(0 0 0 / 0.15); pointer-events: none; }
  .bar { display: flex; gap: 8px; align-items: center; padding: 10px 16px; }
  .info { min-height: 0; border-top: 1px solid var(--line-soft); }
  @media (max-width: 1100px) {
    .peek { position: fixed; inset: 0 0 0 auto; width: min(var(--w), 100vw); z-index: 30; box-shadow: var(--pop-shadow); }
  }
  @media (max-width: 760px) {
    .peek { width: 100vw; }
    .handle { display: none; }
  }
</style>
