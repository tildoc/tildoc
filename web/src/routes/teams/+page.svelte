<script>
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

  let canManage = $derived(current && (current.role === 'owner' || current.role === 'admin' || session.user.is_admin));
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
    const name = prompt('Name of the new team space', current.name);
    if (!name) return;
    try {
      await post('/spaces', { name, team_id: current.id });
      await refreshSpaces();
      toast('Created the team space', 'success');
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
    <h1>Teams</h1>
    <p>A team shares spaces. Roles decide what each member may do in the team’s spaces.</p>
  </header>
  <div class="layout">
    <div class="list sheet">
      {#each teams as t (t.id)}
        <button class:active={current?.id === t.id} onclick={() => (current = t)}>{t.name}<small class="muted">{t.role || 'administrator view'}</small></button>
      {:else}<p class="muted pad">You are not in any team yet.</p>{/each}
      <form onsubmit={createTeam} class="row newteam">
        <input placeholder="New team name" bind:value={newTeam} required aria-label="New team name" style="flex:1" />
        <button class="primary">Create</button>
      </form>
    </div>
    {#if current}
      <div class="detail">
        <div class="row" style="justify-content:space-between">
          <h2 class="tname">{current.name}</h2>
          {#if current.role === 'owner'}<button class="small ghost" onclick={() => (confirm = { kind: 'team' })}>Delete team</button>{/if}
        </div>
        <section class="section">
          <h2>Members</h2>
          <div class="table-wrap">
            <table class="plain">
              <thead><tr><th>Person</th><th>Role</th><th></th></tr></thead>
              <tbody>
                {#each members as m (m.user_id)}
                  <tr>
                    <td>{m.display_name} <small class="muted">@{m.username}</small></td>
                    <td>
                      {#if canManage}
                        <select value={m.role} onchange={(e) => setRole(m, e.target.value)} style="width:auto" aria-label="Role of {m.username}">
                          {#each ['owner', 'admin', 'member', 'viewer'] as r}<option value={r}>{r}</option>{/each}
                        </select>
                      {:else}{m.role}{/if}
                    </td>
                    <td class="actions">{#if canManage || m.user_id === session.user.id}<button class="small ghost" onclick={() => (confirm = { kind: 'member', m })}>{m.user_id === session.user.id ? 'Leave' : 'Remove'}</button>{/if}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          </div>
          {#if canManage}
            <form class="row" style="margin-top:12px" onsubmit={add}>
              <input list="users" placeholder="Username" bind:value={addUser} required style="flex:1;min-width:140px" aria-label="Username" />
              <datalist id="users">{#each users.filter((u) => !members.some((m) => m.user_id === u.id)) as u}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
              <select bind:value={addRole} style="width:auto" aria-label="Role">{#each ['viewer', 'member', 'admin', 'owner'] as r}<option value={r}>{r}</option>{/each}</select>
              <button class="primary">Add member</button>
            </form>
            <dl class="roles">
              <dt>viewer</dt><dd>reads documents</dd>
              <dt>member</dt><dd>also uploads, edits and moves</dd>
              <dt>admin</dt><dd>also deletes and manages members</dd>
              <dt>owner</dt><dd>also manages owners and can delete the team</dd>
            </dl>
          {/if}
        </section>
        <section class="section">
          <h2>Team spaces</h2>
          <ul class="spaces">{#each teamSpaces as s}<li><a href={`/f/${s.root_folder_id}`}>{s.name}</a> <small class="muted mono">{s.slug}</small></li>{:else}<li class="muted">No spaces yet.</li>{/each}</ul>
          {#if canManage}<button onclick={createSpace}><Icon name="plus" size={16} /> New team space</button>{/if}
        </section>
      </div>
    {:else if teams.length}
      <p class="muted">Pick a team to see its members and spaces.</p>
    {/if}
  </div>
</div>

{#if confirm?.kind === 'member'}
  <Confirm title="Remove member" danger confirm="Remove" message={`Remove ${confirm.m.username} from ${current.name}?`}
    onconfirm={() => removeMember(confirm.m)} onclose={() => (confirm = null)} />
{:else if confirm?.kind === 'team'}
  <Confirm title="Delete team" danger confirm="Delete" message={`Delete the team ${current.name}? This only works when its spaces contain no documents.`}
    onconfirm={deleteTeam} onclose={() => (confirm = null)} />
{/if}

<style>
  .layout { display: grid; grid-template-columns: 260px minmax(0, 1fr); gap: 28px; align-items: start; max-width: 1100px; }
  .list { display: flex; flex-direction: column; padding: 6px; }
  .list > button { justify-content: space-between; border: 0; background: none; padding: 6px 10px; }
  .list > button.active { background: var(--accent-soft); color: var(--accent-ink); font-weight: 600; }
  .pad { padding: 8px 10px; margin: 0; }
  .newteam { padding: 8px 4px 2px; border-top: 1px solid var(--line-soft); margin-top: 6px; }
  .tname { font-family: var(--font-read); font-weight: 560; font-size: var(--t-lg); }
  .detail .section { margin-top: 14px; }
  .roles { display: grid; grid-template-columns: max-content 1fr; gap: 2px 12px; font-size: var(--t-sm); color: var(--ink-2); margin: 14px 0 0; }
  .roles dt { font-weight: 600; color: var(--ink); }
  .roles dd { margin: 0; }
  .spaces { padding-left: 18px; margin: 0 0 12px; }
  @media (max-width: 800px) { .layout { grid-template-columns: 1fr; } }
</style>
