package kiro

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func decodeManagementResponse(t *testing.T, out []byte) pluginapi.ManagementResponse {
	t.Helper()
	var env wire.Envelope
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatalf("decode wire envelope: %v", err)
	}
	if !env.OK {
		t.Fatalf("wire envelope error: %+v", env.Error)
	}
	var resp pluginapi.ManagementResponse
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		t.Fatalf("decode ManagementResponse: %v", err)
	}
	return resp
}

func TestConnectHandlerResourceRoute(t *testing.T) {
	session, _ := createLoginSession(10 * time.Minute)
	req := pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/v0/resource/plugins/kiro4cpa/connect",
		Query:  url.Values{"state": {session.State}},
	}
	reqRaw, _ := json.Marshal(req)

	out, err := handleManagement(reqRaw)
	if err != nil {
		t.Fatalf("handleManagement failed: %v", err)
	}

	resp := decodeManagementResponse(t, out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(resp.Body), "Kiro 账号连接与授权") {
		t.Fatalf("expected HTML body containing title")
	}
}

func TestConnectHandlerSubmitCredential(t *testing.T) {
	session, _ := createLoginSession(10 * time.Minute)

	// Submit API key
	reqBody, _ := json.Marshal(submitCredentialRequest{
		State:      session.State,
		Method:     "api_key",
		KiroAPIKey: "kiro-secret-key-123",
	})
	req := pluginapi.ManagementRequest{
		Method: http.MethodPost,
		Path:   "/plugins/kiro4cpa/api/submit-credential",
		Body:   reqBody,
	}
	reqRaw, _ := json.Marshal(req)

	out, err := handleManagement(reqRaw)
	if err != nil {
		t.Fatalf("submit-credential failed: %v", err)
	}
	resp := decodeManagementResponse(t, out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", resp.StatusCode, string(resp.Body))
	}

	// Poll should now be success
	pollReq := pluginapi.ManagementRequest{
		Method: http.MethodGet,
		Path:   "/plugins/kiro4cpa/api/poll",
		Query:  url.Values{"state": {session.State}},
	}
	pollRaw, _ := json.Marshal(pollReq)
	pollOut, _ := handleManagement(pollRaw)
	pollResp := decodeManagementResponse(t, pollOut)
	var pollBody map[string]string
	json.Unmarshal(pollResp.Body, &pollBody)
	if pollBody["status"] != "success" {
		t.Fatalf("expected poll status success, got %s", pollBody["status"])
	}
}
