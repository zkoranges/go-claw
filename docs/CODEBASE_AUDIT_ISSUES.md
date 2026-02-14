# Codebase Audit Issues

**Date:** 2026-02-13
**Auditor:** Post-implementation codebase auditor (extra-high reasoning)
**Baseline:** All tests pass (27 packages). `go vet` clean. 22 files have `gofmt` formatting issues.

---

## Summary Table

| Area | P0 | P1 | P2 | Total |
|------|----|----|----|----|
| 1. Skill parsing behavior | 0 | 1 | 3 | 4 |
| 2. Multi-source skill loading | 0 | 1 | 2 | 3 |
| 3. Installation / update / removal | 1 | 2 | 1 | 4 |
| 4. Persistence & migrations | 0 | 1 | 2 | 3 |
| 5. Brain integration and injection | 0 | 2 | 2 | 4 |
| 6. Daemon wiring & runtime safety | 0 | 1 | 1 | 2 |
| 7. Security posture | 0 | 2 | 1 | 3 |
| 8. Test coverage & regression risk | 0 | 2 | 3 | 5 |
| **Totals** | **1** | **12** | **15** | **28** |

---

## Top 5 Risks (prioritized remediation order)

1. **AUD-007 (P0)**: Skill update deletes existing install before re-cloning; git clone failure causes permanent data loss of the installed skill.
2. **AUD-013 (P1)**: `ReplaceLoadedSkills` drops all instruction/legacy skills from the brain catalog before re-adding them, creating a window where prompts match no skills.
3. **AUD-014 (P1)**: `Stream()` response path does not perform skill matching or injection — only `Respond()` does. Skills are effectively unavailable during streaming.
4. **AUD-016 (P1)**: Legacy skill `enforceWriteRestriction` is regex-based and trivially bypassable via shell features (variable expansion, eval, base64 decode, subshells).
5. **AUD-010 (P1)**: `IncrementSkillFault` uses UPDATE + SELECT as two separate round-trips, creating a TOCTOU race where the returned quarantine status may be stale.

---

## Issues

---

### AUD-001
- **Severity:** P2
- **Area:** 1. Skill parsing behavior
- **Title:** Unclosed frontmatter silently falls through to V1 YAML path
- **Observed behavior:** When a SKILL.md starts with `---` but has no closing `---` delimiter, `extractFrontmatter()` returns `(nil, "", nil)`. `ParseSkillMD` then falls through to Stage 2 (V1 plain YAML) and attempts to parse the entire file as raw YAML. This may succeed or fail unpredictably depending on the markdown body content.
- **Risk:** A malformed SKILL.md with an unclosed frontmatter block may be silently interpreted as plain YAML, producing unexpected field values from markdown content that happens to parse as YAML.
- **Evidence:**
  ```
  rg "No terminating delimiter" internal/sandbox/legacy/skill.go
  ```
  Line 148-150: "No terminating delimiter found. Treat as no canonical frontmatter."
  No test covers this specific path. The closest is `TestLoadOne_InvalidSkillMD` which tests malformed YAML, not unclosed delimiters.
- **Suggested fix (high-level):** Add a test for unclosed frontmatter. Consider returning a warning or error when the opening `---` is present but no closing delimiter is found, rather than silently falling through.
- **Confidence:** High

---

### AUD-002
- **Severity:** P2
- **Area:** 1. Skill parsing behavior
- **Title:** `extractFencedScript` only matches `bash` and `sh` language hints
- **Observed behavior:** The Stage 3 markdown fallback parser uses the regex `` (?s)```(?:bash|sh)?\s*(.*?)``` `` to extract fenced code blocks. Code blocks with other language hints (e.g., `` ```python ``, `` ```javascript ``) are not matched and trigger a "missing script section" error.
- **Risk:** Skills authored with non-bash code fence language hints fail to parse with an unclear error when they lack canonical frontmatter.
- **Evidence:**
  ```
  rg "extractFencedScript" internal/sandbox/legacy/skill.go
  ```
  Line 192: `re := regexp.MustCompile("(?s)` `` ` ``(?:bash|sh)?\s*(.*?)` `` ` ``")`
