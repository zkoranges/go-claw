# Release Readiness Checklist

This checklist is mandatory before merging release-bound changes.

## 1. Build And Test Gates

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test ./... -count=1`
- [ ] `go test -race ./... -count=1`

## 2. Parity Metadata And Evidence Gates

- [ ] If `docs/parity/parity.yaml` changed, each changed row includes:
  - [ ] `owner`
  - [ ] `target_release`
  - [ ] `risk`
  - [ ] `spec_refs`
  - [ ] `traceability_refs`
- [ ] For every changed row with `verified: true`, evidence is explicit and updated:
  - [ ] `evidence` path exists in repo.
  - [ ] Evidence content reflects this change (new artifact or updated log/report).
- [ ] Regenerate and verify scorecard is clean:
  - [ ] `go run ./tools/parity/scorecard -in docs/parity/parity.yaml -out docs/parity/scorecard.generated.md`
  - [ ] `git diff --exit-code -- docs/parity/scorecard.generated.md`
- [ ] `FEATURE_PARITY.md` reflects current scorecard snapshot and comparison date.

## 3. Runtime Operational Smoke Gate

- [ ] Start daemon in a clean temp home with policy granting `acp.read` and `acp.mutate`.
- [ ] Run ACP smoke verifier:
  - [ ] `go run ./tools/verify/runtime_smoke -url ws://127.0.0.1:<port>/ws -token <token> -timeout 15s`
- [ ] Smoke must validate:
  - [ ] `system.hello`
  - [ ] `system.status`
  - [ ] `agent.chat` enqueue
  - [ ] `approval.request` + `approval.required` broadcast
  - [ ] `approval.respond` + `approval.updated`

## 4. Sign-off

- [ ] Runtime owner sign-off
- [ ] Security owner sign-off
- [ ] Docs/parity owner sign-off
