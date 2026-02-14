# Legacy Skills (V1) — Write Restriction Limitations (v0.1)

Legacy skills execute shell scripts via `/bin/sh -lc` (see `internal/sandbox/legacy/skill.go`). This is inherently high-risk.

## What v0.1 enforces

- **Default deny**: legacy execution requires policy capability `legacy.run`.
- **Dangerous command gating**: obvious destructive patterns require `legacy.dangerous` plus a confirmation callback.
- **Best-effort write restriction**: the runner blocks common write patterns that target:
  - absolute paths (e.g. `> /tmp/file`, `tee /etc/...`)
  - home-relative paths (e.g. `> ~/file`)
  - parent directory traversal (e.g. `../outside.txt`)
- **Mitigation**: the runner sets `HOME` to the workspace directory to reduce accidental `~/` writes.

## Known limitations (v0.1)

- The write restriction is **not a full sandbox**. Shell quoting, indirect writes via interpreters (Python/Node/etc.), and other techniques can bypass simple pattern checks.
- GoClaw v0.1 does **not** provide an OS-level filesystem sandbox (e.g. `bwrap`) across all supported platforms.

## Recommended posture

- Keep `skills.legacy_mode: false` unless you explicitly need legacy skills (SPEC `GC-SPEC-CFG-007`).
- Treat legacy skills as **trusted-only** code and review scripts before enabling `legacy.run` in `policy.yaml`.

