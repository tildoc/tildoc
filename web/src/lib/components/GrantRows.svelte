<script>
  // One permission matrix: who (user or team) has which role and which
  // individual permissions, on a space, a file or the instance.
  import Icon from './Icon.svelte';
  import { get, post, del } from '../api.js';
  import { errorToast, session } from '../state.svelte.js';
  import { permLabel, permHelp } from '../perms.js';

  let { url, scope, canEdit = false, held = null, onchanged } = $props(); // held: what the caller may give
  let grants = $state([]);
  let roles = $state([]);
  let catalogue = $state({ space: [], file: [], instance: [] });
  let users = $state([]);
  let teams = $state([]);
  let open = $state(null);
  let who = $state('user'), user = $state(''), team = $state(''), role = $state('');

  const roleScope = $derived(scope === 'instance' ? 'instance' : 'space');
  const mine = (p) => !held || held.includes(p);
  const usable = $derived(roles.filter((r) => r.scope === roleScope && r.permissions.every(mine)));
  const perms = $derived(catalogue[scope].filter(mine));

  async function load() {
    try {
      grants = await get(url);
    } catch (e) {
      errorToast(e);
    }
  }
  $effect(() => {
    load();
    get('/roles').then((r) => (roles = r)).catch(() => {});
    get('/permissions').then((c) => (catalogue = c)).catch(() => {});
    if (canEdit) {
      get('/users').then((u) => (users = u.filter((x) => x.id !== session.user?.id))).catch(() => {});
      get('/teams').then((t) => (teams = t)).catch(() => {});
    }
  });

  const subject = (g) => (g.team_id ? { team_id: g.team_id } : { user: g.user_id });
  async function save(g, nextRole, permissions) {
    try {
      await post(url, { ...subject(g), role: nextRole, permissions });
      await load();
      onchanged?.();
    } catch (e) {
      errorToast(e);
      load();
    }
  }
  function toggle(g, p, on) {
    save(g, g.role, on ? [...g.permissions, p] : g.permissions.filter((x) => x !== p));
  }
  async function add(e) {
    e.preventDefault();
    try {
      await post(url, { ...(who === 'user' ? { user } : { team_id: team }), role, permissions: [] });
      user = '';
      await load();
      onchanged?.();
    } catch (err) {
      errorToast(err);
    }
  }
  async function remove(g) {
    try {
      await del(`${url}/${g.id}`);
      await load();
      onchanged?.();
    } catch (err) {
      errorToast(err);
    }
  }
  $effect(() => {
    if (!role && usable.length) role = usable[0].slug;
  });
  const fromRole = (g) => roles.find((r) => r.slug === g.role)?.permissions ?? [];
</script>

{#if grants.length}
  <ul class="grants">
    {#each grants as g (g.id)}
      <li>
        <div class="line">
          <Icon name={g.team_id ? 'users' : 'key'} size={16} />
          <span class="who">{g.team_id ? `Team ${g.team_name}` : g.display_name || g.username}{#if g.username && !g.team_id}<small class="muted"> @{g.username}</small>{/if}</span>
          {#if canEdit}
            <select value={g.role} aria-label="Role of {g.username || g.team_name}" onchange={(e) => save(g, e.currentTarget.value, g.permissions)}>
              <option value="">Individual only</option>
              {#each usable as r (r.id)}<option value={r.slug}>{r.name}</option>{/each}
              {#if g.role && !usable.some((r) => r.slug === g.role)}<option value={g.role}>{g.role_name}</option>{/if}
            </select>
            <button class="small ghost" aria-expanded={open === g.id} onclick={() => (open = open === g.id ? null : g.id)}>Permissions{g.permissions.length ? ` +${g.permissions.length}` : ''}</button>
            <button class="small ghost" onclick={() => remove(g)}>Remove</button>
          {:else}
            <span class="pill">{g.role_name || 'Individual'}{g.permissions.length ? ` +${g.permissions.length}` : ''}</span>
          {/if}
        </div>
        {#if open === g.id}
          <div class="matrix">
            {#each perms as p (p)}
              {@const inRole = fromRole(g).includes(p)}
              <label class="inline help" title={permHelp(p)}>
                <input type="checkbox" checked={inRole || g.permissions.includes(p)} disabled={inRole} onchange={(e) => toggle(g, p, e.currentTarget.checked)} /> {permLabel(p)}
              </label>
            {/each}
          </div>
        {/if}
      </li>
    {/each}
  </ul>
{:else}
  <p class="muted">Nobody yet.</p>
{/if}

{#if canEdit}
  <form class="add" onsubmit={add}>
    <div class="seg" role="radiogroup" aria-label="Share with">
      <button type="button" role="radio" aria-checked={who === 'user'} class:active={who === 'user'} onclick={() => (who = 'user')}>A person</button>
      <button type="button" role="radio" aria-checked={who === 'team'} class:active={who === 'team'} onclick={() => (who = 'team')}>A team</button>
    </div>
    {#if who === 'user'}
      <input list="grant-users" bind:value={user} placeholder="Username" aria-label="User to add" required />
      <datalist id="grant-users">{#each users as u (u.id)}<option value={u.username}>{u.display_name}</option>{/each}</datalist>
    {:else}
      <select bind:value={team} aria-label="Team" required>
        <option value="" disabled>Choose a team</option>
        {#each teams as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
      </select>
    {/if}
    <select bind:value={role} aria-label="Role">{#each usable as r (r.id)}<option value={r.slug}>{r.name}</option>{/each}</select>
    <button class="primary">Add</button>
  </form>
{/if}

<style>
  .grants { list-style: none; padding: 0; margin: 0 0 16px; display: flex; flex-direction: column; }
  .grants li { padding: 8px 0; border-bottom: 1px solid var(--line-soft); }
  .line { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .line :global(svg) { color: var(--ink-2); }
  .line select { width: auto; }
  .who { flex: 1; min-width: 120px; }
  .matrix { display: grid; grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 4px 12px; padding: 10px 0 4px 26px; }
  .help { cursor: help; }
  .add { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 16px; }
  .add input { flex: 1; min-width: 160px; width: auto; }
  .add select { width: auto; }
</style>
