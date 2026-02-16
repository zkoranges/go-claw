package persistence

import (
	"context"
	"path/filepath"
	"testing"
)

func TestShares_AddAndRetrieve(t *testing.T) {
	t.Run("share_memory_from_agent_a_to_agent_b", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add memory to agent A
		err = store.SetMemory(ctx, "agent-a", "project-name", "go-claw", "user")
		if err != nil {
			t.Fatalf("SetMemory failed: %v", err)
		}

		// Share from A to B
		err = store.AddShare(ctx, "agent-a", "agent-b", "memory", "")
		if err != nil {
			t.Fatalf("AddShare failed: %v", err)
		}

		// Agent B should see the memory via GetSharedMemories
		sharedMems, err := store.GetSharedMemories(ctx, "agent-b")
		if err != nil {
			t.Fatalf("GetSharedMemories failed: %v", err)
		}

		found := false
		for _, mem := range sharedMems {
			if mem.AgentID == "agent-a" && mem.Key == "project-name" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("shared memory not found via GetSharedMemories")
		}
	})

	t.Run("share_pin_from_agent_a_to_agent_b", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add pin to agent A
		err = store.AddPin(ctx, "agent-a", "text", "notes", "Important stuff", false)
		if err != nil {
			t.Fatalf("AddPin failed: %v", err)
		}

		// Share pin from A to B
		err = store.AddShare(ctx, "agent-a", "agent-b", "pin", "")
		if err != nil {
			t.Fatalf("AddShare failed: %v", err)
		}

		// Agent B should see the pin via GetSharedPinsForAgent
		sharedPins, err := store.GetSharedPinsForAgent(ctx, "agent-b")
		if err != nil {
			t.Fatalf("GetSharedPinsForAgent failed: %v", err)
		}

		found := false
		for _, pin := range sharedPins {
			if pin.AgentID == "agent-a" && pin.Source == "notes" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("shared pin not found via GetSharedPinsForAgent")
		}
	})

	t.Run("agent_c_cannot_see_shares_between_a_and_b", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add memory to agent A and share with B only
		store.SetMemory(ctx, "agent-a", "secret-key", "secret-value", "user")
		store.AddShare(ctx, "agent-a", "agent-b", "memory", "")

		// Agent C should NOT see it
		sharedMems, err := store.GetSharedMemories(ctx, "agent-c")
		if err != nil {
			t.Fatalf("GetSharedMemories failed: %v", err)
		}

		for _, mem := range sharedMems {
			if mem.AgentID == "agent-a" && mem.Key == "secret-key" {
				t.Errorf("agent-c should not see memory shared only with agent-b")
			}
		}
	})

	t.Run("share_with_wildcard_asterisk", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add memory and share with all agents
		store.SetMemory(ctx, "agent-a", "public-fact", "public-value", "user")
		err = store.AddShare(ctx, "agent-a", "*", "memory", "")
		if err != nil {
			t.Fatalf("AddShare with wildcard failed: %v", err)
		}

		// All agents (including agent-z) should see it
		sharedMems, err := store.GetSharedMemories(ctx, "agent-z")
		if err != nil {
			t.Fatalf("GetSharedMemories failed: %v", err)
		}

		found := false
		for _, mem := range sharedMems {
			if mem.AgentID == "agent-a" && mem.Key == "public-fact" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("public memory not found for agent-z")
		}
	})
}

func TestShares_Remove(t *testing.T) {
	t.Run("remove_share_revokes_access", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add and share memory
		store.SetMemory(ctx, "agent-a", "key1", "value1", "user")
		err = store.AddShare(ctx, "agent-a", "agent-b", "memory", "")
		if err != nil {
			t.Fatalf("AddShare failed: %v", err)
		}

		// Verify B can see it
		mems, _ := store.GetSharedMemories(ctx, "agent-b")
		if len(mems) == 0 {
			t.Fatalf("shared memory should be visible before removal")
		}

		// Remove share
		err = store.RemoveShare(ctx, "agent-a", "agent-b", "memory", "")
		if err != nil {
			t.Fatalf("RemoveShare failed: %v", err)
		}

		// Verify B can no longer see it
		mems, _ = store.GetSharedMemories(ctx, "agent-b")
		found := false
		for _, mem := range mems {
			if mem.AgentID == "agent-a" {
				found = true
				break
			}
		}
		if found {
			t.Errorf("memory should not be visible after share removal")
		}
	})
}

func TestShares_List(t *testing.T) {
	t.Run("list_shares_for_agent", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Create multiple shares to agent-b
		store.AddShare(ctx, "agent-a", "agent-b", "memory", "")
		store.AddShare(ctx, "agent-c", "agent-b", "pin", "")
		store.AddShare(ctx, "agent-d", "agent-b", "all", "")

		shares, err := store.ListSharesFor(ctx, "agent-b")
		if err != nil {
			t.Fatalf("ListSharesFor failed: %v", err)
		}

		if len(shares) != 3 {
			t.Errorf("expected 3 shares, got %d", len(shares))
		}

		// Verify sources
		sources := make(map[string]bool)
		for _, share := range shares {
			sources[share.SourceAgentID] = true
		}
		if !sources["agent-a"] || !sources["agent-c"] || !sources["agent-d"] {
			t.Errorf("expected all three source agents in shares list")
		}
	})
}

