# Klaro browser assets

These are the unmodified prebuilt browser files from Klaro 0.7.22, licensed
under BSD-3-Clause. oCMS uses this existing version's JavaScript API and CSS;
it does not build Klaro's source or run Klaro's development tools.

The published npm package declares its lint/build tools as dependencies,
including a transitive ESLint/minimatch chain. Keeping only the browser
distribution avoids installing that unused chain. This is not a minimatch
version override or an advisory suppression.

## Provenance

- Package: [klaro 0.7.22](https://www.npmjs.com/package/klaro/v/0.7.22).
- Source archive: <https://registry.npmjs.org/klaro/-/klaro-0.7.22.tgz>.
- Archive integrity, checked before copying:
  `sha512-NkaFxS4S8160/zH1UQzCSYIIsAtwIDHIkJ7xofCrgRqk14OSY+OJuwCjYyW+q5fmIeF4RvsuBH6brPe2BS9Z0A==`.
- Upstream license: `LICENSE`, retained here as `LICENSE.txt`.

| Upstream file | Local file | SHA-256 |
|---|---|---|
| `dist/klaro-no-css.js` | `klaro-no-css.js` | `d698e56eb75013436d80c30c82d980fd221baa1baf72f1b60a5c5dc64f1c6445` |
| `dist/klaro.css` | `klaro.css` | `9068e2bf1936996d6900427159c2b04acc22aa37dac69d841ddb3492b6cf411e` |
| `LICENSE` | `LICENSE.txt` | `77e99125636bef70b534cc86b6f890bf8b33b453d4a9402f3ff4b2cf26e7fa82` |

`npm run copy-deps` copies these files to the existing
`/static/dist/js/klaro.min.js` and `/static/dist/css/klaro.css` paths and
includes the license at `/static/dist/licenses/klaro-LICENSE.txt`.
The same command runs during Docker builds. The generated assets and license
are embedded in the Go binary by `web.Static`.

## Updates

Klaro is now a vendored dependency; npm audit and npm update do not track
these files. Check upstream releases/advisories explicitly when updating.
Verify the new archive integrity, review its browser/runtime changes and
license, then replace all three files and update the hashes above. Build
assets and run the privacy module and dynamic Klaro SRI tests. Preserve the
output URLs, script integrity/cache-busting and consent behavior, or update
their consumers and tests together. Never substitute unverified CDN bytes
or restore the unused npm tooling merely to obtain the prebuilt files.
