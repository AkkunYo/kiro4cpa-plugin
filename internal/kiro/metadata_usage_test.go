package kiro

import (
	"encoding/json"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// usageForEventType drives one non-stream call whose upstream emits a single
// usage frame under eventType and returns the usage object the plugin reports.
func usageForEventType(t *testing.T, eventType string) map[string]int {
	t.Helper()
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 200, Body: kiroBody(
			eventFrame("assistantResponseEvent", `{"content":"Hi"}`),
			eventFrame(eventType, `{"tokenUsage":{"uncachedInputTokens":11,"outputTokens":7,"cacheReadInputTokens":5,"cacheWriteInputTokens":3}}`),
		)}, nil
	})
	resp := callHandler[pluginapi.ExecutorResponse](t, pluginabi.MethodExecutorExecute,
		execRequest(`{"model":"claude-haiku-4-5","messages":[{"role":"user","content":"hi"}]}`))
	var msg struct {
		Usage map[string]int `json:"usage"`
	}
	if err := json.Unmarshal(resp.Payload, &msg); err != nil {
		t.Fatal(err)
	}
	return msg.Usage
}

// TestMetadataEventNamesCarryUsage covers both frame names upstream has used for
// the token usage payload. Accepting only metadataEvent reported every cache
// count as zero, because usage fell back to a local estimate.
func TestMetadataEventNamesCarryUsage(t *testing.T) {
	for _, eventType := range []string{"metadataEvent", "messageMetadataEvent"} {
		usage := usageForEventType(t, eventType)
		if usage["input_tokens"] != 11 || usage["output_tokens"] != 7 ||
			usage["cache_read_input_tokens"] != 5 || usage["cache_creation_input_tokens"] != 3 {
			t.Errorf("%s usage = %v, want real upstream counts", eventType, usage)
		}
	}
}

// TestUnknownEventTypeKeepsStreamUsable guards the diagnostic path: an event
// type the plugin does not model must not fail or truncate the answer.
func TestUnknownEventTypeKeepsStreamUsable(t *testing.T) {
	usage := usageForEventType(t, "someFutureEventType")
	if usage == nil {
		t.Fatal("usage missing when an unknown event type was present")
	}
}
