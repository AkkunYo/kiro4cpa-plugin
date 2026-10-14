package kiro

import (
	"strings"
	"testing"
)

func runParser(chunks []string) (thinking, text string, ends int) {
	var p thinkingParser
	collect := func(segs []segment) {
		for _, s := range segs {
			switch s.kind {
			case segThinking:
				thinking += s.text
			case segText:
				text += s.text
			case segThinkingEnd:
				ends++
			}
		}
	}
	for _, c := range chunks {
		collect(p.feed(c))
	}
	collect(p.flush())
	return
}

func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func TestThinkingParserAnyChunking(t *testing.T) {
	full := "<thinking>\nplan 中文 step</thinking>\n\nanswer with `<thinking>` inside"
	for _, n := range []int{1, 2, 3, 7, 1000} {
		thinking, text, ends := runParser(splitEvery(full, n))
		if thinking != "plan 中文 step" || text != "answer with `<thinking>` inside" || ends != 1 {
			t.Errorf("chunk %d: thinking=%q text=%q ends=%d", n, thinking, text, ends)
		}
	}
}

func TestThinkingParserUnclosedAndPlain(t *testing.T) {
	if th, text, ends := runParser([]string{"<thinking>cut off mid"}); th != "cut off mid" || text != "" || ends != 1 {
		t.Errorf("unclosed: %q %q %d", th, text, ends)
	}
	if th, text, ends := runParser([]string{"<thinking>done</thinking>"}); th != "done" || text != "" || ends != 1 {
		t.Errorf("closed at end: %q %q %d", th, text, ends)
	}
	plain := strings.Repeat("no tags ", 5)
	if th, text, ends := runParser(splitEvery(plain, 3)); th != "" || text != plain || ends != 0 {
		t.Errorf("plain: %q %q %d", th, text, ends)
	}
}
