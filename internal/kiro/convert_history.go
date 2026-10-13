package kiro

import (
	"encoding/json"
	"strings"
)

// buildHistory converts every message before the current one. System text and the
// thinking prefix ride in a leading user/assistant pair; same-role runs are merged
// and the history always ends on an assistant turn.
func buildHistory(req claudeRequest, messages []claudeMessage, prefix, modelID string, toolNames map[string]string) []historyEntry {
	var history []historyEntry
	if lead := leadingInstructions(req.systemText(), prefix); lead != "" {
		history = append(history,
			historyEntry{UserInputMessage: &userInputMessage{Content: lead, ModelID: modelID, Origin: "AI_EDITOR"}},
			historyEntry{AssistantResponseMessage: &assistantMessage{Content: "I will follow these instructions."}},
		)
	}
	var users, assistants []claudeMessage
	flushUsers := func() {
		if len(users) > 0 {
			history = append(history, historyEntry{UserInputMessage: mergeUsers(users, modelID)})
			users = nil
		}
	}
	flushAssistants := func() {
		if len(assistants) > 0 {
			history = append(history, historyEntry{AssistantResponseMessage: mergeAssistants(assistants, toolNames)})
			assistants = nil
		}
	}
	for _, m := range messages {
		switch m.Role {
		case "user":
			flushAssistants()
			users = append(users, m)
		case "assistant":
			flushUsers()
			assistants = append(assistants, m)
		}
	}
	flushAssistants()
	if len(users) > 0 {
		flushUsers()
		history = append(history, historyEntry{AssistantResponseMessage: &assistantMessage{Content: "OK"}})
	}
	return history
}

func leadingInstructions(system, prefix string) string {
	if system == "" {
		return prefix
	}
	system += "\n" + chunkedWritePolicy
	if prefix != "" && !hasThinkingTags(system) {
		return prefix + "\n" + system
	}
	return system
}

func mergeUsers(messages []claudeMessage, modelID string) *userInputMessage {
	var texts []string
	var images []kiroImage
	var results []kiroToolResult
	for _, m := range messages {
		c := parseContent(m.Content)
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
		images = append(images, c.Images...)
		results = append(results, c.ToolResults...)
	}
	msg := &userInputMessage{Content: strings.Join(texts, "\n"), ModelID: modelID, Origin: "AI_EDITOR", Images: images}
	if len(results) > 0 {
		msg.UserInputMessageContext = &inputContext{ToolResults: results}
	}
	return msg
}

func mergeAssistants(messages []claudeMessage, toolNames map[string]string) *assistantMessage {
	var parts []string
	var uses []kiroToolUse
	for _, m := range messages {
		a := convertAssistant(m, toolNames)
		if strings.TrimSpace(a.Content) != "" {
			parts = append(parts, a.Content)
		}
		uses = append(uses, a.ToolUses...)
	}
	content := strings.Join(parts, "\n\n")
	if content == "" && len(uses) > 0 {
		content = " "
	}
	return &assistantMessage{Content: content, ToolUses: uses}
}

// convertAssistant re-wraps thinking in <thinking> tags. Kiro rejects an empty
// assistant content, so a tool-only turn gets a single space.
func convertAssistant(m claudeMessage, toolNames map[string]string) assistantMessage {
	var thinking, text strings.Builder
	var uses []kiroToolUse
	for _, b := range blocks(m.Content) {
		switch b.Type {
		case "thinking":
			thinking.WriteString(b.Thinking)
		case "text":
			text.WriteString(b.Text)
		case "tool_use":
			if b.ID == "" || b.Name == "" {
				continue
			}
			input := b.Input
			if len(input) == 0 || string(input) == "null" {
				input = json.RawMessage(`{}`)
			}
			uses = append(uses, kiroToolUse{ToolUseID: b.ID, Name: mapToolName(b.Name, toolNames), Input: input})
		}
	}
	content := text.String()
	switch {
	case thinking.Len() > 0 && content != "":
		content = "<thinking>" + thinking.String() + "</thinking>\n\n" + content
	case thinking.Len() > 0:
		content = "<thinking>" + thinking.String() + "</thinking>"
	case content == "" && len(uses) > 0:
		content = " "
	}
	return assistantMessage{Content: content, ToolUses: uses}
}
