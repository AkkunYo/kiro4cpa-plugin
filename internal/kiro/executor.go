package kiro

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type executorRPC struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// hostAuthSave is swapped in tests.
var hostAuthSave = hostapi.AuthSave

func handleExecute(request []byte) ([]byte, error) {
	var req executorRPC
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cred, _, err := decodeCredential(req.StorageJSON)
	if err != nil {
		return wire.ErrorStatus("invalid_credential", err.Error(), http.StatusUnauthorized, false), nil
	}
	conv, err := convertClaudeRequest(req.Payload, cred)
	if err != nil {
		return wire.ErrorStatus("invalid_request", err.Error(), http.StatusBadRequest, false), nil
	}
	resp, err := sendWithRefresh(req, cred, conv)
	if err != nil {
		return upstreamErrorEnvelope(err), nil
	}
	var collector responseCollector
	var dec Decoder
	for _, f := range dec.Feed(resp.Body) {
		ev, errEv := DecodeEvent(f)
		if errEv == nil {
			collector.add(ev)
		}
	}
	if collector.streamErr != nil && collector.empty() {
		return wire.ErrorStatus("upstream_stream_error", collector.streamErr.Error(), http.StatusBadGateway, true), nil
	}
	return wire.OK(pluginapi.ExecutorResponse{Payload: collector.message(conv)}), nil
}

func handleCountTokens(request []byte) ([]byte, error) {
	var req executorRPC
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	var claude claudeRequest
	if err := json.Unmarshal(req.Payload, &claude); err != nil {
		return wire.ErrorStatus("invalid_request", err.Error(), http.StatusBadRequest, false), nil
	}
	payload, _ := json.Marshal(map[string]int{"input_tokens": estimateRequestTokens(claude)})
	return wire.OK(pluginapi.ExecutorResponse{Payload: payload}), nil
}

// sendWithRefresh posts to Kiro and, when the bearer token is rejected, forces one
// refresh and retries. CPA only cools a 401'd credential down, so without this a
// single-account setup would be unusable until the next scheduled refresh.
func sendWithRefresh(req executorRPC, cred Credential, conv convertedRequest) (*hostapi.HTTPResponse, error) {
	cfg := config.Get()
	resp, err := postGenerate(req.HostCallbackID, cred, conv, cfg)
	var up *UpstreamError
	if !errors.As(err, &up) || up.Kind != ErrTokenInvalid || cred.AuthMethod == AuthMethodAPIKey {
		return resp, err
	}
	refreshed, errRefresh := refreshCredential(cred, cfg, req.HostCallbackID, time.Now())
	if errRefresh != nil {
		return nil, err
	}
	persistCredential(req, refreshed)
	return postGenerate(req.HostCallbackID, refreshed, conv, cfg)
}

func postGenerate(callbackID string, cred Credential, conv convertedRequest, cfg config.PluginConfig) (*hostapi.HTTPResponse, error) {
	url, err := generateURL(cred, cfg)
	if err != nil {
		return nil, err
	}
	payload := conv.Payload
	payload.ProfileArn = streamingProfileARN(cred)
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	resp, err := hostHTTPDo(hostapi.HTTPRequest{
		HostCallbackID: callbackID, Method: http.MethodPost, URL: url,
		Headers: streamHeaders(cred, cfg), Body: body,
	})
	if err != nil {
		return nil, fmt.Errorf("kiro request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, classifyUpstream(resp.StatusCode, resp.Body)
	}
	return resp, nil
}

// persistCredential writes a refreshed token back so a rotated refresh token is
// not lost. A failure only costs a later refresh, so it does not fail the request.
func persistCredential(req executorRPC, cred Credential) {
	name := filepath.Base(req.AuthAttributes["path"])
	if name == "." || name == "/" {
		return
	}
	fields, err := decodeFields(req.StorageJSON)
	if err == nil {
		var raw []byte
		if raw, err = mergeCredential(fields, cred); err == nil {
			err = hostAuthSave(name, raw)
		}
	}
	if err != nil {
		log.Printf("kiro4cpa: persisting refreshed token for %s failed: %v", req.AuthID, err)
	}
}

func upstreamErrorEnvelope(err error) []byte {
	var up *UpstreamError
	if !errors.As(err, &up) {
		return wire.ErrorStatus("upstream_unavailable", err.Error(), http.StatusBadGateway, true)
	}
	status, retryable := map[ErrorKind]int{
		ErrTokenInvalid: http.StatusUnauthorized, ErrQuotaExhausted: http.StatusPaymentRequired,
		ErrSuspended: http.StatusForbidden, ErrRateLimited: http.StatusTooManyRequests,
		ErrBadRequest: http.StatusBadRequest,
	}[up.Kind], false
	if status == 0 {
		status, retryable = http.StatusBadGateway, true
	}
	return wire.ErrorStatus("upstream_error", up.Error(), status, retryable)
}
