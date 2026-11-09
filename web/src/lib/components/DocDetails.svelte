<script>
  import { tx } from '#lib/i18n/index.js';
  // Details, versions, links and extracted text of one document. Shared by the
  // peek panel and the viewer's info drawer.
  import Icon from './Icon.svelte';
  import TagEditor from './TagEditor.svelte';
  import CollectionPicker from './CollectionPicker.svelte';
  import HistoryGraph from './HistoryGraph.svelte';
  import { get, post, del, api } from '../api.js';
  import { toast, errorToast, session, refreshCollections } from '../state.svelte.js';
  import { size, date, shortType, statusLabel } from '../format.js';
  import { goto } from '$app/navigation';

  // viewVersion (0 = current) + viewVariant ('' = default) select what is shown
  let { doc, file, viewVersion = $bindable(0), viewVariant = $bindable(''), onchanged, compact = false } = $props();
  let picking = $state(false);
  let local = $state(null); // document after a tag change (until the parent reloads)
  let shown = $derived(local && local.id === doc.id ? local : doc);

  $effect(() => {
    doc;
    local = null;
  });

  async function collect(c) {
    picking = false;
    try {
      await post(`/collections/${c.id}/items`, { document_ids: [doc.id] });
      await refreshCollections();
      toast(tx('docdetails_added_to_x', { collection: c.name }), 'success');
      onchanged?.();
    } catch (e) {
      errorToast(e);
    }
  }
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

  const spaceSlug = (id) => session.spaces.find((s) => s.id === id)?.slug ?? '';
  const canWrite = $derived((session.spaces.find((s) => s.id === doc.space_id)?.perm ?? 0) >= 2);
  const canTag = $derived(!!session.spaces.find((s) => s.id === doc.space_id)?.caps?.includes('tag'));

  async function reprocess() {
    try {
      await post(`/documents/${doc.id}/reprocess`);
      toast(tx('docdetails_text_extraction_queued'));
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
      toast(tx('docdetails_uploaded_a_new_version_every_link'), 'success');
      viewVersion = 0;
      viewVariant = '';
      onchanged?.();
    } catch (err) {
      errorToast(err);
    }
  }

  async function purgeVersion(v) {
    if (!confirm(tx('docdetails_delete_version_x_for_good_it', { version: v }))) return;
    try {
      await del(`/documents/${doc.id}/versions/${v}`);
      viewVersion = 0;
      onchanged?.();
    } catch (e) {
      errorToast(e);
    }
  }
  async function restore(v) {
    try {
      await post(`/documents/${doc.id}/versions/${v}/restore`);
      toast(tx('docdetails_restored_version_x_as_the_current', { version: v }), 'success');
      viewVersion = 0;
      viewVariant = '';
      onchanged?.();
    } catch (e) {
      errorToast(e);
    }
  }

  const placeCount = $derived(doc.links.length + (doc.same_content?.length ?? 0));
  const tabs = $derived([
    ['details', tx('common_details')],
    ['history', doc.versions.length > 1 ? tx('docdetails_history_x', { number: doc.versions.length }) : tx('docdetails_history')],
    ['links', placeCount > 1 ? tx('docdetails_places_x', { placeCount }) : tx('docdetails_places')],
    ['text', tx('docdetails_text')]
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
      <div class="org">
        <span class="lbl">{tx('admin_tags')}</span>
        <TagEditor doc={shown} canWrite={canTag} onchange={(d) => { local = d; onchanged?.(); }} />
        <span class="lbl">{tx('docdetails_collections')}</span>
        <div class="colls">
          {#each shown.collections as c (c.id)}<a class="pill" href={`/c/${c.id}`}><Icon name="collection" size={13} /> {c.name}</a>{/each}
          <button class="small ghost" onclick={() => (picking = true)}><Icon name="plus" size={14} /> {tx('common_add_to_collection')}</button>
        </div>
      </div>
      <dl>
        <dt>{tx('common_type')}</dt><dd>{shortType(doc.mime, file?.name ?? doc.name)} <span class="muted">({doc.mime})</span></dd>
        <dt>{tx('common_size')}</dt><dd>{size(doc.size)}</dd>
        {#if doc.pages}<dt>{tx('docdetails_pages')}</dt><dd>{doc.pages}</dd>{/if}
        <dt>{tx('docdetails_text')}</dt>
        <dd>
          <span class="pill status {doc.status}">{statusLabel[doc.status] ?? doc.status}</span>
          {#if doc.text_method}<span class="muted">{doc.text_method === 'ocr' ? tx('docdetails_recognised_by_ocr') : doc.kind === 'pdf-digital' ? tx('docdetails_embedded_directly_in_the_file_no') : tx('docdetails_from_the_text_layer')}</span>{/if}
        </dd>
        {#if doc.process_error}<dt>{tx('docdetails_problem')}</dt><dd class="err">{doc.process_error}</dd>{/if}
        <dt>{tx('docdetails_version')}</dt><dd>{doc.version}</dd>
        <dt>{tx('docdetails_added')}</dt><dd>{date(doc.created_at)}</dd>
        <dt>{tx('common_changed')}</dt><dd>{date(doc.updated_at)}</dd>
        {#if file?.original_path}<dt>{tx('docdetails_imported_as')}</dt><dd class="path">{file.original_path}</dd>{/if}
        <dt>{tx('docdetails_sha_256')}</dt><dd class="mono hash">{doc.sha256}</dd>
      </dl>
      {#if canWrite}
        <div class="row actions">
          <button class="small" onclick={() => versionInput.click()}><Icon name="history" size={15} /> {tx('docdetails_upload_new_version')}</button>
          <button class="small ghost" onclick={reprocess}><Icon name="refresh" size={15} /> {tx('docdetails_extract_text_again')}</button>
          <input type="file" hidden bind:this={versionInput} onchange={newVersion} />
        </div>
      {/if}
      {#if doc.versions.length > 1 || doc.variants?.length}
        <p class="muted small summary">
          {tx('docdetails_version_x_of_x', { version: doc.version, total: doc.versions.length })}{#if doc.variants?.length}{tx('docdetails_derived_variants', { count: doc.variants.length })}{/if}.
          <button class="link" onclick={() => (tab = 'history')}>{tx('docdetails_show_history')}</button>
        </p>
      {/if}
      {#if doc.forked_from}<p class="muted small">{tx('docdetails_this_document_was_split_off_from')}</p>{/if}
    {:else if tab === 'history'}
      <HistoryGraph {doc} {canWrite} bind:viewVersion bind:viewVariant onrestore={restore} onpurge={purgeVersion} />
    {:else if tab === 'links'}
      <ul class="list">
        {#each doc.links as l (l.id)}
          <li>
            <button class="link path" onclick={() => goto(`/f/${l.folder_id}?sel=${l.id}`)}>{spaceSlug(doc.space_id)}{l.path}</button>
            {#if l.original_path !== l.path}<span class="muted small">{tx('docdetails_imported_as_x', { original_path: l.original_path })}</span>{/if}
          </li>
        {/each}
        {#each doc.same_content ?? [] as l (l.id)}
          <li>
            <button class="link path" onclick={() => goto(`/f/${l.folder_id}?sel=${l.id}`)}>{spaceSlug(l.space_id)}{l.path}</button>
            <span class="muted small">{tx('docdetails_same_content_own_versions_and_tags')}</span>
          </li>
        {/each}
      </ul>
      <p class="muted small">{tx('docdetails_identical_content_is_stored_only_once')}</p>
    {:else if tab === 'text'}
      {#if doc.has_text}
        <pre class="text">{text ?? 'Loading…'}</pre>
      {:else if doc.status === 'ocr_pending'}
        <p class="muted">{tx('docdetails_some_pages_have_no_text_layer')}</p>
      {:else if doc.status === 'unsupported'}
        <p class="muted">{tx('docdetails_text_extraction_is_not_available_for')}</p>
      {:else}
        <p class="muted">{tx('docdetails_no_text_yet_x', { status: statusLabel[doc.status] ?? doc.status })}</p>
      {/if}
    {/if}
  </div>
</div>

{#if picking}<CollectionPicker onpick={collect} onclose={() => (picking = false)} />{/if}

<style>
  .org { display: grid; grid-template-columns: max-content 1fr; gap: 8px 14px; align-items: center; margin-bottom: 14px; padding-bottom: 12px; border-bottom: 1px solid var(--line-soft); }
  .org .lbl { color: var(--ink-2); font-size: var(--t-sm); }
  .colls { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  .colls .pill { color: var(--ink); text-decoration: none; }
  .colls .pill:hover { background: var(--accent-soft); }
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
  .summary { margin: 0 0 10px; }
  .list { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; }
  .list li { display: flex; flex-direction: column; gap: 4px; padding: 10px 0; border-bottom: 1px solid var(--line-soft); }
  .list li:last-child { border-bottom: 0; }
  .text { white-space: pre-wrap; overflow-wrap: anywhere; font-size: var(--t-sm); line-height: 1.6; margin: 0; }
</style>
