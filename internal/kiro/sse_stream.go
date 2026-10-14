package kiro

import (
	"encoding/json"
	"strings"
)

type sseEvent struct {
	name string
	data map[string]any
}

// sseStream turns Kiro events into a Claude Messages SSE sequence. Claude blocks
// never overlap, so at most one block is open; starting a block closes the last.
type sseStream struct {
	conv        convertedRequest
	parser      thinkingParser
	next        int
	openIdx     int
	openKind    string
	openToolID  string
	seenTools   map[string]bool
	signature   string
	hasToolUse  bool
	sawThinking bool
	sawOther    bool
	stopReason  string
	usage       *TokenUsage
	outTokens   int
	streamErr   *StreamError
}

func newSSEStream(conv convertedRequest) *sseStream {
	return &sseStream{conv: conv, openIdx: -1, seenTools: map[string]bool{}}
}

func (s *sseStream) start() []sseEvent {
	return []sseEvent{{"message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": "msg_" + strings.ReplaceAll(newUUID(), "-", ""), "type": "message", "role": "assistant",
		"content": []any{}, "model": s.conv.ModelID, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]int{"input_tokens": s.conv.InputTokens, "output_tokens": 1},
	}}}}
}

func (s *sseStream) handle(ev Event) []sseEvent {
	switch {
	case ev.Assistant != nil && ev.Assistant.Content != "":
		s.outTokens += estimateTokens(ev.Assistant.Content)
		if !s.conv.Thinking {
			return s.text(ev.Assistant.Content)
		}
		return s.apply(s.parser.feed(ev.Assistant.Content))
	case ev.Reasoning != nil:
		if ev.Reasoning.Signature != "" {
			s.signature = ev.Reasoning.Signature
		}
		if t := ev.Reasoning.Text; t != "" {
			s.outTokens += estimateTokens(t)
			if s.conv.Thinking {
				return s.thinking(t)
			}
			return s.text(t)
		}
	case ev.ToolUse != nil:
		return s.toolUse(*ev.ToolUse)
	case ev.ContextUsage != nil && ev.ContextUsage.ContextUsagePercentage >= 100:
		s.stopReason = "model_context_window_exceeded"
	case ev.Metadata != nil && ev.Metadata.TokenUsage != nil:
		s.usage = ev.Metadata.TokenUsage
	case ev.Err != nil && ev.Err.Code == "ContentLengthExceededException":
		s.stopReason = "max_tokens"
	case ev.Err != nil:
		s.streamErr = ev.Err
	}
	return nil
}

// failedBeforeContent reports an in-band error that arrived before any block,
// which is better surfaced as an error than as an empty answer.
func (s *sseStream) failedBeforeContent() *StreamError {
	if s.next == 0 {
		return s.streamErr
	}
	return nil
}

func (s *sseStream) finish() []sseEvent {
	out := s.apply(s.parser.flush())
	if s.sawThinking && !s.sawOther {
		// A thinking-only answer means Kiro stopped before the visible reply.
		if s.stopReason == "" {
			s.stopReason = "max_tokens"
		}
		out = append(out, s.text(" ")...)
	}
	out = append(out, s.closeBlock()...)
	stop := s.stopReason
	switch {
	case stop != "":
	case s.hasToolUse:
		stop = "tool_use"
	default:
		stop = "end_turn"
	}
	return append(out,
		sseEvent{"message_delta", map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
			"usage": usageFields(s.usage, s.conv.InputTokens, s.outTokens)}},
		sseEvent{"message_stop", map[string]any{"type": "message_stop"}},
	)
}

func (s *sseStream) apply(segs []segment) []sseEvent {
	var out []sseEvent
	for _, seg := range segs {
		switch seg.kind {
		case segText:
			out = append(out, s.text(seg.text)...)
		case segThinking:
			out = append(out, s.thinking(seg.text)...)
		case segThinkingEnd:
			if s.openKind == "thinking" {
				out = append(out, s.closeBlock()...)
			}
		}
	}
	return out
}

func (s *sseStream) text(t string) []sseEvent {
	var out []sseEvent
	if s.openKind != "text" {
		out = s.openBlock("text", map[string]any{"type": "text", "text": ""})
	}
	s.sawOther = true
	return append(out, s.delta(map[string]any{"type": "text_delta", "text": t}))
}

func (s *sseStream) thinking(t string) []sseEvent {
	var out []sseEvent
	if s.openKind != "thinking" {
		out = s.openBlock("thinking", map[string]any{"type": "thinking", "thinking": ""})
	}
	s.sawThinking = true
	return append(out, s.delta(map[string]any{"type": "thinking_delta", "thinking": t}))
}

func (s *sseStream) toolUse(tu ToolUseEvent) []sseEvent {
	out := s.apply(s.parser.flush())
	s.hasToolUse = true
	if s.openKind != "tool_use" || s.openToolID != tu.ToolUseID {
		if s.seenTools[tu.ToolUseID] {
			return out // late fragment for a block that was already closed
		}
		name := tu.Name
		if original, ok := s.conv.ToolNames[name]; ok {
			name = original
		}
		out = append(out, s.openBlock("tool_use", map[string]any{"type": "tool_use", "id": tu.ToolUseID, "name": name, "input": map[string]any{}})...)
		s.openToolID, s.seenTools[tu.ToolUseID], s.sawOther = tu.ToolUseID, true, true
	}
	if tu.Input != "" {
		s.outTokens += (len(tu.Input) + 3) / 4
		out = append(out, s.delta(map[string]any{"type": "input_json_delta", "partial_json": tu.Input}))
	}
	if tu.Stop {
		out = append(out, s.closeBlock()...)
	}
	return out
}

func (s *sseStream) openBlock(kind string, block map[string]any) []sseEvent {
	out := s.closeBlock()
	s.openIdx, s.openKind = s.next, kind
	s.next++
	return append(out, sseEvent{"content_block_start", map[string]any{"type": "content_block_start", "index": s.openIdx, "content_block": block}})
}

func (s *sseStream) delta(d map[string]any) sseEvent {
	return sseEvent{"content_block_delta", map[string]any{"type": "content_block_delta", "index": s.openIdx, "delta": d}}
}

func (s *sseStream) closeBlock() []sseEvent {
	if s.openIdx < 0 {
		return nil
	}
	var out []sseEvent
	if s.openKind == "thinking" && s.signature != "" {
		out = append(out, s.delta(map[string]any{"type": "signature_delta", "signature": s.signature}))
	}
	out = append(out, sseEvent{"content_block_stop", map[string]any{"type": "content_block_stop", "index": s.openIdx}})
	s.openIdx, s.openKind, s.openToolID = -1, "", ""
	return out
}

// sseChunks emits one SSE line per chunk: CPA's Claude->OpenAI translators test
// each chunk for a "data:" prefix and never split lines themselves.
func sseChunks(events []sseEvent) [][]byte {
	out := make([][]byte, 0, len(events)*2)
	for _, e := range events {
		data, _ := json.Marshal(e.data)
		out = append(out, []byte("event: "+e.name+"\n"), append(append([]byte("data: "), data...), '\n', '\n'))
	}
	return out
}
