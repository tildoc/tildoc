<script>
  // Who a space is shared with; owners and admins add people or teams.
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import { get, post, del } from '../api.js';
  import { session, toast, errorToast, refreshSpaces } from '../state.svelte.js';

  let { space, onclose } = $props();
  let grants = $state([]);
  let users = $state([]);
  let teams = $state([]);
  let who = $state('user');
  let user = $state('');
  let team = $state('');
  let role = $state('viewer');
  const canShare = $derived(space.caps?.includes('share'));
  const roles = $derived(
    [['viewer', 'Can view and download'], ['member', 'Can also add and change'], ['admin', 'Can also delete and share']].filter(
      ([r]) => ({ viewer: 1, member: 2, admin: 3 })[r] <= ({ owner: 4, admin: 3, member: 2, viewer: 1 })[space.role]
    )
  );

  async function load() {
    try {
      grants = await get(`/spaces/${space.id}/grants`);
    } catch (e) {
      errorToast(e);
    }
  }
  $effect(() => {
    load();
    if (canShare) {
      get('/users').then((u) => (users = u.filter((x) => x.id !== session.user.id))).catch(() => {});
      get('/teams').then((t) => (teams = t)).catch(() => {});
    }
  });

  async function add(e) {
    e.preventDefault();
    try {
      await post(`/spaces/${space.id}/grants`, who === 'user' ? { user, role } : { team_id: team, role });
      user = '';
      toast(`Shared ${space.name}`, 'success');
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  async function remove(g) {
    try {
      await del(`/spaces/${space.id}/grants/${g.id}`);
      load();
      refreshSpaces();
    } catch (err) {
      errorToast(err);
    }
  }
  const roleLabel = { viewer: 'Viewer', member: 'Member', admin: 'Admin' };
</script>

<Modal title={`Share ${space.name}`} wide {onclose}>
  <p class="muted">The owners{space.team_name ? ` (team ${space.team_name})` : ''} always have access. Shares add single people or whole teams; a team member never gets more than their role in that team.</p>
  {#if grants.length}
    <ul class="grants">
      {#each grants as g (g.id)}
        <li>
          <Icon name={g.team_id ? 'users' : 'key'} size={16} />
          <span class="who">{g.team_id ? `Team ${g.team_name}` : g.display_name || g.username}{#if g.username && !g.team_id}<small class="muted"> @{g.username}</small>{/if}</span>
          <span class="pill">{roleLabel[g.role]}</span>
          {#if canShare}<button class="small ghost" onclick={() => remove(g)}>Remove</button>{/if}
        </li>
      {/each}
    </ul>
  {:else}
    <p class="muted">Not shared with anyone yet.</p>
  {/if}
  {#if canShare}
    <form class="add" onsubmit={add}>
      <div class="seg" role="radiogroup" aria-label="Share with">
        <button type="button" role="radio" aria-checked={who === 'user'} class:active={who === 'user'} onclick={() => (who = 'user')}>A person</button>
        <button type="button" role="radio" aria-checked={who === 'team'} class:active={who === 'team'} onclick={() => (who = 'team')}>A team</button>
      </div>
      {#if who === 'user'}
        <input list="share-users" bind:value={user} placeholder="Username" aria-label="Username" required />
        <datalist id="share-users">{#each users as u (u.id)}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
      {:else}
        <select bind:value={team} aria-label="Team" required>
          <option value="" disabled>Choose one of your teams</option>
          {#each teams as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
        </select>
      {/if}
      <select bind:value={role} aria-label="Role">{#each roles as [r, l] (r)}<option value={r}>{l}</option>{/each}</select>
      <button class="primary">Share</button>
    </form>
  {/if}
  <div class="row end"><button onclick={onclose}>Done</button></div>
</Modal>

<style>
  .grants { list-style: none; padding: 0; margin: 0 0 16px; display: flex; flex-direction: column; }
  .grants li { display: flex; align-items: center; gap: 10px; padding: 8px 0; border-bottom: 1px solid var(--line-soft); }
  .grants :global(svg) { color: var(--ink-2); }
  .who { flex: 1; }
  .add { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 16px; }
  .add input { flex: 1; min-width: 160px; width: auto; }
  .add select { width: auto; }
</style>
