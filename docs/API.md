# GoClaw API Reference

GoClaw exposes a gateway on `127.0.0.1:18789` (configurable via `bind_addr` in config.yaml).

## Authentication

Authentication is disabled by default. When enabled via config:

```yaml
gateway:
  auth:
    enabled: true
    keys:
      - key: "your-api-key"
```

Provide the key via:
- `Authorization: Bearer <key>` header
- `X-API-Key: <key>` header
- `?api_key=<key>` query parameter

## WebSocket — JSON-RPC

**Endpoint**: `ws://127.0.0.1:18789/ws`

The primary interface uses JSON-RPC 2.0 over WebSocket.

### Connection

```bash
# Using websocat
websocat ws://127.0.0.1:18789/ws
```

### Hello/Version Negotiation

On connect, the server sends a hello message:

```json
{"jsonrpc": "2.0", "method": "system.hello", "params": {"version": "v0.5-dev"}}
```

### Methods

#### `agent.chat` — Send a chat message

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "agent.chat",
  "params": {
    "session_id": "uuid-here",
    "content": "Hello, what can you do?"
  }
}
```

Response includes the task ID and agent response.

#### `system.status` — Get system status

```json
{"jsonrpc": "2.0", "id": 1, "method": "system.status"}
```

#### `agent.subscribe` — Subscribe to agent events

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "agent.subscribe",
  "params": {"session_id": "uuid-here"}
}
```

Receives streaming events as the agent processes tasks.

### Agent Routing

Include `@agentid` prefix in chat content to route to a specific agent:

```json
{"content": "@coder Write a hello world program"}
```

## REST Endpoints

### `POST /v1/chat/completions` — OpenAI-Compatible

Drop-in replacement for OpenAI chat completions.

```bash
curl -X POST http://127.0.0.1:18789/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "gemini-2.5-flash",
    "messages": [{"role": "user", "content": "Hello"}]
  }'
```

Response follows the OpenAI format:

```json
{
  "id": "chatcmpl-...",
  "object": "chat.completion",
  "choices": [{
    "index": 0,
    "message": {"role": "assistant", "content": "..."},
    "finish_reason": "stop"
  }]
}
```

### `GET /v1/models` — List Models

Returns available models in OpenAI format.

### `GET /api/v1/task/stream` — SSE Streaming

Stream task progress via Server-Sent Events.

```bash
curl -N "http://127.0.0.1:18789/api/v1/task/stream?task_id=<uuid>"
```

Events:
- `token` — Incremental token from LLM
- `status` — Task status change
- `done` — Task complete

### `GET /healthz` — Health Check

```bash
curl http://127.0.0.1:18789/healthz
```

Returns JSON with component status:

```json
{
  "status": "ok",
  "uptime_seconds": 3600,
  "workers": {"active": 1, "total": 3},
  "db": "ok"
}
```

### `GET /metrics` — Metrics

Returns JSON metrics including task counts, queue depth, bus stats, and WASM memory.

### `GET /metrics/prometheus` — Prometheus Metrics

Returns metrics in Prometheus text format.

### `GET /.well-known/agent.json` — A2A Agent Card

Returns the Agent-to-Agent protocol discovery card.

### REST API — Internal

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/tasks` | GET | List tasks |
| `/api/tasks/{id}` | GET | Get task by ID |
| `/api/sessions` | GET | List sessions |
| `/api/sessions/{id}` | GET | Get session messages |
| `/api/skills` | GET | List skills |
| `/api/config` | GET | Get configuration |
| `/api/plans` | GET | List plans |
| `/api/plans/{name}/execute` | POST | Execute a plan (returns 202) |

## Rate Limiting

When enabled, rate limiting uses a token bucket algorithm with per-key isolation:

```yaml
gateway:
  rate_limit:
    enabled: true
    requests_per_second: 10
    burst: 20
```

## CORS

Configure allowed origins for browser access:

```yaml
gateway:
  cors:
    allowed_origins:
      - "http://localhost:3000"
```
