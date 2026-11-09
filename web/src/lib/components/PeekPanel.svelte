<script>
  import { tx } from '#lib/i18n/index.js';
  // Side preview of the selected file: the whole document scrolls here, details
  // below. Resizable; "Open" morphs it into the full viewer.
  import { untrack, onMount } from 'svelte';
  import Icon from './Icon.svelte';
  import DocView from './viewer/DocView.svelte';
  import DocDetails from './DocDetails.svelte';
  import { cachedData, revalidate } from '../api.js';
  import { errorToast, stored, store, prefs, session } from '../state.svelte.js';
  import { shownVariant, VARIANT_NAME } from '../format.js';
  import { watchBusy, isBusy } from '../poll.svelte.js';
  import { watchLive } from '../events.svelte.js';
  import { warmup } from '../viewer/pdfjs.js';
  import { canDownload, canEditPdfInOffice } from '../perms.js';

  let { file, onclose, onopen, onchanged, query = '' } = $props();
  let doc = $state(null);
  let viewVersion = $state(0);
  let viewVariant = $state(''); // '' = the default variant of the shown version
  let page = $state(1), numPages = $state(0), zoom = $state('width');
  let width = $state(stored('peekWidth', 460));
  let opening = $state(false);

  async function load(quiet = false) {
    const id = file.document_id;
    const path = `/documents/${id}`;
    if (!doc) doc = cachedData(path) ?? null;
    try {
      const { data: d, changed } = await revalidate(path);
      if (id === file.document_id && (changed || !doc)) doc = d;
    } catch (e) {
      if (!quiet) errorToast(e);
    }
  }

  // a newly selected file (or a new version of it) starts at its latest
  // node; a refresh of the same row keeps what is shown
  let docKey = $derived(`${file.link_id}:${file.document_id}:${file.version}`);
  $effect(() => {
    docKey;
    untrack(() => {
      doc = null;
      viewVersion = 0;
      viewVariant = '';
      page = 1;
      load();
    });
  });

  watchBusy(() => isBusy(doc?.status), () => load(true));
  watchLive((ev) => ev.type === 'space' && !!doc && ev.documents?.includes(doc.id), () => load(true));

  // most previews are PDFs: start pdf.js while the document's data loads
  onMount(() => {
    if (prefs().pdf_viewer !== 'native') warmup().catch(() => {});
  });

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
    // the viewer opens exactly what the preview shows
    onopen?.({ page, version: viewVersion, variant: viewVariant ? variant : '' });
  }

  const variant = $derived(doc ? shownVariant(doc, viewVersion, viewVariant) : 'original');
</script>

<aside class="peek" style:--w="{width}px" aria-label={tx('peekpanel_preview_of', { name: file.name })}>
  <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
  <div class="handle" role="separator" aria-orientation="vertical" aria-label={tx('peekpanel_resize_preview')} tabindex="0"
    aria-valuenow={width} aria-valuemin={320} aria-valuemax={1600} onpointerdown={resize} onkeydown={resizeKey}></div>
  <header>
    <h2 title={file.name}>{file.name}</h2>
    <button class="icon" onclick={onclose} aria-label={tx('peekpanel_close_preview')} title={tx('peekpanel_close_preview_space')}><Icon name="close" /></button>
  </header>

  <div class="preview">
    {#if doc}
      <DocView file={{ ...file, mime: doc.mime, size: doc.size }} {doc} {variant} version={viewVersion} mode="peek" {query}
        transitionName={opening ? 'doc-page' : ''} bind:page bind:numPages bind:zoom />
      {#if numPages > 1}<span class="pageno">{tx('peekpanel_page_x_of_x', { page, numPages })}</span>{/if}
    {/if}
  </div>

  <div class="bar">
    <button class="primary" onclick={open}><Icon name="open" size={16} /> {tx('common_open')}</button>
    {#if !viewVersion && canEditPdfInOffice(doc, file.name, session.office, session.spaces)}<button onclick={() => onopen?.({ office: true })}><Icon name="edit" size={16} /> {tx('peekpanel_edit_in_office')}</button>{/if}
{#if canDownload(doc)}    <a class="btn" href={`/api/v1/files/${file.link_id}/content`} download><Icon name="download" size={16} /> {tx('common_download')}</a>{/if}
    {#if viewVersion || viewVariant}<span class="pill">{variant !== 'original' ? tx('peekpanel_viewing_version_variant', { version: viewVersion || doc?.version, variant: VARIANT_NAME[variant] }) : tx('peekpanel_viewing_version', { version: viewVersion || doc?.version })}</span>{/if}
  </div>

  <div class="info">
    {#if doc}<DocDetails {doc} {file} compact bind:viewVersion bind:viewVariant onchanged={() => { load(); onchanged?.(); }} />{/if}
  </div>
</aside>

<style>
  .peek {
    position: relative; display: grid; grid-template-rows: auto minmax(220px, 1.25fr) auto minmax(150px, 1fr);
    grid-template-columns: minmax(0, 1fr); /* long content never widens the panel */
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
