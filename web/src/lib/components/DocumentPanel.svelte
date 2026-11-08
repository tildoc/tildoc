<script>
  import Icon from './Icon.svelte';
  import { get, post, api } from '../api.js';
  import { toast, errorToast, session } from '../state.svelte.js';
  import { size, date, shortType, statusLabel } from '../format.js';
  import { goto } from '$app/navigation';

  let { file, onclose, onchanged } = $props();
  let doc = $state(null);
  let tab = $state('preview');
  let text = $state(null);
  let textPreview = $state('');
  let variant = $state('original');
  let versionInput;

  async function load() {
    try {
      doc = await get(`/documents/${file.document_id}`);
    } catch (e) {
      errorToast(e);
    }
  }

  $effect(() => {
    file.document_id;
    file.version;
    doc = null;
    text = null;
    textPreview = '';
    variant = 'original';
    load();
  });

  // poll while processing
  $effect(() => {
    if (!doc || !['pending', 'processing'].includes(doc.status)) return;
    const t = setTimeout(load, 2500);
    return () => clearTimeout(t);
  });

  let isPDF = $derived(doc?.mime?.startsWith('application/pdf'));
  let isImage = $derived(doc?.mime?.startsWith('image/'));
  let isText = $derived(doc?.mime?.startsWith('text/') || doc?.mime?.includes('json'));
  let contentURL = $derived(`/api/documents/${file.document_id}/content`);

  $effect(() => {
    if (tab === 'preview' && isText && doc && doc.size < 512 * 1024 && !textPreview) {
      fetch(`${contentURL}?inline=1`).then((r) => r.text()).then((t) => (textPreview = t));
    }
  });

  $effect(() => {
    if (tab === 'text' && text === null && doc?.has_text) {
      get(`/documents/${file.document_id}/text`).then((r) => (text = r.text)).catch(() => (text = ''));
    }
  });

  async function reprocess() {
    try {
      await post(`/documents/${file.document_id}/reprocess`);
      toast('Processing queued');
      load();
    } catch (e) {
      errorToast(e);
    }
  }

  async function newVersion(e) {
    const f = e.target.files[0];
    e.target.value = '';
    if (!f) return;
    try {
      await api('POST', `/documents/${file.document_id}/versions`, f);
      toast('New version uploaded (visible in all links)', 'success');
      load();
      onchanged?.();
    } catch (err) {
      errorToast(err);
    }
  }

  async function restore(v) {
    try {
      await post(`/documents/${file.document_id}/versions/${v}/restore`);
      toast(`Version ${v} restored as new version`, 'success');
      load();
      onchanged?.();
    } catch (e) {
      errorToast(e);
    }
  }

  function spaceSlug(id) {
    return session.spaces.find((s) => s.id === id)?.slug ?? '';
  }
</script>

