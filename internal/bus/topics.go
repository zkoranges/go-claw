package bus

// Additional plan step event topics.
// GC-SPEC-PDR-v7-Phase-3: Event contract for plan execution (TopicPlanStepStarted/Completed defined in bus.go).
const (
	TopicPlanStepFailed = "plan.step.failed"
)

// HITL (Human-In-The-Loop) approval event topics.
// GC-SPEC-PDR-v7-Phase-3: HITL approval workflow events.
const (
	TopicHITLApprovalRequested = "hitl.approval.requested"
	TopicHITLApprovalResponse  = "hitl.approval.response"
)

// Agent alert topic.
// GC-SPEC-PDR-v7-Phase-3: Agent alert notifications.
const (
	TopicAgentAlert = "agent.alert"
)

// PlanStepEvent is published when a plan step starts, completes, or fails.
// GC-SPEC-PDR-v7-Phase-3: Step execution events.
type PlanStepEvent struct {
	ExecutionID string // Plan execution ID
	StepID      string // Step ID within the plan
	TaskID      string // Associated task ID (for started/completed)
	AgentID     string // Agent executing the step
}

// HITLApprovalRequest is published when a step requires human approval.
// GC-SPEC-PDR-v7-Phase-3: HITL approval request event.
type HITLApprovalRequest struct {
	RequestID   string // Unique request ID for matching response
	ExecutionID string // Plan execution ID
	StepID      string // Step ID requiring approval
	Prompt      string // Step prompt that requires approval
	Timeout     int    // Timeout in milliseconds for approval
}

// HITLApprovalResponse is published when a user approves or rejects a step.
// GC-SPEC-PDR-v7-Phase-3: HITL approval response event.
type HITLApprovalResponse struct {
	RequestID string // Matches the corresponding request ID
	Action    string // "approve" or "reject"
	Reason    string // Optional reason for action
}

// AgentAlert is published when an agent needs to alert operators.
// GC-SPEC-PDR-v7-Phase-3: Agent alert notification event.
type AgentAlert struct {
	ExecutionID string // Plan execution ID
	StepID      string // Step ID (if associated with a step)
	Severity    string // "info", "warning", or "error"
	Message     string // Alert message
}
