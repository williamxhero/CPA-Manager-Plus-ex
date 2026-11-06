package plugin

import (
	"context"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"cpa-credential-qwen/internal/config"
)

func TestQuotaRejectsLegacyOrReplacedCredentialLocator(t *testing.T) {
	m := commandManager(t, "coding")
	key := "dummy-selected-key"
	seed, err := locatorSeedFixture(key, "", m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	index := locatorIndexFixture(seed)
	for _, tc := range []struct{ name, key, baseURL, seed, index string }{
		{"legacy path index", key, "", seed, "legacy-path-index"},
		{"missing seed", key, "", "", index},
		{"key replaced", "dummy-replacement-key", "", seed, index},
		{"URL replaced", key, "https://example.test/v2", seed, index},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := m.FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: tc.index, Attributes: map[string]string{"api_key": tc.key, "base_url": tc.baseURL, "auth_index_seed": tc.seed}})
			if err == nil || !strings.Contains(err.Error(), "credential identity") {
				t.Fatalf("stale quota identity accepted: %v", err)
			}
		})
	}
	result, err := m.FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: index, Attributes: map[string]string{"api_key": key, "auth_index_seed": seed}})
	if err != nil || len(result.Groups) == 0 {
		t.Fatalf("matching identity rejected: %v", err)
	}
}

func TestQuotaStorageCredentialKeepsIdentityFence(t *testing.T) {
	m := commandManager(t, "coding")
	oldSeed, err := locatorSeedFixture("synthetic-old-key", "https://old.test/v1", m.cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key, base, locator string
		attrs                    map[string]string
		wantOK                   bool
	}{
		{"current identity without key attributes", "synthetic-current-key", "https://current.test/v1", "current", nil, true},
		{"current identity with stale attributes", "synthetic-current-key", "https://current.test/v1", "current", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, true},
		{"old locator after key edit", "synthetic-current-key", "https://old.test/v1", "old", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, false},
		{"old locator after URL edit", "synthetic-old-key", "https://current.test/v1", "old", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, false},
		{"normalized URL preserves identity", "synthetic-old-key", "https://OLD.test:443/v1/", "old", map[string]string{"api_key": "synthetic-old-key", "base_url": "https://old.test/v1"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed, err := locatorSeedFixture(tc.key, tc.base, m.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if tc.locator == "old" {
				seed = oldSeed
			}
			attrs := map[string]string{"auth_index_seed": seed}
			for k, v := range tc.attrs {
				attrs[k] = v
			}
			req := pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: locatorIndexFixture(seed), Attributes: attrs, StorageJSON: mustJSON(map[string]string{"api_key": tc.key, "base_url": tc.base})}
			result, err := m.FetchQuota(context.Background(), req)
			if tc.wantOK {
				if err != nil || len(result.Groups) == 0 {
					t.Fatalf("current storage identity rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "credential identity") {
				t.Fatalf("old locator accepted after storage changed: %v", err)
			}
		})
	}
}

func TestQwenCredentialIdentityBindsKeyAndURLAcrossParseAndRefresh(t *testing.T) {
	p := authProvider{cfg: config.Config{BaseURL: "https://example.test/v1"}}
	parse := func(key, baseURL, label string, disabled bool) pluginapi.AuthData {
		t.Helper()
		raw := mustJSON(map[string]any{"type": "qwen", "api_key": key, "base_url": baseURL, "label": label, "disabled": disabled})
		resp, err := p.ParseAuth(context.Background(), pluginapi.AuthParseRequest{Provider: ProviderID, FileName: "same.json", RawJSON: raw})
		if err != nil || !resp.Handled {
			t.Fatalf("parse: %v", err)
		}
		return resp.Auth
	}
	original := parse("synthetic-key-a", "", "First", false)
	seed := original.Attributes["auth_index_seed"]
	if seed != "qwen-credential-v1:bd41b8daff81a96c0944a95226030e0218e9ee71264f9a96721439391feae654" {
		t.Fatalf("missing/nonopaque credential identity: %q", seed)
	}
	for _, same := range []pluginapi.AuthData{
		parse("synthetic-key-a", "https://EXAMPLE.test:443/v1/", "Renamed", true),
		parse("synthetic-key-a", "https://example.test/v1", "Other", false),
	} {
		if same.Attributes["auth_index_seed"] != seed {
			t.Fatal("presentation/disabled/canonical URL changed identity")
		}
	}
	for _, replaced := range []pluginapi.AuthData{
		parse("synthetic-key-b", "", "First", false),
		parse("synthetic-key-a", "https://example.test/v2", "First", false),
	} {
		if replaced.Attributes["auth_index_seed"] == seed {
			t.Fatal("key/URL replacement retained credential identity")
		}
	}
	refreshed, err := p.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: original.ID, StorageJSON: original.StorageJSON, Attributes: original.Attributes})
	if err != nil || refreshed.Auth.Attributes["auth_index_seed"] != seed {
		t.Fatalf("refresh identity drift: %v", err)
	}
	attrs := map[string]string{"api_key": "synthetic-key-b", "auth_index_seed": seed}
	replaced, err := p.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: original.ID, Attributes: attrs})
	if err != nil || replaced.Auth.Attributes["auth_index_seed"] == seed {
		t.Fatalf("refresh trusted stale identity: %v", err)
	}
	if attrs["auth_index_seed"] != seed {
		t.Fatal("refresh mutated caller attributes")
	}
	current := parse("synthetic-key-b", "https://example.test/v2", "Edited", false)
	refreshed, err = p.RefreshAuth(context.Background(), pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: original.ID, StorageJSON: current.StorageJSON, Attributes: original.Attributes})
	if err != nil || refreshed.Auth.Attributes["auth_index_seed"] != current.Attributes["auth_index_seed"] || refreshed.Auth.Attributes["api_key"] != "synthetic-key-b" || refreshed.Auth.Attributes["base_url"] != "https://example.test/v2" {
		t.Fatalf("refresh retained stale routing attributes or identity: %v", err)
	}
	if original.Attributes["auth_index_seed"] != seed || original.Attributes["api_key"] != "synthetic-key-a" {
		t.Fatal("storage-aware refresh mutated caller attributes")
	}
	m := commandManager(t, "coding")
	_, err = m.FetchQuota(context.Background(), pluginapi.QuotaFetchRequest{Provider: ProviderID, AuthIndex: locatorIndexFixture(seed), StorageJSON: refreshed.Auth.StorageJSON, Attributes: refreshed.Auth.Attributes})
	if err == nil || !strings.Contains(err.Error(), "credential identity") {
		t.Fatalf("refresh allowed quota to release the original locator: %v", err)
	}
}

