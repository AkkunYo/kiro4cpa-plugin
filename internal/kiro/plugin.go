package kiro

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/AkkunYo/kiro4cpa-plugin/internal/config"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/hostapi"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/models"
	"github.com/AkkunYo/kiro4cpa-plugin/internal/wire"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const (
	catalogKey          = "kiro4cpa_models"
	refreshLead         = 10 * time.Minute
	minRefreshDelay     = time.Minute
	longRefreshInterval = 24 * time.Hour
	maxModelPages       = 10
)

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

type registrationCapability struct {
	ModelRegistrar        bool                         `json:"model_registrar"`
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

func getRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             "kiro4cpa",
			Version:          "1.0.0",
			Author:           "AkkunYo",
			GitHubRepository: "https://github.com/AkkunYo/kiro4cpa-plugin",
			ConfigFields: []pluginapi.ConfigField{
				{Name: "enabled", Type: pluginapi.ConfigFieldTypeBoolean, Description: "Enable Kiro plugin"},
				{Name: "default_endpoint", Type: pluginapi.ConfigFieldTypeString, Description: "Default endpoint (ide or cli)"},
				{Name: "default_region", Type: pluginapi.ConfigFieldTypeString, Description: "Default AWS Region"},
				{Name: "client_version", Type: pluginapi.ConfigFieldTypeString, Description: "Kiro IDE version reported in User-Agent"},
				{Name: "os_version", Type: pluginapi.ConfigFieldTypeString, Description: "OS reported in User-Agent, e.g. darwin#24.6.0"},
				{Name: "node_version", Type: pluginapi.ConfigFieldTypeString, Description: "Node.js version reported in User-Agent"},
			},
		},
		Capabilities: registrationCapability{
			ModelRegistrar:     true,
			ModelProvider:      true,
			AuthProvider:       true,
			Executor:           true,
			ExecutorModelScope: pluginapi.ExecutorModelScopeBoth,
			// CPA translates OpenAI Chat / Responses to and from Claude itself.
			ExecutorInputFormats:  []string{"claude"},
			ExecutorOutputFormats: []string{"claude"},
		},
	}
}

// HandleMethod handles incoming C-ABI RPC calls from CLIProxyAPI.
func HandleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return nil, err
			}
			if err := config.UpdateFromYAML(req.ConfigYAML); err != nil {
				return nil, err
			}
		}
		return wire.OK(getRegistration()), nil

	case pluginabi.MethodPluginQuiesce, pluginabi.MethodPluginShutdown:
		return wire.OK(map[string]bool{"acknowledged": true}), nil

	case pluginabi.MethodModelRegister:
		return wire.OK(pluginapi.ModelRegistrationResponse{
			Provider: models.ProviderName,
			Models:   models.GetDefaultModelInfos(),
		}), nil

	case pluginabi.MethodModelStatic:
		return wire.OK(pluginapi.ModelResponse{
			Provider: models.ProviderName,
			Models:   models.GetDefaultModelInfos(),
		}), nil

	case pluginabi.MethodModelForAuth:
		return handleModelForAuth(request)

	case pluginabi.MethodAuthIdentifier:
		return wire.OK(identifierResponse{Identifier: models.ProviderName}), nil

	case pluginabi.MethodAuthParse:
		return handleAuthParse(request)

	case pluginabi.MethodAuthRefresh:
		return handleAuthRefresh(request)

	case pluginabi.MethodExecutorIdentifier:
		return wire.OK(identifierResponse{Identifier: models.ProviderName}), nil

	case pluginabi.MethodExecutorExecute:
		return handleExecute(request)

	case pluginabi.MethodExecutorExecuteStream:
		return handleExecuteStream(request)

	case pluginabi.MethodExecutorCountTokens:
		return handleCountTokens(request)

	default:
		return wire.Error("unsupported_method", "method not supported yet: "+method), nil
	}
}

