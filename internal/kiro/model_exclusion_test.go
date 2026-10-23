package kiro

import (
	"sort"
	"testing"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func modelsForAuth(t *testing.T, host pluginapi.HostConfigSummary) []string {
	t.Helper()
	catalog := []models.UpstreamModel{{ModelID: "auto"}, {ModelID: "claude-sonnet-4.5"}, {ModelID: "claude-opus-4.8"}, {ModelID: "glm-5"}, {ModelID: "deepseek-3.2"}}
	storage, _ := mergeCredential(withCatalog(map[string]any{}, catalog, time.Now()), Credential{AuthMethod: AuthMethodSocial, RefreshToken: "rt"})
	resp := callHandler[pluginapi.ModelResponse](t, pluginabi.MethodModelForAuth,
		authModelRPC{AuthModelRequest: pluginapi.AuthModelRequest{StorageJSON: storage, Host: host}})
	ids := make([]string, 0, len(resp.Models))
	for _, m := range resp.Models {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}

func TestModelsForAuthAppliesGlobalExclusions(t *testing.T) {
	got := modelsForAuth(t, pluginapi.HostConfigSummary{ExcludedModels: map[string][]string{
		"kiro4cpa": {" Claude-Opus-4.8 ", "auto", "deepseek-*"},
		"claude":   {"glm-5"},
	}})
	want := []string{"claude-sonnet-4.5", "glm-5"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("models %v, want %v", got, want)
	}
}

func TestModelsForAuthWithoutGlobalExclusions(t *testing.T) {
	if got := modelsForAuth(t, pluginapi.HostConfigSummary{}); len(got) != 5 {
		t.Fatalf("models %v", got)
	}
}

func TestMatchWildcardMatchesCPA(t *testing.T) {
	cases := []struct {
		pattern, value string
		want           bool
	}{
		{"glm-5", "glm-5", true},
		{"glm-5", "glm-5.1", false},
		{"claude-*", "claude-opus-4.8", true},
		{"*-4.8", "claude-opus-4.8", true},
		{"claude-*-4.8", "claude-opus-4.8", true},
		{"claude-*-4.8", "claude-opus-4.6", false},
		{"*", "anything", true},
		{"", "auto", false},
	}
	for _, tc := range cases {
		if got := matchWildcard(tc.pattern, tc.value); got != tc.want {
			t.Fatalf("matchWildcard(%q, %q) = %v", tc.pattern, tc.value, got)
		}
	}
}
