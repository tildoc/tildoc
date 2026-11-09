<script>
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

<Modal title={collection ? 'Edit collection' : 'New collection'} {onclose}>
  <form onsubmit={save}>
    <label>Name<input bind:value={name} required placeholder="For example Steuer 2025" /></label>
    {#if !collection}
      <label>Belongs to
        <select bind:value={team}>
          <option value="">Just me</option>
          {#each teams as t (t.id)}<option value={t.id}>Team {t.name}</option>{/each}
        </select>
        <span class="hint">Team members see the documents of it they already have access to.</span>
      </label>
    {/if}
    <label>Also include every document tagged
      <select bind:value={tag}>
        <option value="">No tag (only documents added by hand)</option>
        {#each session.tags as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
      </select>
    </label>
    <div class="row end">
      <button type="button" onclick={onclose}>Cancel</button>
      <button class="primary" disabled={busy}>{collection ? 'Save' : 'Create collection'}</button>
    </div>
  </form>
</Modal>
