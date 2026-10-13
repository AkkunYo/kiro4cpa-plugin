package kiro

import (
	"encoding/json"
	"strings"
)

// claudeRequest is the subset of an Anthropic Messages request the converter reads.
type claudeRequest struct {
	Model        string          `json:"model"`
	System       json.RawMessage `json:"system,omitempty"`
	Messages     []claudeMessage `json:"messages"`
	Tools        []claudeTool    `json:"tools,omitempty"`
	Thinking     *claudeThinking `json:"thinking,omitempty"`
	OutputConfig *struct {
		Effort string `json:"effort"`
	} `json:"output_config,omitempty"`
	Metadata *struct {
		UserID string `json:"user_id"`
	} `json:"metadata,omitempty"`
}

type claudeMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudeThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type claudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Source    *struct {
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source,omitempty"`
}

// blocks accepts both the string and the array form of a content field.
func blocks(raw json.RawMessage) []claudeBlock {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []claudeBlock{{Type: "text", Text: s}}
	}
	var out []claudeBlock
	_ = json.Unmarshal(raw, &out)
	return out
}

func (r claudeRequest) systemText() string {
	var parts []string
	for _, b := range blocks(r.System) {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}
