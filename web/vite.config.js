import { sveltekit } from '@sveltejs/kit/vite';
import adapter from '@sveltejs/adapter-static';
import { defineConfig } from 'vite';

// dev: proxy API + WebDAV to a local `tildoc serve` (TILDOC_HTTP_ADDR)
const backend = process.env.TILDOC_BACKEND || 'http://127.0.0.1:8080';

export default defineConfig({
  plugins: [
    sveltekit({
      adapter: adapter({ fallback: 'index.html' }),
      // strict CSP; the inline bootstrap script is allowed via its hash
      csp: {
        mode: 'hash',
        directives: {
          'default-src': ['self'],
          'script-src': ['self'],
          'style-src': ['self', 'unsafe-inline'],
          'img-src': ['self', 'data:', 'blob:'],
          'frame-src': ['self'],
          'connect-src': ['self'],
          'object-src': ['none'],
          'base-uri': ['self'],
          'form-action': ['self']
        }
      }
    })
  ],
  server: {
    proxy: { '/api': backend, '/dav': backend }
  }
});
