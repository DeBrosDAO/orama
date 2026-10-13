// The inline script the prerenderer puts after each page's HTML.
//
// The static host answers a path it has no file for (a 404, a deep explorer link such as
// /explorer/tx/<hash>) with another page's HTML. Served one page's HTML for a path it wasn't rendered
// for, the script clears it before first paint instead of flashing the wrong page (the app then renders
// the right page from scratch), and says in the document's own <meta name="robots"> that what the
// crawler is looking at is not an indexable copy of the page that HTML was made for: it is kept out of
// search until the app has written the right head for the address. (nginx adds X-Robots-Tag: noindex
// to every page under /explorer/ as well; this covers a crawler that renders scripts and reads the tag.)
//
// It inlines normalizePath from src/content/routes.ts, the same function src/main.tsx uses.

/** @param {(p: string) => string} normalizePath @param {string} robotsNoindex the content of the noindex robots directive */
export const mismatchGuard = (normalizePath, robotsNoindex) =>
  `<script>(function(){var n=${normalizePath.toString()};` +
  'var r=document.getElementById("root");' +
  'if(r.getAttribute("data-prerendered")!==n(location.pathname)){r.replaceChildren();' +
  'var m=document.querySelector(\'meta[name="robots"]\');' +
  `if(m)m.setAttribute("content",${JSON.stringify(robotsNoindex)})}})()</script>`;
