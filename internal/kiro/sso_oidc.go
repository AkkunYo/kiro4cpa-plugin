package kiro

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultSSORegion = "us-east-1"
	builderIDIssuer  = "https://identitycenter.amazonaws.com/ssoins-722374e8c3c8e6c6"
	builderStartURL  = "https://view.awsapps.com/start"
)

var (
	ErrAuthPending = errors.New("authorization_pending")
	ErrSlowDown    = errors.New("slow_down")
)

type RegisterClientResponse struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

type StartDeviceAuthResponse struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"`
	Interval                int    `json:"interval"`
}

type CreateTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	TokenType    string `json:"tokenType"`
	ExpiresIn    int    `json:"expiresIn"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
}

type SSOOIDCClient struct {
	httpClient *http.Client
	baseURLFn  func(region string) string
}

var newSSOOIDCClientFn = NewSSOOIDCClient

func NewSSOOIDCClient() *SSOOIDCClient {
	return &SSOOIDCClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURLFn: func(region string) string {
			if strings.TrimSpace(region) == "" {
				region = defaultSSORegion
			}
			return fmt.Sprintf("https://oidc.%s.amazonaws.com", region)
		},
	}
}

func newSSOOIDCClientForTest(endpoint string) *SSOOIDCClient {
	return &SSOOIDCClient{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		baseURLFn:  func(string) string { return endpoint },
	}
}

func (c *SSOOIDCClient) RegisterClient(ctx context.Context, region string) (*RegisterClientResponse, error) {
	url := fmt.Sprintf("%s/client/register", c.baseURLFn(region))
	return c.registerClientWithURL(ctx, url)
}

func (c *SSOOIDCClient) registerClientWithURL(ctx context.Context, url string) (*RegisterClientResponse, error) {
	payload := map[string]any{
		"clientName": "Kiro CLI",
		"clientType": "public",
		"scopes":     []string{"codewhisperer:completions", "codewhisperer:analysis", "codewhisperer:conversations"},
		"grantTypes": []string{"urn:ietf:params:oauth:grant-type:device_code", "refresh_token"},
		"issuerUrl":  builderIDIssuer,
	}
	var resp RegisterClientResponse
	if err := c.postJSON(ctx, url, payload, &resp); err != nil {
		return nil, fmt.Errorf("register client: %w", err)
	}
	return &resp, nil
}

func (c *SSOOIDCClient) StartDeviceAuth(ctx context.Context, clientID, clientSecret, startURL, region string) (*StartDeviceAuthResponse, error) {
	url := fmt.Sprintf("%s/device_authorization", c.baseURLFn(region))
	return c.startDeviceAuthWithURL(ctx, url, clientID, clientSecret, startURL)
}

func (c *SSOOIDCClient) startDeviceAuthWithURL(ctx context.Context, url, clientID, clientSecret, startURL string) (*StartDeviceAuthResponse, error) {
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"startUrl":     startURL,
	}
	var resp StartDeviceAuthResponse
	if err := c.postJSON(ctx, url, payload, &resp); err != nil {
		return nil, fmt.Errorf("start device auth: %w", err)
	}
	return &resp, nil
}

func (c *SSOOIDCClient) CreateToken(ctx context.Context, clientID, clientSecret, deviceCode, region string) (*CreateTokenResponse, error) {
	url := fmt.Sprintf("%s/token", c.baseURLFn(region))
	return c.createTokenWithURL(ctx, url, clientID, clientSecret, deviceCode)
}

func (c *SSOOIDCClient) createTokenWithURL(ctx context.Context, url, clientID, clientSecret, deviceCode string) (*CreateTokenResponse, error) {
	payload := map[string]string{
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"grantType":    "urn:ietf:params:oauth:grant-type:device_code",
		"deviceCode":   deviceCode,
	}
	var resp CreateTokenResponse
	err := c.postJSON(ctx, url, payload, &resp)
	if err != nil {
		if strings.Contains(err.Error(), "authorization_pending") {
			return nil, ErrAuthPending
		}
		if strings.Contains(err.Error(), "slow_down") {
			return nil, ErrSlowDown
		}
		return nil, err
	}
	return &resp, nil
}

func (c *SSOOIDCClient) postJSON(ctx context.Context, url string, reqBody any, out any) error {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errObj struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errObj)
		msg := errObj.Error
		if errObj.ErrorDescription != "" {
			msg += ": " + errObj.ErrorDescription
		}
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return errors.New(msg)
	}

	return json.NewDecoder(resp.Body).Decode(out)
}
