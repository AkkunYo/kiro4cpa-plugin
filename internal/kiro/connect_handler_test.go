package kiro

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
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

// callRoute sends one management request and decodes the JSON body.
func callRoute(t *testing.T, method, path string, body any, query url.Values) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	reqRaw, _ := json.Marshal(pluginapi.ManagementRequest{Method: method, Path: "/v0/management" + path, Body: raw, Query: query})
	out, err := HandleMethod(pluginabi.MethodManagementHandle, reqRaw)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp := decodeManagementResponse(t, out)
	var decoded map[string]any
	_ = json.Unmarshal(resp.Body, &decoded)
	return resp.StatusCode, decoded
}

func newSession(t *testing.T) string {
	t.Helper()
	s, err := createLoginSession(10 * time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return s.State
}

func TestManagementRegistrationDeclaresConnectRoutes(t *testing.T) {
	reg := callHandler[managementRegistration](t, pluginabi.MethodManagementRegister, map[string]any{})
	if len(reg.Resources) != 1 || reg.Resources[0].Path != "/connect" {
		t.Fatalf("resources %+v", reg.Resources)
	}
	paths := map[string]bool{}
	for _, r := range reg.Routes {
		paths[r.Method+" "+r.Path] = true
	}
	for _, want := range []string{"POST " + routeDevice, "POST " + routeSocialStart, "POST " + routeSocialComplete, "POST " + routeImport, "GET " + routeStatus} {
		if !paths[want] {
			t.Fatalf("missing route %s in %+v", want, reg.Routes)
		}
	}
	if !getRegistration().Capabilities.ManagementAPI {
		t.Fatal("ManagementAPI capability not declared")
	}
}

func TestConnectPageIsServedWithStrictHeaders(t *testing.T) {
	reqRaw, _ := json.Marshal(pluginapi.ManagementRequest{Method: http.MethodGet, Path: "/v0/resource/plugins/kiro4cpa/connect"})
	out, err := handleManagement(reqRaw)
	if err != nil {
		t.Fatal(err)
	}
	resp := decodeManagementResponse(t, out)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(resp.Body), "Kiro 登录") {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(resp.Headers.Get("Content-Security-Policy"), "frame-ancestors 'none'") || resp.Headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers %+v", resp.Headers)
	}
}

func TestLoginStartReturnsConnectPageAndPollWaitsForMethod(t *testing.T) {
	start := callHandler[pluginapi.AuthLoginStartResponse](t, pluginabi.MethodAuthLoginStart, pluginapi.AuthLoginStartRequest{Provider: "kiro4cpa"})
	if start.URL != "/v0/resource/plugins/kiro4cpa/connect#state="+start.State || len(start.State) != 48 {
		t.Fatalf("start %+v", start)
	}
	poll := callHandler[pluginapi.AuthLoginPollResponse](t, pluginabi.MethodAuthLoginPoll, pluginapi.AuthLoginPollRequest{State: start.State})
	if poll.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("poll %+v", poll)
	}
	gone := callHandler[pluginapi.AuthLoginPollResponse](t, pluginabi.MethodAuthLoginPoll, pluginapi.AuthLoginPollRequest{State: "nope"})
	if gone.Status != pluginapi.AuthLoginStatusError {
		t.Fatalf("unknown state %+v", gone)
	}
}

func TestRoutesRejectUnknownState(t *testing.T) {
	cases := []struct {
		path string
		body any
	}{
		{routeDevice, map[string]string{"state": "missing", "kind": "builder"}},
		{routeSocialStart, map[string]string{"state": "missing"}},
		{routeSocialComplete, map[string]string{"state": "missing", "callbackUrl": "http://127.0.0.1:3128/oauth/callback?code=c&state=s"}},
		{routeImport, map[string]string{"state": "missing", "method": "api_key", "kiroApiKey": "ksk_1"}},
	}
	for _, tc := range cases {
		if status, _ := callRoute(t, http.MethodPost, tc.path, tc.body, nil); status != http.StatusNotFound {
			t.Fatalf("%s: status %d", tc.path, status)
		}
	}
	if status, _ := callRoute(t, http.MethodGet, routeStatus, nil, url.Values{"state": {"missing"}}); status != http.StatusNotFound {
		t.Fatalf("status route: %d", status)
	}
}

func TestDeviceRouteValidatesIdCInput(t *testing.T) {
	state := newSession(t)
	for _, startURL := range []string{"", "http://d-1.awsapps.com/start", "https://evil.com/start", "https://a.b.awsapps.com/start", "https://d-1.awsapps.com/other"} {
		status, body := callRoute(t, http.MethodPost, routeDevice, map[string]string{"state": state, "kind": "idc", "startUrl": startURL, "region": "us-east-1"}, nil)
		if status != http.StatusBadRequest {
			t.Fatalf("startUrl %q: %d %v", startURL, status, body)
		}
	}
	if status, _ := callRoute(t, http.MethodPost, routeDevice, map[string]string{"state": state, "kind": "idc", "startUrl": "https://d-1.awsapps.com/start", "region": "evil/x"}, nil); status != http.StatusBadRequest {
		t.Fatalf("bad region accepted: %d", status)
	}
	if status, _ := callRoute(t, http.MethodPost, routeDevice, map[string]string{"state": state, "kind": "other"}, nil); status != http.StatusBadRequest {
		t.Fatalf("bad kind accepted: %d", status)
	}
	if s, _ := getLoginSession(state); s.Method != "" {
		t.Fatalf("rejected input claimed the session: %q", s.Method)
	}
}

