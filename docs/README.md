# Eumetria documentation

The source of https://eumetria-labs.github.io/eumetria/, built with
[Starlight](https://starlight.astro.build). Pages are Markdown in
`src/content/docs`, the sidebar is in `astro.config.mjs`, screenshots are in
`src/assets/screens` (taken by `scripts/docs-shots.js`).

```sh
npm install
npm run dev     # http://localhost:4321/eumetria/
npm run build   # into dist/
```

`.github/workflows/docs.yml` publishes it on every push to `main` that touches `docs/`.
