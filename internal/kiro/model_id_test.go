package kiro

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestKiroModelID(t *testing.T) {
	cases := []struct {
		in       string
		want     string
		thinking bool
	}{
		{"claude-haiku-4-5-20251001", "claude-haiku-4.5", false},
		{"claude-sonnet-4-5", "claude-sonnet-4.5", false},
		{"claude-sonnet-4.5", "claude-sonnet-4.5", false},
		{"claude-opus-4-20250514", "claude-opus-4", false},
		{"claude-opus-5-5", "claude-opus-5.5", false},
		{"claude-fable-5-1-thinking", "claude-fable-5.1", true},
		{"claude-sonnet-5-latest", "claude-sonnet-5", false},
		{"Claude-Sonnet-4-5", "claude-sonnet-4.5", false},
		{"glm-5", "glm-5", false},
		{"deepseek-3.2-thinking", "deepseek-3.2", true},
		{"auto", "auto", false},
	}
	for _, tc := range cases {
		got, thinking := kiroModelID(tc.in)
		if got != tc.want || thinking != tc.thinking {
			t.Errorf("%s: got %s thinking=%v", tc.in, got, thinking)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := estimateTokens("abcdefgh"); got != 2 {
		t.Errorf("ascii: %d", got)
	}
	if got := estimateTokens("你好世"); got != 2 {
		t.Errorf("cjk: %d", got)
	}
	if got := estimateTokens(""); got != 1 {
		t.Errorf("empty: %d", got)
	}
}

func TestMapToolName(t *testing.T) {
	names := map[string]string{}
	if got := mapToolName("Read", names); got != "Read" || len(names) != 0 {
		t.Fatalf("short name changed: %s %v", got, names)
	}
	long := "mcp__" + strings.Repeat("very_long_server_name_", 4) + "tool"
	short := mapToolName(long, names)
	if len(short) > maxToolNameBytes || names[short] != long || !strings.HasPrefix(short, "mcp__very_long") {
		t.Fatalf("short=%s (%d bytes) map=%v", short, len(short), names)
	}
	if again := mapToolName(long, map[string]string{}); again != short {
		t.Fatalf("not deterministic: %s vs %s", again, short)
	}
	cjk := mapToolName(strings.Repeat("工具", 20), names)
	if len(cjk) > maxToolNameBytes || !utf8.ValidString(cjk) {
		t.Fatalf("multibyte name cut badly: %q (%d bytes)", cjk, len(cjk))
	}
}
