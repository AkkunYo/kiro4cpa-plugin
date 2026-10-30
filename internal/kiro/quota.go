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
	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type quotaFetchRPC struct {
	pluginapi.QuotaFetchRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type usageLimits struct {
	SubscriptionInfo *struct {
		SubscriptionTitle string `json:"subscriptionTitle"`
	} `json:"subscriptionInfo"`
	UsageBreakdownList []usageItem `json:"usageBreakdownList"`
}

type usageItem struct {
	ResourceType  string        `json:"resourceType"`
	DisplayName   string        `json:"displayName"`
	Used          float64       `json:"currentUsageWithPrecision"`
	Limit         float64       `json:"usageLimitWithPrecision"`
	NextDateReset float64       `json:"nextDateReset"`
	FreeTrialInfo *usageAllowed `json:"freeTrialInfo"`
	Bonuses       []usageBonus  `json:"bonuses"`
}

type usageAllowed struct {
	Used   float64 `json:"currentUsageWithPrecision"`
	Limit  float64 `json:"usageLimitWithPrecision"`
	Expiry float64 `json:"freeTrialExpiry"`
	Status string  `json:"freeTrialStatus"`
}

type usageBonus struct {
	DisplayName string  `json:"displayName"`
	Used        float64 `json:"currentUsage"`
	Limit       float64 `json:"usageLimit"`
	ExpiresAt   float64 `json:"expiresAt"`
	Status      string  `json:"status"`
}

// Kiro keeps a used-up or expired free trial and bonus in the response with
// status EXPIRED. Like kiro.rs, only ACTIVE ones count; a missing status does not.
const quotaActive = "ACTIVE"

func handleQuotaDescribe() ([]byte, error) {
	return wire.OK(pluginapi.QuotaDescribeResponse{SupportedProviders: []string{models.ProviderName}, DisplayName: "Kiro"}), nil
}

func (r quotaFetchRPC) refreshTarget() refreshTarget {
	return refreshTarget{authID: r.AuthID, path: r.Attributes["path"], callbackID: r.HostCallbackID, storageJSON: r.StorageJSON}
}

// handleQuotaFetch refreshes a rejected token like the executor does, so the
// panel shows quota between scheduled refreshes too.
func handleQuotaFetch(request []byte) ([]byte, error) {
	var req quotaFetchRPC
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cred, _, err := decodeCredential(req.StorageJSON)
	if err != nil {
		return wire.ErrorStatus("invalid_credential", err.Error(), http.StatusUnauthorized, false), nil
	}
	body, err := withRefresh(req.refreshTarget(), cred, func(c Credential) ([]byte, error) {
		return fetchUsageLimits(c, config.Get(), req.HostCallbackID)
	})
	if err != nil {
		return upstreamErrorEnvelope(err), nil
	}
	var limits usageLimits
	if err := json.Unmarshal(body, &limits); err != nil {
		return wire.ErrorStatus("upstream_error", "decode kiro usage limits: "+err.Error(), http.StatusBadGateway, true), nil
	}
	return wire.OK(quotaResponse(limits)), nil
}

// fetchUsageLimits tries each region, with and without the profile ARN, and
// returns the first successful getUsageLimits body.
func fetchUsageLimits(cred Credential, cfg config.PluginConfig, callbackID string) ([]byte, error) {
	headers := runtimeHeaders(cred, cfg)
	regions := restRegionCandidates(cred)

	type attempt struct {
		region     string
		profileArn string
	}
	var attempts []attempt
	for _, reg := range regions {
		if arn := effectiveProfileARN(cred); arn != "" {
			attempts = append(attempts, attempt{region: reg, profileArn: arn})
		}
		attempts = append(attempts, attempt{region: reg, profileArn: ""})
	}

	var lastResp *hostapi.HTTPResponse
	var lastErr error
	for idx, att := range attempts {
		resp, err := hostHTTPDo(hostapi.HTTPRequest{
			HostCallbackID: callbackID,
			Method:         http.MethodGet,
			URL:            usageLimitsURLWith(cred, att.region, att.profileArn),
			Headers:        headers,
		})
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK {
			return resp.Body, nil
		}

		lastResp = resp
		bodyStr := string(resp.Body)
		shouldRetry := resp.StatusCode == http.StatusForbidden ||
			(resp.StatusCode == http.StatusBadRequest &&
				(strings.Contains(bodyStr, "Invalid profileArn") || strings.Contains(bodyStr, "Improperly formed request")))

		if shouldRetry && idx+1 < len(attempts) {
			continue
		}
		break
	}

	if lastResp != nil {
		return nil, classifyUpstream(lastResp.StatusCode, lastResp.Body)
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, errors.New("no available quota endpoint")
}

func usageLimitsURL(c Credential) string {
	return usageLimitsURLWith(c, c.restRegion(), effectiveProfileARN(c))
}

func usageLimitsURLWith(c Credential, region, profileArn string) string {
	q := url.Values{"origin": {"AI_EDITOR"}, "resourceType": {"AGENTIC_REQUEST"}}
	if profileArn != "" {
		q.Set("profileArn", profileArn)
	}
	if region == "" {
		region = c.restRegion()
	}
	return fmt.Sprintf("https://q.%s.amazonaws.com/getUsageLimits?%s", region, q.Encode())
}

func quotaResponse(l usageLimits) pluginapi.QuotaFetchResponse {
	var out pluginapi.QuotaFetchResponse
	if l.SubscriptionInfo != nil && l.SubscriptionInfo.SubscriptionTitle != "" {
		out.Subscription = &pluginapi.QuotaSubscription{Plan: l.SubscriptionInfo.SubscriptionTitle}
	}
	for _, it := range l.UsageBreakdownList {
		name := firstNonEmpty(it.DisplayName, it.ResourceType, "Credit")
		key := strings.ToLower(firstNonEmpty(it.ResourceType, "credit"))
		used, limit := it.Used, it.Limit
		buckets := []pluginapi.QuotaBucket{quotaBucket("monthly", it.Used, it.Limit, it.NextDateReset)}
		if t := it.FreeTrialInfo; t != nil && t.Status == quotaActive && t.Limit > 0 {
			used, limit = used+t.Used, limit+t.Limit
			buckets = append(buckets, quotaBucket("free_trial", t.Used, t.Limit, t.Expiry))
		}
		for _, b := range it.Bonuses {
			if b.Status == quotaActive && b.Limit > 0 {
				used, limit = used+b.Used, limit+b.Limit
				buckets = append(buckets, quotaBucket(firstNonEmpty(b.DisplayName, "bonus"), b.Used, b.Limit, b.ExpiresAt))
			}
		}
		// The summary is the total usable credit, as kiro.rs reports it.
		out.Summary = append(out.Summary,
			pluginapi.QuotaMetric{Key: key + "_used", Label: name + " 已用", Value: used},
			pluginapi.QuotaMetric{Key: key + "_limit", Label: name + " 上限", Value: limit},
		)
		out.Groups = append(out.Groups, pluginapi.QuotaGroup{DisplayName: name, Buckets: buckets})
	}
	return out
}

func quotaBucket(window string, used, limit, resetUnix float64) pluginapi.QuotaBucket {
	b := pluginapi.QuotaBucket{Window: window, Description: fmt.Sprintf("%s / %s", trimFloat(used), trimFloat(limit))}
	if limit > 0 {
		b.RemainingFraction = max(0, min(1, 1-used/limit))
	}
	if resetUnix > 0 {
		b.ResetTime = time.Unix(int64(resetUnix), 0).UTC().Format(time.RFC3339)
	}
	return b
}

func trimFloat(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}
