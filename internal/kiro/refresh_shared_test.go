package kiro

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var staleCred = Credential{AuthMethod: AuthMethodSocial, AccessToken: "at1", RefreshToken: "rt1"}

func TestSharedRefreshRunsOnceForConcurrentCallers(t *testing.T) {
	resetRefreshes(t)
	var calls atomic.Int32
	release := make(chan struct{})
	refresh := func(c Credential) (Credential, error) {
		calls.Add(1)
		<-release
		return Credential{AuthMethod: c.AuthMethod, AccessToken: "at2", RefreshToken: "rt2"}, nil
	}

	const callers = 10
	var started, wg sync.WaitGroup
	got := make([]Credential, callers)
	for i := range callers {
		started.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			started.Done()
			got[i], _ = sharedRefresh("a.json", staleCred, refresh)
		}()
	}
	started.Wait()
	close(release)
	wg.Wait()

	// Callers either joined the running refresh or found its result, so the
	// refresh token was spent once whatever the scheduling.
	if n := calls.Load(); n != 1 {
		t.Fatalf("refresh ran %d times, want 1", n)
	}
	for i, c := range got {
		if c.AccessToken != "at2" || c.RefreshToken != "rt2" {
			t.Fatalf("caller %d got %+v", i, c)
		}
	}
}

func TestSharedRefreshAnswersReplacedTokensWithTheirReplacement(t *testing.T) {
	resetRefreshes(t)
	calls := 0
	refresh := func(c Credential) (Credential, error) {
		calls++
		return Credential{AuthMethod: c.AuthMethod, AccessToken: c.AccessToken + "+", RefreshToken: c.RefreshToken + "+"}, nil
	}

	first, _ := sharedRefresh("a.json", staleCred, refresh)
	// A request queued before the write-back still carries rt1.
	late, _ := sharedRefresh("a.json", staleCred, refresh)
	if calls != 1 || late != first {
		t.Fatalf("stale tokens spent again: calls=%d late=%+v first=%+v", calls, late, first)
	}
	// The new tokens are a new state and refresh normally.
	next, _ := sharedRefresh("a.json", first, refresh)
	if calls != 2 || next.RefreshToken != "rt1++" {
		t.Fatalf("calls=%d next=%+v", calls, next)
	}
}

func TestSharedRefreshDoesNotRememberFailures(t *testing.T) {
	resetRefreshes(t)
	calls := 0
	refresh := func(Credential) (Credential, error) {
		calls++
		return Credential{}, errors.New("kiro down")
	}
	for range 2 {
		if _, err := sharedRefresh("a.json", staleCred, refresh); err == nil {
			t.Fatal("failure was not reported")
		}
	}
	if calls != 2 {
		t.Fatalf("a failed refresh was reused: calls=%d", calls)
	}
}

func TestSharedRefreshKeepsCredentialsApart(t *testing.T) {
	resetRefreshes(t)
	calls := 0
	refresh := func(c Credential) (Credential, error) {
		calls++
		return Credential{AuthMethod: c.AuthMethod, AccessToken: "x", RefreshToken: "y"}, nil
	}
	_, _ = sharedRefresh("a.json", staleCred, refresh)
	_, _ = sharedRefresh("b.json", staleCred, refresh)
	// API keys and unnamed credentials always run their own refresh.
	apiKey := Credential{AuthMethod: AuthMethodAPIKey, KiroAPIKey: "k"}
	_, _ = sharedRefresh("c.json", apiKey, refresh)
	_, _ = sharedRefresh("c.json", apiKey, refresh)
	_, _ = sharedRefresh("", staleCred, refresh)
	_, _ = sharedRefresh("", staleCred, refresh)
	if calls != 6 {
		t.Fatalf("refresh ran %d times, want 6", calls)
	}
}

func TestSharedRefreshRecoversAfterPanic(t *testing.T) {
	resetRefreshes(t)
	func() {
		defer func() { _ = recover() }()
		_, _ = sharedRefresh("a.json", staleCred, func(Credential) (Credential, error) { panic("boom") })
	}()
	got, err := sharedRefresh("a.json", staleCred, func(c Credential) (Credential, error) {
		return Credential{AuthMethod: c.AuthMethod, AccessToken: "at2"}, nil
	})
	if err != nil || got.AccessToken != "at2" {
		t.Fatalf("a panicked refresh blocked the credential: %+v %v", got, err)
	}
}

func TestScheduledRefreshReusesExecutorRefresh(t *testing.T) {
	origSave := hostAuthSave
	hostAuthSave = func(string, []byte) error { return nil }
	t.Cleanup(func() { hostAuthSave = origSave })
	refreshCalls := 0
	stubHTTP(t, func(r hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		switch {
		case strings.Contains(r.URL, "refreshToken"):
			refreshCalls++
			return ok(`{"accessToken":"fresh","refreshToken":"rt-rotated","expiresIn":3600}`)
		case strings.Contains(r.URL, "ListAvailableModels"):
			return ok(`{"models":[{"modelId":"auto"}]}`)
		case r.Headers["Authorization"][0] != "Bearer fresh":
			return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte("The bearer token included in the request is invalid")}, nil
		}
		return &hostapi.HTTPResponse{StatusCode: 200, Body: eventFrame("assistantResponseEvent", `{"content":"pong"}`)}, nil
	})
	callHandler[pluginapi.ExecutorResponse](t, pluginabi.MethodExecutorExecute, execRequest(pingRequest))

	// CPA's scheduled refresh still holds the file as it was before the write-back.
	resp := callHandler[pluginapi.AuthRefreshResponse](t, pluginabi.MethodAuthRefresh,
		authRefreshRPC{AuthRefreshRequest: pluginapi.AuthRefreshRequest{AuthID: "kiro.json", StorageJSON: []byte(kiroRsSocial)}})
	fields, _ := decodeFields(resp.Auth.StorageJSON)
	if refreshCalls != 1 || fields["refreshToken"] != "rt-rotated" || resp.Auth.Disabled {
		t.Fatalf("refresh calls=%d stored refreshToken=%v disabled=%v", refreshCalls, fields["refreshToken"], resp.Auth.Disabled)
	}
}
