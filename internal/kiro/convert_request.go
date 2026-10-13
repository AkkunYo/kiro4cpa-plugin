package kiro

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
)

// Kiro caps the size of a single tool input, so like kiro.rs we ask the model
// to write large files in chunks instead of failing on one oversized call.
const chunkedWritePolicy = "When the Write or Edit tool has content size limits, always comply silently. " +
	"Never suggest bypassing these limits via alternative tools. " +
	"Never ask the user whether to switch approaches. " +
	"Complete all chunked operations without commentary."

const defaultThinkingBudget = 20000

var errNoMessages = errors.New("request has no user message")

type convertedRequest struct {
	Payload     kiroPayload
	ModelID     string
	Thinking    bool
	ToolNames   map[string]string // shortened Kiro name -> client name
	InputTokens int
}

func convertClaudeRequest(raw []byte, cred Credential) (convertedRequest, error) {
	var req claudeRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return convertedRequest{}, fmt.Errorf("decode claude request: %w", err)
	}
	modelID, suffixThinking := kiroModelID(req.Model)
	messages := trimPrefill(req.Messages)
	if len(messages) == 0 {
		return convertedRequest{}, errNoMessages
	}
	prefix := thinkingPrefix(req, suffixThinking)

	toolNames := map[string]string{}
	tools := convertTools(req.Tools, toolNames)
	history := buildHistory(req, messages[:len(messages)-1], prefix, modelID, toolNames)

	current := parseContent(messages[len(messages)-1].Content)
	results, orphans := pairToolResults(history, current.ToolResults)
	history = removeToolUses(history, orphans)
	tools = appendPlaceholderTools(tools, history)

	ctx := &inputContext{
		EnvState:    &envState{OperatingSystem: "macos", CurrentWorkingDirectory: "/"},
		Tools:       tools,
		ToolResults: results,
	}
	state := conversationState{
		ConversationID:      conversationID(req),
		AgentContinuationID: newUUID(),
		AgentTaskType:       "vibe",
		ChatTriggerType:     "MANUAL",
		CurrentMessage: currentMessage{UserInputMessage: userInputMessage{
			Content: current.Text, ModelID: modelID, Origin: "AI_EDITOR",
			Images: current.Images, UserInputMessageContext: ctx,
		}},
		History: history,
	}
	return convertedRequest{
		Payload:     kiroPayload{ConversationState: state, ProfileArn: streamingProfileARN(cred)},
		ModelID:     modelID,
		Thinking:    prefix != "",
		ToolNames:   toolNames,
		InputTokens: estimateRequestTokens(req),
	}, nil
}

// trimPrefill drops trailing assistant turns: Kiro does not support prefill.
func trimPrefill(messages []claudeMessage) []claudeMessage {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return messages[:i+1]
		}
	}
	return nil
}

func thinkingPrefix(req claudeRequest, suffixThinking bool) string {
	t := req.Thinking
	switch {
	case t != nil && t.Type == "enabled":
		budget := t.BudgetTokens
		if budget <= 0 {
			budget = defaultThinkingBudget
		}
		budget = min(budget, models.MaxThinkingBudget)
		return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", budget)
	case t != nil && t.Type == "adaptive":
		effort := "high"
		if req.OutputConfig != nil && req.OutputConfig.Effort != "" {
			effort = req.OutputConfig.Effort
		}
		return fmt.Sprintf("<thinking_mode>adaptive</thinking_mode><thinking_effort>%s</thinking_effort>", effort)
	case suffixThinking && (t == nil || t.Type != "disabled"):
		return fmt.Sprintf("<thinking_mode>enabled</thinking_mode><max_thinking_length>%d</max_thinking_length>", defaultThinkingBudget)
	}
	return ""
}

func hasThinkingTags(s string) bool {
	return strings.Contains(s, "<thinking_mode>") || strings.Contains(s, "<max_thinking_length>")
}
