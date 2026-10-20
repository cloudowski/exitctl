# Vendored assets

| File | Source | SHA-256 |
| ---- | ------ | ------- |
| `cytoscape.min.js` | Cytoscape.js 3.34.3 (MIT), https://cdn.jsdelivr.net/npm/cytoscape@3.34.3/dist/cytoscape.min.js | `5f3b5b529546d5af1fc5628590af033b74511a5b6f789f5f4682845863228b91` |
| `LICENSE.cytoscape` | https://cdn.jsdelivr.net/npm/cytoscape@3.34.3/LICENSE | `eb319c6e6f233607f71e8e2f450391751883cfc0eeb3ca7ef574c13d1d9c2203` |

Downloaded once at development time (2026-10-06). The files are embedded with `go:embed`
(`embed.go`) and inlined into every HTML report; reports make no network requests.

To upgrade: download the new `dist/cytoscape.min.js` and `LICENSE`, update this table with
`shasum -a 256`, and regenerate the HTML golden files (`go test ./internal/render/html/ -update`).

`report.css` and `report.js` are exitctl's own files.
