<script>
  import { tx } from '#lib/i18n/index.js';
  // An office document in Collabora Online: the token goes to its origin by
  // form POST into the iframe (never in this page's URL). Collabora saves on
  // its own, so closing the viewer loses nothing.
  import { onMount, tick, untrack } from 'svelte';
  import { post } from '../../api.js';

  let { doc, file } = $props();

  const target = untrack(() => `office-${doc.id}`);
  const opened = untrack(() => doc.version); // the version the session started from
  let form = $state();
  let action = $state(''), token = $state(''), ttl = $state(0);
  let error = $state('');

  const newer = $derived(!!doc.office_session && doc.version > opened);

  onMount(async () => {
    try {
      const o = await post(`/documents/${doc.id}/office`);
      action = o.url;
      token = o.access_token;
      ttl = o.access_token_ttl;
      await tick();
      form.submit();
    } catch (e) {
      error = e.message;
    }
  });
</script>

<div class="wrap">
  {#if error}
    <div class="msg"><strong>{tx('officeview_this_document_can_t_be_opened')}</strong><span>{error}</span></div>
  {:else}
    <p class="note" class:warn={newer}>
      {#if newer}
        {tx('officeview_version_x_arrived_from_elsewhere_when', { version: doc.version })}
      {:else}
        {tx('officeview_changes_are_saved_automatically_and_become')}
      {/if}
    </p>
    <form bind:this={form} method="post" {action} {target} hidden>
      <input type="hidden" name="access_token" value={token} />
      <input type="hidden" name="access_token_ttl" value={ttl} />
    </form>
    <iframe class="office" name={target} title={file.name} allow="clipboard-read; clipboard-write; fullscreen"></iframe>
  {/if}
</div>

<style>
  .wrap { width: 100%; height: 100%; display: flex; flex-direction: column; background: var(--stage); }
  .note { margin: 0; padding: 6px 14px; font-size: var(--t-sm); color: var(--ink-2, inherit); background: var(--surface-2); }
  .note.warn { background: var(--hit, #fff3a0); color: #1d2a35; }
  .office { flex: 1; width: 100%; border: 0; background: var(--paper); }
  .msg { margin: auto; display: flex; flex-direction: column; gap: 6px; text-align: center; padding: 24px; }
</style>
