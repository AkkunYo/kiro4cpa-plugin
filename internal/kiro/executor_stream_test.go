package kiro

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

// streamStub fakes the host stream callbacks; closed delivers the error string
// passed to host.stream.close once the pump goroutine finishes.
type streamStub struct {
	status    int
	reads     []hostapi.StreamChunk
	emitted   []string
	emitFails int
	ended     []string
	closed    chan string
}

func stubStream(t *testing.T, s *streamStub) *streamStub {
	t.Helper()
	resetLimiter(t)
	s.closed = make(chan string, 1)
	o1, o2, o3, o4, o5 := hostHTTPDoStream, hostStreamRead, hostHTTPStreamEnd, hostStreamEmit, hostStreamClose
	hostHTTPDoStream = func(hostapi.HTTPRequest) (*hostapi.HTTPStreamResponse, error) {
		return &hostapi.HTTPStreamResponse{StatusCode: s.status, StreamID: "up-1"}, nil
	}
	hostStreamRead = func(string) (hostapi.StreamChunk, error) {
		if len(s.reads) == 0 {
			return hostapi.StreamChunk{Done: true}, nil
		}
		c := s.reads[0]
		s.reads = s.reads[1:]
		return c, nil
	}
	hostHTTPStreamEnd = func(id string) error { s.ended = append(s.ended, id); return nil }
	hostStreamEmit = func(_ string, payload []byte) error {
		if s.emitFails > 0 && len(s.emitted) >= s.emitFails {
			return errors.New("client gone")
		}
		s.emitted = append(s.emitted, string(payload))
		return nil
	}
	hostStreamClose = func(_, errMsg string) error { s.closed <- errMsg; return nil }
	t.Cleanup(func() {
		hostHTTPDoStream, hostStreamRead, hostHTTPStreamEnd, hostStreamEmit, hostStreamClose = o1, o2, o3, o4, o5
	})
	return s
}

func streamCall(t *testing.T, payload string) streamEnvelope {
	t.Helper()
	req := execRequest(payload)
	req.StreamID = "out-1"
	raw, _ := json.Marshal(req)
	out, err := HandleMethod(pluginabi.MethodExecutorExecuteStream, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env streamEnvelope
	_ = json.Unmarshal(out, &env)
	return env
}

type streamEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		HTTPStatus int    `json:"http_status"`
		Message    string `json:"message"`
	} `json:"error"`
}

func waitClosed(t *testing.T, s *streamStub) string {
	t.Helper()
	select {
	case msg := <-s.closed:
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("stream was never closed")
		return ""
	}
}

const pingRequest = `{"model":"auto","messages":[{"role":"user","content":"ping"}]}`

func TestExecuteStreamPumpsSSE(t *testing.T) {
	frames := kiroBody(eventFrame("assistantResponseEvent", `{"content":"po"}`), eventFrame("assistantResponseEvent", `{"content":"ng"}`))
	s := stubStream(t, &streamStub{status: 200, reads: []hostapi.StreamChunk{{Payload: frames[:10]}, {Payload: frames[10:]}}})
	env := streamCall(t, pingRequest)
	if !env.OK || string(env.Result) != "{}" {
		t.Fatalf("envelope %+v %s", env, env.Result)
	}
	if msg := waitClosed(t, s); msg != "" {
		t.Fatalf("closed with error %q", msg)
	}
	all := strings.Join(s.emitted, "")
	for _, want := range []string{"event: message_start\n", `"text":"po"`, `"text":"ng"`, `"stop_reason":"end_turn"`, "event: message_stop\n"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
	for _, c := range s.emitted {
		if !(strings.HasPrefix(c, "event: ") && strings.Count(c, "\n") == 1) && !(strings.HasPrefix(c, "data: ") && strings.HasSuffix(c, "\n\n")) {
			t.Fatalf("chunk is not one SSE line: %q", c)
		}
	}
	if len(s.ended) != 1 || s.ended[0] != "up-1" {
		t.Fatalf("upstream not closed: %v", s.ended)
	}
}

func TestExecuteStreamOpenErrorIsReturned(t *testing.T) {
	s := stubStream(t, &streamStub{status: 429, reads: []hostapi.StreamChunk{{Payload: []byte("slow down")}}})
	env := streamCall(t, pingRequest)
	if env.OK || env.Error.HTTPStatus != 429 || !strings.Contains(env.Error.Message, "slow down") || len(s.emitted) != 0 || len(s.ended) != 1 {
		t.Fatalf("env %+v emitted %v ended %v", env, s.emitted, s.ended)
	}
}

func TestExecuteStreamEarlyExceptionSendsNothing(t *testing.T) {
	exc := encodeFrame(map[string]string{":message-type": "exception", ":exception-type": "ThrottlingException"}, []byte("busy"))
	s := stubStream(t, &streamStub{status: 200, reads: []hostapi.StreamChunk{{Payload: exc}}})
	if env := streamCall(t, pingRequest); !env.OK {
		t.Fatalf("env %+v", env)
	}
	if msg := waitClosed(t, s); !strings.Contains(msg, "ThrottlingException") || len(s.emitted) != 0 {
		t.Fatalf("close=%q emitted=%v", msg, s.emitted)
	}
}

func TestExecuteStreamLateExceptionAborts(t *testing.T) {
	frames := kiroBody(eventFrame("assistantResponseEvent", `{"content":"partial"}`),
		encodeFrame(map[string]string{":message-type": "exception", ":exception-type": "InternalServerException"}, []byte("boom")))
	s := stubStream(t, &streamStub{status: 200, reads: []hostapi.StreamChunk{{Payload: frames}}})
	streamCall(t, pingRequest)
	msg := waitClosed(t, s)
	all := strings.Join(s.emitted, "")
	if !strings.Contains(msg, "InternalServerException") || !strings.Contains(all, `"text":"partial"`) || strings.Contains(all, "message_stop") {
		t.Fatalf("close=%q emitted=%s", msg, all)
	}
}

func TestExecuteStreamReadErrorMessage(t *testing.T) {
	s := stubStream(t, &streamStub{status: 200, reads: []hostapi.StreamChunk{{Error: "reset by peer"}}})
	streamCall(t, pingRequest)
	if msg := waitClosed(t, s); msg != "kiro stream read failed: reset by peer" {
		t.Fatalf("close=%q", msg)
	}
}

func TestExecuteStreamStopsWhenClientLeaves(t *testing.T) {
	frames := kiroBody(eventFrame("assistantResponseEvent", `{"content":"a"}`), eventFrame("assistantResponseEvent", `{"content":"b"}`))
	s := stubStream(t, &streamStub{status: 200, emitFails: 2, reads: []hostapi.StreamChunk{{Payload: frames}}})
	streamCall(t, pingRequest)
	if msg := waitClosed(t, s); msg != "" || len(s.ended) != 1 {
		t.Fatalf("close=%q ended=%v", msg, s.ended)
	}
}

func TestExecuteStreamRequiresStreamID(t *testing.T) {
	raw, _ := json.Marshal(execRequest(pingRequest))
	out, _ := HandleMethod(pluginabi.MethodExecutorExecuteStream, raw)
	if env := decodeEnvelope(t, out); env.OK || env.Error.HTTPStatus != 400 {
		t.Fatalf("env %s", out)
	}
}
