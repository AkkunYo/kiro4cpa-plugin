package kiro

import (
	"crypto/rand"
	"fmt"
	"net/url"
	"regexp"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
)

// Regions are spliced into credential-bearing hostnames, so anything outside
// the AWS region shape is rejected to keep tokens on AWS/Kiro hosts.
var regionPattern = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)

const (
	socialPlaceholderARN    = "arn:aws:codewhisperer:us-east-1:699475941385:profile/EHGA3GRVQMUK"
	builderIDPlaceholderARN = "arn:aws:codewhisperer:us-east-1:638616132270:profile/AAAACCCCXXXX"
)

func validateRegion(region string) error {
	if !regionPattern.MatchString(region) {
		return fmt.Errorf("invalid kiro region %q", region)
	}
	return nil
}

// streamingProfileARN is the profileArn injected into generateAssistantResponse.
// Placeholders are only valid there; usage and model calls send the real ARN or none.
func streamingProfileARN(c Credential) string {
	switch {
	case c.AuthMethod == AuthMethodAPIKey:
		return ""
	case c.ProfileArn != "":
		return c.ProfileArn
	case c.AuthMethod == AuthMethodSocial:
		return socialPlaceholderARN
	default:
		return builderIDPlaceholderARN
	}
}

func generateURL(c Credential, cfg config.PluginConfig) (string, error) {
	region := c.apiRegion(cfg.DefaultRegion)
	if err := validateRegion(region); err != nil {
		return "", err
	}
	return fmt.Sprintf("https://q.%s.amazonaws.com/generateAssistantResponse", region), nil
}

func listModelsURL(c Credential, nextToken string) string {
	q := url.Values{"origin": {"AI_EDITOR"}}
	if c.ProfileArn != "" {
		q.Set("profileArn", c.ProfileArn)
	}
	if nextToken != "" {
		q.Set("nextToken", nextToken)
	}
	return fmt.Sprintf("https://q.%s.amazonaws.com/ListAvailableModels?%s", c.restRegion(), q.Encode())
}

func streamHeaders(c Credential, cfg config.PluginConfig) map[string][]string {
	mid := machineID(c)
	h := map[string][]string{
		"Content-Type":                {"application/json"},
		"x-amzn-codewhisperer-optout": {"true"},
		"x-amzn-kiro-agent-mode":      {"vibe"},
		"x-amz-user-agent":            {fmt.Sprintf("aws-sdk-js/1.0.34 KiroIDE-%s-%s", cfg.ClientVersion, mid)},
		"user-agent": {fmt.Sprintf("aws-sdk-js/1.0.34 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererstreaming#1.0.34 m/E KiroIDE-%s-%s",
			cfg.OSVersion, cfg.NodeVersion, cfg.ClientVersion, mid)},
		"amz-sdk-invocation-id": {newUUID()},
		"amz-sdk-request":       {"attempt=1; max=3"},
		"Authorization":         {"Bearer " + c.bearerToken()},
	}
	if t := tokenType(c); t != "" {
		h["tokentype"] = []string{t}
	}
	return h
}

func runtimeHeaders(c Credential, cfg config.PluginConfig) map[string][]string {
	mid := machineID(c)
	h := map[string][]string{
		"Authorization":    {"Bearer " + c.bearerToken()},
		"x-amz-user-agent": {fmt.Sprintf("aws-sdk-js/1.0.0 KiroIDE-%s-%s", cfg.ClientVersion, mid)},
		"user-agent": {fmt.Sprintf("aws-sdk-js/1.0.0 ua/2.1 os/%s lang/js md/nodejs#%s api/codewhispererruntime#1.0.0 m/N,E KiroIDE-%s-%s",
			cfg.OSVersion, cfg.NodeVersion, cfg.ClientVersion, mid)},
	}
	if t := tokenType(c); t != "" {
		h["tokentype"] = []string{t}
	}
	return h
}

// tokenType is required by upstream for non-OAuth bearer kinds; without it
// the request is rejected silently.
func tokenType(c Credential) string {
	switch c.AuthMethod {
	case AuthMethodAPIKey:
		return "API_KEY"
	case AuthMethodExternalIDP:
		return "EXTERNAL_IDP"
	default:
		return ""
	}
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
