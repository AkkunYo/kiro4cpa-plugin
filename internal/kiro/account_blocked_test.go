package kiro

import (
	"strings"
	"testing"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const blockedBody = `{"message":"User is not authorized to make this call.","reason":null}`

// refreshWith runs auth.refresh with the token refresh succeeding and the
// model listing answered by listing.
func refreshWith(t *testing.T, storage string, listing func() (*hostapi.HTTPResponse, error)) (pluginapi.AuthRefreshResponse, map[string]any) {
	t.Helper()
	stubHTTP(t, func(r hostapi.HTTPRequest) (*hostapi.HTTPResponse, error) {
		if strings.Contains(r.URL, "/refreshToken") {
			return ok(`{"accessToken":"at2","expiresIn":3600}`)
		}
		return listing()
	})
	resp := callHandler[pluginapi.AuthRefreshResponse](t, pluginabi.MethodAuthRefresh,
		authRefreshRPC{AuthRefreshRequest: pluginapi.AuthRefreshRequest{AuthID: "k.json", AuthProvider: "kiro4cpa", StorageJSON: []byte(storage)}})
	fields, err := decodeFields(resp.Auth.StorageJSON)
	if err != nil {
		t.Fatal(err)
	}
	return resp, fields
}

func TestRefreshDisablesAccountKiroRejects(t *testing.T) {
	resp, fields := refreshWith(t, kiroRsSocial, func() (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte(blockedBody)}, nil
	})
	if !resp.Auth.Disabled || resp.Auth.Metadata["disabled"] != true || resp.Auth.Metadata["note"] != blockedNote {
		t.Fatalf("auth %+v", resp.Auth)
	}
	if fields["disabled"] != true || fields["note"] != blockedNote || fields[blockedAtKey] == nil {
		t.Fatalf("stored fields %v", fields)
	}
}

func TestRefreshKeepsUserNoteWhenBlocking(t *testing.T) {
	storage := strings.Replace(kiroRsSocial, `{`, `{"note":"主力账号",`, 1)
	resp, fields := refreshWith(t, storage, func() (*hostapi.HTTPResponse, error) {
		return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte(blockedBody)}, nil
	})
	if !resp.Auth.Disabled || fields["note"] != "主力账号" {
		t.Fatalf("disabled %v note %v", resp.Auth.Disabled, fields["note"])
	}
}

func TestRefreshDoesNotDisableOnOtherFailures(t *testing.T) {
	for name, listing := range map[string]func() (*hostapi.HTTPResponse, error){
		"bad bearer": func() (*hostapi.HTTPResponse, error) {
			return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte(`{"message":"The bearer token included in the request is invalid."}`)}, nil
		},
		"other 403": func() (*hostapi.HTTPResponse, error) {
			return &hostapi.HTTPResponse{StatusCode: 403, Body: []byte(`{"message":"Invalid profileArn."}`)}, nil
		},
		"server error": func() (*hostapi.HTTPResponse, error) {
			return &hostapi.HTTPResponse{StatusCode: 503, Body: []byte(`down`)}, nil
		},
	} {
		resp, fields := refreshWith(t, kiroRsSocial, listing)
		if resp.Auth.Disabled || fields["note"] != nil {
			t.Fatalf("%s: disabled %v note %v", name, resp.Auth.Disabled, fields["note"])
		}
	}
}

func TestRefreshClearsBlockedNoteWhenKiroServesAgain(t *testing.T) {
	storage := strings.Replace(kiroRsSocial, `{`, `{"note":"`+blockedNote+`","`+blockedAtKey+`":"2026-10-24T00:00:00Z",`, 1)
	resp, fields := refreshWith(t, storage, func() (*hostapi.HTTPResponse, error) {
		return ok(`{"models":[{"modelId":"auto","modelName":"Auto"}]}`)
	})
	if fields["note"] != "" || fields[blockedAtKey] != nil || resp.Auth.Metadata["note"] != "" {
		t.Fatalf("note %v blockedAt %v meta %v", fields["note"], fields[blockedAtKey], resp.Auth.Metadata["note"])
	}
}
