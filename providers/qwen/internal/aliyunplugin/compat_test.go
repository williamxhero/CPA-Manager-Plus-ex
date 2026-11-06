package aliyunplugin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"cpa-credential-qwen/internal/aliyunplugin"
)

func invoke(t *testing.T, m *aliyunplugin.Manager, method string, payload any) pluginabi.Envelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	response, err := m.HandleCall(method, raw)
	if err != nil {
		t.Fatal(err)
	}
	var env pluginabi.Envelope
	if err := json.Unmarshal(response, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestUnsupportedMethodNeverEchoesUntrustedMethod(t *testing.T) {
	env := invoke(t, aliyunplugin.NewManager(nil), "executor.execute/"+selectedKey, map[string]any{"api_key": selectedKey})
	if env.OK || env.Error.Code != "unsupported_method" || strings.Contains(env.Error.Message, selectedKey) {
		t.Fatalf("unsafe unsupported-method diagnostic: %#v", env.Error)
	}
}

func configure(t *testing.T, m *aliyunplugin.Manager, yaml string) pluginabi.Envelope {
	t.Helper()
	return invoke(t, m, pluginabi.MethodPluginRegister, map[string]any{"config_yaml": []byte(yaml)})
}

func TestRegistrationKeepsQuotaOnlyRuntimeIdentity(t *testing.T) {
	m := aliyunplugin.NewManager(nil)
	env := configure(t, m, "")
	if !env.OK {
		t.Fatalf("register: %#v", env.Error)
	}
	var result struct {
		SchemaVersion uint32             `json:"schema_version"`
		Metadata      pluginapi.Metadata `json:"metadata"`
		Capabilities  map[string]bool    `json:"capabilities"`
	}
	if err := json.Unmarshal(env.Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != pluginabi.SchemaVersion || result.Metadata.Name != "aliyun-bss-quota" || result.Metadata.GitHubRepository != "https://github.com/williamxhero/CPA-Qwen" || len(result.Metadata.ConfigFields) != 17 || len(result.Capabilities) != 1 || !result.Capabilities["quota_provider"] {
		t.Fatalf("registration: %#v", result)
	}
	id := invoke(t, m, pluginabi.MethodQuotaIdentifier, nil)
	if !id.OK || string(id.Result) != `{"identifier":"aliyun-bss-quota"}` {
		t.Fatalf("identifier: %s", id.Result)
	}
}
