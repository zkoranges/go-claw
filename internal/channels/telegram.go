package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/persistence"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TelegramChannel implements the Channel interface for Telegram.
type TelegramChannel struct {
	token      string
	allowedIDs map[int64]struct{}
	router     engine.ChatTaskRouter
	store      *persistence.Store
	logger     *slog.Logger
	bot        *tgbotapi.BotAPI
	eventBus   *bus.Bus

	pendingMu    sync.Mutex
	pendingTasks map[string]int64 // taskID -> chatID

	// streamMu protects streamMsgs for progressive editing.
	streamMu   sync.Mutex
	streamMsgs map[string]*streamState // taskID -> streaming state

	// GC-SPEC-PDR-v7-Phase-3: Event subscriptions for plan execution
	eventSubs []*bus.Subscription // Subscriptions to clean up on shutdown
}

// streamState tracks progressive editing for a streaming task.
type streamState struct {
	chatID    int64
	messageID int
	text      strings.Builder
	lastEdit  time.Time
}

// NewTelegramChannel creates a new Telegram channel.
func NewTelegramChannel(token string, allowedIDs []int64, router engine.ChatTaskRouter, store *persistence.Store, logger *slog.Logger, eventBus ...*bus.Bus) *TelegramChannel {
	allowed := make(map[int64]struct{})
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}
	var eb *bus.Bus
	if len(eventBus) > 0 {
		eb = eventBus[0]
	}
	return &TelegramChannel{
		token:        token,
		allowedIDs:   allowed,
		router:       router,
		store:        store,
		logger:       logger,
		eventBus:     eb,
		pendingTasks: make(map[string]int64),
		streamMsgs:   make(map[string]*streamState),
	}
}

func (t *TelegramChannel) Name() string {
	return "telegram"
}

func (t *TelegramChannel) Start(ctx context.Context) error {
	var err error
	t.bot, err = tgbotapi.NewBotAPI(t.token)
	if err != nil {
		return fmt.Errorf("telegram init failed: %w", err)
	}

	t.logger.Info("telegram bot started", "user", t.bot.Self.UserName)

	// Monitor task completions to send replies via event bus or polling fallback.
	go t.monitorCompletions(ctx)

	// Reconnection loop with exponential backoff.
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		u := tgbotapi.NewUpdate(0)
		u.Timeout = 60
		updates := t.bot.GetUpdatesChan(u)

		pollErr := t.pollUpdates(ctx, updates)

		// Always clean up the old polling goroutine before reconnecting.
		t.bot.StopReceivingUpdates()

		if pollErr != nil {
			t.logger.Warn("telegram poll disconnected, reconnecting", "error", pollErr, "backoff", backoff)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		// pollUpdates returned nil means ctx was cancelled.
		return nil
	}
}

// pollUpdates reads from the update channel until ctx is done, the channel
// closes, or no updates arrive within 2x the long-poll timeout (stall detection).
// Returns nil on context cancellation, or an error to trigger reconnection.
func (t *TelegramChannel) pollUpdates(ctx context.Context, updates tgbotapi.UpdatesChannel) error {
	// tgbotapi uses a 60s long-poll timeout. If we see nothing for 2.5 minutes,
	// the connection is likely dead (the library blocks rather than closing the channel).
	const stallTimeout = 150 * time.Second

	timer := time.NewTimer(stallTimeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case update, ok := <-updates:
			if !ok {
				return fmt.Errorf("update channel closed")
			}

			// Reset stall timer on every received update (including empty long-poll returns).
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(stallTimeout)

			// Handle text messages
			if update.Message != nil {
				if _, ok := t.allowedIDs[update.Message.From.ID]; !ok {
					t.logger.Warn("telegram access denied", "user_id", update.Message.From.ID, "user_name", update.Message.From.UserName)
					continue
				}
				t.handleMessage(ctx, update.Message)
				continue
			}

			// Handle inline button callbacks (HITL approvals)
			if update.CallbackQuery != nil {
				if _, ok := t.allowedIDs[update.CallbackQuery.From.ID]; !ok {
					t.logger.Warn("telegram callback access denied", "user_id", update.CallbackQuery.From.ID)
					continue
				}
				t.handleCallbackQuery(ctx, update.CallbackQuery)
				continue
			}

		case <-timer.C:
			return fmt.Errorf("no updates received for %v (possible disconnect)", stallTimeout)
		}
	}
}

