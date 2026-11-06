package aliyunplugin

import (
	"encoding/json"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	config "cpa-credential-qwen/internal/aliyunconfig"
	"cpa-credential-qwen/internal/bss"
)

const ID = "aliyun-bss-quota"

type Manager struct {
	lifeMu  sync.RWMutex
	mu      sync.RWMutex
	cfg     config.Config
	client  *bss.Client
	host    *HostBridge
	stopped bool
}

func NewManager(host *HostBridge) *Manager { return &Manager{host: host} }

func (m *Manager) HandleCall(method string, request []byte) (response []byte, err error) {
	defer func() {
		if recover() != nil {
			response = ErrEnvelope("plugin_error", "unexpected plugin failure")
			err = nil
		}
	}()
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		m.lifeMu.Lock()
		defer m.lifeMu.Unlock()
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if len(request) > 0 {
			if err := json.Unmarshal(request, &req); err != nil {
				return ErrEnvelope("invalid_request", "invalid lifecycle request"), nil
			}
		}
		cfg, err := config.Load(req.ConfigYAML)
		if err != nil {
			return ErrEnvelope("configuration_error", err.Error()), nil
		}
		var client *bss.Client
		if cfg.QuotaSource == config.SourceBSS {
			client, err = bss.NewClient(bss.Options{AccessKeyID: cfg.AccessKeyID, AccessKeySecret: cfg.AccessKeySecret, Region: cfg.Region, Endpoint: cfg.Endpoint, AllowHTTP: cfg.AllowHTTP, Timeout: cfg.RequestTimeout})
			if err != nil {
				return ErrEnvelope("configuration_error", err.Error()), nil
			}
		}
		m.mu.Lock()
		m.cfg, m.client, m.stopped = cfg, client, false
		m.mu.Unlock()
		return okEnvelope(struct {
			SchemaVersion uint32             `json:"schema_version"`
			Metadata      pluginapi.Metadata `json:"metadata"`
			Capabilities  map[string]bool    `json:"capabilities"`
		}{pluginabi.SchemaVersion, metadata(), map[string]bool{"quota_provider": true}}), nil
	case pluginabi.MethodQuotaIdentifier:
		return okEnvelope(map[string]string{"identifier": ID}), nil
	case pluginabi.MethodQuotaDescribe:
		m.mu.RLock()
		providers := append([]string(nil), m.cfg.SupportedProviders...)
		displayName := m.cfg.DisplayName
		m.mu.RUnlock()
		return okEnvelope(pluginapi.QuotaDescribeResponse{DisplayName: displayName, SupportedProviders: providers, SupportsReset: false}), nil
	case pluginabi.MethodQuotaFetch:
		var req pluginapi.QuotaFetchRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return ErrEnvelope("invalid_request", "invalid quota fetch request"), nil
		}
		result, err := m.FetchQuota(req)
		if err != nil {
			return ErrEnvelope("quota_error", err.Error()), nil
		}
		return okEnvelope(result), nil
	case pluginabi.MethodQuotaReset:
		return ErrEnvelope("not_supported", "aliyun-bss-quota does not support quota reset"), nil
	case pluginabi.MethodPluginShutdown:
		m.lifeMu.Lock()
		defer m.lifeMu.Unlock()
		m.mu.Lock()
		m.cfg = config.Config{}
		m.client = nil
		m.stopped = true
		m.mu.Unlock()
		return okEnvelope(map[string]bool{"shutdown": true}), nil
	default:
		return ErrEnvelope("unsupported_method", "unsupported method"), nil
	}
}

func okEnvelope(result any) []byte {
	data, err := json.Marshal(result)
	if err != nil {
		return ErrEnvelope("plugin_error", "cannot encode plugin result")
	}
	raw, _ := json.Marshal(pluginabi.Envelope{OK: true, Result: data})
	return raw
}

func ErrEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(pluginabi.Envelope{OK: false, Error: &pluginabi.Error{Code: code, Message: message}})
	return raw
}

func metadata() pluginapi.Metadata {
	fields := []pluginapi.ConfigField{
		{Name: "quota-source", Type: "enum", EnumValues: []string{config.SourceCommand, config.SourceBSS}, Description: "Quota backend: command (default) or bss"},
		{Name: "command", Type: "string", Description: "CLI executable path or PATH name (default bailian-quota); executed without a shell"},
		{Name: "command-args", Type: "array", Description: "CLI arguments passed individually (default [--json])"},
		{Name: "command-timeout", Type: "string", Description: "Hard command timeout, a positive duration (default 90s)"},
		{Name: "command-workdir", Type: "string", Description: "Optional CLI working directory (default inherit host working directory)"},
		{Name: "display-name", Type: "string", Description: "Quota display name; defaults to 阿里云百炼额度（CLI） for command or 阿里云余额（AK/SK） for bss"},
		{Name: "access-key-id", Type: "string", Description: "Alibaba Cloud access key ID; literal overrides file and environment"},
		{Name: "access-key-secret", Type: "string", Description: "Alibaba Cloud access key secret; use environment or an external AK file"},
		{Name: "ak-file", Type: "string", Description: "External JSON file containing access_key_id, access_key_secret, region"},
		{Name: "region", Type: "string", Description: "Alibaba Cloud region (default cn-beijing)"},
		{Name: "endpoint", Type: "string", Description: "BSS RPC endpoint (default https://business.aliyuncs.com/)"},
		{Name: "allow-http", Type: "boolean", Description: "Allow HTTP only to a loopback test mock (default false)"},
		{Name: "request-timeout", Type: "string", Description: "Per-request timeout, default 20s, clamped to 1–120s"},
		{Name: "supported-providers", Type: "array", Description: "Advertised provider keys (default openai-compatibility)"},
		{Name: "include-bill", Type: "boolean", Description: "Best-effort current monthly spend (default true)"},
		{Name: "include-resource-packages", Type: "boolean", Description: "Best-effort Model Studio resource packages (default false)"},
		{Name: "debug-log-requests", Type: "boolean", Description: "Log fixed request counts/flags only; accept any provider (default true)"},
	}
	return pluginapi.Metadata{Name: ID, Version: "0.1.0", Author: "Yosef", GitHubRepository: "https://github.com/williamxhero/CPA-Qwen", ConfigFields: fields}
}
