<script>
  import { tx } from '#lib/i18n/index.js';
  // Continuous-scroll PDF view on pdf.js. Only pages near the viewport are
  // rendered; canvases and text layers are released again when scrolled away.
  import { onMount, tick, untrack } from 'svelte';
  import { openPDF, pdfjs, resolveDest } from '../../viewer/pdfjs.js';
  import { terms as parseTerms, findAll } from '../../viewer/find.js';
  import { revalidate } from '../../api.js';

  let {
    url,
    textPath = '', // API path of the server's text for this PDF (pages split by \f), optional
    mode = 'full', // full | peek
    page = $bindable(1),
    numPages = $bindable(0),
    zoom = $bindable('width'), // 'auto' | 'width' | 'page' | number (1 = real size)
    scale = $bindable(1),
    query = '',
    thumbs = false,
    matchCount = $bindable(0),
    current = $bindable(-1),
    searching = $bindable(false),
    initialPage = 1,
    transitionName = ''
  } = $props();

  const PAD = untrack(() => (mode === 'peek' ? 12 : 28));
  const GAP = untrack(() => (mode === 'peek' ? 10 : 18));
  const MAX_PIXELS = 16_777_216;
  const CSS_UNITS = 96 / 72; // PDF points to CSS pixels: scale 1 is real size

  let scroller = $state();
  let thumbBox = $state();
  let cw = $state(0), ch = $state(0);
  let sizes = $state([]); // [{w, h}] at scale 1 (with rotation)
  let rotation = $state(0);
  let loading = $state(true);
  let error = $state('');
  let password = $state(null); // {cb, wrong}
  let pwValue = $state('');
  let pageHits = $state({}); // page -> number of hits
  let matches = $state.raw([]); // [{page, start, end}]

  let pdf = null;
  let task = null;
  let alive = true;
  const slots = new Map(); // page -> element
  const rendered = new Map(); // page -> {scale, rot, task, canvas, text, tl, links}
  const texts = new Map(); // page -> {content, strs, starts}
  const visible = new Set();
  let io = null;
  let anchor = { page: 1, frac: 0 };
  let scrollToCurrent = false;

  let tops = $derived.by(() => {
    const out = [];
    let y = PAD;
    for (const s of sizes) {
      out.push(y);
      y += s.h * scale + GAP;
    }
    return out;
  });

  // ------------------------------------------------------------ loading

  $effect(() => {
    const u = url;
    untrack(() => load(u));
    return () => untrack(teardown);
  });

  let loadGen = 0;
  async function load(u) {
    const gen = ++loadGen;
    loading = true;
    error = '';
    password = null;
    sizes = [];
    matches = [];
    pageHits = {};
    matchCount = 0;
    current = -1;
    anchor = { page: initialPage || 1, frac: 0 };
    try {
      const t = await openPDF(u);
      if (gen !== loadGen) {
        t.destroy();
        return;
      }
      task = t;
      t.onPassword = (cb, reason) => (password = { cb, wrong: reason === 2 });
      const doc = await t.promise;
      if (gen !== loadGen || !alive) return;
      pdf = doc;
      password = null;
      numPages = pdf.numPages;
      const first = await pdf.getPage(1);
      if (gen !== loadGen) return;
      const vp = first.getViewport({ scale: 1 });
      sizes = Array.from({ length: pdf.numPages }, () => ({ w: vp.width * CSS_UNITS, h: vp.height * CSS_UNITS }));
      loading = false;
    } catch (e) {
      if (!alive || gen !== loadGen) return;
      loading = false;
      error = e?.name === 'InvalidPDFException' ? tx('pdfview_the_file_is_not_a_valid') : e?.message || String(e);
    }
  }

  function teardown() {
    loadGen++;
    findGen++;
    for (const n of [...rendered.keys()]) release(n);
    for (const n of [...thumbsDone.keys()]) releaseThumb(n);
    texts.clear();
    task?.destroy();
    task = null;
    pdf = null;
  }

  function submitPassword(e) {
    e.preventDefault();
    password?.cb(pwValue);
    pwValue = '';
  }

  // ------------------------------------------------------------ geometry

  function computeScale() {
    if (!sizes.length || !cw) return scale;
    const ref = sizes[Math.min(page, sizes.length) - 1] ?? sizes[0];
    const availW = Math.max(80, cw - 2 * PAD);
    const availH = Math.max(80, ch - 2 * PAD);
    if (zoom === 'width') return clamp(availW / ref.w);
    if (zoom === 'auto') return clamp(Math.min(availW / ref.w, ref.w > ref.h ? availH / ref.h : 1.25));
    if (zoom === 'page') return clamp(Math.min(availW / ref.w, availH / ref.h));
    return clamp(+zoom || 1);
  }
  const clamp = (s) => Math.min(4, Math.max(0.25, s));

  $effect(() => {
    sizes.length;
    zoom;
    cw;
    ch;
    const s = untrack(computeScale);
    if (Math.abs(s - untrack(() => scale)) > 0.001) scale = s;
  });

  // after a scale change: keep the reading position, then re-render
  $effect(() => {
    scale;
    tops;
    untrack(() => {
      if (!scroller || !sizes.length) return;
      const i = Math.min(anchor.page, sizes.length) - 1;
      scroller.scrollTop = tops[i] + anchor.frac * sizes[i].h * scale - (anchor.frac === 0 ? PAD : 0);
      if (scroller.scrollWidth > scroller.clientWidth) scroller.scrollLeft = (scroller.scrollWidth - scroller.clientWidth) / 2;
      schedule();
    });
  });

  // current page = the most visible page; a page jumped to stays current while it is fully in view
  let pinned = 0;
  function onScroll() {
    if (raf) return;
    raf = requestAnimationFrame(() => {
      raf = 0;
      if (!sizes.length || !scroller) return;
      const st = scroller.scrollTop, vh = scroller.clientHeight, end = st + vh;
      let lo = 0, hi = tops.length - 1;
      while (lo < hi) {
        const mid = (lo + hi + 1) >> 1;
        if (tops[mid] <= st) lo = mid;
        else hi = mid - 1;
      }
      let best = lo, bestVis = -1;
      const vis = (i) => Math.max(0, Math.min(end, tops[i] + sizes[i].h * scale) - Math.max(st, tops[i]));
      for (let i = lo; i < tops.length && tops[i] < end; i++) {
        const v = vis(i);
        if (v > bestVis + 0.5) {
          best = i;
          bestVis = v;
        }
      }
      if (pinned) {
        const i = pinned - 1;
        const full = Math.min(sizes[i].h * scale, vh);
        if (vis(i) >= full - 1) best = i;
        else pinned = 0;
      }
      page = best + 1;
      const j = lo;
      anchor = { page: j + 1, frac: Math.max(0, (st - tops[j]) / (sizes[j].h * scale)) };
    });
  }
  let raf = 0;

  export function goTo(n) {
    if (!sizes.length || !scroller) return;
    n = Math.max(1, Math.min(sizes.length, Math.round(n)));
    anchor = { page: n, frac: 0 };
    pinned = n;
    scroller.scrollTop = tops[n - 1] - PAD;
    page = n;
  }

  export function rotate() {
    rotation = (rotation + 90) % 360;
    sizes = sizes.map((s) => ({ w: s.h, h: s.w }));
    for (const n of [...rendered.keys()]) release(n);
    for (const n of [...thumbsDone.keys()]) releaseThumb(n);
    schedule();
    scheduleThumbs();
  }

  // ------------------------------------------------------------ rendering

  function slot(el, n) {
    slots.set(n, el);
    io?.observe(el);
    return {
      destroy() {
        io?.unobserve(el);
        slots.delete(n);
        visible.delete(n);
        release(n);
      }
    };
  }

  let pending = 0;
  function schedule() {
    if (pending) return;
    pending = requestAnimationFrame(() => {
      pending = 0;
      const order = [...visible].sort((a, b) => Math.abs(a - page) - Math.abs(b - page));
      for (const n of order) renderPage(n);
    });
  }

  function release(n) {
    const r = rendered.get(n);
    if (!r) return;
    r.task?.cancel();
    if (r.canvas) {
      r.canvas.width = r.canvas.height = 0;
      r.canvas.remove();
    }
    r.text?.remove();
    r.links?.remove();
    rendered.delete(n);
  }

  async function renderPage(n) {
    if (!pdf) return;
    let r = rendered.get(n);
    if (r && r.scale === scale && r.rot === rotation) return;
    if (!r) {
      r = {};
      rendered.set(n, r);
    }
    r.task?.cancel();
    const myScale = scale, myRot = rotation;
    r.scale = myScale;
    r.rot = myRot;
    const ok = () => rendered.get(n) === r && r.scale === myScale && r.rot === myRot && slots.has(n) && alive;
    try {
      const p = await pdf.getPage(n);
      if (!ok()) return;
      const rot = (p.rotate + myRot) % 360;
      const base = p.getViewport({ scale: CSS_UNITS, rotation: rot });
      const s = sizes[n - 1];
      if (!scroller) return;
      if (s && (Math.abs(s.w - base.width) > 0.5 || Math.abs(s.h - base.height) > 0.5)) {
        const above = tops[n - 1] < scroller.scrollTop;
        const delta = (base.height - s.h) * myScale;
        sizes[n - 1] = { w: base.width, h: base.height };
        if (above) {
          await tick();
          if (scroller) scroller.scrollTop += delta;
        }
      }
      const vp = p.getViewport({ scale: myScale * CSS_UNITS, rotation: rot });
      let dpr = window.devicePixelRatio || 1;
      if (vp.width * vp.height * dpr * dpr > MAX_PIXELS) dpr = Math.sqrt(MAX_PIXELS / (vp.width * vp.height));
      const canvas = document.createElement('canvas');
      canvas.width = Math.floor(vp.width * dpr);
      canvas.height = Math.floor(vp.height * dpr);
      r.task = p.render({ canvas, viewport: vp, transform: dpr !== 1 ? [dpr, 0, 0, dpr, 0, 0] : undefined });
      await r.task.promise;
      r.task = null;
      if (!ok()) {
        canvas.width = canvas.height = 0;
        return;
      }
      const el = slots.get(n);
      if (r.canvas) {
        r.canvas.width = r.canvas.height = 0;
        r.canvas.remove();
      }
      el.prepend(canvas);
      r.canvas = canvas;
      await renderText(n, p, vp, r, ok);
      if (!r.links && ok()) await renderLinks(n, p, rot, r, ok);
    } catch (e) {
      if (e?.name !== 'RenderingCancelledException') console.warn('page', n, e);
    }
  }

  async function pageText(n, p) {
    let t = texts.get(n);
    if (t) return t;
    p ??= await pdf.getPage(n);
    const content = await p.getTextContent();
    const strs = [], starts = [];
    let joined = '';
    for (const it of content.items) {
      if (it.str === undefined) continue;
      starts.push(joined.length);
      strs.push(it.str);
      joined += it.str + (it.hasEOL ? ' ' : '');
    }
    t = { content, strs, starts, joined };
    texts.set(n, t);
    return t;
  }

  async function renderText(n, p, vp, r, ok) {
    const t = await pageText(n, p);
    if (!ok()) return;
    const { TextLayer } = await pdfjs();
    const div = document.createElement('div');
    div.className = 'textLayer';
    const tl = new TextLayer({ textContentSource: t.content, container: div, viewport: vp });
    await tl.render();
    if (!ok()) return;
    r.text?.remove();
    slots.get(n).append(div);
    r.text = div;
    r.tl = tl;
    highlight(n);
  }

  async function renderLinks(n, p, rot, r, ok) {
    const anns = await p.getAnnotations({ intent: 'display' });
    if (!ok()) return;
    const vp1 = p.getViewport({ scale: 1, rotation: rot });
    const layer = document.createElement('div');
    layer.className = 'linkLayer';
    for (const a of anns) {
      if (a.subtype !== 'Link' || (!a.url && !a.dest)) continue;
      const [x1, y1, x2, y2] = vp1.convertToViewportRectangle(a.rect);
      const link = document.createElement('a');
      link.href = a.url && /^(https?:|mailto:)/i.test(a.url) ? a.url : '#';
      link.title = a.url ? a.url : tx('pdfview_go_to_linked_page');
      Object.assign(link.style, {
        left: (Math.min(x1, x2) / vp1.width) * 100 + '%',
        top: (Math.min(y1, y2) / vp1.height) * 100 + '%',
        width: (Math.abs(x2 - x1) / vp1.width) * 100 + '%',
        height: (Math.abs(y2 - y1) / vp1.height) * 100 + '%'
      });
      link.addEventListener('click', async (e) => {
        e.preventDefault();
        if (a.url) openExternal(a.url);
        else {
          const target = await resolveDest(pdf, a.dest);
          if (target) goTo(target);
        }
      });
      layer.append(link);
    }
    if (layer.childElementCount) {
      slots.get(n).append(layer);
      r.links = layer;
    } else r.links = document.createElement('div');
  }

  function openExternal(u) {
    if (!/^(https?:|mailto:)/i.test(u)) return;
    if (confirm(tx('pdfview_this_link_in_the_document_leaves', { url: u }))) window.open(u, '_blank', 'noopener,noreferrer');
  }

  // ------------------------------------------------------------ find

  let findGen = 0;
  $effect(() => {
    const q = query;
    const ready = sizes.length > 0;
    untrack(() => ready && runFind(q));
  });

  async function runFind(q) {
    const gen = ++findGen;
    const ts = parseTerms(q);
    matches = [];
    pageHits = {};
    matchCount = 0;
    current = -1;
    for (const n of rendered.keys()) highlight(n);
    if (!ts.length || !pdf) return;
    searching = true;
    const all = [];
    const hits = {};
    const pages = await candidatePages(ts);
    if (gen !== findGen || !alive) return;
    for (let i = 0; i < pages.length; i++) {
      const n = pages[i];
      const t = await pageText(n);
      if (gen !== findGen || !alive) return;
      const ranges = findAll(t.joined, ts);
      if (ranges.length) hits[n] = ranges.length;
      for (const [start, end] of ranges) all.push({ page: n, start, end });
      if (i % 25 === 24 || i === pages.length - 1) {
        matches = [...all];
        matchCount = all.length;
        pageHits = { ...hits };
      }
    }
    searching = false;
    if (all.length) {
      const startAt = all.findIndex((m) => m.page >= (initialPage > 1 && current < 0 ? initialPage : page));
      showMatch(startAt >= 0 ? startAt : 0);
    }
    for (const n of rendered.keys()) highlight(n);
  }

  // candidatePages narrows the search with the server's extracted text, so
  // pdf.js only extracts (and range-loads) pages that can contain a hit.
  // Whitespace is ignored on both sides (the extractors space text
  // differently). Any doubt (no text, page count differs) means all pages.
  let serverPages = null; // {path, pages: string[] | null}
  const squash = (s) => s.normalize('NFKC').toLowerCase().replace(/\s+/g, '');
  async function candidatePages(ts) {
    const all = Array.from({ length: pdf.numPages }, (_, i) => i + 1);
    if (!textPath) return all;
    if (serverPages?.path !== textPath) {
      let pages = null;
      try {
        const { data } = await revalidate(textPath);
        const parts = (data.text ?? '').split('\f');
        if (parts.length === pdf.numPages + 1 && !parts[parts.length - 1].trim()) parts.pop();
        if (parts.length === pdf.numPages) pages = parts.map(squash);
      } catch {}
      serverPages = { path: textPath, pages };
    }
    const pages = serverPages.pages;
    if (!pages) return all;
    const sq = ts.map(squash).filter(Boolean);
    return all.filter((n) => sq.some((t) => pages[n - 1].includes(t)));
  }

  export function findNext() {
    if (matches.length) showMatch((current + 1) % matches.length);
  }
  export function findPrev() {
    if (matches.length) showMatch((current - 1 + matches.length) % matches.length);
  }

  function showMatch(i) {
    const prev = matches[current]?.page;
    current = i;
    const m = matches[i];
    if (prev && prev !== m.page) highlight(prev);
    scrollToCurrent = true;
    pinned = m.page;
    if (rendered.get(m.page)?.tl) highlight(m.page);
    else goTo(m.page);
  }

  function highlight(n) {
    const r = rendered.get(n);
    const t = texts.get(n);
    if (!r?.tl || !t) return;
    const divs = r.tl.textDivs;
    const own = matches.map((m, i) => ({ ...m, i })).filter((m) => m.page === n);
    const touched = new Set();
    // reset previously highlighted divs
    for (const d of r.marked ?? []) {
      const idx = divs.indexOf(d);
      if (idx >= 0) d.textContent = t.strs[idx];
    }
    const perDiv = new Map();
    for (const m of own) {
      for (let k = 0; k < t.strs.length; k++) {
        const s = t.starts[k], e = s + t.strs[k].length;
        if (e <= m.start || s >= m.end) continue;
        const list = perDiv.get(k) ?? [];
        list.push({ a: Math.max(0, m.start - s), b: Math.min(t.strs[k].length, m.end - s), cur: m.i === current });
        perDiv.set(k, list);
      }
    }
    let curEl = null;
    for (const [k, list] of perDiv) {
      const d = divs[k];
      if (!d) continue;
      const str = t.strs[k];
      d.textContent = '';
      let pos = 0;
      for (const { a, b, cur } of list) {
        if (a > pos) d.append(str.slice(pos, a));
        const mk = document.createElement('span');
        mk.className = cur ? 'hl current' : 'hl';
        mk.textContent = str.slice(a, b);
        d.append(mk);
        if (cur && !curEl) curEl = mk;
        pos = b;
      }
      if (pos < str.length) d.append(str.slice(pos));
      touched.add(d);
    }
    r.marked = touched;
    if (curEl && scrollToCurrent && scroller) {
      scrollToCurrent = false;
      curEl.scrollIntoView({ block: 'center', inline: 'nearest' });
    }
  }

  // ------------------------------------------------------------ thumbnails

  const thumbSlots = new Map();
  const thumbsDone = new Map();
  const thumbVisible = new Set();
  let tio = null;
  const THUMB_W = 112;

  function thumbSlot(el, n) {
    thumbSlots.set(n, el);
    tio?.observe(el);
    return {
      destroy() {
        tio?.unobserve(el);
        thumbSlots.delete(n);
        thumbVisible.delete(n);
        releaseThumb(n);
      }
    };
  }

  function releaseThumb(n) {
    const t = thumbsDone.get(n);
    if (!t) return;
    t.task?.cancel();
    if (t.canvas) {
      t.canvas.width = t.canvas.height = 0;
      t.canvas.remove();
    }
    thumbsDone.delete(n);
  }

  let thumbPending = 0;
  function scheduleThumbs() {
    if (thumbPending) return;
    thumbPending = requestAnimationFrame(async () => {
      thumbPending = 0;
      for (const n of [...thumbVisible].sort((a, b) => a - b)) await renderThumb(n);
    });
  }

  async function renderThumb(n) {
    if (!pdf || thumbsDone.has(n)) return;
    const t = {};
    thumbsDone.set(n, t);
    try {
      const p = await pdf.getPage(n);
      const rot = (p.rotate + rotation) % 360;
      const base = p.getViewport({ scale: 1, rotation: rot });
      const vp = p.getViewport({ scale: THUMB_W / base.width, rotation: rot });
      const canvas = document.createElement('canvas');
      const dpr = Math.min(2, window.devicePixelRatio || 1);
      canvas.width = Math.floor(vp.width * dpr);
      canvas.height = Math.floor(vp.height * dpr);
      t.task = p.render({ canvas, viewport: vp, transform: dpr !== 1 ? [dpr, 0, 0, dpr, 0, 0] : undefined });
      await t.task.promise;
      if (thumbsDone.get(n) !== t || !thumbSlots.has(n)) return;
      thumbSlots.get(n).querySelector('.paper').append(canvas);
      t.canvas = canvas;
    } catch (e) {
      if (e?.name !== 'RenderingCancelledException') thumbsDone.delete(n);
    }
  }

  // keep the active thumbnail in view
  $effect(() => {
    const n = page;
    if (!thumbs) return;
    untrack(() => thumbSlots.get(n)?.scrollIntoView({ block: 'nearest' }));
  });

  // ------------------------------------------------------------ setup

  function wheel(el) {
    const h = (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      zoom = Math.round(clamp(scale * Math.exp(-e.deltaY * 0.002)) * 100) / 100;
    };
    el.addEventListener('wheel', h, { passive: false });
    return { destroy: () => el.removeEventListener('wheel', h) };
  }

  onMount(() => {
    io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          const n = +e.target.dataset.n;
          if (e.isIntersecting) visible.add(n);
          else {
            visible.delete(n);
            release(n);
          }
        }
        schedule();
      },
      { root: scroller, rootMargin: '120% 0px' }
    );
    for (const el of slots.values()) io.observe(el);
    const ro = new ResizeObserver(() => {
      if (!scroller) return;
      cw = scroller.clientWidth;
      ch = scroller.clientHeight;
    });
    ro.observe(scroller);
    return () => {
      alive = false;
      io.disconnect();
      tio?.disconnect();
      ro.disconnect();
      cancelAnimationFrame(raf);
      cancelAnimationFrame(pending);
      cancelAnimationFrame(thumbPending);
    };
  });

  $effect(() => {
    if (!thumbs || !thumbBox) return;
    untrack(() => {
      tio = new IntersectionObserver(
        (entries) => {
          for (const e of entries) {
            const n = +e.target.dataset.n;
            if (e.isIntersecting) thumbVisible.add(n);
            else {
              thumbVisible.delete(n);
              releaseThumb(n);
            }
          }
          scheduleThumbs();
        },
        { root: thumbBox, rootMargin: '200px 0px' }
      );
      for (const el of thumbSlots.values()) tio.observe(el);
    });
    return () => {
      tio?.disconnect();
      tio = null;
    };
  });