- **Suggested fix (high-level):** Either broaden the regex to match any language hint (since the script is extracted as text), or document that only `bash`/`sh` code blocks are supported in the fallback path.
- **Confidence:** High

---

### AUD-003
- **Severity:** P1
- **Area:** 1. Skill parsing behavior
- **Title:** No size limit on SKILL.md file reads
- **Observed behavior:** Both `loader.go:LoadOne` (line 134) and `brain.go:ensureSkillInstructionsLoaded` (line 775) use `os.ReadFile()` on SKILL.md without any size limit. A malicious or accidentally large SKILL.md could cause unbounded memory allocation.
- **Risk:** A multi-gigabyte SKILL.md in a project or installed skill directory causes OOM on the daemon.
- **Evidence:**
  ```
  rg "os.ReadFile.*SKILL.md" internal/skills/loader.go internal/engine/brain.go
  ```
  loader.go:134 and brain.go:775 — both use `os.ReadFile` with no size guard.
- **Suggested fix (high-level):** Add a size limit check (e.g., `os.Stat` before `os.ReadFile`, reject files > 1MB) or use `io.LimitReader`.
- **Confidence:** High

---

### AUD-004
- **Severity:** P2
- **Area:** 1. Skill parsing behavior
- **Title:** `hasCanonicalFrontmatter` uses `TrimLeft` which accepts leading whitespace
- **Observed behavior:** `hasCanonicalFrontmatter()` at `installer.go:251` uses `strings.TrimLeft(string(data), "\r\n\t ")` before checking for the `---` prefix. This means a file with leading whitespace/newlines before `---` passes the canonical frontmatter check. However, `extractFrontmatter()` also trims the first line, so parsing succeeds — but the two functions have subtly different trimming logic.
- **Risk:** Inconsistent validation: a file could pass `hasCanonicalFrontmatter` but be parsed differently by `extractFrontmatter`. Low practical impact but violates the principle of consistent validation.
- **Evidence:**
  ```
  rg "hasCanonicalFrontmatter" internal/skills/installer.go
  ```
  Line 250-253: uses `TrimLeft` which removes all leading chars in the set.
- **Suggested fix (high-level):** Align trimming logic between `hasCanonicalFrontmatter` and `extractFrontmatter`, or delegate validation to a single function.
- **Confidence:** Medium

---

### AUD-005
- **Severity:** P2
- **Area:** 2. Multi-source skill loading
- **Title:** Duplicate collision-detection logic between Loader and daemon `loadAllSkillMD`
- **Observed behavior:** `loader.go:LoadAll` (lines 56-118) implements its own `seen` map for collision detection within a single Loader instance. Separately, `cmd/goclaw/main.go:loadAllSkillMD` (lines 320-373) implements a second `seen` map to merge results across multiple Loader instances. The two collision-detection implementations use slightly different key derivation: the loader uses `ent.Name()` (directory name), while main.go uses `filepath.Base(ls.SourceDir)` falling back to `ls.Skill.Name`.
- **Risk:** A skill could pass collision detection in one layer but be caught (or missed) in the other, leading to confusing behavior when skill names differ from directory names.
- **Evidence:**
  ```
  rg "seen\[key\]" internal/skills/loader.go cmd/goclaw/main.go
  ```
  loader.go:92 vs main.go:333 — different key derivation strategies.
- **Suggested fix (high-level):** Consolidate collision detection into a single canonical-name function used by both code paths.
- **Confidence:** High

---

### AUD-006
- **Severity:** P1
- **Area:** 2. Multi-source skill loading
- **Title:** Skill name "go" or other common words causes false activation matches
- **Observed behavior:** `brain.go:matchSkillForPrompt` (lines 727-751) uses `strings.Contains(lower, key)` to match skill names against prompts. A skill named "go", "run", "log", "help", or any common word would match virtually every prompt. The longest-match heuristic mitigates collisions between skills but not false positives from short common names.
- **Risk:** Skills with short common-word names activate on unrelated prompts, injecting irrelevant instructions into the system prompt and consuming context window budget.
- **Evidence:**
  ```
  rg "matchSkillForPrompt" internal/engine/brain.go
  ```
  Line 744: `if strings.Contains(lower, key)` — pure substring match with no word-boundary check.
