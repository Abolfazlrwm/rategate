## Summary
<!-- One paragraph: what does this PR change and why? -->

## Type of change
- [ ] feat: new feature
- [ ] fix: bug fix
- [ ] perf: performance improvement
- [ ] docs: documentation only
- [ ] refactor: no behavior change
- [ ] test: test-only
- [ ] chore: deps / CI / tooling

## Checklist — Non-Negotiable Standards
- [ ] `go test -race ./...` passes locally
- [ ] `golangci-lint run ./...` passes (config in `.golangci.yml`)
- [ ] 100% coverage maintained on `internal/algorithms` and `internal/store` (`make test-cover`)
- [ ] No unrecovered panics in request paths (recovery middleware covers new handlers)
- [ ] Every new exported identifier has a doc comment
- [ ] `go vet ./...` clean

## Benchmarks (if perf-sensitive)
<!-- Paste before/after from `make bench` and/or the benchstat diff. -->

```
before:

after:
```

## Backward compatibility
- [ ] No breaking API change
- [ ] Breaking change (explain below and bump major)

## Docs
- [ ] Updated `docs/api-reference.md` for any API change
- [ ] Updated `CHANGELOG.md` under `[Unreleased]`
- [ ] Updated `README.md` benchmark table if numbers changed

## Related issues
<!-- "Closes #123", "Refs #456". -->
