<script>
  // The history graph laid out horizontally (like a git client): time runs
  // left to right, lanes stack downwards. Same layout as HistoryGraph
  // (lib/history.js); the vertical drawing is mirrored on the diagonal, so
  // both always agree. Every node is a button that shows it in the viewer.
  import { tick } from 'svelte';
  import { layoutHistory, edgePath, nodeVariant, nodeSelection, nodeName } from '../history.js';
  import { shownVariant, date } from '../format.js';

  let { doc, viewVersion = $bindable(0), viewVariant = $bindable('') } = $props();

  // wide steps, flat lanes: the tree reads left to right, branches go down
  const STEP = 44, LANE = 10, PAD = 9, R = 4;
  const graph = $derived(layoutHistory(doc.versions));
  // vertical geometry: lane -> x, row -> y; drawn inside a transposing group
  const lx = (lane) => PAD + lane * LANE;
  const ry = (row) => PAD + row * STEP;
  const width = $derived(PAD * 2 + (graph.rows.length - 1) * STEP);
  const height = $derived(PAD * 2 + (graph.lanes - 1) * LANE);
  const color = (lane) => `var(--lane-${lane % 4})`;

  const shownVer = $derived(viewVersion || doc.version);
  const shownVar = $derived(shownVariant(doc, viewVersion, viewVariant));
  const isShown = (r) => r.version === shownVer && nodeVariant(r) === shownVar;
  const shownNode = $derived(graph.rows.find(isShown));

  function select(r) {
    ({ viewVersion, viewVariant } = nodeSelection(doc, r));
  }

  function tip(r) {
    const d = r.data;
    return `${nodeName(r)}${r.version === doc.version && r.kind === 'version' ? ' (newest)' : ''}, ${date(d.created_at)}`;
  }

  // long histories scroll; keep the shown node in view
  let box = $state();
  $effect(() => {
    const n = shownNode;
    if (!box || !n) return;
    tick().then(() => box.querySelector('[aria-pressed="true"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' }));
  });
</script>

<div class="strip" role="group" aria-label="History: versions and variants">
  <div class="scroll" bind:this={box}>
    <div class="canvas" style:width="{width}px" style:height="{height}px">
      <svg width={width} height={height} aria-hidden="true">
        <g transform="matrix(0 1 1 0 0 0)">
          {#each graph.edges as e, i (i)}
            <path d={edgePath(e, lx, ry)} class="edge {e.kind}"
              style:stroke={color(e.kind === 'branch' ? e.to.lane : e.kind === 'merge' ? e.from.lane : e.kind === 'restore' ? e.via : 0)} />
          {/each}
          {#each graph.rows as r (r.id)}
            {#if isShown(r)}<circle cx={lx(r.lane)} cy={ry(r.row)} r={R + 3} class="ring" style:stroke={color(r.lane)} />{/if}
            {#if r.kind === 'version'}
              <circle cx={lx(r.lane)} cy={ry(r.row)} r={R} style:fill={color(0)} style:stroke={color(0)} />
            {:else}
              <circle cx={lx(r.lane)} cy={ry(r.row)} r={R} class="variant" style:stroke={color(r.lane)} />
            {/if}
          {/each}
        </g>
      </svg>
      {#each graph.rows as r (r.id)}
        <button class="node" style:left="{ry(r.row)}px" style:top="{lx(r.lane)}px" aria-pressed={isShown(r)}
          title={tip(r)} aria-label="Show {nodeName(r).replace(/^Version/, 'version')}" onclick={() => select(r)}></button>
      {/each}
    </div>
  </div>
  {#if shownNode}<span class="label">{nodeName(shownNode)}</span>{/if}
</div>

<style>
  .strip {
    display: flex; align-items: center; gap: 8px; min-width: 0; margin-right: 6px;
    --lane-0: var(--stage-ink);
    --lane-1: var(--manila);
    --lane-2: #7cc495;
    --lane-3: var(--stage-ink-2);
  }
  .scroll { overflow-x: auto; overflow-y: hidden; max-width: clamp(180px, 34vw, 620px); scrollbar-width: thin; }
  .canvas { position: relative; }
  svg { display: block; overflow: visible; }
  .edge { fill: none; stroke-width: 2; stroke-linecap: round; }
  .edge.restore { stroke-dasharray: 3 3; }
  .ring { fill: none; stroke-width: 1.5; }
  .variant { fill: var(--stage); stroke-width: 2; }
  .node {
    position: absolute; width: 20px; height: 20px; min-height: 0; min-width: 0; margin: -10px 0 0 -10px; padding: 0;
    border: 0; border-radius: 50%; background: none; cursor: pointer;
  }
  .node:hover:not(:disabled) { background: rgb(255 255 255 / 0.12); }
  .node:focus-visible { outline: 2px solid var(--stage-ink); outline-offset: 0; }
  .label { color: var(--stage-ink-2); font-size: var(--t-sm); white-space: nowrap; }
</style>
