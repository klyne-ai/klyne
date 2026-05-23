## Summary

<!-- 1–3 bullets describing what changed and why. -->

## Test plan

- [ ] `go vet ./...` clean
- [ ] `go test -race ./...` green
- [ ] `cd ui && npm run check` clean
- [ ] `cd ui && npm test` green
- [ ] `cd ui && npm run build` succeeds
- [ ] Manual smoke test (describe what you exercised in the dashboard / CLI)

## Notes for reviewer

<!-- Anything non-obvious: API contract changes, follow-up TODOs,
     screenshots, links to spec docs under docs/superpowers/. -->
