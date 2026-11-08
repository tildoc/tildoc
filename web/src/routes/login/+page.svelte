<script>
  import { goto } from '$app/navigation';
  import { post } from '#lib/api.js';
  import { loadSession } from '#lib/state.svelte.js';
  let username = $state(''), password = $state(''), error = $state(''), busy = $state(false);
  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await post('/auth/login', { username, password });
      await loadSession();
      goto('/');
    } catch (err) {
      error = err.message;
    }
    busy = false;
  }
</script>

<div class="center">
  <form class="card auth" onsubmit={submit}>
    <h1><img src="/favicon.svg" alt="" width="28" /> tildoc</h1>
    <label>Username<input autocomplete="username" bind:value={username} required /></label>
    <label>Password<input type="password" autocomplete="current-password" bind:value={password} required /></label>
    {#if error}<p class="err">{error}</p>{/if}
    <button class="primary" disabled={busy}>Sign in</button>
  </form>
</div>

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .auth { width: min(360px, 100%); display: flex; flex-direction: column; }
  h1 { display: flex; align-items: center; gap: 10px; }
  .err { color: var(--err); margin: 0 0 10px; }
</style>
