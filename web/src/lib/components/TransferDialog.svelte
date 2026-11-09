<script>
  import Rich from '#lib/i18n/Rich.svelte';
  import { tx } from '#lib/i18n/index.js';
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
      toast(tx('transferdialog_transferred_x', { space: space.name }), 'success');
      onclose();
    } catch (err) {
      errorToast(err);
    }
  }
</script>

<Modal title={tx('transferdialog_transfer_x', { space: space.name })} {onclose}>
  <form onsubmit={submit}>
    <p class="muted">{tx('transferdialog_the_new_owner_gets_every_permission')}</p>
    <div class="seg" role="radiogroup" aria-label={tx('transferdialog_transfer_to')}>
      <button type="button" role="radio" aria-checked={who === 'user'} class:active={who === 'user'} onclick={() => (who = 'user')}>{tx('grantrows_a_person')}</button>
      <button type="button" role="radio" aria-checked={who === 'team'} class:active={who === 'team'} onclick={() => (who = 'team')}>{tx('grantrows_a_team')}</button>
    </div>
    {#if who === 'user'}
      <label>{tx('transferdialog_new_owner')}<input list="transfer-users" bind:value={user} required /></label>
      <datalist id="transfer-users">{#each users as u (u.id)}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
    {:else}
      <label>{tx('transferdialog_new_owner')}<select bind:value={team} required><option value="" disabled>{tx('grantrows_choose_a_team')}</option>{#each teams as t (t.id)}<option value={t.id}>{t.name}</option>{/each}</select></label>
    {/if}
    <label>{tx('transferdialog_what_you_keep')}<select bind:value={keep}><option value="">{tx('transferdialog_nothing')}</option>{#each roles as r (r.id)}<option value={r.slug}>{r.name}</option>{/each}</select></label>
    <label><Rich key="transferdialog_type_to_confirm" vars={{ slug: space.slug }} /><input bind:value={confirm} autocomplete="off" required /></label>
    <div class="row end"><button type="button" onclick={onclose}>{tx('common_cancel')}</button><button class="primary" disabled={confirm !== space.slug}>{tx('spaces_transfer')}</button></div>
  </form>
</Modal>
