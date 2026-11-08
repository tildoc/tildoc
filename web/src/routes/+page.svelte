<script>
  import { goto } from '$app/navigation';
  import { session } from '#lib/state.svelte.js';
  $effect(() => {
    const home = session.spaces.find((s) => s.owner_user_id === session.user?.id) ?? session.spaces[0];
    if (home) goto(`/f/${home.root_folder_id}`, { replaceState: true });
  });
</script>

<div class="page"><p class="muted">{session.spaces.length ? 'Opening your space…' : 'You have no spaces yet. Create one under Spaces.'}</p></div>
