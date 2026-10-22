package kiro

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
)

const accountKeyLength = 12

type usageUserInfo struct {
	UserInfo *struct {
		Email  string `json:"email"`
		UserID string `json:"userId"`
	} `json:"userInfo"`
}

// resolveAccountIdentity fills Email/UserID from getUsageLimits so repeated
// logins of one account map to one auth file. It is best effort: on any
// failure the credential is returned unchanged and login still succeeds.
func resolveAccountIdentity(c Credential, cfg config.PluginConfig, now time.Time) Credential {
	if c.AuthMethod == AuthMethodAPIKey || (c.Email != "" && c.UserID != "") {
		return c
	}
	if c.AccessToken == "" {
		refreshed, err := refreshCredential(c, cfg, "", now)
		if err != nil {
			log.Printf("kiro4cpa: login identity refresh failed: %v", err)
			return c
		}
		c = refreshed
	}
	for _, region := range restRegionCandidates(c) {
		resp, err := hostHTTPDo(hostapi.HTTPRequest{
			Method:  http.MethodGet,
			URL:     identityURL(c, region),
			Headers: runtimeHeaders(c, cfg),
		})
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		var body usageUserInfo
		if json.Unmarshal(resp.Body, &body) != nil || body.UserInfo == nil {
			continue
		}
		c.Email = firstNonEmpty(c.Email, body.UserInfo.Email)
		c.UserID = firstNonEmpty(c.UserID, body.UserInfo.UserID)
		return c
	}
	log.Printf("kiro4cpa: login identity unavailable for %s credential", c.AuthMethod)
	return c
}

func identityURL(c Credential, region string) string {
	q := url.Values{
		"origin":          {"AI_EDITOR"},
		"resourceType":    {"AGENTIC_REQUEST"},
		"isEmailRequired": {"true"},
	}
	if arn := effectiveProfileARN(c); arn != "" {
		q.Set("profileArn", arn)
	}
	return fmt.Sprintf("https://q.%s.amazonaws.com/getUsageLimits?%s", region, q.Encode())
}

// accountKey identifies the account behind a credential, or "" when unknown.
// ProfileArn is deliberately excluded: an IdC profile is shared by every user
// of the subscription, so keying on it would merge different people.
func accountKey(c Credential) string {
	source := firstNonEmpty(c.UserID, c.Email)
	if source == "" && c.AuthMethod == AuthMethodAPIKey {
		source = c.KiroAPIKey
	}
	if source == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(c.AuthMethod + "\x00" + source))
	return hex.EncodeToString(sum[:])[:accountKeyLength]
}

// authFileName is kiro4cpa-<provider>-<account>.json, matching the
// kiro4cpa-google-<email>.json files already in use. The account part is the
// email when known, else a hash of the stable identity, else the login state.
func authFileName(c Credential, state string, now time.Time) string {
	provider := strings.ToLower(firstNonEmpty(providerFor(c), c.AuthMethod))
	if c.AuthMethod == AuthMethodAPIKey {
		provider = "apikey"
	}
	suffix := safeFileToken(c.Email)
	if suffix == "" {
		suffix = accountKey(c)
	}
	if suffix == "" {
		suffix = fmt.Sprintf("%d", now.Unix())
		if len(state) >= 8 {
			suffix = state[:8]
		}
	}
	return fmt.Sprintf("kiro4cpa-%s-%s.json", safeFileToken(provider), suffix)
}

// safeFileToken keeps characters CPA accepts in an auth file name.
func safeFileToken(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', strings.ContainsRune("@._+-", r):
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), ".")
}

// Identity providers, spelled as ZyphrZero/kiro.rs stores them in "provider".
const (
	ProviderBuilderID  = "BuilderId"
	ProviderEnterprise = "Enterprise"
	ProviderGoogle     = "Google"
	ProviderGithub     = "Github"
	ProviderAzureAD    = "AzureAD"
)

// providerFor fills in the identity provider when a credential lacks one,
// following kiro.rs: the default start URL is Builder ID, any other is Enterprise.
func providerFor(c Credential) string {
	if c.Provider != "" {
		return c.Provider
	}
	switch c.AuthMethod {
	case AuthMethodIDC:
		if c.StartURL == "" || c.StartURL == builderStartURL {
			return ProviderBuilderID
		}
		return ProviderEnterprise
	case AuthMethodExternalIDP:
		return ProviderAzureAD
	default:
		// kiro.rs assumes Google for a social credential without a provider;
		// an imported file may equally be GitHub, so the label stays "Social".
		return ""
	}
}

// methodLabel names the sign-in method the way the kiro.rs credential card does.
func methodLabel(c Credential) string {
	provider := strings.ToLower(providerFor(c))
	switch c.AuthMethod {
	case AuthMethodAPIKey:
		return "API Key"
	case AuthMethodSocial:
		switch provider {
		case "github":
			return "GitHub"
		case "google":
			return "Google"
		}
		return "Social"
	case AuthMethodIDC:
		switch provider {
		case "enterprise":
			return "Enterprise"
		case "builderid":
			return "Builder ID"
		}
		return "IdC"
	case AuthMethodExternalIDP:
		return "Entra ID"
	}
	return c.AuthMethod
}

// authLabel shows the account email like kiro.rs, with the sign-in method
// alongside because CPA has no separate column for it.
func authLabel(c Credential, fallback string) string {
	account := firstNonEmpty(c.Email, fallback)
	if account == "" {
		return "Kiro " + methodLabel(c)
	}
	return account + " (" + methodLabel(c) + ")"
}
