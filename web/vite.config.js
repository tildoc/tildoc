import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import { defineConfig } from 'vite';

// dev: proxy API + WebDAV to a local `tildoc serve` (TILDOC_HTTP_ADDR)
const backend = process.env.TILDOC_BACKEND || 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [
    sveltekit({
      adapter: adapter({ fallback: 'index.html', precompress: true }),
      // strict CSP; the inline bootstrap script is allowed via its hash.
      // pdf.js: module worker from 'self', WASM image decoders need 'wasm-unsafe-eval' (no JS eval).
      csp: {
        mode: 'hash',
        directives: {
          'default-src': ['self'],
          'script-src': ['self', 'wasm-unsafe-eval'],
          'worker-src': ['self'],
          'font-src': ['self'],
          'style-src': ['self', 'unsafe-inline'],
          'img-src': ['self', 'data:', 'blob:'],
          // the build cannot know the Collabora origin: the servers narrow
          // these two to it with a response header (both policies apply)
          'frame-src': ['self', 'https:', 'http:'],
          'connect-src': ['self'],
          'object-src': ['none'],
          'base-uri': ['self'],
          'form-action': ['self', 'https:', 'http:']
        }
      }
    })
  ],
  server: {
    proxy: { '/api': backend, '/dav': backend }
  }
});