- **Suggested fix (high-level):** Add word-boundary matching (e.g., require whitespace or punctuation around the skill name match), or require a minimum skill name length for auto-activation (e.g., >= 4 chars).
- **Confidence:** High

---

### AUD-007
- **Severity:** P0
- **Area:** 3. Installation / update / removal
- **Title:** Skill update is non-atomic — git clone failure after directory removal causes data loss
- **Observed behavior:** `installer.go:installToDir` with `overwrite=true` (called from `Update`) first calls `os.RemoveAll(destDir)` at line 156, then proceeds with git clone at line 170. If the git clone fails (network error, invalid ref, disk full), the original skill directory has already been deleted and is unrecoverable. The temp directory cleanup (`defer os.RemoveAll(tmp)`) means no artifact remains.
- **Risk:** A transient network failure during `goclaw skill update` permanently destroys the installed skill with no recovery path.
- **Evidence:**
  ```
  rg "overwrite" internal/skills/installer.go
  ```
  Line 155-157: `if overwrite { _ = os.RemoveAll(destDir) }` — unconditional deletion before clone attempt.
- **Suggested fix (high-level):** Clone into temp first, validate SKILL.md, then atomically swap: rename old dir to `.bak`, rename new dir to destDir, remove `.bak` on success. On failure, rename `.bak` back.
- **Confidence:** High

---

### AUD-008
- **Severity:** P1
- **Area:** 3. Installation / update / removal
- **Title:** `ListInstalledSkills` hardcodes `WHERE source = 'github'` — excludes non-GitHub installs
- **Observed behavior:** `store.go:ListInstalledSkills` (line 2165) queries `WHERE COALESCE(source, 'local') = 'github'`. The installer always registers with `source = "github"` (line 2142), even for local filesystem installs. If a future source type (e.g., "registry", "s3") is added, those skills would be invisible to `list`, `update`, and `info` commands.
- **Risk:** The query couples the listing to a single source type string, making the system fragile to new install sources. Additionally, local installs being tagged as "github" is semantically misleading.
- **Evidence:**
  ```
  rg "source.*github" internal/persistence/store.go internal/skills/installer.go
  ```
  store.go:2165 and installer.go:2142 — hardcoded "github" everywhere.
- **Suggested fix (high-level):** Use a distinct source value for local installs (e.g., "local" or "file"). Change the query to `WHERE installed_at IS NOT NULL` or `WHERE abi_version = 'n/a'` to identify installer-registered skills regardless of source.
- **Confidence:** High

---

### AUD-009
- **Severity:** P1
- **Area:** 3. Installation / update / removal
- **Title:** `Remove` silently ignores DB deletion failure
- **Observed behavior:** `installer.go:Remove` (line 82) calls `_ = i.store.RemoveInstalledSkill(ctx, safeName)` with the error explicitly discarded. If the DB is locked, corrupt, or the skill_id doesn't match, the filesystem directory is removed but the DB provenance record persists.
- **Risk:** Orphaned DB records after removal. `goclaw skill list` still shows the removed skill. `goclaw skill update` attempts to update a non-existent directory and fails confusingly.
- **Evidence:**
  ```
  rg "_ = i.store.Remove" internal/skills/installer.go
  ```
  Line 82: `_ = i.store.RemoveInstalledSkill(ctx, safeName)`
- **Suggested fix (high-level):** Propagate the error from `RemoveInstalledSkill`. If desired, make it non-fatal with a warning log rather than silently discarding.
- **Confidence:** High

---

### AUD-010
- **Severity:** P1
- **Area:** 4. Persistence & migrations
- **Title:** `IncrementSkillFault` has TOCTOU race between UPDATE and SELECT
- **Observed behavior:** `store.go:IncrementSkillFault` (lines 2215-2235) executes an UPDATE to increment the fault count and conditionally set state to 'quarantined', followed by a separate SELECT to read the resulting state. These are two independent SQL statements without a wrapping transaction. Between the UPDATE and SELECT, another goroutine could call `ReenableSkill`, `IncrementSkillFault` again, or even `UpsertSkill` (which resets fault_count to 0).
- **Risk:** The returned `quarantined bool` may not reflect the actual state, causing the caller to miss a quarantine event or falsely report one. In a busy system with concurrent skill faults, this could delay or miss quarantine actions.
- **Evidence:**
  ```
  rg "IncrementSkillFault" internal/persistence/store.go
  ```
  Lines 2215-2235: UPDATE on line 2215, SELECT on line 2232 — separate round-trips, no transaction.
