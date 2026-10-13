package plugin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"qwen-cliproxyapi/internal/config"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type authProvider struct{ cfg config.Config }

var _ pluginapi.AuthProvider = authProvider{}

func (authProvider) Identifier() string { return ProviderID }

func (p authProvider) ParseAuth(_ context.Context, req pluginapi.AuthParseRequest) (pluginapi.AuthParseResponse, error) {
	debugTrace("auth parse request provider_match=%t file_present=%t raw_bytes=%d", req.Provider == ProviderID, req.FileName != "", len(req.RawJSON))
	var raw struct {
		Type     string `json:"type"`
		Provider string `json:"provider"`
		ID       string `json:"id"`
		Label    string `json:"label"`
		APIKey   string `json:"api_key"`
		BaseURL  string `json:"base_url"`
	}
	if err := json.Unmarshal(req.RawJSON, &raw); err != nil {
		if req.Provider == ProviderID {
			return pluginapi.AuthParseResponse{}, fmt.Errorf("qwen auth record has invalid JSON")
		}
		return pluginapi.AuthParseResponse{}, nil
	}
	if (req.Provider != "" && req.Provider != ProviderID) || (raw.Type != ProviderID && raw.Provider != ProviderID) {
		return pluginapi.AuthParseResponse{Handled: false}, nil
	}
	if strings.TrimSpace(raw.APIKey) == "" {
		return pluginapi.AuthParseResponse{}, fmt.Errorf("qwen auth record has no api key")
	}
	if raw.ID == "" {
		raw.ID = req.FileName
	}
	debugTrace("auth parse handled file_present=%t id_present=%t api_key_present=%t", req.FileName != "", raw.ID != "", strings.TrimSpace(raw.APIKey) != "")
	attributes := map[string]string{"api_key": raw.APIKey}
	if raw.BaseURL != "" {
		if err := config.ValidateCredentialBaseURL(raw.BaseURL); err != nil {
			return pluginapi.AuthParseResponse{}, fmt.Errorf("qwen auth record has invalid base_url")
		}
		attributes["base_url"] = raw.BaseURL
		// host.auth.save initially registers the filename as its runtime ID.
		// Match it on watcher parsing to update, not duplicate, panel records.
		// Legacy configured credentials retain their existing bare-hash IDs.
		if req.FileName != "" {
			raw.ID = req.FileName
		}
	}
	seed, err := credentialIndexSeed(raw.APIKey, raw.BaseURL, p.cfg)
	if err != nil {
		return pluginapi.AuthParseResponse{}, err
	}
	attributes["auth_index_seed"] = seed
	label := accountLabel(p.cfg, raw.APIKey, raw.Label, 0)
	if raw.BaseURL != "" && strings.TrimSpace(raw.Label) != "" {
		label = raw.Label
	}
	return pluginapi.AuthParseResponse{Handled: true, Auth: pluginapi.AuthData{
		Provider: ProviderID, ID: raw.ID, FileName: req.FileName, Label: label, StorageJSON: req.RawJSON,
		Attributes: attributes,
	}}, nil
}

func (authProvider) StartLogin(context.Context, pluginapi.AuthLoginStartRequest) (pluginapi.AuthLoginStartResponse, error) {
	// The host requires a state even for a manual form; this is not OAuth.
	return pluginapi.AuthLoginStartResponse{Provider: ProviderID, State: rand.Text(), ExpiresAt: time.Now().Add(10 * time.Minute), Metadata: map[string]any{
		"auth_kind":    "manual_api_key",
		"submit_path":  "/v0/management/plugins/" + pluginName + "/credentials",
		"submit_label": "添加凭证",
		"fields": []map[string]any{
			{"name": "base_url", "label": "Base URL", "placeholder": config.DefaultBaseURL, "required": true},
			{"name": "api_key", "label": "API Key", "type": "password", "required": true},
			{"name": "name", "label": "别名 (Alias)", "placeholder": "留空则显示脱敏 API Key", "required": false},
		},
	}}, nil
}

