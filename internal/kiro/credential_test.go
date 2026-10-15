package kiro

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const kiroRsSocial = `{"type":"kiro4cpa","id":1,"accessToken":"at","refreshToken":"rt","profileArn":"arn:aws:codewhisperer:us-east-1:111:profile/X",
"expiresAt":"2026-10-20T13:26:29.054870450+00:00","authMethod":"social","authRegion":"us-east-1","email":"a@b.c",
"subscriptionTitle":"KIRO FREE","disabled":false,"metadata":{"type":"x"},"createdAt":"2026-10-20T00:00:00Z"}`

func TestDecodeKiroRsSocial(t *testing.T) {
	c, fields, err := decodeCredential([]byte(kiroRsSocial))
	if err != nil {
		t.Fatal(err)
	}
	if c.AuthMethod != AuthMethodSocial || c.AuthRegion != "us-east-1" || c.Email != "a@b.c" {
		t.Fatalf("got %+v", c)
	}
	if fields["subscriptionTitle"] != "KIRO FREE" {
		t.Fatalf("unknown field lost: %v", fields)
	}
}

func TestNormalizeAiclient2apiSpellings(t *testing.T) {
	cases := []struct{ raw, method, region string }{
		{`{"authMethod":"builder-id","refreshToken":"r","clientId":"c","clientSecret":"s","idcRegion":"eu-central-1"}`, AuthMethodIDC, "eu-central-1"},
		{`{"authMethod":"social","refreshToken":"r","region":"us-east-1"}`, AuthMethodSocial, "us-east-1"},
		{`{"refreshToken":"r"}`, AuthMethodSocial, "us-east-1"},
		{`{"kiroApiKey":"ksk_1"}`, AuthMethodAPIKey, "us-east-1"},
	}
	for _, tc := range cases {
		c, _, err := decodeCredential([]byte(tc.raw))
		if err != nil {
			t.Fatalf("%s: %v", tc.raw, err)
		}
		if c.AuthMethod != tc.method || c.AuthRegion != tc.region {
			t.Fatalf("%s: got method=%s region=%s", tc.raw, c.AuthMethod, c.AuthRegion)
		}
	}
}

func TestDecodeRejectsInvalidCredentials(t *testing.T) {
	for _, raw := range []string{
		`{"authMethod":"social"}`,
		`{"authMethod":"social","refreshToken":"r","authRegion":"evil.example.com/x"}`,
		`{"authMethod":"idc","refreshToken":"r","clientId":"c"}`,
		`{"authMethod":"external_idp","refreshToken":"r","clientId":"c","tokenEndpoint":"http://idp.example.com/token"}`,
		`{"authMethod":"mystery","refreshToken":"r"}`,
		`[{"refreshToken":"r"}]`,
	} {
		if _, _, err := decodeCredential([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestMergeKeepsUnknownFieldsAndOverridesTokens(t *testing.T) {
	c, fields, _ := decodeCredential([]byte(kiroRsSocial))
	c.AccessToken = "new-at"
	out, err := mergeCredential(fields, c)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	if m["accessToken"] != "new-at" || m["createdAt"] == nil || m["type"] != "kiro4cpa" || fields["accessToken"] != "at" {
		t.Fatalf("merged=%v original accessToken=%v", m, fields["accessToken"])
	}
}

func parseRequest(t *testing.T, provider, raw string) pluginapi.AuthParseResponse {
	t.Helper()
	req, _ := json.Marshal(pluginapi.AuthParseRequest{Provider: provider, FileName: "kiro.json", RawJSON: []byte(raw)})
	out, err := HandleMethod(pluginabi.MethodAuthParse, req)
	if err != nil {
		t.Fatal(err)
	}
	var env wire.Envelope
	_ = json.Unmarshal(out, &env)
	var resp pluginapi.AuthParseResponse
	_ = json.Unmarshal(env.Result, &resp)
	return resp
}

func TestAuthParseSchedulesImmediateRefreshWithoutCatalog(t *testing.T) {
	resp := parseRequest(t, "kiro4cpa", kiroRsSocial)
	if !resp.Handled || resp.Auth.Label != "a@b.c" || resp.Auth.Metadata["email"] != "a@b.c" || resp.Auth.Metadata[refreshIntervalKey] != float64(refreshIntervalSeconds) {
		t.Fatalf("got %+v", resp)
	}
	if resp.Auth.NextRefreshAfter.After(time.Now()) {
		t.Fatalf("expected immediate refresh, got %v", resp.Auth.NextRefreshAfter)
	}
}

func TestAuthParseIgnoresOtherProviders(t *testing.T) {
	if resp := parseRequest(t, "antigravity", kiroRsSocial); resp.Handled {
		t.Fatal("claimed another provider's file")
	}
}
