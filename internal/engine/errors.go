package engine

import "strings"

// ErrorClass categorizes LLM errors for failover decision-making.
type ErrorClass string

const (
	// ErrorClassAuth indicates authentication/authorization failures (401, invalid key).
	ErrorClassAuth ErrorClass = "AUTH"

	// ErrorClassRateLimit indicates rate limiting or quota exhaustion (429).
	ErrorClassRateLimit ErrorClass = "RATE_LIMIT"

	// ErrorClassTimeout indicates request timeout or deadline exceeded.
	ErrorClassTimeout ErrorClass = "TIMEOUT"

	// ErrorClassBilling indicates billing or payment issues.
	ErrorClassBilling ErrorClass = "BILLING"

	// ErrorClassContextOverflow indicates the prompt exceeded the model's context window.
	ErrorClassContextOverflow ErrorClass = "CONTEXT_OVERFLOW"

	// ErrorClassUnknown is the default for unrecognized errors.
	ErrorClassUnknown ErrorClass = "UNKNOWN"
)

// ClassifyError categorizes an LLM error for failover decisions.
// It inspects the error message for known patterns and returns the
// most specific ErrorClass that matches.
func ClassifyError(err error) ErrorClass {
	if err == nil {
		return ErrorClassUnknown
	}
	msg := strings.ToLower(err.Error())

	// Auth errors: 401, unauthorized, invalid key, forbidden, 403.
	if strings.Contains(msg, "401") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "invalid key") ||
		strings.Contains(msg, "invalid api key") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "403") {
		return ErrorClassAuth
	}

	// Rate limit: 429, rate limit, quota exceeded, too many requests.
	if strings.Contains(msg, "429") ||
		strings.Contains(msg, "rate limit") ||
		strings.Contains(msg, "rate_limit") ||
		strings.Contains(msg, "quota") ||
		strings.Contains(msg, "too many requests") {
		return ErrorClassRateLimit
	}

	// Timeout: deadline exceeded, timeout, timed out.
	if strings.Contains(msg, "deadline exceeded") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "timed out") {
		return ErrorClassTimeout
	}

	// Billing: billing, payment, insufficient funds.
	if strings.Contains(msg, "billing") ||
		strings.Contains(msg, "payment") ||
		strings.Contains(msg, "insufficient funds") {
		return ErrorClassBilling
	}

	// Context overflow: context_length, token limit, max tokens, context window.
	if strings.Contains(msg, "context_length") ||
		strings.Contains(msg, "context length") ||
		strings.Contains(msg, "token limit") ||
		strings.Contains(msg, "max tokens") ||
		strings.Contains(msg, "maximum context") ||
		strings.Contains(msg, "context window") {
		return ErrorClassContextOverflow
	}

	return ErrorClassUnknown
}
