# HISTORY

Security-relevant refactorings.

## 2026-11-08: inline PDFs are sandboxed

Before: `?inline=1` responses for PDFs were served without `Content-Security-Policy: sandbox`
so the browser's built-in viewer could render them inside the UI (an iframe on the UI origin).
Every other inline type was already sandboxed.

Now: the UI renders PDFs itself with pdf.js (PDF scripting off, no XFA, no JS eval allowed by
the CSP) from bytes fetched over the API, so every content response carries the sandbox CSP. The
browser's viewer remains available as an opt-in preference (`prefs.pdf_viewer = "native"`);
only for users with that preference does `?inline=1&viewer=native` drop the sandbox for PDFs. The
server checks the preference itself, so a crafted link cannot opt other users out.

## 2026-11-08: capability based permissions, space sharing

Before: access to a space came only from personal ownership or membership in
the owning team; checks compared numeric levels in several places.

Now: `space_role()` in the database computes the strongest role (ownership,
owning team, direct share, team share capped by the member's team role) and
`internal/core/permissions.go` maps roles to capabilities; every check goes
through `requireCap`. Shares never grant ownership and nobody can grant more
than their own role. Access keys still restrict scope and writing.

## 2026-11-08: blob garbage collection vs. exports

Exports and backups stream blob files after reading a snapshot. A shared
advisory lock now keeps garbage collection from deleting files while they
are being streamed.

## 2026-11-09: deleted content is deleted at once; GC race with uploads

Before: purging a document (emptying the trash, deleting from it, retention)
removed only the document; its content (blob file, extracted text, processing
state, OCR variants) stayed until garbage collection, which skipped blobs
used within the last hour. Uploading the same bytes in that hour silently
reused the old text and OCR results. The grace period was also the only
protection for uploads: a file written to the store before its database row
existed could be deleted by a GC run in between, leaving a row without file.

Now: every writer records a blob under the shared blob lock, in the same
transaction that writes the file into place (uploads, new versions, OCR
variants; bundle imports hold the lock for the whole import). GC takes the
lock exclusively, so no grace period is needed and `blobs.last_used_at` is
gone. Purging deletes the content of the purged documents immediately unless
another document still uses it; if an export holds the lock, a GC job
follows shortly.
