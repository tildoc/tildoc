<script>
  import { tx } from '#lib/i18n/index.js';
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
      toast(tx('access_approved_x', { username: u.username }), 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function createUser(e) {
    e.preventDefault();
    try {
      await post('/users', nu);
      toast(tx('access_added_x_with_a_personal_space', { username: nu.username }), 'success');
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
    const pw = prompt(tx('access_new_password_for_x_min_8', { username: u.username }));
    if (pw) update(u, { password: pw });
  }
  async function remove(u) {
    if (!confirm(tx('access_delete_x_their_empty_spaces_go', { username: u.username }))) return;
    try {
      await del(`/users/${u.id}`);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<svelte:head><title>{tx('access_access_tildoc')}</title></svelte:head>

<div class="page">
  <header>
    <h1>{tx('common_access')}</h1>
    <p>{tx('access_people_on_this_server_roles_and')}</p>
  </header>
  {#if can('users.manage')}
  <section class="section">
    <h2>{tx('access_people')}</h2>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('access_person')}</th><th>{tx('access_administrator')}</th><th>{tx('access_status')}</th><th>{tx('common_created')}</th><th></th></tr></thead>
        <tbody>
          {#each users as u (u.id)}
            <tr>
              <td>{u.display_name} <small class="muted">@{u.username}</small></td>
              <td><input type="checkbox" aria-label={tx('access_administrator')} checked={u.is_admin} disabled={u.id === session.user.id} onchange={(e) => update(u, { is_admin: e.target.checked })} /></td>
              <td>{#if u.status === 'pending'}<span class="pill">{tx('access_waiting_for_approval')}</span>{:else}{u.status === 'disabled' ? tx('access_disabled') : tx('access_active')}{/if}</td>
              <td>{date(u.created_at)}</td>
              <td class="actions">
                {#if u.status === 'pending'}
                  <button class="small primary" onclick={() => approve(u)}>{tx('access_approve')}</button>
                  <button class="small ghost" onclick={() => remove(u)}>{tx('access_decline')}</button>
                {:else if u.id !== session.user.id}
                  <button class="small" onclick={() => update(u, { disabled: !u.disabled })}>{u.disabled ? tx('access_enable') : tx('access_disable')}</button>
                  <button class="small" onclick={() => resetPw(u)}>{tx('access_set_password')}</button>
                  <button class="small ghost" onclick={() => remove(u)}>{tx('common_delete')}</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <form onsubmit={createUser} style="margin-top:18px">
      <h3 style="margin-bottom:8px">{tx('access_add_a_person')}</h3>
      <div class="section-grid">
        <label>{tx('common_username')}<input bind:value={nu.username} required /></label>
        <label>{tx('common_display_name')}<input bind:value={nu.display_name} /></label>
        <label>{tx('access_first_password')}<input type="password" autocomplete="new-password" minlength="8" bind:value={nu.password} required /><span class="hint">{tx('common_at_least_8_characters')}</span></label>
      </div>
      <div class="row"><label class="inline" style="margin:0"><input type="checkbox" bind:checked={nu.is_admin} /> {tx('access_administrator')}</label><button class="primary">{tx('access_add_person')}</button></div>
    </form>
  </section>
  {/if}
  {#if can('roles.manage')}
  <section class="section">
    <h2>{tx('access_roles')}</h2>
    <p>{tx('access_a_role_is_a_set_of')}</p>
    {#each [['space', tx('access_on_spaces')], ['instance', tx('access_on_this_server')]] as [scope, title] (scope)}
      <h3 class="sub">{title}</h3>
      <div class="table-wrap">
        <table class="plain matrix">
          <thead><tr><th>{tx('common_role')}</th>{#each catalogue[scope] ?? [] as p (p)}<th title={permHelp(p)}><span>{permLabel(p)}</span></th>{/each}<th></th></tr></thead>
          <tbody>
            {#each roles.filter((r) => r.scope === scope) as r (r.id)}
              <tr>
                <td>{r.name}</td>
                {#each catalogue[scope] ?? [] as p (p)}
                  <td><input type="checkbox" aria-label={tx('access_permission_for_role', { permission: permLabel(p), role: r.name })} checked={r.permissions.includes(p)} disabled={r.slug === 'administrator'} onchange={(e) => setRolePerm(r, p, e.currentTarget.checked)} /></td>
                {/each}
                <td class="actions">{#if !r.built_in}<button class="small ghost" onclick={() => deleteRole(r)}>{tx('common_delete')}</button>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/each}
    <form class="row" style="margin-top:12px" onsubmit={addRole}>
      <input placeholder={tx('access_new_role')} bind:value={newRole} aria-label={tx('access_new_role')} required style="flex:1;min-width:160px" />
      <select bind:value={newScope} aria-label={tx('access_applies_to')} style="width:auto"><option value="space">{tx('access_on_spaces')}</option><option value="instance">{tx('access_on_this_server')}</option></select>
      <button class="primary">{tx('access_add_role')}</button>
    </form>
  </section>
  <section class="section">
    <h2>{tx('access_server_permissions')}</h2>
    <p>{tx('access_who_may_do_what_on_this')}</p>
    <GrantRows url="/instance/grants" scope="instance" canEdit />
  </section>
  {/if}
  {#if can('audit.view')}
  <section class="section">
    <h2>{tx('access_activity')}</h2>
    <p>{tx('access_the_newest_changes_to_permissions_shares')}</p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('access_when')}</th><th>{tx('access_who')}</th><th>{tx('access_what')}</th><th>{tx('access_on')}</th><th>{tx('common_details')}</th></tr></thead>
        <tbody>
          {#each events as e (e.id)}
            <tr><td>{date(e.at)}</td><td>{e.actor}</td><td>{e.action}</td><td>{e.target_type}</td><td class="muted small">{detail(e)}</td></tr>
          {:else}<tr><td colspan="5" class="muted">{tx('access_nothing_yet')}</td></tr>{/each}
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
