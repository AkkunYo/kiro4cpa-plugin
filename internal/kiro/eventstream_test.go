package kiro

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"strings"
	"testing"
)

func encodeFrame(headers map[string]string, payload []byte) []byte {
	var hb []byte
	for name, value := range headers {
		hb = append(hb, byte(len(name)))
		hb = append(hb, name...)
		hb = append(hb, 7)
		hb = binary.BigEndian.AppendUint16(hb, uint16(len(value)))
		hb = append(hb, value...)
	}
	total := preludeLen + len(hb) + len(payload) + 4
	out := binary.BigEndian.AppendUint32(nil, uint32(total))
	out = binary.BigEndian.AppendUint32(out, uint32(len(hb)))
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out[:8]))
	out = append(out, hb...)
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

func eventFrame(eventType, payload string) []byte {
	return encodeFrame(map[string]string{":message-type": "event", ":event-type": eventType}, []byte(payload))
}

func TestDecoderSingleFrame(t *testing.T) {
	var d Decoder
	frames := d.Feed(eventFrame("assistantResponseEvent", `{"content":"pong"}`))
	if len(frames) != 1 || frames[0].EventType() != "assistantResponseEvent" || string(frames[0].Payload) != `{"content":"pong"}` {
		t.Fatalf("got %+v", frames)
	}
}

func TestDecoderByteByByte(t *testing.T) {
	stream := append(eventFrame("assistantResponseEvent", `{"content":"po"}`), eventFrame("assistantResponseEvent", `{"content":"ng"}`)...)
	var d Decoder
	var text string
	for _, b := range stream {
		for _, f := range d.Feed([]byte{b}) {
			ev, err := DecodeEvent(f)
			if err != nil {
				t.Fatal(err)
			}
			text += ev.Assistant.Content
		}
	}
	if text != "pong" || d.Skipped != 0 {
		t.Fatalf("text=%q skipped=%d", text, d.Skipped)
	}
}

func TestDecoderResyncAfterGarbage(t *testing.T) {
	var d Decoder
	frames := d.Feed(append([]byte("garbage!!!!!"), eventFrame("meteringEvent", `{"unit":"credit"}`)...))
	if len(frames) != 1 || d.Skipped == 0 {
		t.Fatalf("frames=%d skipped=%d", len(frames), d.Skipped)
	}
}

func TestDecoderDropsFrameWithBadMessageCRC(t *testing.T) {
	bad := eventFrame("assistantResponseEvent", `{"content":"x"}`)
	bad[len(bad)-1] ^= 0xff
	var d Decoder
	frames := d.Feed(append(bad, eventFrame("assistantResponseEvent", `{"content":"ok"}`)...))
	if len(frames) != 1 || string(frames[0].Payload) != `{"content":"ok"}` || d.Skipped != 1 {
		t.Fatalf("frames=%+v skipped=%d", frames, d.Skipped)
	}
}

func TestDecoderWaitsForIncompleteFrame(t *testing.T) {
	frame := eventFrame("assistantResponseEvent", `{"content":"pong"}`)
	var d Decoder
	if got := d.Feed(frame[:len(frame)-3]); len(got) != 0 {
		t.Fatalf("decoded a partial frame: %+v", got)
	}
	if got := d.Feed(frame[len(frame)-3:]); len(got) != 1 {
		t.Fatalf("frame not completed: %+v", got)
	}
}

