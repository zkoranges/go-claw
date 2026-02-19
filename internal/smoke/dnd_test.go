package smoke

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/basket/go-claw/internal/bus"
	"github.com/basket/go-claw/internal/engine"
	"github.com/basket/go-claw/internal/memory"
	"github.com/basket/go-claw/internal/persistence"
	"github.com/basket/go-claw/internal/shared"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const dndSessionID = "a1b2c3d4-e5f6-7890-abcd-ef1234567890"

const (
	agentDM      = "dm"
	agentWarrior = "warrior"
	agentRogue   = "rogue"
	agentCleric  = "cleric"
	agentWizard  = "wizard"
	agentRanger  = "ranger"
)

// dndAgents maps agent IDs to display names and deterministic responses.
var dndAgents = map[string]struct {
	displayName string
	response    string
}{
	agentDM:      {"Dungeon Master", `{"reply":"You enter a dark cavern. Roll for initiative."}`},
	agentWarrior: {"Fighter", `{"reply":"{\"action\":\"attack\",\"weapon\":\"greatsword\",\"damage\":12,\"hit\":true}"}`},
	agentRogue:   {"Thief", `{"reply":"{\"action\":\"sneak_attack\",\"weapon\":\"dagger\",\"damage\":18,\"hit\":true}"}`},
	agentCleric:  {"Healer", `{"reply":"{\"action\":\"heal\",\"target\":\"warrior\",\"amount\":15,\"spell\":\"cure_wounds\"}"}`},
	agentWizard:  {"Spellcaster", `{"reply":"{\"action\":\"cast\",\"spell\":\"fireball\",\"damage\":24,\"targets\":3}"}`},
	agentRanger:  {"Archer", `{"reply":"{\"action\":\"attack\",\"weapon\":\"longbow\",\"damage\":10,\"hit\":true}"}`},
}

// ---------------------------------------------------------------------------
// dndProcessor — deterministic Processor that returns JSON per agent ID
// ---------------------------------------------------------------------------

type dndProcessor struct {
	mu        sync.Mutex
	processed map[string]int // agent ID → invocation count
}

func newDndProcessor() *dndProcessor {
	return &dndProcessor{processed: make(map[string]int)}
}

func (p *dndProcessor) Process(ctx context.Context, task persistence.Task) (string, error) {
	agentID := shared.AgentID(ctx)
	if agentID == "" {
		agentID = task.AgentID
	}

	p.mu.Lock()
	p.processed[agentID]++
	p.mu.Unlock()

	agent, ok := dndAgents[agentID]
	if !ok {
		return "", fmt.Errorf("unknown agent: %s", agentID)
	}
	return agent.response, nil
}

