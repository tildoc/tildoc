<script>
  import { tx } from '#lib/i18n/index.js';
  import Modal from './Modal.svelte';
  let { title, message, confirm = tx('confirm_confirm'), danger = false, onconfirm, onclose } = $props();
</script>

<Modal {title} {onclose}>
  <p>{message}</p>
  <div class="row end">
    <button onclick={onclose}>{tx('common_cancel')}</button>
    <button class={danger ? 'danger' : 'primary'} onclick={onconfirm}>{confirm}</button>
  </div>
</Modal>
