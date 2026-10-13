package kiro

import (
	"encoding/json"
	"fmt"
)

type AssistantResponseEvent struct {
	Content string `json:"content"`
}

// ToolUseEvent arrives in fragments: Input is a partial JSON string, and Stop
// marks the last fragment of a tool call.
type ToolUseEvent struct {
	Name      string `json:"name"`
	ToolUseID string `json:"toolUseId"`
	Input     string `json:"input"`
	Stop      bool   `json:"stop"`
}

type ReasoningContentEvent struct {
	Text            string `json:"text,omitempty"`
	Signature       string `json:"signature,omitempty"`
	RedactedContent string `json:"redactedContent,omitempty"`
}

type ContextUsageEvent struct {
	ContextUsagePercentage float64 `json:"contextUsagePercentage"`
}

type MeteringEvent struct {
	Unit       string  `json:"unit"`
	UnitPlural string  `json:"unitPlural"`
	Usage      float64 `json:"usage"`
}

type TokenUsage struct {
	UncachedInputTokens   int64 `json:"uncachedInputTokens"`
	OutputTokens          int64 `json:"outputTokens"`
	CacheReadInputTokens  int64 `json:"cacheReadInputTokens"`
	CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
}

type MetadataEvent struct {
	TokenUsage *TokenUsage `json:"tokenUsage,omitempty"`
	StopReason string      `json:"stopReason,omitempty"`
}

// StreamError is an in-band error or exception frame.
type StreamError struct {
	Kind    string // "error" or "exception"
	Code    string
	Message string
}

func (e *StreamError) Error() string {
	return fmt.Sprintf("kiro stream %s %s: %s", e.Kind, e.Code, e.Message)
}

// Event holds exactly one decoded payload; unknown event types leave every field nil.
type Event struct {
	Assistant    *AssistantResponseEvent
	ToolUse      *ToolUseEvent
	Reasoning    *ReasoningContentEvent
	ContextUsage *ContextUsageEvent
	Metering     *MeteringEvent
	Metadata     *MetadataEvent
	Err          *StreamError
}

func DecodeEvent(f Frame) (Event, error) {
	switch f.MessageType() {
	case "error":
		return Event{Err: &StreamError{Kind: "error", Code: f.header(":error-code"), Message: string(f.Payload)}}, nil
	case "exception":
		return Event{Err: &StreamError{Kind: "exception", Code: f.header(":exception-type"), Message: string(f.Payload)}}, nil
	}
	var ev Event
	var target any
	switch f.EventType() {
	case "assistantResponseEvent":
		ev.Assistant = &AssistantResponseEvent{}
		target = ev.Assistant
	case "toolUseEvent":
		ev.ToolUse = &ToolUseEvent{}
		target = ev.ToolUse
	case "reasoningContentEvent":
		ev.Reasoning = &ReasoningContentEvent{}
		target = ev.Reasoning
	case "contextUsageEvent":
		ev.ContextUsage = &ContextUsageEvent{}
		target = ev.ContextUsage
	case "meteringEvent":
		ev.Metering = &MeteringEvent{}
		target = ev.Metering
	case "metadataEvent":
		ev.Metadata = &MetadataEvent{}
		target = ev.Metadata
	default:
		return Event{}, nil
	}
	if err := json.Unmarshal(f.Payload, target); err != nil {
		return Event{}, fmt.Errorf("decode %s: %w", f.EventType(), err)
	}
	return ev, nil
}
