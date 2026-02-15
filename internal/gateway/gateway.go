package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/agent"
	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/config"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/shared"
	"github.com/basket/go-claw/internal/tools"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/google/uuid"
)

const (
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInternal       = -32603

	// Stable app error taxonomy.
	ErrCodeInvalid      = 1000
	ErrCodeBackpressure = 4290 // GC-SPEC-QUE-008: queue saturated
	ErrCodeLLM          = 4000

	maxReplayEventsPerSubscribe = 64
)

type Config struct {
	Store    *persistence.Store
	Registry *agent.Registry
	Policy   policy.Checker
	Bus      *bus.Bus

	AuthToken string

	// AllowOrigins controls accepted Origin headers for browser WS connections (GC-SPEC-ACP-004).
	// Empty list means "same-origin only" (no cross-origin WebSockets).
	AllowOrigins []string

	// ConfigFingerprint is the hash of active config exposed in system.status (GC-SPEC-CFG-005).
	ConfigFingerprint string

	// ApprovalTimeout is the duration after which unanswered approval requests
	// default to deny (GC-SPEC-SEC-008). Zero means 60s.
	ApprovalTimeout time.Duration

	// GC-SPEC-TUI-001: Config/policy mutation for ACP parity with TUI.
	LivePolicy *policy.LivePolicy // nil = config mutations unavailable
	HomeDir    string
	Cfg        *config.Config

	ToolsUpdated <-chan string
	TinygoStatus func() (available bool, detail string)

	// SkillsStatus returns the current skill catalog status for system.status.
	// It should be safe to call concurrently.
	SkillsStatus func(ctx context.Context) ([]tools.SkillStatus, error)

	// Plans holds configured workflow plans (GC-SPEC-PDR-v4-Phase-4).
	Plans map[string]PlanSummary
}

type Server struct {
	cfg Config

	clientsMu sync.RWMutex
	clients   map[*client]struct{}

	approvalsMu sync.Mutex
	approvals   map[string]*approvalRequest

}

type client struct {
	conn       *websocket.Conn
	mu         sync.Mutex
	handshaken bool

	// Event subscription state for session.events.subscribe.
	subMu         sync.Mutex
	subscribedSes map[string]int64 // session_id → last forwarded event_id
	busSub        *bus.Subscription
	busCancel     context.CancelFunc
}

