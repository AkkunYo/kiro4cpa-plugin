package kiro

import (
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// TestHandleMethodAuthLoginFlow runs CPA's view of a Builder ID login:
// auth.login.start, the connect page's device call, then auth.login.poll.
func TestHandleMethodAuthLoginFlow(t *testing.T) {
	var startURL atomic.Value
	stubDeviceFlow(t, &startURL)
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return ok(`{"userInfo":{"email":"dev@example.com","userId":"d-1.user-1"}}`)
	})

	start := callHandler[pluginapi.AuthLoginStartResponse](t, pluginabi.MethodAuthLoginStart, pluginapi.AuthLoginStartRequest{Provider: models.ProviderName})
	pollReq := pluginapi.AuthLoginPollRequest{Provider: models.ProviderName, State: start.State}
	if p := callHandler[pluginapi.AuthLoginPollResponse](t, pluginabi.MethodAuthLoginPoll, pollReq); p.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("before method: %+v", p)
	}

	if status, body := callRoute(t, http.MethodPost, routeDevice, map[string]string{"state": start.State, "kind": "builder"}, nil); status != http.StatusOK {
		t.Fatalf("device %d %v", status, body)
	}
	waitCompleted(t, start.State)

	poll := callHandler[pluginapi.AuthLoginPollResponse](t, pluginabi.MethodAuthLoginPoll, pollReq)
	if poll.Status != pluginapi.AuthLoginStatusSuccess || poll.Auth.Provider != models.ProviderName {
		t.Fatalf("poll %+v", poll)
	}
	if poll.Auth.FileName != "kiro4cpa-builderid-dev@example.com.json" || poll.Auth.Label != "dev@example.com (Builder ID)" {
		t.Fatalf("auth %q / %q", poll.Auth.FileName, poll.Auth.Label)
	}
	cred, _, err := decodeCredential(poll.Auth.StorageJSON)
	if err != nil || cred.AccessToken != "at" || cred.Provider != ProviderBuilderID || cred.UserID != "d-1.user-1" {
		t.Fatalf("stored %+v %v", cred, err)
	}
}
