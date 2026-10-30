package kiro

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const toolRequest = `{"model":"claude-haiku-4-5","thinking":{"type":"enabled","budget_tokens":2048},
 "tools":[{"name":"Read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`

func execRequest(payload string) executorRPC {
	return executorRPC{ExecutorRequest: pluginapi.ExecutorRequest{
		AuthID: "kiro.json", StorageJSON: []byte(kiroRsSocial), Payload: []byte(payload),
		AuthAttributes: map[string]string{"path": "/auth/kiro.json"},
	}, HostCallbackID: "cb-1"}
}

func kiroBody(frames ...[]byte) []byte {
	var out []byte
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

func decodeEnvelope(t *testing.T, out []byte) wire.Envelope {
	t.Helper()
	var env wire.Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestExecuteNonStreamBuildsClaudeMessage(t *testing.T) {
	seen := stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 200, Body: kiroBody(
			eventFrame("assistantResponseEvent", `{"content":"<thinking>plan</thinking>\n\nHello"}`),
			eventFrame("toolUseEvent", `{"name":"Read","toolUseId":"t1","input":"{\"p\":"}`),
			eventFrame("toolUseEvent", `{"name":"Read","toolUseId":"t1","input":"\"a\"}","stop":true}`),
			eventFrame("metadataEvent", `{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":7,"cacheReadInputTokens":5}}`),
		)}, nil
	})
	resp := callHandler[pluginapi.ExecutorResponse](t, pluginabi.MethodExecutorExecute, execRequest(toolRequest))

	var msg struct {
		Model      string            `json:"model"`
		StopReason string            `json:"stop_reason"`
		Content    []json.RawMessage `json:"content"`
		Usage      map[string]int    `json:"usage"`
	}
	if err := json.Unmarshal(resp.Payload, &msg); err != nil {
		t.Fatal(err)
	}
	if msg.Model != "claude-haiku-4.5" || msg.StopReason != "tool_use" || len(msg.Content) != 3 {
		t.Fatalf("message %s", resp.Payload)
	}
	want := []string{`"thinking":"plan"`, `"text":"Hello"`, `"input":{"p":"a"}`}
	for i, w := range want {
		if !strings.Contains(string(msg.Content[i]), w) {
			t.Errorf("block %d = %s, want %s", i, msg.Content[i], w)
		}
	}
	if msg.Usage["input_tokens"] != 11 || msg.Usage["output_tokens"] != 7 || msg.Usage["cache_read_input_tokens"] != 5 {
		t.Fatalf("usage %v", msg.Usage)
	}
	var sent kiroPayload
	_ = json.Unmarshal((*seen)[0].Body, &sent)
	if (*seen)[0].URL != "https://q.us-east-1.amazonaws.com/generateAssistantResponse" || (*seen)[0].HostCallbackID != "cb-1" ||
		sent.ConversationState.CurrentMessage.UserInputMessage.ModelID != "claude-haiku-4.5" {
		t.Fatalf("upstream request %s %s", (*seen)[0].URL, (*seen)[0].Body)
	}
}

func TestExecuteRefreshesOnInvalidTokenAndPersists(t *testing.T) {
	var saved string
	origSave := hostAuthSave
	hostAuthSave = func(name string, raw []byte) error {
		saved = name + ":" + string(raw)
		return nil
	}
	t.Cleanup(func() { hostAuthSave = origSave })
	generateCalls := 0
	stubHTTP(t, func(r hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		if strings.Contains(r.URL, "refreshToken") {
			return ok(`{"accessToken":"fresh","expiresIn":3600}`)
		}
		generateCalls++
		if r.Headers["Authorization"][0] != "Bearer fresh" {
			return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte("The bearer token included in the request is invalid")}, nil
		}
		return &hostapi.HTTPResponse{StatusCode: 200, Body: eventFrame("assistantResponseEvent", `{"content":"pong"}`)}, nil
	})
	resp := callHandler[pluginapi.ExecutorResponse](t, pluginabi.MethodExecutorExecute, execRequest(`{"model":"auto","messages":[{"role":"user","content":"ping"}]}`))
	if generateCalls != 2 || !strings.Contains(string(resp.Payload), `"text":"pong"`) {
		t.Fatalf("calls=%d payload=%s", generateCalls, resp.Payload)
	}
	if !strings.HasPrefix(saved, "kiro.json:") || !strings.Contains(saved, `"accessToken":"fresh"`) || !strings.Contains(saved, `"subscriptionTitle":"KIRO FREE"`) {
		t.Fatalf("saved %s", saved)
	}
}