type approvalRequest struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Details   string    `json:"details,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	done      chan struct{}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id,omitempty"`
	Result  any         `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
	Method  string      `json:"method,omitempty"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func New(cfg Config) *Server {
	s := &Server{
		cfg:       cfg,
		clients:   map[*client]struct{}{},
		approvals: map[string]*approvalRequest{},
	}
	if cfg.ToolsUpdated != nil {
		go s.consumeToolEvents(cfg.ToolsUpdated)
	}
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/metrics/prometheus", s.handlePrometheusMetrics)
	mux.HandleFunc("/healthz", s.handleHealthz)
	// REST API endpoints.
	mux.HandleFunc("/api/tasks", s.handleAPITasks)
	mux.HandleFunc("/api/tasks/", s.handleAPITaskByID)
	mux.HandleFunc("/api/sessions", s.handleAPISessions)
	mux.HandleFunc("/api/sessions/", s.handleAPISessionMessages)
	mux.HandleFunc("/api/skills", s.handleAPISkills)
	mux.HandleFunc("/api/config", s.handleAPIConfig)
	mux.HandleFunc("/api/plans", s.handleAPIPlans)

	// OpenAI-compatible endpoints
	mux.HandleFunc("/v1/chat/completions", s.handleOpenAIChatCompletion)
	mux.HandleFunc("/v1/models", s.handleOpenAIModels)

	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	ctx := context.Background()
	dbOK := true
	if _, _, err := s.cfg.Store.TaskCounts(ctx); err != nil {
		dbOK = false
	}
	policyVersion := ""
	if s.cfg.Policy != nil {
		policyVersion = s.cfg.Policy.PolicyVersion()
	}
	tinygoAvailable := false
	tinygoDetail := "unknown"
	if s.cfg.TinygoStatus != nil {
		tinygoAvailable, tinygoDetail = s.cfg.TinygoStatus()
	}

	var replayBacklog int64
	if eventCount, err := s.cfg.Store.TotalEventCount(ctx); err == nil {
		replayBacklog = eventCount
	}

	agentCount := len(s.cfg.Registry.ListRunningAgents())

	payload := map[string]any{
		"healthy":               dbOK,
		"db_ok":                 dbOK,
		"policy_version":        policyVersion,
		"skill_runtime":         tinygoAvailable,
		"skill_detail":          tinygoDetail,
		"replay_backlog_events": replayBacklog,
		"agent_count":           agentCount,
	}
	w.Header().Set("Content-Type", "application/json")
	if !dbOK {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	// GC-SPEC-ACP-003: keep local auth semantics consistent with ACP endpoints.
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := context.Background()
	mc, _ := s.cfg.Store.MetricsCounts(ctx)
	mem := &runtime.MemStats{}
	runtime.ReadMemStats(mem)

	// Aggregate active tasks from all agents; build per-agent breakdown.
	var activeLanes int32
	runningAgents := s.cfg.Registry.ListRunningAgents()
	perAgent := make([]map[string]any, 0, len(runningAgents))
	for _, a := range runningAgents {
		st := a.Engine.Status()
		activeLanes += st.ActiveTasks
		perAgent = append(perAgent, map[string]any{
			"agent_id":     st.AgentID,
			"active_tasks": st.ActiveTasks,
			"worker_count": st.WorkerCount,
		})
	}

	// Inter-agent message count.
	var agentMsgCount int64
	if c, err := s.cfg.Store.TotalAgentMessageCount(ctx); err == nil {
		agentMsgCount = c
	}

	var delegationCount int64
	if c, err := s.cfg.Store.TotalDelegationCount(ctx); err == nil {
		delegationCount = c
	}

	payload := map[string]any{
		"pending_tasks":        mc.Pending,
		"running_tasks":        mc.Running,
		"active_lanes":         activeLanes,
		"lease_expiries":       mc.LeaseExpiries,
		"retries":              mc.RetryWait,
		"dlq_size":             mc.DeadLetter,
		"policy_deny_rate":     audit.DenyCount(),
		"alloc_bytes":          mem.Alloc,
		"agent_count":          len(runningAgents),
		"agent_messages_total": agentMsgCount,
		"delegations_total":    delegationCount,
		"agents":               perAgent,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *Server) handlePrometheusMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx := context.Background()
	mc, _ := s.cfg.Store.MetricsCounts(ctx)
	mem := &runtime.MemStats{}
	runtime.ReadMemStats(mem)

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP goclaw_pending_tasks Number of pending tasks in queue.\n")
	fmt.Fprintf(w, "# TYPE goclaw_pending_tasks gauge\n")
	fmt.Fprintf(w, "goclaw_pending_tasks %d\n", mc.Pending)
	fmt.Fprintf(w, "# HELP goclaw_running_tasks Number of currently running tasks.\n")
	fmt.Fprintf(w, "# TYPE goclaw_running_tasks gauge\n")
	fmt.Fprintf(w, "goclaw_running_tasks %d\n", mc.Running)
	fmt.Fprintf(w, "# HELP goclaw_active_lanes Number of active worker lanes.\n")
	fmt.Fprintf(w, "# TYPE goclaw_active_lanes gauge\n")
	var activeLanes int32
	for _, a := range s.cfg.Registry.ListRunningAgents() {
		activeLanes += a.Engine.Status().ActiveTasks
	}
	fmt.Fprintf(w, "goclaw_active_lanes %d\n", activeLanes)
	fmt.Fprintf(w, "# HELP goclaw_lease_expiries Total number of lease expiries.\n")
	fmt.Fprintf(w, "# TYPE goclaw_lease_expiries gauge\n")
	fmt.Fprintf(w, "goclaw_lease_expiries %d\n", mc.LeaseExpiries)
	fmt.Fprintf(w, "# HELP goclaw_retries Number of tasks in retry-wait state.\n")
	fmt.Fprintf(w, "# TYPE goclaw_retries gauge\n")
	fmt.Fprintf(w, "goclaw_retries %d\n", mc.RetryWait)
	fmt.Fprintf(w, "# HELP goclaw_dlq_size Number of tasks in dead letter queue.\n")
	fmt.Fprintf(w, "# TYPE goclaw_dlq_size gauge\n")
	fmt.Fprintf(w, "goclaw_dlq_size %d\n", mc.DeadLetter)
	fmt.Fprintf(w, "# HELP goclaw_policy_deny_total Total policy deny count.\n")
	fmt.Fprintf(w, "# TYPE goclaw_policy_deny_total counter\n")
	fmt.Fprintf(w, "goclaw_policy_deny_total %d\n", audit.DenyCount())
	fmt.Fprintf(w, "# HELP goclaw_alloc_bytes Current allocated memory in bytes.\n")
	fmt.Fprintf(w, "# TYPE goclaw_alloc_bytes gauge\n")
	fmt.Fprintf(w, "goclaw_alloc_bytes %d\n", mem.Alloc)
	runningAgents := s.cfg.Registry.ListRunningAgents()
	fmt.Fprintf(w, "# HELP goclaw_agent_count Number of active agents.\n")
	fmt.Fprintf(w, "# TYPE goclaw_agent_count gauge\n")
	fmt.Fprintf(w, "goclaw_agent_count %d\n", len(runningAgents))
	fmt.Fprintf(w, "# HELP goclaw_agent_active_tasks Active tasks per agent.\n")
	fmt.Fprintf(w, "# TYPE goclaw_agent_active_tasks gauge\n")
	for _, a := range runningAgents {
		st := a.Engine.Status()
		fmt.Fprintf(w, "goclaw_agent_active_tasks{agent_id=%q} %d\n", st.AgentID, st.ActiveTasks)
	}
	if msgCount, err := s.cfg.Store.TotalAgentMessageCount(ctx); err == nil {
		fmt.Fprintf(w, "# HELP goclaw_agent_messages_total Total inter-agent messages.\n")
		fmt.Fprintf(w, "# TYPE goclaw_agent_messages_total counter\n")
		fmt.Fprintf(w, "goclaw_agent_messages_total %d\n", msgCount)
	}
	if delegations, err := s.cfg.Store.TotalDelegationCount(ctx); err == nil {
		fmt.Fprintf(w, "# HELP goclaw_delegations_total Total delegate_task invocations.\n")
		fmt.Fprintf(w, "# TYPE goclaw_delegations_total counter\n")
		fmt.Fprintf(w, "goclaw_delegations_total %d\n", delegations)
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// GC-SPEC-ACP-004: enforce explicit origin allowlist for cross-origin requests.
		// Same-origin requests are always allowed by the websocket library.
		OriginPatterns: s.cfg.AllowOrigins,
	})
	if err != nil {
		return
	}
	c := &client{conn: conn}
	s.addClient(c)
	slog.Info("ws: client connected")
	defer func() {
		s.removeClient(c)
		slog.Info("ws: client disconnecting")
		_ = conn.Close(websocket.StatusNormalClosure, "bye")
	}()

	for {
		var req rpcRequest
		if err := wsjson.Read(r.Context(), conn, &req); err != nil {
			slog.Error("ws: read error, closing", "error", err)
			return
		}
		slog.Info("ws: request", "method", req.Method, "id", string(req.ID))
		resp := s.handleRPC(r.Context(), c, req)
		if resp == nil {
			continue
		}
		if err := c.write(r.Context(), resp); err != nil {
			slog.Error("ws: write response error", "method", req.Method, "error", err)
		}
	}
}

