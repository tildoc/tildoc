AGENTS.md

# OVERALL GUIDELINES - maintained by Owner
    
## Engineering principles 
        
- **Future-ready by default.** Every change you implement must be modular,
  cleanly separated, and secure. Structure code so that future changes are easy
  to add without rewriting what already exists. Prefer clear interfaces,
  minimal coupling, and sensible defaults.
- **Security is not optional.** Validate inputs, handle errors explicitly, and
  never expose secrets or credentials in code, that is subject to be commited.
  If you find in existing project a security issue - bring this up with a WARNING!
- **Privacy is not optional.** Solutions we design and work on are subject
  to be published. Do not include there private data. If such is needed,
  always create a template configuration file, fill the private data into a
  gitignored file.
  
## Planning — mandatory before any programming task
            
When the user requests a programming task, **do not start coding immediately.**
        
1. Read the existing `PLAN.md` in the project root (if it exists).
2. Create or update `PLAN.md` to reflect the requested task. Include:
   - **Goal:** what the user asked for, in your own words.
   - **Steps:** a numbered breakdown of what you will implement.
   - **Open questions:** anything you need clarified before starting.
3. Present the plan to the user and wait for confirmation before writing code.
4. As you complete steps, update `PLAN.md` to mark them done
   (e.g. `- [x] step`).
5. Keep `PLAN.md` up to date when requirements change mid-conversation.

## Coding principles

- if you have to decide for a programming language at the beginning of a project
  tell the user about the decision for one language and give him possiblities to weigthen
  the use of a language by himself.
- Use if possible always the possibilities of a programming language it ships 
  with instead of using a library
- If you have use a library, use as less as possible, focus on broadly used libraries
  that cover a lot of requirements at once.
- If you refactor code, do not leave a History Text at the place, where code has been removed.
  If the refactoring was due to some security issue, add the history text to a HISTORY.md

## Interfacing principles

- The user you are communicating with is an experienced Developer.
  Reduce your output to concrete formulations. If requested to explain something
  you can focus on technical in-depth explanations
- When giving command instructions, mark them with code blocks 
  ```
  code
  ``` 
  To make them clearly different from the text instructions

## Testing
    
- We like solutions, that can be tested. Especially Integration Testing.
  However if the task is complex to simulate ask the user about the plan to 
  implement for testing specifically.
        
## Backwards compatibility
        
- **We are in Developer Mode.** Do not preserve backwards compatibility unless
  the user explicitly asks for it.
- However, if a change you are about to make **breaks existing functionality**,
  warn the user clearly before proceeding. Explain what will break and why.
        
## Diagrams         
                    
- When visualising architecture, flows, or relationships, use **Mermaid**
  diagrams (in Markdown fenced code blocks with `mermaid` as language).
- Place diagrams in `PLAN.md`, `README.md`, or inline in your response —
  wherever they add the most clarity.

## Storage Location

