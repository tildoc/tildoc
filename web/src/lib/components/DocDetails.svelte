<script>
  // Details, versions, links and extracted text of one document. Shared by the
  // peek panel and the viewer's info drawer.
  import Icon from './Icon.svelte';
  import { get, post, api } from '../api.js';
  import { toast, errorToast, session } from '../state.svelte.js';
  import { size, date, shortType, statusLabel } from '../format.js';
  import { goto } from '$app/navigation';

  let { doc, file, viewVersion = $bindable(0), onchanged, compact = false } = $props();
  let tab = $state('details');
  let text = $state(null);
  let versionInput = $state();

  $effect(() => {
    doc?.id;
    doc?.version;
    text = null;
  });

  $effect(() => {
    if (tab === 'text' && text === null && doc?.has_text) {
      get(`/documents/${doc.id}/text`).then((r) => (text = r.text)).catch(() => (text = ''));
    }
  });

  const contentURL = $derived(`/api/documents/${doc.id}/content`);
  const spaceSlug = (id) => session.spaces.find((s) => s.id === id)?.slug ?? '';
  const canWrite = $derived((session.spaces.find((s) => s.id === doc.space_id)?.perm ?? 0) >= 2);

  async function reprocess() {
    try {
      await post(`/documents/${doc.id}/reprocess`);
      toast('Text extraction queued');
      onchanged?.();
    } catch (e) {
      errorToast(e);
    }
  }

  async function newVersion(e) {
    const f = e.target.files[0];
    e.target.value = '';
    if (!f) return;
    try {
      await api('POST', `/documents/${doc.id}/versions`, f);
      toast('Uploaded a new version. Every link shows it.', 'success');
      viewVersion = 0;
      onchanged?.();
    } catch (err) {
      errorToast(err);
    }
  }

  async function restore(v) {
    try {
      await post(`/documents/${doc.id}/versions/${v}/restore`);
      toast(`Restored version ${v} as the current version`, 'success');
      viewVersion = 0;
      onchanged?.();
    } catch (e) {
      errorToast(e);
    }
  }

  const tabs = $derived([
    ['details', 'Details'],
    ['versions', doc.versions.length > 1 ? `Versions (${doc.versions.length})` : 'Versions'],
    ['links', doc.links.length > 1 ? `Places (${doc.links.length})` : 'Places'],
    ['text', 'Text']
  ]);
</script>