func TestShares_SpecificKey(t *testing.T) {
	t.Run("share_specific_key_only", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add multiple memories to agent-a
		store.SetMemory(ctx, "agent-a", "public-key", "public-value", "user")
		store.SetMemory(ctx, "agent-a", "private-key", "private-value", "user")

		// Share only public-key with agent-b
		err = store.AddShare(ctx, "agent-a", "agent-b", "memory", "public-key")
		if err != nil {
			t.Fatalf("AddShare failed: %v", err)
		}

		// Check specific memory is accessible
		isShared, err := store.IsMemoryShared(ctx, "agent-a", "agent-b", "public-key")
		if err != nil {
			t.Fatalf("IsMemoryShared failed: %v", err)
		}
		if !isShared {
			t.Errorf("public-key should be shared")
		}

		// Check private-key is NOT accessible
		isShared, err = store.IsMemoryShared(ctx, "agent-a", "agent-b", "private-key")
		if err != nil {
			t.Fatalf("IsMemoryShared failed: %v", err)
		}
		if isShared {
			t.Errorf("private-key should not be shared")
		}
	})
}

func TestShares_Isolation(t *testing.T) {
	t.Run("shares_isolated_per_target_agent", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add memories to agent-a
		store.SetMemory(ctx, "agent-a", "fact1", "value1", "user")
		store.SetMemory(ctx, "agent-a", "fact2", "value2", "user")

		// Share fact1 with agent-b, fact2 with agent-c
		store.AddShare(ctx, "agent-a", "agent-b", "memory", "fact1")
		store.AddShare(ctx, "agent-a", "agent-c", "memory", "fact2")

		// Check agent-b sees only fact1
		isShared, _ := store.IsMemoryShared(ctx, "agent-a", "agent-b", "fact1")
		if !isShared {
			t.Errorf("agent-b should see fact1")
		}

		isShared, _ = store.IsMemoryShared(ctx, "agent-a", "agent-b", "fact2")
		if isShared {
			t.Errorf("agent-b should not see fact2")
		}

		// Check agent-c sees only fact2
		isShared, _ = store.IsMemoryShared(ctx, "agent-a", "agent-c", "fact1")
		if isShared {
			t.Errorf("agent-c should not see fact1")
		}

		isShared, _ = store.IsMemoryShared(ctx, "agent-a", "agent-c", "fact2")
		if !isShared {
			t.Errorf("agent-c should see fact2")
		}
	})
}

func TestShares_DuplicateAndTypes(t *testing.T) {
	t.Run("duplicate_share_is_noop", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		// Add same share twice
		err = store.AddShare(ctx, "agent-a", "agent-b", "memory", "")
		if err != nil {
			t.Fatalf("first AddShare failed: %v", err)
		}

		err = store.AddShare(ctx, "agent-a", "agent-b", "memory", "")
		if err != nil {
			t.Fatalf("second AddShare failed: %v", err)
		}

		// Should still have only one share
		shares, err := store.ListSharesFor(ctx, "agent-b")
		if err != nil {
			t.Fatalf("ListSharesFor failed: %v", err)
		}

		count := 0
		for _, s := range shares {
			if s.SourceAgentID == "agent-a" && s.ShareType == "memory" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected 1 share, got %d", count)
		}
	})

	t.Run("share_types_isolation", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "test.db")
		store, err := Open(dbPath, nil)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		defer store.Close()

		ctx := context.Background()

		store.SetMemory(ctx, "agent-a", "key1", "value1", "user")
		store.AddPin(ctx, "agent-a", "text", "pin1", "content", false)

		// Share memory only
		store.AddShare(ctx, "agent-a", "agent-b", "memory", "")

		// Agent B should see memory but NOT pins
		mems, _ := store.GetSharedMemories(ctx, "agent-b")
		if len(mems) == 0 {
			t.Fatalf("should see shared memories")
		}

		pins, _ := store.GetSharedPinsForAgent(ctx, "agent-b")
		if len(pins) > 0 {
			t.Errorf("should not see pins when only memory is shared")
		}

		// Share pins only
		store.AddShare(ctx, "agent-a", "agent-c", "pin", "")

		mems, _ = store.GetSharedMemories(ctx, "agent-c")
		if len(mems) > 0 {
			t.Errorf("should not see memories when only pins are shared")
		}

		pins, _ = store.GetSharedPinsForAgent(ctx, "agent-c")
		if len(pins) == 0 {
			t.Fatalf("should see shared pins")
		}
	})
}
