package aliyunplugin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"qwen-cliproxyapi/internal/aliyunplugin"
)

func describe(t *testing.T, m *aliyunplugin.Manager) pluginapi.QuotaDescribeResponse {
	t.Helper()
	env := invoke(t, m, pluginabi.MethodQuotaDescribe, nil)
	var desc pluginapi.QuotaDescribeResponse
	if !env.OK || json.Unmarshal(env.Result, &desc) != nil {
		t.Fatal("invalid describe response")
	}
	return desc
}

func TestDebugAcceptAnyProviderLogsOnlyFixedCountersAndFlags(t *testing.T) {
	var messages []string
	bridge := aliyunplugin.NewHostBridge(func(method string, payload []byte) ([]byte, error) {
		if method != pluginabi.MethodHostLog {
			t.Error("unexpected host method")
		}
		var log struct{ Level, Message string }
		if json.Unmarshal(payload, &log) != nil || log.Level != "info" {
			t.Error("invalid log")
		}
		messages = append(messages, log.Message)
		return []byte(`{"ok":true}`), nil
	})
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableAmount":"1"}}`)
	}, "", bridge)
	result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{Provider: selectedKey, AuthID: selectedKey, AuthIndex: selectedKey, Attributes: map[string]string{selectedKey: "attribute-value-secret", "api_key": selectedKey}, Metadata: map[string]any{"token": selectedKey}, StorageJSON: []byte(`{"token":"` + selectedKey + `"}`)})
	if err != nil || len(result.Summary) != 1 {
		t.Fatalf("debug default no longer accepts any provider: %v", err)
	}
	if len(messages) != 1 || messages[0] != "quota.fetch attribute_count=2 has_secret=true" {
		t.Fatalf("unexpected diagnostic: %q", messages)
	}
}

func TestOptionalBSSFailuresNeverLogUntrustedMessages(t *testing.T) {
	var messages []string
	bridge := aliyunplugin.NewHostBridge(func(_ string, payload []byte) ([]byte, error) {
		messages = append(messages, string(payload))
		return nil, nil
	})
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Action") == "QueryAccountBalance" {
			fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableAmount":"9"}}`)
			return
		}
		fmt.Fprint(w, `{"Success":false,"Code":"Denied","Message":"`+selectedKey+` signature-test-value"}`)
	}, "include-bill: true\ninclude-resource-packages: true\n", bridge)
	result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{})
	if err != nil || len(result.Summary) != 1 || result.Summary[0].Value != 9 {
		t.Fatalf("optional failure lost balance: %v", err)
	}
	if len(messages) != 3 {
		t.Fatalf("expected request + two optional failure labels, got %d", len(messages))
	}
	if strings.Contains(strings.Join(messages, "\n"), selectedKey) || strings.Contains(strings.Join(messages, "\n"), "signature-test-value") {
		t.Fatal("optional diagnostics leaked")
	}
}

func TestReconfigureIsAtomicAndCanSwitchCommandAndBSS(t *testing.T) {
	t.Setenv("ALIYUN_COMPAT_STDOUT", commandReading)
	m := commandManager(t, "emit", "supported-providers: [before]\n")
	for _, bad := range []string{"quota-source: invalid", "command-timeout: 0s", "quota-source: bss\nendpoint: invalid", "quota-source: bss\nak-file: non-existent-fixture-file"} {
		env := invoke(t, m, pluginabi.MethodPluginReconfigure, map[string]any{"config_yaml": []byte(bad)})
		if env.OK || env.Error.Code != "configuration_error" {
			t.Fatal("invalid reconfiguration accepted")
		}
		result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{})
		if err != nil || result.Subscription == nil || describe(t, m).SupportedProviders[0] != "before" {
			t.Fatal("failed reconfiguration replaced active state")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableAmount":"7"}}`)
	}))
	defer server.Close()
	bssYAML := fmt.Sprintf("quota-source: bss\nendpoint: %s\nallow-http: true\naccess-key-id: fixture-id\naccess-key-secret: fixture-secret\ninclude-bill: false\nsupported-providers: [after]\n", server.URL)
	for _, cfg := range []string{bssYAML, commandYAML(t, "emit")} {
		env := invoke(t, m, pluginabi.MethodPluginReconfigure, map[string]any{"config_yaml": []byte(cfg)})
		var reg struct {
			SchemaVersion uint32 `json:"schema_version"`
		}
		if !env.OK || json.Unmarshal(env.Result, &reg) != nil || reg.SchemaVersion != pluginabi.SchemaVersion {
			t.Fatal("reconfigure must return current registration")
		}
		result, err := m.FetchQuota(pluginapi.QuotaFetchRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if cfg == bssYAML {
			if result.Subscription != nil || len(result.Summary) != 1 || result.Summary[0].Value != 7 || describe(t, m).SupportedProviders[0] != "after" {
				t.Fatal("command to BSS switch failed")
			}
		} else if result.Subscription == nil {
			t.Fatal("BSS to command switch failed")
		}
	}
}

