package kiro

import (
	"encoding/json"
	"strings"
)

type parsedContent struct {
	Text        string
	Images      []kiroImage
	ToolResults []kiroToolResult
}

func parseContent(raw json.RawMessage) parsedContent {
	var out parsedContent
	var texts []string
	for _, b := range blocks(raw) {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "image":
			if img, ok := toKiroImage(b.Source); ok {
				out.Images = append(out.Images, img)
			}
		case "tool_result":
			if b.ToolUseID == "" {
				continue
			}
			text, images := toolResultContent(b.Content)
			out.Images = append(out.Images, images...)
			status := "success"
			if b.IsError {
				status = "error"
			}
			out.ToolResults = append(out.ToolResults, kiroToolResult{
				ToolUseID: b.ToolUseID, Content: []kiroText{{Text: text}}, Status: status,
			})
		}
	}
	out.Text = strings.Join(texts, "\n")
	return out
}

func toolResultContent(raw json.RawMessage) (string, []kiroImage) {
	if len(raw) == 0 {
		return "", nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var items []claudeBlock
	if json.Unmarshal(raw, &items) != nil {
		return string(raw), nil
	}
	var texts []string
	var images []kiroImage
	for _, it := range items {
		switch {
		case it.Text != "":
			texts = append(texts, it.Text)
		case it.Type == "image":
			if img, ok := toKiroImage(it.Source); ok {
				images = append(images, img)
			}
		}
	}
	return strings.Join(texts, "\n"), images
}

func toKiroImage(src *struct {
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}) (kiroImage, bool) {
	if src == nil || src.Data == "" {
		return kiroImage{}, false
	}
	format, ok := map[string]string{
		"image/jpeg": "jpeg", "image/png": "png", "image/gif": "gif", "image/webp": "webp",
	}[src.MediaType]
	if !ok {
		return kiroImage{}, false
	}
	img := kiroImage{Format: format}
	img.Source.Bytes = src.Data
	return img, true
}

// pairToolResults keeps only current tool_results that answer a tool_use still
// unanswered in history, and returns the tool_use IDs that remain orphaned.
func pairToolResults(history []historyEntry, current []kiroToolResult) ([]kiroToolResult, map[string]bool) {
	used, answered := map[string]bool{}, map[string]bool{}
	for _, h := range history {
		if a := h.AssistantResponseMessage; a != nil {
			for _, u := range a.ToolUses {
				used[u.ToolUseID] = true
			}
		}
		if u := h.UserInputMessage; u != nil && u.UserInputMessageContext != nil {
			for _, r := range u.UserInputMessageContext.ToolResults {
				answered[r.ToolUseID] = true
			}
		}
	}
	pending := map[string]bool{}
	for id := range used {
		if !answered[id] {
			pending[id] = true
		}
	}
	var kept []kiroToolResult
	for _, r := range current {
		if pending[r.ToolUseID] {
			kept = append(kept, r)
			delete(pending, r.ToolUseID)
		}
	}
	return kept, pending
}

// removeToolUses drops orphaned tool_use entries: Kiro requires every tool_use to have a result.
func removeToolUses(history []historyEntry, orphans map[string]bool) []historyEntry {
	if len(orphans) == 0 {
		return history
	}
	out := make([]historyEntry, len(history))
	for i, h := range history {
		out[i] = h
		a := h.AssistantResponseMessage
		if a == nil {
			continue
		}
		var keep []kiroToolUse
		for _, u := range a.ToolUses {
			if !orphans[u.ToolUseID] {
				keep = append(keep, u)
			}
		}
		content := a.Content
		if strings.TrimSpace(content) == "" {
			content = " "
		}
		out[i].AssistantResponseMessage = &assistantMessage{Content: content, ToolUses: keep}
	}
	return out
}
