package kiro

// estimateTokens follows kiro.rs: about 1.5 CJK characters or 4 other characters per token.
func estimateTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			cjk++
		} else {
			other++
		}
	}
	n := (cjk*2+2)/3 + (other+3)/4
	if n < 1 {
		return 1
	}
	return n
}

func estimateRequestTokens(r claudeRequest) int {
	total := estimateTokens(r.systemText())
	for _, m := range r.Messages {
		total += estimateTokens(string(m.Content))
	}
	for _, t := range r.Tools {
		total += estimateTokens(t.Name + t.Description + string(t.InputSchema))
	}
	return total
}
