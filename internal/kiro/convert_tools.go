package kiro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const (
	maxToolNameBytes   = 63
	maxDescriptionRune = 10000

	writeChunkHint = "- IMPORTANT: If the content to write exceeds 150 lines, you MUST only write the first 50 lines using this tool, " +
		"then use `Edit` tool to append the remaining content in chunks of no more than 50 lines each. " +
		"If needed, leave a unique placeholder to help append content. Do NOT attempt to write all content at once."
	editChunkHint = "- IMPORTANT: If the `new_string` content exceeds 50 lines, you MUST split it into multiple Edit calls, " +
		"each replacing no more than 50 lines at a time. If used to append content, leave a unique placeholder to help append content. " +
		"On the final chunk, do NOT include the placeholder."
)

func convertTools(tools []claudeTool, toolNames map[string]string) []kiroTool {
	out := make([]kiroTool, 0, len(tools))
	for _, t := range tools {
		desc := t.Description
		switch t.Name {
		case "Write":
			desc += "\n" + writeChunkHint
		case "Edit":
			desc += "\n" + editChunkHint
		}
		if utf8.RuneCountInString(desc) > maxDescriptionRune {
			desc = string([]rune(desc)[:maxDescriptionRune])
		}
		spec := kiroToolSpec{Name: mapToolName(t.Name, toolNames), Description: desc}
		spec.InputSchema.JSON = normalizeSchema(t.InputSchema)
		out = append(out, kiroTool{ToolSpecification: spec})
	}
	return out
}

// normalizeSchema guarantees the object/properties/required/additionalProperties
// shape Kiro validates against.
func normalizeSchema(raw json.RawMessage) json.RawMessage {
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		obj = map[string]any{}
	}
	if s, _ := obj["type"].(string); s == "" {
		obj["type"] = "object"
	}
	if _, ok := obj["properties"].(map[string]any); !ok {
		obj["properties"] = map[string]any{}
	}
	required := []string{}
	if arr, ok := obj["required"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	obj["required"] = required
	switch obj["additionalProperties"].(type) {
	case bool, map[string]any:
	default:
		obj["additionalProperties"] = true
	}
	out, _ := json.Marshal(obj)
	return out
}

// mapToolName shortens names over Kiro's 63-byte limit to prefix + "_" + 8 hex
// of sha256, recording the reverse mapping for the response.
func mapToolName(name string, toolNames map[string]string) string {
	if len(name) <= maxToolNameBytes {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:])[:8]
	limit := maxToolNameBytes - len(suffix)
	var prefix strings.Builder
	for _, r := range name {
		if prefix.Len()+utf8.RuneLen(r) > limit {
			break
		}
		prefix.WriteRune(r)
	}
	short := prefix.String() + suffix
	toolNames[short] = name
	return short
}

// appendPlaceholderTools defines tools referenced in history but absent from the
// request; Kiro matches tool names case-insensitively and rejects undefined ones.
func appendPlaceholderTools(tools []kiroTool, history []historyEntry) []kiroTool {
	known := map[string]bool{}
	for _, t := range tools {
		known[strings.ToLower(t.ToolSpecification.Name)] = true
	}
	out := tools
	for _, h := range history {
		if h.AssistantResponseMessage == nil {
			continue
		}
		for _, u := range h.AssistantResponseMessage.ToolUses {
			key := strings.ToLower(u.Name)
			if known[key] {
				continue
			}
			known[key] = true
			spec := kiroToolSpec{Name: u.Name, Description: "Tool used in conversation history"}
			spec.InputSchema.JSON = json.RawMessage(`{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{},"required":[],"additionalProperties":true}`)
			out = append(out, kiroTool{ToolSpecification: spec})
		}
	}
	return out
}

// conversationID reuses Claude Code's session UUID from metadata.user_id so one
// session maps to one Kiro conversation.
func conversationID(req claudeRequest) string {
	if req.Metadata == nil || req.Metadata.UserID == "" {
		return newUUID()
	}
	userID := req.Metadata.UserID
	var obj struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(userID), &obj) == nil && isUUID(obj.SessionID) {
		return obj.SessionID
	}
	if i := strings.Index(userID, "session_"); i >= 0 && len(userID) >= i+8+36 && isUUID(userID[i+8:i+8+36]) {
		return userID[i+8 : i+8+36]
	}
	return newUUID()
}

func isUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}
