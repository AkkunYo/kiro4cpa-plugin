package kiro

import "strings"

const (
	thinkingOpen  = "<thinking>"
	thinkingClose = "</thinking>"
)

// A tag touching one of these characters is quoted text (e.g. `<thinking>` in
// code), not a real thinking boundary. Same set as kiro.rs.
const quoteChars = "`\"'\\#!@$%^&*()-_=+[]{};:<>,.?/"

func quotedAt(s string, i int) bool {
	return i >= 0 && i < len(s) && strings.IndexByte(quoteChars, s[i]) >= 0
}

func findThinkingStart(s string) int {
	for from := 0; ; {
		i := strings.Index(s[from:], thinkingOpen)
		if i < 0 {
			return -1
		}
		pos := from + i
		if !quotedAt(s, pos-1) && !quotedAt(s, pos+len(thinkingOpen)) {
			return pos
		}
		from = pos + 1
	}
}

// findThinkingEnd returns a closing tag followed by a blank line; atEnd also
// accepts one followed only by whitespace up to the end of s.
func findThinkingEnd(s string, atEnd bool) int {
	for from := 0; ; {
		i := strings.Index(s[from:], thinkingClose)
		if i < 0 {
			return -1
		}
		pos := from + i
		after := pos + len(thinkingClose)
		if !quotedAt(s, pos-1) && !quotedAt(s, after) {
			rest := s[after:]
			if strings.HasPrefix(rest, "\n\n") || (atEnd && strings.TrimSpace(rest) == "") {
				return pos
			}
		}
		from = pos + 1
	}
}

// extractThinking splits a complete response into its thinking and visible text.
func extractThinking(text string) (thinking, rest string) {
	start := findThinkingStart(text)
	if start < 0 {
		return "", text
	}
	before, body := text[:start], text[start+len(thinkingOpen):]
	end := findThinkingEnd(body, false)
	after := ""
	switch {
	case end >= 0:
		after = body[end+len(thinkingClose)+2:]
	default:
		if end = findThinkingEnd(body, true); end < 0 {
			return "", text
		}
		after = strings.TrimLeft(body[end+len(thinkingClose):], " \t\r\n")
	}
	thinking = strings.TrimPrefix(body[:end], "\n")
	if strings.TrimSpace(before) != "" {
		rest = before
	}
	return thinking, rest + after
}