func TestDecodeEventTypes(t *testing.T) {
	var d Decoder
	stream := append(eventFrame("toolUseEvent", `{"name":"read","toolUseId":"t1","input":"{\"p\":","stop":false}`),
		eventFrame("metadataEvent", `{"tokenUsage":{"uncachedInputTokens":12,"outputTokens":3}}`)...)
	stream = append(stream, encodeFrame(map[string]string{":message-type": "exception", ":exception-type": "ThrottlingException"}, []byte("slow down"))...)
	frames := d.Feed(stream)
	if len(frames) != 3 {
		t.Fatalf("want 3 frames, got %d", len(frames))
	}
	tool, _ := DecodeEvent(frames[0])
	if tool.ToolUse == nil || tool.ToolUse.ToolUseID != "t1" || tool.ToolUse.Input != `{"p":` {
		t.Fatalf("tool event %+v", tool.ToolUse)
	}
	meta, _ := DecodeEvent(frames[1])
	if meta.Metadata == nil || meta.Metadata.TokenUsage.UncachedInputTokens != 12 || meta.Metadata.TokenUsage.OutputTokens != 3 {
		t.Fatalf("metadata event %+v", meta.Metadata)
	}
	exc, _ := DecodeEvent(frames[2])
	if exc.Err == nil || exc.Err.Code != "ThrottlingException" || exc.Err.Message != "slow down" {
		t.Fatalf("exception %+v", exc.Err)
	}
}

func TestDecodeHeaderValueTypes(t *testing.T) {
	var hb []byte
	add := func(name string, typ byte, val []byte) {
		hb = append(hb, byte(len(name)))
		hb = append(hb, name...)
		hb = append(hb, typ)
		hb = append(hb, val...)
	}
	add("t", 0, nil)
	add("f", 1, nil)
	add("b", 2, []byte{0xff})
	add("s", 3, []byte{0x00, 0x07})
	add("i", 4, binary.BigEndian.AppendUint32(nil, 42))
	add("l", 5, binary.BigEndian.AppendUint64(nil, 1<<40))
	add("raw", 6, []byte{0, 2, 0xde, 0xad})
	add("ts", 8, binary.BigEndian.AppendUint64(nil, 1700000000000))
	add("id", 9, bytes.Repeat([]byte{0xab}, 16))
	h, err := decodeHeaders(hb)
	if err != nil {
		t.Fatal(err)
	}
	if h["t"] != true || h["f"] != false || h["b"] != int8(-1) || h["s"] != int16(7) || h["i"] != int32(42) ||
		h["l"] != int64(1<<40) || h["ts"] != int64(1700000000000) || !bytes.Equal(h["raw"].([]byte), []byte{0xde, 0xad}) ||
		len(h["id"].([]byte)) != 16 {
		t.Fatalf("headers %v", h)
	}
	if _, err := decodeHeaders([]byte{1, 'x', 42}); err == nil {
		t.Fatal("accepted unknown header type")
	}
	if _, err := decodeHeaders([]byte{1, 'x', 7, 0, 9, 'a'}); err == nil {
		t.Fatal("accepted truncated string header")
	}
}

func TestDecodeEventVariants(t *testing.T) {
	ev := func(et, payload string) Frame {
		return Frame{Headers: map[string]any{":message-type": "event", ":event-type": et}, Payload: []byte(payload)}
	}
	r, _ := DecodeEvent(ev("reasoningContentEvent", `{"text":"hm","signature":"sig"}`))
	c, _ := DecodeEvent(ev("contextUsageEvent", `{"contextUsagePercentage":12.5}`))
	m, _ := DecodeEvent(ev("meteringEvent", `{"unit":"credit","usage":0.01}`))
	if r.Reasoning.Signature != "sig" || c.ContextUsage.ContextUsagePercentage != 12.5 || m.Metering.Usage != 0.01 {
		t.Fatalf("reasoning=%+v context=%+v metering=%+v", r.Reasoning, c.ContextUsage, m.Metering)
	}
	e, _ := DecodeEvent(Frame{Headers: map[string]any{":message-type": "error", ":error-code": "InternalError"}, Payload: []byte("boom")})
	if e.Err == nil || e.Err.Kind != "error" || !strings.Contains(e.Err.Error(), "InternalError") {
		t.Fatalf("error frame %+v", e.Err)
	}
	if u, err := DecodeEvent(ev("futureEvent", `{}`)); err != nil || u != (Event{}) {
		t.Fatalf("unknown event %+v err=%v", u, err)
	}
	if _, err := DecodeEvent(ev("assistantResponseEvent", `{`)); err == nil {
		t.Fatal("accepted malformed payload")
	}
}
