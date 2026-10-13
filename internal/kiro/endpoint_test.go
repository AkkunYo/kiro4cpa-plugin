package kiro

import (
	"strings"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
)

func TestValidateRegion(t *testing.T) {
	for _, r := range []string{"us-east-1", "eu-central-1", "ap-southeast-1", "us-gov-west-1"} {
		if err := validateRegion(r); err != nil {
			t.Errorf("rejected %q", r)
		}
	}
	for _, r := range []string{"", "US-EAST-1", "us-east-1\n", "us-east-1.evil.com", "evil.com/x", "us-east-1@x", "us-east"} {
		if err := validateRegion(r); err == nil {
			t.Errorf("accepted %q", r)
		}
	}
}

func TestStreamingProfileARN(t *testing.T) {
	cases := []struct {
		cred Credential
		want string
	}{
		{Credential{AuthMethod: AuthMethodAPIKey, ProfileArn: "arn:real"}, ""},
		{Credential{AuthMethod: AuthMethodSocial, ProfileArn: "arn:real"}, "arn:real"},
		{Credential{AuthMethod: AuthMethodSocial}, socialPlaceholderARN},
		{Credential{AuthMethod: AuthMethodIDC}, builderIDPlaceholderARN},
	}
	for _, tc := range cases {
		if got := streamingProfileARN(tc.cred); got != tc.want {
			t.Errorf("%+v: got %q", tc.cred, got)
		}
	}
}

func TestGenerateURLRegion(t *testing.T) {
	cfg := config.Get()
	cases := []struct {
		cred Credential
		want string
	}{
		{Credential{ProfileArn: "arn:aws:codewhisperer:eu-central-1:1:profile/X"}, "https://q.eu-central-1.amazonaws.com/generateAssistantResponse"},
		{Credential{APIRegion: "us-west-2", ProfileArn: "arn:aws:codewhisperer:eu-central-1:1:profile/X"}, "https://q.us-west-2.amazonaws.com/generateAssistantResponse"},
		{Credential{ProfileArn: "arn:aws:codewhisperer:evil.com:1:profile/X"}, "https://q.us-east-1.amazonaws.com/generateAssistantResponse"},
	}
	for _, tc := range cases {
		got, err := generateURL(tc.cred, cfg)
		if err != nil || got != tc.want {
			t.Errorf("%+v: got %q err=%v", tc.cred, got, err)
		}
	}
}

func TestListModelsURL(t *testing.T) {
	got := listModelsURL(Credential{AuthRegion: "eu-west-1", ProfileArn: "arn:a/b"}, "tok")
	if got != "https://q.eu-central-1.amazonaws.com/ListAvailableModels?nextToken=tok&origin=AI_EDITOR&profileArn=arn%3Aa%2Fb" {
		t.Fatalf("got %s", got)
	}
	if got := listModelsURL(Credential{AuthRegion: "ap-southeast-1"}, ""); got != "https://q.us-east-1.amazonaws.com/ListAvailableModels?origin=AI_EDITOR" {
		t.Fatalf("got %s", got)
	}
}

func TestHeadersTokenTypeAndBearer(t *testing.T) {
	cfg := config.Get()
	api := streamHeaders(Credential{AuthMethod: AuthMethodAPIKey, KiroAPIKey: "ksk_1"}, cfg)
	if api["tokentype"][0] != "API_KEY" || api["Authorization"][0] != "Bearer ksk_1" {
		t.Fatalf("api key headers %v", api)
	}
	ext := runtimeHeaders(Credential{AuthMethod: AuthMethodExternalIDP, AccessToken: "at", RefreshToken: "rt"}, cfg)
	if ext["tokentype"][0] != "EXTERNAL_IDP" {
		t.Fatalf("external idp headers %v", ext)
	}
	social := streamHeaders(Credential{AuthMethod: AuthMethodSocial, AccessToken: "at", RefreshToken: "rt-test"}, cfg)
	if _, ok := social["tokentype"]; ok || social["Authorization"][0] != "Bearer at" {
		t.Fatalf("social headers %v", social)
	}
	if !strings.HasSuffix(social["user-agent"][0], "KiroIDE-1.4.5-0b57ce32592129d9e832f82147ef2a6cea350b58ca1e0596cfa331778daf69ff") {
		t.Fatalf("user-agent %s", social["user-agent"][0])
	}
}

func TestClassifyUpstream(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   ErrorKind
	}{
		{401, "", ErrTokenInvalid},
		{403, "The bearer token included in the request is invalid", ErrTokenInvalid},
		{403, "your account is suspended; we locked your account", ErrSuspended},
		{402, `{"reason":"MONTHLY_REQUEST_COUNT"}`, ErrQuotaExhausted},
		{429, "", ErrRateLimited},
		{400, "CONTENT_LENGTH_EXCEEDS_THRESHOLD", ErrBadRequest},
		{503, "", ErrRetryable},
		{524, "", ErrRetryable},
	}
	for _, tc := range cases {
		if got := classifyUpstream(tc.status, []byte(tc.body)).Kind; got != tc.want {
			t.Errorf("%d %q: got %d want %d", tc.status, tc.body, got, tc.want)
		}
	}
}
