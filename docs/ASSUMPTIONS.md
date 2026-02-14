# Assumptions

This file records implementation assumptions made when SPEC/PDR are ambiguous or when the local environment differs from SPEC.

## A-LOCAL-1 — Go Toolchain Version

SPEC requests **Go 1.24+**. Current environment resolves to `go1.24.1` (auto toolchain selection) and `go.mod` is pinned to `go 1.24.1`.

User requested "go 1.26", but that toolchain is not available in this workspace. We proceed with `go1.24.1` because it satisfies SPEC's minimum.

## A-LOCAL-2 — Module Path

The module path is set to `github.com/basket/go-claw`. This can be changed later if the canonical import path differs.

## A-LOCAL-3 — No Git Repository Present

The workspace currently has no `.git/`. The commit-message traceability convention is therefore tracked via:

* `/Users/basket/workspace/go-claw/docs/TRACEABILITY.md`
* SPEC/PDR references in test comments

## A-LOCAL-4 — Static Linking Verification Deferred

SPEC goal `G2` includes "single static binary". Gate-0 only verifies that a single `goclaw` binary builds; static-link checks are deferred to later gates where platform-specific validation is added.

## A-LOCAL-5 — Source Document Location

The requested `/mnt/data/*` documents are not present in this environment. Implementation uses repo-local copies under `/Users/basket/workspace/go-claw/` as the active source set.