<aside class="panel">
  <header>
    <div class="title">
      <strong title={file.name}>{file.name}</strong>
      <small>{shortType(file.mime, file.name)} · {size(file.size)} · v{file.version}</small>
    </div>
    <button class="icon" onclick={onclose} aria-label="Close panel"><Icon name="close" /></button>
  </header>

  <nav class="tabs">
    {#each [['preview', 'Preview'], ['text', 'Text'], ['versions', 'Versions'], ['links', 'Links'], ['info', 'Info']] as [k, l]}
      <button class:active={tab === k} onclick={() => (tab = k)}>{k === 'links' && doc?.links.length > 1 ? `${l} (${doc.links.length})` : l}</button>
    {/each}
  </nav>

  <div class="content">
    {#if !doc}
      <p class="muted">Loading…</p>
    {:else if tab === 'preview'}
      {#if isPDF}
        {#if doc.has_searchable}
          <div class="seg">
            <button class:active={variant === 'original'} onclick={() => (variant = 'original')}>Original</button>
            <button class:active={variant === 'searchable'} onclick={() => (variant = 'searchable')}>Searchable (OCR)</button>
          </div>
        {/if}
        <iframe title="PDF preview" src={`${contentURL}?inline=1${variant === 'searchable' ? '&variant=searchable' : ''}`}></iframe>
      {:else if isImage}
        <img alt={file.name} src={`${contentURL}?inline=1`} />
      {:else if isText}
        <pre class="textprev">{doc.size >= 512 * 1024 ? 'File too large for preview – see Text tab.' : textPreview}</pre>
      {:else}
        <p class="muted">No preview for this file type. The file is stored safely and can be downloaded.</p>
      {/if}
    {:else if tab === 'text'}
      {#if doc.has_text}
        <p class="muted small">Extracted via {doc.text_method}</p>
        <pre class="textprev">{text ?? 'Loading…'}</pre>
      {:else if doc.status === 'ocr_pending'}
        <p class="muted">This document has pages without a text layer. OCR is pending (OCR tools not available on the worker).</p>
      {:else if doc.status === 'unsupported'}
        <p class="muted">Text extraction is not supported for this file type.</p>
      {:else}
        <p class="muted">No text available ({statusLabel[doc.status] ?? doc.status}).</p>
      {/if}
    {:else if tab === 'versions'}
      <ul class="list">
        {#each doc.versions as v (v.version)}
          <li>
            <div><strong>v{v.version}</strong> {#if v.version === doc.version}<span class="tag">current</span>{/if}
              <small class="muted">{date(v.created_at)} · {size(v.size)} · {v.created_by || 'system'} · {v.source}</small></div>
            <div class="row">
              <a class="btn small" href={`${contentURL}?version=${v.version}`} download>Download</a>
              {#if v.version !== doc.version}<button class="small" onclick={() => restore(v.version)}>Restore</button>{/if}
            </div>
          </li>
        {/each}
      </ul>
      {#if doc.forked_from}<p class="muted small">Forked (copy-on-write) from another document.</p>{/if}
    {:else if tab === 'links'}
      <p class="muted small">This document appears in these places. Editing it through one link creates its own copy (copy-on-write); "New version" below applies to all links.</p>
      <ul class="list">
        {#each doc.links as l (l.id)}
          <li>
            <button class="link" onclick={() => goto(`/f/${l.folder_id}?sel=${l.id}`)}>/{spaceSlug(doc.space_id)}{l.path}</button>
            {#if l.original_path !== l.path}<small class="muted">imported as {l.original_path}</small>{/if}
          </li>
        {/each}
      </ul>
    {:else if tab === 'info'}
      <dl>
        <dt>Status</dt><dd><span class="status {doc.status}">{statusLabel[doc.status] ?? doc.status}</span> {doc.kind}{#if doc.pages} · {doc.pages} pages{/if}</dd>
        {#if doc.process_error}<dt>Error</dt><dd class="err">{doc.process_error}</dd>{/if}
        <dt>Type</dt><dd>{doc.mime}</dd>
        <dt>Size</dt><dd>{size(doc.size)} ({doc.size.toLocaleString()} bytes)</dd>
        <dt>SHA-256</dt><dd class="mono">{doc.sha256}</dd>
        <dt>MD5</dt><dd class="mono">{doc.md5}</dd>
        <dt>Added</dt><dd>{date(doc.created_at)}</dd>
        <dt>Modified</dt><dd>{date(doc.updated_at)}</dd>
        <dt>Original path</dt><dd>{file.original_path}</dd>
        <dt>Document ID</dt><dd class="mono">{doc.id}</dd>
      </dl>
    {/if}
  </div>

  <footer>
    <a class="btn primary" href={contentURL} download><Icon name="download" size={16} /> Original</a>
    {#if doc?.has_searchable}<a class="btn" href={`${contentURL}?variant=searchable`} download>Searchable PDF</a>{/if}
    <button onclick={() => versionInput.click()}><Icon name="history" size={16} /> New version</button>
    <button class="ghost" onclick={reprocess} title="Re-run text extraction / OCR"><Icon name="refresh" size={16} /></button>
    <input type="file" hidden bind:this={versionInput} onchange={newVersion} />
  </footer>
</aside>

<style>
  .panel { display: flex; flex-direction: column; background: var(--surface); border-left: 1px solid var(--border); width: 420px; min-width: 0; height: 100%; }
  header { display: flex; align-items: flex-start; justify-content: space-between; padding: 12px 14px; gap: 8px; border-bottom: 1px solid var(--border); }
  .title { display: flex; flex-direction: column; min-width: 0; }
  .title strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .title small { color: var(--muted); }
  .tabs { display: flex; gap: 2px; padding: 6px 8px 0; border-bottom: 1px solid var(--border); overflow-x: auto; }
  .tabs button { border: 0; background: none; border-bottom: 2px solid transparent; border-radius: 0; padding: 6px 10px; color: var(--muted); }
  .tabs button.active { border-bottom-color: var(--accent); color: var(--text); }
  .content { flex: 1; overflow: auto; padding: 12px 14px; display: flex; flex-direction: column; gap: 8px; }
  iframe { width: 100%; flex: 1; min-height: 420px; border: 1px solid var(--border); border-radius: 8px; background: #fff; }
  img { max-width: 100%; border-radius: 8px; }
  .textprev { white-space: pre-wrap; word-break: break-word; font-size: 0.82rem; background: var(--surface-2); padding: 10px; border-radius: 8px; margin: 0; }
  .list { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 8px; }
  .list li { display: flex; flex-direction: column; gap: 4px; padding: 8px; border: 1px solid var(--border-soft); border-radius: 8px; }
  .tag { font-size: 0.7rem; background: var(--accent-soft); color: var(--accent-text); padding: 0 6px; border-radius: 99px; }
  dl { display: grid; grid-template-columns: 110px 1fr; gap: 6px 10px; margin: 0; font-size: 0.85rem; }
  dt { color: var(--muted); }
  dd { margin: 0; word-break: break-all; }
  .mono { font-family: ui-monospace, monospace; font-size: 0.78rem; }
  .err { color: var(--err); }
  .small { font-size: 0.8rem; }
  .seg { display: flex; gap: 4px; }
  .seg button { padding: 3px 10px; font-size: 0.8rem; }
  .seg button.active { background: var(--accent-soft); color: var(--accent-text); border-color: var(--accent); }
  footer { display: flex; flex-wrap: wrap; gap: 6px; padding: 10px 14px; border-top: 1px solid var(--border); }
  .status { font-size: 0.78rem; padding: 1px 7px; border-radius: 99px; background: var(--surface-2); }
  .status.done { background: var(--ok-soft); color: var(--ok); }
  .status.failed { background: var(--err-soft); color: var(--err); }
  @media (max-width: 1100px) {
    .panel { position: fixed; inset: 0 0 0 auto; width: min(460px, 100vw); z-index: 30; box-shadow: -10px 0 40px rgb(0 0 0 / 0.25); }
  }
</style>
