package kiro

import (
	"strings"
	"testing"
)

func eventNames(events []sseEvent) string {
	names := make([]string, len(events))
	for i, e := range events {
		names[i] = e.name
		if e.name == "content_block_start" {
			names[i] += ":" + e.data["content_block"].(map[string]any)["type"].(string)
		}
		if e.name == "content_block_delta" {
			names[i] += ":" + e.data["delta"].(map[string]any)["type"].(string)
		}
	}
	return strings.Join(names, " ")
}

func runSSE(conv convertedRequest, evs ...Event) []sseEvent {
	s := newSSEStream(conv)
	out := s.start()
	for _, ev := range evs {
		out = append(out, s.handle(ev)...)
	}
	return append(out, s.finish()...)
}

func TestSSEThinkingTextAndTool(t *testing.T) {
	conv := convertedRequest{ModelID: "claude-haiku-4.5", Thinking: true, InputTokens: 9, ToolNames: map[string]string{"short_1": "a_very_long_original_name"}}
	out := runSSE(conv,
		Event{Assistant: &AssistantResponseEvent{Content: "<thinking>plan</thinking>\n\nHello"}},
		Event{ToolUse: &ToolUseEvent{Name: "short_1", ToolUseID: "t1", Input: `{"p":`}},
		Event{ToolUse: &ToolUseEvent{Name: "short_1", ToolUseID: "t1", Input: `"a"}`, Stop: true}},
		Event{Metadata: &MetadataEvent{TokenUsage: &TokenUsage{UncachedInputTokens: 11, OutputTokens: 7}}},
	)
	want := "message_start content_block_start:thinking content_block_delta:thinking_delta content_block_stop " +
		"content_block_start:text content_block_delta:text_delta content_block_stop " +
		"content_block_start:tool_use content_block_delta:input_json_delta content_block_delta:input_json_delta content_block_stop " +
		"message_delta message_stop"
	if got := eventNames(out); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if name := out[7].data["content_block"].(map[string]any)["name"]; name != "a_very_long_original_name" {
		t.Fatalf("tool name not restored: %v", name)
	}
	delta := out[len(out)-2].data
	if delta["delta"].(map[string]any)["stop_reason"] != "tool_use" || delta["usage"].(map[string]int64)["input_tokens"] != 11 {
		t.Fatalf("message_delta %v", delta)
	}
	block := -1
	for i, e := range out {
		if e.name == "content_block_start" {
			block++
		}
		if idx, ok := e.data["index"]; ok && idx.(int) != block {
			t.Fatalf("event %d (%s) index %v, want %d", i, e.name, idx, block)
		}
	}
}

func TestSSEThinkingOnlyIsMaxTokens(t *testing.T) {
	out := runSSE(convertedRequest{Thinking: true}, Event{Assistant: &AssistantResponseEvent{Content: "<thinking>still going"}})
	want := "message_start content_block_start:thinking content_block_delta:thinking_delta content_block_stop " +
		"content_block_start:text content_block_delta:text_delta content_block_stop message_delta message_stop"
	if got := eventNames(out); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if out[len(out)-2].data["delta"].(map[string]any)["stop_reason"] != "max_tokens" {
		t.Fatalf("stop %v", out[len(out)-2].data)
	}
}

func TestSSEReasoningEventSignature(t *testing.T) {
	out := runSSE(convertedRequest{Thinking: true},
		Event{Reasoning: &ReasoningContentEvent{Text: "hm", Signature: "sig"}},
		Event{Assistant: &AssistantResponseEvent{Content: "answer"}},
	)
	want := "message_start content_block_start:thinking content_block_delta:thinking_delta content_block_delta:signature_delta content_block_stop " +
		"content_block_start:text content_block_delta:text_delta content_block_stop message_delta message_stop"
	if got := eventNames(out); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestSSEPlainTextAndStopReasons(t *testing.T) {
	out := runSSE(convertedRequest{}, Event{Assistant: &AssistantResponseEvent{Content: "<thinking>not parsed</thinking>\n\nx"}},
		Event{Err: &StreamError{Kind: "exception", Code: "ContentLengthExceededException"}})
	if got := eventNames(out); got != "message_start content_block_start:text content_block_delta:text_delta content_block_stop message_delta message_stop" {
		t.Fatalf("got %s", got)
	}
	if out[2].data["delta"].(map[string]any)["text"] != "<thinking>not parsed</thinking>\n\nx" ||
		out[len(out)-2].data["delta"].(map[string]any)["stop_reason"] != "max_tokens" {
		t.Fatalf("events %v", out)
	}
	ctx := runSSE(convertedRequest{}, Event{Assistant: &AssistantResponseEvent{Content: "x"}}, Event{ContextUsage: &ContextUsageEvent{ContextUsagePercentage: 100}})
	if ctx[len(ctx)-2].data["delta"].(map[string]any)["stop_reason"] != "model_context_window_exceeded" {
		t.Fatalf("context stop %v", ctx[len(ctx)-2].data)
	}
}

func TestSSEFailedBeforeContent(t *testing.T) {
	s := newSSEStream(convertedRequest{})
	s.handle(Event{Err: &StreamError{Kind: "exception", Code: "ThrottlingException"}})
	if s.failedBeforeContent() == nil {
		t.Fatal("early error not reported")
	}
	s = newSSEStream(convertedRequest{})
	s.handle(Event{Assistant: &AssistantResponseEvent{Content: "x"}})
	s.handle(Event{Err: &StreamError{Kind: "exception", Code: "ThrottlingException"}})
	if s.failedBeforeContent() != nil {
		t.Fatal("error after content must not abort the stream")
	}
}

func TestSSEChunksOneLinePerChunk(t *testing.T) {
	chunks := sseChunks([]sseEvent{{"message_stop", map[string]any{"type": "message_stop"}}})
	if len(chunks) != 2 || string(chunks[0]) != "event: message_stop\n" || string(chunks[1]) != "data: {\"type\":\"message_stop\"}\n\n" {
		t.Fatalf("chunks %q", chunks)
	}
}
