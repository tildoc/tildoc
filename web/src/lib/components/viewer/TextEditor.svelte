<script>
  import Rich from '#lib/i18n/Rich.svelte';
  import { tx } from '#lib/i18n/index.js';
  // Text editor for text/* documents in the full viewer (CodeMirror, loaded on
  // demand). One editor per document: this window holds a lease renewed every
  // 5 s (free again 15 s after the last renewal). Others see the text
  // read-only and follow it live until the lease is free. Saving always shows
  // the changes first and creates a new version; if a version arrived
  // meanwhile (WebDAV, S3, SFTP), the review warns and compares with it — the
  // last save wins, the other version stays in the history.
  import { onMount, onDestroy, tick } from 'svelte';
  import { beforeNavigate, afterNavigate } from '$app/navigation';
  import Icon from '../Icon.svelte';
  import Modal from '../Modal.svelte';
  import { api, get, ApiError } from '../../api.js';
  import { session, toast, errorToast } from '../../state.svelte.js';
  import { watchLive } from '../../events.svelte.js';

  let { doc, file, onsaved } = $props();

  const MAX = 5 << 20;
  const RENEW_MS = 5000;
  const clientId = crypto.randomUUID();

  let host = $state();
  let ed = null; // {view, getText, setText, setReadOnly, focus, destroy}
  let cm = null; // the lazily loaded module
  let mode = $state('loading'); // loading | editing | readonly
  let reason = $state(''); // why read-only
  let lease = $state(null); // the other holder (read-only because of it)
  let dirty = $state(false);
  let base = { text: '', version: 0 }; // what the editor started from / last saved
  let bom = false, crlf = false;
  let newer = $state(null); // a version that arrived while editing: {version, source, by}
  let review = $state(null); // {original, warn, saving}
  let diffHost = $state();
  let saveBtn = $state();
  let diffView = null;
  let renewTimer = null, retryTimer = null;
  let alive = true;

  const canWrite = () => (session.spaces.find((s) => s.id === doc.space_id)?.perm ?? 0) >= 2;
  const contentURL = (v) => `/api/v1/documents/${doc.id}/content?inline=1${v ? `&version=${v}` : ''}`;

  // fetch the current text; fatal decoding tells whether it is UTF-8
  async function fetchText() {
    const res = await fetch(contentURL(0), { credentials: 'same-origin', cache: 'no-store' });
    if (!res.ok) throw new ApiError(res.status, res.statusText);
    const buf = await res.arrayBuffer();
    let utf8 = true, text;
    try {
      text = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(buf);
    } catch {
      utf8 = false;
      text = new TextDecoder('utf-8').decode(buf);
    }
    const hasBom = text.startsWith('﻿');
    return { text: hasBom ? text.slice(1) : text, bom: hasBom, crlf: text.includes('\r\n'), utf8 };
  }

  async function acquire(takeover = false) {
    try {
      await api('POST', `/documents/${doc.id}/edit-lease`, { client_id: clientId, takeover });
      lease = null;
      return true;
    } catch (e) {
      if (e.status !== 409) throw e;
      const cur = await get(`/documents/${doc.id}`).catch(() => null);
      lease = cur?.edit_lease ?? { name: 'someone', mine: false };
      return false;
    }
  }

  function setMode(m, why = '') {
    mode = m;
    reason = why;
    ed?.setReadOnly(m !== 'editing');
    clearInterval(renewTimer);
    clearTimeout(retryTimer);
    if (m === 'editing') renewTimer = setInterval(renew, RENEW_MS);
    // a lease nobody renews expires silently: check again when it would
    else if (lease?.expires_at) retryTimer = setTimeout(recheck, Math.max(1000, new Date(lease.expires_at) - Date.now() + 1000));
  }

  async function renew() {
    try {
      await api('PUT', `/documents/${doc.id}/edit-lease`, { client_id: clientId });
    } catch (e) {
      if (e.status === 409) {
        // taken over (another window of yours, or expired after a long sleep)
        const cur = await get(`/documents/${doc.id}`).catch(() => null);
        lease = cur?.edit_lease ?? null;
        setMode('readonly', tx('texteditor_this_document_is_being_edited_elsewhere'));
      }
    }
  }

  // read-only because someone else edits: follow their saves, take over when free
  async function recheck() {
    if (!alive || mode === 'editing') return;
    if (!canWrite() || reason !== 'lease') return;
    const cur = await get(`/documents/${doc.id}`).catch(() => null);
    const fresh = await fetchText().catch(() => null);
    if (cur && fresh && !dirty) {
      ed?.setText(fresh.text);
      base = { text: fresh.text, version: cur.version };
      dirty = false;
    }
    if (await acquire().catch(() => false)) {
      setMode('editing');
      toast(tx('texteditor_you_can_edit_this_document_now'), 'success');
    } else setMode('readonly', 'lease');
  }

  onMount(async () => {
    let t;
    try {
      [t, cm] = await Promise.all([fetchText(), import('../../editor/cm.js')]);
    } catch (e) {
      errorToast(e);
      return;
    }
    if (!alive) return;
    bom = t.bom;
    crlf = t.crlf;
    base = { text: t.text, version: doc.version };
    ed = await cm.createEditor({
      parent: host, text: t.text, lang: cm.languageName(file?.name ?? doc.name, doc.mime), crlf, readOnly: true,
      onChange: () => (dirty = ed && ed.getText() !== base.text),
      onSave: () => mode === 'editing' && openReview()
    });
    if (!alive) return ed.destroy();
    if (!canWrite()) setMode('readonly', tx('texteditor_you_can_read_this_document_but'));
    else if (doc.size > MAX) setMode('readonly', tx('texteditor_text_files_larger_than_5_mb'));
    else if (!t.utf8) setMode('readonly', tx('texteditor_this_file_is_not_utf_8'));
    else if (await acquire().catch(() => false)) {
      setMode('editing');
      await tick(); // the editor is hidden while loading; hidden elements take no focus
      ed.focus();
    } else setMode('readonly', 'lease');
  });

  function release() {
    clearInterval(renewTimer);
    clearTimeout(retryTimer);
    if (mode === 'editing') {
      // keepalive: also delivered while the tab closes
      fetch(`/api/v1/documents/${doc.id}/edit-lease?client=${clientId}`, {
        method: 'DELETE', keepalive: true, credentials: 'same-origin', headers: { 'X-Tildoc-CSRF': '1' }
      }).catch(() => {});
    }
  }

  onDestroy(() => {
    alive = false;
    release();
    diffView?.destroy();
    ed?.destroy();
  });

  // live: lease taken/released, new versions from other channels
  watchLive((ev) => ev.type === 'space' && ev.documents?.includes(doc.id), async () => {
    const cur = await get(`/documents/${doc.id}`).catch(() => null);
    if (!cur || !alive) return;
    if (mode === 'readonly' && reason === 'lease') {
      lease = cur.edit_lease;
      if (!lease || cur.version !== base.version) recheck();
      return;
    }
    if (mode === 'editing' && cur.version > base.version && cur.versions?.[0]?.source !== 'editor') {
      const v = cur.versions[0];
      newer = { version: cur.version, source: v.source, by: v.created_by };
    }
  });

  // SvelteKit moves the focus to the page after a navigation: give it back,
  // so typing goes into the text (and Escape stays in the editor)
  afterNavigate(() => {
    if (mode === 'editing') ed?.focus();
  });

  // leaving with unsaved changes asks first
  beforeNavigate((nav) => {
    if (dirty && mode === 'editing' && !confirm(tx('texteditor_discard_your_unsaved_changes'))) nav.cancel();
  });
  function beforeUnload(e) {
    if (dirty && mode === 'editing') e.preventDefault();
  }

  // ---------------------------------------------------------- saving

  async function openReview() {
    if (!ed || mode !== 'editing') return;
    const text = ed.getText();
    let cur;
    try {
      cur = await get(`/documents/${doc.id}`);
    } catch (e) {
      return errorToast(e);
    }
    const changedMeanwhile = cur.version !== base.version;
    let original = base.text;
    if (changedMeanwhile) {
      const latest = await fetchText().catch(() => null);
      if (latest) original = latest.text;
    }
    if (text === original) {
      toast(changedMeanwhile ? tx('texteditor_your_text_equals_the_newest_version') : tx('texteditor_no_changes_to_save'));
      return;
    }
    const v = cur.versions?.[0];
    review = { original, text, next: cur.version + 1, warn: changedMeanwhile ? { version: cur.version, by: v?.created_by, source: v?.source } : null, saving: false };
    await tick();
    diffView = await cm.createDiff({ parent: diffHost, original, modified: text, lang: cm.languageName(file?.name ?? doc.name, doc.mime) });
    saveBtn?.focus(); // Enter confirms (the editor behind the dialog had the focus)
  }

  function closeReview() {
    diffView?.destroy();
    diffView = null;
    review = null;
    ed?.focus();
  }

  async function save(e) {
    e?.preventDefault();
    if (!review || review.saving) return;
    review.saving = true;
    const body = new Blob([(bom ? '﻿' : '') + review.text], { type: 'text/plain' });
    try {
      const d = await api('PUT', `/documents/${doc.id}/text?client=${clientId}`, body);
      base = { text: review.text, version: d.version };
      dirty = ed.getText() !== base.text;
      newer = null;
      closeReview();
      toast(tx('texteditor_saved_as_version_x', { version: d.version }), 'success');
      onsaved?.(d);
    } catch (err) {
      review.saving = false;
      errorToast(err);
    }
  }
