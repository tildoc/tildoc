<script>
  import { goto } from '$app/navigation';
  import { post } from '#lib/api.js';
  import { loadSession } from '#lib/state.svelte.js';
  let username = $state('admin'), display = $state(''), password = $state(''), password2 = $state(''), error = $state('');
  async function submit(e) {
    e.preventDefault();
    if (password !== password2) {
      error = 'Passwords do not match';
      return;
    }
    try {
      await post('/setup', { username, password, display_name: display });
      await loadSession();
      goto('/');
    } catch (err) {
      error = err.message;
    }
  }
</script>

<div class="center">
  <form class="card auth" onsubmit={submit}>
    <h1>Welcome to tildoc</h1>
    <p class="muted">Create the administrator account. This is only possible while no account exists.</p>
    <label>Username<input bind:value={username} required /></label>
    <label>Display name<input bind:value={display} /></label>
    <label>Password (min. 8 characters)<input type="password" autocomplete="new-password" bind:value={password} required minlength="8" /></label>
    <label>Repeat password<input type="password" autocomplete="new-password" bind:value={password2} required /></label>
    {#if error}<p class="err">{error}</p>{/if}
    <button class="primary">Create administrator</button>
  </form>
</div>

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .auth { width: min(400px, 100%); display: flex; flex-direction: column; }
  .err { color: var(--err); }
</style>
