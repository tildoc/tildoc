<script>
  import { tx } from '#lib/i18n/index.js';
  // Create or edit a collection: name, owner (me or a team I manage), optional tag rule.
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import { get, post, patch } from '../api.js';
  import { session, errorToast, refreshTags, refreshCollections } from '../state.svelte.js';

  let { collection = null, onsaved, onclose } = $props();
  let name = $state(untrack(() => collection?.name ?? ''));
  let team = $state('');
  let tag = $state(untrack(() => collection?.tag_id ?? ''));
  let teams = $state([]);
  let busy = $state(false);

  $effect(() => {
    refreshTags().catch(() => {});
    if (!collection) get('/teams').then((t) => (teams = t.filter((x) => x.role === 'manager'))).catch(() => {});
  });

  async function save(e) {
    e.preventDefault();
    busy = true;
    try {
      const c = collection
        ? await patch(`/collections/${collection.id}`, { name, tag_id: tag })
        : await post('/collections', { name, team_id: team, tag_id: tag || null });
      await refreshCollections();
      onsaved?.(c);
    } catch (err) {
      errorToast(err);
    }
    busy = false;
  }
</script>

<Modal title={collection ? tx('collectionform_edit_collection') : tx('common_new_collection')} {onclose}>
  <form onsubmit={save}>
    <label>{tx('common_name')}<input bind:value={name} required placeholder={tx('collectionform_for_example_steuer_2025')} /></label>
    {#if !collection}
      <label>{tx('common_belongs_to')}
        <select bind:value={team}>
          <option value="">{tx('common_just_me')}</option>
          {#each teams as t (t.id)}<option value={t.id}>{tx('common_team_x', { name: t.name })}</option>{/each}
        </select>
        <span class="hint">{tx('collectionform_team_members_see_the_documents_of')}</span>
      </label>
    {/if}
    <label>{tx('collectionform_also_include_every_document_tagged')}
      <select bind:value={tag}>
        <option value="">{tx('collectionform_no_tag_only_documents_added_by')}</option>
        {#each session.tags as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
      </select>
    </label>
    <div class="row end">
      <button type="button" onclick={onclose}>{tx('common_cancel')}</button>
      <button class="primary" disabled={busy}>{collection ? tx('collectionform_save') : tx('collectionform_create_collection')}</button>
    </div>
  </form>
</Modal>