<div class="details" class:compact>
  <div class="tabs" role="tablist">
    {#each tabs as [k, l] (k)}
      <button role="tab" aria-selected={tab === k} class:active={tab === k} onclick={() => (tab = k)}>{l}</button>
    {/each}
  </div>

  <div class="body">
    {#if tab === 'details'}
      <dl>
        <dt>Type</dt><dd>{shortType(doc.mime, file?.name ?? doc.name)} <span class="muted">({doc.mime})</span></dd>
        <dt>Size</dt><dd>{size(doc.size)}</dd>
        {#if doc.pages}<dt>Pages</dt><dd>{doc.pages}</dd>{/if}
        <dt>Text</dt>
        <dd>
          <span class="pill status {doc.status}">{statusLabel[doc.status] ?? doc.status}</span>
          {#if doc.text_method}<span class="muted">{doc.text_method === 'ocr' ? 'recognised by OCR' : 'from the text layer'}</span>{/if}
        </dd>
        {#if doc.process_error}<dt>Problem</dt><dd class="err">{doc.process_error}</dd>{/if}
        <dt>Version</dt><dd>{doc.version}</dd>
        <dt>Added</dt><dd>{date(doc.created_at)}</dd>
        <dt>Changed</dt><dd>{date(doc.updated_at)}</dd>
        {#if file?.original_path}<dt>Imported as</dt><dd class="path">{file.original_path}</dd>{/if}
        <dt>SHA-256</dt><dd class="mono hash">{doc.sha256}</dd>
      </dl>
      {#if canWrite}
        <div class="row actions">
          <button class="small" onclick={() => versionInput.click()}><Icon name="history" size={15} /> Upload new version</button>
          <button class="small ghost" onclick={reprocess}><Icon name="refresh" size={15} /> Extract text again</button>
          <input type="file" hidden bind:this={versionInput} onchange={newVersion} />
        </div>
      {/if}
      {#if doc.forked_from}<p class="muted small">This document was split off from another one when it was changed through one of several places (copy-on-write).</p>{/if}
    {:else if tab === 'versions'}
      <ol class="list" reversed>
        {#each [...doc.versions].reverse() as v (v.version)}
          <li class:viewing={viewVersion === v.version || (!viewVersion && v.version === doc.version)}>
            <div class="vhead">
              <strong>Version {v.version}</strong>
              {#if v.version === doc.version}<span class="pill">current</span>{/if}
            </div>
            <span class="muted small">{date(v.created_at)}, {size(v.size)}, by {v.created_by || 'system'} via {v.source}</span>
            <div class="row">
              {#if v.version !== doc.version}
                <button class="small" onclick={() => (viewVersion = viewVersion === v.version ? 0 : v.version)}>{viewVersion === v.version ? 'Show current' : 'View'}</button>
                {#if canWrite}<button class="small" onclick={() => restore(v.version)}>Restore</button>{/if}
              {/if}
              <a class="btn small ghost" href={`${contentURL}?version=${v.version}`} download><Icon name="download" size={14} /> Download</a>
            </div>
          </li>
        {/each}
      </ol>
    {:else if tab === 'links'}
      <p class="muted small">The same document appears in these places. “Upload new version” changes it everywhere. Overwriting the file in just one place, for example from Dolphin, splits that place off into its own document.</p>
      <ul class="list">
        {#each doc.links as l (l.id)}
          <li>
            <button class="link path" onclick={() => goto(`/f/${l.folder_id}?sel=${l.id}`)}>{spaceSlug(doc.space_id)}{l.path}</button>
            {#if l.original_path !== l.path}<span class="muted small">imported as {l.original_path}</span>{/if}
          </li>
        {/each}
      </ul>
    {:else if tab === 'text'}
      {#if doc.has_text}
        <pre class="text">{text ?? 'Loading…'}</pre>
      {:else if doc.status === 'ocr_pending'}
        <p class="muted">Some pages have no text layer. OCR has not run yet because the worker has no OCR tools.</p>
      {:else if doc.status === 'unsupported'}
        <p class="muted">Text extraction is not available for this file type.</p>
      {:else}
        <p class="muted">No text yet ({statusLabel[doc.status] ?? doc.status}).</p>
      {/if}
    {/if}
  </div>
</div>

<style>
  .details { display: flex; flex-direction: column; min-height: 0; height: 100%; }
  .tabs { display: flex; gap: 2px; padding: 0 12px; border-bottom: 1px solid var(--line); overflow-x: auto; flex: none; }
  .tabs button { border: 0; border-bottom: 2px solid transparent; border-radius: 0; background: none; padding: 8px 10px 6px; min-height: 0; color: var(--ink-2); font-size: var(--t-sm); }
  .tabs button:hover:not(:disabled) { background: none; color: var(--ink); }
  .tabs button.active { border-bottom-color: var(--accent); color: var(--ink); font-weight: 600; }
  .body { flex: 1; overflow: auto; padding: 14px 16px 18px; }
  dl { display: grid; grid-template-columns: max-content 1fr; gap: 6px 14px; margin: 0 0 14px; font-size: var(--t-sm); }
  dt { color: var(--ink-2); }
  dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .hash { font-size: 0.72rem; line-height: 1.4; }
  .path { overflow-wrap: anywhere; }
  .actions { margin-bottom: 10px; }
  .list { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; }
  .list li { display: flex; flex-direction: column; gap: 4px; padding: 10px 0; border-bottom: 1px solid var(--line-soft); }
  .list li:last-child { border-bottom: 0; }
  .list li.viewing { box-shadow: inset 3px 0 0 var(--accent); padding-left: 10px; }
  .vhead { display: flex; align-items: center; gap: 8px; }
  .text { white-space: pre-wrap; overflow-wrap: anywhere; font-family: var(--font-read); font-size: var(--t-sm); line-height: 1.6; margin: 0; }
</style>
