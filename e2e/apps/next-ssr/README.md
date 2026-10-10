# ref-next-ssr

The Next.js SSR reference app that `e2e/features/reference-apps` deploys with
`orama deploy nextjs --ssr`.

`next` is pinned to 14.2.35, the latest 14.2.x release (14.2.33 and later
carry the 14.2 line's security fixes). JSON has no comments, so the note on
the lockfile lives here:

- This app has no `package-lock.json` yet: it could not be generated in the
  offline session that pinned the version. The first run that has registry
  access must run `npm install --package-lock-only` in this directory and
  commit the resulting `package-lock.json`, so every later install resolves
  the same dependency tree (a bare `npm install` resolves transitive
  dependencies afresh on every run).
- When bumping `next`, regenerate the lockfile the same way in the same change.
