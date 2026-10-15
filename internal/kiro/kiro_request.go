package kiro

import "encoding/json"

type kiroPayload struct {
	ConversationState conversationState `json:"conversationState"`
	ProfileArn        string            `json:"profileArn,omitempty"`
}

type conversationState struct {
	ConversationID      string         `json:"conversationId"`
	AgentContinuationID string         `json:"agentContinuationId"`
	AgentTaskType       string         `json:"agentTaskType"`
	ChatTriggerType     string         `json:"chatTriggerType"`
	CurrentMessage      currentMessage `json:"currentMessage"`
	History             []historyEntry `json:"history,omitempty"`
}

type currentMessage struct {
	UserInputMessage userInputMessage `json:"userInputMessage"`
}

// historyEntry holds exactly one of the two message kinds.
type historyEntry struct {
	UserInputMessage         *userInputMessage `json:"userInputMessage,omitempty"`
	AssistantResponseMessage *assistantMessage `json:"assistantResponseMessage,omitempty"`
}

type userInputMessage struct {
	Content                 string        `json:"content"`
	ModelID                 string        `json:"modelId"`
	Origin                  string        `json:"origin,omitempty"`
	Images                  []kiroImage   `json:"images,omitempty"`
	UserInputMessageContext *inputContext `json:"userInputMessageContext,omitempty"`
}

type assistantMessage struct {
	Content  string        `json:"content"`
	ToolUses []kiroToolUse `json:"toolUses,omitempty"`
}

type inputContext struct {
	EnvState    *envState        `json:"envState,omitempty"`
	ToolResults []kiroToolResult `json:"toolResults,omitempty"`
	Tools       []kiroTool       `json:"tools,omitempty"`
}

type envState struct {
	OperatingSystem         string `json:"operatingSystem"`
	CurrentWorkingDirectory string `json:"currentWorkingDirectory"`
}

type kiroImage struct {
	Format string `json:"format"`
	Source struct {
		Bytes string `json:"bytes"`
	} `json:"source"`
}

type kiroToolUse struct {
	ToolUseID string          `json:"toolUseId"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
}

type kiroToolResult struct {
	ToolUseID string     `json:"toolUseId"`
	Content   []kiroText `json:"content"`
	Status    string     `json:"status"`
}

type kiroText struct {
	Text string `json:"text"`
}

type kiroTool struct {
	ToolSpecification kiroToolSpec `json:"toolSpecification"`
}

type kiroToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		JSON json.RawMessage `json:"json"`
	} `json:"inputSchema"`
}
