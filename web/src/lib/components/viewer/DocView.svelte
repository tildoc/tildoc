<script>
  // Picks the right view for a document: pdf.js (or the browser's viewer, by
  // preference), image, text, or a file sheet for types without a preview.
  import PdfView from './PdfView.svelte';
  import ImageView from './ImageView.svelte';
  import TextView from './TextView.svelte';
  import TextEditor from './TextEditor.svelte';
  import Icon from '../Icon.svelte';
  import { prefs } from '../../state.svelte.js';
  import { size as fmtSize, shortType, textVariant } from '../../format.js';

  let {
    file, // {document_id, name, mime, size}
    doc = null, // the document (optional): enables the fast in-document search
    edit = false, // text files open in the editor (full viewer, current version)
    onsaved, // editor saved a new version
    variant = 'original',
    version = 0,
    mode = 'full',
    query = '',
    thumbs = false,
    initialPage = 1,
    transitionName = '',
    page = $bindable(1),
    numPages = $bindable(0),
    zoom = $bindable('width'),
    scale = $bindable(1),
    matchCount = $bindable(0),
    current = $bindable(-1),
    searching = $bindable(false)
  } = $props();

  let inner = $state();

  const mime = $derived(file?.mime ?? '');
  const kind = $derived(
    mime.startsWith('application/pdf') ? (prefs().pdf_viewer === 'native' ? 'native' : 'pdf')
    : /^image\/(png|jpeg|gif|webp)/.test(mime) ? 'image'
    : mime.startsWith('text/') || mime.includes('json') ? 'text'
    : 'other'
  );
  const base = $derived(`/api/documents/${file.document_id}/content`);
  const url = $derived.by(() => {
    const q = new URLSearchParams({ inline: '1' });
    if (variant && variant !== 'original') q.set('variant', variant);
    if (version) q.set('version', String(version));
    if (kind === 'native') q.set('viewer', 'native');
    return `${base}?${q}`;
  });

  // The server's extracted text (pages separated by form feeds) lets the PDF
  // find pre-select pages; only valid for the variant the text came from.
  const textPath = $derived(
    doc && doc.status === 'done' && (!version || version === doc.version) && variant === textVariant(doc)
      ? `/documents/${doc.id}/text`
      : ''
  );

  const editing = $derived(kind === 'text' && edit && !!doc && (!version || version === doc.version));

  export const capabilities = () => ({
    find: kind === 'pdf' || (kind === 'text' && !editing), // the editor has its own search (Ctrl+F)
    zoom: kind === 'pdf' || kind === 'image',
    pages: kind === 'pdf',
    kind
  });
  export function findNext() { inner?.findNext?.(); }
  export function findPrev() { inner?.findPrev?.(); }
  export function goTo(n) { inner?.goTo?.(n); }
  export function rotate() { inner?.rotate?.(); }
</script>

{#if kind === 'pdf'}
  {#key url}
    <PdfView bind:this={inner} {url} {textPath} {mode} {query} {thumbs} {initialPage} {transitionName}
      bind:page bind:numPages bind:zoom bind:scale bind:matchCount bind:current bind:searching />
  {/key}
{:else if kind === 'native'}
  <iframe class="native" title={file.name} src={url} style:view-transition-name={transitionName || null}></iframe>
{:else if kind === 'image'}
  <ImageView {url} alt={file.name} {mode} {transitionName} bind:zoom bind:scale />
{:else if editing}
  {#key doc.id}<TextEditor {doc} {file} {onsaved} />{/key}
{:else if kind === 'text'}
  <TextView bind:this={inner} {url} mime={file.mime} name={file.name} size={file.size} {query} {mode} bind:matchCount bind:current />
{:else}
  <div class="other {mode}">
    <div class="filesheet" style:view-transition-name={transitionName || null}>
      <Icon name="file" size={40} />
      <strong>{file.name}</strong>
      <span>{shortType(file.mime, file.name)} file, {fmtSize(file.size)}</span>
      <p>There is no preview for this type. The file is stored unchanged and can be downloaded.</p>
      <a class="btn primary" href={base} download><Icon name="download" size={16} /> Download</a>
    </div>
  </div>
{/if}

<style>
  .native { display: block; width: 100%; height: 100%; border: 0; background: var(--stage); }
  .other { height: 100%; display: grid; place-items: center; background: var(--stage); padding: 24px; }
  .other.peek { background: var(--surface-2); padding: 12px; }
  .filesheet { background: var(--paper); color: #1d2a35; box-shadow: var(--paper-shadow); padding: 32px 36px; max-width: 360px; width: 100%;
    display: flex; flex-direction: column; align-items: center; gap: 6px; text-align: center; }
  .filesheet strong { font-size: var(--t-md); font-weight: 650; overflow-wrap: anywhere; }
  .filesheet span, .filesheet p { color: #56636b; font-size: var(--t-sm); margin: 0; }
  .filesheet .btn { margin-top: 12px; }
  .peek .filesheet { padding: 20px; }
</style>