- **Suggested fix (high-level):** Wrap the UPDATE and SELECT in an explicit transaction (`db.BeginTx`), or use SQLite's `RETURNING` clause to get the updated state in a single statement.
- **Confidence:** High

---

### AUD-011
- **Severity:** P2
- **Area:** 4. Persistence & migrations
- **Title:** `skill_registry.state` column has no CHECK constraint
- **Observed behavior:** The `state` column in `skill_registry` (store.go line 384) is defined as `TEXT NOT NULL DEFAULT 'active'` with no CHECK constraint. Valid state values ('active', 'quarantined') are enforced only in application logic. Any SQL statement (manual or buggy) could insert arbitrary state values like 'disabled', 'deleted', or empty string.
- **Risk:** Invalid state values could cause unexpected behavior in `IsSkillQuarantined` (which only checks for `state == "quarantined"`) — a skill with an invalid state would not be quarantined but also not behave as "active".
- **Evidence:**
  ```
  rg "state TEXT" internal/persistence/store.go
  ```
  Line 384: `state TEXT NOT NULL DEFAULT 'active'` — no CHECK constraint unlike `side_effect_status` in `tool_call_dedup` (line 374).
- **Suggested fix (high-level):** Add `CHECK(state IN ('active', 'quarantined'))` to the CREATE TABLE statement, similar to the CHECK constraint on `tool_call_dedup.side_effect_status`.
- **Confidence:** Medium

---

### AUD-012
- **Severity:** P2
- **Area:** 4. Persistence & migrations
- **Title:** `RegisterInstalledSkill` always sets `source = 'github'` even for local installs
- **Observed behavior:** `store.go:RegisterInstalledSkill` (line 2142) defaults `source` to "github" when the provided source is empty. The caller in `installer.go:installToDir` (line 212) always passes "github" as the source, even for local filesystem installs via `file://` or absolute paths.
- **Risk:** Semantic confusion: provenance records for local installs falsely claim GitHub origin. This could mislead audit trails and make it impossible to distinguish local vs remote installs from the DB alone.
- **Evidence:**
  ```
  rg "RegisterInstalledSkill" internal/skills/installer.go internal/persistence/store.go
  ```
  installer.go:212: `i.store.RegisterInstalledSkill(ctx, name, "github", srcURL, ref)` — hardcoded "github".
  store.go:2142: `source = "github"` default.
- **Suggested fix (high-level):** Pass the actual source type from `parseGitHubishURL` (which already distinguishes "local" from GitHub hosts). Store "local" for local installs, "github" for GitHub.
- **Confidence:** High

---

### AUD-013
- **Severity:** P1
- **Area:** 5. Brain integration and injection
- **Title:** `ReplaceLoadedSkills` creates a transient window with empty skill catalog
- **Observed behavior:** `brain.go:ReplaceLoadedSkills` (lines 702-715) first acquires the write lock, deletes all instruction/legacy entries from `loadedSkills`, then releases the lock. It then calls `RegisterLoadedSkills` which acquires the lock again to add new entries. Between these two lock acquisitions, any concurrent `matchSkillForPrompt` or `use_skill` call sees an empty catalog.
- **Risk:** During hot-reload, concurrent `Respond()` calls briefly cannot match or invoke any instruction/legacy skills. While transient (microseconds), under load this could cause observable skill activation failures.
- **Evidence:**
  ```
  rg "ReplaceLoadedSkills" internal/engine/brain.go
  ```
  Lines 702-715: Unlock at line 712, then RegisterLoadedSkills at line 714 re-acquires the lock.
- **Suggested fix (high-level):** Hold the write lock for the entire replace operation: delete old entries and insert new entries within a single lock acquisition, rather than releasing and re-acquiring.
- **Confidence:** High