func (t *TelegramChannel) handleMessage(ctx context.Context, msg *tgbotapi.Message) {
	content := strings.TrimSpace(msg.Text)
	if content == "" {
		return
	}

	// Parse @agent prefix for agent routing.
	agentID := "default"
	if strings.HasPrefix(content, "@") {
		parts := strings.SplitN(content, " ", 2)
		agentID = strings.TrimPrefix(parts[0], "@")
		if len(parts) > 1 {
			content = strings.TrimSpace(parts[1])
		} else {
			content = ""
		}
	}
	if content == "" {
		return
	}

	// Map Telegram user+agent to a persistent session ID (per-agent isolation).
	sessionID := fmt.Sprintf("telegram-%d-agent-%s", msg.From.ID, agentID)

	// Route through ChatTaskRouter (handles session, history, task creation).
	taskID, err := t.router.CreateChatTask(ctx, agentID, sessionID, content)
	if err != nil {
		t.logger.Error("failed to create telegram task", "error", err)
		t.reply(msg.Chat.ID, fmt.Sprintf("Error: could not schedule task: %v", err))
		return
	}

	// Map task ID to chat ID for reply routing
	t.pendingMu.Lock()
	t.pendingTasks[taskID] = msg.Chat.ID
	t.pendingMu.Unlock()

	// We use the KV store to keep track of which task belongs to which chat/message
	kvKey := fmt.Sprintf("task_reply:%s", taskID)
	kvVal := fmt.Sprintf("%d", msg.Chat.ID)
	if err := t.store.KVSet(ctx, kvKey, kvVal); err != nil {
		t.logger.Warn("failed to map task to chat", "error", err)
	}
}

// handleCallbackQuery handles inline button clicks from HITL approval messages.
func (t *TelegramChannel) handleCallbackQuery(ctx context.Context, query *tgbotapi.CallbackQuery) {
	// Parse the callback data (format: "hitl:requestID:action")
	requestID, action, err := parseHITLCallback(query.Data)
	if err != nil {
		// Not a HITL callback, ignore
		return
	}

	// Send notification to acknowledge button press
	notification := tgbotapi.NewCallbackWithAlert(query.ID, fmt.Sprintf("Processing %s...", action))
	if _, err := t.bot.Request(notification); err != nil {
		t.logger.Warn("failed to send callback notification", "error", err)
	}

	// Publish approval response to event bus
	if t.eventBus != nil {
		response := bus.HITLApprovalResponse{
			RequestID: requestID,
			Action:    action, // "approve" or "reject"
			Reason:    fmt.Sprintf("via Telegram (%s)", query.From.UserName),
		}
		t.eventBus.Publish(bus.TopicHITLApprovalResponse, response)
	}
}

func (t *TelegramChannel) monitorCompletions(ctx context.Context) {
	if t.eventBus != nil {
		go t.monitorStreamTokens(ctx)
		t.monitorViaBus(ctx)
		return
	}
	t.monitorViaPolling(ctx)
}

// monitorViaBus subscribes to the event bus for task lifecycle events.
func (t *TelegramChannel) monitorViaBus(ctx context.Context) {
	sub := t.eventBus.Subscribe("task.")
	defer t.eventBus.Unsubscribe(sub)

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.Ch():
			payload, ok := ev.Payload.(map[string]string)
			if !ok {
				continue
			}
			taskID := payload["task_id"]
			if taskID == "" {
				continue
			}

			t.pendingMu.Lock()
			chatID, pending := t.pendingTasks[taskID]
			if pending {
				delete(t.pendingTasks, taskID)
			}
			t.pendingMu.Unlock()

			if !pending {
				continue
			}

			switch ev.Topic {
			case "task.succeeded":
				task, err := t.store.GetTask(ctx, taskID)
				if err != nil {
					t.logger.Warn("failed to get completed task", "task_id", taskID, "error", err)
					continue
				}
				replyText := task.Result
				var resMap map[string]string
				if json.Unmarshal([]byte(task.Result), &resMap) == nil {
					if val, ok := resMap["reply"]; ok {
						replyText = val
					}
				}

				// If we were streaming this task, do a final edit instead of a new message.
				t.streamMu.Lock()
				state, wasStreaming := t.streamMsgs[taskID]
				if wasStreaming {
					delete(t.streamMsgs, taskID)
				}
				t.streamMu.Unlock()

				if wasStreaming && state.messageID != 0 {
					t.editMessageText(chatID, state.messageID, replyText)
				} else {
					t.reply(chatID, replyText)
				}

			case "task.failed":
				task, err := t.store.GetTask(ctx, taskID)
				if err != nil {
					t.reply(chatID, "Task failed (details unavailable).")
					continue
				}
				errMsg := task.Error
				if errMsg == "" {
					errMsg = "unknown error"
				}
				t.reply(chatID, fmt.Sprintf("Task failed: %s", errMsg))

			case "task.canceled":
				t.reply(chatID, "Task was canceled.")
			}
		}
	}
}