func TestExecuteMapsUpstreamErrors(t *testing.T) {
	cases := []struct {
		status    int
		body      string
		want      int
		retryable bool
	}{
		{429, "slow", 429, false},
		{402, `{"reason":"MONTHLY_REQUEST_COUNT"}`, 402, false},
		{403, "User is not authorized", 403, false},
		{400, "INVALID_MODEL_ID", 400, false},
		{503, "down", 502, true},
	}
	for _, tc := range cases {
		stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
			return &hostapi.HTTPResponse{StatusCode: tc.status, Body: []byte(tc.body)}, nil
		})
		raw, _ := json.Marshal(execRequest(toolRequest))
		out, err := HandleMethod(pluginabi.MethodExecutorExecute, raw)
		env := decodeEnvelope(t, out)
		if err != nil || env.OK || env.Error.HTTPStatus != tc.want || env.Error.Retryable != tc.retryable {
			t.Errorf("upstream %d: env=%s err=%v", tc.status, out, err)
		}
	}
}

func TestExecuteStreamExceptionWithoutContentIsRetryable(t *testing.T) {
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 200, Body: encodeFrame(
			map[string]string{":message-type": "exception", ":exception-type": "InternalServerException"}, []byte("boom"))}, nil
	})
	raw, _ := json.Marshal(execRequest(toolRequest))
	out, _ := HandleMethod(pluginabi.MethodExecutorExecute, raw)
	if env := decodeEnvelope(t, out); env.OK || env.Error.HTTPStatus != 502 || !env.Error.Retryable {
		t.Fatalf("env %s", out)
	}
}

func TestExecuteNonStreamExceptionAfterContentFails(t *testing.T) {
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 200, Body: kiroBody(
			eventFrame("assistantResponseEvent", `{"content":"partial"}`),
			encodeFrame(map[string]string{":message-type": "exception", ":exception-type": "InternalServerException"}, []byte("boom")),
		)}, nil
	})
	raw, _ := json.Marshal(execRequest(pingRequest))
	out, _ := HandleMethod(pluginabi.MethodExecutorExecute, raw)
	if env := decodeEnvelope(t, out); env.OK || env.Error.HTTPStatus != 502 {
		t.Fatalf("truncated answer must not look complete: %s", out)
	}
}

func TestExecuteNonStreamStopReasons(t *testing.T) {
	cases := []struct {
		name   string
		frames []byte
		want   string
	}{
		{"context full", kiroBody(eventFrame("assistantResponseEvent", `{"content":"x"}`),
			eventFrame("contextUsageEvent", `{"contextUsagePercentage":100}`)), "model_context_window_exceeded"},
		{"thinking only", eventFrame("reasoningContentEvent", `{"text":"plan"}`), "max_tokens"},
	}
	for _, tc := range cases {
		stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
			return &hostapi.HTTPResponse{StatusCode: 200, Body: tc.frames}, nil
		})
		resp := callHandler[pluginapi.ExecutorResponse](t, pluginabi.MethodExecutorExecute, execRequest(toolRequest))
		var msg struct {
			StopReason string `json:"stop_reason"`
		}
		if err := json.Unmarshal(resp.Payload, &msg); err != nil || msg.StopReason != tc.want {
			t.Errorf("%s: %s", tc.name, resp.Payload)
		}
	}
}

func TestHandleMethodRecoversPanic(t *testing.T) {
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) { panic("boom") })
	raw, _ := json.Marshal(execRequest(pingRequest))
	out, err := HandleMethod(pluginabi.MethodExecutorExecute, raw)
	if err == nil || out != nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

func TestExecuteRejectsBadInput(t *testing.T) {
	raw, _ := json.Marshal(execRequest(`{"model":"auto","messages":[]}`))
	out, _ := HandleMethod(pluginabi.MethodExecutorExecute, raw)
	if env := decodeEnvelope(t, out); env.OK || env.Error.HTTPStatus != 400 {
		t.Fatalf("env %s", out)
	}
}

func TestCountTokens(t *testing.T) {
	resp := callHandler[pluginapi.ExecutorResponse](t, pluginabi.MethodExecutorCountTokens, execRequest(toolRequest))
	var body map[string]int
	if err := json.Unmarshal(resp.Payload, &body); err != nil || body["input_tokens"] <= 0 {
		t.Fatalf("payload %s", resp.Payload)
	}
}

func TestPersistCredentialFallsBackToAuthID(t *testing.T) {
	var savedName string
	var savedData []byte
	oldSave := hostAuthSave
	hostAuthSave = func(name string, raw []byte) error {
		savedName = name
		savedData = raw
		return nil
	}
	defer func() { hostAuthSave = oldSave }()

	req := executorRPC{
		ExecutorRequest: pluginapi.ExecutorRequest{
			AuthID:      "kiro4cpa-google-user@example.com.json",
			StorageJSON: []byte(`{"type":"kiro4cpa","refreshToken":"old-rt"}`),
		},
	}
	persistCredential(req.refreshTarget(), Credential{RefreshToken: "new-rt"})
	if savedName != "kiro4cpa-google-user@example.com.json" {
		t.Fatalf("savedName = %q, want AuthID fallback", savedName)
	}
	if !strings.Contains(string(savedData), `"refreshToken":"new-rt"`) {
		t.Fatalf("savedData = %s, missing new token", savedData)
	}
}
