# Troubleshooting GoClaw

## Port 18789 Already in Use

**Symptom**: `bind: address already in use` on startup.

**Cause**: A previous GoClaw daemon is still running.

**Fix**:
```bash
lsof -ti :18789 | xargs kill
```

Then restart GoClaw.

## Database Locked

**Symptom**: `database is locked` errors in logs.

**Cause**: Another process has the SQLite database open, or a stale lock exists.

**Fix**:
1. Check for stale daemon processes:
   ```bash
   ps aux | grep goclaw
   ```
2. Kill any stale processes, then restart.
3. If the issue persists, force a WAL checkpoint:
   ```bash
   sqlite3 ~/.goclaw/goclaw.db "PRAGMA wal_checkpoint(TRUNCATE);"
   ```

## API Key Errors

### Gemini / Google AI

**Symptom**: `GEMINI_API_KEY not set` or 401/403 from Gemini API.

**Fix**:
1. Get a key from [Google AI Studio](https://aistudio.google.com/apikey)
2. Set in environment: `export GEMINI_API_KEY=your-key`
3. Or set in config: `/config set gemini_api_key <key>`

### Brave Search

**Symptom**: Web search returns no results or 401.

**Fix**:
1. Get a key from [Brave Search API](https://brave.com/search/api/)
2. Set: `export BRAVE_API_KEY=your-key`
3. Or in config.yaml: `api_keys: { brave_search: "your-key" }`
4. DuckDuckGo is always available as fallback (no key needed)

### Ollama (Local Models)

**Symptom**: Connection refused to Ollama.

**Fix**:
1. Install Ollama: https://ollama.ai
2. Start the server: `ollama serve`
3. Pull a model: `ollama pull qwen3:8b`
4. Set provider in config:
   ```yaml
   llm:
     provider: "ollama"
   providers:
     ollama:
       base_url: "http://localhost:11434"
   ```

## Schema Migration on First Run

**Symptom**: Slow first startup (a few seconds).

**Cause**: GoClaw runs schema migrations on the SQLite database. This is normal and happens once.

**What to expect**: Migrations run incrementally (v1 through v14). Each migration adds tables, columns, or indexes. The database is ready once you see the TUI prompt or `startup phase: ready` in logs.

## Startup Self-Check Failures

**Symptom**: `goclaw doctor` reports failures.

Run diagnostics:
```bash
goclaw doctor
goclaw doctor -json   # machine-readable output
```

Common issues:
- **No config.yaml**: Run `goclaw` interactively to trigger the genesis wizard
- **Missing SOUL.md**: Will be created by the genesis wizard
- **DB corruption**: Delete `~/.goclaw/goclaw.db` and restart (loses task history)
- **Permission errors**: Check that `~/.goclaw/` is writable

## TUI Not Starting

**Symptom**: Blank screen or immediate exit.

**Possible causes**:
1. `GOCLAW_NO_TUI=1` is set in environment — unset it
2. Terminal doesn't support Bubbletea — try a different terminal emulator
3. Pipe or redirect on stdout — GoClaw detects non-TTY and skips TUI

## Agent Not Responding

**Symptom**: Messages sent but no response.

**Check**:
1. Is the LLM provider configured? `/config list`
2. Is the API key valid? Check logs: `just logs-recent`
3. Are workers available? Check `/healthz` or `goclaw status`
4. Is the queue full? Check `max_queue_depth` in config

## Tasks Stuck in RUNNING State

**Symptom**: Tasks never complete.

**Cause**: Worker may have crashed or the LLM call timed out.

**Fix**:
1. Check `task_timeout_seconds` in config (default: 120s)
2. Restart the daemon — stuck tasks will be recovered
3. Check logs for LLM errors: `just logs-recent`

## Reset Everything

If all else fails, reset to a clean state:

```bash
just reset   # removes ~/.goclaw (asks for confirmation)
just run     # starts fresh with genesis wizard
```

Or for a complete clean:

```bash
just prune   # kills daemon + removes all data + build artifacts
```
