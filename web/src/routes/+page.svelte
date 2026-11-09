<script>
  import { tx } from '#lib/i18n/index.js';
  import { goto } from '$app/navigation';
  import { session } from '#lib/state.svelte.js';
  $effect(() => {
    const home = session.spaces.find((s) => s.owner_user_id === session.user?.id) ?? session.spaces[0];
    if (home) goto(`/f/${home.root_folder_id}`, { replaceState: true });
  });
</script>

<div class="page"><p class="muted">{session.spaces.length ? tx('home_opening_your_space') : tx('home_you_have_no_spaces_yet_create')}</p></div>
