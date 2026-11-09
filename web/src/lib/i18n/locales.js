// The translations that exist, loaded on demand (Vite only: see index.js).
const files = import.meta.glob(['./locales/*.json', '!./locales/en-us.json', '!./locales/template.json']);

export const loaders = Object.fromEntries(Object.entries(files).map(([path, load]) => [path.match(/([^/]+)\.json$/)[1], load]));
export const availableLocales = Object.keys(loaders).sort();