func (p *dndProcessor) count(agentID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.processed[agentID]
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openDndStore(t *testing.T, b *bus.Bus) *persistence.Store {
	t.Helper()
	store, err := persistence.Open(filepath.Join(t.TempDir(), "goclaw.db"), b)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func dndWaitForStatus(t *testing.T, store *persistence.Store, taskID string, want persistence.TaskStatus, timeout time.Duration) *persistence.Task {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := store.GetTask(context.Background(), taskID)
		if err == nil && task.Status == want {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := store.GetTask(context.Background(), taskID)
	t.Fatalf("timed out waiting for task %s status %s, got %v", taskID, want, task.Status)
	return nil
}

func createDndTask(t *testing.T, store *persistence.Store, agentID, prompt string) string {
	t.Helper()
	taskID, err := store.CreateTaskForAgent(context.Background(), agentID, dndSessionID, fmt.Sprintf(`{"content":"%s"}`, prompt))
	if err != nil {
		t.Fatalf("create task for %s: %v", agentID, err)
	}
	return taskID
}

// ---------------------------------------------------------------------------
// TestDnDSession
// ---------------------------------------------------------------------------

func TestDnDSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Shared infrastructure
	eventBus := bus.New()
	store := openDndStore(t, eventBus)
	proc := newDndProcessor()

	// Create session
	if err := store.EnsureSession(ctx, dndSessionID); err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	// Register all 6 agents
	for id, info := range dndAgents {
		err := store.CreateAgent(ctx, persistence.AgentRecord{
			AgentID:     id,
			DisplayName: info.displayName,
			Status:      "active",
		})
		if err != nil {
			t.Fatalf("create agent %s: %v", id, err)
		}
	}

	// Create and start 6 engines (one per agent, 1 worker each)
	engines := make(map[string]*engine.Engine)
	for id := range dndAgents {
		eng := engine.New(store, proc, engine.Config{
			WorkerCount:  1,
			PollInterval: 10 * time.Millisecond,
			TaskTimeout:  5 * time.Second,
			AgentID:      id,
			Bus:          eventBus,
		})
		engines[id] = eng
		eng.Start(ctx)
	}
	t.Cleanup(func() {
		cancel()
		for _, eng := range engines {
			eng.Drain(5 * time.Second)
		}
	})

	// Players (all agents except DM)
	players := []string{agentWarrior, agentRogue, agentCleric, agentWizard, agentRanger}

	// ----- Phase 1: Concurrent Combat -----
	t.Run("Phase1_ConcurrentCombat", func(t *testing.T) {
		taskIDs := make(map[string]string)
		for _, id := range players {
			taskIDs[id] = createDndTask(t, store, id, "Roll for initiative")
		}

		// Wait for all tasks to succeed
		for id, taskID := range taskIDs {
			dndWaitForStatus(t, store, taskID, persistence.TaskStatusSucceeded, 10*time.Second)
			if c := proc.count(id); c < 1 {
				t.Errorf("agent %s was never invoked", id)
			}
		}
	})

	// ----- Phase 2: Inter-Agent Messaging -----
	t.Run("Phase2_InterAgentMessaging", func(t *testing.T) {
		// Subscribe to agent-related bus events before sending
		agentSub := eventBus.Subscribe("agent.")

		// DM sends message to each player
		for _, id := range players {
			err := store.SendAgentMessage(ctx, agentDM, id, fmt.Sprintf("Welcome, %s! Prepare for combat.", id))
			if err != nil {
				t.Fatalf("send message to %s: %v", id, err)
			}
		}

		// Each player reads their messages
		for _, id := range players {
			msgs, err := store.ReadAgentMessages(ctx, id, 10)
			if err != nil {
				t.Fatalf("read messages for %s: %v", id, err)
			}
			if len(msgs) == 0 {
				t.Errorf("agent %s has no messages", id)
				continue
			}
			if msgs[0].FromAgent != agentDM {
				t.Errorf("expected message from %s, got %s", agentDM, msgs[0].FromAgent)
			}
			if msgs[0].Content == "" {
				t.Error("message content is empty")
			}
		}

		// DM should have 0 unread messages
		dmMsgs, err := store.ReadAgentMessages(ctx, agentDM, 10)
		if err != nil {
			t.Fatalf("read DM messages: %v", err)
		}
		if len(dmMsgs) != 0 {
			t.Errorf("DM should have 0 unread messages, got %d", len(dmMsgs))
		}

		eventBus.Unsubscribe(agentSub)
	})

	// ----- Phase 3: Delegation -----
	t.Run("Phase3_Delegation", func(t *testing.T) {
		// DM creates a task for the warrior
		taskID := createDndTask(t, store, agentWarrior, "Attack the goblin with your greatsword!")

		// Wait for the warrior to process it
		task := dndWaitForStatus(t, store, taskID, persistence.TaskStatusSucceeded, 10*time.Second)

		// Parse the result: processor returns {"reply":"..."}
		var result struct {
			Reply string `json:"reply"`
		}
		if err := json.Unmarshal([]byte(task.Result), &result); err != nil {
			t.Fatalf("unmarshal result: %v", err)
		}

		// Parse the warrior's action from the reply
		var action struct {
			Action string `json:"action"`
			Weapon string `json:"weapon"`
			Damage int    `json:"damage"`
			Hit    bool   `json:"hit"`
		}
		if err := json.Unmarshal([]byte(result.Reply), &action); err != nil {
			t.Fatalf("unmarshal warrior action: %v", err)
		}

		if action.Action != "attack" {
			t.Errorf("expected action=attack, got %s", action.Action)
		}
		if action.Weapon != "greatsword" {
			t.Errorf("expected weapon=greatsword, got %s", action.Weapon)
		}
		if action.Damage != 12 {
			t.Errorf("expected damage=12, got %d", action.Damage)
		}
		if !action.Hit {
			t.Error("expected hit=true")
		}
	})

	// ----- Phase 4: Structured Output -----
	t.Run("Phase4_StructuredOutput", func(t *testing.T) {
		// Combat action schema
		combatSchema := json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string"},
				"weapon": {"type": "string"},
				"damage": {"type": "integer"},
				"hit":    {"type": "boolean"}
			},
			"required": ["action", "damage"]
		}`)

		combatValidator, err := engine.NewStructuredValidator(combatSchema, 1, true)
		if err != nil {
			t.Fatalf("create combat validator: %v", err)
		}

		// Heal schema
		healSchema := json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string"},
				"target": {"type": "string"},
				"amount": {"type": "integer"},
				"spell":  {"type": "string"}
			},
			"required": ["action", "target", "amount"]
		}`)

		healValidator, err := engine.NewStructuredValidator(healSchema, 1, true)
		if err != nil {
			t.Fatalf("create heal validator: %v", err)
		}

		// Validate 3 combat responses
		combatResponses := []string{
			`{"action":"attack","weapon":"greatsword","damage":12,"hit":true}`,
			`{"action":"sneak_attack","weapon":"dagger","damage":18,"hit":true}`,
			`{"action":"attack","weapon":"longbow","damage":10,"hit":true}`,
		}
		for i, resp := range combatResponses {
			result, err := combatValidator.ValidateResponse(resp)
			if err != nil {
				t.Errorf("combat response %d validation error: %v", i, err)
				continue
			}
			if !result.Valid {
				t.Errorf("combat response %d: expected valid", i)
			}
		}

		// Validate heal response
		healResp := `{"action":"heal","target":"warrior","amount":15,"spell":"cure_wounds"}`
		result, err := healValidator.ValidateResponse(healResp)
		if err != nil {
			t.Fatalf("heal validation error: %v", err)
		}
		if !result.Valid {
			t.Error("heal response should be valid")
		}

		// Reject invalid response (missing required field "damage")
		invalid := `{"action":"attack","weapon":"sword"}`
		_, err = combatValidator.ValidateResponse(invalid)
		if err == nil {
			t.Error("expected validation error for missing required field")
		}

		// Extract JSON from narrative text with fenced code block
		narrative := "The warrior swings mightily!\n```json\n{\"action\":\"attack\",\"damage\":12}\n```\nThe goblin falls."
		result, err = combatValidator.ValidateResponse(narrative)
		if err != nil {
			t.Fatalf("narrative extraction error: %v", err)
		}
		if !result.Valid {
			t.Error("expected valid JSON extracted from narrative")
		}
	})

	// ----- Phase 5: Agent Memory -----
	t.Run("Phase5_AgentMemory", func(t *testing.T) {
		// Create character stats for each player
		playerStats := map[string][]memory.KeyValue{
			agentWarrior: {
				{Key: "class", Value: "Fighter", RelevanceScore: 0.95},
				{Key: "hp", Value: "45/45", RelevanceScore: 0.9},
				{Key: "weapon", Value: "Greatsword +1", RelevanceScore: 0.85},
				{Key: "backstory_detail", Value: "once saw a cloud", RelevanceScore: 0.05}, // below threshold
			},
			agentRogue: {
				{Key: "class", Value: "Rogue", RelevanceScore: 0.95},
				{Key: "hp", Value: "32/32", RelevanceScore: 0.9},
				{Key: "weapon", Value: "Dagger of Venom", RelevanceScore: 0.85},
			},
			agentCleric: {
				{Key: "class", Value: "Cleric", RelevanceScore: 0.95},
				{Key: "hp", Value: "38/38", RelevanceScore: 0.9},
				{Key: "spell_slots", Value: "4/4", RelevanceScore: 0.8},
			},
			agentWizard: {
				{Key: "class", Value: "Wizard", RelevanceScore: 0.95},
				{Key: "hp", Value: "28/28", RelevanceScore: 0.9},
				{Key: "spell_slots", Value: "6/6", RelevanceScore: 0.8},
			},
			agentRanger: {
				{Key: "class", Value: "Ranger", RelevanceScore: 0.95},
				{Key: "hp", Value: "36/36", RelevanceScore: 0.9},
				{Key: "weapon", Value: "Longbow +1", RelevanceScore: 0.85},
			},
		}

		// Verify low-relevance filtered out (warrior has 4 entries, one below 0.1)
		warriorBlock := memory.NewCoreMemoryBlock(playerStats[agentWarrior])
		warriorFormatted := warriorBlock.Format()
		if containsStr(warriorFormatted, "backstory_detail") {
			t.Error("low-relevance memory should be filtered out")
		}
		if !containsStr(warriorFormatted, "class") {
			t.Error("high-relevance memory should be present")
		}

		// Verify sorted by relevance DESC (class=0.95 should appear before weapon=0.85)
		if idx1, idx2 := indexOf(warriorFormatted, "class"), indexOf(warriorFormatted, "weapon"); idx1 >= idx2 {
			t.Error("memories should be sorted by relevance DESC")
		}

		// Verify Format() contains <core_memory> tags
		if !containsStr(warriorFormatted, "<core_memory>") || !containsStr(warriorFormatted, "</core_memory>") {
			t.Error("Format() should wrap with <core_memory> tags")
		}

		// Verify EstimateTokens() > 0 and reasonable
		tokens := warriorBlock.EstimateTokens()
		if tokens <= 0 {
			t.Errorf("EstimateTokens() should be > 0, got %d", tokens)
		}
		if tokens > 500 {
			t.Errorf("EstimateTokens() unreasonably large: %d", tokens)
		}

		// Verify empty block returns ""
		emptyBlock := memory.NewCoreMemoryBlock(nil)
		if emptyBlock.Format() != "" {
			t.Error("empty block Format() should return empty string")
		}
		if emptyBlock.EstimateTokens() != 0 {
			t.Error("empty block EstimateTokens() should return 0")
		}

		// Verify 5 players have distinct formatted memories
		seen := make(map[string]bool)
		for _, id := range players {
			block := memory.NewCoreMemoryBlock(playerStats[id])
			formatted := block.Format()
			if formatted == "" {
				t.Errorf("agent %s has empty memory", id)
			}
			if seen[formatted] {
				t.Errorf("agent %s has duplicate memory format", id)
			}
			seen[formatted] = true
		}
	})

	// ----- Phase 6: Event Bus -----
	t.Run("Phase6_EventBus", func(t *testing.T) {
		// Subscribe to task.* and all events
		taskSub := eventBus.Subscribe("task.")
		allSub := eventBus.Subscribe("")

		initialCount := eventBus.SubscriberCount()
		if initialCount < 2 {
			t.Errorf("expected at least 2 subscribers, got %d", initialCount)
		}

		// Create 3 tasks across different agents
		taskIDs := []string{
			createDndTask(t, store, agentWarrior, "Swing sword"),
			createDndTask(t, store, agentRogue, "Pick lock"),
			createDndTask(t, store, agentWizard, "Cast spell"),
		}
		for _, taskID := range taskIDs {
			dndWaitForStatus(t, store, taskID, persistence.TaskStatusSucceeded, 10*time.Second)
		}

		// Drain events with timeout
		var taskEvents []bus.Event
		var allEvents []bus.Event

		drainDone := time.After(2 * time.Second)
	drain:
		for {
			select {
			case ev := <-taskSub.Ch():
				taskEvents = append(taskEvents, ev)
			case ev := <-allSub.Ch():
				allEvents = append(allEvents, ev)
			case <-drainDone:
				break drain
			}
		}

		// Verify task events have "task." prefix
		for _, ev := range taskEvents {
			if len(ev.Topic) < 5 || ev.Topic[:5] != "task." {
				t.Errorf("task event has wrong prefix: %s", ev.Topic)
			}
		}

		// All-topic subscriber should have received at least as many events
		if len(allEvents) < len(taskEvents) {
			t.Errorf("all-events (%d) should be >= task-events (%d)", len(allEvents), len(taskEvents))
		}

		if len(taskEvents) == 0 {
			t.Error("expected at least some task events")
		}

		// Test subscribe/unsubscribe changes SubscriberCount
		beforeUnsub := eventBus.SubscriberCount()
		eventBus.Unsubscribe(taskSub)
		eventBus.Unsubscribe(allSub)
		afterUnsub := eventBus.SubscriberCount()
		if afterUnsub >= beforeUnsub {
			t.Errorf("SubscriberCount should decrease after Unsubscribe: before=%d after=%d", beforeUnsub, afterUnsub)
		}
	})
}

// ---------------------------------------------------------------------------
// String helpers
// ---------------------------------------------------------------------------

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
