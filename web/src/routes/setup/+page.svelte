<script>
  import { goto } from '$app/navigation';
  import { post } from '#lib/api.js';
  import { loadSession } from '#lib/state.svelte.js';
  let username = $state('admin'), display = $state(''), password = $state(''), password2 = $state(''), error = $state('');
  async function submit(e) {
    e.preventDefault();
    if (password !== password2) {
      error = 'The two passwords are different.';
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
  <form class="auth" onsubmit={submit}>
    <p class="brand"><img src="/favicon.svg" alt="" width="26" height="26" />tildoc</p>
    <h1>Create the administrator</h1>
    <p class="muted">This works only while no account exists. The administrator manages people and processing; their documents stay private like everyone else’s.</p>
    <label>Username<input bind:value={username} required /></label>
    <label>Display name<input bind:value={display} /></label>
    <label>Password <span class="hint">at least 8 characters</span><input type="password" autocomplete="new-password" bind:value={password} required minlength="8" /></label>
    <label>Repeat password<input type="password" autocomplete="new-password" bind:value={password2} required /></label>
    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <button class="primary">Create administrator</button>
  </form>
</div>

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .auth { width: min(420px, 100%); display: flex; flex-direction: column; padding: 32px 32px 28px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); }
  .brand { display: flex; align-items: center; gap: 10px; font-family: var(--font-read); font-weight: 600; font-size: var(--t-md); margin: 0 0 22px; }
  h1 { margin-bottom: 10px; }
  .muted { font-size: var(--t-sm); margin-bottom: 18px; }
  button { margin-top: 4px; min-height: 38px; }
</style>
