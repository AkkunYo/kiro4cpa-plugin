package kiro

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var idcLogin = Credential{AuthMethod: AuthMethodIDC, AuthRegion: "us-east-1", AccessToken: "at",
	RefreshToken: "rt", ClientID: "cid", ClientSecret: "sec"}

func TestResolveAccountIdentityReadsUserInfo(t *testing.T) {
	seen := stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return ok(`{"userInfo":{"email":"a@b.c","userId":"u-1"},"usageBreakdownList":[]}`)
	})
	got := resolveAccountIdentity(idcLogin, config.Get(), time.Now())
	if got.Email != "a@b.c" || got.UserID != "u-1" {
		t.Fatalf("identity %+v", got)
	}
	r := (*seen)[0]
	if !strings.Contains(r.URL, "/getUsageLimits?") || !strings.Contains(r.URL, "isEmailRequired=true") ||
		r.Headers["Authorization"][0] != "Bearer at" {
		t.Fatalf("request %+v", r)
	}
}

func TestResolveAccountIdentityFallsBackToNextRegion(t *testing.T) {
	seen := stubHTTP(t, func(r hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		if strings.Contains(r.URL, "q.us-east-1.") {
			return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte(`{}`)}, nil
		}
		return ok(`{"userInfo":{"userId":"u-eu"}}`)
	})
	got := resolveAccountIdentity(idcLogin, config.Get(), time.Now())
	if got.UserID != "u-eu" || len(*seen) != 2 {
		t.Fatalf("identity %+v after %d calls", got, len(*seen))
	}
}

func TestResolveAccountIdentityIsBestEffort(t *testing.T) {
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) { return nil, errors.New("offline") })
	got := resolveAccountIdentity(idcLogin, config.Get(), time.Now())
	if got.Email != "" || got.UserID != "" || got.AccessToken != "at" {
		t.Fatalf("credential changed on failure: %+v", got)
	}
}

func TestResolveAccountIdentityRefreshesSocialTokenFirst(t *testing.T) {
	seen := stubHTTP(t, func(r hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		if strings.Contains(r.URL, "/refreshToken") {
			return ok(`{"accessToken":"fresh","expiresIn":3600}`)
		}
		return ok(`{"userInfo":{"email":"s@b.c","userId":"u-s"}}`)
	})
	social := Credential{AuthMethod: AuthMethodSocial, AuthRegion: "us-east-1", RefreshToken: "rt"}
	got := resolveAccountIdentity(social, config.Get(), time.Now())
	if got.AccessToken != "fresh" || got.UserID != "u-s" {
		t.Fatalf("identity %+v", got)
	}
	if (*seen)[1].Headers["Authorization"][0] != "Bearer fresh" {
		t.Fatalf("identity call used stale token: %+v", (*seen)[1])
	}
}

func TestResolveAccountIdentitySkipsAPIKey(t *testing.T) {
	seen := stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) { return ok(`{}`) })
	resolveAccountIdentity(Credential{AuthMethod: AuthMethodAPIKey, KiroAPIKey: "ksk_1"}, config.Get(), time.Now())
	if len(*seen) != 0 {
		t.Fatalf("api key login made %d upstream calls", len(*seen))
	}
}

func TestAuthFileNameStableForSameAccount(t *testing.T) {
	now := time.Now()
	a := idcLogin
	a.UserID = "u-1"
	b := a
	b.AccessToken, b.RefreshToken = "other-at", "other-rt"

	first := authFileName(a, "state-aaaaaaaa", now)
	second := authFileName(b, "state-bbbbbbbb", now)
	if first != second {
		t.Fatalf("same account produced %q and %q", first, second)
	}
	other := a
	other.UserID = "u-2"
	if authFileName(other, "state-aaaaaaaa", now) == first {
		t.Fatal("different accounts share a file name")
	}
	social := a
	social.AuthMethod = AuthMethodSocial
	if authFileName(social, "state-aaaaaaaa", now) == first {
		t.Fatal("different login methods share a file name")
	}
}

