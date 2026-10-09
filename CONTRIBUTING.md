# Contributing

Bug reports, ideas and pull requests are welcome.

## Before you start

- Open an issue first for anything bigger than a small fix, so we can agree
  on the approach.
- Planned work lives in the maintainer's Jira, not in the repo. To propose
  something, open an issue.

## Develop

Go (the version in `go.mod`) is all you need.

```sh
make          # build ./laneway
make test     # go test ./...
go vet ./...
gofmt -l .    # must print nothing
```

Tests never talk to a real Jira: use `httptest` servers for the client and
the model harness in `internal/ui/harness_test.go` for the UI. The docs'
screenshots are shot from `laneway -demo` by `scripts/screenshots`.

`internal/contract` holds the demo's answers against a recording of a real
Jira's (key paths and types, no values). Re-record, reads only:
`go test ./internal/contract -run TestRecord -record corne-team -project LAN,LWC`.
LWC is a company-managed fixture project; keep its data varied.

`e2e/` drives the built app on the demo. The TUI runs in tmux, inside
`go test ./...` (skipped without tmux). The web runs in Playwright:
`cd e2e/web && npm ci && npx playwright test` (`CHROMIUM=/path` uses an
installed browser). Both fail on a request the demo can't answer
(`LANEWAY_DEMO_UNHANDLED`); serve it in `internal/demo`, shaped as the
contract recording has it. `LANEWAY_DEMO_FLAVOUR=laneway` (in Playwright
`test.use({ flavour: 'laneway' })`) makes the demo laneway-server: serve
laneway-server's own endpoints there, never in the plain demo. Each web
test's worst INP and CLS (web-vitals) go to `test-results/vitals.json` and
the CI job summary. `perf.spec.mjs`
holds INP to budgets on a big board (50ms moving, 100ms the rest), in CI
one test at a time: `INP_BUDGETS=1 npx playwright test tests/perf.spec.mjs
--workers=1` (`CPU_SLOWDOWN=4` for a slower machine).

## Pull requests

- One change per PR, with a test that fails without it.
- Match the surrounding code: comment density, naming, idiom.
- Keep the diff small; no drive-by reformatting.
- CI (build, vet, test, gofmt, govulncheck) must be green.

By contributing you agree your work is released under the [MIT license](LICENSE).