// Literal protocol vectors, independent of production locator helpers.
func locatorSeedFixture(key, base string, cfg config.Config) (string, error) {
	if base == "" {
		base = cfg.BaseURL
	}
	if base == "" {
		base = config.DefaultBaseURL
	}
	switch key + "\x00" + base {
	case "dummy-selected-key\x00https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1":
		return "qwen-credential-v1:4ad38c73ffaee513a6eba37cada30e95ba4843de87687f23481d6078f44c51dc", nil
	case "synthetic-old-key\x00https://old.test/v1":
		return "qwen-credential-v1:68b334638996335360bc74db56404552941d5cf7b2dd28321eadbc45fd51fde1", nil
	case "synthetic-current-key\x00https://current.test/v1":
		return "qwen-credential-v1:16f6b906eeb39f8f6deaddd318e65f60458d52b14894f1735c3a7b31b1c5204d", nil
	case "synthetic-current-key\x00https://old.test/v1":
		return "qwen-credential-v1:c5b949dd3e8682a5dcbf0f64dea16f82d4dd73df5157542dd6d531da1c369410", nil
	case "synthetic-old-key\x00https://current.test/v1":
		return "qwen-credential-v1:fec07ff2b8a6116170a1112434113785c4b07784b926d13adce0a6c86caa5448", nil
	case "synthetic-old-key\x00https://OLD.test:443/v1/":
		return "qwen-credential-v1:68b334638996335360bc74db56404552941d5cf7b2dd28321eadbc45fd51fde1", nil
	}
	return "", fmt.Errorf("missing test locator vector")
}
func locatorIndexFixture(seed string) string {
	switch seed {
	case "qwen-credential-v1:bd41b8daff81a96c0944a95226030e0218e9ee71264f9a96721439391feae654":
		return "7ddd2a889512ab48"
	case "qwen-credential-v1:16f6b906eeb39f8f6deaddd318e65f60458d52b14894f1735c3a7b31b1c5204d":
		return "4459f096d7c17cd4"
	case "qwen-credential-v1:4ad38c73ffaee513a6eba37cada30e95ba4843de87687f23481d6078f44c51dc":
		return "71f6dae281eee436"
	case "qwen-credential-v1:68b334638996335360bc74db56404552941d5cf7b2dd28321eadbc45fd51fde1":
		return "96e94b75f0ad2677"
	case "qwen-credential-v1:c5b949dd3e8682a5dcbf0f64dea16f82d4dd73df5157542dd6d531da1c369410":
		return "d06d1374893be3a9"
	case "qwen-credential-v1:fec07ff2b8a6116170a1112434113785c4b07784b926d13adce0a6c86caa5448":
		return "cc9cf8596dd0b66f"
	}
	panic("missing test auth index vector")
}

func TestAuthDispatchRefreshUsesConfiguredLocatorWithoutMutatingInput(t *testing.T) {
	m := NewManager(nil)
	m.cfg = config.Config{BaseURL: "https://example.test/v1"}
	attrs := map[string]string{"api_key": "synthetic-key-a", "extra": "kept"}
	metadata := map[string]any{"note": "kept"}
	req := pluginapi.AuthRefreshRequest{AuthProvider: ProviderID, AuthID: "record.json", Attributes: attrs, Metadata: metadata}
	raw, err := m.HandleCall(pluginabi.MethodAuthRefresh, mustJSON(req))
	if err != nil {
		t.Fatal(err)
	}
	var resp pluginapi.AuthRefreshResponse
	decodeResult(t, raw, &resp)
	if resp.Auth.Attributes["auth_index_seed"] != "qwen-credential-v1:bd41b8daff81a96c0944a95226030e0218e9ee71264f9a96721439391feae654" || resp.Auth.Attributes["base_url"] != "https://example.test/v1" || resp.Auth.Attributes["extra"] != "kept" || resp.Auth.Metadata["note"] != "kept" || resp.Auth.ID != "record.json" {
		t.Fatal("refresh dispatch lost config, locator or unrelated fields")
	}
	if len(attrs) != 2 || attrs["auth_index_seed"] != "" {
		t.Fatal("mutated request map")
	}
}
