package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/basket/go-claw/internal/audit"
	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/policy"
	"github.com/basket/go-claw/internal/shared"
	"github.com/firebase/genkit/go/ai"
	"github.com/firebase/genkit/go/genkit"
)

const (
	capSendMessage  = "tools.send_message"
	capReadMessages = "tools.read_messages"
)

// SendMessageInput is the input for the send_message tool.
type SendMessageInput struct {
	// ToAgent is the recipient agent ID.
	ToAgent string `json:"to_agent"`
	// Content is the message text.
	Content string `json:"content"`
}

// SendMessageOutput is the output for the send_message tool.
type SendMessageOutput struct {
	// Status indicates whether the message was sent.
	Status string `json:"status"`
}

// ReadMessagesInput is the input for the read_messages tool.
type ReadMessagesInput struct {
	// Limit is the max number of messages to return. Default 10.
	Limit int `json:"limit,omitempty"`
}

// ReadMessagesOutput is the output for the read_messages tool.
type ReadMessagesOutput struct {
	// Messages contains the unread messages.
	Messages []MessageEntry `json:"messages"`
	// Count is the number of messages returned.
	Count int `json:"count"`
}

// MessageEntry represents a single message in the output.
type MessageEntry struct {
	FromAgent string `json:"from_agent"`
	Content   string `json:"content"`
	SentAt    string `json:"sent_at"`
}

func sendMessage(ctx context.Context, input *SendMessageInput, store *persistence.Store, pol policy.Checker) (*SendMessageOutput, error) {
	if pol == nil || !pol.AllowCapability(capSendMessage) {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", capSendMessage, "missing_capability", pv, "send_message")
		return nil, fmt.Errorf("policy denied capability %q", capSendMessage)
	}

	pv := pol.PolicyVersion()

	if input.ToAgent == "" {
		return nil, fmt.Errorf("send_message: to_agent must be non-empty")
	}
	if strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("send_message: content must be non-empty")
	}

	fromAgent := shared.AgentID(ctx)
	if fromAgent == "" {
		fromAgent = "default"
	}

	if fromAgent == input.ToAgent {
		return nil, fmt.Errorf("send_message: cannot send a message to yourself")
	}

	// Validate target agent exists.
	agent, err := store.GetAgent(ctx, input.ToAgent)
	if err != nil {
		return nil, fmt.Errorf("send_message: check target agent: %w", err)
	}
	if agent == nil {
		return nil, fmt.Errorf("send_message: target agent %q not found", input.ToAgent)
	}

	if err := store.SendAgentMessage(ctx, fromAgent, input.ToAgent, input.Content); err != nil {
		return nil, fmt.Errorf("send_message: %w", err)
	}

	// Publish bus event for autonomous agent wake-up.
	if b := store.Bus(); b != nil {
		b.Publish(bus.TopicAgentMessage, bus.AgentMessageEvent{
			FromAgent: fromAgent,
			ToAgent:   input.ToAgent,
			Content:   input.Content,
			Depth:     shared.MessageDepth(ctx),
		})
	}

	slog.Info("send_message: message sent",
		"from", fromAgent,
		"to", input.ToAgent,
	)
	audit.Record("allow", capSendMessage, "message_sent", pv, fmt.Sprintf("%s->%s", fromAgent, input.ToAgent))

	return &SendMessageOutput{Status: "sent"}, nil
}

func readMessages(ctx context.Context, input *ReadMessagesInput, store *persistence.Store, pol policy.Checker) (*ReadMessagesOutput, error) {
	if pol == nil || !pol.AllowCapability(capReadMessages) {
		pv := ""
		if pol != nil {
			pv = pol.PolicyVersion()
		}
		audit.Record("deny", capReadMessages, "missing_capability", pv, "read_messages")
		return nil, fmt.Errorf("policy denied capability %q", capReadMessages)
	}

	pv := pol.PolicyVersion()

	agentID := shared.AgentID(ctx)
	if agentID == "" {
		agentID = "default"
	}

	limit := input.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	msgs, err := store.ReadAgentMessages(ctx, agentID, limit)
	if err != nil {
		return nil, fmt.Errorf("read_messages: %w", err)
	}

	entries := make([]MessageEntry, len(msgs))
	for i, m := range msgs {
		entries[i] = MessageEntry{
			FromAgent: m.FromAgent,
			Content:   m.Content,
			SentAt:    m.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	audit.Record("allow", capReadMessages, "messages_read", pv, fmt.Sprintf("agent=%s count=%d", agentID, len(entries)))

	return &ReadMessagesOutput{
		Messages: entries,
		Count:    len(entries),
	}, nil
}

func registerMessaging(g *genkit.Genkit, reg *Registry) []ai.ToolRef {
	sendTool := genkit.DefineTool(g, "send_message",
		"Send a message to another agent. The message will be stored and the recipient can read it with read_messages. Requires tools.send_message capability.",
		func(ctx *ai.ToolContext, input SendMessageInput) (SendMessageOutput, error) {
			reg.publishToolCall(ctx, "send_message")
			out, err := sendMessage(ctx, &input, reg.Store, reg.Policy)
			if err != nil {
				return SendMessageOutput{}, err
			}
			return *out, nil
		},
	)

	readTool := genkit.DefineTool(g, "read_messages",
		"Read unread messages from other agents. Messages are marked as read after retrieval. Requires tools.read_messages capability.",
		func(ctx *ai.ToolContext, input ReadMessagesInput) (ReadMessagesOutput, error) {
			reg.publishToolCall(ctx, "read_messages")
			out, err := readMessages(ctx, &input, reg.Store, reg.Policy)
			if err != nil {
				return ReadMessagesOutput{}, err
			}
			return *out, nil
		},
	)

	return []ai.ToolRef{sendTool, readTool}
}