- If you write helper scripts, never store something in /tmp/ - sessions might be continued after reboot and
  /tmp/* will be gone. Use /tmp/* only in case it is really temporary work - e.g. in the scripts you produce.

# PROJECT SPECIFIC GUIDELINES - maintained by LLM




- **Licence:** dual, GNU AGPL-3.0-only (`LICENSE`) and commercial, held by
  Matthias Robert Wiora; contributions need `CLA.md`. Everything linked or
  bundled must stay permissive (BSD, MIT, Apache-2.0, ISC; fonts OFL), or the
  commercial licence breaks: no GPL/AGPL libraries. Separate containers and
  programs are fine.
- **Priorities: Security first, a clean Data Model second.** The schema is the
  foundation everything else builds on; keep it normalized, explicit and
  consistent (constraints in the database, not only in Go; no redundant or
  ambiguous columns; names follow the Terminology below). While in
  Development Mode there are no deployments to migrate: breaking schema
  changes are fine and preferred over workarounds. Fold changes into
  `internal/db/migrations/0001_init.sql` instead of stacking migrations
  until the first real release (dev databases are recreated, see
  `test/ui/run.sh`). Still warn the user before a breaking change.
- **Terminology (binding, see Glossary in PLAN.md):** Space (formerly "Silo"),
  Team (formerly "Organization"), Folder, Document, Link (a document's
  placement: folder + name, 1..N per document), Version, Blob (internal,
  SHA-256 content), Import. Use these terms consistently in code, schema,
  API and docs.
- **Architecture rules:** only `internal/core` accesses PostgreSQL and the blob
  store. Protocol adapters (`api`, `s3`, `dav`, `sftpd`) go through `core`
  (and `vfs` for path-based access). New document types = new case in
  `internal/worker/processor.go`; derived data is keyed by blob SHA-256.
- **Testing:** `make test` (Postgres via Docker), `make test-docker` (incl. OCR),
  `make e2e` (aws-cli/rclone), `test/e2e/compose.sh` (full stack),
  `test/ui/run.sh` (Chromium). Run the relevant ones after every larger change.
- **Dev data** lives in `.data/` (gitignored; dot-dir so `go ./...` ignores it).
- **UI stack:** SvelteKit 3 (config in `web/vite.config.js`, `#lib/` imports
  via package.json `imports`), Svelte 5 runes, plain CSS, no UI libraries.
  Runtime dependencies: `pdfjs-dist` and CodeMirror 6 (`@codemirror/*`,
  `@lezer/highlight`), both lazy loaded (`lib/viewer/pdfjs.js`,
  `lib/editor/cm.js`). Design tokens live in
  `web/src/app.css` ("light table": paper only for documents, highlighter
  yellow for search hits, Atkinson Hyperlegible Next as the only typeface;
  subpages use the full width; form hints go below the input). Viewer code:
  `web/src/lib/components/viewer/` (DocView picks PdfView / ImageView /
  TextView / native iframe). `web/static/vendor/` is generated, never edit it.
- **Permissions** are only computed in `internal/core/permissions.go`
  (`requireCap`, `requireDocCap`, `Principal.Can`; SQL `space_permissions()`,
  `document_permissions()`, `instance_permissions()`). The catalogue is the
  `permissions` table plus `capNames`/`instNames`; roles and grants are data.
  Never compare role names. Permissions only add up (no deny). Instance
  permissions never give access to documents. Reading a file as a file needs
  `download`; only the browser preview works with `view`.
- **Sharing:** `grants` (space, document, instance), `share_links` (tokens only
  hashed and only in the URL fragment); a link never exceeds its creator's
  current rights. Everything anonymous goes through `ShareAccess` in
  `internal/core/shares.go`, the only caller of `SystemPrincipal` for it.
- **Logins:** `identities` + `Authenticator` (`internal/core/identity.go`);
  match accounts by (provider, subject), never by e-mail address.
  OIDC protocol in `internal/oidc`; a first external login creates a `pending`
  user, who has no access until approved.
- **Export format compatibility:** during development only the current format
  version is imported. From the moment of productivation (the owner defines it)
  importing older exports is mandatory: add version upgraders, never break it.
  `TestBackupCoversEveryColumn` fails for any new column until it is handled.
- **Audit:** security-relevant changes write `s.audit(...)` in the transaction
  that makes them. `audit_events` is append-only.
- **Encryption:** instance-level only (`masterKeyVersion` prefix on secrets); no
  per-space keys, content-addressed blobs are shared across spaces.
- **Bundles** (`internal/core/bundle.go`, `import.go`, `backup.go`): format in
  `docs/bundle-format.md`; bump `BundleVersion` for incompatible changes and
  keep imports going through the normal ingest rules.
- **Window key handlers** must ignore `e.defaultPrevented` events and keys
  typed into inputs or `contenteditable` (the text editor); dialogs and inputs
  that handle a key call `preventDefault()`.
- **Text editor:** one editor per document via `edit_leases`
  (`internal/core/editing.go`); saving needs the lease of that window
  (`client_id`). Never re-key or re-mount the editor on new versions: it
  holds unsaved text.
- **Office documents:** Collabora or ONLYOFFICE, both only via WOPI
  (`internal/wopi`, `internal/api/wopi.go`, `internal/core/office.go`, setup in
  `docs/office-servers.md`), always on their own origin (`config.checkOffice`).
  Only the end of a session creates a version; saves go to the working copy.
  ONLYOFFICE is the default. Empty files open with `editnew`; new files never
  dedupe (`CreateEmptyFile`).
  Tests use `internal/wopi/wopitest` instead of real Collabora. Never re-mount
  `OfficeView` on new versions.
- **Version:** `internal/version.Version`, from `git describe` at link time;
  releases are git tags (`vX.Y.Z`). Never expose it without login.
- **Live updates:** schema triggers `pg_notify('tildoc_events', …)` (see end
  of `0001_init.sql`); `core.EventHub` filters per subscriber; `/api/events`
  is SSE. New tables whose changes the UI shows get a trigger, never a new
  polling loop. UI views use `watchLive` (`events.svelte.js`) and keep
  `watchBusy` (`poll.svelte.js`) only as fallback.
- **API responses:** `writeJSON` adds ETag/gzip for GET; JSON stays
  `no-store`. Revisited views load with `cachedData` + `revalidate`
  (`api.js`). Navigate to the viewer with `openView` (`nav.js`).
- **History graph:** layout is the pure function `web/src/lib/history.js`
  (unit tests: `cd web && npm test`); restores are stored
  (`document_versions.restored_from`), continuations are derived from
  content hashes, never stored.
- **Large lists:** `FileTable` renders a window of rows above 200 rows;
  rows must keep one fixed height.