type authRefreshRPC struct {
	pluginapi.AuthRefreshRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type authModelRPC struct {
	pluginapi.AuthModelRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// handleAuthParse runs synchronously while CPA loads auth files, so it never
// touches the network. A credential without a stored catalogue is scheduled
// for an immediate refresh, which fetches the catalogue off the startup path.
func handleAuthParse(request []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	if req.Provider != models.ProviderName {
		return wire.OK(pluginapi.AuthParseResponse{Handled: false}), nil
	}
	cred, fields, err := decodeCredential(req.RawJSON)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", req.FileName, err)
	}
	storage, err := mergeCredential(fields, cred)
	if err != nil {
		return nil, err
	}
	next := time.Now()
	if _, ok := catalogFrom(fields); ok {
		next = scheduledRefresh(cred, next)
	}
	return wire.OK(pluginapi.AuthParseResponse{
		Handled: true,
		Auth: pluginapi.AuthData{
			Provider:         models.ProviderName,
			FileName:         req.FileName,
			Label:            firstNonEmpty(cred.Email, req.FileName),
			StorageJSON:      storage,
			Metadata:         displayMetadata(cred),
			NextRefreshAfter: next,
		},
	}), nil
}

func handleAuthRefresh(request []byte) ([]byte, error) {
	var req authRefreshRPC
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	cred, fields, err := decodeCredential(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	cfg := config.Get()
	now := time.Now()
	metadata := cloneMap(req.Metadata)

	refreshed, err := refreshCredential(cred, cfg, req.HostCallbackID, now)
	if errors.Is(err, ErrInvalidGrant) {
		// A revoked refresh token never recovers; disable instead of retrying forever.
		metadata["disabled"] = true
		next := now.Add(longRefreshInterval)
		return wire.OK(pluginapi.AuthRefreshResponse{
			Auth: pluginapi.AuthData{
				Provider: models.ProviderName, ID: req.AuthID, Disabled: true,
				Metadata: metadata, NextRefreshAfter: next,
			},
			NextRefreshAfter: next,
		}), nil
	}
	if err != nil {
		return nil, err
	}

	if catalog, errList := fetchModelCatalog(refreshed, cfg, req.HostCallbackID); errList == nil {
		fields = withCatalog(fields, catalog, now)
	} else {
		log.Printf("kiro4cpa: listing models for %s failed, keeping stored catalogue: %v", req.AuthID, errList)
	}
	storage, err := mergeCredential(fields, refreshed)
	if err != nil {
		return nil, err
	}
	next := scheduledRefresh(refreshed, now)
	if floor := now.Add(minRefreshDelay); next.Before(floor) {
		next = floor
	}
	disabled, _ := metadata["disabled"].(bool)
	return wire.OK(pluginapi.AuthRefreshResponse{
		Auth: pluginapi.AuthData{
			Provider: models.ProviderName, ID: req.AuthID, Disabled: disabled,
			StorageJSON: storage, Metadata: metadata, NextRefreshAfter: next,
		},
		NextRefreshAfter: next,
	}), nil
}

// handleModelForAuth reads only the stored catalogue so CPA registration never waits on the network.
func handleModelForAuth(request []byte) ([]byte, error) {
	var req authModelRPC
	if err := json.Unmarshal(request, &req); err != nil {
		return nil, err
	}
	infos := models.GetDefaultModelInfos()
	if fields, err := decodeFields(req.StorageJSON); err == nil {
		if catalog, ok := catalogFrom(fields); ok {
			infos = models.ToModelInfos(catalog)
		}
	}
	return wire.OK(pluginapi.ModelResponse{Provider: models.ProviderName, Models: infos}), nil
}

// scheduledRefresh is expiresAt - refreshLead; an unparsable expiry refreshes now.
func scheduledRefresh(c Credential, now time.Time) time.Time {
	if c.AuthMethod == AuthMethodAPIKey {
		return now.Add(longRefreshInterval)
	}
	expiresAt, err := time.Parse(time.RFC3339, c.ExpiresAt)
	if err != nil {
		return now
	}
	return expiresAt.Add(-refreshLead)
}

func displayMetadata(c Credential) map[string]any {
	if c.Email == "" {
		return nil
	}
	return map[string]any{"email": c.Email}
}

func fetchModelCatalog(c Credential, cfg config.PluginConfig, callbackID string) ([]models.UpstreamModel, error) {
	var all []models.UpstreamModel
	nextToken := ""
	for page := 0; page < maxModelPages; page++ {
		resp, err := hostHTTPDo(hostapi.HTTPRequest{
			HostCallbackID: callbackID,
			Method:         http.MethodGet,
			URL:            listModelsURL(c, nextToken),
			Headers:        runtimeHeaders(c, cfg),
		})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, classifyUpstream(resp.StatusCode, resp.Body)
		}
		var body struct {
			Models    []models.UpstreamModel `json:"models"`
			NextToken string                 `json:"nextToken"`
		}
		if err := json.Unmarshal(resp.Body, &body); err != nil {
			return nil, fmt.Errorf("decode kiro models: %w", err)
		}
		all = append(all, body.Models...)
		if body.NextToken == "" {
			if len(all) == 0 {
				return nil, errors.New("kiro returned an empty model list")
			}
			return all, nil
		}
		nextToken = body.NextToken
	}
	return nil, fmt.Errorf("kiro model list exceeded %d pages", maxModelPages)
}

type storedCatalog struct {
	FetchedAt string                 `json:"fetchedAt"`
	Models    []models.UpstreamModel `json:"models"`
}

func withCatalog(fields map[string]any, catalog []models.UpstreamModel, now time.Time) map[string]any {
	out := cloneMap(fields)
	out[catalogKey] = storedCatalog{FetchedAt: now.UTC().Format(time.RFC3339), Models: catalog}
	return out
}

func catalogFrom(fields map[string]any) ([]models.UpstreamModel, bool) {
	raw, ok := fields[catalogKey]
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var stored storedCatalog
	if json.Unmarshal(b, &stored) != nil || len(stored.Models) == 0 {
		return nil, false
	}
	return stored.Models, true
}
