<script>
  // Who is on this server and what they may do.
  import { get, post, patch, del } from '#lib/api.js';
  import { session, toast, errorToast } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import GrantRows from '#lib/components/GrantRows.svelte';
  import { permLabel, permHelp } from '#lib/perms.js';

  const can = (p) => session.user?.instance_permissions?.includes(p);
  let roles = $state([]), catalogue = $state({ space: [], instance: [] }), newRole = $state(''), newScope = $state('space');
  async function loadRoles() {
    [roles, catalogue] = await Promise.all([get('/roles'), get('/permissions')]);
  }
  $effect(() => { loadRoles().catch(errorToast); });
  async function setRolePerm(r, p, on) {
    try {
      await patch(`/roles/${r.id}`, { permissions: on ? [...r.permissions, p] : r.permissions.filter((x) => x !== p) });
    } catch (e) {
      errorToast(e);
    }
    loadRoles();
  }
  async function addRole(e) {
    e.preventDefault();
    try {
      await post('/roles', { name: newRole, scope: newScope, permissions: [] });
      newRole = '';
      loadRoles();
    } catch (err) {
      errorToast(err);
    }
  }
  async function deleteRole(r) {
    try {
      await del(`/roles/${r.id}`);
      loadRoles();
    } catch (err) {
      errorToast(err);
    }
  }
  let events = $state([]);
  $effect(() => { if (can('audit.view')) get('/admin/audit').then((e) => (events = e)).catch(errorToast); });
  const detail = (e) => Object.entries(e.details ?? {}).filter(([, v]) => v !== null && v !== '' && !(Array.isArray(v) && !v.length)).map(([k, v]) => `${k}: ${typeof v === 'object' ? JSON.stringify(v) : v}`).join(', ');
  let users = $state([]);
  let nu = $state({ username: '', password: '', display_name: '', is_admin: false });
  async function load() {
    if (can('users.manage')) users = await get('/users');
  }
  $effect(() => { load().catch(errorToast); });
  async function approve(u) {
    try {
      await post(`/users/${u.id}/approve`, {});
      toast(`Approved ${u.username}`, 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function createUser(e) {
    e.preventDefault();
    try {
      await post('/users', nu);
      toast(`Added ${nu.username} with a personal space`, 'success');
      nu = { username: '', password: '', display_name: '', is_admin: false };
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  async function update(u, body) {
    try {
      await patch(`/users/${u.id}`, body);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function resetPw(u) {
    const pw = prompt(`New password for ${u.username} (min. 8 characters)`);
    if (pw) update(u, { password: pw });
  }
  async function remove(u) {
    if (!confirm(`Delete ${u.username}? Their empty spaces go too. Spaces with documents must be transferred or deleted first.`)) return;
    try {
      await del(`/users/${u.id}`);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<svelte:head><title>Access – tildoc</title></svelte:head>

<div class="page">
  <header>
    <h1>Access</h1>
    <p>People on this server, roles, and server permissions. Permissions on single spaces are set in each space's sharing dialog.</p>
  </header>
  {#if can('users.manage')}
  <section class="section">
    <h2>People</h2>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>Person</th><th>Administrator</th><th>Status</th><th>Created</th><th></th></tr></thead>
        <tbody>
          {#each users as u (u.id)}
            <tr>
              <td>{u.display_name} <small class="muted">@{u.username}</small></td>
              <td><input type="checkbox" aria-label="Administrator" checked={u.is_admin} disabled={u.id === session.user.id} onchange={(e) => update(u, { is_admin: e.target.checked })} /></td>
              <td>{#if u.status === 'pending'}<span class="pill">Waiting for approval</span>{:else}{u.status === 'disabled' ? 'Disabled' : 'Active'}{/if}</td>
              <td>{date(u.created_at)}</td>
              <td class="actions">
                {#if u.status === 'pending'}
                  <button class="small primary" onclick={() => approve(u)}>Approve</button>
                  <button class="small ghost" onclick={() => remove(u)}>Decline</button>
                {:else if u.id !== session.user.id}
                  <button class="small" onclick={() => update(u, { disabled: !u.disabled })}>{u.disabled ? 'Enable' : 'Disable'}</button>
                  <button class="small" onclick={() => resetPw(u)}>Set password</button>
                  <button class="small ghost" onclick={() => remove(u)}>Delete</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <form onsubmit={createUser} style="margin-top:18px">
      <h3 style="margin-bottom:8px">Add a person</h3>
      <div class="section-grid">
        <label>Username<input bind:value={nu.username} required /></label>
        <label>Display name<input bind:value={nu.display_name} /></label>
        <label>First password<input type="password" autocomplete="new-password" minlength="8" bind:value={nu.password} required /><span class="hint">At least 8 characters.</span></label>
      </div>
      <div class="row"><label class="inline" style="margin:0"><input type="checkbox" bind:checked={nu.is_admin} /> Administrator</label><button class="primary">Add person</button></div>
    </form>
  </section>
  {/if}
  {#if can('roles.manage')}
  <section class="section">
    <h2>Roles</h2>
    <p>A role is a set of permissions. Changing one changes it for everyone who holds it.</p>
    {#each [['space', 'On spaces'], ['instance', 'On this server']] as [scope, title] (scope)}
      <h3 class="sub">{title}</h3>
      <div class="table-wrap">
        <table class="plain matrix">
          <thead><tr><th>Role</th>{#each catalogue[scope] ?? [] as p (p)}<th title={permHelp(p)}><span>{permLabel(p)}</span></th>{/each}<th></th></tr></thead>
          <tbody>
            {#each roles.filter((r) => r.scope === scope) as r (r.id)}
              <tr>
                <td>{r.name}</td>
                {#each catalogue[scope] ?? [] as p (p)}
                  <td><input type="checkbox" aria-label="{permLabel(p)} for {r.name}" checked={r.permissions.includes(p)} disabled={r.slug === 'administrator'} onchange={(e) => setRolePerm(r, p, e.currentTarget.checked)} /></td>
                {/each}
                <td class="actions">{#if !r.built_in}<button class="small ghost" onclick={() => deleteRole(r)}>Delete</button>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/each}
    <form class="row" style="margin-top:12px" onsubmit={addRole}>
      <input placeholder="New role" bind:value={newRole} aria-label="New role" required style="flex:1;min-width:160px" />
      <select bind:value={newScope} aria-label="Applies to" style="width:auto"><option value="space">On spaces</option><option value="instance">On this server</option></select>
      <button class="primary">Add role</button>
    </form>
  </section>
  <section class="section">
    <h2>Server permissions</h2>
    <p>Who may do what on this server. These never give access to documents.</p>
    <GrantRows url="/instance/grants" scope="instance" canEdit />
  </section>
  {/if}
  {#if can('audit.view')}
  <section class="section">
    <h2>Activity</h2>
    <p>The newest changes to permissions, shares, keys and accounts. This list can't be edited.</p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>When</th><th>Who</th><th>What</th><th>On</th><th>Details</th></tr></thead>
        <tbody>
          {#each events as e (e.id)}
            <tr><td>{date(e.at)}</td><td>{e.actor}</td><td>{e.action}</td><td>{e.target_type}</td><td class="muted small">{detail(e)}</td></tr>
          {:else}<tr><td colspan="5" class="muted">Nothing yet.</td></tr>{/each}
        </tbody>
      </table>
    </div>
  </section>
  {/if}
</div>

<style>
  .sub { margin: 18px 0 6px; }
  .matrix th span { display: inline-block; max-width: 74px; font-size: var(--t-sm); font-weight: 500; white-space: normal; cursor: help; }
  .matrix td:not(:first-child) { text-align: center; }
</style>
