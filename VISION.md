# GoClaw Vision

**The Ollama of multi-agent AI.**

One binary. Your terminal. Multiple AI agents that talk to each other and work together.

---

## What GoClaw Is

GoClaw is a local-first, single-binary runtime for running and orchestrating multiple AI agents from your terminal. You configure specialist agents in YAML, interact with them through a TUI or API, and they collaborate on your behalf — delegating tasks, sharing context, and executing multi-step workflows.

It is not a framework you import into your code. It is not a cloud platform. It is not a Python library with 47 dependencies. It is a tool you install and run.

```
brew install goclaw   # (eventual goal)
goclaw                # that's it
```

## Why This Exists

The multi-agent AI landscape in 2026 is crowded and Python-dominated. LangGraph, CrewAI, AutoGen, Google ADK, and OpenAI's Agents SDK are all capable frameworks. But they all share the same deployment model: import a library, wire up external services (Redis, PostgreSQL, a web server), build your own interface, manage a Python environment.

For a developer who wants to run three AI agents that can hand off work to each other, this is like needing to deploy Kubernetes to run a shell script.

GoClaw takes the Ollama approach. Ollama didn't compete with vLLM on inference performance — it won by making local LLM serving trivially simple. GoClaw does the same for multi-agent orchestration:

- **Single binary.** No Python, no Node, no Docker. Download and run.
- **Local-first.** Everything runs on your machine. SQLite for state. No cloud required.
- **TUI-native.** Your terminal is the interface. Not a browser tab, not a Jupyter notebook.
- **YAML-configured.** Define agents in a config file, not in code.
- **Provider-agnostic.** Anthropic, OpenAI, Google, Ollama — use whatever models you want.

## Who This Is For

- **Developers who live in terminals.** If you use tmux, neovim, lazygit, and k9s, GoClaw fits your workflow.
- **Go developers** underserved by the Python-dominated agent ecosystem.
- **Solo developers and small teams** who want multi-agent power without deploying infrastructure.
- **Privacy-conscious users** who want everything local and auditable.
- **Anyone tired of context-switching** between ChatGPT for writing, Claude for code, and a separate tool for research — who wants persistent, specialized agents in one place.

## The Four Interaction Patterns

GoClaw supports a spectrum of human-AI collaboration, from simple to autonomous. You use whichever pattern fits the task.

### Pattern 1: Direct Chat (works today)

Talk to a specialist agent directly. Each agent has its own system prompt, model, and personality. Switch between them like switching terminal tabs.

```
> /agent code
You: Add pagination to the /users endpoint
Code Agent: I'll add cursor-based pagination...

> /agent docs
You: Update the API docs for /users pagination
Docs Agent: Here's the updated documentation...
```

**Value over ChatGPT/Claude:** Persistent specialization. Each agent already knows its domain. No re-explaining context every conversation.

### Pattern 2: Agent Delegation (building now)

An agent recognizes it needs help from another specialist and delegates automatically. You see the delegation happening and can intervene.

```
You: Refactor the payment module
Code Agent: I'll restructure the payment handlers...
            ⏳ Delegated to Security Agent: "Review PCI compliance"
            ⏳ Delegated to Test Agent: "Generate test cases"
Security Agent: Found 2 issues — card data logged in debug mode...
Code Agent: Fixed. Here's the complete refactored module with
            security fixes and test coverage.
```

**Value:** One prompt triggers a multi-specialist workflow. The human stays in control but doesn't have to orchestrate manually.

### Pattern 3: Team Workflows (next milestone)

Repeatable multi-agent pipelines defined as data. You trigger a named plan; agents execute in the right order, passing results between steps.

```yaml
# In config.yaml
plans:
  content-pipeline:
    steps:
      - id: research
        agent: researcher
        input: "{user_input}"
      - id: write
        agent: writer
        input: "{research.output}"
        depends_on: [research]
      - id: review
        agent: editor
        input: "{write.output}"
        depends_on: [write]
```

```
> /plan content-pipeline "Write a blog post about our new auth system"

Plan: content-pipeline
  ✅ research    (Researcher)   4.1s  $0.003
  🔄 write       (Writer)       running...
  ⏳ review      (Editor)       waiting for: write
```

**Value:** Codified workflows. Same plan runs every time. Swap agents or models without changing the workflow. A human wrote this plan today; an LLM could generate it tomorrow.

### Pattern 4: Goal-Oriented Execution (future)

You state a goal. An agent decomposes it into a plan, selects agents by capability, and executes. This is not science fiction — Claude Code does this today for coding tasks. The runtime just needs to provide the right primitives.

```
You: Review PR #247 for security, performance, and correctness
System: Creating plan...
  → security-agent: scan for vulnerabilities
  → perf-agent: benchmark critical paths
  → code-agent: review logic and edge cases
  → (parallel execution, results aggregated)
```

**When this becomes real:** When the delegation contract (Pattern 2), capability registry, shared context, and DAG executor (Pattern 3) are solid. Pattern 4 is not a separate feature — it's Patterns 2 + 3 with an LLM generating the plan instead of a human.

## Architecture Principles

### Plans are data, not code

Workflows are defined as JSON/YAML task graphs, not hardcoded Go functions. This means:
- Humans can author plans in config files today
- LLMs can generate plans via tool calls tomorrow
- The executor doesn't care who authored the plan

