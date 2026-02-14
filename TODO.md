# TODO — Project GoClaw

**Last Updated:** 2026-02-14

## Open

### Memory Phase 2 — FTS5 full-text search over workspace
- Add `memory_documents` table with FTS5 virtual table
- Chunk documents (800 words, 15% overlap)
- Full-text search via `MATCH` queries

### Web Dashboard Phase 2 — embedded static UI
- Embed HTML/CSS/JS via `go:embed`
- Single-page app with chat, tasks, skills, logs views

### Known issues
- See [docs/CODEBASE_AUDIT_ISSUES.md](docs/CODEBASE_AUDIT_ISSUES.md) for 28 findings (1 P0, 12 P1, 15 P2)

## Decisions

- **Browser automation (NG-001)**: Non-goal per SPEC. Reconsidered only on user demand.
- **Distributed clustering**: Out of scope for v0.1/v0.2.
- **WASM compiler**: `tinygo` stays as external `$PATH` dependency. Pre-compiled `.wasm` loads without it.
- **SKILL.md format**: Canonical Agent Skills spec (YAML frontmatter + markdown body) with V1 plain YAML backward compat.
