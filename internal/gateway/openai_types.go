package gateway

import "encoding/json"

// ChatCompletionRequest represents an OpenAI-compatible chat completion request.
type ChatCompletionRequest struct {
	Model    string                  `json:"model"`
	Messages []ChatCompletionMessage `json:"messages"`
	Stream   bool                    `json:"stream,omitempty"`
	User     string                  `json:"user,omitempty"`
	Tools    []any                   `json:"tools,omitempty"`

	// Sampling parameters.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"` // non-standard but supported by Genkit
	MaxTokens   *int     `json:"max_tokens,omitempty"`
	Stop        []string `json:"stop,omitempty"`

	// Structured output.
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat specifies the format of the response.
type ResponseFormat struct {
	Type       string          `json:"type"`                  // "text", "json_object", or "json_schema"
	JSONSchema json.RawMessage `json:"json_schema,omitempty"` // JSON Schema object
}

// ChatCompletionMessage represents a message in the chat history.
type ChatCompletionMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a tool invocation in the response.
type ToolCall struct {
	Index    int          `json:"index"`
	ID       string       `json:"id"`
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction holds the name and arguments of the called function.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// ChatCompletionResponse represents the response from the chat completion API.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
}

// ChatCompletionChoice represents a single choice in the completion response.
type ChatCompletionChoice struct {
	Index        int                    `json:"index"`
	Message      *ChatCompletionMessage `json:"message,omitempty"`
	Delta        *ChatCompletionMessage `json:"delta,omitempty"`
	FinishReason *string                `json:"finish_reason,omitempty"`
}

// Usage represents token usage statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ModelListResponse represents the response from the models list API.
type ModelListResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model represents a single model in the list.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
