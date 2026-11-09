<script>
  // Single files other people shared with you.
  import { get } from '#lib/api.js';
  import { openView } from '#lib/nav.js';
  import { errorToast } from '#lib/state.svelte.js';
  import { size, date, shortType } from '#lib/format.js';

  let files = $state(null);
  $effect(() => { get('/shared').then((f) => (files = f)).catch(errorToast); });
</script>

<svelte:head><title>Shared with me – tildoc</title></svelte:head>

<div class="page">
  <header><h1>Shared with me</h1><p>Single files other people gave you access to.</p></header>
  {#if files}
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>Name</th><th>Type</th><th>Size</th><th>Changed</th></tr></thead>
        <tbody>
          {#each files as f (f.link_id)}
            <tr>
              <td><button class="link" onclick={() => openView(`/view/${f.link_id}`)}>{f.name}</button></td>
              <td>{shortType(f.mime, f.name)}</td><td>{size(f.size)}</td><td>{date(f.modified_at)}</td>
            </tr>
          {:else}<tr><td colspan="4" class="muted">Nothing has been shared with you yet.</td></tr>{/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
