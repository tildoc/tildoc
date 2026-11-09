<script>
  // Hands a space to another user or team, at once.
  import Modal from './Modal.svelte';
  import { get, post } from '../api.js';
  import { errorToast, refreshSpaces, session, toast } from '../state.svelte.js';

  let { space, onclose } = $props();
  let who = $state('user'), user = $state(''), team = $state(''), keep = $state(''), confirm = $state('');
  let users = $state([]), teams = $state([]), roles = $state([]);
  $effect(() => {
    get('/users').then((u) => (users = u.filter((x) => x.id !== session.user.id))).catch(() => {});
    get('/teams').then((t) => (teams = t)).catch(() => {});
    get('/roles').then((r) => (roles = r.filter((x) => x.scope === 'space'))).catch(() => {});
  });
  async function submit(e) {
    e.preventDefault();
    try {
      await post(`/spaces/${space.id}/transfer`, { ...(who === 'user' ? { user } : { team_id: team }), keep_role: keep, confirm });
      await refreshSpaces();
      toast(`Transferred ${space.name}`, 'success');
      onclose();
    } catch (err) {
      errorToast(err);
    }
  }
</script>

<Modal title={`Transfer ${space.name}`} {onclose}>
  <form onsubmit={submit}>
    <p class="muted">The new owner gets every permission at once. This cannot be undone by you unless you keep a role that allows it.</p>
    <div class="seg" role="radiogroup" aria-label="Transfer to">
      <button type="button" role="radio" aria-checked={who === 'user'} class:active={who === 'user'} onclick={() => (who = 'user')}>A person</button>
      <button type="button" role="radio" aria-checked={who === 'team'} class:active={who === 'team'} onclick={() => (who = 'team')}>A team</button>
    </div>
    {#if who === 'user'}
      <label>New owner<input list="transfer-users" bind:value={user} required /></label>
      <datalist id="transfer-users">{#each users as u (u.id)}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
    {:else}
      <label>New owner<select bind:value={team} required><option value="" disabled>Choose a team</option>{#each teams as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select></label>
    {/if}
    <label>What you keep<select bind:value={keep}><option value="">Nothing</option>{#each roles as r (r.id)}<option value={r.slug}>{r.name}</option>{/each}</select></label>
    <label>Type <code>{space.slug}</code> to confirm<input bind:value={confirm} autocomplete="off" required /></label>
    <div class="row end"><button type="button" onclick={onclose}>Cancel</button><button class="primary" disabled={confirm !== space.slug}>Transfer</button></div>
  </form>
</Modal>
