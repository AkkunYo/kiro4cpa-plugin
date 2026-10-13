package kiro

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
)

// ErrInvalidGrant means the refresh token is permanently revoked.
var ErrInvalidGrant = errors.New("kiro refresh token rejected (invalid_grant)")

// Observed Kiro access tokens live 3600s; used when upstream omits expiresIn.
const defaultTokenLifetime = time.Hour

// hostHTTPDo is swapped in tests.
var hostHTTPDo = hostapi.HTTPDo

func refreshCredential(c Credential, cfg config.PluginConfig, callbackID string, now time.Time) (Credential, error) {
	if c.AuthMethod == AuthMethodAPIKey {
		return c, nil
	}
	req, err := buildRefreshRequest(c, cfg)
	if err != nil {
		return c, err
	}
	req.HostCallbackID = callbackID
	resp, err := hostHTTPDo(req)
	if err != nil {
		return c, fmt.Errorf("kiro refresh: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return c, refreshFailure(resp.StatusCode, resp.Body)
	}
	return applyRefresh(c, resp.Body, now)
}

func buildRefreshRequest(c Credential, cfg config.PluginConfig) (hostapi.HTTPRequest, error) {
	if err := validateRegion(c.AuthRegion); err != nil {
		return hostapi.HTTPRequest{}, err
	}
	switch c.AuthMethod {
	case AuthMethodIDC:
		body, _ := json.Marshal(map[string]string{
			"clientId": c.ClientID, "clientSecret": c.ClientSecret,
			"refreshToken": c.RefreshToken, "grantType": "refresh_token",
		})
		return hostapi.HTTPRequest{
			Method: http.MethodPost,
			URL:    fmt.Sprintf("https://oidc.%s.amazonaws.com/token", c.AuthRegion),
			Headers: map[string][]string{
				"Content-Type":          {"application/json"},
				"user-agent":            {fmt.Sprintf("aws-sdk-js/3.980.0 ua/2.1 os/%s lang/js md/nodejs#%s api/sso-oidc#3.980.0 m/E KiroIDE", cfg.OSVersion, cfg.NodeVersion)},
				"x-amz-user-agent":      {"aws-sdk-js/3.980.0 KiroIDE"},
				"amz-sdk-invocation-id": {newUUID()},
				"amz-sdk-request":       {"attempt=1; max=4"},
			},
			Body: body,
		}, nil
	case AuthMethodExternalIDP:
		form := url.Values{
			"client_id":     {c.ClientID},
			"grant_type":    {"refresh_token"},
			"refresh_token": {c.RefreshToken},
		}
		if c.Scopes != "" {
			form.Set("scope", c.Scopes)
		}
		return hostapi.HTTPRequest{
			Method: http.MethodPost,
			URL:    c.TokenEndpoint,
			Headers: map[string][]string{
				"Content-Type": {"application/x-www-form-urlencoded"},
				"Accept":       {"application/json"},
			},
			Body: []byte(form.Encode()),
		}, nil
	default:
		body, _ := json.Marshal(map[string]string{"refreshToken": c.RefreshToken})
		return hostapi.HTTPRequest{
			Method: http.MethodPost,
			URL:    fmt.Sprintf("https://prod.%s.auth.desktop.kiro.dev/refreshToken", c.AuthRegion),
			Headers: map[string][]string{
				"Accept":       {"application/json, text/plain, */*"},
				"Content-Type": {"application/json"},
				"User-Agent":   {fmt.Sprintf("KiroIDE-%s-%s", cfg.ClientVersion, machineID(c))},
			},
			Body: body,
		}, nil
	}
}

type kiroRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int64  `json:"expiresIn"`
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

func applyRefresh(c Credential, body []byte, now time.Time) (Credential, error) {
	var r kiroRefreshResponse
	if c.AuthMethod == AuthMethodExternalIDP {
		var o oauthTokenResponse
		if err := json.Unmarshal(body, &o); err != nil {
			return c, fmt.Errorf("decode kiro refresh response: %w", err)
		}
		r = kiroRefreshResponse{AccessToken: o.AccessToken, RefreshToken: o.RefreshToken, ExpiresIn: o.ExpiresIn}
	} else if err := json.Unmarshal(body, &r); err != nil {
		return c, fmt.Errorf("decode kiro refresh response: %w", err)
	}
	if r.AccessToken == "" {
		return c, errors.New("kiro refresh response has no accessToken")
	}
	c.AccessToken = r.AccessToken
	if r.RefreshToken != "" {
		c.RefreshToken = r.RefreshToken
	}
	if r.ProfileArn != "" {
		c.ProfileArn = r.ProfileArn
	}
	lifetime := defaultTokenLifetime
	if r.ExpiresIn > 0 {
		lifetime = time.Duration(r.ExpiresIn) * time.Second
	}
	c.ExpiresAt = now.Add(lifetime).UTC().Format(time.RFC3339)
	return c, nil
}

func refreshFailure(status int, body []byte) error {
	if status == http.StatusBadRequest && strings.Contains(string(body), "invalid_grant") {
		return ErrInvalidGrant
	}
	return classifyUpstream(status, body)
}