---

### AUD-014
- **Severity:** P1
- **Area:** 5. Brain integration and injection
- **Title:** `Stream()` does not perform skill matching or instruction injection
- **Observed behavior:** `brain.go:Stream()` (lines 481-581) does not call `matchSkillForPrompt()` or `ensureSkillInstructionsLoaded()`. Compare with `Respond()` (lines 370-391) which has the full skill progressive disclosure path. When a user sends a prompt via the streaming endpoint, skills are never activated.
- **Risk:** Skills are silently unavailable during streaming sessions. Users who use `agent.chat.stream` (WebSocket streaming) get different behavior than `agent.chat` users, with no indication that skills are disabled.
- **Evidence:**
  ```
  rg "matchSkillForPrompt\|ensureSkillInstructionsLoaded" internal/engine/brain.go
  ```
  `matchSkillForPrompt` appears only in `Respond()` (line 375), not in `Stream()`.
- **Suggested fix (high-level):** Add the same skill matching and injection logic to `Stream()` before building the generate options.
- **Confidence:** High

---

### AUD-015
- **Severity:** P2
- **Area:** 5. Brain integration and injection
- **Title:** `expandSkillFileReferences` has no total size cap for inlined content
- **Observed behavior:** `brain.go:expandSkillFileReferences` (lines 811-883) reads and inlines every matched file reference from `scripts/`, `references/`, and `assets/` directories. Individual files are capped at 64KB (line 868), but there is no limit on the total number of files or total accumulated size. A skill with 100 reference files could produce 6.4MB of instructions.
- **Risk:** Large skill instructions consume excessive LLM context window budget, cause slow responses, and increase API costs. A malicious skill could also cause high memory usage.
- **Evidence:**
  ```
  rg "maxBytes" internal/engine/brain.go
  ```
  Line 868: `const maxBytes = 64 << 10` — per-file limit only, no aggregate limit.
- **Suggested fix (high-level):** Add a total size cap (e.g., 256KB) across all expanded file references. Stop inlining once the cap is reached and log a warning.
- **Confidence:** High

---

### AUD-016
- **Severity:** P1
- **Area:** 7. Security posture
- **Title:** Legacy skill `enforceWriteRestriction` is trivially bypassable
- **Observed behavior:** `legacy/skill.go:enforceWriteRestriction` (lines 291-314) uses regex matching to detect shell redirects (`>`, `>>`, `tee`) and path traversal (`../`). This is trivially bypassed via: shell variable expansion (`$HOME`), eval (`eval "cmd > /etc/file"`), base64-encoded commands (`echo Y3A= | base64 -d | sh`), subshell nesting, `cp`/`mv` commands (not checked), `python -c "open('/etc/file','w')"`, or `curl -o /path`.
- **Risk:** A malicious legacy skill could write to arbitrary filesystem locations despite the restriction, as acknowledged by the code comment at line 292: "v0.1 limitation: without an OS-level sandbox, this check is best-effort."
- **Evidence:**
  ```
  rg "enforceWriteRestriction" internal/sandbox/legacy/skill.go
  ```
  Lines 291-314. The function itself acknowledges the limitation. `isDangerous` (lines 270-283) only checks 4 patterns: `rm `, `rm\t`, `dd `, `mkfs`.
- **Suggested fix (high-level):** Since the restriction is acknowledged as best-effort, ensure the code path is gated by both `legacy.run` and `legacy.dangerous` capabilities (currently it is). Consider adding a prominent warning in system.status when legacy_mode is enabled. Long-term, the Docker sandbox (`tools/docker.go`) should be the recommended execution path for untrusted scripts.
- **Confidence:** High

---

### AUD-017
- **Severity:** P1
- **Area:** 7. Security posture
- **Title:** Legacy skill `Run` sets HOME to workspace but does not prevent absolute path access
- **Observed behavior:** `legacy/skill.go:Run` (lines 253-258) sets `HOME` and `WORKSPACE` environment variables to the workspace directory, but executes the script via `/bin/sh -lc` with the full host environment inherited (`os.Environ()`). The script has unrestricted read access to the entire filesystem and can access files via absolute paths regardless of the HOME override.
- **Risk:** A legacy skill with `legacy.run` capability can read any file on the host filesystem (SSH keys, credentials, config files) and exfiltrate data via network calls if network access is available.
- **Evidence:**
  ```
  rg "os.Environ" internal/sandbox/legacy/skill.go
  ```
  Line 254: `cmd.Env = append(os.Environ(), ...)` — full host environment inherited.
