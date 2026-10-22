package kiro

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The PKCE challenge for the RFC 7636 appendix B verifier.
func TestPKCEChallengeMatchesRFC7636(t *testing.T) {
	if got := pkceChallenge("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"); got != "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM" {
		t.Fatalf("challenge %s", got)
	}
}

func TestSocialPortalURL(t *testing.T) {
	u, err := url.Parse(socialPortalURL("st", "ch"))
	if err != nil || u.Host != "app.kiro.dev" || u.Path != "/signin" {
		t.Fatalf("url %v", u)
	}
	q := u.Query()
	if q.Get("state") != "st" || q.Get("code_challenge") != "ch" || q.Get("code_challenge_method") != "S256" ||
		q.Get("redirect_uri") != "http://127.0.0.1:3128" || q.Get("redirect_from") != "KiroIDE" {
		t.Fatalf("query %v", q)
	}
}

func TestParseSocialCallback(t *testing.T) {
	cb, err := parseSocialCallback(" http://127.0.0.1:3128/oauth/callback?code=c1&state=s1&login_option=github ")
	if err != nil || cb.Code != "c1" || cb.State != "s1" || cb.provider() != ProviderGithub {
		t.Fatalf("%+v %v", cb, err)
	}
	if got := cb.fullRedirectURI(); got != "http://127.0.0.1:3128/oauth/callback?login_option=github" {
		t.Fatalf("redirect %s", got)
	}
	plain, _ := parseSocialCallback("http://localhost:3128/signin/callback?code=c&state=s")
	if plain.fullRedirectURI() != "http://127.0.0.1:3128/signin/callback" || plain.provider() != ProviderGoogle {
		t.Fatalf("plain %+v", plain)
	}
	for _, bad := range []string{
		"",
		"not a url",
		"http://127.0.0.1:3128/oauth/callback",
		"http://127.0.0.1:3128/other?code=c&state=s",
		"http://127.0.0.1:3128/oauth/callback?state=s",
		"http://127.0.0.1:3128/oauth/callback?error=access_denied&error_description=denied",
	} {
		if _, err := parseSocialCallback(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSocialLoginExchangesCodeAndCompletes(t *testing.T) {
	var got map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("User-Agent"), "KiroIDE-") {
			t.Errorf("request %s %s", r.Method, r.Header.Get("User-Agent"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"accessToken":"at","refreshToken":"rt","profileArn":"arn:aws:codewhisperer:us-east-1:1:profile/X","expiresIn":3600}`))
	}))
	defer server.Close()
	orig := socialTokenURL
	socialTokenURL = server.URL
	defer func() { socialTokenURL = orig }()

	state := newSession(t)
	status, start := callRoute(t, http.MethodPost, routeSocialStart, map[string]string{"state": state}, nil)
	if status != http.StatusOK {
		t.Fatalf("start %d %v", status, start)
	}
	portal, _ := url.Parse(start["url"].(string))
	socialState := portal.Query().Get("state")
	session, _ := getLoginSession(state)

	wrong := "http://127.0.0.1:3128/oauth/callback?code=c&state=other&login_option=google"
	if status, _ := callRoute(t, http.MethodPost, routeSocialComplete, map[string]string{"state": state, "callbackUrl": wrong}, nil); status != http.StatusBadRequest {
		t.Fatalf("state mismatch accepted: %d", status)
	}

	callback := "http://127.0.0.1:3128/oauth/callback?code=the-code&state=" + url.QueryEscape(socialState) + "&login_option=google"
	if status, body := callRoute(t, http.MethodPost, routeSocialComplete, map[string]string{"state": state, "callbackUrl": callback}, nil); status != http.StatusOK {
		t.Fatalf("complete %d %v", status, body)
	}
	if got["code"] != "the-code" || got["code_verifier"] != session.CodeVerifier ||
		got["redirect_uri"] != "http://127.0.0.1:3128/oauth/callback?login_option=google" ||
		portal.Query().Get("code_challenge") != pkceChallenge(session.CodeVerifier) {
		t.Fatalf("exchange body %v", got)
	}
	done, _ := getLoginSession(state)
	c := done.Completed
	if c == nil || c.AuthMethod != AuthMethodSocial || c.Provider != ProviderGoogle || c.RefreshToken != "rt" || c.ProfileArn == "" || done.CodeVerifier != "" {
		t.Fatalf("credential %+v verifier %q", c, done.CodeVerifier)
	}
}

func TestSocialCompleteRequiresStart(t *testing.T) {
	state := newSession(t)
	cb := "http://127.0.0.1:3128/oauth/callback?code=c&state=s"
	if status, _ := callRoute(t, http.MethodPost, routeSocialComplete, map[string]string{"state": state, "callbackUrl": cb}, nil); status != http.StatusConflict {
		t.Fatalf("status %d", status)
	}
}

func TestSocialExchangeFailureIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer server.Close()
	orig := socialTokenURL
	socialTokenURL = server.URL
	defer func() { socialTokenURL = orig }()

	state := newSession(t)
	_, start := callRoute(t, http.MethodPost, routeSocialStart, map[string]string{"state": state}, nil)
	portal, _ := url.Parse(start["url"].(string))
	cb := "http://127.0.0.1:3128/oauth/callback?code=c&state=" + url.QueryEscape(portal.Query().Get("state"))
	status, body := callRoute(t, http.MethodPost, routeSocialComplete, map[string]string{"state": state, "callbackUrl": cb}, nil)
	if status != http.StatusBadGateway || !strings.Contains(body["error"].(string), "invalid_grant") {
		t.Fatalf("%d %v", status, body)
	}
	if s, _ := getLoginSession(state); s.Completed != nil {
		t.Fatal("failed exchange completed the session")
	}
}
