// REST client for the tildoc API. Cookie session + CSRF header.
import { goto } from '$app/navigation';

export class ApiError extends Error {
  constructor(status, message, code) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn) {
  onUnauthorized = fn;
}

export async function api(method, path, body, opts = {}) {
  const headers = { 'X-Tildoc-CSRF': '1' };
  let payload;
  if (body instanceof Blob || body instanceof ArrayBuffer) {
    payload = body;
    headers['Content-Type'] = 'application/octet-stream';
  } else if (body !== undefined) {
    payload = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  const res = await fetch('/api' + path, { method, headers, body: payload, credentials: 'same-origin' });
  if (res.status === 401 && !opts.noRedirect) {
    onUnauthorized();
    goto('/login');
  }
  if (!res.ok) {
    let err = {};
    try {
      err = await res.json();
    } catch {}
    throw new ApiError(res.status, err.error || res.statusText, err.code);
  }
  if (res.status === 204) return null;
  const ct = res.headers.get('content-type') || '';
  return ct.includes('json') ? res.json() : res.text();
}

export const get = (p, o) => api('GET', p, undefined, o);
export const post = (p, b) => api('POST', p, b ?? {});
export const patch = (p, b) => api('PATCH', p, b);
export const put = (p, b) => api('PUT', p, b);
export const del = (p) => api('DELETE', p);

// uploadFile streams one file with progress (XHR) into folderId at relPath.
export function uploadFile(folderId, relPath, file, importId, onProgress) {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const q = new URLSearchParams({ path: relPath });
    if (importId) q.set('import', importId);
    xhr.open('PUT', `/api/folders/${folderId}/files?${q}`);
    xhr.setRequestHeader('X-Tildoc-CSRF', '1');
    xhr.setRequestHeader('Content-Type', 'application/octet-stream');
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress?.(e.loaded / e.total);
    xhr.onload = () => {
      let body = {};
      try {
        body = JSON.parse(xhr.responseText);
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) resolve(body);
      else reject(new ApiError(xhr.status, body.error || xhr.statusText, body.code));
    };
    xhr.onerror = () => reject(new ApiError(0, 'network error'));
    xhr.send(file);
  });
}