func (s *Server) authorize(r *http.Request) bool {
	if s.cfg.AuthToken == "" {
		return false
	}
	authz := strings.TrimSpace(r.Header.Get("Authorization"))
	if authz == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authz, prefix) {
		return false
	}
	token := strings.TrimSpace(strings.TrimPrefix(authz, prefix))
	return token != "" && token == s.cfg.AuthToken
}

func isMutatingMethod(method string) bool {
	switch method {
	case "agent.chat", "agent.chat.stream", "agent.abort", "session.purge",
		"agent.create", "agent.remove":
		return true
	default:
		return false
	}
}

func requiredCapabilityForMethod(method string) string {
	switch method {
	case "agent.chat", "agent.chat.stream", "agent.abort", "approval.request", "approval.respond", "session.purge":
		return "acp.mutate"
	case "session.history", "session.list", "session.events.subscribe", "system.status", "approval.list",
		"cron.list", "subtask.list", "agent.list", "agent.status", "incident.export",
		"config.list":
		return "acp.read"
	case "cron.add", "cron.remove", "cron.enable", "cron.disable", "subtask.create",
		"agent.create", "agent.remove",
		"config.set", "config.model.set", "policy.domain.add":
		return "acp.mutate"
	default:
		return ""
	}
}

func (s *Server) handleRPC(ctx context.Context, c *client, req rpcRequest) *rpcResponse {
	id, hasID := decodeID(req.ID)
	if req.JSONRPC != "2.0" || req.Method == "" {
		if !hasID {
			return nil
		}
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: ErrCodeInvalidRequest, Message: "invalid JSON-RPC request"},
		}
	}
	if isMutatingMethod(req.Method) && !c.isHandshaken() {
		if !hasID {
			return nil
		}
		return &rpcResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: ErrCodeInvalidRequest, Message: "system.hello required before mutating calls"},
		}
	}
	if capability := requiredCapabilityForMethod(req.Method); capability != "" {
		if s.cfg.Policy == nil || !s.cfg.Policy.AllowCapability(capability) {
			policyVersion := ""
			if s.cfg.Policy != nil {
				policyVersion = s.cfg.Policy.PolicyVersion()
			}
			audit.Record("deny", capability, "missing_capability", policyVersion, req.Method)
			if !hasID {
				return nil
			}
			return &rpcResponse{
				JSONRPC: "2.0",
				ID:      id,
				Error:   &rpcError{Code: ErrCodeInvalid, Message: fmt.Sprintf("policy denied capability %q", capability)},
			}
		}
		audit.Record("allow", capability, "capability_granted", s.cfg.Policy.PolicyVersion(), req.Method)
	}

	var result any
	var rpcErr *rpcError

	switch req.Method {
	case "system.hello":
		c.markHandshaken()
		result = map[string]any{
			"protocol":      "acp",
			"version":       "1.0",
			"supported_min": "1.0",
			"supported_max": "1.0",
		}
	case "agent.chat":
		var p struct {
			SessionID string `json:"session_id"`
			Content   string `json:"content"`
			Text      string `json:"text"`     // GC-SPEC-ACP-009: OpenClaw backward-compat alias.
			AgentID   string `json:"agent_id"` // Optional, defaults to "default".
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		// GC-SPEC-ACP-009: Accept "text" as alias for "content".
		if p.Content == "" && p.Text != "" {
			p.Content = p.Text
		}
		if _, err := uuid.Parse(p.SessionID); err != nil || p.Content == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "session_id must be uuid and content must be non-empty"}
			break
		}
		agentID := p.AgentID
		if agentID == "" {
			agentID = shared.DefaultAgentID
		}
		// GC-SPEC-RUN-004: Generate trace_id for this request.
		traceID := shared.NewTraceID()
		traceCtx := shared.WithTraceID(ctx, traceID)
		taskID, err := s.cfg.Registry.CreateChatTask(traceCtx, agentID, p.SessionID, p.Content)
		if err != nil {
			if errors.Is(err, engine.ErrQueueSaturated) {
				rpcErr = &rpcError{Code: ErrCodeBackpressure, Message: "queue saturated; retry later"}
			} else {
				rpcErr = &rpcError{Code: ErrCodeLLM, Message: err.Error()}
			}
			break
		}
		slog.Info("ws: agent.chat task created", "task_id", taskID, "agent_id", agentID, "session_id", p.SessionID, "trace_id", traceID)
		result = map[string]any{"task_id": taskID}
	case "agent.chat.stream":
		var p struct {
			SessionID string `json:"session_id"`
			Content   string `json:"content"`
			Text      string `json:"text"`     // GC-SPEC-ACP-009: OpenClaw backward-compat alias.
			AgentID   string `json:"agent_id"` // Optional, defaults to "default".
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		// GC-SPEC-ACP-009: Accept "text" as alias for "content".
		if p.Content == "" && p.Text != "" {
			p.Content = p.Text
		}
		if _, err := uuid.Parse(p.SessionID); err != nil || p.Content == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "session_id must be uuid and content must be non-empty"}
			break
		}
		agentID := p.AgentID
		if agentID == "" {
			agentID = shared.DefaultAgentID
		}
		// Generate trace_id for this request.
		streamTraceID := shared.NewTraceID()
		traceCtx := shared.WithTraceID(ctx, streamTraceID)
		streamTaskID, err := s.cfg.Registry.StreamChatTask(traceCtx, agentID, p.SessionID, p.Content, func(content string) error {
			// Stream chunks back to client as notifications.
			// task_id is sent in the initial RPC response, not in every chunk,
			// to avoid a closure race with the return value assignment.
			return c.write(ctx, rpcResponse{
				JSONRPC: "2.0",
				Method:  "agent.chat.stream",
				Params:  map[string]any{"content": content},
			})
		})
		if err != nil {
			if errors.Is(err, engine.ErrQueueSaturated) {
				rpcErr = &rpcError{Code: ErrCodeBackpressure, Message: "queue saturated; retry later"}
			} else {
				rpcErr = &rpcError{Code: ErrCodeLLM, Message: err.Error()}
			}
			break
		}
		slog.Info("ws: agent.chat.stream task created", "task_id", streamTaskID, "agent_id", agentID, "session_id", p.SessionID, "trace_id", streamTraceID)
		result = map[string]any{"task_id": streamTaskID}
	case "agent.abort":
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.TaskID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		ok, err := s.cfg.Registry.AbortTask(ctx, p.TaskID)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"aborted": ok}
	case "session.history":
		var p struct {
			SessionID string `json:"session_id"`
			Limit     int    `json:"limit"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		if p.Limit <= 0 || p.Limit > 100 {
			p.Limit = 100
		}
		items, err := s.cfg.Store.ListHistory(ctx, p.SessionID, "", p.Limit)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"items": items}
	case "session.events.subscribe":
		var p struct {
			SessionID   string `json:"session_id"`
			FromEventID int64  `json:"from_event_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		minEventID, maxEventID, err := s.cfg.Store.TaskEventBounds(ctx, p.SessionID)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		if p.FromEventID > 0 && minEventID > 0 && p.FromEventID < (minEventID-1) {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "replay_gap"}
			break
		}
		events, err := s.cfg.Store.ListTaskEventsFrom(ctx, p.SessionID, p.FromEventID, 1000)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		slog.Info("ws: subscribe replay", "session", p.SessionID, "events", len(events), "min", minEventID, "max", maxEventID)
		if len(events) > maxReplayEventsPerSubscribe {
			_ = c.write(ctx, rpcResponse{
				JSONRPC: "2.0",
				Method:  "system.backpressure",
				Params: map[string]any{
					"session_id": p.SessionID,
					"reason":     "replay_window_too_large",
					"max_events": maxReplayEventsPerSubscribe,
					"replayed":   len(events),
				},
			})
			_ = c.conn.Close(websocket.StatusPolicyViolation, "backpressure")
			return nil
		}
		for _, event := range events {
			_ = c.write(ctx, rpcResponse{
				JSONRPC: "2.0",
				Method:  "session.event",
				Params: map[string]any{
					"event_id":   event.EventID,
					"task_id":    event.TaskID,
					"session_id": event.SessionID,
					"event_type": event.EventType,
					"state_from": event.StateFrom,
					"state_to":   event.StateTo,
					"run_id":     event.RunID,
					"trace_id":   event.TraceID,
					"payload":    event.Payload,
					"created_at": event.CreatedAt,
				},
			})
		}
		// Register for live event forwarding via the bus.
		s.subscribeClientToSession(c, p.SessionID, maxEventID)

		result = map[string]any{
			"subscribed":      true,
			"replayed":        len(events),
			"latest_event_id": maxEventID,
		}
	case "session.list":
		var p struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			p.Limit = 20
		}
		sessions, err := s.cfg.Store.ListSessions(ctx, p.Limit)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"sessions": sessions}
	case "approval.request":
		var p struct {
			Action  string `json:"action"`
			Details string `json:"details"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || strings.TrimSpace(p.Action) == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		approvalID := uuid.NewString()
		record := &approvalRequest{
			ID:        approvalID,
			Action:    strings.TrimSpace(p.Action),
			Details:   strings.TrimSpace(p.Details),
			Status:    "PENDING",
			CreatedAt: time.Now().UTC(),
			done:      make(chan struct{}),
		}
		status := record.Status
		s.approvalsMu.Lock()
		s.approvals[approvalID] = record
		s.approvalsMu.Unlock()
		s.broadcast("approval.required", map[string]any{
			"approval_id": approvalID,
			"action":      record.Action,
			"details":     record.Details,
			"status":      record.Status,
			"created_at":  record.CreatedAt,
		})
		// GC-SPEC-SEC-008: auto-deny on timeout (default 60s).
		go s.approvalTimeoutDeny(approvalID)
		result = map[string]any{
			"approval_id": approvalID,
			"status":      status,
		}
	case "approval.respond":
		var p struct {
			ApprovalID string `json:"approval_id"`
			Decision   string `json:"decision"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.ApprovalID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params"}
			break
		}
		decision := strings.ToLower(strings.TrimSpace(p.Decision))
		if decision != "approve" && decision != "deny" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "decision must be approve or deny"}
			break
		}
		s.approvalsMu.Lock()
		record, ok := s.approvals[p.ApprovalID]
		responseApprovalID := ""
		responseStatus := ""
		if ok {
			if decision == "approve" {
				record.Status = "APPROVED"
			} else {
				record.Status = "DENIED"
			}
			responseApprovalID = record.ID
			responseStatus = record.Status
			// Signal any blocking RequestApproval caller.
			select {
			case <-record.done:
			default:
				close(record.done)
			}
		}
		s.approvalsMu.Unlock()
		if !ok {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "approval request not found"}
			break
		}
		s.broadcast("approval.updated", map[string]any{
			"approval_id": responseApprovalID,
			"status":      responseStatus,
		})
		result = map[string]any{
			"approval_id": responseApprovalID,
			"status":      responseStatus,
		}
	case "approval.list":
		s.approvalsMu.Lock()
		items := make([]map[string]any, 0, len(s.approvals))
		for _, approval := range s.approvals {
			items = append(items, map[string]any{
				"approval_id": approval.ID,
				"action":      approval.Action,
				"details":     approval.Details,
				"status":      approval.Status,
				"created_at":  approval.CreatedAt,
			})
		}
		s.approvalsMu.Unlock()
		result = map[string]any{"items": items}
	case "system.status":
		pending, running, err := s.cfg.Store.TaskCounts(ctx)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)

		// Aggregate across all agents.
		var totalActiveLanes int32
		var totalWorkerCount int
		var lastError string
		runningAgents := s.cfg.Registry.ListRunningAgents()
		for _, a := range runningAgents {
			st := a.Engine.Status()
			totalActiveLanes += st.ActiveTasks
			totalWorkerCount += st.WorkerCount
			if st.LastError != "" {
				lastError = st.LastError
			}
		}

		tinygoAvailable := false
		tinygoDetail := "tinygo status unavailable"
		if s.cfg.TinygoStatus != nil {
			tinygoAvailable, tinygoDetail = s.cfg.TinygoStatus()
		}

		var skillStatus []tools.SkillStatus
		if s.cfg.SkillsStatus != nil {
			if items, err := s.cfg.SkillsStatus(ctx); err == nil {
				skillStatus = items
			} else {
				slog.Warn("skills status unavailable", "error", err)
			}
		}

		// Per-agent breakdown.
		agentConfigs := s.cfg.Registry.ListAgents()
		agentStatuses := make([]map[string]any, 0, len(agentConfigs))
		for _, ac := range agentConfigs {
			st, _ := s.cfg.Registry.AgentStatus(ac.AgentID)
			entry := map[string]any{
				"agent_id":     ac.AgentID,
				"display_name": ac.DisplayName,
				"provider":     ac.Provider,
				"model":        ac.Model,
				"worker_count": ac.WorkerCount,
				"status":       "active",
			}
			if st != nil {
				entry["active_tasks"] = st.ActiveTasks
			}
			agentStatuses = append(agentStatuses, entry)
		}

		result = map[string]any{
			"healthy":          true,
			"db_ok":            true,
			"active_lanes":     totalActiveLanes,
			"worker_count":     totalWorkerCount,
			"queue_depth":      pending,
			"running_tasks":    running,
			"memory_alloc":     mem.Alloc,
			"config_hash":      s.cfg.ConfigFingerprint,
			"tinygo_available": tinygoAvailable,
			"tinygo_detail":    tinygoDetail,
			"skills":           skillStatus,
			"last_error":       lastError,
			"agent_count":      len(agentStatuses),
			"agents":           agentStatuses,
			"time_unix":        time.Now().Unix(),
		}
	case "session.purge":
		// GC-SPEC-DATA-006: User-triggered PII purge for a session.
		var p struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.SessionID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "invalid params: session_id required"}
			break
		}
		policyVersion := ""
		if s.cfg.Policy != nil {
			policyVersion = s.cfg.Policy.PolicyVersion()
		}
		purgeResult, err := s.cfg.Store.PurgeSessionPII(ctx, p.SessionID, policyVersion, "user")
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{
			"session_id":          p.SessionID,
			"messages_deleted":    purgeResult.MessagesDeleted,
			"tasks_tombstoned":    purgeResult.TaskPayloadsTombed,
			"events_tombstoned":   purgeResult.TaskEventsTombed,
			"redactions_recorded": purgeResult.RedactionsRecorded,
		}
	case "cron.list":
		schedules, err := s.cfg.Store.ListSchedules(ctx)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"schedules": schedules}
	case "cron.add":
		var p struct {
			Name      string `json:"name"`
			CronExpr  string `json:"cron_expr"`
			Payload   string `json:"payload"`
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" || p.CronExpr == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "name and cron_expr required"}
			break
		}
		if p.SessionID == "" {
			p.SessionID = uuid.NewString()
		}
		sched := persistence.Schedule{
			ID:        uuid.NewString(),
			Name:      p.Name,
			CronExpr:  p.CronExpr,
			Payload:   p.Payload,
			SessionID: p.SessionID,
			Enabled:   true,
		}
		if err := s.cfg.Store.InsertSchedule(ctx, sched); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"schedule_id": sched.ID}
	case "cron.remove":
		var p struct {
			ScheduleID string `json:"schedule_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.ScheduleID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "schedule_id required"}
			break
		}
		if err := s.cfg.Store.DeleteSchedule(ctx, p.ScheduleID); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"deleted": true}
	case "cron.enable":
		var p struct {
			ScheduleID string `json:"schedule_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.ScheduleID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "schedule_id required"}
			break
		}
		if err := s.cfg.Store.EnableSchedule(ctx, p.ScheduleID, true); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"enabled": true}
	case "cron.disable":
		var p struct {
			ScheduleID string `json:"schedule_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.ScheduleID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "schedule_id required"}
			break
		}
		if err := s.cfg.Store.EnableSchedule(ctx, p.ScheduleID, false); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"disabled": true}
	case "subtask.list":
		var p struct {
			ParentTaskID string `json:"parent_task_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.ParentTaskID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "parent_task_id required"}
			break
		}
		subtasks, err := s.cfg.Store.GetSubtasks(ctx, p.ParentTaskID)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"subtasks": subtasks}
	case "subtask.create":
		var p struct {
			ParentTaskID string `json:"parent_task_id"`
			SessionID    string `json:"session_id"`
			Payload      string `json:"payload"`
			Priority     int    `json:"priority"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.ParentTaskID == "" || p.Payload == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "parent_task_id and payload required"}
			break
		}
		if p.SessionID == "" {
			p.SessionID = uuid.NewString()
		}
		taskID, err := s.cfg.Store.CreateSubtask(ctx, p.ParentTaskID, p.SessionID, p.Payload, p.Priority)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"task_id": taskID}
	case "agent.create":
		var p struct {
			AgentID            string   `json:"agent_id"`
			DisplayName        string   `json:"display_name"`
			Provider           string   `json:"provider"`
			Model              string   `json:"model"`
			APIKey             string   `json:"api_key"`
			APIKeyEnv          string   `json:"api_key_env"`
			Soul               string   `json:"soul"`
			WorkerCount        int      `json:"worker_count"`
			TaskTimeoutSeconds int      `json:"task_timeout_seconds"`
			MaxQueueDepth      int      `json:"max_queue_depth"`
			SkillsFilter       []string `json:"skills_filter"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.AgentID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "agent_id is required"}
			break
		}
		cfg := agent.AgentConfig{
			AgentID:            p.AgentID,
			DisplayName:        p.DisplayName,
			Provider:           p.Provider,
			Model:              p.Model,
			APIKey:             p.APIKey,
			APIKeyEnv:          p.APIKeyEnv,
			Soul:               p.Soul,
			WorkerCount:        p.WorkerCount,
			TaskTimeoutSeconds: p.TaskTimeoutSeconds,
			MaxQueueDepth:      p.MaxQueueDepth,
			SkillsFilter:       p.SkillsFilter,
		}
		if err := s.cfg.Registry.CreateAgent(ctx, cfg); err != nil {
			slog.Warn("ws: agent.create failed", "agent_id", p.AgentID, "error", err)
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		slog.Info("ws: agent.create succeeded", "agent_id", p.AgentID, "provider", p.Provider, "model", p.Model)
		result = map[string]any{"agent_id": p.AgentID, "status": "active"}
	case "agent.remove":
		var p struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.AgentID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "agent_id is required"}
			break
		}
		if err := s.cfg.Registry.RemoveAgent(ctx, p.AgentID, 5*time.Second); err != nil {
			slog.Warn("ws: agent.remove failed", "agent_id", p.AgentID, "error", err)
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		slog.Info("ws: agent.remove succeeded", "agent_id", p.AgentID)
		result = map[string]any{"agent_id": p.AgentID, "removed": true}
	case "incident.export":
		// GC-SPEC-OBS-006: Bounded run bundle for offline debugging.
		var p struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.TaskID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "task_id is required"}
			break
		}
		bundle, err := s.cfg.Store.ExportIncident(ctx, p.TaskID, s.cfg.ConfigFingerprint)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = bundle

	// GC-SPEC-TUI-001: ACP parity — config and policy mutation methods.
	case "config.list":
		masked := make(map[string]string)
		if s.cfg.Cfg != nil {
			for k, v := range s.cfg.Cfg.APIKeys {
				if len(v) > 4 {
					masked[k] = v[:4] + "****"
				} else {
					masked[k] = "****"
				}
			}
		}
		result = map[string]any{"api_keys": masked}

	case "config.set":
		var p struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Key == "" || p.Value == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "key and value are required"}
			break
		}
		if s.cfg.HomeDir == "" {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: "home dir not configured"}
			break
		}
		if err := config.SetAPIKey(s.cfg.HomeDir, p.Key, p.Value); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		if s.cfg.Cfg != nil {
			if s.cfg.Cfg.APIKeys == nil {
				s.cfg.Cfg.APIKeys = make(map[string]string)
			}
			s.cfg.Cfg.APIKeys[p.Key] = p.Value
		}
		result = map[string]any{"key": p.Key, "saved": true}

	case "config.model.set":
		var p struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Model == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "model is required"}
			break
		}
		if s.cfg.HomeDir == "" {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: "home dir not configured"}
			break
		}
		if p.Provider == "" {
			p.Provider = "google"
		}
		if err := config.SetModel(s.cfg.HomeDir, p.Provider, p.Model); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		if s.cfg.Cfg != nil {
			s.cfg.Cfg.LLMProvider = p.Provider
			s.cfg.Cfg.GeminiModel = p.Model
		}
		result = map[string]any{"provider": p.Provider, "model": p.Model, "saved": true}

	case "policy.domain.add":
		var p struct {
			Domain string `json:"domain"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Domain == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "domain is required"}
			break
		}
		if s.cfg.LivePolicy == nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: "policy not available"}
			break
		}
		if err := s.cfg.LivePolicy.AllowDomain(p.Domain); err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{"domain": p.Domain, "allowed": true}

	case "agent.list":
		configs := s.cfg.Registry.ListAgents()
		agents := make([]map[string]any, len(configs))
		for i, c := range configs {
			st, _ := s.cfg.Registry.AgentStatus(c.AgentID)
			agentInfo := map[string]any{
				"agent_id":     c.AgentID,
				"display_name": c.DisplayName,
				"provider":     c.Provider,
				"model":        c.Model,
				"worker_count": c.WorkerCount,
				"status":       "active",
			}
			if st != nil {
				agentInfo["active_tasks"] = st.ActiveTasks
			}
			agents[i] = agentInfo
		}
		result = map[string]any{"agents": agents}
	case "agent.status":
		var p struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.AgentID == "" {
			rpcErr = &rpcError{Code: ErrCodeInvalid, Message: "agent_id is required"}
			break
		}
		st, err := s.cfg.Registry.AgentStatus(p.AgentID)
		if err != nil {
			rpcErr = &rpcError{Code: ErrCodeInternal, Message: err.Error()}
			break
		}
		result = map[string]any{
			"agent_id":     p.AgentID,
			"worker_count": st.WorkerCount,
			"active_tasks": st.ActiveTasks,
			"last_error":   st.LastError,
		}
	default:
		rpcErr = &rpcError{Code: ErrCodeMethodNotFound, Message: fmt.Sprintf("method not found: %s", req.Method)}
	}

	if !hasID {
		return nil
	}
	if rpcErr != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: id, Error: rpcErr}
	}
	return &rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func decodeID(raw json.RawMessage) (any, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, false
	}
	return generic, true
}

// ApprovalSummary is a snapshot of an approval request for TUI display (GC-SPEC-TUI-003).
type ApprovalSummary struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// PendingApprovals returns approval requests in PENDING state (GC-SPEC-TUI-003).
func (s *Server) PendingApprovals() []ApprovalSummary {
	s.approvalsMu.Lock()
	defer s.approvalsMu.Unlock()
	var out []ApprovalSummary
	for _, a := range s.approvals {
		if a.Status == "PENDING" {
			out = append(out, ApprovalSummary{
				ID:        a.ID,
				Action:    a.Action,
				Status:    a.Status,
				CreatedAt: a.CreatedAt,
			})
		}
	}
	return out
}

// RespondToApproval sets the decision on an existing approval request.
// This is used by the TUI to approve/deny without going through the WS RPC path (GC-SPEC-TUI-003).
func (s *Server) RespondToApproval(approvalID, decision string) error {
	decision = strings.ToLower(strings.TrimSpace(decision))
	if decision != "approve" && decision != "deny" {
		return fmt.Errorf("decision must be approve or deny")
	}
	s.approvalsMu.Lock()
	record, ok := s.approvals[approvalID]
	if !ok {
		s.approvalsMu.Unlock()
		return fmt.Errorf("approval request %q not found", approvalID)
	}
	if decision == "approve" {
		record.Status = "APPROVED"
	} else {
		record.Status = "DENIED"
	}
	select {
	case <-record.done:
	default:
		close(record.done)
	}
	s.approvalsMu.Unlock()
	s.broadcast("approval.updated", map[string]any{
		"approval_id": approvalID,
		"status":      record.Status,
	})
	return nil
}

// RequestApproval creates an approval request and blocks until it is approved,
// denied, or the context is cancelled. This implements tools.ApprovalBroker
// (GC-SPEC-SEC-008) so that high-risk tool actions can require human approval
// via ACP clients rather than being silently executed or unconditionally blocked.
func (s *Server) RequestApproval(ctx context.Context, action, details string) (bool, error) {
	approvalID := uuid.NewString()
	record := &approvalRequest{
		ID:        approvalID,
		Action:    action,
		Details:   details,
		Status:    "PENDING",
		CreatedAt: time.Now().UTC(),
		done:      make(chan struct{}),
	}
	s.approvalsMu.Lock()
	s.approvals[approvalID] = record
	s.approvalsMu.Unlock()

	s.broadcast("approval.required", map[string]any{
		"approval_id": approvalID,
		"action":      record.Action,
		"details":     record.Details,
		"status":      record.Status,
		"created_at":  record.CreatedAt,
	})
	go s.approvalTimeoutDeny(approvalID)

	// Block until decided or context cancelled.
	select {
	case <-record.done:
		s.approvalsMu.Lock()
		status := record.Status
		s.approvalsMu.Unlock()
		approved := status == "APPROVED"
		if approved {
			audit.Record("allow", "approval.decided", "approved", "", approvalID)
		} else {
			audit.Record("deny", "approval.decided", "denied", "", approvalID)
		}
		return approved, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

const defaultApprovalTimeout = 60 * time.Second

func (s *Server) approvalTimeout() time.Duration {
	if s.cfg.ApprovalTimeout > 0 {
		return s.cfg.ApprovalTimeout
	}
	return defaultApprovalTimeout
}

// approvalTimeoutDeny auto-denies an approval request after timeout (GC-SPEC-SEC-008).
func (s *Server) approvalTimeoutDeny(approvalID string) {
	time.Sleep(s.approvalTimeout())
	s.approvalsMu.Lock()
	record, ok := s.approvals[approvalID]
	if !ok || record.Status != "PENDING" {
		s.approvalsMu.Unlock()
		return
	}
	record.Status = "DENIED"
	updatedStatus := record.Status
	updatedID := record.ID
	// Signal any blocking RequestApproval caller.
	select {
	case <-record.done:
	default:
		close(record.done)
	}
	s.approvalsMu.Unlock()
	audit.Record("deny", "approval.timeout", "approval_timeout_default_deny", "", approvalID)
	s.broadcast("approval.updated", map[string]any{
		"approval_id": updatedID,
		"status":      updatedStatus,
	})
	slog.Info("ws: approval auto-denied on timeout", "approval_id", approvalID)
}

func (s *Server) consumeToolEvents(ch <-chan string) {
	for name := range ch {
		s.broadcast("tools.updated", map[string]any{"name": name})
	}
}

func (s *Server) broadcast(method string, params interface{}) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	slog.Info("ws: broadcast", "method", method, "clients", len(s.clients))
	for c := range s.clients {
		if err := c.write(context.Background(), rpcResponse{
			JSONRPC: "2.0",
			Method:  method,
			Params:  params,
		}); err != nil {
			slog.Error("ws: broadcast write error", "method", method, "error", err)
		}
	}
}

func (s *Server) addClient(c *client) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	s.clients[c] = struct{}{}
}

func (s *Server) removeClient(c *client) {
	// Clean up bus subscription for event forwarding.
	c.subMu.Lock()
	if c.busCancel != nil {
		c.busCancel()
	}
	if c.busSub != nil && s.cfg.Bus != nil {
		s.cfg.Bus.Unsubscribe(c.busSub)
	}
	c.subMu.Unlock()

	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	delete(s.clients, c)
}

func (c *client) write(ctx context.Context, payload interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return wsjson.Write(ctx, c.conn, payload)
}

func (c *client) markHandshaken() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handshaken = true
}

func (c *client) isHandshaken() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handshaken
}

// subscribeClientToSession registers a WS client for live event push on a session.
// On the first subscription, it starts a bus listener goroutine that forwards
// matching task events to the client's WebSocket connection.
func (s *Server) subscribeClientToSession(c *client, sessionID string, lastEventID int64) {
	if s.cfg.Bus == nil {
		return
	}

	c.subMu.Lock()
	defer c.subMu.Unlock()

	if c.subscribedSes == nil {
		c.subscribedSes = make(map[string]int64)
	}
	c.subscribedSes[sessionID] = lastEventID

	// Start the bus listener goroutine on first subscription.
	if c.busSub == nil {
		c.busSub = s.cfg.Bus.Subscribe("task.")
		var busCtx context.Context
		busCtx, c.busCancel = context.WithCancel(context.Background())
		go s.forwardBusEvents(busCtx, c)
	}
}

// forwardBusEvents reads task lifecycle events from the bus and pushes new
// task_events to the WS client for any session the client has subscribed to.
func (s *Server) forwardBusEvents(ctx context.Context, c *client) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-c.busSub.Ch():
			if !ok {
				return
			}
			payload, _ := ev.Payload.(map[string]string)
			if payload == nil {
				continue
			}
			sessID := payload["session_id"]
			if sessID == "" {
				continue
			}

			c.subMu.Lock()
			lastID, subscribed := c.subscribedSes[sessID]
			c.subMu.Unlock()
			if !subscribed {
				continue
			}

			// Query new task events since the last forwarded event_id.
			events, err := s.cfg.Store.ListTaskEventsFrom(ctx, sessID, lastID, 100)
			if err != nil || len(events) == 0 {
				continue
			}

			var maxSent int64
			for _, te := range events {
				_ = c.write(ctx, rpcResponse{
					JSONRPC: "2.0",
					Method:  "session.event",
					Params: map[string]any{
						"event_id":   te.EventID,
						"task_id":    te.TaskID,
						"session_id": te.SessionID,
						"event_type": te.EventType,
						"state_from": te.StateFrom,
						"state_to":   te.StateTo,
						"run_id":     te.RunID,
						"trace_id":   te.TraceID,
						"payload":    te.Payload,
						"created_at": te.CreatedAt,
					},
				})
				if te.EventID > maxSent {
					maxSent = te.EventID
				}
			}

			// Update high-water mark so we don't re-send.
			if maxSent > 0 {
				c.subMu.Lock()
				if maxSent > c.subscribedSes[sessID] {
					c.subscribedSes[sessID] = maxSent
				}
				c.subMu.Unlock()
			}
		}
	}
}

// --- REST API handlers ---

func (s *Server) handleAPITasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	statusFilter := r.URL.Query().Get("status")
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	tasks, total, err := s.cfg.Store.ListTasksPaginated(r.Context(), statusFilter, limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tasks": tasks, "total": total})
}

func (s *Server) handleAPITaskByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	taskID := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	if taskID == "" {
		http.Error(w, "task_id required", http.StatusBadRequest)
		return
	}
	task, err := s.cfg.Store.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(task)
}

func (s *Server) handleAPISessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	sessions, err := s.cfg.Store.ListSessions(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"sessions": sessions})
}

func (s *Server) handleAPISessionMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Path: /api/sessions/{id}/messages
	path := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[1] != "messages" || parts[0] == "" {
		http.Error(w, "invalid path: expected /api/sessions/{id}/messages", http.StatusBadRequest)
		return
	}
	sessionID := parts[0]
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := s.cfg.Store.ListHistory(r.Context(), sessionID, "", limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"messages": items})
}

func (s *Server) handleAPISkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.cfg.SkillsStatus == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"skills": []any{}})
		return
	}
	skills, err := s.cfg.SkillsStatus(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"skills": skills})
}

func (s *Server) handleAPIConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	policyVersion := ""
	if s.cfg.Policy != nil {
		policyVersion = s.cfg.Policy.PolicyVersion()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"config_hash":    s.cfg.ConfigFingerprint,
		"policy_version": policyVersion,
	})
}

// PlanSummary is a lightweight view of a configured plan for the REST API.
// GC-SPEC-PDR-v4-Phase-4: Plan system.
type PlanSummary struct {
	Name      string   `json:"name"`
	StepCount int      `json:"step_count"`
	AgentIDs  []string `json:"agent_ids"`
}

// handleAPIPlans returns the list of configured plans (GC-SPEC-PDR-v4-Phase-4: Plan system).
func (s *Server) handleAPIPlans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.authorize(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	plans := make([]PlanSummary, 0, len(s.cfg.Plans))
	for _, p := range s.cfg.Plans {
		plans = append(plans, p)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"plans": plans})
}
