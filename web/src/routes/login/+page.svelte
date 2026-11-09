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
  <form class="auth" onsubmit={submit}>
    <p class="brand"><img src="/icon-64.png" alt="" width="26" height="26" />tildoc</p>
    <h1>Sign in</h1>
    <label>Username<input autocomplete="username" bind:value={username} required /></label>
    <label>Password<input type="password" autocomplete="current-password" bind:value={password} required /></label>
    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <button class="primary" disabled={busy}>Sign in</button>
  </form>
</div>

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .auth { width: min(360px, 100%); display: flex; flex-direction: column; padding: 32px 32px 28px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); }
  .brand { display: flex; align-items: center; gap: 10px; font-weight: 600; font-size: var(--t-md); margin: 0 0 22px; }
  h1 { margin-bottom: 18px; }
  .err { margin: 0 0 12px; }
  button { margin-top: 4px; min-height: 38px; }
</style>
