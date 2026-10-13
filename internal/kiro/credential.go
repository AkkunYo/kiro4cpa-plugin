package kiro

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	AuthMethodSocial      = "social"
	AuthMethodIDC         = "idc"
	AuthMethodExternalIDP = "external_idp"
	AuthMethodAPIKey      = "api_key"

	defaultRegion = "us-east-1"
)

// Credential covers the kiro.rs, aiclient2api and CPA spellings of a Kiro credential.
type Credential struct {
	AccessToken   string `json:"accessToken,omitempty"`
	RefreshToken  string `json:"refreshToken,omitempty"`
	ProfileArn    string `json:"profileArn,omitempty"`
	ExpiresAt     string `json:"expiresAt,omitempty"`
	AuthMethod    string `json:"authMethod,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Region        string `json:"region,omitempty"`
	AuthRegion    string `json:"authRegion,omitempty"`
	IdcRegion     string `json:"idcRegion,omitempty"`
	APIRegion     string `json:"apiRegion,omitempty"`
	ClientID      string `json:"clientId,omitempty"`
	ClientSecret  string `json:"clientSecret,omitempty"`
	TokenEndpoint string `json:"tokenEndpoint,omitempty"`
	Scopes        string `json:"scopes,omitempty"`
	KiroAPIKey    string `json:"kiroApiKey,omitempty"`
	MachineID     string `json:"machineId,omitempty"`
	Email         string `json:"email,omitempty"`
}

func decodeFields(raw []byte) (map[string]any, error) {
	fields := map[string]any{}
	if len(bytes.TrimSpace(raw)) == 0 {
		return fields, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&fields); err != nil {
		return nil, fmt.Errorf("decode kiro credential: %w", err)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	return fields, nil
}

// decodeCredential returns the normalized credential together with every
// original field, so unknown keys survive a refresh write-back.
func decodeCredential(raw []byte) (Credential, map[string]any, error) {
	fields, err := decodeFields(raw)
	if err != nil {
		return Credential{}, nil, err
	}
	var cred Credential
	if err := json.Unmarshal(raw, &cred); err != nil {
		return Credential{}, nil, fmt.Errorf("decode kiro credential: %w", err)
	}
	cred = cred.normalized()
	if err := cred.validate(); err != nil {
		return Credential{}, nil, err
	}
	return cred, fields, nil
}

func (c Credential) normalized() Credential {
	switch strings.ToLower(strings.TrimSpace(c.AuthMethod)) {
	case "builder-id", "builderid", "iam", "idc":
		c.AuthMethod = AuthMethodIDC
	case "azuread", "entra", "external_idp", "externalidp":
		c.AuthMethod = AuthMethodExternalIDP
	case "api_key", "apikey":
		c.AuthMethod = AuthMethodAPIKey
	case "social":
		c.AuthMethod = AuthMethodSocial
	case "":
		if c.KiroAPIKey != "" {
			c.AuthMethod = AuthMethodAPIKey
		} else {
			c.AuthMethod = AuthMethodSocial
		}
	}
	c.AuthRegion = firstNonEmpty(c.AuthRegion, c.IdcRegion, c.Region, defaultRegion)
	return c
}

func (c Credential) validate() error {
	if err := validateRegion(c.AuthRegion); err != nil {
		return err
	}
	if c.APIRegion != "" {
		if err := validateRegion(c.APIRegion); err != nil {
			return err
		}
	}
	switch c.AuthMethod {
	case AuthMethodAPIKey:
		if c.KiroAPIKey == "" {
			return errors.New("kiro api_key credential requires kiroApiKey")
		}
		return nil
	case AuthMethodSocial, AuthMethodIDC, AuthMethodExternalIDP:
	default:
		return fmt.Errorf("unsupported kiro authMethod %q", c.AuthMethod)
	}
	if c.RefreshToken == "" {
		return errors.New("kiro credential requires refreshToken")
	}
	switch c.AuthMethod {
	case AuthMethodIDC:
		if c.ClientID == "" || c.ClientSecret == "" {
			return errors.New("kiro idc credential requires clientId and clientSecret")
		}
	case AuthMethodExternalIDP:
		if c.ClientID == "" {
			return errors.New("kiro external_idp credential requires clientId")
		}
		u, err := url.Parse(c.TokenEndpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return errors.New("kiro external_idp credential requires an https tokenEndpoint")
		}
	}
	return nil
}

func (c Credential) bearerToken() string {
	if c.AuthMethod == AuthMethodAPIKey {
		return c.KiroAPIKey
	}
	return c.AccessToken
}

// apiRegion is the region serving generateAssistantResponse: an explicit
// apiRegion, else the real profile ARN's region, else the configured default.
func (c Credential) apiRegion(fallback string) string {
	return firstNonEmpty(c.APIRegion, regionFromARN(c.ProfileArn), fallback, defaultRegion)
}

// restRegion is the region for ListAvailableModels/getUsageLimits, which only
// us-east-1 and eu-central-1 serve.
func (c Credential) restRegion() string {
	if strings.HasPrefix(c.AuthRegion, "eu-") {
		return "eu-central-1"
	}
	return "us-east-1"
}

func regionFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 4 || validateRegion(parts[3]) != nil {
		return ""
	}
	return parts[3]
}

func mergeCredential(fields map[string]any, cred Credential) ([]byte, error) {
	overlayRaw, err := json.Marshal(cred)
	if err != nil {
		return nil, err
	}
	var overlay map[string]any
	if err := json.Unmarshal(overlayRaw, &overlay); err != nil {
		return nil, err
	}
	merged := cloneMap(fields)
	for k, v := range overlay {
		merged[k] = v
	}
	return json.Marshal(merged)
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