// monitorViaPolling is the fallback when no event bus is configured.
func (t *TelegramChannel) monitorViaPolling(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.checkPendingTasks(ctx)
		}
	}
}

func (t *TelegramChannel) checkPendingTasks(ctx context.Context) {
	t.pendingMu.Lock()
	// Copy keys to avoid holding lock during DB ops
	tasksToCheck := make([]string, 0, len(t.pendingTasks))
	for id := range t.pendingTasks {
		tasksToCheck = append(tasksToCheck, id)
	}
	t.pendingMu.Unlock()

	for _, taskID := range tasksToCheck {
		task, err := t.store.GetTask(ctx, taskID)
		if err != nil {
			continue // Task might not exist yet or DB error
		}

		if task.Status == persistence.TaskStatusSucceeded || task.Status == persistence.TaskStatusFailed {
			t.pendingMu.Lock()
			chatID, ok := t.pendingTasks[taskID]
			delete(t.pendingTasks, taskID)
			t.pendingMu.Unlock()

			if !ok {
				continue
			}

			replyText := ""
			if task.Status == persistence.TaskStatusFailed {
				replyText = fmt.Sprintf("❌ Task failed: %s", task.Error)
			} else {
				var resMap map[string]string
				if err := json.Unmarshal([]byte(task.Result), &resMap); err == nil {
					if val, ok := resMap["reply"]; ok {
						replyText = val
					} else {
						replyText = task.Result
					}
				} else {
					replyText = task.Result
				}
			}

			// Better formatting
			t.reply(chatID, replyText)
		}
	}
}

func (t *TelegramChannel) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := t.bot.Send(msg); err != nil {
		t.logger.Error("failed to send telegram reply", "error", err)
	}
}

// monitorStreamTokens subscribes to stream.token bus events and progressively
// edits Telegram messages as tokens arrive from the LLM.
func (t *TelegramChannel) monitorStreamTokens(ctx context.Context) {
	sub := t.eventBus.Subscribe("stream.")
	defer t.eventBus.Unsubscribe(sub)

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-sub.Ch():
			if ev.Topic != "stream.token" {
				continue
			}
			payload, ok := ev.Payload.(map[string]string)
			if !ok {
				continue
			}
			taskID := payload["task_id"]
			chunk := payload["chunk"]
			if taskID == "" || chunk == "" {
				continue
			}

			// Look up chat ID from pending tasks.
			t.pendingMu.Lock()
			chatID, pending := t.pendingTasks[taskID]
			t.pendingMu.Unlock()
			if !pending {
				continue
			}

			t.streamMu.Lock()
			state, exists := t.streamMsgs[taskID]
			if !exists {
				// First chunk: send a new placeholder message.
				state = &streamState{chatID: chatID}
				msg := tgbotapi.NewMessage(chatID, chunk)
				sent, err := t.bot.Send(msg)
				if err != nil {
					t.logger.Warn("failed to send stream placeholder", "task_id", taskID, "error", err)
					t.streamMu.Unlock()
					continue
				}
				state.messageID = sent.MessageID
				state.text.WriteString(chunk)
				state.lastEdit = time.Now()
				t.streamMsgs[taskID] = state
				t.streamMu.Unlock()
				continue
			}

			// Accumulate chunk text.
			state.text.WriteString(chunk)

			// Rate-limit edits to ~1/second to avoid Telegram 429 errors.
			if time.Since(state.lastEdit) < time.Second {
				t.streamMu.Unlock()
				continue
			}
			text := state.text.String()
			msgID := state.messageID
			state.lastEdit = time.Now()
			t.streamMu.Unlock()

			t.editMessageText(chatID, msgID, text)
		}
	}
}

// editMessageText progressively updates an existing Telegram message.
// Used for streaming responses — the message is edited in-place as tokens arrive.
func (t *TelegramChannel) editMessageText(chatID int64, messageID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	if _, err := t.bot.Send(edit); err != nil {
		t.logger.Warn("failed to edit telegram message (progressive)", "error", err)
	}
}

