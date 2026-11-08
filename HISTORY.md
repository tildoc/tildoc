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
