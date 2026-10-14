package kiro

import (
	"encoding/json"
	"strings"
)

// responseCollector folds a complete Kiro event stream into one Claude message.
type responseCollector struct {
	text      strings.Builder
	reasoning strings.Builder
	signature string
	toolOrder []string
	tools     map[string]*collectedTool
	usage     *TokenUsage
	maxTokens bool
	streamErr *StreamError
}

type collectedTool struct {
	name  string
	input strings.Builder
}

func (c *responseCollector) add(ev Event) {
	switch {
	case ev.Assistant != nil:
		c.text.WriteString(ev.Assistant.Content)
	case ev.Reasoning != nil:
		c.reasoning.WriteString(ev.Reasoning.Text)
		if ev.Reasoning.Signature != "" {
			c.signature = ev.Reasoning.Signature
		}
	case ev.ToolUse != nil:
		if c.tools == nil {
			c.tools = map[string]*collectedTool{}
		}
		t, ok := c.tools[ev.ToolUse.ToolUseID]
		if !ok {
			t = &collectedTool{name: ev.ToolUse.Name}
			c.tools[ev.ToolUse.ToolUseID] = t
			c.toolOrder = append(c.toolOrder, ev.ToolUse.ToolUseID)
		}
		t.input.WriteString(ev.ToolUse.Input)
	case ev.Metadata != nil && ev.Metadata.TokenUsage != nil:
		c.usage = ev.Metadata.TokenUsage
	case ev.Err != nil:
		if ev.Err.Code == "ContentLengthExceededException" {
			c.maxTokens = true
		} else {
			c.streamErr = ev.Err
		}
	}
}

func (c *responseCollector) empty() bool {
	return c.text.Len() == 0 && c.reasoning.Len() == 0 && len(c.toolOrder) == 0
}

func (c *responseCollector) message(conv convertedRequest) []byte {
	text := c.text.String()
	thinking := c.reasoning.String()
	if conv.Thinking && thinking == "" {
		thinking, text = extractThinking(text)
	}
	content := []map[string]any{}
	if thinking != "" {
		block := map[string]any{"type": "thinking", "thinking": thinking}
		if c.signature != "" {
			block["signature"] = c.signature
		}
		content = append(content, block)
	}
	if text != "" || len(c.toolOrder) == 0 {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, id := range c.toolOrder {
		t := c.tools[id]
		input := json.RawMessage(t.input.String())
		if !json.Valid(input) {
			input = json.RawMessage(`{}`)
		}
		name := t.name
		if original, ok := conv.ToolNames[name]; ok {
			name = original
		}
		content = append(content, map[string]any{"type": "tool_use", "id": id, "name": name, "input": input})
	}
	stop := "end_turn"
	switch {
	case c.maxTokens:
		stop = "max_tokens"
	case len(c.toolOrder) > 0:
		stop = "tool_use"
	}
	out, _ := json.Marshal(map[string]any{
		"id": "msg_" + strings.ReplaceAll(newUUID(), "-", ""), "type": "message", "role": "assistant",
		"model": conv.ModelID, "content": content, "stop_reason": stop, "stop_sequence": nil,
		"usage": usageFields(c.usage, conv.InputTokens, estimateTokens(text+thinking)),
	})
	return out
}

// usageFields prefers Kiro's real token counts; input_tokens excludes cached
// tokens, matching Anthropic's usage semantics.
func usageFields(u *TokenUsage, estimatedInput, estimatedOutput int) map[string]int64 {
	if u != nil {
		return map[string]int64{
			"input_tokens":                max(u.UncachedInputTokens, 0),
			"output_tokens":               max(u.OutputTokens, 0),
			"cache_read_input_tokens":     max(u.CacheReadInputTokens, 0),
			"cache_creation_input_tokens": max(u.CacheWriteInputTokens, 0),
		}
	}
	return map[string]int64{"input_tokens": int64(estimatedInput), "output_tokens": int64(estimatedOutput)}
}