// GC-SPEC-PDR-v7-Phase-3: Event subscription and handling methods

// SubscribeToEvents subscribes to plan execution and HITL events.
// Called at startup to enable Telegram to receive and forward event notifications.
func (t *TelegramChannel) SubscribeToEvents() {
	if t.eventBus == nil {
		return
	}

	// Subscribe to plan step events
	subs := []*bus.Subscription{
		t.eventBus.Subscribe(bus.TopicPlanStepStarted),
		t.eventBus.Subscribe(bus.TopicPlanStepCompleted),
		t.eventBus.Subscribe(bus.TopicPlanStepFailed),
		t.eventBus.Subscribe(bus.TopicHITLApprovalRequested),
		t.eventBus.Subscribe(bus.TopicAgentAlert),
	}

	t.eventSubs = subs

	// Start event handlers for each subscription
	for _, sub := range subs {
		sub := sub // Capture for closure
		go func() {
			for {
				ev := <-sub.Ch()
				if ev.Topic == "" {
					// Channel closed on shutdown
					return
				}
				t.handleEvent(&ev)
			}
		}()
	}
}

// handleEvent dispatches events to appropriate handlers.
func (t *TelegramChannel) handleEvent(ev *bus.Event) {
	switch ev.Topic {
	case bus.TopicPlanStepCompleted:
		go t.onPlanStepCompleted(ev.Payload)
	case bus.TopicPlanStepFailed:
		go t.onPlanStepFailed(ev.Payload)
	case bus.TopicHITLApprovalRequested:
		go t.onHITLRequest(ev.Payload)
	case bus.TopicAgentAlert:
		go t.onAgentAlert(ev.Payload)
	}
}

// onPlanStepCompleted handles completed plan step events.
func (t *TelegramChannel) onPlanStepCompleted(data interface{}) {
	stepEv, ok := data.(bus.PlanStepEvent)
	if !ok {
		t.logger.Warn("invalid PlanStepEvent payload", "type", fmt.Sprintf("%T", data))
		return
	}

	// Format message with ✅ emoji
	msg := fmt.Sprintf("✅ Step `%s` completed (execution: `%s`)",
		escapeMarkdownV2(stepEv.StepID),
		escapeMarkdownV2(stepEv.ExecutionID))

	// Send to all allowed chats (in real usage, would track which chat owns this plan)
	for chatID := range t.allowedIDs {
		t.replyMarkdown(chatID, msg)
	}
}

// onPlanStepFailed handles failed plan step events.
func (t *TelegramChannel) onPlanStepFailed(data interface{}) {
	stepEv, ok := data.(bus.PlanStepEvent)
	if !ok {
		t.logger.Warn("invalid PlanStepEvent payload", "type", fmt.Sprintf("%T", data))
		return
	}

	// Format message with ❌ emoji
	msg := fmt.Sprintf("❌ Step `%s` failed (execution: `%s`)",
		escapeMarkdownV2(stepEv.StepID),
		escapeMarkdownV2(stepEv.ExecutionID))

	// Send to all allowed chats
	for chatID := range t.allowedIDs {
		t.replyMarkdown(chatID, msg)
	}
}

// onHITLRequest handles human-in-the-loop approval requests.
func (t *TelegramChannel) onHITLRequest(data interface{}) {
	req, ok := data.(bus.HITLApprovalRequest)
	if !ok {
		t.logger.Warn("invalid HITLApprovalRequest payload", "type", fmt.Sprintf("%T", data))
		return
	}

	// Create inline keyboard with Approve/Reject buttons
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"✅ Approve",
				fmt.Sprintf("hitl:%s:approve", req.RequestID),
			),
			tgbotapi.NewInlineKeyboardButtonData(
				"❌ Reject",
				fmt.Sprintf("hitl:%s:reject", req.RequestID),
			),
		),
	)

	// Format message with step details
	msg := fmt.Sprintf("🔓 *HITL Approval Required*\n\nStep: `%s`\n\nPrompt:\n```\n%s\n```",
		escapeMarkdownV2(req.StepID),
		escapeMarkdownV2(req.Prompt))

	// Send to all allowed chats with keyboard
	for chatID := range t.allowedIDs {
		t.replyMarkdownWithKeyboard(chatID, msg, &keyboard)
	}
}

