package kiro

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

//go:embed connect_page.html
var connectPageHTML []byte

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}

	path := strings.TrimPrefix(req.Path, "/v0/resource")
	path = strings.TrimPrefix(path, "/v0")

	switch {
	case req.Method == http.MethodGet && (strings.HasSuffix(path, "/connect") || strings.Contains(path, "/plugins/kiro4cpa/connect")):
		return wire.OK(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers:    http.Header{"Content-Type": []string{"text/html; charset=utf-8"}},
			Body:       connectPageHTML,
		}), nil

	case req.Method == http.MethodPost && strings.HasSuffix(path, "/api/device-code"):
		return handleAPIDeviceCode(req)

	case req.Method == http.MethodGet && strings.HasSuffix(path, "/api/poll"):
		return handleAPIPoll(req)

	case req.Method == http.MethodPost && strings.HasSuffix(path, "/api/submit-credential"):
		return handleAPISubmitCredential(req)

	default:
		return wire.OK(pluginapi.ManagementResponse{
			StatusCode: http.StatusNotFound,
			Headers:    http.Header{"Content-Type": []string{"application/json"}},
			Body:       []byte(`{"error":"not found"}`),
		}), nil
	}
}

type deviceCodeRequest struct {
	State    string `json:"state"`
	Kind     string `json:"kind"` // "builder" or "idc"
	StartURL string `json:"startUrl"`
	Region   string `json:"region"`
}

func handleAPIDeviceCode(req pluginapi.ManagementRequest) ([]byte, error) {
	var body deviceCodeRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	_, ok := getLoginSession(body.State)
	if !ok {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "session expired or not found"})
	}

	region := body.Region
	if region == "" {
		region = defaultSSORegion
	}
	startURL := body.StartURL
	if body.Kind == "builder" || startURL == "" {
		startURL = builderStartURL
	}

	oidc := NewSSOOIDCClient()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	regResp, err := oidc.RegisterClient(ctx, region)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]string{"error": "AWS client registration failed: " + err.Error()})
	}

	authResp, err := oidc.StartDeviceAuth(ctx, regResp.ClientID, regResp.ClientSecret, startURL, region)
	if err != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]string{"error": "AWS device authorization failed: " + err.Error()})
	}

	updateLoginSession(body.State, func(s *LoginSession) {
		s.ClientID = regResp.ClientID
		s.ClientSecret = regResp.ClientSecret
		s.DeviceCode = authResp.DeviceCode
		s.UserCode = authResp.UserCode
		s.VerifyURL = authResp.VerificationURIComplete
		s.Region = region
		s.StartURL = startURL
		s.Polling = true
	})

	// Background poll AWS token
	go pollAWSTokenInBackground(body.State, oidc, regResp.ClientID, regResp.ClientSecret, authResp.DeviceCode, region, startURL, body.Kind, authResp.ExpiresIn)

	return jsonResponse(http.StatusOK, map[string]any{
		"userCode":                authResp.UserCode,
		"verificationUri":         authResp.VerificationURI,
		"verificationUriComplete": authResp.VerificationURIComplete,
		"expiresIn":               authResp.ExpiresIn,
	})
}

func pollAWSTokenInBackground(state string, oidc *SSOOIDCClient, clientID, clientSecret, deviceCode, region, startURL, kind string, expiresIn int) {
	if expiresIn <= 0 {
		expiresIn = 600
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if time.Now().After(deadline) {
				failLoginSession(state, "AWS login timed out")
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			tok, err := oidc.CreateToken(ctx, clientID, clientSecret, deviceCode, region)
			cancel()

			if err == nil {
				authMethod := AuthMethodIDC
				if kind == "builder" {
					authMethod = "builder-id"
				}
				cred := &Credential{
					AuthMethod:   authMethod,
					AccessToken:  tok.AccessToken,
					RefreshToken: tok.RefreshToken,
					ClientID:     clientID,
					ClientSecret: clientSecret,
					Region:       region,
					AuthRegion:   region,
					ProfileArn:   tok.ProfileArn,
					ExpiresAt:    time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339),
				}
				completeLoginSession(state, cred, "Kiro device login completed")
				return
			}

			if errors.Is(err, ErrAuthPending) || errors.Is(err, ErrSlowDown) {
				continue
			}

			failLoginSession(state, "AWS token authorization failed: "+err.Error())
			return
		}
	}
}

func handleAPIPoll(req pluginapi.ManagementRequest) ([]byte, error) {
	state := req.Query.Get("state")
	session, ok := getLoginSession(state)
	if !ok {
		return jsonResponse(http.StatusOK, map[string]string{"status": "error", "error": "session expired or not found"})
	}

	if session.Completed != nil {
		return jsonResponse(http.StatusOK, map[string]string{"status": "success", "message": session.Message})
	}
	if session.Error != "" {
		return jsonResponse(http.StatusOK, map[string]string{"status": "error", "error": session.Error})
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": "pending"})
}

type submitCredentialRequest struct {
	State        string `json:"state"`
	Method       string `json:"method"`
	RefreshToken string `json:"refreshToken"`
	Region       string `json:"region"`
	KiroAPIKey   string `json:"kiroApiKey"`
}

func handleAPISubmitCredential(req pluginapi.ManagementRequest) ([]byte, error) {
	var body submitCredentialRequest
	if err := json.Unmarshal(req.Body, &body); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	_, ok := getLoginSession(body.State)
	if !ok {
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "session expired or not found"})
	}

	if body.Method == "api_key" {
		if strings.TrimSpace(body.KiroAPIKey) == "" {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "Kiro API key is required"})
		}
		cred := &Credential{
			AuthMethod: AuthMethodAPIKey,
			KiroAPIKey: strings.TrimSpace(body.KiroAPIKey),
		}
		completeLoginSession(body.State, cred, "Kiro API key connected")
		return jsonResponse(http.StatusOK, map[string]bool{"ok": true})
	}

	if body.Method == "social" {
		if strings.TrimSpace(body.RefreshToken) == "" {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "refreshToken is required"})
		}
		region := strings.TrimSpace(body.Region)
		if region == "" {
			region = defaultRegion
		}
		cred := &Credential{
			AuthMethod:   AuthMethodSocial,
			RefreshToken: strings.TrimSpace(body.RefreshToken),
			Region:       region,
			AuthRegion:   region,
		}
		completeLoginSession(body.State, cred, "Kiro social token connected")
		return jsonResponse(http.StatusOK, map[string]bool{"ok": true})
	}

	return jsonResponse(http.StatusBadRequest, map[string]string{"error": "unsupported auth method"})
}

func jsonResponse(statusCode int, data any) ([]byte, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return wire.OK(pluginapi.ManagementResponse{
		StatusCode: statusCode,
		Headers:    http.Header{"Content-Type": []string{"application/json"}},
		Body:       b,
	}), nil
}
