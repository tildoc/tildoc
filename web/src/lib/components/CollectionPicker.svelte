<script>
  import { tx } from '#lib/i18n/index.js';
  // Pick a collection to add documents to (or create one on the fly).
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import CollectionForm from './CollectionForm.svelte';
  import { session } from '../state.svelte.js';

  let { count = 1, onpick, onclose } = $props();
  let creating = $state(false);
  let editable = $derived(session.collections.filter((c) => c.can_edit));
</script>

{#if creating}
  <CollectionForm onsaved={(c) => onpick(c)} onclose={() => (creating = false)} />
{:else}
  <Modal title={tx('collectionpicker_add_to_collection', { count })} {onclose}>
    {#if editable.length}
      <ul class="list">
        {#each editable as c (c.id)}
          <li>
            <button onclick={() => onpick(c)}>
              <Icon name="collection" size={16} />
              <span class="nm">{c.name}</span>
              <span class="muted small">{c.team_name ? tx('common_team_x', { name: c.team_name }) : tx('collectionpicker_personal')}, {c.count} {c.count === 1 ? 'document' : 'documents'}</span>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted">{tx('collectionpicker_you_have_no_collection_you_can')}</p>
    {/if}
    <div class="row end">
      <button onclick={onclose}>{tx('common_cancel')}</button>
      <button class="primary" onclick={() => (creating = true)}><Icon name="plus" size={16} /> {tx('common_new_collection')}</button>
    </div>
  </Modal>
{/if}

<style>
  .list { list-style: none; margin: 0 0 14px; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .list button { width: 100%; justify-content: flex-start; border: 0; background: none; gap: 10px; padding: 8px 10px; }
  .list button:hover:not(:disabled) { background: var(--accent-soft); }
  .list :global(svg) { color: var(--ink-2); flex: none; }
  .nm { font-weight: 600; flex: 1; text-align: left; overflow: hidden; text-overflow: ellipsis; }
</style>
