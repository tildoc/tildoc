<!-- A text with inline emphasis, so that a sentence stays one translation.
     Builds elements from parsed parts; a translation can't inject markup. -->
<script>
  import { txParts } from './index.js';

  let { key, vars } = $props();
  const parts = $derived(txParts(key, vars));
</script>

{#each parts as p}{#if p.tag === 'strong'}<strong>{p.text}</strong>{:else if p.tag === 'code'}<code>{p.text}</code>{:else if p.tag === 'kbd'}<kbd>{p.text}</kbd>{:else}{p.text}{/if}{/each}
