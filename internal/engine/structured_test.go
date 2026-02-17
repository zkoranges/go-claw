package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
)

// testSchema is used across most validation tests.
var testSchema = json.RawMessage(`{
	"type": "object",
	"properties": {
		"category": {"type": "string", "enum": ["bug", "feature"]},
		"confidence": {"type": "number", "minimum": 0, "maximum": 1}
	},
	"required": ["category", "confidence"]
}`)

// --- JSON extraction tests ---

func TestExtractJSON_FencedBlock(t *testing.T) {
	input := "Here is the result:\n```json\n{\"category\": \"bug\", \"confidence\": 0.9}\n```\nDone."
	got := extractJSON(input)
	if got == "" {
		t.Fatal("expected JSON extraction from fenced block, got empty")
	}
	if !isJSON(got) {
		t.Fatalf("extracted string is not valid JSON: %q", got)
	}
}

func TestExtractJSON_GenericFenced(t *testing.T) {
	input := "Output:\n```\n{\"category\": \"feature\", \"confidence\": 0.5}\n```\n"
	got := extractJSON(input)
	if got == "" {
		t.Fatal("expected JSON extraction from generic fenced block, got empty")
	}
	if !isJSON(got) {
		t.Fatalf("extracted string is not valid JSON: %q", got)
	}
}

