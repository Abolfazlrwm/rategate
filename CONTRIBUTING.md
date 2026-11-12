# Contributing to RateGate

First off, thanks for taking the time to contribute. The following is a set
of guidelines for contributing to RateGate. These are mostly guidelines, not
rules. Use your best judgment, and feel free to propose changes to this
document in a pull request.

## Code of Conduct

Be kind. Be technical. Personal attacks, harassment and discrimination are
not tolerated and will result in an immediate ban.

## How to Contribute

1. Fork the repository and create your branch from `main`.
2. If you add or change public API, update `docs/api-reference.md` and add
   a CHANGELOG entry under `[Unreleased]`.
3. Make sure the full gate passes locally:

   ```bash
   make check   # fmt + vet + test -race + lint
   ```

4. Open a pull request using the PR template. Reference any related issues.
5. For non-trivial changes, add a benchmark delta using `make bench` and
   paste the before/after into the PR description.

## Non-Negotiable Standards

The following are enforced in CI and a PR cannot merge without them:

- **100% test coverage** on `internal/algorithms` and `internal/store`.
  Other packages target >= 80%.
- **Race detector green**: `go test -race ./...` must pass.
- **golangci-lint green** with the strict config in `.golangci.yml`.
  Enabled linters include `errcheck`, `gosec`, `revive`, `gocyclo`
  (max complexity 15), `gofmt`, `goimports`, `unused`, `staticcheck`.
- **No unrecovered panics** in request paths. Use the recovery middleware.
- **Every exported identifier has a doc comment** starting with its name.
- **CI green** before merge. The `bench` workflow posts a performance diff
  on every PR; a regression > 10% on any benchmark blocks merge pending
  review.

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add sliding-window approximation via HyperLogLog
fix: prevent token-bucket underflow on concurrent refill
docs: clarify Lua script atomicity in ADR-0002
perf: shard memory store by 256 buckets
test: add race-scenario for sliding-window eviction
chore: bump go-redis to v9.7.0
```

## Branching and Releases

- `main` is always releasable.
- Release tags follow `vMAJOR.MINOR.PATCH`.
- Releases are cut by `release.yml` via goreleaser; see the strategy table
  in `README.md` for the roadmap (v0.1.0 -> v1.0.0).

## Adding an Algorithm or Store

1. Implement the `Algorithm` / `Store` interface in `internal/`.
2. Add table-driven tests covering: happy path, boundary (limit == 0,
   burst == 1), concurrency (race), and refill/reset semantics.
3. Add a benchmark in the same `_test.go` file.
4. Wire it through `internal/config` defaults and the server's policy
   registry.
5. Update `docs/architecture.md` and the README feature list.

## Reporting Bugs

Open an issue using the bug report template. Include: Go version, Redis
version (if applicable), store backend, the exact command you ran, the
observed vs expected behavior, and (if possible) a minimal reproducer.

## License

By contributing, you agree that your contributions will be licensed under
the Apache-2.0 license.