func stubDeviceFlow(t *testing.T, startURL *atomic.Value) {
	t.Helper()
	var tokenCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/register":
			_ = json.NewEncoder(w).Encode(RegisterClientResponse{ClientID: "cid", ClientSecret: "sec"})
		case "/device_authorization":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			startURL.Store(body["startUrl"])
			_ = json.NewEncoder(w).Encode(StartDeviceAuthResponse{DeviceCode: "dc", UserCode: "ABCD-EFGH",
				VerificationURIComplete: "https://view.awsapps.com/start/#/device?user_code=ABCD-EFGH", ExpiresIn: 600, Interval: 1})
		case "/token":
			if atomic.AddInt32(&tokenCalls, 1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(CreateTokenResponse{AccessToken: "at", RefreshToken: "rt", ExpiresIn: 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	origClient, origInterval := newSSOOIDCClientFn, pollDeviceInterval
	newSSOOIDCClientFn = func() *SSOOIDCClient { return newSSOOIDCClientForTest(server.URL) }
	pollDeviceInterval = func(int) time.Duration { return 10 * time.Millisecond }
	t.Cleanup(func() { newSSOOIDCClientFn, pollDeviceInterval = origClient, origInterval })
}

func waitCompleted(t *testing.T, state string) *Credential {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := getLoginSession(state); ok && (s.Completed != nil || s.Error != "") {
			if s.Error != "" {
				t.Fatalf("login failed: %s", s.Error)
			}
			return s.Completed
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("device login did not complete")
	return nil
}

func TestBuilderIDDeviceLoginCompletes(t *testing.T) {
	var startURL atomic.Value
	stubDeviceFlow(t, &startURL)
	state := newSession(t)

	status, body := callRoute(t, http.MethodPost, routeDevice, map[string]string{"state": state, "kind": "builder", "region": "eu-west-1"}, nil)
	if status != http.StatusOK || body["userCode"] != "ABCD-EFGH" || !strings.HasPrefix(body["verificationUri"].(string), "https://view.awsapps.com/") {
		t.Fatalf("%d %v", status, body)
	}
	cred := waitCompleted(t, state)
	if startURL.Load() != builderStartURL || cred.AuthMethod != AuthMethodIDC || cred.Provider != ProviderBuilderID ||
		cred.Region != "us-east-1" || cred.ClientID != "cid" || cred.RefreshToken != "rt" {
		t.Fatalf("credential %+v (startUrl %v)", cred, startURL.Load())
	}
	if status, body := callRoute(t, http.MethodGet, routeStatus, nil, url.Values{"state": {state}}); status != http.StatusOK || body["status"] != "success" {
		t.Fatalf("status route %d %v", status, body)
	}
	// A completed session cannot be reused for another method.
	if status, _ := callRoute(t, http.MethodPost, routeImport, map[string]string{"state": state, "method": "api_key", "kiroApiKey": "ksk_1"}, nil); status != http.StatusConflict {
		t.Fatalf("reuse status %d", status)
	}
}

func TestIdCDeviceLoginIsEnterprise(t *testing.T) {
	var startURL atomic.Value
	stubDeviceFlow(t, &startURL)
	state := newSession(t)
	status, _ := callRoute(t, http.MethodPost, routeDevice, map[string]string{"state": state, "kind": "idc", "startUrl": "https://d-90660b1234.awsapps.com/start/", "region": "eu-central-1"}, nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	cred := waitCompleted(t, state)
	if startURL.Load() != "https://d-90660b1234.awsapps.com/start" || cred.Provider != ProviderEnterprise || cred.AuthRegion != "eu-central-1" {
		t.Fatalf("credential %+v", cred)
	}
}

func TestImportRoute(t *testing.T) {
	cases := []struct {
		body   map[string]string
		method string
		ok     bool
	}{
		{map[string]string{"method": "api_key", "kiroApiKey": " ksk_1 "}, AuthMethodAPIKey, true},
		{map[string]string{"method": "social", "refreshToken": "aorAAAAAG-rt"}, AuthMethodSocial, true},
		{map[string]string{"method": "idc", "refreshToken": "rt", "clientId": "c", "clientSecret": "s", "region": "eu-central-1"}, AuthMethodIDC, true},
		{map[string]string{"method": "idc", "refreshToken": "rt"}, "", false},
		{map[string]string{"method": "social", "refreshToken": "rt", "region": "evil.example.com/x"}, "", false},
		{map[string]string{"method": "api_key"}, "", false},
		{map[string]string{"method": "other"}, "", false},
	}
	for _, tc := range cases {
		state := newSession(t)
		tc.body["state"] = state
		status, body := callRoute(t, http.MethodPost, routeImport, tc.body, nil)
		if !tc.ok {
			if status != http.StatusBadRequest {
				t.Fatalf("%v: status %d %v", tc.body, status, body)
			}
			continue
		}
		s, _ := getLoginSession(state)
		if status != http.StatusOK || s.Completed == nil || s.Completed.AuthMethod != tc.method {
			t.Fatalf("%v: %d %v %+v", tc.body, status, body, s.Completed)
		}
	}
}

func TestSessionRejectsSecondMethod(t *testing.T) {
	state := newSession(t)
	if status, _ := callRoute(t, http.MethodPost, routeSocialStart, map[string]string{"state": state}, nil); status != http.StatusOK {
		t.Fatalf("social start %d", status)
	}
	if status, _ := callRoute(t, http.MethodPost, routeImport, map[string]string{"state": state, "method": "api_key", "kiroApiKey": "ksk_1"}, nil); status != http.StatusConflict {
		t.Fatalf("second method status %d", status)
	}
}
