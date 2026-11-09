<script>
  let { name, size = 18 } = $props();
  const paths = {
    folder: 'M3 6.5A1.5 1.5 0 0 1 4.5 5h4.2l1.8 2h9A1.5 1.5 0 0 1 21 8.5v9A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5z',
    file: 'M6 3h8l4 4v14H6zM14 3v4h4',
    pdf: 'M6 3h8l4 4v14H6zM14 3v4h4M8.5 16h7M8.5 13h7',
    text: 'M6 3h8l4 4v14H6zM14 3v4h4M8.5 11h7M8.5 14h7M8.5 17h4',
    image: 'M4 5h16v14H4zM4 16l5-5 4 4 3-3 4 4M15 9.5a1 1 0 1 0 .01 0',
    upload: 'M12 16V4M7 9l5-5 5 5M4 20h16',
    download: 'M12 4v12M7 11l5 5 5-5M4 20h16',
    search: 'M10.5 17a6.5 6.5 0 1 1 0-13 6.5 6.5 0 0 1 0 13zM15.5 15.5 20 20',
    trash: 'M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13',
    settings: 'M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zM19.4 13a7.6 7.6 0 0 0 0-2l2-1.5-2-3.4-2.4.9a7.4 7.4 0 0 0-1.7-1L15 3.5h-4l-.4 2.5a7.4 7.4 0 0 0-1.7 1l-2.4-.9-2 3.4 2 1.5a7.6 7.6 0 0 0 0 2l-2 1.5 2 3.4 2.4-.9a7.4 7.4 0 0 0 1.7 1l.4 2.5h4l.4-2.5a7.4 7.4 0 0 0 1.7-1l2.4.9 2-3.4z',
    users: 'M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2.5 20a6.5 6.5 0 0 1 13 0M16 4.3a3.5 3.5 0 0 1 0 6.4M18 14.5a6.5 6.5 0 0 1 3.5 5.5',
    shield: 'M12 3l7 3v6c0 4.5-3 7.5-7 9-4-1.5-7-4.5-7-9V6z',
    space: 'M4 5h16v4H4zM4 11h16v8H4zM8 15h8',
    chevron: 'M9 6l6 6-6 6',
    plus: 'M12 5v14M5 12h14',
    move: 'M5 12h14M15 8l4 4-4 4M3 5v14',
    copy: 'M8 8h11v11H8zM5 16V5h11',
    link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
    edit: 'M4 20h4L19 9l-4-4L4 16zM13.5 6.5l4 4',
    restore: 'M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4',
    close: 'M6 6l12 12M18 6 6 18',
    columns: 'M4 5h16v14H4zM10 5v14M15 5v14',
    menu: 'M4 7h16M4 12h16M4 17h16',
    logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10',
    refresh: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v4h-4',
    key: 'M15 9a4 4 0 1 1-3.5 6l-1.5 1.5H8V18H6v2H3v-3l6.5-6.5A4 4 0 0 1 15 9zM16 8h.01',
    up: 'M12 19V5M6 11l6-6 6 6',
    down: 'M12 5v14M6 13l6 6 6-6',
    history: 'M12 7v5l3 2M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4',
    info: 'M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v6M12 7.5h.01',
    home: 'M4 11l8-7 8 7M6 9.5V20h12V9.5',
    left: 'M15 6l-6 6 6 6',
    right: 'M9 6l6 6-6 6',
    'zoom-in': 'M10.5 17a6.5 6.5 0 1 1 0-13 6.5 6.5 0 0 1 0 13zM15.5 15.5 20 20M8 10.5h5M10.5 8v5',
    'zoom-out': 'M10.5 17a6.5 6.5 0 1 1 0-13 6.5 6.5 0 0 1 0 13zM15.5 15.5 20 20M8 10.5h5',
    pages: 'M4 4h6v7H4zM4 14h6v6H4zM14 4h6v16h-6z',
    panel: 'M4 5h16v14H4zM15 5v14',
    open: 'M14 4h6v6M20 4l-8 8M10 5H5v14h14v-5',
    rotate: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v4h-4',
    more: 'M5 12h.01M12 12h.01M19 12h.01',
    check: 'M5 12.5l4.5 4.5L19 7.5',
    lock: 'M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 017 0v3',
    moon: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z',
    tag: 'M3.5 12.5V4h8.5l8.5 8.5-8.5 8.5zM8 8.5h.01',
    collection: 'M5 8h14v11H5zM7 5h10M9 2.5h6'
  };
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"
  stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={paths[name] ?? paths.file} />
</svg>