func TestAuthFileNameAndLabelFollowProvider(t *testing.T) {
	now := time.Now()
	cases := []struct {
		cred      Credential
		file, lbl string
	}{
		{Credential{AuthMethod: AuthMethodSocial, Provider: ProviderGoogle, Email: "User.Name@Example.com"},
			"kiro4cpa-google-user.name@example.com.json", "User.Name@Example.com (Google)"},
		{Credential{AuthMethod: AuthMethodSocial, Provider: ProviderGithub, Email: "a@b.c"},
			"kiro4cpa-github-a@b.c.json", "a@b.c (GitHub)"},
		{Credential{AuthMethod: AuthMethodIDC, StartURL: "https://d-1.awsapps.com/start", Email: "e@corp.com"},
			"kiro4cpa-enterprise-e@corp.com.json", "e@corp.com (Enterprise)"},
		{Credential{AuthMethod: AuthMethodIDC, Email: "x/../y@z.io"},
			"kiro4cpa-builderid-x..y@z.io.json", "x/../y@z.io (Builder ID)"},
	}
	for _, tc := range cases {
		if got := authFileName(tc.cred, "state-123456", now); got != tc.file {
			t.Fatalf("file %q, want %q", got, tc.file)
		}
		if got := authLabel(tc.cred, ""); got != tc.lbl {
			t.Fatalf("label %q, want %q", got, tc.lbl)
		}
	}
	key := Credential{AuthMethod: AuthMethodAPIKey, KiroAPIKey: "ksk_1"}
	if got := authFileName(key, "s", now); !strings.HasPrefix(got, "kiro4cpa-apikey-") || strings.Contains(got, "ksk_1") {
		t.Fatalf("api key file %q", got)
	}
	if authLabel(key, "") != "Kiro API Key" {
		t.Fatalf("api key label %q", authLabel(key, ""))
	}
}

func TestAuthFileNameFallsBackToStateWithoutIdentity(t *testing.T) {
	// A shared IdC profile must not merge different users.
	c := idcLogin
	c.ProfileArn = "arn:aws:codewhisperer:us-east-1:1:profile/SHARED"
	if got := authFileName(c, "abcdefgh1234", time.Now()); got != "kiro4cpa-builderid-abcdefgh.json" {
		t.Fatalf("file name %q", got)
	}
}

func TestRepeatedPollReusesResolvedIdentity(t *testing.T) {
	session, err := createLoginSession(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	social := &Credential{AuthMethod: AuthMethodSocial, AuthRegion: "us-east-1", RefreshToken: "rt-once"}
	completeLoginSession(session.State, social, "done")

	calls := 0
	stubHTTP(t, func(r hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		calls++
		if strings.Contains(r.URL, "/refreshToken") {
			if calls > 1 {
				return &hostapi.HTTPResponse{StatusCode: 400, Body: []byte(`{"error":"invalid_grant"}`)}, nil
			}
			return ok(`{"accessToken":"at","refreshToken":"rt-rotated","expiresIn":3600}`)
		}
		return ok(`{"userInfo":{"email":"s@b.c","userId":"u-s"}}`)
	})

	first := buildLoginSuccessResponse(*social, session.State)
	stored, _ := getLoginSession(session.State)
	second := buildLoginSuccessResponse(*stored.Completed, session.State)
	if first.Auth.FileName != second.Auth.FileName || calls != 2 {
		t.Fatalf("files %q / %q after %d calls", first.Auth.FileName, second.Auth.FileName, calls)
	}
}

func TestLoginSuccessResponseWithoutIdentity(t *testing.T) {
	stubHTTP(t, func(hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 500, Body: []byte(`{}`)}, nil
	})
	c := idcLogin
	c.AuthMethod = "builder-id"
	resp := buildLoginSuccessResponse(c, "abcdefgh1234")
	if resp.Status != pluginapi.AuthLoginStatusSuccess {
		t.Fatalf("status %s: %s", resp.Status, resp.Message)
	}
	if resp.Auth.FileName != "kiro4cpa-builderid-abcdefgh.json" || resp.Auth.Label != "Kiro Builder ID" {
		t.Fatalf("auth %q / %q", resp.Auth.FileName, resp.Auth.Label)
	}
}
