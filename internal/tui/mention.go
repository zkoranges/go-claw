package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// MentionResult holds the parsed result of an @mention in user input.
type MentionResult struct {
	AgentID string // Target agent ID (empty if no mention)
	Message string // Message with @mention stripped
	Sticky  bool   // True if @@ was used or @agent with no message
}

// ParseMention extracts @agent or @@agent from the beginning of input.
//
// Rules:
//   - /commands are never treated as mentions
//   - @agent <msg> routes single message (Sticky=false)
//   - @agent with no message = sticky switch (same as @@agent)
//   - @@agent switches permanently (Sticky=true)
//   - @@agent <msg> switches permanently and sends message
//   - Agent IDs: a-z, A-Z, 0-9, hyphens. Must start alphanumeric. No trailing hyphen.
func ParseMention(input string) MentionResult {
	input = strings.TrimSpace(input)

	// Commands take priority
	if strings.HasPrefix(input, "/") {
		return MentionResult{Message: input}
	}

	// Check for @@ (sticky switch)
	if strings.HasPrefix(input, "@@") {
		rest := input[2:]
		agentID, message := extractAgentID(rest)
		if agentID != "" {
			return MentionResult{
				AgentID: agentID,
				Message: strings.TrimSpace(message),
				Sticky:  true,
			}
		}
		return MentionResult{Message: input}
	}

	// Check for @ (single message or sticky if no message)
	if strings.HasPrefix(input, "@") {
		rest := input[1:]
		agentID, message := extractAgentID(rest)
		if agentID != "" {
			msg := strings.TrimSpace(message)
			return MentionResult{
				AgentID: agentID,
				Message: msg,
				Sticky:  msg == "", // no message = sticky
			}
		}
		return MentionResult{Message: input}
	}

	return MentionResult{Message: input}
}

// extractAgentID pulls a valid agent ID token from the start of text.
func extractAgentID(text string) (agentID string, rest string) {
	if len(text) == 0 {
		return "", text
	}
	if !isAgentIDStart(rune(text[0])) {
		return "", text
	}

	for i, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
			id := text[:i]
			if id[len(id)-1] == '-' {
				return "", text
			}
			return id, text[i:]
		}
		if !isAgentIDChar(r) {
			return "", text
		}
	}

	id := text
	if id[len(id)-1] == '-' {
		return "", text
	}
	return id, ""
}

func isAgentIDStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

func isAgentIDChar(r rune) bool {
	return isAgentIDStart(r) || r == '-'
}

// HighlightMention renders @mentions in cyan in user messages.
func HighlightMention(content string) string {
	if !strings.HasPrefix(content, "@") {
		return content
	}
	spaceIdx := strings.IndexByte(content, ' ')
	if spaceIdx == -1 {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(content)
	}
	mention := content[:spaceIdx]
	rest := content[spaceIdx:]
	return lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(mention) + rest
}
