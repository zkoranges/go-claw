package memory

import (
	"strings"
	"testing"
)

func TestCoreMemoryBlock(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "empty block returns empty string",
			fn: func(t *testing.T) {
				block := NewCoreMemoryBlock([]KeyValue{})
				formatted := block.Format()
				if formatted != "" {
					t.Errorf("expected empty string, got %q", formatted)
				}
			},
		},
		{
			name: "single memory formats correctly",
			fn: func(t *testing.T) {
				memories := []KeyValue{
					{Key: "language", Value: "Go", RelevanceScore: 1.0},
				}
				block := NewCoreMemoryBlock(memories)
				formatted := block.Format()

				if !strings.Contains(formatted, "<core_memory>") {
					t.Errorf("missing opening tag")
				}
				if !strings.Contains(formatted, "language: Go") {
					t.Errorf("missing memory entry")
				}
				if !strings.Contains(formatted, "</core_memory>") {
					t.Errorf("missing closing tag")
				}
			},
		},
		{
			name: "multiple memories format in relevance order",
			fn: func(t *testing.T) {
				memories := []KeyValue{
					{Key: "project", Value: "go-claw", RelevanceScore: 0.8},
					{Key: "language", Value: "Go", RelevanceScore: 1.0},
					{Key: "style", Value: "concise", RelevanceScore: 0.5},
				}
				block := NewCoreMemoryBlock(memories)
				formatted := block.Format()

				// Check all entries present
				if !strings.Contains(formatted, "project: go-claw") {
					t.Errorf("missing project entry")
				}
				if !strings.Contains(formatted, "language: Go") {
					t.Errorf("missing language entry")
				}
				if !strings.Contains(formatted, "style: concise") {
					t.Errorf("missing style entry")
				}

				// Check order: language (1.0) should come before project (0.8) should come before style (0.5)
				langIdx := strings.Index(formatted, "language: Go")
				projIdx := strings.Index(formatted, "project: go-claw")
				styleIdx := strings.Index(formatted, "style: concise")

				if !(langIdx < projIdx && projIdx < styleIdx) {
					t.Errorf("memories not in relevance order DESC")
				}
			},
		},
		{
			name: "filters memories below relevance threshold (0.1)",
			fn: func(t *testing.T) {
				memories := []KeyValue{
					{Key: "important", Value: "value", RelevanceScore: 0.5},
					{Key: "forgotten", Value: "decayed", RelevanceScore: 0.05},
					{Key: "very_decayed", Value: "almost_gone", RelevanceScore: 0.0},
				}
				block := NewCoreMemoryBlock(memories)
				formatted := block.Format()

				if !strings.Contains(formatted, "important: value") {
					t.Errorf("important memory should be included")
				}
				if strings.Contains(formatted, "forgotten") {
					t.Errorf("below-threshold memory should be filtered out")
				}
				if strings.Contains(formatted, "very_decayed") {
					t.Errorf("very low relevance memory should be filtered out")
				}
			},
		},
		{
			name: "token estimation",
			fn: func(t *testing.T) {
				memories := []KeyValue{
					{Key: "language", Value: "Go", RelevanceScore: 1.0},
				}
				block := NewCoreMemoryBlock(memories)
				tokens := block.EstimateTokens()

				if tokens <= 0 {
					t.Errorf("expected positive token count, got %d", tokens)
				}

				// Rough check: "<core_memory>\nlanguage: Go\n</core_memory>" is about 50 chars = ~12-13 tokens
				if tokens < 10 || tokens > 20 {
					t.Errorf("unexpected token count %d (expected ~12-13)", tokens)
				}
			},
		},
		{
			name: "large block token count",
			fn: func(t *testing.T) {
				memories := []KeyValue{
					{Key: "key1", Value: "value1", RelevanceScore: 1.0},
					{Key: "key2", Value: "value2", RelevanceScore: 0.9},
					{Key: "key3", Value: "a longer description value", RelevanceScore: 0.8},
				}
				block := NewCoreMemoryBlock(memories)
				tokens := block.EstimateTokens()

				if tokens <= 0 {
					t.Errorf("expected positive token count, got %d", tokens)
				}

				// With 3 memories and longer values, should be more than 20 tokens
				if tokens < 15 {
					t.Errorf("expected larger token count for 3 memories, got %d", tokens)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}
