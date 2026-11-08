<script>
  import { untrack, onMount } from 'svelte';
  import Modal from './Modal.svelte';
  let { title, label = 'Name', value = '', confirm = 'OK', onsubmit, onclose } = $props();
  let v = $state(untrack(() => value));
  let input;
  // once when the dialog opens: focus and select the name without its
  // extension. (As an $effect reading v it re-ran on every keystroke and
  // re-selected the text, so each key replaced everything typed before.)
  onMount(() => {
    input.focus();
    const dot = v.lastIndexOf('.');
    input.setSelectionRange(0, dot > 0 ? dot : v.length);
  });
</script>

<Modal {title} {onclose}>
  <form onsubmit={(e) => { e.preventDefault(); v.trim() && onsubmit(v.trim()); }}>
    <label>{label}<input bind:this={input} bind:value={v} /></label>
    <div class="row end"><button type="button" onclick={onclose}>Cancel</button><button class="primary" type="submit">{confirm}</button></div>
  </form>
</Modal>