</script>

<div class="pdfview {mode}" class:withthumbs={thumbs}>
  {#if thumbs}
    <nav class="thumbs" bind:this={thumbBox} aria-label={tx('docdetails_pages')}>
      {#each sizes as s, i (i)}
        <button class="thumb" class:active={page === i + 1} data-n={i + 1} use:thumbSlot={i + 1} onclick={() => goTo(i + 1)}
          aria-label={pageHits[i + 1] ? tx('pdfview_page_matches', { page: i + 1, count: pageHits[i + 1] }) : tx('pdfview_page', { page: i + 1 })} aria-current={page === i + 1 ? 'page' : undefined}>
          <span class="paper" style:aspect-ratio="{s.w} / {s.h}"></span>
          <span class="tn">{i + 1}{#if pageHits[i + 1]}<span class="hitmark" title={tx('pdfview_matches', { count: pageHits[i + 1] })}></span>{/if}</span>
        </button>
      {/each}
    </nav>
  {/if}

  <div class="scroller" bind:this={scroller} onscroll={onScroll} use:wheel tabindex="-1">
    {#if loading && !password}
      <p class="state">{tx('common_opening_document')}</p>
    {:else if password}
      <form class="state pw" onsubmit={submitPassword}>
        <strong>{tx('pdfview_this_pdf_is_password_protected')}</strong>
        {#if password.wrong}<span class="err">{tx('pdfview_that_password_is_not_correct')}</span>{/if}
        <input type="password" bind:value={pwValue} aria-label={tx('pdfview_pdf_password')} autocomplete="off" />
        <button class="primary">{tx('common_open')}</button>
      </form>
    {:else if error}
      <div class="state"><strong>{tx('pdfview_this_pdf_can_t_be_displayed')}</strong><span>{error}</span><span>{tx('pdfview_download_it_to_open_it_in')}</span></div>
    {/if}
    <div class="pages" style:padding="{PAD}px" style:gap="{GAP}px">
      {#each sizes as s, i (i)}
        <div class="pg" data-n={i + 1} use:slot={i + 1}
          style:width="{s.w * scale}px" style:height="{s.h * scale}px" style:--total-scale-factor={scale * CSS_UNITS}
          style:view-transition-name={transitionName && i + 1 === page ? transitionName : null}
          aria-label={tx('pdfview_page', { page: i + 1 })}></div>
      {/each}
    </div>
  </div>
</div>

<style>
  .pdfview { display: grid; grid-template-columns: 1fr; min-height: 0; height: 100%; background: var(--stage); color: var(--stage-ink); }
  .pdfview.withthumbs { grid-template-columns: 148px 1fr; }
  .pdfview.peek { background: var(--surface-2); color: var(--ink-2); }
  .scroller { position: relative; overflow: auto; min-height: 0; min-width: 0; outline: none; overscroll-behavior: contain; }
  .pages { display: flex; flex-direction: column; width: max-content; min-width: 100%; }
  .pg {
    position: relative;
    isolation: isolate;
    margin: 0 auto;
    flex: none;
    background: var(--paper);
    box-shadow: var(--paper-shadow);
    --scale-round-x: 1px;
    --scale-round-y: 1px;
  }
  .pg :global(canvas) { position: absolute; inset: 0; width: 100%; height: 100%; display: block; }
  .state { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; text-align: center; padding: 24px; margin: 0; max-width: none; z-index: 2; }
  .state.pw input { max-width: 260px; }
  .pdfview:not(.peek) .state strong { color: var(--stage-ink); }

  .thumbs { overflow: auto; padding: 14px 18px; display: flex; flex-direction: column; gap: 14px; background: var(--stage-2); border-right: 1px solid rgb(255 255 255 / 0.06); }
  .thumb { display: flex; flex-direction: column; align-items: center; gap: 4px; padding: 0; border: 0; background: none; min-height: 0; color: var(--stage-ink-2); }
  .thumb:hover:not(:disabled) { background: none; color: var(--stage-ink); }
  .thumb .paper { position: relative; display: block; width: 112px; background: var(--paper); box-shadow: 0 1px 3px rgb(0 0 0 / 0.4); outline: 2px solid transparent; outline-offset: 3px; }
  .thumb .paper :global(canvas) { position: absolute; inset: 0; width: 100%; height: 100%; }
  .thumb.active .paper { outline-color: var(--accent); }
  .thumb.active { color: var(--stage-ink); font-weight: 600; }
  .tn { font-size: var(--t-sm); display: inline-flex; align-items: center; gap: 5px; }
  .hitmark { width: 8px; height: 8px; border-radius: 50%; background: var(--highlight); box-shadow: 0 0 0 1px rgb(0 0 0 / 0.25); }

  /* pdf.js text layer (subset of pdf_viewer.css) */
  .pg :global(.textLayer) {
    position: absolute; text-align: initial; inset: 0; overflow: clip; opacity: 1; line-height: 1;
    text-size-adjust: none; forced-color-adjust: none; transform-origin: 0 0; caret-color: CanvasText; z-index: 1;
    /* the text layer is its own stacking context: blend it as a whole with the
       rendered page so highlights act like a highlighter (text stays visible) */
    mix-blend-mode: multiply;
    --min-font-size: 1;
    --text-scale-factor: calc(var(--total-scale-factor) * var(--min-font-size));
    --min-font-size-inv: calc(1 / var(--min-font-size));
  }
  .pg :global(.textLayer :is(span, br)) { color: transparent; position: absolute; white-space: pre; cursor: text; transform-origin: 0% 0%; user-select: text; }
  .pg :global(.textLayer > :not(.markedContent)), .pg :global(.textLayer .markedContent span:not(.markedContent)) {
    z-index: 1; --font-height: 0; font-size: calc(var(--text-scale-factor) * var(--font-height));
    --scale-x: 1; --rotate: 0deg; transform: rotate(var(--rotate)) scaleX(var(--scale-x)) scale(var(--min-font-size-inv));
  }
  .pg :global(.textLayer .markedContent) { display: contents; }
  .pg :global(.textLayer span[role='img']) { user-select: none; cursor: default; }
  .pg :global(.textLayer ::selection) { background: rgb(31 78 121 / 0.3); }
  .pg :global(.textLayer .hl) { position: relative; margin: -1px -2px; padding: 1px 2px; border-radius: 2px; background: var(--highlight); transform: none !important; font-size: inherit !important; }
  .pg :global(.textLayer .hl.current) { background: var(--highlight-strong); box-shadow: 0 0 0 2px #9a7b00; }
  .pg :global(.linkLayer) { position: absolute; inset: 0; z-index: 2; pointer-events: none; }
  .pg :global(.linkLayer a) { position: absolute; pointer-events: auto; border-radius: 2px; }
  .pg :global(.linkLayer a:hover), .pg :global(.linkLayer a:focus-visible) { background: rgb(31 78 121 / 0.12); outline: 1px solid rgb(31 78 121 / 0.5); }

  @media (max-width: 760px) {
    .pdfview.withthumbs { grid-template-columns: 1fr; }
    .thumbs { display: none; }
  }
</style>
