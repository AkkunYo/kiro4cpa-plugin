package kiro

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSSOOIDCClientFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/client/register":
			json.NewEncoder(w).Encode(RegisterClientResponse{ClientID: "cid-123", ClientSecret: "csec-456"})
		case "/device_authorization":
			json.NewEncoder(w).Encode(StartDeviceAuthResponse{
				DeviceCode:              "dev-code-789",
				UserCode:                "ABCD-EFGH",
				VerificationURIComplete: "https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD-EFGH",
				ExpiresIn:               600,
				Interval:                1,
			})
		case "/token":
			json.NewEncoder(w).Encode(CreateTokenResponse{
				AccessToken:  "access-tok",
				RefreshToken: "refresh-tok",
				ExpiresIn:    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newSSOOIDCClientForTest(server.URL)
	ctx := context.Background()

	reg, err := client.registerClientWithURL(ctx, server.URL+"/client/register")
	if err != nil || reg.ClientID != "cid-123" {
		t.Fatalf("register failed: %v", err)
	}

	auth, err := client.startDeviceAuthWithURL(ctx, server.URL+"/device_authorization", "cid-123", "csec-456", "https://view.awsapps.com/start")
	if err != nil || auth.UserCode != "ABCD-EFGH" {
		t.Fatalf("device auth failed: %v", err)
	}

	tok, err := client.createTokenWithURL(ctx, server.URL+"/token", "cid-123", "csec-456", "dev-code-789")
	if err != nil || tok.AccessToken != "access-tok" {
		t.Fatalf("create token failed: %v", err)
	}
}