// onAgentAlert handles agent alert notifications.
func (t *TelegramChannel) onAgentAlert(data interface{}) {
	alert, ok := data.(bus.AgentAlert)
	if !ok {
		t.logger.Warn("invalid AgentAlert payload", "type", fmt.Sprintf("%T", data))
		return
	}

	// Map severity to emoji
	emoji := "ℹ️"
	switch alert.Severity {
	case "warning":
		emoji = "⚠️"
	case "error":
		emoji = "🚨"
	}

	// Format message
	msg := fmt.Sprintf("%s *%s Alert*\n%s",
		emoji,
		escapeMarkdownV2(alert.Severity),
		escapeMarkdownV2(alert.Message))

	// Send to all allowed chats
	for chatID := range t.allowedIDs {
		t.replyMarkdown(chatID, msg)
	}
}

// replyMarkdown sends a markdown-formatted message.
func (t *TelegramChannel) replyMarkdown(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "MarkdownV2"
	if _, err := t.bot.Send(msg); err != nil {
		t.logger.Error("failed to send telegram markdown reply", "error", err)
	}
}

// replyMarkdownWithKeyboard sends a markdown-formatted message with inline keyboard.
func (t *TelegramChannel) replyMarkdownWithKeyboard(chatID int64, text string, keyboard *tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "MarkdownV2"
	msg.ReplyMarkup = keyboard
	if _, err := t.bot.Send(msg); err != nil {
		t.logger.Error("failed to send telegram message with keyboard", "error", err)
	}
}

// escapeMarkdownV2 escapes special characters for Telegram MarkdownV2.
// Must escape: _ * [ ] ( ) ~ > # + - = | { } . !
// GC-SPEC-PDR-v7-Phase-3: MarkdownV2 character escaping.
func escapeMarkdownV2(s string) string {
	// Characters that need escaping in MarkdownV2
	specialChars := "_*[]()~>#+-=|{}.!"

	result := make([]byte, 0, len(s)*2) // Pre-allocate for efficiency

	for i := 0; i < len(s); i++ {
		c := s[i]
		// Check if character needs escaping
		if strings.ContainsAny(string(c), specialChars) {
			result = append(result, '\\')
		}
		result = append(result, c)
	}

	return string(result)
}

// formatPlanProgress formats plan execution progress as markdown.
// Each step shown with status emoji.
// GC-SPEC-PDR-v7-Phase-3: Plan progress formatting.
func formatPlanProgress(planName string, steps []bus.PlanStepEvent) string {
	if len(steps) == 0 {
		return fmt.Sprintf("Plan `%s` in progress\\.\\.\\.\\.", escapeMarkdownV2(planName))
	}

	result := fmt.Sprintf("📋 Plan: `%s`\n\n", escapeMarkdownV2(planName))
	for i, step := range steps {
		result += fmt.Sprintf("%d\\. Step `%s` (agent: `%s`)\n",
			i+1,
			escapeMarkdownV2(step.StepID),
			escapeMarkdownV2(step.AgentID))
	}

	return result
}

// parsePlanCommand parses a /plan command.
// Format: /plan <planName> [input...]
// GC-SPEC-PDR-v7-Phase-3: Plan command parsing.
func parsePlanCommand(input string) (planName, planInput string, err error) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/plan") {
		return "", "", fmt.Errorf("not a plan command")
	}

	// Remove /plan prefix and trim
	remaining := strings.TrimSpace(input[5:])
	if remaining == "" {
		return "", "", fmt.Errorf("plan name required")
	}

	// Split into plan name and optional input
	parts := strings.SplitN(remaining, " ", 2)
	planName = parts[0]

	if len(parts) > 1 {
		planInput = strings.TrimSpace(parts[1])
	}

	return planName, planInput, nil
}

// parseHITLCallback parses HITL callback data.
// Format: hitl:requestID:action
// GC-SPEC-PDR-v7-Phase-3: HITL callback parsing.
func parseHITLCallback(data string) (requestID, action string, err error) {
	data = strings.TrimSpace(data)

	if !strings.HasPrefix(data, "hitl:") {
		return "", "", fmt.Errorf("not a HITL callback")
	}

	// Remove hitl: prefix
	remaining := data[5:]

	// Split on : to get requestID and action
	parts := strings.SplitN(remaining, ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid HITL callback format")
	}

	requestID = parts[0]
	action = parts[1]

	if requestID == "" || action == "" {
		return "", "", fmt.Errorf("requestID and action required")
	}

	return requestID, action, nil
}