- **Suggested fix (high-level):** Document this as a known limitation of legacy mode. Add a warning in system.status when `legacy.run` capability is granted. Recommend Docker sandbox mode for untrusted skills.
- **Confidence:** High

---

### AUD-018
- **Severity:** P2
- **Area:** 7. Security posture
- **Title:** No rate limiting on `use_skill` tool invocations
- **Observed behavior:** The `use_skill` Genkit tool (brain.go:215-252) performs disk I/O on every invocation (via `ensureSkillInstructionsLoaded`). While the instructions are cached after first load, the tool can be invoked repeatedly by the LLM within a single turn (up to `MaxTurns=3`). There is no per-session or per-minute rate limit.
- **Risk:** An adversarial prompt could cause the LLM to repeatedly invoke `use_skill` across multiple skills, causing disk I/O storms and excessive context window consumption. The `MaxTurns=3` limit provides some protection but applies to all tools collectively.
- **Evidence:**
  ```
  rg "use_skill" internal/engine/brain.go
  ```
  Line 215: Tool defined without rate limiting. Line 431: `ai.WithMaxTurns(3)` limits total tool turns but not individual tool call frequency.
- **Suggested fix (high-level):** Consider per-skill cooldown or deduplication within a single response (don't re-inject if already injected in this session turn). The `MaxTurns=3` limit is a reasonable but coarse protection.
- **Confidence:** Medium

---

### AUD-019
- **Severity:** P2
- **Area:** 5. Brain integration and injection
- **Title:** `matchSkillForPrompt` iterates map — non-deterministic match order on tie
- **Observed behavior:** `brain.go:matchSkillForPrompt` (lines 727-751) iterates over the `loadedSkills` map to find the longest key that is a substring of the prompt. Go map iteration order is randomized. If two skills have names of equal length that both match the prompt, the winner is non-deterministic across runs.
- **Risk:** Non-deterministic skill activation when multiple skills of the same name length match. While unlikely in practice, this violates the principle of deterministic behavior.
- **Evidence:**
  ```
  rg "for key, entry := range b.loadedSkills" internal/engine/brain.go
  ```
  Line 734: Map iteration — order is not guaranteed.
- **Suggested fix (high-level):** On tie (equal key length), apply a secondary tiebreaker such as alphabetical order of the key.
- **Confidence:** Medium

---

### AUD-020
- **Severity:** P1
- **Area:** 6. Daemon wiring & runtime safety
- **Title:** `reloadSkills` performs WASM module loading without concurrency guard against the WASM watcher
- **Observed behavior:** `cmd/goclaw/main.go:reloadSkills` (lines 408-453) is called from the skillWatcher goroutine (line 617). It calls `wasmHost.LoadModuleFromBytes` (line 447) to load WASM modules. Simultaneously, the WASM `hotswap.go` watcher (started at line 289) may also be loading modules via `wasmHost.LoadModuleFromBytes`. Both goroutines access the WASM host concurrently. While `wasmHost` has internal mutex protection (`modulesMu`), the skill-level reload (remove + re-add) is not atomic: between `brain.ReplaceLoadedSkills` (line 429) and the WASM module loading loop (lines 432-453), a WASM watcher event could register a module that `ReplaceLoadedSkills` just removed.
- **Risk:** Race between WASM watcher and skill watcher could cause a module to be loaded in the WASM host but not registered in the brain, or vice versa. The skill would appear in some status views but not others.
- **Evidence:**
  ```
  rg "LoadModuleFromBytes" cmd/goclaw/main.go internal/sandbox/wasm/hotswap.go
  ```
  main.go:447 (from skillWatcher goroutine) and hotswap.go:177 (from WASM watcher goroutine) — concurrent access.
- **Suggested fix (high-level):** Consider using a single reload coordinator that serializes all skill and WASM reload operations, or add a mutex that guards the entire reload-and-register sequence.
- **Confidence:** Medium

---

### AUD-021
- **Severity:** P2
- **Area:** 6. Daemon wiring & runtime safety
- **Title:** `toolsUpdated` channel drops events silently when buffer is full
- **Observed behavior:** `cmd/goclaw/main.go:forwardUpdates` (lines 601-612) and the skillWatcher forwarder (lines 615-623) both use non-blocking sends with `default:` case on the `toolsUpdated` channel (buffer size 32). If the gateway consumer is slow, tool-update notifications are silently dropped.
- **Risk:** Connected WebSocket clients may miss `tools.updated` notifications, causing their UI to show stale skill status. This is a UX issue rather than a correctness issue, since the client can re-fetch status on demand.
- **Evidence:**
  ```
  rg "toolsUpdated" cmd/goclaw/main.go
  ```
  Lines 600, 608, 619 — non-blocking sends with `default:` case.
- **Suggested fix (high-level):** Acceptable trade-off for backpressure protection. Consider logging dropped events at DEBUG level so the issue is diagnosable.
- **Confidence:** Medium

---

### AUD-022
- **Severity:** P1
- **Area:** 8. Test coverage & regression risk
- **Title:** No tests for skills watcher (`watcher.go`)
- **Observed behavior:** `internal/skills/watcher.go` (179 lines) has no corresponding `watcher_test.go`. The watcher implements debouncing logic, dynamic directory watching, event filtering, and timer management — all critical for hot-reload correctness.
- **Risk:** Regressions in debounce timing, event filtering, or dynamic watch registration would not be caught by CI. The watcher is the critical bridge between filesystem changes and skill hot-reload.
- **Evidence:**
  ```
  ls internal/skills/watcher_test.go
  ```
  File does not exist. The TODO.md at line 40 claims "Implemented in `internal/skills/watcher.go`, `internal/skills/watcher_test.go`" but no such test file exists.
- **Suggested fix (high-level):** Add tests covering: (1) debounce coalescing, (2) filtering of non-skill files, (3) dynamic watch of newly created skill directories, (4) context cancellation cleanup.
- **Confidence:** High

---

### AUD-023
- **Severity:** P1
- **Area:** 8. Test coverage & regression risk
- **Title:** 22 source files have `gofmt` formatting issues
- **Observed behavior:** Running `gofmt -l .` lists 22 Go source files that are not properly formatted. This includes core files like `cmd/goclaw/main.go`, `internal/engine/brain.go`, `internal/policy/policy.go`, and multiple test files.
- **Risk:** Inconsistent formatting makes code review harder and indicates that pre-commit hooks or CI formatting checks are not enforced. Some organizations treat formatting failures as CI blockers.
- **Evidence:**
  ```
  gofmt -l .
  ```
  Returns 22 files including `cmd/goclaw/main.go`, `internal/config/config.go`, `internal/engine/brain.go`, `internal/policy/policy.go`, etc.
- **Suggested fix (high-level):** Run `gofmt -w .` to fix all formatting, then add a CI step or pre-commit hook that fails on `gofmt -l` output.
- **Confidence:** High

---

### AUD-024
- **Severity:** P2
- **Area:** 8. Test coverage & regression risk
- **Title:** Multiple tests use `time.Sleep` — flaky test risk
- **Observed behavior:** At least 19 test call sites use `time.Sleep` for synchronization, spread across 8 packages: `cron/scheduler_test.go`, `gateway/gateway_test.go`, `engine/engine_test.go`, `engine/failover_test.go`, `persistence/store_test.go`, `config/watcher_test.go`, `smoke/research_loop_test.go`, `smoke/skills_daemon_e2e_test.go`, `smoke/startup_order_test.go`.
- **Risk:** Sleep-based synchronization is inherently flaky under CI load. Slow CI runners or resource contention could cause intermittent test failures.
- **Evidence:**
  ```
  rg "time\.Sleep" --glob "*_test.go" internal/
  ```
  Returns 19 matches across 8 files.
- **Suggested fix (high-level):** Replace `time.Sleep` with event-driven synchronization (channels, condition variables, or polling loops with deadlines) where feasible. Some sleeps (e.g., debounce testing) are inherently timing-dependent and acceptable with generous margins.
- **Confidence:** Medium

---

### AUD-025
- **Severity:** P2
- **Area:** 8. Test coverage & regression risk
- **Title:** `internal/channels` package has no test files
- **Observed behavior:** The `internal/channels/` package (containing `channel.go` and `telegram.go`) has no `_test.go` files. The Telegram channel integration handles user message routing, session mapping, and task polling — all critical paths.
- **Risk:** Regressions in channel routing, allowlist enforcement, or message formatting would not be caught. The Telegram bot token handling and allowlist are security-relevant.
- **Evidence:**
  ```
  ls internal/channels/*_test.go
  ```
  No test files found. `go test ./...` output confirms: `? github.com/basket/go-claw/internal/channels [no test files]`
- **Suggested fix (high-level):** Add unit tests for allowlist enforcement, session-to-user mapping, and message routing logic (using a mock Telegram API).
- **Confidence:** High

---

### AUD-026
- **Severity:** P2
- **Area:** 8. Test coverage & regression risk
- **Title:** No test for concurrent skill reload + brain activation race
- **Observed behavior:** There are no tests that exercise concurrent `ReplaceLoadedSkills` + `Respond` or `use_skill` invocations. The brain tests (`brain_test.go`) are all single-threaded.
- **Risk:** The transient empty catalog issue (AUD-013) and the WASM watcher race (AUD-020) are untested. Data races could exist but are not detected because tests are not run with `-race` flag by default.
- **Evidence:**
  ```
  rg "t\.Parallel\|go func" internal/engine/brain_test.go
  ```
  No parallel tests or goroutines in brain tests. No `-race` flag in the documented test commands.
- **Suggested fix (high-level):** Add a concurrent stress test that runs `ReplaceLoadedSkills` and `Respond` in parallel goroutines with `-race` enabled. Add `-race` to the default test command.
- **Confidence:** High

---

### AUD-027
- **Severity:** P2
- **Area:** 3. Installation / update / removal
- **Title:** `copyTreeExcludingGit` does not handle `.git` as a file (sparse checkout / submodule)
- **Observed behavior:** `installer.go:copyTreeExcludingGit` (lines 366-372) skips `.git` entries when `d.IsDir()` is true (`filepath.SkipDir`). However, in sparse checkouts, worktrees, or submodules, `.git` can be a regular file (containing a `gitdir:` pointer). In that case, the `.git` file would be copied to the installed skill directory.
- **Risk:** A `.git` file in the installed skill reveals the original clone location and may contain sensitive path information.
- **Evidence:**
  ```
  rg "\.git" internal/skills/installer.go
  ```
  Lines 366-372: Only checks `d.IsDir()` for `.git`, not `rel == ".git"` for files.
- **Suggested fix (high-level):** Change the `.git` check to skip both files and directories named `.git`: check `rel == ".git"` regardless of `d.IsDir()`.
- **Confidence:** Medium

---

### AUD-028
- **Severity:** P2
- **Area:** 2. Multi-source skill loading
- **Title:** Loader silently skips symlinked skill directories with no log
- **Observed behavior:** `loader.go:LoadAll` (line 87-88) uses `os.ReadDir` which returns `DirEntry` objects. For symlinks pointing to directories, `ent.IsDir()` returns `false` (because `os.ReadDir` uses `lstat`, not `stat`). The entry is silently skipped at line 88 (`if !ent.IsDir() { continue }`). There is no log message indicating that a symlinked skill directory was found and skipped.
- **Risk:** A user who symlinks a skill directory into `$GOCLAW_HOME/skills/` will see the skill silently ignored with no diagnostic message.
- **Evidence:**
  ```
  rg "ent.IsDir" internal/skills/loader.go
  ```
  Line 88: `if !ent.IsDir() { continue }` — no log for non-directory entries that might be symlinks.
- **Suggested fix (high-level):** After the `!ent.IsDir()` check, add a secondary check: if the entry is a symlink to a directory (via `os.Stat`), log a warning message like "skill directory is a symlink; symlinks are not followed."
- **Confidence:** Medium
