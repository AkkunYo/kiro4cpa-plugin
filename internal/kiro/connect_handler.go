package kiro

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

//go:embed connect_page.html
var connectPageHTML []byte

// The connect page is a public resource and holds no data; every action goes
// through management routes, which CPA authenticates with the management key.
const (
	connectResourcePath = "/connect"
	loginRoutePrefix    = "/plugins/kiro4cpa/login/"
	routeDevice         = loginRoutePrefix + "device"
	routeSocialStart    = loginRoutePrefix + "social/start"
	routeSocialComplete = loginRoutePrefix + "social/complete"
	routeImport         = loginRoutePrefix + "import"
	routeStatus         = loginRoutePrefix + "status"
	maxPastedField      = 16 << 10
)

func connectPageURL(state string) string {
	return "/v0/resource/plugins/" + pluginID + connectResourcePath + "#state=" + state
}

type managementRegistration struct {
	Routes    []pluginapi.ManagementRoute `json:"routes,omitempty"`
	Resources []pluginapi.ResourceRoute   `json:"resources,omitempty"`
}

func managementRoutes() managementRegistration {
	return managementRegistration{
		Routes: []pluginapi.ManagementRoute{
			{Method: http.MethodPost, Path: routeDevice},
			{Method: http.MethodPost, Path: routeSocialStart},
			{Method: http.MethodPost, Path: routeSocialComplete},
			{Method: http.MethodPost, Path: routeImport},
			{Method: http.MethodGet, Path: routeStatus},
		},
		Resources: []pluginapi.ResourceRoute{
			{Path: connectResourcePath, Description: "Adds a Kiro account; the OAuth page opens it with a login state."},
		},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	path := strings.TrimRight(req.Path, "/")
	switch {
	case req.Method == http.MethodGet && strings.HasSuffix(path, "/plugins/"+pluginID+connectResourcePath):
		return wire.OK(pluginapi.ManagementResponse{
			StatusCode: http.StatusOK,
			Headers: http.Header{
				"Content-Type":            {"text/html; charset=utf-8"},
				"Cache-Control":           {"no-store"},
				"X-Content-Type-Options":  {"nosniff"},
				"Referrer-Policy":         {"no-referrer"},
				"Content-Security-Policy": {"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"},
			},
			Body: connectPageHTML,
		}), nil
	case req.Method == http.MethodPost && strings.HasSuffix(path, routeDevice):
		return handleAPIDevice(req)
	case req.Method == http.MethodPost && strings.HasSuffix(path, routeSocialStart):
		return handleAPISocialStart(req)
	case req.Method == http.MethodPost && strings.HasSuffix(path, routeSocialComplete):
		return handleAPISocialComplete(req)
	case req.Method == http.MethodPost && strings.HasSuffix(path, routeImport):
		return handleAPIImport(req)
	case req.Method == http.MethodGet && strings.HasSuffix(path, routeStatus):
		return handleAPIStatus(req)
	default:
		return jsonResponse(http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// claimSession reserves a pending session for one method. A session is used
// once: picking a second method after the first started is rejected.
func claimSession(state, method string) (*LoginSession, error) {
	var claimed *LoginSession
	var conflict bool
	ok := updateLoginSession(state, func(s *LoginSession) {
		if s.Completed != nil || (s.Method != "" && s.Method != method) {
			conflict = true
			return
		}
		s.Method = method
		s.Error = ""
		snapshot := *s
		claimed = &snapshot
	})
	switch {
	case !ok:
		return nil, errSessionNotFound
	case conflict:
		return nil, errSessionUsed
	}
	return claimed, nil
}

var (
	errSessionNotFound = errors.New("登录会话不存在或已过期，请回到面板重新发起登录")
	errSessionUsed     = errors.New("该登录会话已使用其他方式，请回到面板重新发起登录")
)

func sessionError(err error) ([]byte, error) {
	status := http.StatusConflict
	if errors.Is(err, errSessionNotFound) {
		status = http.StatusNotFound
	}
	return jsonResponse(status, map[string]string{"error": err.Error()})
}

func decodeBody(req pluginapi.ManagementRequest, out any) error {
	if len(req.Body) > maxPastedField*4 {
		return errors.New("请求内容过大")
	}
	if err := json.Unmarshal(req.Body, out); err != nil {
		return errors.New("请求格式不正确")
	}
	return nil
}

type deviceRequest struct {
	State    string `json:"state"`
	Kind     string `json:"kind"` // "builder" or "idc"
	StartURL string `json:"startUrl"`
	Region   string `json:"region"`
}

func handleAPIDevice(req pluginapi.ManagementRequest) ([]byte, error) {
	var body deviceRequest
	if err := decodeBody(req, &body); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	region := firstNonEmpty(body.Region, defaultSSORegion)
	startURL := builderStartURL
	switch body.Kind {
	case "builder":
		region = defaultSSORegion
	case "idc":
		if err := validateRegion(region); err != nil {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "Region 格式不正确"})
		}
		if !validIdCStartURL(body.StartURL) {
			return jsonResponse(http.StatusBadRequest, map[string]string{"error": "Start URL 应为 https://<目录>.awsapps.com/start"})
		}
		startURL = strings.TrimRight(strings.TrimSpace(body.StartURL), "/")
	default:
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "不支持的登录方式"})
	}
	if _, err := claimSession(body.State, "device-"+body.Kind); err != nil {
		return sessionError(err)
	}

	oidc := newSSOOIDCClientFn()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	regResp, err := oidc.RegisterClient(ctx, region)
	if err != nil {
		failLoginSession(body.State, "AWS 客户端注册失败: "+err.Error())
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "AWS 客户端注册失败: " + err.Error()})
	}
	authResp, err := oidc.StartDeviceAuth(ctx, regResp.ClientID, regResp.ClientSecret, startURL, region)
	if err != nil {
		failLoginSession(body.State, "AWS 设备授权失败: "+err.Error())
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "AWS 设备授权失败: " + err.Error()})
	}
	verifyURL := firstNonEmpty(authResp.VerificationURIComplete, authResp.VerificationURI)
	if !trustedAWSURL(verifyURL) {
		failLoginSession(body.State, "AWS 返回了不可信的授权地址")
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": "AWS 返回了不可信的授权地址"})
	}

	updateLoginSession(body.State, func(s *LoginSession) {
		s.ClientID = regResp.ClientID
		s.ClientSecret = regResp.ClientSecret
		s.DeviceCode = authResp.DeviceCode
		s.UserCode = authResp.UserCode
		s.VerifyURL = verifyURL
		s.Region = region
		s.StartURL = startURL
		s.Polling = true
	})
	go pollDeviceToken(body.State, oidc, deviceGrant{
		clientID: regResp.ClientID, clientSecret: regResp.ClientSecret, deviceCode: authResp.DeviceCode,
		region: region, startURL: startURL, interval: authResp.Interval, expiresIn: authResp.ExpiresIn,
	})

	return jsonResponse(http.StatusOK, map[string]any{
		"userCode":        authResp.UserCode,
		"verificationUri": verifyURL,
		"expiresIn":       authResp.ExpiresIn,
	})
}

func validIdCStartURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	sub, ok := strings.CutSuffix(host, ".awsapps.com")
	return ok && sub != "" && !strings.Contains(sub, ".") && u.Port() == "" &&
		strings.TrimRight(u.Path, "/") == "/start"
}

func trustedAWSURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return strings.HasSuffix(host, ".amazonaws.com") || strings.HasSuffix(host, ".awsapps.com")
}

type deviceGrant struct {
	clientID, clientSecret, deviceCode, region, startURL string
	interval, expiresIn                                  int
}

// pollDeviceInterval is shortened in tests.
var pollDeviceInterval = func(seconds int) time.Duration {
	return time.Duration(max(seconds, 5)) * time.Second
}

func pollDeviceToken(state string, oidc *SSOOIDCClient, g deviceGrant) {
	expiresIn := g.expiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	wait := pollDeviceInterval(g.interval)
	for {
		time.Sleep(wait)
		if _, ok := getLoginSession(state); !ok {
			return
		}
		if time.Now().After(deadline) {
			failLoginSession(state, "AWS 授权已超时，请重新发起登录")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		tok, err := oidc.CreateToken(ctx, g.clientID, g.clientSecret, g.deviceCode, g.region)
		cancel()
		switch {
		case err == nil:
			cred := &Credential{
				AuthMethod:   AuthMethodIDC,
				Provider:     providerFor(Credential{AuthMethod: AuthMethodIDC, StartURL: g.startURL}),
				AccessToken:  tok.AccessToken,
				RefreshToken: tok.RefreshToken,
				ClientID:     g.clientID,
				ClientSecret: g.clientSecret,
				Region:       g.region,
				AuthRegion:   g.region,
				StartURL:     g.startURL,
				ProfileArn:   tok.ProfileArn,
				ExpiresAt:    time.Now().UTC().Add(time.Duration(tok.ExpiresIn) * time.Second).Format(time.RFC3339),
			}
			completeLoginSession(state, cred, "Kiro 设备授权已完成")
			return
		case errors.Is(err, ErrSlowDown):
			wait += 5 * time.Second
		case errors.Is(err, ErrAuthPending):
		default:
			failLoginSession(state, "AWS 授权失败: "+err.Error())
			return
		}
	}
}

type stateRequest struct {
	State string `json:"state"`
}

func handleAPISocialStart(req pluginapi.ManagementRequest) ([]byte, error) {
	var body stateRequest
	if err := decodeBody(req, &body); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if _, err := claimSession(body.State, "social"); err != nil {
		return sessionError(err)
	}
	verifier, err1 := randomURLToken(32)
	socialState, err2 := randomURLToken(24)
	if err1 != nil || err2 != nil {
		return jsonResponse(http.StatusInternalServerError, map[string]string{"error": "生成登录参数失败"})
	}
	updateLoginSession(body.State, func(s *LoginSession) {
		s.CodeVerifier = verifier
		s.SocialState = socialState
	})
	return jsonResponse(http.StatusOK, map[string]string{
		"url":         socialPortalURL(socialState, pkceChallenge(verifier)),
		"redirectUri": socialRedirectURI,
	})
}

type socialCompleteRequest struct {
	State       string `json:"state"`
	CallbackURL string `json:"callbackUrl"`
}

func handleAPISocialComplete(req pluginapi.ManagementRequest) ([]byte, error) {
	var body socialCompleteRequest
	if err := decodeBody(req, &body); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	session, err := claimSession(body.State, "social")
	if err != nil {
		return sessionError(err)
	}
	if session.CodeVerifier == "" {
		return jsonResponse(http.StatusConflict, map[string]string{"error": "请先点击“打开 Kiro 登录页”"})
	}
	cb, err := parseSocialCallback(body.CallbackURL)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if cb.State != session.SocialState {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": "回调地址的 state 与本次登录不匹配，请重新打开 Kiro 登录页"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tok, err := exchangeSocialCode(ctx, cb, session.CodeVerifier, config.Get())
	if err != nil {
		return jsonResponse(http.StatusBadGateway, map[string]string{"error": err.Error()})
	}
	lifetime := defaultTokenLifetime
	if tok.ExpiresIn > 0 {
		lifetime = time.Duration(tok.ExpiresIn) * time.Second
	}
	cred := &Credential{
		AuthMethod:   AuthMethodSocial,
		Provider:     cb.provider(),
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ProfileArn:   tok.ProfileArn,
		Region:       defaultRegion,
		AuthRegion:   defaultRegion,
		ExpiresAt:    time.Now().UTC().Add(lifetime).Format(time.RFC3339),
	}
	completeLoginSession(body.State, cred, "Kiro "+methodLabel(*cred)+" 登录已完成")
	return jsonResponse(http.StatusOK, map[string]bool{"ok": true})
}

type importRequest struct {
	State        string `json:"state"`
	Method       string `json:"method"` // "social", "idc" or "api_key"
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	Region       string `json:"region"`
	KiroAPIKey   string `json:"kiroApiKey"`
}

func handleAPIImport(req pluginapi.ManagementRequest) ([]byte, error) {
	var body importRequest
	if err := decodeBody(req, &body); err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	cred, err := importedCredential(body)
	if err != nil {
		return jsonResponse(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	if _, err := claimSession(body.State, "import-"+cred.AuthMethod); err != nil {
		return sessionError(err)
	}
	completeLoginSession(body.State, &cred, "Kiro 凭据已导入")
	return jsonResponse(http.StatusOK, map[string]bool{"ok": true})
}

func importedCredential(body importRequest) (Credential, error) {
	trim := func(s string) string { return strings.TrimSpace(s) }
	for _, v := range []string{body.RefreshToken, body.ClientID, body.ClientSecret, body.KiroAPIKey} {
		if len(v) > maxPastedField {
			return Credential{}, errors.New("凭据内容过长")
		}
	}
	region := firstNonEmpty(body.Region, defaultRegion)
	var cred Credential
	switch body.Method {
	case "api_key":
		cred = Credential{AuthMethod: AuthMethodAPIKey, KiroAPIKey: trim(body.KiroAPIKey)}
	case "social":
		cred = Credential{AuthMethod: AuthMethodSocial, RefreshToken: trim(body.RefreshToken), Region: region, AuthRegion: region}
	case "idc":
		cred = Credential{AuthMethod: AuthMethodIDC, RefreshToken: trim(body.RefreshToken),
			ClientID: trim(body.ClientID), ClientSecret: trim(body.ClientSecret), Region: region, AuthRegion: region}
	default:
		return Credential{}, errors.New("不支持的导入方式")
	}
	cred = cred.normalized()
	if err := cred.validate(); err != nil {
		return Credential{}, err
	}
	cred.Provider = providerFor(cred)
	return cred, nil
}

func handleAPIStatus(req pluginapi.ManagementRequest) ([]byte, error) {
	session, ok := getLoginSession(req.Query.Get("state"))
	if !ok {
		return jsonResponse(http.StatusNotFound, map[string]string{"status": "error", "error": errSessionNotFound.Error()})
	}
	switch {
	case session.Completed != nil:
		return jsonResponse(http.StatusOK, map[string]string{"status": "success", "message": session.Message})
	case session.Error != "":
		return jsonResponse(http.StatusOK, map[string]string{"status": "error", "error": session.Error})
	}
	return jsonResponse(http.StatusOK, map[string]string{"status": "pending", "method": session.Method})
}

func jsonResponse(statusCode int, data any) ([]byte, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	return wire.OK(pluginapi.ManagementResponse{
		StatusCode: statusCode,
		Headers: http.Header{
			"Content-Type":           {"application/json; charset=utf-8"},
			"Cache-Control":          {"no-store"},
			"X-Content-Type-Options": {"nosniff"},
		},
		Body: b,
	}), nil
}
