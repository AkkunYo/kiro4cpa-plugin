package models

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	ProviderName = "kiro4cpa"

	DefaultInputTokenLimit  = int64(200000)
	DefaultOutputTokenLimit = int64(8192)

	// Kiro passes the budget through <max_thinking_length>; the cap matches kiro.rs.
	MaxThinkingBudget = 24576
)

// UpstreamModel is one entry of Kiro's ListAvailableModels response.
type UpstreamModel struct {
	ModelID             string       `json:"modelId"`
	ModelName           string       `json:"modelName,omitempty"`
	Description         string       `json:"description,omitempty"`
	TokenLimits         *TokenLimits `json:"tokenLimits,omitempty"`
	SupportedInputTypes []string     `json:"supportedInputTypes,omitempty"`
}

type TokenLimits struct {
	MaxInputTokens  int64 `json:"maxInputTokens,omitempty"`
	MaxOutputTokens int64 `json:"maxOutputTokens,omitempty"`
}

// FallbackModels is what a KIRO FREE account listed on 2026-10-20. It is served
// only for credentials whose live catalogue has never been fetched.
var FallbackModels = []UpstreamModel{
	{ModelID: "auto"},
	{ModelID: "claude-sonnet-4.5"},
	{ModelID: "claude-haiku-4.5"},
	{ModelID: "deepseek-3.2"},
	{ModelID: "minimax-m2.5"},
	{ModelID: "minimax-m2.1"},
	{ModelID: "glm-5"},
	{ModelID: "qwen3-coder-next"},
}

func GetDefaultModelInfos() []pluginapi.ModelInfo {
	return ToModelInfos(FallbackModels)
}

// ToModelInfos publishes Kiro's own model IDs only; client spellings such as
// claude-haiku-4-5-20251001 are mapped by the caller (e.g. NewAPI model mapping).
func ToModelInfos(upstream []UpstreamModel) []pluginapi.ModelInfo {
	out := make([]pluginapi.ModelInfo, 0, len(upstream))
	for _, m := range upstream {
		if id := strings.TrimSpace(m.ModelID); id != "" {
			out = append(out, modelInfo(id, m))
		}
	}
	return out
}

func modelInfo(id string, m UpstreamModel) pluginapi.ModelInfo {
	in, outLimit := DefaultInputTokenLimit, DefaultOutputTokenLimit
	if m.TokenLimits != nil {
		if m.TokenLimits.MaxInputTokens > 0 {
			in = m.TokenLimits.MaxInputTokens
		}
		if m.TokenLimits.MaxOutputTokens > 0 {
			outLimit = m.TokenLimits.MaxOutputTokens
		}
	}
	isClaude := strings.HasPrefix(id, "claude-")
	modelType := "openai"
	if isClaude {
		modelType = "claude"
	}
	display := m.ModelName
	if display == "" {
		display = id
	}
	return pluginapi.ModelInfo{
		ID:                         id,
		Object:                     "model",
		OwnedBy:                    ProviderName,
		Type:                       modelType,
		Name:                       id,
		DisplayName:                display,
		Description:                m.Description,
		InputTokenLimit:            in,
		OutputTokenLimit:           outLimit,
		ContextLength:              in,
		MaxCompletionTokens:        outLimit,
		SupportedGenerationMethods: []string{"generateContent", "chat"},
		SupportedInputModalities:   inputModalities(m.SupportedInputTypes),
		SupportedOutputModalities:  []string{"text"},
		Thinking:                   thinkingSupport(isClaude),
		UserDefined:                true,
	}
}

func inputModalities(types []string) []string {
	if len(types) == 0 {
		return []string{"text"}
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		out = append(out, strings.ToLower(t))
	}
	return out
}

func thinkingSupport(enabled bool) *pluginapi.ThinkingSupport {
	if !enabled {
		return nil
	}
	return &pluginapi.ThinkingSupport{
		Min:            1024,
		Max:            MaxThinkingBudget,
		ZeroAllowed:    true,
		DynamicAllowed: true,
	}
}
