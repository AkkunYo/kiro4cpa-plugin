package hostapi

import (
	"encoding/json"
	"fmt"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// HostCall is the plugin->host bridge injected at init time.
var HostCall func(method string, payload []byte) ([]byte, error)

func callHost(method string, payload []byte) ([]byte, error) {
	if HostCall == nil {
		return nil, fmt.Errorf("host callback not initialized")
	}
	return HostCall(method, payload)
}

// callJSON sends req to a host callback and decodes the envelope result into out.
func callJSON(method string, req any, out any) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	raw, err := callHost(method, payload)
	if err != nil {
		return err
	}
	var env wire.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode %s envelope: %w", method, err)
	}
	if !env.OK {
		if env.Error != nil && env.Error.Message != "" {
			return fmt.Errorf("%s failed: %s", method, env.Error.Message)
		}
		return fmt.Errorf("%s failed", method)
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("decode %s result: %w", method, err)
		}
	}
	return nil
}

type HTTPRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method,omitempty"`
	URL            string              `json:"url,omitempty"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

type HTTPResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

func HTTPDo(req HTTPRequest) (*HTTPResponse, error) {
	var resp HTTPResponse
	if err := callJSON(pluginabi.MethodHostHTTPDo, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// HTTPStreamResponse carries the upstream status and a stream to pull the body from.
type HTTPStreamResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers,omitempty"`
	StreamID   string              `json:"stream_id,omitempty"`
}

type StreamChunk struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type streamRef struct {
	StreamID string `json:"stream_id"`
}

func HTTPDoStream(req HTTPRequest) (*HTTPStreamResponse, error) {
	var resp HTTPStreamResponse
	if err := callJSON(pluginabi.MethodHostHTTPDoStream, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func HTTPStreamRead(streamID string) (StreamChunk, error) {
	var chunk StreamChunk
	err := callJSON(pluginabi.MethodHostHTTPStreamRead, streamRef{StreamID: streamID}, &chunk)
	return chunk, err
}

func HTTPStreamClose(streamID string) error {
	return callJSON(pluginabi.MethodHostHTTPStreamClose, streamRef{StreamID: streamID}, nil)
}

type authSaveRequest struct {
	Name string          `json:"name"`
	JSON json.RawMessage `json:"json"`
}

// AuthSave persists credential JSON to an auth file through the host.
func AuthSave(name string, raw []byte) error {
	return callJSON(pluginabi.MethodHostAuthSave, authSaveRequest{Name: name, JSON: raw}, nil)
}

type streamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

// StreamEmit pushes one executor output chunk to CPA.
func StreamEmit(streamID string, payload []byte) error {
	return callJSON(pluginabi.MethodHostStreamEmit, streamEmitRequest{StreamID: streamID, Payload: payload}, nil)
}

// StreamClose ends executor output; a non-empty errMsg reports a failed stream.
func StreamClose(streamID, errMsg string) error {
	return callJSON(pluginabi.MethodHostStreamClose, streamEmitRequest{StreamID: streamID, Error: errMsg}, nil)
}
