# RateGate playground (live demo source)

This is the source for the interactive playground at
<https://abolfazlrwm.github.io/rategate/> — a single self-contained HTML page
that runs faithful JavaScript ports of the server's rate-limiting algorithms
(token bucket, sliding window, adaptive AIMD) entirely in the browser, against
the same decision logic the Go server uses.

## Build

```sh
python3 playground/build.py docs/index.html
```

`build.py` concatenates `src/` (numbered `00-core.js` … `50-main.js` plus
`algorithms.js` and `appcore.js`), inlines the subset fonts and stylesheet,
computes the Content-Security-Policy hashes, and writes one HTML file. It
exits non-zero on a leftover `{{PLACEHOLDER}}`, a `</script` inside the
script, a glyph the subset fonts cannot draw, or a DOM id the script looks up
but the markup lacks.

## Verify

```sh
python3 -m http.server 8765 --dir docs &       # serve the built page
bash playground/verify.sh   http://127.0.0.1:8765/ /tmp/render/rg   # 4 widths + interactions
bash playground/edge_probe.sh http://127.0.0.1:8765/                # limit=0 / refill=0 edges
```

## Tests

```sh
node --test playground/test/algorithms.test.cjs playground/test/appcore.test.cjs
```

57 unit tests replay the Go tests in `internal/algorithms/*_test.go` against
the JavaScript ports and add randomized invariant checks. They run in CI via
`.github/workflows/playground.yml`.