</script>

<svelte:window onbeforeunload={beforeUnload} />

<div class="editor">
  {#if mode === 'readonly'}
    <div class="note" role="status">
      <Icon name="lock" size={15} />
      {#if reason === 'lease'}
        {#if lease?.mine}
          <span>{tx('texteditor_you_are_editing_this_document_in')}</span>
          <button class="small" onclick={async () => (await acquire(true)) && setMode('editing')}>{tx('texteditor_edit_here_instead')}</button>
        {:else}
          <span><Rich key="texteditor_is_editing" vars={{ name: lease?.name ?? tx('texteditor_someone') }} /></span>
        {/if}
      {:else}
        <span>{reason}</span>
      {/if}
    </div>
  {:else if mode === 'editing'}
    <div class="bar">
      <span class="state" class:dirty>{dirty ? tx('texteditor_unsaved_changes') : tx('texteditor_saved')}</span>
      {#if newer}<span class="warn">{newer.by ? tx('texteditor_arrived_meanwhile_by', { version: newer.version, source: newer.source, by: newer.by }) : tx('texteditor_arrived_meanwhile', { version: newer.version, source: newer.source })}</span>{/if}
      <span class="keys">{tx('texteditor_ctrl_f_search_and_replace_alt')}</span>
      <button class="primary small" onclick={openReview} disabled={!dirty} title={tx('texteditor_review_the_changes_and_save_ctrl')}><Icon name="check" size={15} /> {tx('texteditor_save')}</button>
    </div>
  {/if}
  <div class="host" bind:this={host} class:loading={mode === 'loading'}></div>
</div>

{#if review}
  <Modal title={tx('texteditor_save_changes')} wide onclose={closeReview}>
    <form onsubmit={save}>
      {#if review.warn}
        <p class="warn">{(review.warn.by ? (review.warn.source ? tx('texteditor_saved_meanwhile_by_via', { version: review.warn.version, by: review.warn.by, source: review.warn.source }) : tx('texteditor_saved_meanwhile_by', { version: review.warn.version, by: review.warn.by, source: review.warn.source })) : review.warn.source ? tx('texteditor_saved_meanwhile_via', { version: review.warn.version, by: review.warn.by, source: review.warn.source }) : tx('texteditor_saved_meanwhile', { version: review.warn.version, by: review.warn.by, source: review.warn.source }))}</p>
      {:else}
        <p class="muted small">{tx('texteditor_changes_against_the_version_you_opened', { next: review.next })}</p>
      {/if}
      <div class="diff" bind:this={diffHost}></div>
      <div class="row end">
        <button type="button" onclick={closeReview}>{tx('texteditor_keep_editing')}</button>
        <button class="primary" type="submit" bind:this={saveBtn} disabled={review.saving}>{review.saving ? tx('texteditor_saving') : tx('texteditor_save_as_version_x', { next: review.next })}</button>
      </div>
    </form>
  </Modal>
{/if}

<style>
  .editor { display: flex; flex-direction: column; height: 100%; min-height: 0; background: var(--stage); }
  .host { flex: 1; min-height: 0; width: 100%; overflow: hidden; }
  .host.loading { visibility: hidden; }
  .bar, .note { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; padding: 8px 16px; background: var(--stage-2); color: var(--stage-ink); font-size: var(--t-sm); }
  .note { color: var(--stage-ink); }
  .state { color: var(--stage-ink-2); }
  .state.dirty { color: var(--highlight); }
  .keys { color: var(--stage-ink-2); margin-left: auto; }
  .bar .warn { color: var(--highlight); }
  .diff { height: min(60vh, 560px); border: 1px solid var(--line); border-radius: var(--r-control); overflow: hidden; margin: 8px 0 14px; }
  p.warn { background: var(--warn-soft); color: var(--warn); padding: 8px 12px; border-radius: var(--r-control); margin: 0 0 6px; }
  @media (max-width: 760px) { .keys { display: none; } }
</style>
