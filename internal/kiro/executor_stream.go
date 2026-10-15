package kiro

import (
	"fmt"
	"log"
	"net/http"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
)

// handleExecuteStream opens the upstream stream synchronously, so an HTTP error
// still reaches CPA as a status it can fail over on, then pumps the body to CPA
// from a goroutine. An empty chunk list tells CPA to wait for host.stream.emit.
func handleExecuteStream(request []byte) ([]byte, error) {
	req, cred, conv, errEnv, err := prepareExecution(request)
	if err != nil || errEnv != nil {
		return errEnv, err
	}
	if req.StreamID == "" {
		return wire.ErrorStatus("invalid_request", "missing stream_id", http.StatusBadRequest, false), nil
	}
	upstream, err := withRefresh(req, cred, func(c Credential) (string, error) {
		return openGenerateStream(req.HostCallbackID, c, conv, config.Get())
	})
	if err != nil {
		return upstreamErrorEnvelope(err), nil
	}
	go pumpStream(upstream, req.StreamID, conv)
	return wire.OK(struct {
		Chunks []any `json:"chunks,omitempty"`
	}{}), nil
}

func openGenerateStream(callbackID string, cred Credential, conv convertedRequest, cfg config.PluginConfig) (string, error) {
	httpReq, err := generateRequest(callbackID, cred, conv, cfg)
	if err != nil {
		return "", err
	}
	resp, err := hostHTTPDoStream(httpReq)
	if err != nil {
		return "", fmt.Errorf("kiro request: %w", err)
	}
	if resp.StatusCode == http.StatusOK {
		return resp.StreamID, nil
	}
	var body []byte
	if resp.StreamID != "" {
		if chunk, errRead := hostStreamRead(resp.StreamID); errRead == nil {
			body = chunk.Payload
		}
		_ = hostHTTPStreamEnd(resp.StreamID)
	}
	return "", classifyUpstream(resp.StatusCode, body)
}

func pumpStream(upstream, streamID string, conv convertedRequest) {
	closed := false
	finish := func(errMsg string) {
		if closed {
			return
		}
		closed = true
		_ = hostHTTPStreamEnd(upstream)
		_ = hostStreamClose(streamID, errMsg)
	}
	defer func() {
		// A panic here would take down the whole CPA process.
		if r := recover(); r != nil {
			log.Printf("kiro4cpa: stream pump panic: %v", r)
			finish(fmt.Sprintf("kiro4cpa internal error: %v", r))
		}
	}()

	sse := newSSEStream(conv)
	// message_start waits for the first content so an early in-band error closes
	// the stream before any byte reaches the client.
	pending := sse.start()
	var dec Decoder
	for {
		chunk, err := hostStreamRead(upstream)
		if err != nil {
			finish("kiro stream read failed: " + err.Error())
			return
		}
		if chunk.Error != "" {
			finish("kiro stream read failed: " + chunk.Error)
			return
		}
		for _, f := range dec.Feed(chunk.Payload) {
			ev, errEv := DecodeEvent(f)
			if errEv != nil {
				continue
			}
			events := sse.handle(ev)
			// A later in-band error also ends the stream: CPA appends an error
			// after the content already sent, so a truncated answer never ends
			// with a clean message_stop.
			if se := sse.streamErr; se != nil {
				log.Printf("kiro4cpa: upstream stream error: %v", se)
				finish(se.Error())
				return
			}
			if len(events) == 0 {
				continue
			}
			if !emit(streamID, append(pending, events...)) {
				finish("")
				return
			}
			pending = nil
		}
		if chunk.Done {
			break
		}
	}
	emit(streamID, append(pending, sse.finish()...))
	finish("")
}

// emit reports false once CPA stops accepting chunks (the client went away).
func emit(streamID string, events []sseEvent) bool {
	for _, c := range sseChunks(events) {
		if err := hostStreamEmit(streamID, c); err != nil {
			return false
		}
	}
	return true
}
