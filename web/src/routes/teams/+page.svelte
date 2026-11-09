<script>
  import { tx } from '#lib/i18n/index.js';
  import { get, post, put, del, patch } from '#lib/api.js';
  import { session, refreshSpaces, toast, errorToast } from '#lib/state.svelte.js';
  import Confirm from '#lib/components/Confirm.svelte';
  import Icon from '#lib/components/Icon.svelte';

  let teams = $state([]);
  let current = $state(null);
  let members = $state([]);
  let users = $state([]);
  let newTeam = $state('');
  let addUser = $state(''), addRole = $state('member');
  let confirm = $state(null);

  async function load() {
    teams = await get('/teams');
    if (current) current = teams.find((t) => t.id === current.id) ?? null;
  }
  $effect(() => {
    load().catch(errorToast);
    get('/users').then((u) => (users = u)).catch(() => {});
  });
  $effect(() => {
    if (current) get(`/teams/${current.id}/members`).then((m) => (members = m)).catch(errorToast);
  });

  let canManage = $derived(current && (current.role === 'manager' || session.user.is_admin));
  let teamSpaces = $derived(session.spaces.filter((s) => s.owner_team_id === current?.id));

  async function createTeam(e) {
    e.preventDefault();
    try {
      const t = await post('/teams', { name: newTeam });
      newTeam = '';
      await load();
      current = teams.find((x) => x.id === t.id);
    } catch (err) {
      errorToast(err);
    }
  }
  async function setRole(m, role) {
    try {
      members = await put(`/teams/${current.id}/members/${m.user_id}`, { role });
      await refreshSpaces();
    } catch (e) {
      errorToast(e);
      members = await get(`/teams/${current.id}/members`);
    }
  }
  async function add(e) {
    e.preventDefault();
    try {
      members = await put(`/teams/${current.id}/members/${encodeURIComponent(addUser)}`, { role: addRole });
      addUser = '';
    } catch (err) {
      errorToast(err);
    }
  }
  async function removeMember(m) {
    confirm = null;
    try {
      await del(`/teams/${current.id}/members/${m.user_id}`);
      members = members.filter((x) => x.user_id !== m.user_id);
      if (m.user_id === session.user.id) {
        current = null;
        await load();
        await refreshSpaces();
      }
    } catch (e) {
      errorToast(e);
    }
  }
  async function createSpace() {
    const name = prompt(tx('teams_name_of_the_new_team_space'), current.name);
    if (!name) return;
    try {
      await post('/spaces', { name, team_id: current.id });
      await refreshSpaces();
      toast(tx('teams_created_the_team_space'), 'success');
    } catch (e) {
      errorToast(e);
    }
  }
  async function deleteTeam() {
    confirm = null;
    try {
      await del(`/teams/${current.id}`);
      current = null;
      await load();
      await refreshSpaces();
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <header>
    <h1>{tx('layout_teams')}</h1>
    <p>{tx('teams_a_team_shares_spaces_roles_decide')}</p>
  </header>
  <div class="layout">
    <div class="list sheet">
      {#each teams as t (t.id)}
        <button class:active={current?.id === t.id} onclick={() => (current = t)}>{t.name}<small class="muted">{t.role || tx('teams_administrator_view')}</small></button>
      {:else}<p class="muted pad">{tx('teams_you_are_not_in_any_team')}</p>{/each}
      <form onsubmit={createTeam} class="row newteam">
        <input placeholder={tx('teams_new_team_name')} bind:value={newTeam} required aria-label={tx('teams_new_team_name')} style="flex:1" />
        <button class="primary">{tx('f_create')}</button>
      </form>
    </div>
    {#if current}
      <div class="detail">
        <div class="row" style="justify-content:space-between">
          <h2 class="tname">{current.name}</h2>
          {#if canManage}<button class="small ghost" onclick={() => (confirm = { kind: 'team' })}>{tx('teams_delete_team')}</button>{/if}
        </div>
        <section class="section">
          <h2>{tx('teams_members')}</h2>
          <div class="table-wrap">
            <table class="plain">
              <thead><tr><th>{tx('access_person')}</th><th>{tx('common_role')}</th><th></th></tr></thead>
              <tbody>
                {#each members as m (m.user_id)}
                  <tr>
                    <td>{m.display_name} <small class="muted">@{m.username}</small></td>
                    <td>
                      {#if canManage}
                        <select value={m.role} onchange={(e) => setRole(m, e.target.value)} style="width:auto" aria-label={tx('common_role_of', { name: m.username })}>
                          {#each ['manager', 'member'] as r}<option value={r}>{r}</option>{/each}
                        </select>
                      {:else}{m.role}{/if}
                    </td>
                    <td class="actions">{#if canManage || m.user_id === session.user.id}<button class="small ghost" onclick={() => (confirm = { kind: 'member', m })}>{m.user_id === session.user.id ? tx('teams_leave') : tx('common_remove')}</button>{/if}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          {#if canManage}
            <form class="row" style="margin-top:12px" onsubmit={add}>
              <input list="users" placeholder={tx('common_username')} bind:value={addUser} required style="flex:1;min-width:140px" aria-label={tx('common_username')} />
              <datalist id="users">{#each users.filter((u) => !members.some((m) => m.user_id === u.id)) as u}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
              <select bind:value={addRole} style="width:auto" aria-label={tx('common_role')}>{#each ['member', 'manager'] as r}<option value={r}>{r}</option>{/each}</select>
              <button class="primary">{tx('teams_add_member')}</button>
            </form>
            <dl class="roles">
              <dt>{tx('teams_member')}</dt><dd>{tx('teams_gets_what_the_team_has_on')}</dd>
              <dt>{tx('teams_manager')}</dt><dd>{tx('teams_also_owns_the_team_s_spaces')}</dd>
            </dl>
          {/if}
        </section>
        <section class="section">
          <h2>{tx('teams_team_spaces')}</h2>
          <ul class="spaces">{#each teamSpaces as s}<li><a href={`/f/${s.root_folder_id}`}>{s.name}</a> <small class="muted mono">{s.slug}</small></li>{:else}<li class="muted">{tx('teams_no_spaces_yet')}</li>{/each}</ul>
          {#if canManage}<button onclick={createSpace}><Icon name="plus" size={16} /> {tx('teams_new_team_space')}</button>{/if}
        </section>
      </div>
    {:else if teams.length}
      <p class="muted">{tx('teams_pick_a_team_to_see_its')}</p>
    {/if}
  </div>
</div>

{#if confirm?.kind === 'member'}
  <Confirm title={tx('teams_remove_member')} danger confirm={tx('common_remove')} message={tx('teams_remove_x_from_x', { username: confirm.m.username, current: current.name })}
    onconfirm={() => removeMember(confirm.m)} onclose={() => (confirm = null)} />
{:else if confirm?.kind === 'team'}
  <Confirm title={tx('teams_delete_team')} danger confirm="Delete" message={tx('teams_delete_the_team_x_this_only', { current: current.name })}
    onconfirm={deleteTeam} onclose={() => (confirm = null)} />
{/if}

<style>
  .layout { display: grid; grid-template-columns: 260px minmax(0, 1fr); gap: 28px; align-items: start; }
  .list { display: flex; flex-direction: column; padding: 6px; }
  .list > button { justify-content: space-between; border: 0; background: none; padding: 6px 10px; }
  .list > button.active { background: var(--accent-soft); color: var(--accent-ink); font-weight: 600; }
  .pad { padding: 8px 10px; margin: 0; }
  .newteam { padding: 8px 4px 2px; border-top: 1px solid var(--line-soft); margin-top: 6px; }
  .tname { font-weight: 650; font-size: var(--t-lg); }
  .detail .section { margin-top: 14px; }
  .roles { display: grid; grid-template-columns: max-content 1fr; gap: 2px 12px; font-size: var(--t-sm); color: var(--ink-2); margin: 14px 0 0; }
  .roles dt { font-weight: 600; color: var(--ink); }
  .roles dd { margin: 0; }
  .spaces { padding-left: 18px; margin: 0 0 12px; }
  @media (max-width: 800px) { .layout { grid-template-columns: 1fr; } }
</style>
