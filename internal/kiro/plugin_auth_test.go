package kiro

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestHandleMethodAuthLoginFlow(t *testing.T) {
	var tokenCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/register":
			_ = json.NewEncoder(w).Encode(RegisterClientResponse{ClientID: "cid-123", ClientSecret: "csec-456"})
		case "/device_authorization":
			_ = json.NewEncoder(w).Encode(StartDeviceAuthResponse{
				DeviceCode:              "dev-code-789",
				UserCode:                "ABCD-EFGH",
				VerificationURIComplete: "https://view.awsapps.com/start/#/device?user_code=ABCD-EFGH",
				ExpiresIn:               600,
				Interval:                1,
			})
		case "/token":
			callCount := atomic.AddInt32(&tokenCalls, 1)
			if callCount == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending","error_description":"Pending"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(CreateTokenResponse{
				AccessToken:  "fresh-access-tok",
				RefreshToken: "fresh-refresh-tok",
				ProfileArn:   "arn:aws:codewhisperer:us-east-1:123456789012:profile/PROFX",
				ExpiresIn:    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	origClientFn := newSSOOIDCClientFn
	newSSOOIDCClientFn = func() *SSOOIDCClient { return newSSOOIDCClientForTest(server.URL) }
	defer func() { newSSOOIDCClientFn = origClientFn }()

	startReq := pluginapi.AuthLoginStartRequest{
		Provider: models.ProviderName,
	}
	startRaw, _ := json.Marshal(startReq)

	outStart, err := HandleMethod(pluginabi.MethodAuthLoginStart, startRaw)
	if err != nil {
		t.Fatalf("MethodAuthLoginStart failed: %v", err)
	}

	var startEnvelope struct {
		OK     bool                             `json:"ok"`
		Result pluginapi.AuthLoginStartResponse `json:"result"`
	}
	if err := json.Unmarshal(outStart, &startEnvelope); err != nil || !startEnvelope.OK {
		t.Fatalf("unmarshal start response: %v (%s)", err, string(outStart))
	}

	res := startEnvelope.Result
	if res.State == "" {
		t.Fatal("expected non-empty state")
	}
	if !strings.HasPrefix(res.URL, "https://view.awsapps.com/start/#/device?user_code=") {
		t.Fatalf("unexpected verification URL: %s", res.URL)
	}
	if res.Metadata["userCode"] != "ABCD-EFGH" {
		t.Fatalf("unexpected userCode metadata: %v", res.Metadata["userCode"])
	}

	// First poll -> pending
	pollReq := pluginapi.AuthLoginPollRequest{
		Provider: models.ProviderName,
		State:    res.State,
		Metadata: res.Metadata,
	}
	pollRaw, _ := json.Marshal(pollReq)

	outPoll, err := HandleMethod(pluginabi.MethodAuthLoginPoll, pollRaw)
	if err != nil {
		t.Fatalf("MethodAuthLoginPoll failed: %v", err)
	}

	var pollEnvelope struct {
		OK     bool                            `json:"ok"`
		Result pluginapi.AuthLoginPollResponse `json:"result"`
	}
	if err := json.Unmarshal(outPoll, &pollEnvelope); err != nil || !pollEnvelope.OK {
		t.Fatalf("unmarshal poll response: %v", err)
	}
	if pollEnvelope.Result.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("expected pending status on first call, got %s", pollEnvelope.Result.Status)
	}

	// Second poll -> success
	outPoll2, err := HandleMethod(pluginabi.MethodAuthLoginPoll, pollRaw)
	if err != nil {
		t.Fatalf("MethodAuthLoginPoll second call failed: %v", err)
	}
	json.Unmarshal(outPoll2, &pollEnvelope)
	if pollEnvelope.Result.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("expected success status, got %s (msg: %s)", pollEnvelope.Result.Status, pollEnvelope.Result.Message)
	}
	if pollEnvelope.Result.Auth.Provider != models.ProviderName {
		t.Fatalf("expected provider %s, got %s", models.ProviderName, pollEnvelope.Result.Auth.Provider)
	}
	if !strings.HasPrefix(pollEnvelope.Result.Auth.FileName, "kiro4cpa-builder-id-") {
		t.Fatalf("unexpected fileName: %s", pollEnvelope.Result.Auth.FileName)
	}

	// Verify storageJSON contains merged valid credential
	cred, _, errDecode := decodeCredential(pollEnvelope.Result.Auth.StorageJSON)
	if errDecode != nil {
		t.Fatalf("decode stored credential: %v", errDecode)
	}
	if cred.AccessToken != "fresh-access-tok" || cred.RefreshToken != "fresh-refresh-tok" {
		t.Fatalf("unexpected tokens in credential: %+v", cred)
	}
}