func TestExtractJSON_RawObject(t *testing.T) {
	input := `{"category": "bug", "confidence": 0.8}`
	got := extractJSON(input)
	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

func TestExtractJSON_RawArray(t *testing.T) {
	input := `[1, 2, 3]`
	got := extractJSON(input)
	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

func TestExtractJSON_NestedObjects(t *testing.T) {
	input := `{"outer": {"inner": {"deep": true}}, "list": [1, {"a": 2}]}`
	got := extractJSON(input)
	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

func TestExtractJSON_NoJSON(t *testing.T) {
	input := "This is just plain text without any JSON."
	got := extractJSON(input)
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractJSON_TextAroundJSON(t *testing.T) {
	input := `I analyzed the issue and here's my assessment: {"category": "bug", "confidence": 0.95} — that's my conclusion.`
	got := extractJSON(input)
	if got == "" {
		t.Fatal("expected JSON extraction from text with surrounding content, got empty")
	}
	if !isJSON(got) {
		t.Fatalf("extracted string is not valid JSON: %q", got)
	}
	// Verify parsed content
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("unmarshal extracted JSON: %v", err)
	}
	if m["category"] != "bug" {
		t.Fatalf("expected category=bug, got %v", m["category"])
	}
}

// --- Validation tests ---

func TestValidateResponse_Valid(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	result, err := sv.ValidateResponse(`{"category": "bug", "confidence": 0.9}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Valid {
		t.Fatal("expected valid result")
	}
	if result.JSON == "" {
		t.Fatal("expected non-empty JSON field")
	}
	if result.Parsed == nil {
		t.Fatal("expected non-nil Parsed field")
	}
}

func TestValidateResponse_InvalidSchema(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// Missing required "confidence" field
	_, err = sv.ValidateResponse(`{"category": "bug"}`)
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
	var valErr *ValidationError
	if ve, ok := err.(*ValidationError); ok {
		valErr = ve
	}
	if valErr == nil {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
}

func TestValidateResponse_MissingJSON_Strict(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, true)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	_, err = sv.ValidateResponse("No JSON here, just text.")
	if err == nil {
		t.Fatal("expected error in strict mode for missing JSON")
	}
	var valErr *ValidationError
	if ve, ok := err.(*ValidationError); ok {
		valErr = ve
	}
	if valErr == nil {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if valErr.Raw != "No JSON here, just text." {
		t.Fatalf("expected raw text in error, got %q", valErr.Raw)
	}
}

func TestValidateResponse_MissingJSON_NonStrict(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	result, err := sv.ValidateResponse("No JSON here, just text.")
	if err != nil {
		t.Fatalf("unexpected error in non-strict mode: %v", err)
	}
	if result.Valid {
		t.Fatal("expected invalid result for missing JSON in non-strict mode")
	}
	if result.Warning == "" {
		t.Fatal("expected warning message")
	}
	if result.Raw != "No JSON here, just text." {
		t.Fatalf("expected raw text preserved, got %q", result.Raw)
	}
}

func TestValidateResponse_InvalidJSON(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// This has a JSON-like structure but is actually invalid JSON (no quotes on key)
	// The extractJSON will find the balanced braces but UnmarshalJSON should fail...
	// Actually {broken is not valid JSON so isJSON returns false, so extractJSON returns empty.
	// Let's use a fenced block to force extraction of invalid JSON.
	input := "```json\n{broken json\n```"
	result, err := sv.ValidateResponse(input)
	if err != nil {
		// In non-strict mode with no valid JSON found, we get a warning, not error.
		// But the fenced block extraction returns the string even if not valid JSON.
		// It will hit the UnmarshalJSON path and return a ValidationError.
		var valErr *ValidationError
		if ve, ok := err.(*ValidationError); ok {
			valErr = ve
		}
		if valErr == nil {
			t.Fatalf("expected *ValidationError, got %T: %v", err, err)
		}
		return
	}
	// If we get here, it means no JSON was extracted (fenced block had invalid content)
	if result.Valid {
		t.Fatal("should not be valid")
	}
}

func TestValidateResponse_TypeMismatch(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// confidence should be number, not string
	_, err = sv.ValidateResponse(`{"category": "bug", "confidence": "high"}`)
	if err == nil {
		t.Fatal("expected validation error for type mismatch")
	}
	var valErr *ValidationError
	if ve, ok := err.(*ValidationError); ok {
		valErr = ve
	}
	if valErr == nil {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
}

func TestValidateResponse_EnumMismatch(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// category "enhancement" is not in enum ["bug", "feature"]
	_, err = sv.ValidateResponse(`{"category": "enhancement", "confidence": 0.5}`)
	if err == nil {
		t.Fatal("expected validation error for enum mismatch")
	}
}

func TestValidateResponse_NumberOutOfRange(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// confidence 1.5 is above maximum 1
	_, err = sv.ValidateResponse(`{"category": "bug", "confidence": 1.5}`)
	if err == nil {
		t.Fatal("expected validation error for number out of range")
	}
}

func TestNewStructuredValidator_InvalidSchema(t *testing.T) {
	badSchema := json.RawMessage(`{this is not valid json}`)
	_, err := NewStructuredValidator(badSchema, 2, false)
	if err == nil {
		t.Fatal("expected error for invalid schema JSON")
	}
}

func TestNewStructuredValidator_DefaultRetries(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 0, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	if sv.MaxRetries() != 2 {
		t.Fatalf("expected default maxRetries=2, got %d", sv.MaxRetries())
	}
}

func TestStructuredValidator_SchemaJSON(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 3, true)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}
	if sv.SchemaJSON() == nil {
		t.Fatal("expected non-nil SchemaJSON")
	}
	if sv.MaxRetries() != 3 {
		t.Fatalf("expected maxRetries=3, got %d", sv.MaxRetries())
	}
}

// --- ValidateAndRetry tests ---

// structuredMockBrain implements Brain for structured output retry tests.
// Uses a response list with sequential index, unlike the callback-based
// mockBrain in failover_test.go.
type structuredMockBrain struct {
	responses []string
	idx       int
}

func (m *structuredMockBrain) Respond(_ context.Context, _, _ string) (string, error) {
	if m.idx >= len(m.responses) {
		return "no response", nil
	}
	r := m.responses[m.idx]
	m.idx++
	return r, nil
}

func (m *structuredMockBrain) Stream(ctx context.Context, sessionID, content string, onChunk func(string) error) error {
	r, err := m.Respond(ctx, sessionID, content)
	if err != nil {
		return err
	}
	return onChunk(r)
}

func TestValidateAndRetry_FirstTrySuccess(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	brain := &structuredMockBrain{} // should not be called
	sessionID := uuid.New().String()

	validJSON, parsed, valErr, err := ValidateAndRetry(
		context.Background(), brain, sessionID, sv,
		`{"category": "feature", "confidence": 0.7}`,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valErr != "" {
		t.Fatalf("unexpected validation error: %s", valErr)
	}
	if validJSON == "" {
		t.Fatal("expected non-empty validJSON")
	}
	if parsed == nil {
		t.Fatal("expected non-nil parsed")
	}
}

func TestValidateAndRetry_RetriesOnFailure(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// First response is invalid (missing confidence), second is valid
	brain := &structuredMockBrain{
		responses: []string{
			`{"category": "bug", "confidence": 0.85}`,
		},
	}
	sessionID := uuid.New().String()

	// Initial response is invalid (missing required field)
	validJSON, parsed, valErr, err := ValidateAndRetry(
		context.Background(), brain, sessionID, sv,
		`{"category": "bug"}`,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valErr != "" {
		t.Fatalf("unexpected validation error: %s", valErr)
	}
	if validJSON == "" {
		t.Fatal("expected non-empty validJSON after retry")
	}
	if parsed == nil {
		t.Fatal("expected non-nil parsed after retry")
	}
}

func TestValidateAndRetry_ExhaustsRetries(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 1, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// All responses are invalid
	brain := &structuredMockBrain{
		responses: []string{
			`{"category": "invalid"}`,
		},
	}
	sessionID := uuid.New().String()

	validJSON, parsed, valErr, err := ValidateAndRetry(
		context.Background(), brain, sessionID, sv,
		`{"category": "invalid"}`,
	)
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if valErr == "" {
		t.Fatal("expected validation error after exhausting retries")
	}
	if validJSON != "" {
		t.Fatalf("expected empty validJSON, got %q", validJSON)
	}
	if parsed != nil {
		t.Fatal("expected nil parsed")
	}
}

func TestValidateAndRetry_NilValidator(t *testing.T) {
	brain := &structuredMockBrain{}
	sessionID := uuid.New().String()

	validJSON, parsed, valErr, err := ValidateAndRetry(
		context.Background(), brain, sessionID, nil,
		`anything`,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if valErr != "" {
		t.Fatalf("unexpected validation error: %s", valErr)
	}
	if validJSON != "" {
		t.Fatalf("expected empty validJSON for nil validator, got %q", validJSON)
	}
	if parsed != nil {
		t.Fatal("expected nil parsed for nil validator")
	}
}

func TestValidateAndRetry_BrainError(t *testing.T) {
	sv, err := NewStructuredValidator(testSchema, 2, false)
	if err != nil {
		t.Fatalf("new validator: %v", err)
	}

	// Brain that always fails
	brain := &structuredErrorBrain{err: fmt.Errorf("LLM unavailable")}
	sessionID := uuid.New().String()

	_, _, _, err = ValidateAndRetry(
		context.Background(), brain, sessionID, sv,
		`{"category": "invalid"}`, // initial invalid response triggers retry
	)
	if err == nil {
		t.Fatal("expected error from brain failure")
	}
}

// structuredErrorBrain always returns an error.
type structuredErrorBrain struct {
	err error
}

func (b *structuredErrorBrain) Respond(_ context.Context, _, _ string) (string, error) {
	return "", b.err
}

func (b *structuredErrorBrain) Stream(_ context.Context, _, _ string, _ func(string) error) error {
	return b.err
}

// --- extractBalanced edge cases ---

func TestExtractBalanced_Empty(t *testing.T) {
	got := extractBalanced("")
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestExtractBalanced_Unclosed(t *testing.T) {
	got := extractBalanced(`{"key": "value"`)
	if got != "" {
		t.Fatalf("expected empty for unclosed brace, got %q", got)
	}
}

func TestExtractBalanced_StringWithBraces(t *testing.T) {
	input := `{"msg": "hello { world }"}`
	got := extractBalanced(input)
	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

func TestExtractBalanced_EscapedQuotes(t *testing.T) {
	input := `{"msg": "say \"hello\""}`
	got := extractBalanced(input)
	if got != input {
		t.Fatalf("expected %q, got %q", input, got)
	}
}

func TestExtractJSON_FencedBlockWithExtraWhitespace(t *testing.T) {
	input := "```json\n  {\"a\": 1}  \n```"
	got := extractJSON(input)
	if got == "" {
		t.Fatal("expected JSON from fenced block with whitespace")
	}
	if !isJSON(got) {
		t.Fatalf("not valid JSON: %q", got)
	}
}
