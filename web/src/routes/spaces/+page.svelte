<script>
  import { get, post, patch, del } from '#lib/api.js';
  import { session, refreshSpaces, toast, errorToast } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import { goto } from '$app/navigation';
  import Confirm from '#lib/components/Confirm.svelte';

  let teams = $state([]);
  let name = $state(''), slug = $state(''), team = $state('');
  let confirm = $state(null);
  $effect(() => { get('/teams').then((t) => (teams = t)).catch(() => {}); });
  let manageable = $derived(teams.filter((t) => t.role === 'owner' || t.role === 'admin'));

  async function create(e) {
    e.preventDefault();
    try {
      const s = await post('/spaces', { name, slug, team_id: team });
      await refreshSpaces();
      name = slug = '';
      toast(`Space ${s.slug} created`, 'success');
    } catch (err) {
      errorToast(err);
    }
  }
  async function rename(s) {
    const n = prompt('New name', s.name);
    if (!n) return;
    try {
      await patch(`/spaces/${s.id}`, { name: n });
      await refreshSpaces();
    } catch (e) {
      errorToast(e);
    }
  }
  async function remove(s) {
    confirm = null;
    try {
      await del(`/spaces/${s.id}`);
      await refreshSpaces();
      toast('Space deleted', 'success');
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <h1>Spaces</h1>
  <p class="muted">A space is a top-level container: personal, or owned by a team. Its slug is the S3 bucket name and the top folder in WebDAV/SFTP.</p>
  <div class="card" style="padding:0;overflow:auto">
    <table class="plain">
      <thead><tr><th>Name</th><th>Slug</th><th>Owner</th><th>Your role</th><th>Created</th><th></th></tr></thead>
      <tbody>
        {#each session.spaces as s (s.id)}
          <tr>
            <td><button class="link" onclick={() => goto(`/f/${s.root_folder_id}`)}>{s.name}</button></td>
            <td class="mono">{s.slug}</td>
            <td>{s.team_name ? `Team ${s.team_name}` : 'Personal'}</td>
            <td>{s.role}</td>
            <td>{date(s.created_at)}</td>
            <td class="row end">
              {#if s.perm >= 3}<button class="small" onclick={() => rename(s)}>Rename</button>{/if}
              {#if s.perm >= 3}<button class="small" onclick={() => (confirm = s)}>Delete</button>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <form class="card" onsubmit={create}>
    <h2 style="margin-top:0">New space</h2>
    <div class="grid">
      <label>Name<input bind:value={name} required /></label>
      <label>Slug (optional, a-z 0-9 -)<input bind:value={slug} placeholder="derived from name" /></label>
      <label>Owner
        <select bind:value={team}>
          <option value="">Personal (me)</option>
          {#each manageable as t}<option value={t.id}>Team {t.name}</option>{/each}
        </select>
      </label>
    </div>
    <button class="primary">Create space</button>
  </form>
</div>

{#if confirm}
  <Confirm title="Delete space" danger confirm="Delete" message={`Delete space “${confirm.name}”? Only empty spaces can be deleted.`}
    onconfirm={() => remove(confirm)} onclose={() => (confirm = null)} />
{/if}

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 0 12px; }
</style>