func TestCommandDoesNotDependOnBSSConfigAndFetchRequiresRegistration(t *testing.T) {
	m := aliyunplugin.NewManager(nil)
	if _, err := m.FetchQuota(pluginapi.QuotaFetchRequest{}); err == nil {
		t.Fatal("unregistered fetch succeeded")
	}
	if env := configure(t, m, "endpoint: invalid\nak-file: non-existent-fixture-file"); !env.OK {
		t.Fatalf("command depended on BSS config: %#v", env.Error)
	}
}

func TestShutdownDrainsInFlightAndRejectsFetchUntilSuccessfulRegistration(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	m := mockManager(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		fmt.Fprint(w, `{"Success":true,"Code":"200","Data":{"AvailableAmount":"1"}}`)
	}, "", nil)
	fetched := make(chan error, 1)
	go func() { _, err := m.FetchQuota(pluginapi.QuotaFetchRequest{}); fetched <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("fetch never started")
	}
	shutdown := make(chan error, 1)
	go func() { _, err := m.HandleCall(pluginabi.MethodPluginShutdown, nil); shutdown <- err }()
	select {
	case <-shutdown:
		close(release)
		t.Fatal("shutdown returned while fetch was in flight")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	if err := <-fetched; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-shutdown:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not drain")
	}
	if _, err := m.FetchQuota(pluginapi.QuotaFetchRequest{}); err == nil {
		t.Fatal("fetch after shutdown succeeded")
	}
	desc := describe(t, m)
	if desc.DisplayName != "" || len(desc.SupportedProviders) != 0 {
		t.Fatal("shutdown retained visible configuration")
	}
	if env := invoke(t, m, pluginabi.MethodPluginReconfigure, map[string]any{"config_yaml": []byte("quota-source: invalid")}); env.OK {
		t.Fatal("invalid restart accepted")
	}
	if _, err := m.FetchQuota(pluginapi.QuotaFetchRequest{}); err == nil {
		t.Fatal("failed reconfigure revived stopped state")
	}
	if env := invoke(t, m, pluginabi.MethodPluginShutdown, nil); !env.OK {
		t.Fatal("shutdown not idempotent")
	}
	if env := configure(t, m, ""); !env.OK || describe(t, m).DisplayName == "" {
		t.Fatal("successful registration could not restart")
	}
}

func TestMetadataListsUniqueCompatibilityConfigurationFields(t *testing.T) {
	env := configure(t, aliyunplugin.NewManager(nil), "")
	var reg struct {
		Metadata pluginapi.Metadata `json:"metadata"`
	}
	if !env.OK || json.Unmarshal(env.Result, &reg) != nil {
		t.Fatal("registration")
	}
	seen := map[string]pluginapi.ConfigField{}
	for _, field := range reg.Metadata.ConfigFields {
		if _, ok := seen[field.Name]; ok {
			t.Fatalf("duplicate field %s", field.Name)
		}
		seen[field.Name] = field
	}
	for name, kind := range map[string]pluginapi.ConfigFieldType{"quota-source": "enum", "command": "string", "command-args": "array", "command-timeout": "string", "command-workdir": "string", "display-name": "string", "access-key-id": "string", "access-key-secret": "string", "ak-file": "string"} {
		if field, ok := seen[name]; !ok || field.Type != kind || field.Description == "" {
			t.Fatalf("missing field %s", name)
		}
	}
}
