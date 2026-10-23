package kiro

import (
	"strings"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// withoutGlobalExclusions drops the models disabled in CPA's global
// oauth-excluded-models list. CPA lets a credential's own excluded_models
// replace that list, so a credential with one private exclusion would
// otherwise republish every globally disabled model. Filtering here makes the
// global list always apply, with the credential's list added on top by CPA.
func withoutGlobalExclusions(infos []pluginapi.ModelInfo, host pluginapi.HostConfigSummary) []pluginapi.ModelInfo {
	var patterns []string
	for provider, rules := range host.ExcludedModels {
		if !strings.EqualFold(strings.TrimSpace(provider), models.ProviderName) {
			continue
		}
		for _, rule := range rules {
			if p := strings.ToLower(strings.TrimSpace(rule)); p != "" {
				patterns = append(patterns, p)
			}
		}
	}
	if len(patterns) == 0 {
		return infos
	}
	out := make([]pluginapi.ModelInfo, 0, len(infos))
	for _, info := range infos {
		id := strings.ToLower(strings.TrimSpace(info.ID))
		blocked := false
		for _, p := range patterns {
			if matchWildcard(p, id) {
				blocked = true
				break
			}
		}
		if !blocked {
			out = append(out, info)
		}
	}
	return out
}

// matchWildcard mirrors CPA's sdk/cliproxy matchWildcard so a rule hides the
// same models here as it does in CPA.
func matchWildcard(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	parts := strings.Split(pattern, "*")
	if prefix := parts[0]; prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = value[len(prefix):]
	}
	if suffix := parts[len(parts)-1]; suffix != "" {
		if !strings.HasSuffix(value, suffix) {
			return false
		}
		value = value[:len(value)-len(suffix)]
	}
	for i := 1; i < len(parts)-1; i++ {
		segment := parts[i]
		if segment == "" {
			continue
		}
		idx := strings.Index(value, segment)
		if idx < 0 {
			return false
		}
		value = value[idx+len(segment):]
	}
	return true
}
