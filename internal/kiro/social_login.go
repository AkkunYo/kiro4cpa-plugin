package kiro

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
)

// Social sign-in follows the Kiro IDE flow as ported by ZyphrZero/kiro.rs:
// the Kiro portal asks for Google or GitHub, then redirects to a loopback
// address the IDE has registered. CPA runs remotely, so that redirect lands on
// a dead page and the user pastes its URL back into the connect page.
const (
	kiroPortalURL       = "https://app.kiro.dev"
	kiroAuthEndpoint    = "https://prod.us-east-1.auth.desktop.kiro.dev"
	socialRedirectURI   = "http://127.0.0.1:3128"
	maxSocialTokenBytes = 1 << 20
)

// socialTokenURL is swapped in tests.
var socialTokenURL = kiroAuthEndpoint + "/oauth/token"

var socialHTTPClient = &http.Client{Timeout: 30 * time.Second}

func randomURLToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func socialPortalURL(state, challenge string) string {
	q := url.Values{
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {socialRedirectURI},
		"redirect_from":         {"KiroIDE"},
	}
	return kiroPortalURL + "/signin?" + q.Encode()
}

type socialCallback struct {
	Code        string
	State       string
	LoginOption string
	Path        string
}

// parseSocialCallback reads the pasted loopback redirect URL.
func parseSocialCallback(raw string) (socialCallback, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.RawQuery == "" {
		return socialCallback{}, errors.New("请粘贴浏览器地址栏中完整的回调地址")
	}
	if u.Path != "/oauth/callback" && u.Path != "/signin/callback" {
		return socialCallback{}, errors.New("回调地址路径不正确，应为 /oauth/callback")
	}
	q := u.Query()
	if e := q.Get("error"); e != "" {
		return socialCallback{}, fmt.Errorf("Kiro 登录失败: %s", firstNonEmpty(q.Get("error_description"), e))
	}
	cb := socialCallback{Code: q.Get("code"), State: q.Get("state"), LoginOption: q.Get("login_option"), Path: u.Path}
	if cb.Code == "" {
		return socialCallback{}, errors.New("回调地址缺少 code")
	}
	return cb, nil
}

// fullRedirectURI is the redirect_uri the portal actually used, which the
// token endpoint requires byte-for-byte.
func (cb socialCallback) fullRedirectURI() string {
	uri := socialRedirectURI + cb.Path
	if cb.LoginOption != "" {
		uri += "?login_option=" + url.QueryEscape(cb.LoginOption)
	}
	return uri
}

func (cb socialCallback) provider() string {
	if strings.EqualFold(cb.LoginOption, "github") {
		return ProviderGithub
	}
	return ProviderGoogle
}

type socialTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn"`
	ExpiresIn    int64  `json:"expiresIn"`
}

func exchangeSocialCode(ctx context.Context, cb socialCallback, verifier string, cfg config.PluginConfig) (socialTokenResponse, error) {
	body, _ := json.Marshal(map[string]string{
		"code":          cb.Code,
		"code_verifier": verifier,
		"redirect_uri":  cb.fullRedirectURI(),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, socialTokenURL, bytes.NewReader(body))
	if err != nil {
		return socialTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "KiroIDE-"+cfg.ClientVersion)
	resp, err := socialHTTPClient.Do(req)
	if err != nil {
		return socialTokenResponse{}, fmt.Errorf("Kiro token 交换失败: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxSocialTokenBytes))
	if resp.StatusCode != http.StatusOK {
		return socialTokenResponse{}, fmt.Errorf("Kiro token 交换失败: HTTP %d %s", resp.StatusCode, truncate(string(raw), 200))
	}
	var tok socialTokenResponse
	if err := json.Unmarshal(raw, &tok); err != nil {
		return socialTokenResponse{}, fmt.Errorf("解析 Kiro token 响应失败: %w", err)
	}
	if tok.AccessToken == "" || tok.RefreshToken == "" {
		return socialTokenResponse{}, errors.New("Kiro token 响应缺少 accessToken 或 refreshToken")
	}
	return tok, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