### Agents declare capabilities

Agents aren't just names — they have queryable capabilities. Delegation can target a specific agent or request a capability and let the runtime route to the right specialist.

```yaml
agents:
  - name: security-reviewer
    capabilities: [code-review, security, go, python]
  - name: technical-writer
    capabilities: [documentation, markdown, explanation]
```

### Everything is a task tree

Every piece of work — a chat message, a delegation, a plan step — is a task with an optional parent. Task trees let you trace "who asked for this and why" from any leaf back to the original user request.

### Durable by default

SQLite stores everything: task state, agent activity, shared context, plan execution history. Kill the process mid-task, restart, and the state is intact. No Redis, no Postgres, no external dependencies.

### The human stays in the loop

GoClaw is not an autonomous agent swarm. The user can see what every agent is doing, cancel delegations, approve plan steps, and intervene at any point. Autonomy is earned incrementally, not assumed.

## What GoClaw Is NOT

- **Not a LangGraph competitor.** LangGraph is a more powerful orchestration library for developers building custom applications. GoClaw is a ready-to-use tool for developers who want multi-agent workflows without building an application.
- **Not an AutoGPT-style autonomous agent.** Those try to be fully autonomous and fail because LLMs can't plan reliably in open-ended domains. GoClaw keeps the human in control.
- **Not a workflow engine.** Temporal and Airflow orchestrate code. GoClaw orchestrates LLM agents. The DAG executor is intentionally simple because the hard part isn't scheduling — it's the AI doing useful work.
- **Not a web application.** The TUI is the primary interface. A WebSocket API exists for programmatic access and integration, not as a web UI.

## Competitive Positioning

| | GoClaw | LangGraph | CrewAI | Google ADK | n8n/Dify |
|---|---|---|---|---|---|
| Form factor | Binary + TUI | Python library | Python library | Multi-lang library | Web platform |
| Deploy | `./goclaw` | pip + Redis + app | pip + app | pip/go + app | Docker + web server |
| Multi-agent | Core feature | Core feature | Core feature | Core feature | Partial |
| Interactive chat | TUI built-in | Build your own | Build your own | Dev web UI | Web UI |
| Automated workflows | Plan-as-data | Graph compiler | Crews/Flows | Workflow agents | Visual builder |
| State | SQLite (local) | Checkpointer (pluggable) | Memory (pluggable) | Sessions (pluggable) | PostgreSQL |
| Target user | Terminal developers | App builders | Rapid prototypers | Cloud developers | No-code/low-code |

The gap GoClaw fills: **no existing tool combines interactive multi-agent chat with automated workflows in a single-binary, terminal-native runtime.**

## Roadmap

### v0.1 — Solid Foundation (current)
- ✅ Multi-agent TUI with agent switching
- ✅ Multi-provider support (OpenAI, Gemini, Anthropic, Ollama)
- ✅ SQLite persistence, WebSocket API
- ✅ Inter-agent messaging (sync)
- ✅ Policy enforcement, WASM skills

### v0.2 — Delegation Done Right
- Async delegation with structured request/response
- Agent capability manifest and discovery
- Shared context store per task tree
- Task parent/child relationships
- Delegation visibility in TUI (status, navigation, cancellation)

### v0.3 — Team Workflows
- Plan-as-data format (YAML/JSON task graphs)
- DAG executor with dependency resolution and parallel execution
- `/plan` command to trigger named workflows
- Plan progress view in TUI
- Human-in-the-loop approval gates

### v0.4 — Interoperability & Ecosystem
- MCP client support (universal tool integration)
- A2A protocol support (cross-framework agent communication)
- Analytics API for external dashboards
- Plugin system for custom tools and integrations

### v1.0 — The Swiss Army Knife
- All four interaction patterns working reliably
- LLM-generated plans (Pattern 4 via tool calls)
- Stable API, documented configuration, tested at scale
- Community-contributed agent templates and plan libraries

### Future (no timeline)
- Web UI option alongside TUI
- Distributed execution across machines
- Agent marketplace
- Fine-tuned models for planning and evaluation

## The Bet

GoClaw bets that:

1. **Multi-agent is going mainstream.** As LLMs improve, single-agent interactions become insufficient for complex tasks. Delegation, specialization, and coordination become standard patterns.

2. **Deployment simplicity wins.** Ollama proved it for model serving. The same principle applies to agent orchestration. Most developers don't need LangGraph's full power — they need something that works in 30 seconds.

3. **The terminal is the right interface for agent work.** Agents are collaborators in a development workflow, not web apps you visit. They belong next to your editor, your git client, and your monitoring tools.

4. **Plans-as-data is the right abstraction.** When you separate "what to do" (the plan) from "how to do it" (the executor) from "who decides" (human or LLM), you get a system that naturally evolves as LLMs improve — without rewriting infrastructure.

5. **Local-first, durable state matters.** When agents handle real work — code reviews, document generation, research — you need to trust that state survives crashes, restarts, and network failures. SQLite delivers this without operational overhead.

If these bets are right, GoClaw becomes the default way developers interact with AI agent teams from their terminal. If they're wrong, it's still a useful tool for anyone who wants persistent, specialized AI agents without deploying a platform.

---

*GoClaw is open source and compatible with OpenClaw. Built in Go. Ships as a single binary.*

