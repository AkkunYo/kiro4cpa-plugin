package kiro

import (
	"strings"
	"unicode/utf8"
)

type segmentKind int

const (
	segText segmentKind = iota
	segThinking
	segThinkingEnd
)

type segment struct {
	kind segmentKind
	text string
}

// thinkingParser splits streamed assistant text into thinking and visible text.
// It holds back just enough of the tail to recognize a tag split across chunks,
// using the same tag rules as extractThinking.
type thinkingParser struct {
	buf         string
	inThinking  bool
	extracted   bool
	stripLeadNL bool
}

func (p *thinkingParser) feed(s string) []segment {
	p.buf += s
	var out []segment
	for {
		switch {
		case !p.inThinking && !p.extracted:
			start := findThinkingStart(p.buf)
			if start < 0 {
				out = p.emitSafe(out, segText, len(thinkingOpen), true)
				return out
			}
			if before := p.buf[:start]; strings.TrimSpace(before) != "" {
				out = append(out, segment{segText, before})
			}
			p.buf = p.buf[start+len(thinkingOpen):]
			p.inThinking, p.stripLeadNL = true, true
		case p.inThinking:
			if p.stripLeadNL && p.buf != "" {
				p.buf = strings.TrimPrefix(p.buf, "\n")
				p.stripLeadNL = false
			}
			end := findThinkingEnd(p.buf, false)
			if end < 0 {
				out = p.emitSafe(out, segThinking, len(thinkingClose)+2, false)
				return out
			}
			if end > 0 {
				out = append(out, segment{segThinking, p.buf[:end]})
			}
			out = append(out, segment{kind: segThinkingEnd})
			p.buf = p.buf[end+len(thinkingClose)+2:]
			p.inThinking, p.extracted = false, true
		default:
			if p.buf != "" {
				out = append(out, segment{segText, p.buf})
				p.buf = ""
			}
			return out
		}
	}
}

// emitSafe releases everything except a tail that could still begin a tag.
func (p *thinkingParser) emitSafe(out []segment, kind segmentKind, holdBack int, skipBlank bool) []segment {
	cut := len(p.buf) - holdBack
	for cut > 0 && !utf8.RuneStart(p.buf[cut]) {
		cut--
	}
	if cut <= 0 {
		return out
	}
	chunk := p.buf[:cut]
	if skipBlank && strings.TrimSpace(chunk) == "" {
		return out
	}
	p.buf = p.buf[cut:]
	return append(out, segment{kind, chunk})
}

// flush is called at a tool call or end of stream, when no more text can complete a tag.
func (p *thinkingParser) flush() []segment {
	defer func() { p.buf = "" }()
	switch {
	case p.inThinking:
		p.inThinking, p.extracted = false, true
		body, rest := p.buf, ""
		if end := findThinkingEnd(p.buf, true); end >= 0 {
			body, rest = p.buf[:end], strings.TrimLeft(p.buf[end+len(thinkingClose):], " \t\r\n")
		}
		var out []segment
		if body != "" {
			out = append(out, segment{segThinking, body})
		}
		out = append(out, segment{kind: segThinkingEnd})
		if rest != "" {
			out = append(out, segment{segText, rest})
		}
		return out
	case p.buf != "":
		return []segment{{segText, p.buf}}
	}
	return nil
}