func (authProvider) PollLogin(context.Context, pluginapi.AuthLoginPollRequest) (pluginapi.AuthLoginPollResponse, error) {
	return pluginapi.AuthLoginPollResponse{}, fmt.Errorf("qwen login is unsupported; configure a manual api key")
}

func (p authProvider) RefreshAuth(_ context.Context, req pluginapi.AuthRefreshRequest) (pluginapi.AuthRefreshResponse, error) {
	debugTrace("auth refresh request provider_match=%t id_present=%t storage_json_bytes=%d attr_count=%d metadata_count=%d", req.AuthProvider == ProviderID, req.AuthID != "", len(req.StorageJSON), len(req.Attributes), len(req.Metadata))
	if req.AuthProvider != ProviderID {
		return pluginapi.AuthRefreshResponse{}, fmt.Errorf("qwen auth refresh requires a selected API key credential")
	}
	key, baseURL, err := resolveCredential(req.Attributes, req.StorageJSON, p.cfg)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	seed, err := credentialIndexSeed(key, baseURL, p.cfg)
	if err != nil {
		return pluginapi.AuthRefreshResponse{}, err
	}
	attributes := make(map[string]string, len(req.Attributes)+1)
	for key, value := range req.Attributes {
		attributes[key] = value
	}
	attributes["api_key"] = key
	attributes["base_url"] = baseURL
	attributes["auth_index_seed"] = seed
	return pluginapi.AuthRefreshResponse{Auth: pluginapi.AuthData{Provider: req.AuthProvider, ID: req.AuthID, StorageJSON: req.StorageJSON, Metadata: req.Metadata, Attributes: attributes}}, nil
}

// credentialIndexSeed binds quota ownership to key + effective endpoint, not
// filename, label or disabled state. The host gives auth_index_seed precedence
// over file paths; only the digest crosses this identity boundary.
func credentialIndexSeed(key, baseURL string, cfg config.Config) (string, error) {
	if baseURL == "" {
		baseURL = cfg.BaseURL
	}
	if baseURL == "" {
		baseURL = config.DefaultBaseURL
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if config.ValidateCredentialBaseURL(baseURL) != nil {
		return "", fmt.Errorf("qwen credential has invalid base_url")
	}
	u, _ := url.Parse(baseURL)
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		u.Host = strings.TrimSuffix(u.Host, ":"+u.Port())
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(key) + "\x00" + u.String()))
	return "qwen-credential-v1:" + hex.EncodeToString(digest[:]), nil
}

func credentialAuthIndex(seed string) string {
	// core v8.0.15 stableAuthIndex(indexSeed): sha256's first eight bytes.
	digest := sha256.Sum256([]byte("auth_index_seed:" + seed))
	return hex.EncodeToString(digest[:8])
}

// Labels are presentation only. Stable auth IDs and stored host metadata are preserved.
func accountLabel(cfg config.Config, key, existing string, fallbackIndex int) string {
	index := fallbackIndex
	for i, entry := range cfg.APIKeys {
		if entry.Value == key {
			if name := strings.TrimSpace(entry.Name); name != "" {
				return name
			}
			index = i
			break
		}
	}
	label := strings.TrimSpace(existing)
	if label != "" && !strings.HasPrefix(label, "Qwen credential ") && !strings.HasPrefix(label, "qwen-key-") && !defaultQwenLabelRe.MatchString(label) {
		return label
	}
	if label := maskAPIKey(key); label != "" {
		return label
	}
	return fmt.Sprintf("Qwen %d", index+1)
}

var defaultQwenLabelRe = regexp.MustCompile(`^Qwen \d+$`)

// maskAPIKey renders a key as first4...last4 (shorter keys are masked harder) so a
// credential without an alias is still distinguishable in the panel without exposing it.
func maskAPIKey(key string) string {
	runes := []rune(strings.TrimSpace(key))
	switch {
	case len(runes) >= 12:
		return string(runes[:4]) + "..." + string(runes[len(runes)-4:])
	case len(runes) >= 8:
		return string(runes[:2]) + "..." + string(runes[len(runes)-2:])
	case len(runes) >= 2:
		return string(runes[:1]) + "..." + string(runes[len(runes)-1:])
	case len(runes) == 1:
		return "..."
	default:
		return ""
	}
}
