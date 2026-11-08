<script>
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
  <Modal title={count === 1 ? 'Add to collection' : `Add ${count} documents to collection`} {onclose}>
    {#if editable.length}
      <ul class="list">
        {#each editable as c (c.id)}
          <li>
            <button onclick={() => onpick(c)}>
              <Icon name="collection" size={16} />
              <span class="nm">{c.name}</span>
              <span class="muted small">{c.team_name ? `Team ${c.team_name}` : 'Personal'}, {c.count} {c.count === 1 ? 'document' : 'documents'}</span>
            </button>
          </li>
        {/each}
      </ul>
    {:else}
      <p class="muted">You have no collection you can add to yet.</p>
    {/if}
    <div class="row end">
      <button onclick={onclose}>Cancel</button>
      <button class="primary" onclick={() => (creating = true)}><Icon name="plus" size={16} /> New collection</button>
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
