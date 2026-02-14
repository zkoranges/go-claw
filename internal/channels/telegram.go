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

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := t.bot.GetUpdatesChan(u)

	// Monitor task completions to send replies via event bus or polling fallback.
	go t.monitorCompletions(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case update := <-updates:
			if update.Message == nil {
				continue
			}

			// Security check
			if _, ok := t.allowedIDs[update.Message.From.ID]; !ok {
				t.logger.Warn("telegram access denied", "user_id", update.Message.From.ID, "user_name", update.Message.From.UserName)
				continue
			}

			t.handleMessage(ctx, update.Message)
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
		t.reply(msg.Chat.ID, "Error: could not schedule task.")
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

func (t *TelegramChannel) monitorCompletions(ctx context.Context) {
	if t.eventBus != nil {
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
				t.reply(chatID, replyText)

			case "task.failed":
				task, err := t.store.GetTask(ctx, taskID)
				if err != nil {
					t.reply(chatID, "Task failed (details unavailable).")
					continue
				}
				t.reply(chatID, fmt.Sprintf("Task failed: %s", task.Error))

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
