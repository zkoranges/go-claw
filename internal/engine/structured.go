package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// StructuredValidator validates agent responses against a JSON Schema.
type StructuredValidator struct {
	schema     *jsonschema.Schema
	schemaJSON json.RawMessage
	maxRetries int
	strictMode bool
}

// NewStructuredValidator compiles a JSON Schema for validation.
func NewStructuredValidator(schemaJSON json.RawMessage, maxRetries int, strict bool) (*StructuredValidator, error) {
	// Use jsonschema.UnmarshalJSON for correct number handling (json.Number).
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemaJSON)))
	if err != nil {
		return nil, fmt.Errorf("unmarshal schema JSON: %w", err)
	}

	c := jsonschema.NewCompiler()
	if err := c.AddResource("schema.json", doc); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	schema, err := c.Compile("schema.json")
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	if maxRetries == 0 {
		maxRetries = 2
	}
	return &StructuredValidator{
		schema:     schema,
		schemaJSON: schemaJSON,
		maxRetries: maxRetries,
		strictMode: strict,
	}, nil
}

// SchemaJSON returns the raw schema for provider-level injection.
func (sv *StructuredValidator) SchemaJSON() json.RawMessage {
	return sv.schemaJSON
}

// MaxRetries returns the configured max retries.
func (sv *StructuredValidator) MaxRetries() int {
	return sv.maxRetries
}

// StructuredResult is the outcome of validating a response.
type StructuredResult struct {
	Valid   bool
	Raw     string
	JSON    string
	Parsed  any
	Warning string
}

// ValidationError describes a schema validation failure.
type ValidationError struct {
	Message string
	Raw     string
	Parsed  any
}

func (e *ValidationError) Error() string { return e.Message }

// ValidateResponse extracts JSON from the agent's response and validates it against the schema.
func (sv *StructuredValidator) ValidateResponse(responseText string) (*StructuredResult, error) {
	jsonStr := extractJSON(responseText)
	if jsonStr == "" {
		if sv.strictMode {
			return nil, &ValidationError{
				Message: "response does not contain valid JSON",
				Raw:     responseText,
			}
		}
		return &StructuredResult{
			Valid:   false,
			Raw:     responseText,
			Warning: "no JSON found in response; passing through raw text",
		}, nil
	}

	// Use jsonschema.UnmarshalJSON for correct number handling (json.Number
	// instead of float64), which is required by the validator.
	parsed, err := jsonschema.UnmarshalJSON(strings.NewReader(jsonStr))
	if err != nil {
		return nil, &ValidationError{
			Message: fmt.Sprintf("invalid JSON: %s", err),
			Raw:     responseText,
		}
	}

	if err := sv.schema.Validate(parsed); err != nil {
		return nil, &ValidationError{
			Message: fmt.Sprintf("schema validation failed: %s", err),
			Raw:     responseText,
			Parsed:  parsed,
		}
	}

	return &StructuredResult{
		Valid:  true,
		Raw:    responseText,
		JSON:   jsonStr,
		Parsed: parsed,
	}, nil
}

// extractJSON finds a JSON object or array in the response text.
func extractJSON(text string) string {
	// 1. Try fenced JSON block: ```json\n...\n```
	if idx := strings.Index(text, "```json"); idx >= 0 {
		start := idx + 7
		// Skip optional newline after ```json
		if start < len(text) && text[start] == '\n' {
			start++
		}
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if candidate != "" {
				return candidate
			}
		}
	}

	// 2. Try generic fenced block: ```\n...\n```
	if idx := strings.Index(text, "```\n"); idx >= 0 {
		start := idx + 4
		if end := strings.Index(text[start:], "```"); end >= 0 {
			candidate := strings.TrimSpace(text[start : start+end])
			if isJSON(candidate) {
				return candidate
			}
		}
	}

	// 3. Try raw JSON: find first { or [ and match closing
	for i := 0; i < len(text); i++ {
		if text[i] == '{' || text[i] == '[' {
			candidate := extractBalanced(text[i:])
			if candidate != "" && isJSON(candidate) {
				return candidate
			}
		}
	}

	return ""
}

// isJSON checks if a string is valid JSON.
func isJSON(s string) bool {
	var v any
	return json.Unmarshal([]byte(s), &v) == nil
}

// extractBalanced extracts a balanced JSON structure from the start of the string.
func extractBalanced(s string) string {
	if len(s) == 0 {
		return ""
	}

	open := s[0]
	var close byte
	switch open {
	case '{':
		close = '}'
	case '[':
		close = ']'
	default:
		return ""
	}

	depth := 0
	inString := false
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if escaped {
			escaped = false
			continue
		}

		if ch == '\\' && inString {
			escaped = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if ch == open {
			depth++
		} else if ch == close {
			depth--
			if depth == 0 {
				return s[:i+1]
			}
		}
	}

	return ""
}

// ValidateAndRetry validates a response and retries if invalid.
// Returns the validated JSON string, parsed value, any validation error message,
// and a fatal error if retry generation itself fails.
func ValidateAndRetry(ctx context.Context, brain Brain, sessionID string, validator *StructuredValidator, responseText string) (validJSON string, parsed any, validationErr string, err error) {
	if validator == nil {
		return "", nil, "", nil
	}

	for attempt := 0; attempt <= validator.MaxRetries(); attempt++ {
		result, valErr := validator.ValidateResponse(responseText)
		if valErr == nil && result != nil && result.Valid {
			return result.JSON, result.Parsed, "", nil
		}

		if attempt == validator.MaxRetries() {
			// Exhausted retries
			if valErr != nil {
				return "", nil, valErr.Error(), nil
			}
			if result != nil {
				return "", nil, result.Warning, nil
			}
			return "", nil, "validation failed", nil
		}

		// Build error feedback message
		var errMsg string
		if valErr != nil {
			errMsg = valErr.Error()
		} else if result != nil {
			errMsg = result.Warning
		}

		// Retry: ask brain to fix the response
		retryPrompt := fmt.Sprintf(
			"Your response did not match the required JSON schema. Error: %s\n\n"+
				"Please try again, ensuring your response contains valid JSON matching the schema.",
			errMsg,
		)

		responseText, err = brain.Respond(ctx, sessionID, retryPrompt)
		if err != nil {
			return "", nil, "", fmt.Errorf("retry generate: %w", err)
		}
	}

	return "", nil, "validation failed after retries", nil
}
