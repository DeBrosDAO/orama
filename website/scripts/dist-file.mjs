import { existsSync, statSync } from "node:fs";
import { extname, join, resolve, sep } from "node:path";

/**
 * The file under `dist` that a site URL path is served from, the way the
 * nginx config serves it (`try_files $uri $uri/index.html`), or null when
 * there is none. Never resolves outside `dist`.
 */
export function distFileFor(dist, pathname) {
  let clean;
  try {
    clean = decodeURIComponent(pathname);
  } catch {
    return null;
  }
  const root = resolve(dist);
  const rel = extname(clean) ? clean : join(clean, "index.html");
  const file = resolve(root, `.${rel}`);
  if (!file.startsWith(root + sep)) return null;
  return existsSync(file) && statSync(file).isFile() ? file : null;
}
