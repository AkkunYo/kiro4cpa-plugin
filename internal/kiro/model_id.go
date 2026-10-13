package kiro

import (
	"regexp"
	"strings"
)

var claudeModelPattern = regexp.MustCompile(`^claude-(sonnet|opus|haiku|fable)-(\d+)(?:[-.](\d))?(?:-(?:\d{8}|latest))?$`)

// kiroModelID maps a client model name to the Kiro backend ID. Dashed Claude
// versions become dotted (claude-haiku-4-5-20251001 -> claude-haiku-4.5); a
// "-thinking" suffix requests thinking. Other names pass through unchanged and
// upstream decides whether the account may use them.
func kiroModelID(model string) (id string, thinking bool) {
	model = strings.TrimSpace(model)
	if base, ok := strings.CutSuffix(model, "-thinking"); ok {
		model, thinking = base, true
	}
	m := claudeModelPattern.FindStringSubmatch(strings.ToLower(model))
	if m == nil {
		return model, thinking
	}
	id = "claude-" + m[1] + "-" + m[2]
	if m[3] != "" {
		id += "." + m[3]
	}
	return id, thinking
}
