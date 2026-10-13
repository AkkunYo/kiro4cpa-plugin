package kiro

import (
	"strings"
	"testing"
)

var socialCred = Credential{AuthMethod: AuthMethodSocial, AuthRegion: "us-east-1", RefreshToken: "rt", AccessToken: "at",
	ProfileArn: "arn:aws:codewhisperer:us-east-1:111:profile/X"}

func mustConvert(t *testing.T, raw string) convertedRequest {
	t.Helper()
	c, err := convertClaudeRequest([]byte(raw), socialCred)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestConvertClaudeCodeTurn(t *testing.T) {
	c := mustConvert(t, `{"model":"claude-haiku-4-5-20251001","system":[{"type":"text","text":"You are helpful."}],
	 "thinking":{"type":"enabled","budget_tokens":99999},
	 "metadata":{"user_id":"user_abc_account__session_0a1b2c3d-1111-2222-3333-444455556666"},
	 "tools":[{"name":"Read","description":"read a file","input_schema":{"properties":{"p":{"type":"string"}},"required":["p",3]}},
	          {"name":"Write","description":"write"}],
	 "messages":[
	  {"role":"user","content":"read a.txt"},
	  {"role":"assistant","content":[{"type":"thinking","thinking":"hm"},{"type":"tool_use","id":"t1","name":"Read","input":{"p":"a.txt"}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"hello"}]},{"type":"text","text":"now?"}]},
	  {"role":"assistant","content":"prefill to drop"}]}`)

	st := c.Payload.ConversationState
	if c.ModelID != "claude-haiku-4.5" || !c.Thinking || st.ConversationID != "0a1b2c3d-1111-2222-3333-444455556666" {
		t.Fatalf("model=%s thinking=%v conv=%s", c.ModelID, c.Thinking, st.ConversationID)
	}
	if c.Payload.ProfileArn != socialCred.ProfileArn || st.AgentTaskType != "vibe" || st.ChatTriggerType != "MANUAL" {
		t.Fatalf("payload %+v", c.Payload)
	}
	lead := st.History[0].UserInputMessage.Content
	if !strings.HasPrefix(lead, "<thinking_mode>enabled</thinking_mode><max_thinking_length>24576</max_thinking_length>\nYou are helpful.") ||
		!strings.Contains(lead, chunkedWritePolicy) || st.History[1].AssistantResponseMessage.Content != "I will follow these instructions." {
		t.Fatalf("lead pair %q / %+v", lead, st.History[1])
	}
	if len(st.History) != 4 || st.History[2].UserInputMessage.Content != "read a.txt" {
		t.Fatalf("history %+v", st.History)
	}
	a := st.History[3].AssistantResponseMessage
	if a.Content != "<thinking>hm</thinking>" || len(a.ToolUses) != 1 || string(a.ToolUses[0].Input) != `{"p":"a.txt"}` {
		t.Fatalf("assistant %+v", a)
	}
	cur := st.CurrentMessage.UserInputMessage
	ctx := cur.UserInputMessageContext
	if cur.Content != "now?" || cur.Origin != "AI_EDITOR" || ctx.EnvState == nil || len(ctx.ToolResults) != 1 ||
		ctx.ToolResults[0].Content[0].Text != "hello" || ctx.ToolResults[0].Status != "success" {
		t.Fatalf("current %+v ctx %+v", cur, ctx)
	}
	if len(ctx.Tools) != 2 || !strings.Contains(ctx.Tools[1].ToolSpecification.Description, "150 lines") ||
		string(ctx.Tools[0].ToolSpecification.InputSchema.JSON) != `{"additionalProperties":true,"properties":{"p":{"type":"string"}},"required":["p"],"type":"object"}` {
		t.Fatalf("tools %+v", ctx.Tools)
	}
}

func TestConvertDropsOrphansAndAddsPlaceholders(t *testing.T) {
	c := mustConvert(t, `{"model":"glm-5","messages":[
	  {"role":"user","content":"go"},
	  {"role":"assistant","content":[{"type":"tool_use","id":"lost","name":"Bash","input":{}},{"type":"tool_use","id":"t2","name":"Grep","input":{}}]},
	  {"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"},{"type":"tool_result","tool_use_id":"ghost","content":"x"}]}]}`)
	st := c.Payload.ConversationState
	uses := st.History[1].AssistantResponseMessage.ToolUses
	results := st.CurrentMessage.UserInputMessage.UserInputMessageContext.ToolResults
	if len(uses) != 1 || uses[0].ToolUseID != "t2" || len(results) != 1 || results[0].ToolUseID != "t2" {
		t.Fatalf("uses %+v results %+v", uses, results)
	}
	tools := st.CurrentMessage.UserInputMessage.UserInputMessageContext.Tools
	if len(tools) != 1 || tools[0].ToolSpecification.Name != "Grep" || c.Thinking || st.History[0].AssistantResponseMessage != nil {
		t.Fatalf("placeholder tools %+v", tools)
	}
}

func TestConvertMergesUserRunsAndClosesWithAssistant(t *testing.T) {
	c := mustConvert(t, `{"model":"auto","messages":[
	  {"role":"user","content":"a"},{"role":"user","content":[{"type":"image","source":{"media_type":"image/png","data":"AAA"}}]},
	  {"role":"assistant","content":"b"},{"role":"user","content":"c"},{"role":"user","content":"d"},{"role":"user","content":"e"}]}`)
	h := c.Payload.ConversationState.History
	if len(h) != 4 || h[0].UserInputMessage.Content != "a" || len(h[0].UserInputMessage.Images) != 1 ||
		h[0].UserInputMessage.Images[0].Format != "png" || h[2].UserInputMessage.Content != "c\nd" || h[3].AssistantResponseMessage.Content != "OK" {
		t.Fatalf("history %+v", h)
	}
	if c.Payload.ConversationState.CurrentMessage.UserInputMessage.Content != "e" {
		t.Fatalf("current %+v", c.Payload.ConversationState.CurrentMessage)
	}
}

func TestConvertRejectsRequestWithoutUserMessage(t *testing.T) {
	if _, err := convertClaudeRequest([]byte(`{"model":"auto","messages":[{"role":"assistant","content":"x"}]}`), socialCred); err == nil {
		t.Fatal("accepted a request with no user message")
	}
}

func TestExtractThinking(t *testing.T) {
	cases := []struct{ in, thinking, rest string }{
		{"<thinking>\nplan</thinking>\n\nanswer", "plan", "answer"},
		{"<thinking>plan</thinking>", "plan", ""},
		{"use `<thinking>` tags", "", "use `<thinking>` tags"},
		{"no tags here", "", "no tags here"},
		{"<thinking>never closed", "", "<thinking>never closed"},
		{"lead <thinking>p</thinking>\n\nr", "p", "lead r"},
	}
	for _, tc := range cases {
		th, rest := extractThinking(tc.in)
		if th != tc.thinking || rest != tc.rest {
			t.Errorf("%q: thinking=%q rest=%q", tc.in, th, rest)
		}
	}
}

func TestThinkingPrefix(t *testing.T) {
	adaptive := claudeRequest{Thinking: &claudeThinking{Type: "adaptive"}}
	adaptive.OutputConfig = &struct {
		Effort string `json:"effort"`
	}{Effort: "low"}
	cases := []struct {
		name   string
		req    claudeRequest
		suffix bool
		want   string
	}{
		{"enabled default budget", claudeRequest{Thinking: &claudeThinking{Type: "enabled"}}, false, "<max_thinking_length>20000<"},
		{"adaptive effort", adaptive, false, "<thinking_mode>adaptive</thinking_mode><thinking_effort>low</thinking_effort>"},
		{"adaptive default effort", claudeRequest{Thinking: &claudeThinking{Type: "adaptive"}}, false, "<thinking_effort>high<"},
		{"suffix enables", claudeRequest{}, true, "<thinking_mode>enabled</thinking_mode>"},
		{"explicit disable wins over suffix", claudeRequest{Thinking: &claudeThinking{Type: "disabled"}}, true, ""},
		{"off", claudeRequest{}, false, ""},
	}
	for _, tc := range cases {
		got := thinkingPrefix(tc.req, tc.suffix)
		if (tc.want == "" && got != "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q", tc.name, got)
		}
	}
}
