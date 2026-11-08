<script>
  import { get, post, put, del, patch } from '#lib/api.js';
  import { session, refreshSpaces, toast, errorToast } from '#lib/state.svelte.js';
  import Confirm from '#lib/components/Confirm.svelte';

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
      toast('Team space created', 'success');
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
  <h1>Teams</h1>
  <div class="layout">
    <div class="card list">
      {#each teams as t (t.id)}
        <button class:active={current?.id === t.id} onclick={() => (current = t)}>{t.name}<small class="muted">{t.role || 'admin view'}</small></button>
      {:else}<p class="muted">You are not a member of any team.</p>{/each}
      <form onsubmit={createTeam} class="row" style="margin-top:10px">
        <input placeholder="New team name" bind:value={newTeam} required style="flex:1" />
        <button class="primary">Create</button>
      </form>
    </div>
    {#if current}
      <div class="card detail">
        <div class="row" style="justify-content:space-between">
          <h2 style="margin:0">{current.name}</h2>
          {#if current.role === 'owner'}<button class="small" onclick={() => (confirm = { kind: 'team' })}>Delete team</button>{/if}
        </div>
        <h2>Members</h2>
        <table class="plain">
          <thead><tr><th>User</th><th>Role</th><th></th></tr></thead>
          <tbody>
            {#each members as m (m.user_id)}
              <tr>
                <td>{m.display_name} <small class="muted">@{m.username}</small></td>
                <td>
                  {#if canManage}
                    <select value={m.role} onchange={(e) => setRole(m, e.target.value)} style="width:auto">
                      {#each ['owner', 'admin', 'member', 'viewer'] as r}<option value={r}>{r}</option>{/each}
                    </select>
                  {:else}{m.role}{/if}
                </td>
                <td class="row end">{#if canManage || m.user_id === session.user.id}<button class="small" onclick={() => (confirm = { kind: 'member', m })}>{m.user_id === session.user.id ? 'Leave' : 'Remove'}</button>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
        {#if canManage}
          <form class="row" style="margin-top:10px" onsubmit={add}>
            <input list="users" placeholder="username" bind:value={addUser} required style="flex:1;min-width:140px" />
            <datalist id="users">{#each users.filter((u) => !members.some((m) => m.user_id === u.id)) as u}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
            <select bind:value={addRole} style="width:auto">{#each ['viewer', 'member', 'admin', 'owner'] as r}<option value={r}>{r}</option>{/each}</select>
            <button class="primary">Add member</button>
          </form>
          <p class="muted small">viewer: read · member: upload, edit, move · admin: + delete, manage members · owner: + manage owners, delete team</p>
        {/if}
        <h2>Team spaces</h2>
        <ul>{#each teamSpaces as s}<li><a href={`/f/${s.root_folder_id}`}>{s.name}</a> <small class="muted mono">{s.slug}</small></li>{:else}<li class="muted">None yet</li>{/each}</ul>
        {#if canManage}<button onclick={createSpace}>New team space</button>{/if}
      </div>
    {/if}
  </div>
</div>

{#if confirm?.kind === 'member'}
  <Confirm title="Remove member" danger confirm="Remove" message={`Remove ${confirm.m.username} from ${current.name}?`}
    onconfirm={() => removeMember(confirm.m)} onclose={() => (confirm = null)} />
{:else if confirm?.kind === 'team'}
  <Confirm title="Delete team" danger confirm="Delete" message={`Delete team ${current.name}? Only possible when its spaces contain no documents.`}
    onconfirm={deleteTeam} onclose={() => (confirm = null)} />
{/if}

<style>
  .layout { display: grid; grid-template-columns: 280px 1fr; gap: 14px; align-items: start; }
  .list { display: flex; flex-direction: column; gap: 4px; }
  .list > button { justify-content: space-between; border: 0; }
  .list > button.active { background: var(--accent-soft); color: var(--accent-text); }
  .small { font-size: 0.8rem; }
  @media (max-width: 800px) { .layout { grid-template-columns: 1fr; } }
</style>
