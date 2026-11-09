<script>
  import { SOURCE_URL } from '#lib/format.js';
  import { goto } from '$app/navigation';
  import { get, post } from '#lib/api.js';
  import { loadSession } from '#lib/state.svelte.js';
  let username = $state(''), password = $state(''), error = $state(''), busy = $state(false);
  let providers = $state([]);
  const q = new URLSearchParams(location.search);
  const pending = q.has('pending');
  const failure = { expired: 'That sign-in expired. Try again.', cancelled: 'The sign-in was cancelled.', provider: 'The sign-in could not be completed. Try again.', disabled: 'This account cannot sign in.' }[q.get('error')] ?? '';
  $effect(() => { get('/auth/providers', { noRedirect: true }).then((p) => (providers = p)).catch(() => {}); });
  async function submit(e) {
    e.preventDefault();
    busy = true;
    error = '';
    try {
      await post('/auth/login', { username, password });
      await loadSession();
      let back = null;
      try {
        back = sessionStorage.getItem('afterLogin'); // came from a link that needs a login
        sessionStorage.removeItem('afterLogin');
      } catch {}
      if (back?.startsWith(location.origin + '/s#')) location.replace(back);
      else goto('/');
    } catch (err) {
      error = err.message;
    }
    busy = false;
  }
</script>

<div class="center">
  <form class="auth" onsubmit={submit}>
    <p class="brand"><img src="/icon-64.png" alt="" width="26" height="26" /><a href={SOURCE_URL} target="_blank" rel="noopener noreferrer" title="Source code on GitHub">tildoc</a></p>
    <h1>Sign in</h1>
    {#if pending}<p class="note">Your account is waiting for approval by an administrator. Sign in again once it is approved.</p>{/if}
    {#if failure}<p class="note">{failure}</p>{/if}
    <label>Username<input autocomplete="username" bind:value={username} required /></label>
    <label>Password<input type="password" autocomplete="current-password" bind:value={password} required /></label>
    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <button class="primary" disabled={busy}>Sign in</button>
    {#each providers as p (p.id)}
      <a class="btn provider" href={`/api/auth/${p.id}/start`} data-sveltekit-reload>Sign in with {p.name}</a>
    {/each}
  </form>
</div>

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .auth { width: min(360px, 100%); display: flex; flex-direction: column; padding: 32px 32px 28px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); }
  .brand a { color: inherit; }
  .note { margin: 0 0 12px; padding: 8px 10px; background: var(--surface-2); font-size: var(--t-sm); }
  .provider { display: block; text-align: center; margin-top: 10px; }
  .brand { display: flex; align-items: center; gap: 10px; font-weight: 600; font-size: var(--t-md); margin: 0 0 22px; }
  h1 { margin-bottom: 18px; }
  .err { margin: 0 0 12px; }
  button { margin-top: 4px; min-height: 38px; }
</style>
