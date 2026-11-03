package plugin

import (
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"strings"
	"testing"
)

func TestRegistrationUsesCanonicalRepositoryAndPreservesRuntimeID(t *testing.T) {
	m, _ := credentialHost(t)
	defer m.HandleCall(pluginabi.MethodPluginShutdown, nil)
	raw, err := m.HandleCall(pluginabi.MethodPluginRegister, lifecycleBody(providerYAML))
	if err != nil {
		t.Fatal(err)
	}
	var registration registrationResult
	decodeResult(t, raw, &registration)
	if registration.Metadata.GitHubRepository != "https://github.com/williamxhero/CPA-Qwen" {
		t.Fatalf("stale repository metadata: %s", registration.Metadata.GitHubRepository)
	}
	if registration.Metadata.Logo != "/v0/resource/plugins/qwen-cliproxyapi/logo.svg" {
		t.Fatal("runtime plugin ID changed")
	}
	hasBindings := false
	for _, field := range registration.Metadata.ConfigFields {
		if field.Name == "quota-resource-bindings" {
			hasBindings = true
		}
	}
	if !hasBindings {
		t.Fatal("quota resource bindings registration lost")
	}
	raw, _ = m.HandleCall(pluginabi.MethodAuthLoginStart, []byte(`{}`))
	if !decodeEnv(t, raw).OK || !strings.Contains(string(raw), "/v0/management/plugins/qwen-cliproxyapi/credentials") {
		t.Fatal("runtime credential route changed")
	}
}
